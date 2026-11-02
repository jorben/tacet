//go:build darwin

package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/testenv"
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

func (v *versions) set(a string) { v.mu.Lock(); v.attempt = a; v.mu.Unlock() }

type nopLoader struct{}

func (nopLoader) Load(context.Context, kernel.ArtifactRef) (detect.Unit, error) {
	return detect.Unit{}, fmt.Errorf("not used")
}

func taskKey(string) ([]byte, error) { return bytes.Repeat([]byte{9}, 32), nil }

func facet() snapshot.DetectFacet {
	return snapshot.DetectFacet{Digest: "d", Content: snapshot.DetectContent{RulesOnly: true, Categories: []string{"phone"}, AdapterFloor: 0.5, Calibrated: 0.9}}
}

const (
	nsCT   = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsRel  = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsW    = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	mainCT = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)

func docx(t *testing.T, body string) []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, f := range [][2]string{
		{"[Content_Types].xml", `<Types xmlns="` + nsCT + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="` + mainCT + `"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="` + nsRel + `"><Relationship Id="m" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<w:document xmlns:w="` + nsW + `"><w:body>` + body + `</w:body></w:document>`},
	} {
		w, err := z.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f[1]))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type rig struct {
	t      *testing.T
	ws     *workspace.Workspace
	refs   *refs.Manager
	ver    *versions
	python string
	pipe   *extract.Pipeline
	fr     *findings.Repository
	ex     exceptions.Repository
	exp    *export.Service
	n      int
	// what the input run stored
	units            map[string]extract.Unit // by unit id
	byPos            map[[2]any]string       // (part, ordinal) -> unit id
	found            []findings.Finding
	stale            bool
	module, pluginID string
	noText           bool
	noUnits          bool
	kept             bool
}

func newRig(t *testing.T, input []byte) *rig {
	return newRigFor(t, input, "tacet_format_docx", "tacet.docx")
}

func newRigFor(t *testing.T, input []byte, module, pluginID string) *rig {
	t.Helper()
	python := testenv.Python(t)
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "v.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(extract.Module(), findings.Module(), coverage.Module(), exceptions.Module(), export.Module(), publish.Module(), Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{t: t, module: module, pluginID: pluginID, ver: &versions{attempt: "x1"}, python: python, units: map[string]extract.Unit{}, byPos: map[[2]any]string{}}
	r.ws, err = workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), r.ver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.ws.Close() })
	res := filepath.Join(dir, "res")
	os.MkdirAll(res, 0o700)
	r.refs, err = refs.New(res, r.ws.Admit, taskKey)
	if err != nil {
		t.Fatal(err)
	}
	// the input, encrypted under the key refs will derive for it
	tk, _ := taskKey("j1")
	key, _ := keys.ResourceKey(tk, "in1")
	path := filepath.Join(dir, "input-src")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.NewResourceWriter(f, key, "in1", 1<<24)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(input)
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err = r.refs.Adopt("j1", "in1", path); err != nil {
		t.Fatal(err)
	}
	det, err := detect.New(detect.Config{Loader: nopLoader{}, Rules: []rules.Rule{{ID: "phone", Category: "phone", Pattern: `1[3-9][0-9]{9}`, Boundary: "digit"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.fr, err = findings.NewRepository(func(string) ([]byte, error) { return bytes.Repeat([]byte{4}, 32), nil })
	if err != nil {
		t.Fatal(err)
	}
	bodies := extract.FileBodies{Dir: filepath.Join(dir, "bodies"), Key: func(string) ([]byte, error) { return bytes.Repeat([]byte{5}, 32), nil }}
	r.pipe, err = extract.New(extract.Pipeline{WS: r.ws, Detector: det, Findings: r.fr, Bodies: bodies})
	if err != nil {
		t.Fatal(err)
	}
	wr, err := export.NewWriter()
	if err != nil {
		t.Fatal(err)
	}
	r.exp = &export.Service{WS: r.ws, Refs: r.refs, W: wr}
	return r
}

func (r *rig) att(id string) kernel.AttemptRef {
	r.ver.set(id)
	return kernel.AttemptRef{JobID: "j1", AttemptID: id, OwnerGeneration: r.ws.Generation(), JobVersion: 1}
}

func (r *rig) launch(files []*os.File) *host.Worker {
	r.t.Helper()
	r.n++
	w, err := host.Start(host.Spec{Path: r.python, Args: []string{"-I", "-m", r.module}, Dir: r.t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: files, Generation: fmt.Sprintf("gen_validate_e2e_test_%d", r.n)})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { w.Terminate(time.Second) })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = w.Session.Initialize(ctx, r.pluginID, "0.1.0", r.ws.Generation()); err != nil {
		r.t.Fatal(err)
	}
	return w
}

func bg() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 90*time.Second)
}

// extract runs the real extraction and detection on the input.
func (r *rig) extractInput() {
	r.t.Helper()
	a := r.att("x1")
	g, err := r.refs.IssueRead(a, "in1")
	if err != nil {
		r.t.Fatal(err)
	}
	files, wires, err := refs.Handoff(g)
	if err != nil {
		r.t.Fatal(err)
	}
	w := r.launch(files)
	ctx, cancel := bg()
	defer cancel()
	if _, err = r.pipe.Run(ctx, extract.Request{Session: w.Session, Attempt: a, Batch: "b1", Facet: facet(), Params: map[string]any{"input": wires[0]}}); err != nil {
		r.t.Fatal(err)
	}
	if err = r.ws.Commit(ctx, func(tx store.Tx) error {
		us, err := r.pipe.Units.Units(tx, "x1", 0, 100)
		for _, u := range us {
			r.units[u.UnitID] = u
			r.byPos[[2]any{u.Part, u.Ordinal}] = u.UnitID
		}
		if err != nil {
			return err
		}
		r.found, err = r.fr.Page(tx, findings.Filter{Job: "j1"}, 0, 100)
		return err
	}); err != nil {
		r.t.Fatal(err)
	}
	sort.Slice(r.found, func(i, j int) bool {
		if r.found[i].Unit != r.found[j].Unit {
			return r.found[i].Unit < r.found[j].Unit
		}
		return r.found[i].Start < r.found[j].Start
	})
}

func (r *rig) planFor(which []int, action string) plan.Plan {
	r.t.Helper()
	var specs []plan.Spec
	for _, i := range which {
		f := r.found[i]
		u := r.units[f.Unit]
		specs = append(specs, plan.Spec{Kind: plan.TextOp, Unit: f.Unit, Start: f.Start, End: f.End, Action: action, Category: f.Category,
			Source: []plan.Segment{{Locator: plan.Locator(u.Part, u.Ordinal)}}, ContainerEnd: u.Chars})
	}
	p, err := plan.Build("in", "res", "r", specs)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *rig) exportPlan(p plan.Plan, repl func(plan.Operation) (string, error)) export.Candidate {
	r.t.Helper()
	a := r.att("e1")
	st, err := export.Stage(r.refs, a, "in1", p, repl, 1<<24)
	if err != nil {
		r.t.Fatal(err)
	}
	w := r.launch(st.Files)
	ctx, cancel := bg()
	defer cancel()
	c, failed, err := r.exp.Export(ctx, w.Session, a, st, p)
	if err != nil {
		r.t.Fatalf("%v %v", err, failed)
	}
	return c
}

func (r *rig) validate(c export.Candidate, p plan.Plan, repl func(plan.Operation) (string, error), known bool) (Result, error) {
	return r.validateWith(c, p, repl, known, c.PlanResource, false)
}

func (r *rig) validateWith(c export.Candidate, p plan.Plan, repl func(plan.Operation) (string, error), known bool, planRes string, widenUsed bool) (Result, error) {
	r.t.Helper()
	a := r.att("v1")
	st, err := Stage(r.refs, a, "in1", planRes, c.Resource)
	if err != nil {
		r.t.Fatal(err)
	}
	w := r.launch(st.Files)
	runner, err := NewRunner()
	if err != nil {
		r.t.Fatal(err)
	}
	svc := &Service{Commit: r.ws.Commit, Admit: func(tx store.Tx, a kernel.AttemptRef) error {
		if r.stale {
			return fmt.Errorf("stale")
		}
		return r.ws.AdmitTx(tx, a)
	}, Runner: runner, Scanner: r.pipe, Findings: r.fr, Refs: r.refs,
		UnitOf: func(_ store.Tx, part string, ordinal int) (string, uint64, bool) {
			id, ok := r.byPos[[2]any{part, ordinal}]
			return id, 1, ok && !r.noUnits
		},
		Value: func(_ store.Tx, unit string, s, e int) []byte {
			body, err := r.pipe.Bodies.Get(r.units[unit].Body)
			if err != nil {
				return nil
			}
			return []byte(string([]rune(body)[s:e]))
		},
		Persist: func(tx store.Tx, job string, ds []Draft, validation string) error {
			var out []exceptions.Draft
			for _, d := range ds {
				out = append(out, exceptions.Draft{Type: exceptions.Type(d.Type), ScopeKey: d.ScopeKey, Reason: d.Reason})
			}
			_, err := r.ex.SyncPostExport(tx, job, out, validation)
			return err
		}}
	var dests []coverage.Destination
	if err = r.ws.Commit(context.Background(), func(tx store.Tx) (err error) { _, dests, _, err = (export.Repository{}).Current(tx, "j1"); return }); err != nil {
		r.t.Fatal(err)
	}
	edits, err := EditsOf(p, repl)
	if err != nil {
		r.t.Fatal(err)
	}
	var kn []Known
	if known {
		for _, f := range r.found {
			u := r.units[f.Unit]
			kn = append(kn, Known{Part: u.Part, Ordinal: u.Ordinal, Start: f.Start, End: f.End})
		}
	}
	ctx, cancel := bg()
	defer cancel()
	return svc.Run(ctx, Request{Session: w.Session, Attempt: a, Batch: "b1", Stage: st, Plan: p, CandidateDigest: c.Digest, Destinations: dests, Edits: edits, Known: kn, WidenUsed: widenUsed, KeptGaps: r.kept, ExpectNoText: r.noText,
		Widen: func(rs []plan.Residual) error { _, err := plan.Widen(p, "in", "res", "r", rs); return err }, Facet: facet()})
}

func fixed(s string) func(plan.Operation) (string, error) {
	return func(plan.Operation) (string, error) { return s, nil }
}

var _ = rand.Read

const body = `<w:p><w:r><w:t>甲 13812345678 乙 13900000000 丙</w:t></w:r></w:p><w:p><w:r><w:t>无敏感内容</w:t></w:r></w:p>`

func (r *rig) post() []findings.Finding {
	r.t.Helper()
	var out []findings.Finding
	r.ws.Commit(context.Background(), func(tx store.Tx) (err error) {
		out, err = r.fr.Page(tx, findings.Filter{Job: "j1", Sources: nil}, 0, 100)
		return
	})
	var post []findings.Finding
	for _, f := range out {
		if f.Stage == findings.StagePostExport {
			post = append(post, f)
		}
	}
	return post
}

func TestRealDOCXFullyRedactedCandidatePassesValidation(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	if len(r.found) != 2 {
		t.Fatalf("input findings: %+v", r.found)
	}
	p := r.planFor([]int{0, 1}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	res, err := r.validate(c, p, fixed("[手机号]"), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != Passed || res.Status != StatusPassed || res.NewFindings != 0 || res.Generated != 0 || res.Residuals != 0 || res.Mismatches != 0 {
		t.Fatalf("%+v", res)
	}
	var got Result
	var ok bool
	r.ws.Commit(context.Background(), func(tx store.Tx) (err error) { got, ok, err = Latest(tx, "j1"); return })
	if !ok || got.Conclusion != Passed || got.Candidate != c.Digest || got.PlanVersion != p.Version {
		t.Fatalf("%+v %v", got, ok)
	}
	if len(r.post()) != 0 {
		t.Fatal("a clean candidate produced findings")
	}
}

func TestRealDOCXNewHitInRetainedTextIsLocatedAtItsInputPosition(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	// only the first number is planned; the second stays in the output and is not known to the input run
	p := r.planFor([]int{0}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	res, err := r.validate(c, p, fixed("[手机号]"), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != NewFindings || res.NewFindings != 1 || res.Generated != 0 || res.ResultVersion != 2 {
		t.Fatalf("%+v", res)
	}
	post := r.post()
	second := r.found[1]
	if len(post) == 1 && post[0].Template != "word/document.xml" {
		t.Fatalf("the part must be recorded with the finding: %q", post[0].Template)
	}
	if len(post) != 1 || post[0].Unit != second.Unit || post[0].Start != second.Start || post[0].End != second.End || post[0].Category != "phone" {
		t.Fatalf("the new finding must sit where the number is in the INPUT (shifted back past the replacement): %+v want %+v", post, second)
	}
	// the same value groups with the input's own finding (same keyed hash)
	if post[0].Group == "" || post[0].Group != second.Group {
		t.Fatalf("group %q vs %q", post[0].Group, second.Group)
	}
	// the next validation of the same candidate does not add it again once it is known
	res2, err := r.validate(c, p, fixed("[手机号]"), true)
	if err != nil || res2.Conclusion != Passed {
		t.Fatalf("%+v %v", res2, err)
	}
}

func TestRealDOCXPlaceholderThatTriggersARuleBecomesAReviewItemNotAFinding(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	p := r.planFor([]int{0, 1}, "placeholder")
	repl := fixed("13800000000") // a generated value that the phone rule matches
	c := r.exportPlan(p, repl)
	res, err := r.validate(c, p, repl, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != NewFindings || res.Generated != 2 || res.NewFindings != 0 {
		t.Fatalf("%+v", res)
	}
	if len(r.post()) != 0 {
		t.Fatal("a hit in generated text must not become a finding at an input position")
	}
	var open []exceptions.Row
	r.ws.Commit(context.Background(), func(tx store.Tx) (err error) {
		open, err = r.ex.Items(tx, exceptions.Filter{Jobs: []string{"j1"}}, "", 50)
		return
	})
	if len(open) != 2 || open[0].Type != exceptions.PolicyReview || !open[0].PostExport || open[0].Reason != "generated_content_hit" {
		t.Fatalf("%+v", open)
	}
	// a candidate made with a harmless placeholder resolves them
	p2 := r.planFor([]int{0, 1}, "placeholder")
	c2 := r.exportPlan(p2, fixed("[手机号]"))
	res2, err := r.validate(c2, p2, fixed("[手机号]"), true)
	if err != nil || res2.Conclusion != Passed {
		t.Fatalf("%+v %v", res2, err)
	}
	r.ws.Commit(context.Background(), func(tx store.Tx) (err error) {
		open, err = r.ex.Items(tx, exceptions.Filter{Jobs: []string{"j1"}}, "", 50)
		return
	})
	if len(open) != 0 {
		t.Fatalf("a clean validation must resolve the earlier items: %+v", open)
	}
}

func (r *rig) resultOf() (Result, bool) {
	var got Result
	var ok bool
	r.ws.Commit(context.Background(), func(tx store.Tx) (err error) { got, ok, err = Latest(tx, "j1"); return })
	return got, ok
}

func (r *rig) openExceptions() int {
	var n int
	r.ws.Commit(context.Background(), func(tx store.Tx) error {
		items, err := r.ex.Items(tx, exceptions.Filter{Jobs: []string{"j1"}}, "", 50)
		n = len(items)
		return err
	})
	return n
}

// A candidate that does not do what the plan the validator is given says leaves the
// original content behind: that is a residual, widened once and failed after that.
func TestRealDOCXCandidateThatDoesNotMatchThePlanLeavesAResidual(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	partial := r.planFor([]int{0}, "placeholder")
	c := r.exportPlan(partial, fixed("[手机号]"))
	full := r.planFor([]int{0, 1}, "placeholder")
	// the plan the validator reads is the full one; the candidate was written from the partial one
	st2, err := export.Stage(r.refs, r.att("e2"), "in1", full, fixed("[手机号]"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.validateWith(c, full, fixed("[手机号]"), true, st2.PlanResource, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != Widened || res.Residuals != 1 || res.Status != StatusFailed || len(res.ResidualUnits) != 1 || res.ResidualUnits[0].Unit != r.found[1].Unit {
		t.Fatalf("%+v", res)
	}
	if len(r.post()) != 0 || r.openExceptions() != 0 {
		t.Fatal("a failed validation must not touch findings or exceptions")
	}
	res, err = r.validateWith(c, full, fixed("[手机号]"), true, st2.PlanResource, true)
	if err != nil || res.Conclusion != FailedResidual {
		t.Fatalf("after the one widening the file fails: %+v %v", res, err)
	}
	if got, ok := r.resultOf(); !ok || got.Conclusion != FailedResidual {
		t.Fatalf("%+v %v", got, ok)
	}
}

func TestAValidationThatCannotFinishRecordsNothingAndSaysNotRun(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	p := r.planFor([]int{0, 1}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	// the host expects checks for operations the plugin's plan does not contain
	bigger := r.planFor([]int{0}, "placeholder")
	bigger.Operations = append(bigger.Operations, plan.Operation{ID: "op_ghost", Spec: bigger.Operations[0].Spec})
	res, err := r.validate(c, bigger, fixed("[手机号]"), true)
	if err == nil || res.Conclusion != NotRun || res.Status != StatusNotRun {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := r.resultOf(); ok {
		t.Fatal("an unfinished validation was recorded")
	}
}

func TestAValidationOfASupersededAttemptRecordsNothing(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	p := r.planFor([]int{0}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	r.stale = true
	res, err := r.validate(c, p, fixed("[手机号]"), false)
	if err == nil || res.Conclusion != NotRun {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := r.resultOf(); ok || len(r.post()) != 0 {
		t.Fatal("a stale attempt wrote results or findings")
	}
}

func TestANewHitWhoseUnitTheInputRunDidNotStoreBecomesAnUnlocatedReviewItem(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	r.noUnits = true
	p := r.planFor([]int{0}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	res, err := r.validate(c, p, fixed("[手机号]"), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != NewFindings || res.NewFindings != 0 || res.Unlocated != 1 || len(r.post()) != 0 || r.openExceptions() != 1 {
		t.Fatalf("%+v open=%d", res, r.openExceptions())
	}
	var items []exceptions.Row
	r.ws.Commit(context.Background(), func(tx store.Tx) (err error) {
		items, err = r.ex.Items(tx, exceptions.Filter{Jobs: []string{"j1"}}, "", 50)
		return
	})
	if items[0].Type != exceptions.UnreliableLocation || items[0].Reason != "new_hit_unit_unknown" {
		t.Fatalf("%+v", items)
	}
}

func TestAPassWithAKeptGapIsPartial(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	r.kept = true
	p := r.planFor([]int{0, 1}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	res, err := r.validate(c, p, fixed("[手机号]"), true)
	if err != nil || res.Conclusion != Passed || res.Status != StatusPartial || !res.KeptGaps {
		t.Fatalf("%+v %v", res, err)
	}
	if got, ok := r.resultOf(); !ok || got.Status != StatusPartial || !got.KeptGaps {
		t.Fatalf("%+v", got)
	}
}

func TestACandidateThatCannotBeReopenedFailsAsCorruptInsteadOfBeingRescanned(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	p := r.planFor([]int{0, 1}, "placeholder")
	c := r.exportPlan(p, fixed("[手机号]"))
	g, err := r.refs.IssueWrite(r.att("g1"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	w, err := g.HostWriter(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("PK\x03\x04 this is not a package"))
	w.Close()
	if _, err = g.Freeze(); err != nil {
		t.Fatal(err)
	}
	bad := c
	bad.Resource = g.ResourceID()
	res, err := r.validate(bad, p, fixed("[手机号]"), true)
	if err != nil || res.Conclusion != FailedCorrupt || res.Status != StatusFailed {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.post()) != 0 || r.openExceptions() != 0 {
		t.Fatal("a corrupt candidate must not produce findings")
	}
}
