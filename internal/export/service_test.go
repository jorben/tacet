package export

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type versions struct {
	mu      sync.Mutex
	attempt string
}

func (v *versions) Current(store.Tx, string) (string, uint64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.attempt, 1, true
}

type rig struct {
	t    *testing.T
	ws   *workspace.Workspace
	refs *refs.Manager
	svc  *Service
	dir  string
	att  kernel.AttemptRef
	ver  *versions
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "e.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
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
	ver := &versions{attempt: "a1"}
	ws, err := workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), ver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	res := filepath.Join(dir, "res")
	os.MkdirAll(res, 0o700)
	m, err := refs.New(res, ws.Admit, func(job string) ([]byte, error) { return bytes.Repeat([]byte{9}, 32), nil })
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWriter()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, ws: ws, refs: m, dir: res, ver: ver, att: kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: ws.Generation(), JobVersion: 1}}
	r.svc = &Service{WS: ws, Refs: m, W: w}
	return r
}

func (r *rig) input() string {
	r.t.Helper()
	src := filepath.Join(r.dir, "input-src")
	os.WriteFile(src, []byte("opaque"), 0o600)
	if err := r.refs.Adopt("j1", "in1", src); err != nil {
		r.t.Fatal(err)
	}
	return "in1"
}

func replacement(plan.Operation) (string, error) { return "[x]", nil }

func (r *rig) current() (Candidate, []coverage.Destination, bool) {
	r.t.Helper()
	var c Candidate
	var d []coverage.Destination
	var ok bool
	if err := r.ws.Commit(context.Background(), func(tx store.Tx) (err error) { c, d, ok, err = r.svc.Repo.Current(tx, "j1"); return }); err != nil {
		r.t.Fatal(err)
	}
	return c, d, ok
}

