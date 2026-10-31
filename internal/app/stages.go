package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/policy"
	"github.com/jorben/tacet/internal/preflight"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/validate"
	"github.com/jorben/tacet/internal/verdict"
	"github.com/jorben/tacet/internal/workspace"
)

type runner struct {
	kind kernel.StageKind
	e    *Engine
	run  func(ctx sched.StageContext) sched.StageOutcome
}

func (r runner) Kind() kernel.StageKind { return r.kind }
func (r runner) Claim(sched.JobView) kernel.ResourceClaim {
	return kernel.ResourceClaim{MemoryBytes: 256 << 20, CPUSlots: 1, WorkerKind: "format", ScratchBytes: 64 << 20, CandidateBytes: 64 << 20, QueueBytes: 1 << 20}
}
func (r runner) Run(ctx sched.StageContext) sched.StageOutcome { return r.run(ctx) }

func (e *Engine) runners() []sched.StageRunner {
	return []sched.StageRunner{
		runner{kernel.StagePreflight, e, e.preflight}, runner{kernel.StageExtractDetect, e, e.extractDetect},
		runner{kernel.StageResolve, e, e.resolve}, runner{kernel.StageExport, e, e.export},
		runner{kernel.StageValidate, e, e.validate}, runner{kernel.StagePublish, e, e.publish},
	}
}

func failed(code string) sched.StageOutcome {
	return sched.StageOutcome{Kind: sched.Failed, Err: &kernel.ErrorCode{Code: code}}
}

func done(detail string) sched.StageOutcome {
	return sched.StageOutcome{Kind: sched.Completed, Detail: detail}
}

// classify maps an error to an outcome: resource conditions retry, the rest fail.
func classify(err error, fallback string) sched.StageOutcome {
	var busy *netgate.BusyError
	switch {
	case errors.As(err, &busy):
		return sched.StageOutcome{Kind: sched.Retry, Err: &kernel.ErrorCode{Code: netgate.CodeActivityBusy}}
	case errors.Is(err, context.Canceled):
		return sched.StageOutcome{Kind: sched.Cancelled}
	case errors.Is(err, publish.ErrNameConflict):
		return sched.StageOutcome{Kind: sched.Blocked, Detail: "target", Err: &kernel.ErrorCode{Code: "PUBLISH_NAME_CONFLICT"}}
	}
	code := fallback
	if err != nil && len(err.Error()) <= 64 && strings.ToUpper(err.Error()) == err.Error() && !strings.ContainsAny(err.Error(), " :/") {
		code = err.Error()
	}
	return failed(code)
}

type loaded struct {
	member Member
	run    Run
	snap   *snapshot.Snapshot
	plug   plugin
}

func (e *Engine) load(ctx context.Context, job string) (loaded, error) {
	var l loaded
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		m, err := getMember(tx, job)
		if err != nil {
			return err
		}
		l.member = m
		if l.run, err = getRun(tx, m.Batch); err != nil {
			l.run = Run{Batch: m.Batch}
		}
		v, err := e.jobs.SnapshotVersion(tx, m.Batch)
		if err != nil {
			return nil // before confirmation (preflight) there is no snapshot yet
		}
		l.snap, err = e.snap.Load(tx, m.Batch, v)
		return err
	})
	if err != nil {
		return l, err
	}
	p, ok := e.plugins[l.member.Format]
	if !ok {
		return l, errors.New("FORMAT_UNSUPPORTED")
	}
	l.plug = p
	return l, nil
}

// -- preflight -------------------------------------------------------------

