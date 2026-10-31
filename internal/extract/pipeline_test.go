//go:build darwin || windows

package extract

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/retention"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type versions struct {
	mu      sync.Mutex
	attempt string
}

func (v *versions) Current(_ store.Tx, job string) (string, uint64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.attempt, 1, true
}
func (v *versions) set(a string) { v.mu.Lock(); v.attempt = a; v.mu.Unlock() }

type memBodies struct {
	mu sync.Mutex
	m  map[string]string
}

func (b *memBodies) Put(id, t string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[id] = t
	return nil
}
func (b *memBodies) Get(id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.m[id]; ok {
		return t, nil
	}
	return "", ErrBody
}
func (b *memBodies) Delete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, id)
	return nil
}
func (b *memBodies) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.m) }

type nopLoader struct{}

func (nopLoader) Load(context.Context, kernel.ArtifactRef) (detect.Unit, error) {
	return detect.Unit{}, errors.New("not used")
}

type modelFn func(ctx context.Context, text string) ([]detect.ModelSpan, error)

func (m modelFn) Run(ctx context.Context, t string) ([]detect.ModelSpan, error) { return m(ctx, t) }

type env struct {
	t      *testing.T
	p      *Pipeline
	ws     *workspace.Workspace
	db     *store.DB
	ver    *versions
	bodies *memBodies
	att    kernel.AttemptRef
}

func setup(t *testing.T, model detect.ModelRunner) *env {
	t.Helper()
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "s.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module(), findings.Module(), coverage.Module(), retention.Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, ver: &versions{attempt: "a1"}, bodies: &memBodies{m: map[string]string{}}}
	e.ws, err = workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), e.ver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.ws.Close() })
	svc, err := detect.New(detect.Config{Loader: nopLoader{}, Model: model,
		Rules: []rules.Rule{{ID: "phone", Category: "phone", Pattern: `1[3-9][0-9]{9}`, Boundary: "digit"}}})
	if err != nil {
		t.Fatal(err)
	}
	fr, err := findings.NewRepository(func(string) ([]byte, error) { return bytes.Repeat([]byte{4}, 32), nil })
	if err != nil {
		t.Fatal(err)
	}
	e.p, err = New(Pipeline{WS: e.ws, Detector: svc, Findings: fr, Bodies: e.bodies})
	if err != nil {
		t.Fatal(err)
	}
	e.att = kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: e.ws.Generation(), JobVersion: 1}
	return e
}

func rulesOnly() snapshot.DetectFacet {
	return snapshot.DetectFacet{Digest: "d", Content: snapshot.DetectContent{RulesOnly: true, Categories: []string{"phone", "person"},
		LabelMap: map[string]string{"private_person": "person"}, AdapterFloor: 0.5, Calibrated: 0.9}}
}

func modelFacet() snapshot.DetectFacet {
	f := rulesOnly()
	f.Content.RulesOnly, f.Content.ModelID = false, "m"
	return f
}

// plugin scripts a format plugin on the other end of a pipe.
func (e *env) plugin(script func(send func(map[string]any), id any, stream string)) *host.Session {
	e.t.Helper()
	local, remote := net.Pipe()
	s, err := host.NewSession(local)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { s.Close(); remote.Close() })
	read := func() map[string]any {
		remote.SetReadDeadline(time.Now().Add(10 * time.Second))
		body, err := host.ReadFrame(remote)
		if err != nil {
			return nil
		}
		var m map[string]any
		json.Unmarshal(body, &m)
		return m
	}
	send := func(m map[string]any) {
		body, _ := json.Marshal(m)
		remote.SetWriteDeadline(time.Now().Add(10 * time.Second))
		host.WriteFrame(remote, body)
		if m["method"] == "stream.item" {
			read() // the host answers every committed item with credit; a real plugin reads it before going on
		}
	}
	go func() {
		req := read()
		if req == nil {
			return
		}
		send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{
			"protocol": "1.0", "plugin": map[string]any{"id": "tacet.docx", "version": "0.1.0"}, "roles": []string{"format"}}})
		run := read()
		if run == nil {
			return
		}
		script(send, run["id"], run["params"].(map[string]any)["stream"].(map[string]any)["id"].(string))
		for read() != nil {
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = s.Initialize(ctx, "tacet.docx", "0.1.0", e.ws.Generation()); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func item(st string, seq int, body map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": st, "seq": seq, "item": body}}
}
func end(id any, st string, last int, status string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"type": "stream.end", "stream": st, "last_seq": last, "status": status}}
}
func unit(id, text string, ordinal int) map[string]any {
	return map[string]any{"kind": "text_unit", "unit_id": id, "part": "word/document.xml", "ordinal": ordinal, "text": text, "visibility": "visible"}
}
func state(part, class, st string) map[string]any {
	m := map[string]any{"kind": "part_state", "part": part, "class": class, "state": st}
	if st == "not_inspected" || st == "failed" {
		m["reason"] = "PART_NOT_INSPECTED"
	}
	return m
}
func complete(units, parts int) map[string]any {
	return map[string]any{"kind": "extract_complete", "complete": true, "units": units, "parts": parts}
}

