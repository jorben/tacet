package host

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const requestBody = `{"id":3,"jsonrpc":"2.0","method":"format.unlock","params":{"job_id":"j"}}`

func TestSplicedSecretsDecodeToExactlyTheOriginalBytes(t *testing.T) {
	for name, password := range map[string]string{
		"plain": "hunter2", "quote and backslash": `a"b\c`, "control": "a\x00b\x1fc\nd\te", "cjk": "密码 パスワード",
		"emoji and combining": "🔐e\u0301", "json lookalike": `","job_id":"x`, "line separators": "a\u2028b\u2029c",
	} {
		frame, err := spliceSecrets([]byte(requestBody), []Secret{{Field: "password", Value: []byte(password)}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var message struct {
			Params map[string]any `json:"params"`
		}
		if err = json.Unmarshal(frame, &message); err != nil {
			t.Fatalf("%s: not JSON: %v: %s", name, err, frame)
		}
		if message.Params["password"] != password || message.Params["job_id"] != "j" || len(message.Params) != 2 {
			t.Fatalf("%s: params %v", name, message.Params)
		}
	}
}

func TestSecretsNeverReallocateTheFrameEvenForTheWorstCaseEscaping(t *testing.T) {
	worst := bytes.Repeat([]byte{0x01}, maxSecretBytes) // every byte becomes six
	frame, err := spliceSecrets([]byte(requestBody), []Secret{{Field: "password", Value: worst}, {Field: "other", Value: worst}})
	if err != nil {
		t.Fatal(err)
	}
	if cap(frame) != len(frame) {
		t.Fatalf("capacity %d for %d bytes: the buffer was sized wrongly or grew, leaving an uncleared copy", cap(frame), len(frame))
	}
	if !json.Valid(frame) {
		t.Fatal("invalid JSON")
	}
}

func TestSecretsAreRefusedWhenMalformed(t *testing.T) {
	ok := Secret{Field: "password", Value: []byte("x")}
	for name, c := range map[string]struct {
		body    string
		secrets []Secret
	}{
		"empty value":        {requestBody, []Secret{{Field: "password"}}},
		"too long":           {requestBody, []Secret{{Field: "password", Value: bytes.Repeat([]byte("a"), maxSecretBytes+1)}}},
		"invalid utf-8":      {requestBody, []Secret{{Field: "password", Value: []byte{0xff, 0xfe}}}},
		"field with quote":   {requestBody, []Secret{{Field: `pass"word`, Value: []byte("x")}}},
		"field uppercase":    {requestBody, []Secret{{Field: "Password", Value: []byte("x")}}},
		"field empty":        {requestBody, []Secret{{Field: "", Value: []byte("x")}}},
		"body not an object": {`{"params":1}`, []Secret{ok}},
		"body too short":     {`}`, []Secret{ok}},
		"second invalid":     {requestBody, []Secret{ok, {Field: "second"}}},
	} {
		if frame, err := spliceSecrets([]byte(c.body), c.secrets); err == nil {
			t.Errorf("%s: accepted %s", name, frame)
		}
	}
}

// recording keeps every slice handed to Write (not a copy), so a test can see
// whether the host cleared its buffer once the call finished.
type recording struct {
	net.Conn
	mu     sync.Mutex
	writes [][]byte
}

func (r *recording) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.writes = append(r.writes, b)
	r.mu.Unlock()
	return r.Conn.Write(b)
}

func TestStreamSecretDeliversThePasswordAndClearsEveryHostBuffer(t *testing.T) {
	local, remote := net.Pipe()
	rec := &recording{Conn: local}
	s, err := NewSession(rec)
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{t: t, conn: remote}
	t.Cleanup(func() { s.Close(); remote.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	init := make(chan error, 1)
	go func() { init <- s.Initialize(ctx, "fixture.test", "0.1.0", 5) }()
	p.answerInitialize("fixture.test", "0.1.0", "1.0")
	if err = <-init; err != nil {
		t.Fatal(err)
	}

	password := []byte("pa\"ss wörd")
	res := make(chan streamResult, 1)
	go func() {
		out, err := s.StreamSecret(ctx, "format.unlock", attempt5, map[string]any{"input": "x"}, []Secret{{Field: "password", Value: password}},
			func(context.Context, json.RawMessage) error { return nil })
		res <- streamResult{out, err}
	}()
	req := p.read()
	params := req["params"].(map[string]any)
	if params["password"] != "pa\"ss wörd" || params["input"] != "x" || params["job_id"] != "j" {
		t.Fatalf("the plugin did not receive a complete request: %v", params)
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": 0, "status": "completed"}})
	if r := wait(t, res); r.err != nil || r.out.Status != "completed" {
		t.Fatalf("%+v", r)
	}
	// Cleared by the host after the write; the caller's own slice is the caller's to clear.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	found := false
	for _, w := range rec.writes {
		if bytes.Contains(w, []byte("wörd")) {
			t.Fatal("a host buffer that carried the password was not cleared")
		}
		if bytes.Equal(w, make([]byte, len(w))) && len(w) > 40 {
			found = true // the frame with the secret, now all zero
		}
	}
	if !found {
		t.Fatal("did not observe the secret-bearing frame at all")
	}
	if string(password) != "pa\"ss wörd" {
		t.Fatal("the host modified the caller's slice")
	}
}

func TestStreamSecretRefusesIncompleteOrAmbiguousRequestsBeforeSendingAnything(t *testing.T) {
	s, p := ready(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commit := func(context.Context, json.RawMessage) error { return nil }
	for name, call := range map[string]func() error{
		"no secrets": func() error {
			_, err := s.StreamSecret(ctx, "format.unlock", attempt5, nil, nil, commit)
			return err
		},
		"secret shadows a parameter": func() error {
			_, err := s.StreamSecret(ctx, "format.unlock", attempt5, map[string]any{"password": "visible"}, []Secret{{Field: "password", Value: []byte("x")}}, commit)
			return err
		},
		"malformed secret": func() error {
			_, err := s.StreamSecret(ctx, "format.unlock", attempt5, nil, []Secret{{Field: "password", Value: nil}}, commit)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// nothing reached the peer; the next real request is the first frame it sees
	go s.Stream(ctx, "fixture.stream", attempt5, nil, commit)
	if req := p.read(); !strings.HasPrefix(req["method"].(string), "fixture.") {
		t.Fatalf("an earlier refused call sent %v", req["method"])
	}
}