func (e *Engine) preflight(ctx sched.StageContext) sched.StageOutcome {
	a := ctx.Attempt()
	l, err := e.load(ctx, a.JobID)
	if err != nil {
		return classify(err, "PREFLIGHT_FAILED")
	}
	ident := func(p string) (preflight.FileID, error) {
		id, err := platform.Identify(p)
		return preflight.FileID{Volume: id.Volume, Index: id.Index, Links: id.Links}, err
	}
	src, err := preflight.Inspect(l.member.Source, ident)
	if err != nil {
		return failed("SOURCE_UNREADABLE")
	}
	m := l.member
	// one resource id per attempt: a preflight that was stopped after it copied the original leaves that copy
	// behind (it is removed with the job), and the retry must not collide with it
	inputID := "in_" + a.AttemptID
	if m.Input == "" {
		if err = e.copyInput(a.JobID, inputID, src.Real); err != nil {
			return failed("INPUT_COPY_FAILED")
		}
		m.Input, m.Size, m.Fast = inputID, src.Size, src.Fast
		if err = e.ws.Commit(ctx, func(tx store.Tx) error { return putMember(tx, m) }); err != nil {
			return failed("STORE_FAILED")
		}
	}
	defer e.refs.Revoke(a)
	// a password handed over by the user is used once, here, and wiped
	if pw := e.takeSecret(a.JobID); pw != nil {
		defer keys.Zero(pw)
		copyID, out := e.unlock(ctx, a, l.plug, m.Input, pw)
		if out != nil {
			return *out
		}
		m.Input, m.Unlocked, m.Detail = copyID, true, ""
		if err = e.ws.Commit(ctx, func(tx store.Tx) error { return putMember(tx, m) }); err != nil {
			return failed("STORE_FAILED")
		}
	}
	g, err := e.refs.IssueRead(a, m.Input)
	if err != nil {
		return classify(err, "RESOURCE_UNAVAILABLE")
	}
	files, wires, err := refs.Handoff(g)
	if err != nil {
		return failed("RESOURCE_UNAVAILABLE")
	}
	w, err := e.launch(ctx, l.plug, files)
	if err != nil {
		return launchFailed(ctx, err)
	}
	defer w.Terminate(time.Second)
	probe, err := (&preflight.PluginProber{Session: w.Session, Attempt: a, Input: wires[0]}).Probe(ctx)
	if err != nil {
		return failed("PROBE_FAILED")
	}
	accepted := map[string]bool{}
	for _, f := range l.plug.man.Formats {
		accepted[f] = true
	}
	o, err := preflight.Decide(probe, accepted, preflight.DefaultLimits())
	if err != nil {
		return failed("PROBE_INVALID")
	}
	switch o.State {
	case preflight.Locked:
		return done(jobs.DetailPasswordRequired)
	case preflight.Unprocessable:
		return failed(o.Reason)
	}
	return done("")
}