func (e *env) run(s *host.Session, facet snapshot.DetectFacet) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return e.p.Run(ctx, Request{Session: s, Attempt: e.att, Batch: "b1", Params: map[string]any{}, Facet: facet})
}

func query[T any](e *env, fn func(store.Tx) (T, error)) T {
	e.t.Helper()
	var out T
	if err := e.ws.Commit(context.Background(), func(tx store.Tx) (err error) { out, err = fn(tx); return err }); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) visible() []findings.Finding {
	return query(e, func(tx store.Tx) ([]findings.Finding, error) {
		return e.p.Findings.Page(tx, findings.Filter{Job: "j1"}, 0, 200)
	})
}

// script of a normal two-unit document
func normal(send func(map[string]any), id any, st string) {
	send(item(st, 1, state("[Content_Types].xml", "other", "inspected")))
	send(item(st, 2, unit("u1", "联系 13812345678 结束", 1)))
	send(item(st, 3, unit("u2", "无敏感内容", 2)))
	send(item(st, 4, state("word/document.xml", "text", "inspected")))
	send(item(st, 5, complete(2, 2)))
	send(end(id, st, 5, "completed"))
}

func TestACompleteExtractionCommitsUnitsFindingsAndCoverageAndSealsThem(t *testing.T) {
	e := setup(t, nil)
	r, err := e.run(e.plugin(normal), rulesOnly())
	if err != nil {
		t.Fatal(err)
	}
	if r.Units != 2 || r.Parts != 2 || r.Gaps != 0 || r.Candidates != 1 || r.ResultVersion != 1 {
		t.Fatalf("%+v", r)
	}
	got := e.visible()
	if len(got) != 1 || got[0].Category != "phone" || got[0].Unit != "u1" || got[0].Start != 3 || got[0].End != 14 || got[0].Template != "word/document.xml" || got[0].Group == "" {
		t.Fatalf("%+v", got)
	}
	units := query(e, func(tx store.Tx) ([]Unit, error) { return e.p.Units.Units(tx, "a1", 0, 10) })
	if len(units) != 2 || units[0].UnitID != "u1" || units[1].Seq != 2 || units[0].Chars != len([]rune("联系 13812345678 结束")) {
		t.Fatalf("%+v", units)
	}
	if body, _ := e.bodies.Get(units[0].Body); body != "联系 13812345678 结束" {
		t.Fatalf("body %q", body)
	}
	type led struct {
		parts    []coverage.Part
		complete bool
	}
	l := query(e, func(tx store.Tx) (led, error) {
		p, c, err := e.p.Coverage.Ledger(tx, "j1")
		return led{p, c}, err
	})
	if !l.complete || len(l.parts) != 2 {
		t.Fatalf("%+v", l)
	}
	if rep, err := coverage.Assess(l.parts, l.complete); err != nil || len(rep.Unresolved) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestNoUnitTextOrValueLandsInTheDatabase(t *testing.T) {
	e := setup(t, nil)
	if _, err := e.run(e.plugin(normal), rulesOnly()); err != nil {
		t.Fatal(err)
	}
	query(e, func(tx store.Tx) (int, error) {
		c, _ := store.InTransaction(tx)
		for _, table := range []string{"extract_unit", "extract_attempt", "findings_finding", "coverage_part"} {
			rows, err := c.Query("SELECT * FROM " + table)
			if err != nil {
				t.Fatal(err)
			}
			cols, _ := rows.Columns()
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				rows.Scan(ptrs...)
				for _, v := range vals {
					s := string(append([]byte(nil), []byte(toString(v))...))
					if strings.Contains(s, "13812345678") || strings.Contains(s, "无敏感内容") || strings.Contains(s, "联系") {
						t.Fatalf("%s holds text: %q", table, s)
					}
				}
			}
			rows.Close()
		}
		return 0, nil
	})
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

func TestPartGapsAreCountedAndReachTheLedger(t *testing.T) {
	e := setup(t, nil)
	r, err := e.run(e.plugin(func(send func(map[string]any), id any, st string) {
		send(item(st, 1, state("word/document.xml", "text", "failed")))
		send(item(st, 2, state("custom/blob.bin", "unknown", "not_inspected")))
		send(item(st, 3, state("word/theme.xml", "other", "inspected")))
		send(item(st, 4, complete(0, 3)))
		send(end(id, st, 4, "completed"))
	}), rulesOnly())
	if err != nil || r.Gaps != 2 || r.Units != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	parts, done, _ := func() ([]coverage.Part, bool, error) {
		var p []coverage.Part
		var c bool
		err := e.ws.Commit(context.Background(), func(tx store.Tx) (err error) { p, c, err = e.p.Coverage.Ledger(tx, "j1"); return err })
		return p, c, err
	}()
	rep, err := coverage.Assess(parts, done)
	if err != nil || len(rep.Unresolved) < 2 {
		t.Fatalf("both the unreadable text part and the unknown part must be gaps: %+v %v", rep, err)
	}
}

func TestEveryIncompleteOrViolatingEndLeavesNothingBehind(t *testing.T) {
	cases := map[string]func(send func(map[string]any), id any, st string){
		"stream ended failed": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, unit("u1", "联系 13812345678", 1)))
			send(end(id, st, 1, "failed"))
		},
		"no completion marker": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, unit("u1", "联系 13812345678", 1)))
			send(end(id, st, 2, "completed"))
		},
		"unit count declared too high": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, unit("u1", "联系 13812345678", 1)))
			send(item(st, 3, complete(2, 1)))
			send(end(id, st, 3, "completed"))
		},
		"unit count declared too low": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, unit("u1", "联系 13812345678", 1)))
			send(item(st, 3, complete(0, 1)))
			send(end(id, st, 3, "completed"))
		},
		"part count declared wrong": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, unit("u1", "联系 13812345678", 1)))
			send(item(st, 3, complete(1, 5)))
			send(end(id, st, 3, "completed"))
		},
		"no parts at all": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, unit("u1", "联系 13812345678", 1)))
			send(item(st, 2, complete(1, 1)))
			send(end(id, st, 2, "completed"))
		},
		"a part reported twice": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, state("p", "text", "failed")))
			send(item(st, 3, complete(0, 1)))
			send(end(id, st, 3, "completed"))
		},
		"item after the completion marker": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, complete(0, 1)))
			send(item(st, 3, unit("u9", "联系 13812345678", 1)))
			send(end(id, st, 3, "completed"))
		},
		"failed stream whose counts happen to match": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, complete(0, 1)))
			send(end(id, st, 2, "failed"))
		},
		"cancelled stream whose counts happen to match": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, complete(0, 1)))
			send(end(id, st, 2, "cancelled"))
		},
		"completion marker sent twice": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, state("p", "text", "inspected")))
			send(item(st, 2, complete(0, 1)))
			send(item(st, 3, complete(0, 1)))
			send(end(id, st, 3, "completed"))
		},
		"unknown item kind": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "debug", "text": "TEST_SECRET"}))
			send(end(id, st, 1, "completed"))
		},
		"unit with an unknown visibility": func(send func(map[string]any), id any, st string) {
			u := unit("u1", "x", 1)
			u["visibility"] = "secret"
			send(item(st, 1, u))
			send(end(id, st, 1, "completed"))
		},
		"empty unit text": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, unit("u1", "", 1)))
			send(end(id, st, 1, "completed"))
		},
		"unit with an extra member": func(send func(map[string]any), id any, st string) {
			u := unit("u1", "x", 1)
			u["note"] = "hidden channel"
			send(item(st, 1, u))
			send(end(id, st, 1, "completed"))
		},
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			e := setup(t, nil)
			if _, err := e.run(e.plugin(script), rulesOnly()); err == nil {
				t.Fatal("an incomplete or violating extraction was accepted")
			}
			if got := e.visible(); len(got) != 0 {
				t.Fatalf("findings of a failed run are visible: %+v", got)
			}
			if units := query(e, func(tx store.Tx) ([]Unit, error) { return e.p.Units.Units(tx, "a1", 0, 10) }); len(units) != 0 {
				t.Fatalf("units of a failed run are visible: %+v", units)
			}
			if e.bodies.count() != 0 {
				t.Fatalf("%d bodies left behind", e.bodies.count())
			}
			_, done, err := func() ([]coverage.Part, bool, error) {
				var p []coverage.Part
				var c bool
				err := e.ws.Commit(context.Background(), func(tx store.Tx) (err error) { p, c, err = e.p.Coverage.Ledger(tx, "j1"); return err })
				return p, c, err
			}()
			if done {
				t.Fatalf("a failed run left a complete ledger (%v)", err)
			}
		})
	}
}

