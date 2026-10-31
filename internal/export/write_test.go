package export

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/store"
)

type peer struct {
	t    *testing.T
	conn net.Conn
}

func (p *peer) read() map[string]any {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := host.ReadFrame(p.conn)
	if err != nil {
		p.t.Fatalf("peer read: %v", err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

// loose: the host may already have closed the session on a refused item.
func (p *peer) send(m map[string]any) {
	b, _ := json.Marshal(m)
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = host.WriteFrame(p.conn, b)
}

var att = kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: 5, JobVersion: 1}

func start(t *testing.T, gen uint64) (*host.Session, *peer) {
	t.Helper()
	local, remote := net.Pipe()
	s, err := host.NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); remote.Close() })
	p := &peer{t: t, conn: remote}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Initialize(ctx, "fixture.test", "0.1.0", gen) }()
	req := p.read()
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"protocol": "1.0", "plugin": map[string]any{"id": "fixture.test", "version": "0.1.0"}, "roles": []string{"format"}}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return s, p
}

func twoOpPlan(t *testing.T) plan.Plan {
	t.Helper()
	sp := func(unit string, s, e int) plan.Spec {
		return plan.Spec{Kind: plan.TextOp, Unit: unit, Start: s, End: e, Action: "mask", Category: "phone", Source: []plan.Segment{{Locator: plan.Locator("word/document.xml", 1)}}, ContainerEnd: 50}
	}
	p, err := plan.Build("in", "res", "r", []plan.Spec{sp("u1", 0, 5), sp("u2", 0, 5)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func staged() *Staged {
	e := &refs.Encryption{Version: 1, ResourceID: "x", KeyHex: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"}
	return &Staged{Input: refs.Wire{Ref: "r_in", Mode: refs.Read, Handle: 3, Version: 1, Encryption: e}, Plan: refs.Wire{Ref: "r_pl", Mode: refs.Read, Handle: 4, Version: 1, Encryption: e},
		Candidate: refs.Wire{Ref: "r_c", Mode: refs.Write, Handle: 5, Encryption: e}}
}

func opRes(op, status, reason string) map[string]any {
	m := map[string]any{"kind": "op_result", "op": op, "status": status}
	if reason != "" {
		m["reason"] = reason
	}
	return m
}
func fate(part, f string) map[string]any {
	return map[string]any{"kind": "part_fate", "part": part, "fate": f}
}
func complete(ops, parts, bytes int) map[string]any {
	return map[string]any{"kind": "write_complete", "complete": true, "operations": ops, "parts": parts, "bytes": bytes}
}

// run plays script as the plugin and returns what Write returned. After an item
// the host answers with credit unless it refused the item, so only accepted ones
// are followed by a read.
func run(t *testing.T, items []map[string]any, status string) (Result, []NotExecuted, error) {
	t.Helper()
	s, p := start(t, 5)
	w, err := NewWriter()
	if err != nil {
		t.Fatal(err)
	}
	pl := twoOpPlan(t)
	type out struct {
		r  Result
		f  []NotExecuted
		er error
	}
	done := make(chan out, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		r, f, er := w.Write(ctx, s, att, staged(), pl)
		done <- out{r, f, er}
	}()
	req := p.read()
	if req["method"] != "format.write" {
		t.Fatalf("%v", req["method"])
	}
	params := req["params"].(map[string]any)
	for _, k := range []string{"input", "plan", "candidate"} {
		if params[k].(map[string]any)["ref"] == nil {
			t.Fatalf("%s reference missing", k)
		}
	}
	ids := map[string]string{}
	for i, o := range pl.Operations {
		ids[string(rune('A'+i))] = o.ID
	}
	for i, it := range items {
		for k, v := range it {
			if s, ok := v.(string); ok && len(s) == 1 && s[0] >= 'A' && s[0] <= 'B' && k == "op" {
				it[k] = ids[s]
			}
		}
		p.send(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": i + 1, "item": it}})
		p.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		host.ReadFrame(p.conn) // credit, or EOF when the item was refused
	}
	end := map[string]any{"type": "stream.end", "stream": "a1", "last_seq": len(items), "status": status}
	if status == "failed" {
		end["error"] = map[string]any{"code": "PLUGIN_TASK_FAILED", "retry": "none"}
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": end})
	select {
	case o := <-done:
		return o.r, o.f, o.er
	case <-time.After(10 * time.Second):
		t.Fatal("Write did not return")
	}
	return Result{}, nil, nil
}

func good() []map[string]any {
	return []map[string]any{opRes("A", "executed", ""), opRes("B", "executed", ""), fate("word/document.xml", "redacted"), fate("_rels/.rels", "kept"), complete(2, 2, 900)}
}

func TestACompleteWriteAccountsForEveryOperationAndPart(t *testing.T) {
	r, f, err := run(t, good(), "completed")
	if err != nil || len(f) != 0 {
		t.Fatalf("%v %v", err, f)
	}
	if r.Executed != 2 || r.Bytes != 900 || len(r.Destinations) != 2 || r.Destinations[0].PartID != "word/document.xml" || string(r.Destinations[0].Fate) != "redacted" || string(r.Destinations[1].Fate) != "kept" {
		t.Fatalf("%+v", r)
	}
}

func TestAnOperationThePluginDidNotExecuteFailsTheExportAndIsNamed(t *testing.T) {
	items := []map[string]any{opRes("A", "executed", ""), opRes("B", "not_executed", "RANGE_INVALID"), fate("word/document.xml", "kept"), complete(2, 1, 900)}
	_, f, err := run(t, items, "completed")
	if !errors.Is(err, ErrNotExecuted) || len(f) != 1 || f[0].Reason != "RANGE_INVALID" {
		t.Fatalf("%v %v", err, f)
	}
}

func TestEveryDeviationFromTheStreamContractIsAProtocolViolation(t *testing.T) {
	op := func(n string) map[string]any { return opRes(n, "executed", "") }
	for name, c := range map[string]struct {
		items  []map[string]any
		status string
	}{
		"missing operation":               {[]map[string]any{op("A"), fate("p", "kept"), complete(2, 1, 9)}, "completed"},
		"missing operation, honest count": {[]map[string]any{op("A"), fate("p", "kept"), complete(1, 1, 9)}, "completed"},
		"unknown operation":               {[]map[string]any{op("A"), op("B"), opRes("op_ghost", "executed", ""), fate("p", "kept"), complete(3, 1, 9)}, "completed"},
		"duplicate operation":             {[]map[string]any{op("A"), op("A"), fate("p", "kept"), complete(2, 1, 9)}, "completed"},
		"duplicate part":                  {[]map[string]any{op("A"), op("B"), fate("p", "kept"), fate("p", "removed"), complete(2, 1, 9)}, "completed"},
		"no completion marker":            {[]map[string]any{op("A"), op("B"), fate("p", "kept")}, "completed"},
		"operation count differs":         {[]map[string]any{op("A"), op("B"), fate("p", "kept"), complete(5, 1, 9)}, "completed"},
		"counts differ":                   {[]map[string]any{op("A"), op("B"), fate("p", "kept"), complete(2, 5, 9)}, "completed"},
		"item after completion":           {[]map[string]any{op("A"), op("B"), fate("p", "kept"), complete(2, 1, 9), fate("q", "kept")}, "completed"},
		"stream failed":                   {good(), "failed"},
		"stream cancelled":                {good(), "cancelled"},
		"executed with a reason":          {[]map[string]any{opRes("A", "executed", "OOPS"), op("B"), fate("p", "kept"), complete(2, 1, 9)}, "completed"},
		"converted without a type":        {[]map[string]any{op("A"), op("B"), fate("p", "converted"), complete(2, 1, 9)}, "completed"},
		"type on a kept part":             {[]map[string]any{op("A"), op("B"), {"kind": "part_fate", "part": "p", "fate": "kept", "expected_type": "x/y"}, complete(2, 1, 9)}, "completed"},
		"schema violation":                {[]map[string]any{{"kind": "op_result", "op": "A", "status": "executed", "path": "/tmp/x"}}, "completed"},
	} {
		if _, _, err := run(t, c.items, c.status); !errors.Is(err, ErrProtocol) && !errors.Is(err, host.ErrProtocolSession) && !errors.Is(err, host.ErrProtocolSchema) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestWriteRefusesAnUnwiredCall(t *testing.T) {
	w, _ := NewWriter()
	if _, _, err := w.Write(context.Background(), nil, att, staged(), plan.Plan{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	s, _ := start(t, 5)
	if _, _, err := w.Write(context.Background(), s, att, nil, plan.Plan{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// runPlugin plays a plugin for Service.Export: it appends payload to the candidate
// handle encrypted under the issued key, then streams the script.
func (r *rig) runPlugin(st *Staged, p plan.Plan, payload string, script func(ids []string) []map[string]any) (Candidate, []NotExecuted, error) {
	r.t.Helper()
	s, pe := start(r.t, r.att.OwnerGeneration)
	type out struct {
		c Candidate
		f []NotExecuted
		e error
	}
	done := make(chan out, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		c, f, e := r.svc.Export(ctx, s, r.att, st, p)
		done <- out{c, f, e}
	}()
	req := pe.read()
	if payload != "" {
		key, _ := hex.DecodeString(st.Candidate.Encryption.KeyHex)
		w, err := store.NewResourceWriter(st.Files[2], key, st.Candidate.Encryption.ResourceID, 1<<20)
		if err != nil {
			r.t.Fatal(err)
		}
		w.Write([]byte(payload))
		w.Close()
	}
	ids := make([]string, len(p.Operations))
	for i, o := range p.Operations {
		ids[i] = o.ID
	}
	items := script(ids)
	for i, it := range items {
		pe.send(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": r.att.AttemptID, "seq": i + 1, "item": it}})
		pe.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		host.ReadFrame(pe.conn)
	}
	pe.send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"type": "stream.end", "stream": r.att.AttemptID, "last_seq": len(items), "status": "completed"}})
	select {
	case o := <-done:
		return o.c, o.f, o.e
	case <-time.After(10 * time.Second):
		r.t.Fatal("Export did not return")
	}
	return Candidate{}, nil, nil
}
