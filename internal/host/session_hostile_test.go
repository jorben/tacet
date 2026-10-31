package host

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
)

// peer is an in-process plugin that speaks raw frames, so tests can send what
// a buggy or hostile plugin would. Every blocking call has a deadline.
type peer struct {
	t    *testing.T
	conn net.Conn
}

func newPeer(t *testing.T) (*Session, *peer) {
	t.Helper()
	local, remote := net.Pipe()
	s, err := NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); remote.Close() })
	return s, &peer{t: t, conn: remote}
}

func (p *peer) read() map[string]any {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	body, err := ReadFrame(p.conn)
	if err != nil {
		p.t.Fatalf("peer read: %v", err)
	}
	var m map[string]any
	if err = json.Unmarshal(body, &m); err != nil {
		p.t.Fatal(err)
	}
	return m
}

func (p *peer) send(m map[string]any) {
	p.t.Helper()
	body, _ := json.Marshal(m)
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := WriteFrame(p.conn, body); err != nil {
		p.t.Fatalf("peer write: %v", err)
	}
}

func (p *peer) raw(b []byte) {
	p.t.Helper()
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	p.conn.Write(b)
}

func (p *peer) answerInitialize(id, version, protocol string) {
	p.t.Helper()
	req := p.read()
	if req["method"] != "initialize" {
		p.t.Fatalf("expected initialize, got %v", req["method"])
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{
		"protocol": protocol, "plugin": map[string]any{"id": id, "version": version}, "roles": []string{"format"}}})
}

func ready(t *testing.T) (*Session, *peer) {
	t.Helper()
	s, p := newPeer(t)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go func() { done <- s.Initialize(ctx, "fixture.test", "0.1.0", 5) }()
	p.answerInitialize("fixture.test", "0.1.0", "1.0")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return s, p
}

var attempt5 = kernel.AttemptRef{JobID: "j", AttemptID: "a1", OwnerGeneration: 5, JobVersion: 1}

type streamResult struct {
	out StreamOutcome
	err error
}

// startStream runs Stream in the background, consumes the request and returns it.
func startStream(t *testing.T, s *Session, p *peer, ctx context.Context, a kernel.AttemptRef, commit func(context.Context, json.RawMessage) error) (<-chan streamResult, map[string]any) {
	t.Helper()
	res := make(chan streamResult, 1)
	go func() {
		out, err := s.Stream(ctx, "fixture.stream", a, nil, commit)
		res <- streamResult{out, err}
	}()
	return res, p.read()
}

func wait(t *testing.T, res <-chan streamResult) streamResult {
	t.Helper()
	select {
	case r := <-res:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return")
		return streamResult{}
	}
}

func item(stream string, seq int, kind string, pad int) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{
		"stream": stream, "seq": seq, "item": map[string]any{"kind": kind, "pad": strings.Repeat("x", pad)}}}
}