func TestAStaleAttemptStopsTheRunBeforeAnythingIsCommitted(t *testing.T) {
	e := setup(t, nil)
	e.ver.set("another-attempt") // the job moved on: this attempt was abandoned
	if _, err := e.run(e.plugin(normal), rulesOnly()); !errors.Is(err, workspace.ErrStale) {
		t.Fatalf("got %v", err)
	}
	if e.bodies.count() != 0 || len(e.visible()) != 0 {
		t.Fatal("a stale run stored something")
	}
}

func TestADetectionFailureFailsTheWholeRunNotJustTheUnit(t *testing.T) {
	boom := errors.New("model crashed")
	e := setup(t, modelFn(func(context.Context, string) ([]detect.ModelSpan, error) { return nil, boom }))
	if _, err := e.run(e.plugin(normal), modelFacet()); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if len(e.visible()) != 0 || e.bodies.count() != 0 {
		t.Fatal("a run with failed detection left results")
	}
}

func TestModelAndRuleCandidatesBothArePersistedWithTheirSources(t *testing.T) {
	e := setup(t, modelFn(func(_ context.Context, text string) ([]detect.ModelSpan, error) {
		return []detect.ModelSpan{{Label: "private_person", Start: 0, End: 2, Score: 0.95}}, nil
	}))
	f := modelFacet()
	if _, err := e.run(e.plugin(func(send func(map[string]any), id any, st string) {
		send(item(st, 1, state("p", "text", "inspected")))
		send(item(st, 2, unit("u1", "张伟 13812345678", 1)))
		send(item(st, 3, complete(1, 1)))
		send(end(id, st, 3, "completed"))
	}), f); err != nil {
		t.Fatal(err)
	}
	sources := map[kernel.FindingSource]string{}
	for _, x := range e.visible() {
		sources[x.Source] = x.Category
	}
	if sources[kernel.SourceModel] != "person" || sources[kernel.SourceRule] != "phone" {
		t.Fatalf("%v", sources)
	}
}

