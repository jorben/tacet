package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

type fakePlugin struct {
	t    *testing.T
	conn net.Conn
}

func (p *fakePlugin) read() map[string]any {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	body, err := host.ReadFrame(p.conn)
	if err != nil {
		p.t.Fatalf("plugin read: %v", err)
	}
	var m map[string]any
	json.Unmarshal(body, &m)
	return m
}

func (p *fakePlugin) send(m map[string]any) {
	p.t.Helper()
	body, _ := json.Marshal(m)
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := host.WriteFrame(p.conn, body); err != nil {
		p.t.Fatalf("plugin write: %v", err)
	}
}

// sendLoose is send for a plugin that may be talking to a host which has already
// closed the session on a refused item; whether the write lands is not the point.
func (p *fakePlugin) sendLoose(m map[string]any) {
	body, _ := json.Marshal(m)
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = host.WriteFrame(p.conn, body)
}

func startUnlocker(t *testing.T) (*PluginUnlocker, *fakePlugin, *int) {
	local, remote := net.Pipe()
	s, err := host.NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); remote.Close() })
	p := &fakePlugin{t: t, conn: remote}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	init := make(chan error, 1)
	go func() { init <- s.Initialize(ctx, "fixture.test", "0.1.0", 5) }()
	req := p.read()
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"protocol": "1.0", "plugin": map[string]any{"id": "fixture.test", "version": "0.1.0"}, "roles": []string{"format"}}})
	if err = <-init; err != nil {
		t.Fatal(err)
	}
	sealed := new(int)
	key := &refs.Encryption{Version: 1, ResourceID: "x", KeyHex: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"}
	return &PluginUnlocker{Session: s, Attempt: kernel.AttemptRef{JobID: "j", AttemptID: "a1", OwnerGeneration: 5, JobVersion: 1},
		Input:  refs.Wire{Ref: "r_in", Mode: refs.Read, Handle: 3, Version: 1, Encryption: key},
		Output: refs.Wire{Ref: "r_out", Mode: refs.Write, Handle: 4, Encryption: key},
		Seal:   func() (string, error) { *sealed++; return "res_copy", nil }}, p, sealed
}

func end(status string, last int, code string) map[string]any {
	r := map[string]any{"type": "stream.end", "stream": "a1", "last_seq": last, "status": status}
	if code != "" {
		r["error"] = map[string]any{"code": code, "retry": "none"}
	}
	return r
}

func TestPluginUnlockerAcceptsOnlyOneUnlockDoneAndSealsOnlyThen(t *testing.T) {
	good := map[string]any{"kind": "unlock_done", "bytes": 10}
	for name, c := range map[string]struct {
		items  []map[string]any
		result map[string]any
		want   error
		sealed int
	}{
		"success":                   {[]map[string]any{good}, end("completed", 1, ""), nil, 1},
		"completed without a copy":  {nil, end("completed", 0, ""), ErrUnlock, 0},
		"wrong item kind":           {[]map[string]any{{"kind": "other", "bytes": 10}}, end("completed", 1, ""), ErrUnlock, 0},
		"empty copy":                {[]map[string]any{{"kind": "unlock_done", "bytes": 0}}, end("completed", 1, ""), ErrUnlock, 0},
		"two copies":                {[]map[string]any{good, good}, end("completed", 2, ""), ErrUnlock, 0},
		"wrong password":            {nil, end("failed", 0, "PASSWORD_INCORRECT"), ErrPasswordWrong, 0},
		"corrupt input":             {nil, end("failed", 0, "INPUT_CORRUPT"), ErrUnlock, 0},
		"cancelled":                 {nil, end("cancelled", 0, ""), ErrUnlock, 0},
		"failed with a copy first":  {[]map[string]any{good}, end("failed", 1, "PLUGIN_TASK_FAILED"), ErrUnlock, 0},
		"wrong password after item": {[]map[string]any{good}, end("failed", 1, "PASSWORD_INCORRECT"), ErrPasswordWrong, 0},
	} {
		u, p, sealed := startUnlocker(t)
		done := make(chan error, 1)
		go func() { _, err := Unlock(context.Background(), u, "r_in", []byte("pw")); done <- err }()
		req := p.read()
		params := req["params"].(map[string]any)
		if params["password"] != "pw" || params["input"].(map[string]any)["ref"] != "r_in" || params["output"].(map[string]any)["mode"] != "write" {
			t.Fatalf("%s: request %v", name, params)
		}
		for i, item := range c.items {
			p.sendLoose(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": i + 1, "item": item}})
			// only an accepted item earns credit; a refused one ends the session
			if i == 0 && item["kind"] == "unlock_done" && item["bytes"].(int) > 0 {
				if got := p.read(); got["method"] != "stream.credit" {
					t.Fatalf("%s: expected credit, got %v", name, got["method"])
				}
			}
		}
		p.sendLoose(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": c.result})
		select {
		case err := <-done:
			if !errors.Is(err, c.want) && !(c.want == nil && err == nil) {
				t.Errorf("%s: %v want %v", name, err, c.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Unlock did not return", name)
		}
		if *sealed != c.sealed {
			t.Errorf("%s: sealed %d times", name, *sealed)
		}
	}
}

func TestPluginUnlockerRefusesAnotherInputBeforeSendingAnything(t *testing.T) {
	u, p, _ := startUnlocker(t)
	pw := []byte("pw")
	if _, err := Unlock(context.Background(), u, "r_other", pw); !errors.Is(err, ErrUnlock) {
		t.Fatalf("%v", err)
	}
	p.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if body, err := host.ReadFrame(p.conn); err == nil {
		t.Fatalf("a request for another input reached the plugin: %s", body)
	}
	if pw[0] != 0 {
		t.Fatal("the password of a refused request must still be cleared")
	}
}

func TestPluginUnlockerRefusesWrongReferenceModesBeforeSending(t *testing.T) {
	for name, edit := range map[string]func(u *PluginUnlocker){
		"input is writable":  func(u *PluginUnlocker) { u.Input.Mode = refs.Write },
		"output is readable": func(u *PluginUnlocker) { u.Output.Mode = refs.Read },
		"no session":         func(u *PluginUnlocker) { u.Session = nil },
		"no seal":            func(u *PluginUnlocker) { u.Seal = nil },
	} {
		u, p, _ := startUnlocker(t)
		edit(u)
		if _, err := Unlock(context.Background(), u, "r_in", []byte("pw")); !errors.Is(err, ErrUnlock) {
			t.Errorf("%s: %v", name, err)
		}
		p.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if body, err := host.ReadFrame(p.conn); err == nil {
			t.Errorf("%s: a request was sent: %s", name, body)
		}
	}
}

func TestPluginUnlockerReportsASealThatFailsOrNamesNothingAsAFailure(t *testing.T) {
	for name, seal := range map[string]func() (string, error){
		"seal fails":         func() (string, error) { return "", errors.New("disk full") },
		"seal names nothing": func() (string, error) { return "", nil },
	} {
		u, p, _ := startUnlocker(t)
		u.Seal = seal
		done := make(chan error, 1)
		go func() { _, err := Unlock(context.Background(), u, "r_in", []byte("pw")); done <- err }()
		req := p.read()
		p.send(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": 1, "item": map[string]any{"kind": "unlock_done", "bytes": 5}}})
		p.read() // credit
		p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": end("completed", 1, "")})
		if err := <-done; !errors.Is(err, ErrUnlock) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func hostRead(p *fakePlugin) ([]byte, error) { return host.ReadFrame(p.conn) }