func TestHandshakeRefusesAnotherIdentityVersionOrProtocol(t *testing.T) {
	for name, tc := range map[string][3]string{
		"other plugin id": {"evil.plugin", "0.1.0", "1.0"},
		"other version":   {"fixture.test", "9.9.9", "1.0"},
		"other protocol":  {"fixture.test", "0.1.0", "2.0"},
	} {
		t.Run(name, func(t *testing.T) {
			s, p := newPeer(t)
			done := make(chan error, 1)
			go func() { done <- s.Initialize(context.Background(), "fixture.test", "0.1.0", 5) }()
			p.answerInitialize(tc[0], tc[1], tc[2])
			select {
			case err := <-done:
				if !errors.Is(err, ErrProtocolSession) {
					t.Fatalf("got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("handshake hung")
			}
			if _, err := s.Stream(context.Background(), "fixture.stream", attempt5, nil, func(context.Context, json.RawMessage) error { return nil }); err == nil {
				t.Fatal("a stream ran on a session whose handshake failed")
			}
		})
	}
}

func TestStreamRequestCarriesTheAttemptIdentityAndBothCreditDimensions(t *testing.T) {
	s, p := ready(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, req := startStream(t, s, p, ctx, attempt5, func(context.Context, json.RawMessage) error { return nil })
	params := req["params"].(map[string]any)
	stream := params["stream"].(map[string]any)
	credit := stream["credit"].(map[string]any)
	if params["job_id"] != "j" || params["attempt_id"] != "a1" || params["owner_generation"] != float64(5) || params["job_version"] != float64(1) ||
		credit["bytes"] != float64(1<<20) || credit["items"] != float64(1) {
		t.Fatalf("request: %v", params)
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": 0, "status": "completed"}})
	if r := wait(t, res); r.err != nil || r.out.Status != "completed" {
		t.Fatalf("empty stream: %+v", r)
	}
}

func TestItemCreditIsOneUntilTheHostCommitsAndReleases(t *testing.T) {
	s, p := ready(t)
	var committed atomic.Int64
	res, req := startStream(t, s, p, context.Background(), attempt5, func(context.Context, json.RawMessage) error {
		committed.Add(1)
		return nil
	})
	p.send(item("a1", 1, "k", 10))
	credit := p.read()
	if credit["method"] != "stream.credit" || credit["params"].(map[string]any)["committed_seq"] != float64(1) {
		t.Fatalf("credit message: %v", credit)
	}
	p.send(item("a1", 2, "k", 10))
	_ = p.read()
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": 2, "status": "completed"}})
	if r := wait(t, res); r.err != nil || r.out.LastSequence != 2 || committed.Load() != 2 {
		t.Fatalf("result %+v committed=%d", r, committed.Load())
	}
}

func TestNoCreditIsGrantedWhileTheDurableCommitIsStillRunning(t *testing.T) {
	s, p := ready(t)
	entered, release := make(chan struct{}), make(chan struct{})
	res, req := startStream(t, s, p, context.Background(), attempt5, func(context.Context, json.RawMessage) error {
		close(entered)
		<-release // storage is slow: the item is received but not yet durable
		return nil
	})
	p.send(item("a1", 1, "k", 10))
	<-entered
	p.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if body, err := ReadFrame(p.conn); err == nil {
		t.Fatalf("credit granted before the commit finished: %s", body)
	}
	close(release)
	credit := p.read()
	if credit["method"] != "stream.credit" {
		t.Fatalf("expected credit after commit, got %v", credit)
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": 1, "status": "completed"}})
	if r := wait(t, res); r.err != nil || r.out.LastSequence != 1 {
		t.Fatalf("got %+v", r)
	}
}

func TestItemLargerThanTheByteCreditIsRejectedBeforeCommit(t *testing.T) {
	s, p := ready(t)
	var committed atomic.Int64
	res, _ := startStream(t, s, p, context.Background(), attempt5, func(context.Context, json.RawMessage) error {
		committed.Add(1)
		return nil
	})
	p.send(item("a1", 1, "big", 1<<20+100)) // fits a frame, exceeds the 1 MiB byte credit
	r := wait(t, res)
	if !errors.Is(r.err, ErrProtocolCredit) || committed.Load() != 0 {
		t.Fatalf("got %v committed=%d", r.err, committed.Load())
	}
}

func TestHostileStreamMessagesEndTheSessionWithoutCommitting(t *testing.T) {
	cases := map[string]struct {
		send func(p *peer, req map[string]any)
		want error
	}{
		"item of another stream (late result of an old attempt)": {func(p *peer, _ map[string]any) { p.send(item("old-attempt", 1, "k", 1)) }, ErrProtocolSession},
		"sequence jump": {func(p *peer, _ map[string]any) { p.send(item("a1", 2, "k", 1)) }, ErrProtocolSequence},
		"sequence zero": {func(p *peer, _ map[string]any) { p.send(item("a1", 0, "k", 1)) }, ErrProtocolSequence},
		"item without the required kind": {func(p *peer, _ map[string]any) {
			p.send(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": 1, "item": map[string]any{}}})
		}, ErrProtocolSchema},
		"end claims more than was received": {func(p *peer, req map[string]any) {
			p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": 3, "status": "completed"}})
		}, ErrProtocolSession},
		"end for another stream": {func(p *peer, req map[string]any) {
			p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": "zzz", "last_seq": 0, "status": "completed"}})
		}, ErrProtocolSession},
		"response id of another request": {func(p *peer, req map[string]any) {
			p.send(map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": 0, "status": "completed"}})
		}, ErrProtocolSession},
		"error response": {func(p *peer, req map[string]any) {
			p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "error": map[string]any{"code": -32000, "message": "Plugin request failed", "data": map[string]any{"code": "PLUGIN_TASK_FAILED", "retry": "none"}}})
		}, ErrProtocolSession},
		"frame longer than the protocol max": {func(p *peer, _ map[string]any) {
			var h [4]byte
			binary.BigEndian.PutUint32(h[:], MaxMessageBytes+1)
			p.raw(h[:])
		}, ErrProtocolSession},
		"truncated frame then EOF": {func(p *peer, _ map[string]any) { p.raw([]byte{0, 0, 0, 20, '{'}); p.conn.Close() }, ErrProtocolSession},
		"non JSON frame":           {func(p *peer, _ map[string]any) { p.raw([]byte{0, 0, 0, 3, 'a', 'b', 'c'}) }, ErrProtocolSession},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, p := ready(t)
			var committed atomic.Int64
			res, req := startStream(t, s, p, context.Background(), attempt5, func(context.Context, json.RawMessage) error {
				committed.Add(1)
				return nil
			})
			tc.send(p, req)
			r := wait(t, res)
			if !errors.Is(r.err, tc.want) {
				t.Fatalf("got %v, want %v", r.err, tc.want)
			}
			if committed.Load() != 0 {
				t.Fatal("a hostile message reached the durable sink")
			}
			if _, err := s.Stream(context.Background(), "fixture.stream", kernel.AttemptRef{JobID: "j", AttemptID: "a2", OwnerGeneration: 5, JobVersion: 1}, nil,
				func(context.Context, json.RawMessage) error { return nil }); err == nil {
				t.Fatal("the session stayed usable after a protocol violation")
			}
		})
	}
}

func TestACommitFailureEndsTheStreamAndGrantsNoCredit(t *testing.T) {
	s, p := ready(t)
	boom := errors.New("storage refused")
	res, _ := startStream(t, s, p, context.Background(), attempt5, func(context.Context, json.RawMessage) error { return boom })
	p.send(item("a1", 1, "k", 1))
	if r := wait(t, res); !errors.Is(r.err, boom) {
		t.Fatalf("got %v", r.err)
	}
	p.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if body, err := ReadFrame(p.conn); err == nil {
		t.Fatalf("credit sent after a failed commit: %s", body)
	}
}

func TestCancellationSendsAnIdentifiedCancelAndStopsWaiting(t *testing.T) {
	s, p := ready(t)
	ctx, cancel := context.WithCancel(context.Background())
	res, _ := startStream(t, s, p, ctx, attempt5, func(context.Context, json.RawMessage) error { return nil })
	inReceiveLoop(t, p)
	cancel()
	msg := p.read()
	params, _ := msg["params"].(map[string]any)
	if msg["method"] != "task.cancel" || params["attempt_id"] != "a1" || params["job_id"] != "j" ||
		params["owner_generation"] != float64(5) || params["job_version"] != float64(1) {
		t.Fatalf("cancel message: %v", msg)
	}
	if r := wait(t, res); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("got %v", r.err)
	}
}

func TestCancellationToAPluginThatStoppedReadingStillReturns(t *testing.T) {
	s, p := ready(t)
	ctx, cancel := context.WithCancel(context.Background())
	res, _ := startStream(t, s, p, ctx, attempt5, func(context.Context, json.RawMessage) error { return nil })
	inReceiveLoop(t, p)
	// from here on the plugin never reads again, so the cancel message cannot be delivered
	started := time.Now()
	cancel()
	r := wait(t, res)
	if !errors.Is(r.err, context.Canceled) || time.Since(started) > 2*time.Second {
		t.Fatalf("got %v after %v", r.err, time.Since(started))
	}
}

func TestDeadlineIsPassedToThePluginAndEnforcedByTheHost(t *testing.T) {
	s, p := ready(t)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	res, req := startStream(t, s, p, ctx, attempt5, func(context.Context, json.RawMessage) error { return nil })
	if d := req["params"].(map[string]any)["deadline_ms"].(float64); d <= 0 || d > 400 {
		t.Fatalf("deadline_ms %v", d)
	}
	r := wait(t, res) // the plugin never answers
	if !errors.Is(r.err, context.DeadlineExceeded) {
		t.Fatalf("got %v", r.err)
	}
}

func TestStreamRefusesAnotherGenerationBeforeSendingAnything(t *testing.T) {
	s, p := ready(t)
	stale := attempt5
	stale.OwnerGeneration = 4
	if _, err := s.Stream(context.Background(), "fixture.stream", stale, nil, func(context.Context, json.RawMessage) error { return nil }); !errors.Is(err, ErrProtocolSession) {
		t.Fatalf("got %v", err)
	}
	p.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if body, err := ReadFrame(p.conn); err == nil {
		t.Fatalf("a request for another generation reached the plugin: %s", body)
	}
}

// inReceiveLoop proves the host has finished sending the request and is waiting
// for results: one item is answered with its credit.
func inReceiveLoop(t *testing.T, p *peer) {
	t.Helper()
	p.send(item("a1", 1, "k", 1))
	if m := p.read(); m["method"] != "stream.credit" {
		t.Fatalf("expected credit, got %v", m)
	}
	// The host's send of that credit returns a moment after the plugin has read
	// it; cancelling inside that window takes the "cancelled while sending" path,
	// which closes the session without a cancel message. There is no observable
	// event for "send returned", so allow scheduling to settle.
	time.Sleep(100 * time.Millisecond)
}