func TestAnEmptyDocumentCompletesWithoutUnits(t *testing.T) {
	e := setup(t, nil)
	r, err := e.run(e.plugin(func(send func(map[string]any), id any, st string) {
		send(item(st, 1, state("word/document.xml", "text", "inspected")))
		send(item(st, 2, complete(0, 1)))
		send(end(id, st, 2, "completed"))
	}), rulesOnly())
	if err != nil || r.Units != 0 || r.ResultVersion != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRerunningAnAttemptWithANewIdStartsFromACleanLedger(t *testing.T) {
	e := setup(t, nil)
	if _, err := e.run(e.plugin(normal), rulesOnly()); err != nil {
		t.Fatal(err)
	}
	e.att.AttemptID = "a2"
	e.ver.set("a2")
	if _, err := e.run(e.plugin(func(send func(map[string]any), id any, st string) {
		send(item(st, 1, state("only", "text", "inspected")))
		send(item(st, 2, complete(0, 1)))
		send(end(id, st, 2, "completed"))
	}), rulesOnly()); err != nil {
		t.Fatal(err)
	}
	parts := query(e, func(tx store.Tx) ([]coverage.Part, error) { p, _, err := e.p.Coverage.Ledger(tx, "j1"); return p, err })
	if len(parts) != 1 || parts[0].ID != "only" {
		t.Fatalf("rows of the first attempt survived: %+v", parts)
	}
}

func TestPipelineRequiresItsParts(t *testing.T) {
	if _, err := New(Pipeline{}); !errors.Is(err, ErrPipeline) {
		t.Fatal(err)
	}
}

func TestUnitsRepositoryRejectsGapsReplaysAndSealedWrites(t *testing.T) {
	e := setup(t, nil)
	do := func(fn func(store.Tx) error) error { return e.ws.Commit(context.Background(), fn) }
	u := Unit{Attempt: "x", Seq: 1, UnitID: "u", Part: "p", Ordinal: 1, Visibility: "visible", Chars: 3, Body: "b"}
	if err := do(func(tx store.Tx) error { return e.p.Units.Add(tx, "j", u) }); err != nil {
		t.Fatal(err)
	}
	skip := u
	skip.Seq = 3
	replay := u
	for name, bad := range map[string]Unit{"skipped sequence": skip, "replayed sequence": replay, "no body": {Attempt: "x", Seq: 2, UnitID: "u", Part: "p", Ordinal: 1, Chars: 1}} {
		if err := do(func(tx store.Tx) error { return e.p.Units.Add(tx, "j", bad) }); !errors.Is(err, ErrUnits) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := do(func(tx store.Tx) error { _, err := e.p.Units.Seal(tx, "j", "x", 5); return err }); !errors.Is(err, ErrUnits) {
		t.Errorf("seal with a wrong count: %v", err)
	}
	if err := do(func(tx store.Tx) error { _, err := e.p.Units.Seal(tx, "j", "x", 1); return err }); err != nil {
		t.Fatal(err)
	}
	if err := do(func(tx store.Tx) error { e2 := u; e2.Seq = 2; return e.p.Units.Add(tx, "j", e2) }); !errors.Is(err, ErrSealed) {
		t.Errorf("a sealed attempt took more units: %v", err)
	}
	if err := do(func(tx store.Tx) error { _, err := e.p.Units.Discard(tx, "x"); return err }); !errors.Is(err, ErrSealed) {
		t.Errorf("a sealed attempt was discarded: %v", err)
	}
}

func TestEncryptedBodiesRoundTripAndNeverHoldPlaintext(t *testing.T) {
	dir := t.TempDir()
	b := FileBodies{Dir: dir, Key: func(id string) ([]byte, error) { return bytes.Repeat([]byte{7}, 32), nil }}
	text := "联系 TEST_SECRET_MARK 13812345678 😀"
	if err := b.Put("b_a1_1", text); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "b_a1_1.body"))
	if bytes.Contains(raw, []byte("TEST_SECRET_MARK")) || bytes.Contains(raw, []byte("13812345678")) {
		t.Fatal("plaintext in the body file")
	}
	if got, err := b.Get("b_a1_1"); err != nil || got != text {
		t.Fatalf("%q %v", got, err)
	}
	if err := b.Put("b_a1_1", "again"); err == nil {
		t.Fatal("a body was overwritten")
	}
	wrong := FileBodies{Dir: dir, Key: func(string) ([]byte, error) { return bytes.Repeat([]byte{8}, 32), nil }}
	if _, err := wrong.Get("b_a1_1"); err == nil {
		t.Fatal("a body opened under another key")
	}
	for _, bad := range []string{"../x", "a/b", "", "a.b", strings.Repeat("a", 200)} {
		if err := b.Put(bad, "x"); err == nil {
			t.Errorf("id %q accepted", bad)
		}
	}
	if err := b.Delete("b_a1_1"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("b_a1_1"); err != nil {
		t.Fatalf("delete is idempotent: %v", err)
	}
}

func TestABodyWrittenForAUnitWhoseCommitFailsIsRemoved(t *testing.T) {
	e := setup(t, nil)
	_, err := e.run(e.plugin(func(send func(map[string]any), id any, st string) {
		send(item(st, 1, state("p", "text", "inspected")))
		e.ver.set("moved-on") // the attempt is abandoned between two items
		send(item(st, 2, unit("u1", "联系 13812345678", 1)))
		send(end(id, st, 2, "completed"))
	}), rulesOnly())
	if !errors.Is(err, workspace.ErrStale) {
		t.Fatalf("got %v", err)
	}
	if e.bodies.count() != 0 {
		t.Fatalf("%d bodies left behind by a unit that never committed", e.bodies.count())
	}
}