func TestStageWritesAFrozenPlanAndHandsOutThreeGrants(t *testing.T) {
	r := newRig(t)
	p := twoOpPlan(t)
	st, err := Stage(r.refs, r.att, r.input(), p, replacement, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 3 || st.Input.Mode != refs.Read || st.Plan.Mode != refs.Read || st.Candidate.Mode != refs.Write || st.Plan.Digest == "" || st.Input.Handle != 3 || st.Candidate.Handle != 5 {
		t.Fatalf("%+v", st)
	}
	// the plan resource decrypts to exactly the encoded plan
	key, _ := hex.DecodeString(st.Plan.Encryption.KeyHex)
	info, _ := st.Files[1].Stat()
	rd, err := store.OpenResource(st.Files[1], info.Size(), key, st.Plan.Encryption.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, rd.Size())
	rd.ReadAt(got, 0)
	want, _ := plan.Encode(p, replacement)
	if !bytes.Equal(got, want) || !strings.Contains(string(got), `"replacement":"[x]"`) {
		t.Fatalf("%q", got)
	}
	// the plugin's read handles cannot be written
	if _, err = st.Files[0].Write([]byte("x")); err == nil {
		t.Fatal("the input handle is writable")
	}
	if _, err = st.Files[1].Write([]byte("x")); err == nil {
		t.Fatal("the plan handle is writable")
	}
}

func TestStageRefusesWhatItCannotHonour(t *testing.T) {
	r := newRig(t)
	p := twoOpPlan(t)
	in := r.input()
	region, _ := plan.Build("in", "res", "r", []plan.Spec{{Kind: plan.RegionOp, Unit: "page:1", Action: "fill", Rect: &plan.Rect{L: 0, T: 0, R: 5, B: 5}}})
	for name, call := range map[string]func() error{
		"no manager":    func() error { _, err := Stage(nil, r.att, in, p, replacement, 10); return err },
		"no input":      func() error { _, err := Stage(r.refs, r.att, "", p, replacement, 10); return err },
		"no limit":      func() error { _, err := Stage(r.refs, r.att, in, p, replacement, 0); return err },
		"no replacer":   func() error { _, err := Stage(r.refs, r.att, in, p, nil, 10); return err },
		"region op":     func() error { _, err := Stage(r.refs, r.att, in, region, replacement, 10); return err },
		"unknown input": func() error { _, err := Stage(r.refs, r.att, "nope", p, replacement, 10); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAFullExportFreezesTheCandidateAndRegistersItWithItsDestinations(t *testing.T) {
	r := newRig(t)
	p := twoOpPlan(t)
	st, err := Stage(r.refs, r.att, r.input(), p, replacement, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	c, failed, err := r.runPlugin(st, p, "candidate bytes", func(ids []string) []map[string]any {
		return []map[string]any{opRes(ids[0], "executed", ""), opRes(ids[1], "executed", ""), fate("word/document.xml", "redacted"), fate("_rels/.rels", "kept"), complete(2, 2, 15)}
	})
	if err != nil || len(failed) != 0 {
		t.Fatalf("%v %v", err, failed)
	}
	if c.PlanVersion != p.Version || c.Operations != 2 || !strings.HasPrefix(c.Digest, "sha256:") || c.Bytes != 15 {
		t.Fatalf("%+v", c)
	}
	got, dests, ok := r.current()
	if !ok || got != c || got.PlanResource == "" || len(dests) != 2 || dests[0].PartID != "_rels/.rels" || string(dests[0].Fate) != "kept" || string(dests[1].Fate) != "redacted" {
		t.Fatalf("%+v %+v %v", got, dests, ok)
	}
	// the candidate is now a frozen, readable resource of the job
	if d, err := r.refs.Digest(c.Resource); err != nil || d != c.Digest {
		t.Fatalf("%q %v", d, err)
	}
	// a newer candidate replaces the registration and names the old resource
	var prev string
	if err = r.ws.Commit(context.Background(), func(tx store.Tx) (err error) {
		prev, err = r.svc.Repo.Register(tx, Candidate{Job: "j1", PlanVersion: "v2", PlanResource: "pl2", Resource: "res_new", Digest: "sha256:x", Bytes: 1, Operations: 0}, []coverage.Destination{{PartID: "p", Fate: coverage.FateKept}})
		return
	}); err != nil || prev != c.Resource {
		t.Fatalf("%q %v", prev, err)
	}
	if _, d, _ := r.current(); len(d) != 1 {
		t.Fatalf("the old destinations survived: %+v", d)
	}
}

func TestAnExportThatFailsRegistersNothingAndReleasesTheCandidate(t *testing.T) {
	r := newRig(t)
	p := twoOpPlan(t)
	n := 0
	for name, c := range map[string]struct {
		payload string
		script  func(ids []string) []map[string]any
		want    error
	}{
		"operation not executed": {"x", func(ids []string) []map[string]any {
			return []map[string]any{opRes(ids[0], "executed", ""), opRes(ids[1], "not_executed", "RANGE_INVALID"), fate("p", "kept"), complete(2, 1, 1)}
		}, ErrNotExecuted},
		"plugin claims bytes but wrote nothing": {"", func(ids []string) []map[string]any {
			return []map[string]any{opRes(ids[0], "executed", ""), opRes(ids[1], "executed", ""), fate("p", "kept"), complete(2, 1, 9)}
		}, nil},
		"protocol violation": {"x", func(ids []string) []map[string]any {
			return []map[string]any{opRes(ids[0], "executed", ""), fate("p", "kept"), complete(2, 1, 1)}
		}, ErrProtocol},
	} {
		// a failed stage revokes the attempt's grants for good, so every case is a new attempt
		n++
		r.att.AttemptID = "case" + string(rune('a'+n))
		r.ver.mu.Lock()
		r.ver.attempt = r.att.AttemptID
		r.ver.mu.Unlock()
		st, err := Stage(r.refs, r.att, r.input2(name), p, replacement, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = r.runPlugin(st, p, c.payload, c.script)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: %v", name, err)
		}
		if _, werr := r.refs.IssueWrite(r.att, 10); werr == nil {
			t.Errorf("%s: the failed stage left the attempt able to get new grants", name)
		}
		if _, _, ok := r.current(); ok {
			t.Errorf("%s: a candidate was registered", name)
		}
	}
}

func (r *rig) input2(name string) string {
	id := "in-" + strings.ReplaceAll(name, " ", "_")
	src := filepath.Join(r.dir, id+"-src")
	os.WriteFile(src, []byte("opaque"), 0o600)
	if err := r.refs.Adopt("j1", id, src); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func TestAnExportOfASupersededAttemptRegistersNothing(t *testing.T) {
	r := newRig(t)
	p := twoOpPlan(t)
	st, err := Stage(r.refs, r.att, r.input(), p, replacement, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.runPlugin(st, p, "candidate bytes", func(ids []string) []map[string]any {
		r.ver.mu.Lock()
		r.ver.attempt = "a2" // another attempt took over the job meanwhile
		r.ver.mu.Unlock()
		return []map[string]any{opRes(ids[0], "executed", ""), opRes(ids[1], "executed", ""), fate("p", "kept"), complete(2, 1, 15)}
	})
	if err == nil {
		t.Fatal("a stale attempt exported")
	}
	if _, _, ok := r.current(); ok {
		t.Fatal("a stale attempt registered a candidate")
	}
}

func TestRepositoryRefusesIncompleteRegistrationsAndPurgesOnlyItsJob(t *testing.T) {
	r := newRig(t)
	ok := Candidate{Job: "j1", PlanVersion: "v", PlanResource: "pl", Resource: "r", Digest: "sha256:x", Bytes: 1}
	d := []coverage.Destination{{PartID: "p", Fate: coverage.FateKept}}
	for name, c := range map[string]struct {
		c Candidate
		d []coverage.Destination
	}{"no job": {Candidate{PlanVersion: "v", PlanResource: "pl", Resource: "r", Digest: "x", Bytes: 1}, d}, "no plan": {Candidate{Job: "j1", PlanResource: "pl", Resource: "r", Digest: "x", Bytes: 1}, d},
		"no resource": {Candidate{Job: "j1", PlanVersion: "v", PlanResource: "pl", Digest: "x", Bytes: 1}, d}, "no digest": {Candidate{Job: "j1", PlanVersion: "v", PlanResource: "pl", Resource: "r", Bytes: 1}, d},
		"empty": {Candidate{Job: "j1", PlanVersion: "v", PlanResource: "pl", Resource: "r", Digest: "x"}, d}, "no plan resource": {Candidate{Job: "j1", PlanVersion: "v", Resource: "r", Digest: "x", Bytes: 1}, d}, "no destinations": {ok, nil}} {
		if err := r.ws.Commit(context.Background(), func(tx store.Tx) error { _, err := r.svc.Repo.Register(tx, c.c, c.d); return err }); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	other := ok
	other.Job = "j2"
	r.ws.Commit(context.Background(), func(tx store.Tx) error {
		r.svc.Repo.Register(tx, ok, d)
		_, err := r.svc.Repo.Register(tx, other, d)
		return err
	})
	if err := r.ws.Commit(context.Background(), func(tx store.Tx) error { return r.svc.Repo.Purge(tx, "j1") }); err != nil {
		t.Fatal(err)
	}
	if _, _, found := r.current(); found {
		t.Fatal("j1 survived")
	}
	r.ws.Commit(context.Background(), func(tx store.Tx) error {
		_, dd, f, _ := r.svc.Repo.Current(tx, "j2")
		if !f || len(dd) != 1 {
			t.Error("j2 lost its candidate")
		}
		return nil
	})
	if err := r.ws.Commit(context.Background(), func(tx store.Tx) error { return r.svc.Repo.Purge(tx, "") }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestAnOversizedPlanIsRefusedBeforeAnythingIsIssued(t *testing.T) {
	r := newRig(t)
	old := MaxPlanBytes
	MaxPlanBytes = 10
	defer func() { MaxPlanBytes = old }()
	if _, err := Stage(r.refs, r.att, r.input(), twoOpPlan(t), replacement, 1<<20); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if g, err := r.refs.IssueWrite(r.att, 10); err != nil || g == nil {
		t.Fatalf("a refused stage must not have revoked or consumed anything: %v", err)
	}
}
