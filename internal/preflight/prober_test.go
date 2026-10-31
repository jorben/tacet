package preflight

import (
	"context"
	"errors"
	"testing"
	"time"
)

func probeItem(format, enc string) map[string]any {
	return map[string]any{"kind": "probe_result", "format": format, "encryption": enc, "pages": 3, "images": 2, "embedded": 1, "bytes": 99}
}

func TestPluginProberTakesExactlyOneProbeResult(t *testing.T) {
	for name, c := range map[string]struct {
		items  []map[string]any
		result map[string]any
		want   *Probe
	}{
		"ok":               {[]map[string]any{probeItem("docx", "password_required")}, end("completed", 1, ""), &Probe{Format: "docx", Encryption: PasswordRequired, Pages: 3, Images: 2, Embedded: 1, Bytes: 99}},
		"no result":        {nil, end("completed", 0, ""), nil},
		"wrong kind":       {[]map[string]any{{"kind": "other", "format": "docx", "encryption": "none", "pages": 0, "images": 0, "embedded": 0, "bytes": 0}}, end("completed", 1, ""), nil},
		"two results":      {[]map[string]any{probeItem("docx", "none"), probeItem("docx", "none")}, end("completed", 2, ""), nil},
		"failed":           {nil, end("failed", 0, "INPUT_CORRUPT"), nil},
		"failed with item": {[]map[string]any{probeItem("docx", "none")}, end("failed", 1, "PLUGIN_TASK_FAILED"), nil},
		"cancelled":        {nil, end("cancelled", 0, ""), nil},
	} {
		u, p, _ := startUnlocker(t)
		prober := &PluginProber{Session: u.Session, Attempt: u.Attempt, Input: u.Input}
		type res struct {
			p   Probe
			err error
		}
		done := make(chan res, 1)
		go func() { pr, err := prober.Probe(context.Background()); done <- res{pr, err} }()
		req := p.read()
		if req["method"] != "format.probe" || req["params"].(map[string]any)["input"].(map[string]any)["ref"] != "r_in" {
			t.Fatalf("%s: request %v", name, req)
		}
		for i, item := range c.items {
			p.sendLoose(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": i + 1, "item": item}})
			if i == 0 && item["kind"] == "probe_result" {
				p.read() // credit
			}
		}
		p.sendLoose(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": c.result})
		select {
		case r := <-done:
			if c.want == nil && !errors.Is(r.err, ErrProbe) || c.want != nil && (r.err != nil || r.p != *c.want) {
				t.Errorf("%s: %+v %v", name, r.p, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Probe did not return", name)
		}
	}
}

func TestPluginProberRefusesAWritableInputBeforeSending(t *testing.T) {
	u, p, _ := startUnlocker(t)
	in := u.Output // a write reference
	prober := &PluginProber{Session: u.Session, Attempt: u.Attempt, Input: in}
	if _, err := prober.Probe(context.Background()); !errors.Is(err, ErrProbe) {
		t.Fatal(err)
	}
	if (&PluginProber{Input: u.Input}).Input.Mode == "" {
		t.Fatal("setup")
	}
	if _, err := (&PluginProber{Input: u.Input}).Probe(context.Background()); !errors.Is(err, ErrProbe) {
		t.Fatalf("no session: %v", err)
	}
	p.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if body, err := hostRead(p); err == nil {
		t.Fatalf("a request was sent: %s", body)
	}
}
