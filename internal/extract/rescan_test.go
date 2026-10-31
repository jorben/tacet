//go:build darwin || windows

package extract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/store"
)

func ctxT() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func ledger(e *env) ([]coverage.Part, bool, error) {
	var p []coverage.Part
	var done bool
	err := e.ws.Commit(context.Background(), func(tx store.Tx) (err error) { p, done, err = e.p.Coverage.Ledger(tx, "j1"); return })
	return p, done, err
}

func (e *env) rescan(script func(send func(map[string]any), id any, stream string)) (ScanResult, error) {
	e.t.Helper()
	s := e.plugin(script)
	ctx, cancel := ctxT()
	defer cancel()
	return e.p.Rescan(ctx, Request{Session: s, Attempt: e.att, Batch: "b1", Params: map[string]any{}, Facet: rulesOnly()})
}

func TestRescanFindsTheHitsOfACandidateAndWritesNothing(t *testing.T) {
	e := setup(t, nil)
	r, err := e.rescan(normal)
	if err != nil {
		t.Fatal(err)
	}
	if r.Units != 2 || len(r.Parts) != 2 || len(r.Hits) != 1 {
		t.Fatalf("%+v", r)
	}
	h := r.Hits[0]
	if h.Part != "word/document.xml" || h.Ordinal != 1 || h.Start != 3 || h.End != 14 || h.Category != "phone" || h.Chars != len([]rune("联系 13812345678 结束")) {
		t.Fatalf("%+v", h)
	}
	if got := e.visible(); len(got) != 0 {
		t.Fatalf("a rescan stored findings: %+v", got)
	}
	if n := query(e, func(tx store.Tx) ([]Unit, error) { return e.p.Units.Units(tx, "a1", 0, 10) }); len(n) != 0 {
		t.Fatal("a rescan stored units")
	}
	if e.bodies.count() != 0 {
		t.Fatal("a rescan stored bodies")
	}
	if _, done, _ := ledger(e); done {
		t.Fatal("a rescan wrote a coverage ledger")
	}
}

func TestRescanHoldsTheSameCompletenessRulesAsRun(t *testing.T) {
	for name, script := range map[string]func(send func(map[string]any), id any, st string){
		"no completion marker": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(end(id, st, 1, "completed"))
		},
		"counts differ": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, unit("u1", "abc", 1)))
			send(item(st, 2, state("p", "text", "inspected")))
			send(item(st, 3, complete(5, 1)))
			send(end(id, st, 3, "completed"))
		},
		"duplicate part": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, state("p", "text", "inspected")))
			send(item(st, 3, complete(0, 1)))
			send(end(id, st, 3, "completed"))
		},
		"parts differ": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, complete(0, 3)))
			send(end(id, st, 2, "completed"))
		},
		"empty unit": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, unit("u1", "", 1)))
			send(item(st, 2, state("p", "text", "inspected")))
			send(item(st, 3, complete(1, 1)))
			send(end(id, st, 3, "completed"))
		},
		"stream failed": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, complete(0, 1)))
			send(end(id, st, 2, "failed"))
		},
		"no parts": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, complete(0, 0)))
			send(end(id, st, 1, "completed"))
		},
		"unknown item": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "surprise"}))
		},
	} {
		e := setup(t, nil)
		if _, err := e.rescan(script); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRescanRefusesToTrimWhenTooManyHitsAppear(t *testing.T) {
	e := setup(t, nil)
	old := MaxRescanHits
	MaxRescanHits = 1
	defer func() { MaxRescanHits = old }()
	_, err := e.rescan(func(send func(map[string]any), id any, st string) {
		send(item(st, 1, unit("u1", "13812345678 和 13900000000", 1)))
		send(item(st, 2, state("p", "text", "inspected")))
		send(item(st, 3, complete(1, 1)))
		send(end(id, st, 3, "completed"))
	})
	if !errors.Is(err, ErrRescanLimit) {
		t.Fatal(err)
	}
}
