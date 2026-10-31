package validate

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jorben/tacet/internal/plan"
	"net"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

var att = kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: 5, JobVersion: 1}

func wire(ref string, mode refs.Mode, h int) refs.Wire {
	return refs.Wire{Ref: ref, Mode: mode, Handle: h, Version: 1, Encryption: &refs.Encryption{Version: 1, ResourceID: ref,
		KeyHex: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"}}
}

func playPlugin(t *testing.T, items []map[string]any, status string, ops []string) (Report, error) {
	t.Helper()
	local, remote := net.Pipe()
	s, err := host.NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); remote.Close() })
	read := func() map[string]any {
		remote.SetReadDeadline(time.Now().Add(5 * time.Second))
		b, err := host.ReadFrame(remote)
		if err != nil {
			return nil
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		remote.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = host.WriteFrame(remote, b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	init := make(chan error, 1)
	go func() { init <- s.Initialize(ctx, "fixture.test", "0.1.0", 5) }()
	req := read()
	send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"protocol": "1.0", "plugin": map[string]any{"id": "fixture.test", "version": "0.1.0"}, "roles": []string{"format"}}})
	if err := <-init; err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner()
	if err != nil {
		t.Fatal(err)
	}
	type out struct {
		rep Report
		err error
	}
	done := make(chan out, 1)
	go func() {
		rep, err := r.Run(ctx, s, att, wire("r_in", refs.Read, 3), wire("r_pl", refs.Read, 4), wire("r_c", refs.Read, 5), ops, plan.Look{})
		done <- out{rep, err}
	}()
	call := read()
	if call["method"] != "format.validate" {
		t.Fatalf("%v", call["method"])
	}
	for i, it := range items {
		send(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": i + 1, "item": it}})
		remote.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		host.ReadFrame(remote)
	}
	end := map[string]any{"type": "stream.end", "stream": "a1", "last_seq": len(items), "status": status}
	if status == "failed" {
		end["error"] = map[string]any{"code": "PLUGIN_TASK_FAILED", "retry": "none"}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": call["id"], "result": end})
	select {
	case o := <-done:
		return o.rep, o.err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	return Report{}, nil
}

func inPart(p string) map[string]any {
	return map[string]any{"kind": "input_part", "part": p, "content_type": "application/xml"}
}
func outPart(p, src string) map[string]any {
	m := map[string]any{"kind": "output_part", "part": p, "content_type": "application/xml", "references": []string{}}
	if src != "" {
		m["source"] = src
	}
	return m
}
func chk(op string, ex, res bool) map[string]any {
	return map[string]any{"kind": "op_check", "op": op, "executed": ex, "residual": res, "unit": "u"}
}
func vcomplete(in, out, ops int, reopened bool) map[string]any {
	return map[string]any{"kind": "validate_complete", "complete": true, "input_parts": in, "output_parts": out, "operations": ops, "reopened": reopened}
}

func TestAWellFormedValidationBecomesAReportOfFacts(t *testing.T) {
	rep, err := playPlugin(t, []map[string]any{inPart("a"), inPart("b"), outPart("a", "a"), outPart("c", ""), chk("op1", true, false), chk("op2", false, true),
		{"kind": "unverified", "scope": "z", "reason": "PART_NOT_INSPECTED"}, {"kind": "unverified", "scope": "a", "reason": "PART_NOT_INSPECTED"}, vcomplete(2, 2, 2, true)}, "completed", []string{"op1", "op2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.InputParts) != 2 || len(rep.OutputParts) != 2 || rep.OutputParts[0].SourceID != "a" || rep.OutputParts[1].SourceID != "" || !rep.Reopened {
		t.Fatalf("%+v", rep)
	}
	if !rep.Checks["op1"].Executed || rep.Checks["op2"].Executed || !rep.Checks["op2"].Residual || rep.Checks["op1"].Unit != "u" {
		t.Fatalf("%+v", rep.Checks)
	}
	if len(rep.Unverified) != 2 || rep.Unverified[0].Scope != "a" {
		t.Fatalf("unverified items must be sorted: %+v", rep.Unverified)
	}
}

func TestACandidateThatWasNotReopenedClaimsNothingElse(t *testing.T) {
	rep, err := playPlugin(t, []map[string]any{inPart("a"), vcomplete(1, 0, 0, false)}, "completed", []string{"op1"})
	if err != nil || rep.Reopened || len(rep.Checks) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	// ...and may not claim checks or parts while saying so
	for name, items := range map[string][]map[string]any{
		"checks":       {inPart("a"), chk("op1", true, false), vcomplete(1, 0, 1, false)},
		"output parts": {inPart("a"), outPart("a", "a"), vcomplete(1, 1, 0, false)},
	} {
		if _, err := playPlugin(t, items, "completed", []string{"op1"}); !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEveryDeviationFromTheValidationStreamContractIsRefused(t *testing.T) {
	ops := []string{"op1", "op2"}
	both := []map[string]any{chk("op1", true, false), chk("op2", true, false)}
	cat := func(parts ...[]map[string]any) []map[string]any {
		var out []map[string]any
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	one := func(m map[string]any) []map[string]any { return []map[string]any{m} }
	for name, c := range map[string]struct {
		items  []map[string]any
		status string
	}{
		"missing check":           {cat(one(inPart("a")), one(chk("op1", true, false)), one(vcomplete(1, 0, 1, true))), "completed"},
		"unknown operation":       {cat(one(inPart("a")), both, one(chk("ghost", true, false)), one(vcomplete(1, 0, 3, true))), "completed"},
		"duplicate check":         {cat(one(inPart("a")), both, one(chk("op1", true, false)), one(vcomplete(1, 0, 3, true))), "completed"},
		"duplicate input part":    {cat(one(inPart("a")), one(inPart("a")), both, one(vcomplete(2, 0, 2, true))), "completed"},
		"duplicate output part":   {cat(one(inPart("a")), one(outPart("a", "a")), one(outPart("a", "a")), both, one(vcomplete(1, 2, 2, true))), "completed"},
		"no completion":           {cat(one(inPart("a")), both), "completed"},
		"input count differs":     {cat(one(inPart("a")), both, one(vcomplete(7, 0, 2, true))), "completed"},
		"output count differs":    {cat(one(inPart("a")), both, one(vcomplete(1, 7, 2, true))), "completed"},
		"operation count differs": {cat(one(inPart("a")), both, one(vcomplete(1, 0, 9, true))), "completed"},
		"item after completion":   {cat(one(inPart("a")), both, one(vcomplete(1, 0, 2, true)), one(inPart("b"))), "completed"},
		"stream failed":           {cat(one(inPart("a")), both, one(vcomplete(1, 0, 2, true))), "failed"},
		"stream cancelled":        {cat(one(inPart("a")), both, one(vcomplete(1, 0, 2, true))), "cancelled"},
		"schema violation":        {one(map[string]any{"kind": "op_check", "op": "op1", "executed": true, "residual": false, "path": "/tmp/x"}), "completed"},
	} {
		if _, err := playPlugin(t, c.items, c.status, ops); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRunRefusesAnUnwiredCall(t *testing.T) {
	r, _ := NewRunner()
	if _, err := r.Run(context.Background(), nil, att, wire("a", refs.Read, 3), wire("b", refs.Read, 4), wire("c", refs.Read, 5), nil, plan.Look{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