// copyInput writes the original into the workspace as an encrypted, immutable resource.
func (e *Engine) copyInput(job, id, path string) error {
	tk, err := e.taskKey(job)
	if err != nil {
		return err
	}
	defer keys.Zero(tk)
	key, err := keys.ResourceKey(tk, id)
	if err != nil {
		return err
	}
	defer keys.Zero(key)
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	dst := filepath.Join(e.opt.Dir, "resources", id+".src")
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w, err := store.NewResourceWriter(out, key, id, 8<<30)
	if err == nil {
		_, err = io.Copy(w, in)
	}
	if err == nil {
		err = w.Close()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	final := filepath.Join(e.opt.Dir, "resources", id)
	if err = os.Rename(dst, final); err != nil {
		return err
	}
	return e.refs.Adopt(job, id, final)
}

// -- extract and detect ------------------------------------------------------

// pipeline builds the extraction and detection pipeline of a snapshot. A snapshot that uses the model
// gets a model worker for the length of the stage: call release when the stage is done.
func (e *Engine) pipeline(ctx context.Context, a kernel.AttemptRef, l loaded) (pipe *extract.Pipeline, release func(), err error) {
	c := l.snap.Content()
	release = func() {}
	var det *detect.Service
	if c.Detect.ModelID != "" && !c.Detect.RulesOnly {
		runner, rel, merr := e.modelRunner(ctx, a)
		if merr != nil {
			return nil, nil, merr
		}
		release = rel
		if det, err = e.buildDetector(c, runner); err != nil {
			release()
			return nil, nil, err
		}
	} else if det, err = e.detector(c); err != nil {
		return nil, nil, err
	}
	b, err := e.bodies(l.member.Job)
	if err != nil {
		release()
		return nil, nil, err
	}
	if pipe, err = extract.New(extract.Pipeline{WS: e.ws, Detector: det, Findings: e.fr, Bodies: b}); err != nil {
		release()
		return nil, nil, err
	}
	return pipe, release, nil
}

func (e *Engine) extractDetect(ctx sched.StageContext) sched.StageOutcome {
	a := ctx.Attempt()
	l, err := e.load(ctx, a.JobID)
	if err != nil || l.snap == nil {
		return classify(err, "SNAPSHOT_MISSING")
	}
	pipe, releaseModel, err := e.pipeline(ctx, a, l)
	if err != nil {
		return classify(err, "DETECTOR_UNAVAILABLE")
	}
	defer releaseModel()
	g, err := e.refs.IssueRead(a, l.member.Input)
	if err != nil {
		return classify(err, "RESOURCE_UNAVAILABLE")
	}
	defer e.refs.Revoke(a)
	files, wires, err := refs.Handoff(g)
	if err != nil {
		return failed("RESOURCE_UNAVAILABLE")
	}
	w, err := e.launch(ctx, l.plug, files)
	if err != nil {
		return launchFailed(ctx, err)
	}
	defer w.Terminate(time.Second)
	if _, err = pipe.Run(ctx, extract.Request{Session: w.Session, Attempt: a, Batch: l.member.Batch, Facet: l.snap.Detect(), Params: map[string]any{"input": wires[0]}}); err != nil {
		return classify(err, "EXTRACT_FAILED")
	}
	return done("")
}

// -- resolve -----------------------------------------------------------------

func policyOf(c snapshot.Content) policy.Policy {
	p := policy.Policy{CategoryAction: map[string]policy.Action{}, MinConfidence: c.Dispose.MinConfidence, Calibrated: c.Detect.Calibrated,
		HumanReview: map[string]bool{}, GapRemove: c.Dispose.ImageDefault == "remove", StrictFile: c.Dispose.Mode == "strict"}
	for cat, a := range c.Dispose.CategoryAction {
		p.CategoryAction[cat] = policy.Action(a)
	}
	// categories a person can mark by hand have no detection rule, hence no scheme entry
	for _, cat := range []string{"person", "address", "organization", "other"} {
		if _, ok := p.CategoryAction[cat]; !ok {
			p.CategoryAction[cat] = policy.Placeholder
		}
	}
	for _, cat := range c.Dispose.HumanReview {
		p.HumanReview[cat] = true
	}
	return p
}

func (e *Engine) resolve(ctx sched.StageContext) sched.StageOutcome {
	a := ctx.Attempt()
	l, err := e.load(ctx, a.JobID)
	if err != nil || l.snap == nil {
		return classify(err, "SNAPSHOT_MISSING")
	}
	if _, err = e.pol.Run(ctx, policy.Request{Job: a.JobID, Policy: policyOf(l.snap.Content()), DisposeDigest: l.snap.Digest(kernel.FacetDispose)}); err != nil {
		return classify(err, "RESOLVE_FAILED")
	}
	var open int
	if err = e.ws.Commit(ctx, func(tx store.Tx) (err error) { open, err = (exceptions.Repository{}).Open(tx, a.JobID); return }); err != nil {
		return failed("STORE_FAILED")
	}
	if open > 0 {
		return sched.StageOutcome{Kind: sched.NeedsReview}
	}
	return done("")
}

// -- plan --------------------------------------------------------------------

func renderer() plan.Renderer {
	return plan.Renderer{Placeholders: map[string]string{"phone": "[手机号]", "email": "[邮箱]", "id_number": "[证件号]", "payment_card": "[银行卡]", "account": "[账号]",
		"ip_address": "[IP]", "url": "[链接]", "secret": "[凭证]", "date": "[日期]", "person": "[姓名]", "address": "[地址]", "organization": "[机构]"},
		Fallback: "[已处理]", MaskHead: 3, MaskTail: 4, MaskChar: '*'}
}

type built struct {
	plan   plan.Plan
	repl   func(plan.Operation) (string, error)
	inputs int
}

// buildPlan turns the stored dispositions into the deterministic operation plan.
func (e *Engine) buildPlan(ctx context.Context, l loaded) (built, error) {
	job := l.member.Job
	bodies, err := e.bodies(job)
	if err != nil {
		return built{}, err
	}
	var ds []policy.Disposition
	var res policy.Stored
	var attempt string
	if err = e.ws.Commit(ctx, func(tx store.Tx) error {
		var ok bool
		if res, ok, err = policy.Resolution(tx, job); err != nil || !ok {
			return errors.New("RESOLUTION_MISSING")
		}
		for after := 0; ; {
			page, err := policy.Dispositions(tx, job, after, policy.MaxPage)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			ds, after = append(ds, page...), after+len(page)
		}
		if attempt, ok, err = e.units.Sealed(tx, job); err != nil || !ok {
			return errors.New("UNITS_MISSING")
		}
		return nil
	}); err != nil {
		return built{}, err
	}
	rd := renderer()
	text := map[string]string{} // unit id -> body, loaded on demand
	original := func(unit string, s, en int) (string, error) {
		body, ok := text[unit]
		if !ok {
			var u extract.Unit
			var found bool
			if err := e.ws.Commit(ctx, func(tx store.Tx) (err error) { u, found, err = e.units.ByID(tx, attempt, unit); return }); err != nil || !found {
				return "", errors.New("UNIT_MISSING")
			}
			if body, err = bodies.Get(u.Body); err != nil {
				return "", err
			}
			text[unit] = body
		}
		r := []rune(body)
		if s < 0 || en > len(r) || s >= en {
			return "", errors.New("RANGE_INVALID")
		}
		return string(r[s:en]), nil
	}
	var specs []plan.Spec
	for _, d := range ds {
		switch d.Action {
		case policy.Keep:
		case policy.RemovePart:
			specs = append(specs, plan.Spec{Kind: plan.PartOp, Unit: d.ID, Action: "remove_part"})
		default:
			var u extract.Unit
			var found bool
			if err = e.ws.Commit(ctx, func(tx store.Tx) (err error) { u, found, err = e.units.ByID(tx, attempt, d.Unit); return }); err != nil || !found {
				return built{}, errors.New("UNIT_MISSING")
			}
			specs = append(specs, plan.Spec{Kind: plan.TextOp, Unit: d.Unit, Start: d.Start, End: d.End, Action: string(d.Action), Category: d.Category,
				Source: []plan.Segment{{Locator: plan.Locator(u.Part, u.Ordinal)}}, ContainerEnd: u.Chars})
		}
	}
	repl := func(o plan.Operation) (string, error) {
		orig, err := original(o.Spec.Unit, o.Spec.Start, o.Spec.End)
		if err != nil {
			return "", err
		}
		return rd.Text(o.Spec.Action, o.Spec.Category, orig)
	}
	p, err := plan.Build(l.member.Input, res.Key, l.snap.Digest(kernel.FacetRender), specs)
	if err != nil {
		return built{}, err
	}
	if l.member.Detail != "" && strings.HasPrefix(l.member.Detail, "widen:") {
		var rs []plan.Residual
		for _, u := range strings.Split(strings.TrimPrefix(l.member.Detail, "widen:"), ",") {
			if u != "" {
				rs = append(rs, plan.Residual{Kind: plan.TextOp, Unit: u})
			}
		}
		if len(rs) > 0 {
			if p, err = plan.Widen(p, l.member.Input, res.Key, l.snap.Digest(kernel.FacetRender), rs); err != nil {
				return built{}, err
			}
		}
	}
	return built{plan: p, repl: repl}, nil
}

func (e *Engine) export(ctx sched.StageContext) sched.StageOutcome {
	a := ctx.Attempt()
	l, err := e.load(ctx, a.JobID)
	if err != nil || l.snap == nil {
		return classify(err, "SNAPSHOT_MISSING")
	}
	b, err := e.buildPlan(ctx, l)
	if err != nil {
		return classify(err, "PLAN_FAILED")
	}
	st, err := export.Stage(e.refs, a, l.member.Input, b.plan, b.repl, 8<<30)
	if err != nil {
		return classify(err, "EXPORT_STAGE_FAILED")
	}
	st.Look = lookOf(l.snap)
	w, err := e.launch(ctx, l.plug, st.Files)
	if err != nil {
		st.Release(e.refs, a)
		return launchFailed(ctx, err)
	}
	defer w.Terminate(time.Second)
	if _, _, err = e.exp.Export(ctx, w.Session, a, st, b.plan); err != nil {
		return classify(err, "EXPORT_FAILED")
	}
	return done("")
}

// -- validate ----------------------------------------------------------------

func (e *Engine) validate(ctx sched.StageContext) sched.StageOutcome {
	a := ctx.Attempt()
	l, err := e.load(ctx, a.JobID)
	if err != nil || l.snap == nil {
		return classify(err, "SNAPSHOT_MISSING")
	}
	b, err := e.buildPlan(ctx, l)
	if err != nil {
		return classify(err, "PLAN_FAILED")
	}
	var cand export.Candidate
	var dests []coverage.Destination
	var attempt string
	var known []validate.Known
	var keptGaps bool
	if err = e.ws.Commit(ctx, func(tx store.Tx) error {
		c, d, ok, err := (export.Repository{}).Current(tx, a.JobID)
		if err != nil || !ok {
			return errors.New("CANDIDATE_MISSING")
		}
		cand, dests = c, d
		if attempt, ok, err = e.units.Sealed(tx, a.JobID); err != nil || !ok {
			return errors.New("UNITS_MISSING")
		}
		fs, err := findingsOf(tx, e.fr, a.JobID)
		if err != nil {
			return err
		}
		for _, f := range fs {
			u, ok, err := e.units.ByID(tx, attempt, f.Unit)
			if err != nil || !ok {
				continue
			}
			known = append(known, validate.Known{Part: u.Part, Ordinal: u.Ordinal, Start: f.Start, End: f.End})
		}
		return nil
	}); err != nil {
		return classify(err, "VALIDATE_PREPARE_FAILED")
	}
	if cand.PlanVersion != b.plan.Version {
		return failed("PLAN_VERSION_MISMATCH") // the candidate was not made from this plan
	}
	edits, err := validate.EditsOf(b.plan, b.repl)
	if err != nil {
		return classify(err, "PLAN_FAILED")
	}
	st, err := validate.Stage(e.refs, a, l.member.Input, cand.PlanResource, cand.Resource)
	if err != nil {
		return classify(err, "VALIDATE_STAGE_FAILED")
	}
	st.Look = lookOf(l.snap)
	defer e.refs.Revoke(a)
	w, err := e.launch(ctx, l.plug, st.Files)
	if err != nil {
		return launchFailed(ctx, err)
	}
	defer w.Terminate(time.Second)
	pipe, releaseModel, err := e.pipeline(ctx, a, l)
	if err != nil {
		return classify(err, "DETECTOR_UNAVAILABLE")
	}
	defer releaseModel()
	svc := &validate.Service{Commit: e.ws.Commit, Admit: e.ws.AdmitTx, Runner: e.runner, Scanner: pipe, Findings: e.fr, Refs: e.refs,
		UnitOf: func(tx store.Tx, part string, ordinal int) (string, uint64, bool) {
			u, ok, err := e.units.ByPosition(tx, attempt, part, ordinal)
			return u.UnitID, 1, ok && err == nil
		},
		Persist: func(tx store.Tx, job string, ds []validate.Draft, validation string) error {
			out := make([]exceptions.Draft, len(ds))
			for i, d := range ds {
				out[i] = exceptions.Draft{Type: exceptions.Type(d.Type), ScopeKey: d.ScopeKey, Reason: d.Reason}
			}
			_, err := e.exc.SyncPostExport(tx, job, out, validation)
			return err
		}}
	_ = keptGaps
	res, err := svc.Run(ctx, validate.Request{Session: w.Session, Attempt: a, Batch: l.member.Batch, Stage: st, Plan: b.plan, CandidateDigest: cand.Digest, Destinations: dests,
		Edits: edits, Known: known, WidenUsed: false, Widen: func(rs []plan.Residual) error {
			_, err := plan.Widen(b.plan, l.member.Input, cand.PlanVersion, l.snap.Digest(kernel.FacetRender), rs)
			return err
		},
		Facet: l.snap.Detect(), ExpectNoText: l.member.Format == "pdf"})
	if err != nil && res.Conclusion != validate.NotRun {
		return classify(err, "VALIDATE_FAILED")
	}
	if res.Conclusion == validate.Widened {
		var us []string
		for _, r := range res.ResidualUnits {
			us = append(us, r.Unit)
		}
		m := l.member
		m.Detail = "widen:" + strings.Join(us, ",")
		e.ws.Commit(ctx, func(tx store.Tx) error { return putMember(tx, m) })
	}
	return done(string(res.Conclusion))
}

func findingsOf(tx store.Tx, fr *findings.Repository, job string) ([]findings.Finding, error) {
	var out []findings.Finding
	for after := int64(0); ; {
		page, err := fr.Page(tx, findings.Filter{Job: job}, after, findings.MaxPage)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		out, after = append(out, page...), page[len(page)-1].ID
	}
}

// -- publish -----------------------------------------------------------------

// outputName is the neutral name of a delivered file. The file number restarts in every batch, so the
// batch id is part of the name: two batches delivered to the same folder never meet on one name
// (publishing never overwrites, so a collision would block the second one).
func outputName(m Member, partial bool) string {
	base := m.Batch + "-" + m.Name
	ext := "." + m.Format
	if partial {
		base += publish.PartialMarker
	}
	return base + ext
}

func (e *Engine) publish(ctx sched.StageContext) sched.StageOutcome {
	a := ctx.Attempt()
	l, err := e.load(ctx, a.JobID)
	if err != nil || l.snap == nil {
		return classify(err, "SNAPSHOT_MISSING")
	}
	ident := func(p string) (preflight.FileID, error) {
		id, err := platform.Identify(p)
		return preflight.FileID{Volume: id.Volume, Index: id.Index, Links: id.Links}, err
	}
	src, err := preflight.Inspect(l.member.Source, ident)
	if err != nil || src.Fast != l.member.Fast {
		return sched.StageOutcome{Kind: sched.Blocked, Detail: "input_changed", Err: &kernel.ErrorCode{Code: "INPUT_CHANGED"}}
	}
	var cand export.Candidate
	var val validate.Result
	var pending, retained int
	var key string
	if err = e.ws.Commit(ctx, func(tx store.Tx) error {
		c, _, ok, err := (export.Repository{}).Current(tx, a.JobID)
		if err != nil || !ok {
			return errors.New("CANDIDATE_MISSING")
		}
		cand = c
		if val, ok, err = validate.Latest(tx, a.JobID); err != nil || !ok {
			return errors.New("VALIDATION_MISSING")
		}
		if pending, err = (exceptions.Repository{}).Open(tx, a.JobID); err != nil {
			return err
		}
		st, ok, err := policy.Resolution(tx, a.JobID)
		if err != nil || !ok {
			return errors.New("RESOLUTION_MISSING")
		}
		key = st.Key
		for after := 0; ; {
			page, err := policy.Dispositions(tx, a.JobID, after, policy.MaxPage)
			if err != nil || len(page) == 0 {
				return err
			}
			for _, d := range page {
				if d.Action == policy.Keep && isGapKind(d.Category) {
					retained++
				}
			}
			after += len(page)
		}
	}); err != nil {
		return classify(err, "PUBLISH_PREPARE_FAILED")
	}
	partial := retained > 0
	facts := verdict.Facts{Phase: verdict.PhaseReady, Validated: val.Conclusion == validate.Passed && val.PlanVersion == cand.PlanVersion && val.Candidate == cand.Digest,
		InputVerified: true, PendingExceptions: pending, RetainedGaps: retained, PartialEligible: partial && val.Status == validate.StatusPartial, PublishGranted: true}
	g, err := e.refs.IssueRead(a, cand.Resource)
	if err != nil {
		return classify(err, "RESOURCE_UNAVAILABLE")
	}
	defer e.refs.Revoke(a)
	h := sha256.New()
	rd, _, err := g.Plaintext()
	if err != nil {
		return failed("CANDIDATE_UNREADABLE")
	}
	if _, err = io.Copy(h, rd); err != nil {
		return failed("CANDIDATE_UNREADABLE")
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	rd, _, err = g.Plaintext()
	if err != nil {
		return failed("CANDIDATE_UNREADABLE")
	}
	name := outputName(l.member, partial)
	// A publish interrupted after the file was written (and settled at start-up) is not done twice: the
	// file is already there, with this content.
	if !e.delivered(ctx, a.JobID, l.run.OutputDir, name, digest) {
		_, err = e.pub.Publish(ctx, publish.Request{Attempt: a, Key: key, Facts: facts, Candidate: rd, Digest: digest, Dir: l.run.OutputDir, Name: name, AllowNonAtomic: false})
		if err != nil {
			return classify(err, "PUBLISH_FAILED")
		}
	}
	// the delivered file's checksum is what the manifest lists
	m := l.member
	m.Candidate = digest
	if err = e.ws.Commit(ctx, func(tx store.Tx) error { return putMember(tx, m) }); err != nil {
		return failed("STORE_FAILED")
	}
	if partial {
		return done(jobsDetailPartial)
	}
	return done("")
}

const jobsDetailPartial = "partial"

func isGapKind(c string) bool {
	switch coverage.GapKind(c) {
	case coverage.GapScanPage, coverage.GapImageWithText, coverage.GapEmbeddedObject, coverage.GapOutlinedText, coverage.GapUninspectedPart:
		return true
	}
	return false
}

var _ = utf8.RuneLen
var _ = workspace.ErrStale

// unlock decrypts the stored original into a new encrypted copy that every later
// stage reads (FR-09). A wrong password sends the file back to waiting for one.
func (e *Engine) unlock(ctx context.Context, a kernel.AttemptRef, p plugin, original string, password []byte) (string, *sched.StageOutcome) {
	gin, err := e.refs.IssueRead(a, original)
	if err != nil {
		o := classify(err, "RESOURCE_UNAVAILABLE")
		return "", &o
	}
	gout, err := e.refs.IssueWrite(a, 8<<30)
	if err != nil {
		o := classify(err, "RESOURCE_UNAVAILABLE")
		return "", &o
	}
	files, wires, err := refs.Handoff(gin, gout)
	if err != nil {
		o := failed("RESOURCE_UNAVAILABLE")
		return "", &o
	}
	w, err := e.launch(ctx, p, files)
	if err != nil {
		o := launchFailed(ctx, err)
		return "", &o
	}
	defer w.Terminate(time.Second)
	u := &preflight.PluginUnlocker{Session: w.Session, Attempt: a, Input: wires[0], Output: wires[1],
		Seal: func() (string, error) { _, err := gout.Freeze(); return gout.ResourceID(), err }}
	ref, err := preflight.Unlock(ctx, u, wires[0].Ref, password)
	switch {
	case errors.Is(err, preflight.ErrPasswordWrong):
		o := sched.StageOutcome{Kind: sched.Completed, Detail: jobs.DetailPasswordRequired, Err: &kernel.ErrorCode{Code: "PASSWORD_INCORRECT"}}
		return "", &o
	case err != nil:
		o := failed("UNLOCK_FAILED")
		return "", &o
	}
	return ref, nil
}

// delivered says whether this very file (folder, name, content) is already recorded as delivered.
func (e *Engine) delivered(ctx context.Context, job, dir, name, digest string) bool {
	var found bool
	e.ws.Commit(ctx, func(tx store.Tx) error {
		ds, superseded, err := publish.Deliveries(tx, job)
		if err != nil {
			return nil
		}
		for i, d := range ds {
			if !superseded[i] && d.Dir == dir && d.Name == name && d.Digest == digest {
				found = true
			}
		}
		return nil
	})
	return found
}

// lookOf is how the batch's rasterised outputs are painted, as frozen when it started.
func lookOf(s *snapshot.Snapshot) plan.Look {
	r := s.Content().Render
	return plan.Look{DPI: r.DPI, Style: r.Style}
}
