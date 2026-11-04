package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/lifecycle"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/report"
	"github.com/jorben/tacet/internal/retention"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/validate"
	"github.com/jorben/tacet/internal/verdict"
)

var _ Core = (*Engine)(nil)

// exitGrace is how long an exit waits for running stages to reach a safe boundary before the processes
// that are left are reclaimed (PF-04).
const exitGrace = 10 * time.Second

// Close runs the exit order of architecture 6.2: stop dispatching, bring running stages to a boundary,
// save where every file stands, reclaim the worker processes, wait for a publish in its commit section,
// settle retention, and release the workspace last.
func (e *Engine) Close() error { return e.close(lifecycle.Normal) }

// CloseForSystem is the exit when the system shuts down or the user logs out (architecture 6.2): the time
// is short, so dispatch stops, the running stages are cancelled (their results are not recorded), the
// plugin processes are reclaimed and the workspace is released, without waiting or cleaning. What was
// committed and the publish intents carry the work over; the next start recovers it.
func (e *Engine) CloseForSystem() error { return e.close(lifecycle.SystemShutdown) }

func (e *Engine) close(mode lifecycle.ExitMode) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrClosed
	}
	e.closed = true
	for id, pw := range e.secrets {
		keys.Zero(pw)
		delete(e.secrets, id)
	}
	e.mu.Unlock()
	waitDone := func(ctx context.Context) error {
		select {
		case <-e.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	stopper, err := lifecycle.NewStopper([]lifecycle.Step{
		{Name: lifecycle.ExitStopDispatch, Run: func(context.Context) error {
			e.queue.PauseDispatch(true)
			e.stopProbe()
			if mode == lifecycle.SystemShutdown {
				// cancelled before the workers are reclaimed: a stage whose worker dies then ends as
				// cancelled, which is not recorded, instead of as failed, which would be
				e.cancel()
				e.act.stop()
			}
			return nil
		}},
		// the dispatcher's context ends the stages it runs; whatever is not at a boundary after the grace
		// period is reclaimed by the next steps
		{Name: lifecycle.ExitCancel, Run: func(ctx context.Context) error {
			e.cancel()
			waitDone(ctx)
			e.act.stop()
			return nil
		}},
		{Name: lifecycle.ExitCheckpoint, Run: e.recoverJobs},
		{Name: lifecycle.ExitReap, Run: func(ctx context.Context) error {
			e.probeStarted() // stopped by now: a probe worker already started is reaped, none starts later
			n, err := e.reapWorkers()
			if e.afterReap != nil {
				e.afterReap(n)
			}
			return err
		}},
		{Name: lifecycle.ExitPublishWait, Run: func(ctx context.Context) error {
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return waitDone(c)
		}},
		{Name: lifecycle.ExitRetention, Run: e.applyRetention},
		{Name: lifecycle.ExitRelease, Run: func(context.Context) error { return e.ws.Close() }},
	}, exitGrace, e.opt.Trace)
	if err != nil {
		return err
	}
	if err = stopper.Stop(context.Background(), mode); err != nil {
		var se *lifecycle.StepError
		if errors.As(err, &se) {
			return se.Err
		}
	}
	return err
}

func isAbsDir(p string) bool {
	fi, err := os.Stat(p)
	return filepath.IsAbs(p) && err == nil && fi.IsDir()
}

// Import creates a draft batch and starts the read-only preflight of every file.
func (e *Engine) Import(ctx context.Context, r ImportRequest) (BatchStatus, error) {
	kind := jobs.RegularBatch
	if r.Kind == "quick" {
		kind = jobs.QuickBatch
	}
	if len(r.Files) == 0 || len(r.Files) > 100000 || (r.Kind != "quick" && r.Kind != "batch") || (kind == jobs.QuickBatch && len(r.Files) != 1) || !isAbsDir(r.OutputDir) {
		return BatchStatus{}, ErrInvalid
	}
	for _, f := range r.Files {
		fi, err := os.Stat(f)
		if !filepath.IsAbs(f) || err != nil || !fi.Mode().IsRegular() {
			return BatchStatus{}, ErrInvalid
		}
	}
	var batch, displaced string
	var ids []string
	var members []Member
	preset := r.Preset
	if preset == "" {
		preset = e.defaultPreset(ctx) // reads the model catalog: before the transaction
	}
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		n, err := next(tx, "batch")
		if err != nil {
			return err
		}
		batch = fmt.Sprintf("b%04d", n)
		if err = putRun(tx, Run{Batch: batch, Preset: preset, OutputDir: r.OutputDir, Kind: r.Kind}); err != nil {
			return err
		}
		if r.ParkWaiting {
			displaced, err = e.jobs.ParkHolder(tx, kind, func(b string) (bool, error) {
				held, err := getRun(tx, b)
				return held.Grant, err
			})
			if err != nil {
				return err
			}
		}
		if e.beforeDraft != nil {
			if err = e.beforeDraft(); err != nil {
				return err
			}
		}
		if err = e.jobs.CreateDraft(tx, batch, kind); err != nil {
			return err
		}
		for i, f := range r.Files {
			jn, err := next(tx, "job")
			if err != nil {
				return err
			}
			m := Member{Job: fmt.Sprintf("j%06d", jn), Batch: batch, Source: f, Name: fmt.Sprintf("f%04d", i+1), Format: strings.ToLower(strings.TrimPrefix(filepath.Ext(f), "."))}
			ids, members = append(ids, m.Job), append(members, m)
			if err = putMember(tx, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return BatchStatus{}, mapErr(err)
	}
	if e.beforeMembers != nil {
		if err = e.beforeMembers(); err != nil {
			return BatchStatus{}, err
		}
	}
	if err = e.createKeys(batch, ids...); err != nil {
		return BatchStatus{}, err
	}
	// members are added (and their preflight dispatched) only after the keys exist
	if err = e.ws.Commit(ctx, func(tx store.Tx) error {
		for i, m := range members {
			if err := e.jobs.AddMember(tx, batch, m.Job, i); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return BatchStatus{}, mapErr(err)
	}
	e.grantRetention(ctx, ids)
	if displaced != "" {
		e.changed(displaced)
	}
	e.changed(batch)
	st, err := e.Status(ctx, batch, 50)
	st.Displaced = displaced
	return st, err
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, jobs.ErrQuickActive):
		return fmt.Errorf("%w", errors.New(CodeQuickTaskActive))
	case errors.Is(err, jobs.ErrBatchActive):
		return errors.New(CodeBatchActive)
	case errors.Is(err, jobs.ErrVersionConflict):
		return ErrVersion
	}
	return err
}

func running(s jobs.State) bool {
	switch s {
	case jobs.Preflighting, jobs.Queued, jobs.ExtractingDetecting, jobs.Resolving, jobs.Exporting, jobs.Validating, jobs.Publishing, jobs.Cancelling:
		return true
	}
	return false
}

func (e *Engine) members(ctx context.Context, batch string) ([]jobs.Job, error) {
	var out []jobs.Job
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		for after := -1; ; {
			page, err := e.jobs.Members(tx, batch, after, 200)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				return nil
			}
			out, after = append(out, page...), page[len(page)-1].Ordinal
		}
	})
	return out, err
}

func (e *Engine) pollIdle(ctx context.Context, batch string, preflightOnly bool) ([]jobs.Job, error) {
	for {
		// Read before the members. The start that Resume is carrying out clears its request in the same commit
		// that confirms the batch, so "no request" read first means every file already shows its confirmed state.
		pending := !preflightOnly && e.startPending(ctx, batch)
		ms, err := e.members(ctx, batch)
		if err != nil {
			return nil, err
		}
		busy := pending
		confirmed := !preflightOnly && e.confirmed(ctx, batch)
		for _, m := range ms {
			s := m.Position.State
			switch {
			case preflightOnly:
				// A file resumed from its inspection is queued to be inspected again from the start: until the
				// scheduler takes it, it is neither inspecting nor inspected, and a start must not see it as ready.
				busy = busy || s == jobs.Preflighting || (s == jobs.Queued && m.Position.Cursor == kernel.StagePreflight)
			case running(s), s == jobs.ReadyToPublish && e.hasGrant(ctx, batch):
				busy = true
			case s == jobs.Preflighted && confirmed:
				// A file unlocked after the batch started joins it as soon as its inspection
				// is stored; until that follow-up lands it is still on its way, not finished.
				busy = true
			}
		}
		if !busy {
			return ms, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
}

// confirmed reports whether the batch has been started (its snapshot is bound).
func (e *Engine) confirmed(ctx context.Context, batch string) bool {
	var ok bool
	e.ws.Commit(ctx, func(tx store.Tx) error { _, err := e.jobs.SnapshotVersion(tx, batch); ok = err == nil; return nil })
	return ok
}

func (e *Engine) hasGrant(ctx context.Context, batch string) bool {
	var g bool
	e.ws.Commit(ctx, func(tx store.Tx) error { r, err := getRun(tx, batch); g = err == nil && r.Grant; return nil })
	return g
}

// Preflight waits for the read-only inspection of a draft batch.
func (e *Engine) Preflight(ctx context.Context, batch string) (BatchStatus, error) {
	if _, err := e.pollIdle(ctx, batch, true); err != nil {
		return BatchStatus{}, err
	}
	return e.Status(ctx, batch, 200)
}

// Start freezes the snapshot and confirms the batch in one commit, then lets the
// pipeline run. Authorize states the publish authorization up front (FR-37).
func (e *Engine) Start(ctx context.Context, r StartRequest) error {
	if r.Mode != "" && r.Mode != "guided" && r.Mode != "strict" {
		return ErrInvalid
	}
	// The request is kept before the wait for the inspection: if the call ends first (an interrupt, a closed
	// window), Resume carries the same start out, with the same authorization.
	if err := e.recordStart(ctx, r); err != nil {
		return err
	}
	ms, err := e.pollIdle(ctx, r.Batch, true)
	if err != nil {
		return err
	}
	preset := r.Preset
	if preset == "" {
		preset = e.defaultPreset(ctx)
	}
	avail := e.available(ctx) // the catalog is read before the transaction
	err = e.ws.Commit(ctx, func(tx store.Tx) error {
		run, err := getRun(tx, r.Batch)
		if err != nil {
			return ErrNotFound
		}
		formats := map[string]bool{}
		refsByFormat := map[string]snapshot.PluginRef{}
		for _, j := range ms {
			m, err := getMember(tx, j.ID)
			if err != nil {
				return err
			}
			if p, ok := e.plugins[m.Format]; ok && j.Position.State != jobs.Unprocessable {
				formats[m.Format] = true
				refsByFormat[m.Format] = p.id
			}
		}
		if len(formats) == 0 {
			return fmt.Errorf("%w: no processable file", ErrNotReady)
		}
		out := r.OutputDir
		if out == "" {
			out = run.OutputDir
		}
		content, err := e.resolveContent(tx, preset, refsByFormat, out, avail)
		if err != nil {
			return err
		}
		if r.Mode == "strict" { // chosen for this batch; frozen with it (dispose facet)
			content.Dispose.Mode = "strict"
		}
		snap, err := snapshot.Freeze(content)
		if err != nil {
			return err
		}
		if err = e.snap.Save(tx, r.Batch, 0, snap); err != nil {
			return err
		}
		if err = e.jobs.BindSnapshot(tx, r.Batch, 0, snap.Version()); err != nil {
			return err
		}
		run.Preset, run.OutputDir, run.Grant, run.Partial, run.Pending = preset, out, r.Authorize, r.AllowPartial, ""
		if err = putRun(tx, run); err != nil {
			return err
		}
		_, err = e.jobs.ConfirmBatch(tx, r.Batch)
		return err
	})
	if err == nil {
		e.changed(r.Batch)
	}
	return mapErr(err)
}

// resolveContent resolves a preset into the content a snapshot freezes, for files of the given formats
// published to out. A preset that uses a model needs it installed; the build that runs is chosen here, so a
// resumed run uses the same weights and provider.
func (e *Engine) resolveContent(tx store.Tx, preset string, refsByFormat map[string]snapshot.PluginRef, out string, avail map[string]bool) (snapshot.Content, error) {
	env := snapshot.Env{Models: map[string]snapshot.Model{}, FormatActions: map[string]map[string]bool{}}
	for f := range refsByFormat {
		env.Formats = append(env.Formats, f)
		env.FormatActions[f] = map[string]bool{"delete": true, "placeholder": true, "mask": true, "keep": true, "remove_part": true}
	}
	sort.Strings(env.Formats)
	digests := map[string]string{}
	cat, err := e.categoryCatalog(tx)
	if err != nil {
		return snapshot.Content{}, err
	}
	env.Categories = map[string]bool{}
	for _, id := range cat.IDs() {
		env.Categories[id] = true
	}
	for _, m := range e.models {
		env.Models[m.id] = snapshot.Model{Categories: m.categories, AdapterFloor: m.floor, MemoryBytes: m.kind.memory}
		digests[m.id] = m.digest
	}
	settings, serr := e.cfg.Settings(tx)
	style := ""
	if serr == nil {
		style = settings.RedactStyle
	}
	content, report, err := snapshot.ResolvePreset(tx, e.cfg, preset, snapshot.Bindings{Environment: env, ModelDigests: digests, Style: style,
		Runtime:  snapshot.RuntimeContent{Protocol: "1.0", Formats: refsByFormat},
		Delivery: snapshot.DeliveryContent{OutputDir: out, Naming: "neutral", PublishPolicy: "explicit", PartialRule: "marked"}})
	if err != nil {
		return snapshot.Content{}, err
	}
	if len(report.Blocking) > 0 {
		return snapshot.Content{}, fmt.Errorf("%w: %s", ErrNotReady, report.Blocking[0].Code)
	}
	// the placeholder text of the enabled and hand-markable categories is frozen with the run
	content.Render.Placeholders = cat.Placeholders(content.Detect.Categories)
	if content.Detect.ModelID != "" && !content.Detect.RulesOnly {
		m := e.modelByID(content.Detect.ModelID)
		if m == nil || !avail[m.id] {
			return snapshot.Content{}, fmt.Errorf("%w: %w", ErrNotReady, ErrModelNotInstalled)
		}
		content.Detect.Execution = e.execution(settings, m, avail)
	}
	return content, nil
}

// phaseOf maps a task state to the verdict's coarse phase.
func phaseOf(s jobs.State) verdict.Phase {
	switch s {
	case jobs.AwaitingReview:
		return verdict.PhaseAwaitingReview
	case jobs.Blocked, jobs.Locked:
		return verdict.PhaseBlocked
	case jobs.Failed:
		return verdict.PhaseFailed
	case jobs.Unprocessable:
		return verdict.PhaseUnprocessable
	case jobs.Skipped:
		return verdict.PhaseSkipped
	case jobs.Cancelled:
		return verdict.PhaseCancelled
	case jobs.ReadyToPublish:
		return verdict.PhaseReady
	case jobs.Publishing:
		return verdict.PhasePublishing
	case jobs.Completed, jobs.Partial:
		return verdict.PhasePublished
	}
	return verdict.PhaseProcessing
}

// factsOf is the one place a stored task becomes verdict facts, for both front ends.
func factsOf(j jobs.Job, pending int, granted bool) verdict.Facts {
	f := verdict.Facts{Phase: phaseOf(j.Position.State), PluginQualified: true, CategoriesQualified: true, PendingExceptions: pending, PublishGranted: granted}
	switch j.Position.State {
	case jobs.ReadyToPublish, jobs.Publishing, jobs.Completed:
		f.Validated, f.InputVerified = true, true
	case jobs.Partial:
		f.Validated, f.InputVerified, f.RetainedGaps, f.PartialEligible = true, true, 1, true
	}
	return f
}

// fileStatus describes one file; only the rows that are shown pay for the counts.
func (e *Engine) fileStatus(tx store.Tx, j jobs.Job, run Run, pending int, s verdict.Status) (FileStatus, error) {
	m, err := getMember(tx, j.ID)
	if err != nil {
		return FileStatus{}, err
	}
	n, _ := e.fr.Count(tx, findings.Filter{Job: j.ID})
	cats, _ := e.fr.CountByCategory(tx, findings.Filter{Job: j.ID})
	fs := FileStatus{ID: j.ID, Name: m.Name, State: string(j.Position.State), Status: string(s), Detail: m.Detail, Format: m.Format, Pending: pending, Findings: n, Categories: cats}
	if pending > 0 {
		fs.Confirm = awaitingConfirm(tx, j.ID)
	}
	if j.Position.State == jobs.Completed || j.Position.State == jobs.Partial {
		fs.Output, fs.Digest = outputName(m, j.Position.State == jobs.Partial), m.Candidate
	}
	return fs, nil
}

// Files returns the rows [offset, offset+limit) of a batch in import order.
func (e *Engine) Files(ctx context.Context, batch string, offset, limit int) ([]FileStatus, error) {
	if offset < 0 || limit <= 0 || limit > 200 {
		return nil, ErrInvalid
	}
	var out []FileStatus
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		run, err := getRun(tx, batch)
		if err != nil {
			return ErrNotFound
		}
		page, err := e.jobs.Members(tx, batch, offset-1, limit)
		if err != nil {
			return err
		}
		for _, j := range page {
			pending, _ := (exceptions.Repository{}).Open(tx, j.ID)
			fs, err := e.fileStatus(tx, j, run, pending, verdict.Judge(factsOf(j, pending, run.Grant)))
			if err != nil {
				return err
			}
			out = append(out, fs)
		}
		return nil
	})
	return out, err
}

func (e *Engine) Status(ctx context.Context, batch string, limit int) (BatchStatus, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	ms, err := e.members(ctx, batch)
	if err != nil || len(ms) == 0 {
		return BatchStatus{}, ErrNotFound
	}
	st := BatchStatus{ID: batch, Total: len(ms), Counts: map[string]int{}}
	var statuses []verdict.Status
	err = e.ws.Commit(ctx, func(tx store.Tx) error {
		run, _ := getRun(tx, batch)
		st.Kind = run.Kind
		st.Authorized = run.Grant
		var err error
		if st.Parked, err = e.jobs.Parked(tx, batch); err != nil {
			return err
		}
		for i, j := range ms {
			pending, _ := (exceptions.Repository{}).Open(tx, j.ID)
			s := verdict.Judge(factsOf(j, pending, run.Grant))
			statuses = append(statuses, s)
			st.Counts[string(s)]++
			switch j.Position.State {
			case jobs.Preflighting, jobs.Queued, jobs.ExtractingDetecting, jobs.Resolving,
				jobs.Exporting, jobs.Validating, jobs.Publishing:
				st.Active++
			}
			if i < limit {
				fs, err := e.fileStatus(tx, j, run, pending, s)
				if err != nil {
					return err
				}
				st.Files = append(st.Files, fs)
			}
		}
		return nil
	})
	if err != nil {
		return BatchStatus{}, err
	}
	st.Started = e.confirmed(ctx, batch)
	sum := verdict.Summarize(statuses)
	st.Phase, st.Delivered, st.ExitCode = string(sum.Phase), sum.Delivered, verdict.ExitCode(statuses)
	st.Truncated = len(ms) > len(st.Files)
	return st, nil
}

func (e *Engine) Wait(ctx context.Context, batch string) (BatchStatus, error) {
	if _, err := e.pollIdle(ctx, batch, false); err != nil {
		return BatchStatus{}, err
	}
	e.ws.Commit(ctx, func(tx store.Tx) error { return e.jobs.FinishBatch(tx, batch) })
	return e.Status(ctx, batch, 200)
}

// afterState runs what the app owns after a stage outcome: the publish
// authorization of a file that became ready, the report of a published one and
// the release of a finished batch.
func (e *Engine) afterState(ctx context.Context, j jobs.Job) {
	switch j.Position.State {
	case jobs.Preflighted:
		// a file unlocked after its batch was confirmed joins that batch's snapshot
		var bound bool
		e.ws.Commit(ctx, func(tx store.Tx) error {
			_, err := e.jobs.SnapshotVersion(tx, j.Batch)
			bound = err == nil
			return nil
		})
		if bound {
			if e.beforeConfirm != nil {
				e.beforeConfirm()
			}
			e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvConfirm}, true)
		}
	case jobs.ReadyToPublish:
		var run Run
		var key string
		err := e.ws.Commit(ctx, func(tx store.Tx) error {
			m, err := getMember(tx, j.ID)
			if err != nil {
				return err
			}
			if run, err = getRun(tx, m.Batch); err != nil || !run.Grant {
				return errAbort
			}
			key, err = e.currentKey(tx, j.ID)
			return err
		})
		if err != nil {
			return
		}
		if e.pub.Grant(ctx, j.ID, key, publish.Explicit, run.Partial) == nil {
			e.dispatch(j.ID, kernel.StagePublish)
		}
	case jobs.Completed, jobs.Partial:
		e.writeReport(ctx, j)
	}
	if j.Position.State.Terminal() {
		e.ws.Commit(ctx, func(tx store.Tx) error { return e.jobs.FinishBatch(tx, j.Batch) })
	}
}

func (e *Engine) writeReport(ctx context.Context, j jobs.Job) {
	var m Member
	var run Run
	var val validate.Result
	e.ws.Commit(ctx, func(tx store.Tx) error {
		m, _ = getMember(tx, j.ID)
		run, _ = getRun(tx, j.Batch)
		val, _, _ = validate.Latest(tx, j.ID)
		return nil
	})
	st := verdict.Judge(factsOf(j, 0, true))
	f := report.File{ID: m.Name, AppVersion: Version, Components: map[string]string{"format": "builtin"}, RuleSetVersion: RuleSetID, PolicyDigest: "builtin", Status: st,
		Coverage: report.Coverage{Inspected: 1}, Validation: report.Validation{Status: string(val.Status), Conclusion: string(val.Conclusion)}}
	for _, u := range val.Unverified {
		f.Validation.Unverified = append(f.Validation.Unverified, u.Scope)
	}
	if m.Format == "pdf" {
		f.Validation.Limits = append(f.Validation.Limits, "RASTER_OUTPUT_HAS_NO_TEXT_LAYER")
	}
	g := report.NewGuard(m.Source, filepath.Base(m.Source), strings.TrimSuffix(filepath.Base(m.Source), filepath.Ext(m.Source)))
	report.Write(ctx, publish.Artifacts{Dir: run.OutputDir}, f, g)
}

// Findings lists a file's candidates with the decision that applies to each.
func (e *Engine) Findings(ctx context.Context, job string, after int64, limit int) ([]Finding, error) {
	if limit <= 0 || limit > findings.MaxPage {
		limit = findings.MaxPage
	}
	var out []Finding
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		page, err := e.fr.Page(tx, findings.Filter{Job: job}, after, limit)
		if err != nil {
			return err
		}
		eff, err := review.Effective(tx, page)
		if err != nil {
			return err
		}
		for _, f := range page {
			x := Finding{ID: f.ID, Unit: f.Unit, Category: f.Category, Source: string(f.Source), Score: f.Score, Start: f.Start, End: f.End}
			if d, ok := eff[f.ID]; ok {
				x.Decision = "redact"
				if d.Keep {
					x.Decision = "keep"
				}
			}
			out = append(out, x)
		}
		return nil
	})
	return out, err
}

// DecideFinding records a single decision and, for a file waiting on review,
// resolves it again.
func (e *Engine) DecideFinding(ctx context.Context, job string, finding int64, keep bool) error {
	var batch string
	if err := e.ws.Commit(ctx, func(tx store.Tx) error { m, err := getMember(tx, job); batch = m.Batch; return err }); err != nil {
		return ErrNotFound
	}
	var n int
	e.ws.Commit(ctx, func(tx store.Tx) error { n, _ = next(tx, "decision"); return nil })
	if _, err := e.revs.DecideItem(ctx, review.ItemRequest{Request: fmt.Sprintf("d%d", n), Batch: batch, Job: job, Finding: finding, Keep: keep}); err != nil {
		return err
	}
	return e.apply(ctx, job, jobs.Event{Kind: jobs.EvDecisionsChange}, true)
}

// apply sends an event to a task, tolerating an event that does not apply when
// quiet is set (a decision on a file that is not waiting changes nothing yet).
func (e *Engine) apply(ctx context.Context, job string, ev jobs.Event, quiet bool) error {
	var before jobs.Job
	var applied bool
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		j, err := e.jobs.Get(tx, job)
		if err != nil {
			return err
		}
		if _, err = e.jobs.Apply(tx, job, j.Version, ev); err != nil {
			if quiet && errors.Is(err, jobs.ErrInvalidTransition) {
				return nil
			}
			return err
		}
		before, applied = j, true
		return nil
	})
	if err == nil && applied {
		e.stop(before) // the attempt the event replaced must not keep running
		e.changed(before.Batch)
	}
	return err
}

func (e *Engine) each(ctx context.Context, batch string, f func(j jobs.Job) error) error {
	ms, err := e.members(ctx, batch)
	if err != nil {
		return ErrNotFound
	}
	for _, j := range ms {
		if err := f(j); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Pause(ctx context.Context, batch string) error {
	return e.each(ctx, batch, func(j jobs.Job) error {
		return e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvPause}, true)
	})
}

// stop asks the scheduler to give up the attempt a task had running.
func (e *Engine) stop(j jobs.Job) {
	if j.AttemptID != "" {
		e.disp.Cancel(kernel.AttemptRef{JobID: j.ID, AttemptID: j.AttemptID, OwnerGeneration: e.ws.Generation(), JobVersion: j.Version})
	}
}

func (e *Engine) Resume(ctx context.Context, batch string) error {
	err := e.each(ctx, batch, func(j jobs.Job) error {
		if err := e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvResume}, true); err != nil {
			return err
		}
		// a file resumed where nothing runs any more (inspected but not yet joined to a started batch, or
		// ready to be published) is moved on from here
		var now jobs.Job
		if e.ws.Commit(ctx, func(tx store.Tx) (err error) { now, err = e.jobs.Get(tx, j.ID); return }) == nil {
			e.afterState(ctx, now)
		}
		return nil
	})
	if err == nil {
		e.finishPendingStart(batch)
	}
	return err
}

func (e *Engine) Cancel(ctx context.Context, batch string) error {
	defer e.ws.Commit(ctx, func(tx store.Tx) error { return e.jobs.FinishBatch(tx, batch) })
	return e.each(ctx, batch, func(j jobs.Job) error {
		if j.Position.State.Terminal() {
			return nil
		}
		if err := e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvCancel}, true); err != nil {
			return err
		}
		if j.AttemptID == "" {
			return e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvCancelDone}, true)
		}
		return nil
	})
}

// Retry re-runs failed files once and releases files blocked by a resource or
// target condition; files that need a password or a changed input are left alone.
func (e *Engine) Retry(ctx context.Context, batch string, only []string) error {
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	return e.each(ctx, batch, func(j jobs.Job) error {
		if len(want) > 0 && !want[j.ID] {
			return nil
		}
		switch j.Position.State {
		case jobs.Failed:
			return e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvReprocess}, true)
		case jobs.Blocked:
			return e.apply(ctx, j.ID, jobs.Event{Kind: jobs.EvUnblock}, true)
		}
		return nil
	})
}

// Clean removes everything the workspace holds for a finished batch's files.
func (e *Engine) Clean(ctx context.Context, batch string) error {
	ms, err := e.members(ctx, batch)
	if err != nil {
		return ErrNotFound
	}
	for _, j := range ms {
		if !j.Position.State.Terminal() {
			return fmt.Errorf("%w: batch has unfinished files", ErrNotAllowed)
		}
	}
	for _, j := range ms {
		switch err := e.ret.Purge(ctx, j.ID); {
		case err == nil:
		case errors.Is(err, retention.ErrPublishing):
			return fmt.Errorf("%w: a file is being published", ErrNotAllowed)
		default:
			return err
		}
	}
	return nil
}

func (e *Engine) takeSecret(job string) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	pw := e.secrets[job]
	delete(e.secrets, job)
	return pw
}

// Unlock moves a locked file back to its preflight with the password in memory.
func (e *Engine) Unlock(ctx context.Context, job string, password []byte) error {
	defer keys.Zero(password)
	if len(password) == 0 || len(password) > 1024 {
		return ErrInvalid
	}
	var waiting bool
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		j, err := e.jobs.Get(tx, job)
		waiting = j.Position.State == jobs.Locked || (j.Position.State == jobs.Blocked && j.Position.Block == jobs.BlockPassword)
		return err
	}); err != nil {
		return ErrNotFound
	}
	if !waiting {
		return fmt.Errorf("%w: file is not waiting for a password", ErrNotAllowed)
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrClosed
	}
	e.secrets[job] = append([]byte(nil), password...)
	e.mu.Unlock()
	if err := e.apply(ctx, job, jobs.Event{Kind: jobs.EvUnlocked}, false); err != nil {
		keys.Zero(e.takeSecret(job))
		return err
	}
	return nil
}

// Subscribe registers fn for changes of any batch (state, findings, decisions).
func (e *Engine) Subscribe(fn func(batch string)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listenSeq++
	id := e.listenSeq
	e.listeners[id] = fn
	return func() {
		e.mu.Lock()
		delete(e.listeners, id)
		e.mu.Unlock()
	}
}

func (e *Engine) changed(batch string) {
	e.mu.Lock()
	fns := make([]func(string), 0, len(e.listeners))
	for _, f := range e.listeners {
		fns = append(fns, f)
	}
	e.mu.Unlock()
	for _, f := range fns {
		f(batch)
	}
}

func (e *Engine) batchOf(ctx context.Context, job string) string {
	var b string
	e.ws.Commit(ctx, func(tx store.Tx) error { m, _ := getMember(tx, job); b = m.Batch; return nil })
	return b
}

// utf16Offsets converts rune offsets of text into UTF-16 code unit offsets.
func utf16Offsets(text string, offsets ...int) []int {
	out := make([]int, len(offsets))
	units, runes := 0, 0
	idx := make([]int, len(offsets))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return offsets[idx[a]] < offsets[idx[b]] })
	k := 0
	for _, r := range text {
		for k < len(idx) && offsets[idx[k]] == runes {
			out[idx[k]] = units
			k++
		}
		runes++
		units++
		if r > 0xFFFF {
			units++
		}
	}
	for ; k < len(idx); k++ {
		out[idx[k]] = units
	}
	return out
}

func (e *Engine) Document(ctx context.Context, job string, after, limit int) ([]DocUnit, error) {
	if limit <= 0 || limit > 100 || after < 0 {
		return nil, ErrInvalid
	}
	bodies, err := e.bodies(job)
	if err != nil {
		return nil, ErrNotFound
	}
	var out []DocUnit
	err = e.ws.Commit(ctx, func(tx store.Tx) error {
		attempt, ok, err := e.units.Sealed(tx, job)
		if err != nil || !ok {
			return err
		}
		us, err := e.units.Units(tx, attempt, after, limit)
		if err != nil {
			return err
		}
		ids := make([]string, len(us))
		for i, u := range us {
			ids[i] = u.UnitID
		}
		var fs []findings.Finding
		for from := int64(0); len(ids) > 0; {
			page, err := e.fr.Page(tx, findings.Filter{Job: job, Units: ids}, from, findings.MaxPage)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			fs, from = append(fs, page...), page[len(page)-1].ID
		}
		eff, err := review.Effective(tx, fs)
		if err != nil {
			return err
		}
		byUnit := map[string][]findings.Finding{}
		for _, f := range fs {
			byUnit[f.Unit] = append(byUnit[f.Unit], f)
		}
		for _, u := range us {
			text, err := bodies.Get(u.Body)
			if err != nil {
				return err
			}
			d := DocUnit{Seq: u.Seq, Part: u.Part, Ordinal: u.Ordinal, Visibility: u.Visibility, Text: text, Findings: []DocFinding{}}
			if u.Location != (extract.Location{}) {
				loc := u.Location
				d.Location = &loc
			}
			for _, f := range byUnit[u.UnitID] {
				o := utf16Offsets(text, f.Start, f.End)
				x := DocFinding{ID: f.ID, Start: o[0], End: o[1], Category: f.Category, Source: string(f.Source), Score: f.Score, Low: f.LowConf}
				if a, ok := eff[f.ID]; ok {
					x.Decision = "redact"
					if a.Keep {
						x.Decision = "keep"
					}
				}
				d.Findings = append(d.Findings, x)
			}
			out = append(out, d)
		}
		return nil
	})
	return out, err
}

// Publish records the publish authorization of a batch and releases the files
// that are already waiting for it; later arrivals publish themselves.
func (e *Engine) Publish(ctx context.Context, batch string, partial bool, outputDir string) error {
	if outputDir != "" && !isAbsDir(outputDir) {
		return ErrInvalid
	}
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		run, err := getRun(tx, batch)
		if err != nil {
			return ErrNotFound
		}
		run.Grant, run.Partial = true, run.Partial || partial
		if outputDir != "" {
			run.OutputDir = outputDir
		}
		return putRun(tx, run)
	}); err != nil {
		return err
	}
	ms, err := e.members(ctx, batch)
	if err != nil {
		return ErrNotFound
	}
	for _, j := range ms {
		if j.Position.State == jobs.ReadyToPublish {
			e.afterState(ctx, j)
		}
	}
	e.changed(batch)
	return nil
}

// DecideFindings records the decisions one by one under their own request ids and
// resolves the file once afterwards.
func (e *Engine) DecideFindings(ctx context.Context, job string, ds []FindingDecision) error {
	if len(ds) == 0 || len(ds) > 100000 {
		return ErrInvalid
	}
	var batch string
	var n int
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		m, err := getMember(tx, job)
		batch = m.Batch
		if err != nil {
			return err
		}
		n, err = next(tx, "decision")
		return err
	}); err != nil {
		return ErrNotFound
	}
	for i, d := range ds {
		if _, err := e.revs.DecideItem(ctx, review.ItemRequest{Request: fmt.Sprintf("d%d_%d", n, i), Batch: batch, Job: job, Finding: d.ID, Keep: d.Keep}); err != nil {
			return err
		}
	}
	return e.apply(ctx, job, jobs.Event{Kind: jobs.EvDecisionsChange}, true)
}

// utf16ToRune converts a UTF-16 offset of text into a rune offset (-1 when it
// falls inside a surrogate pair or beyond the end).
func utf16ToRune(text string, off int) int {
	units, runes := 0, 0
	for _, r := range text {
		if units == off {
			return runes
		}
		if units > off {
			return -1
		}
		units++
		if r > 0xFFFF {
			units++
		}
		runes++
	}
	if units == off {
		return runes
	}
	return -1
}

func (e *Engine) AddManual(ctx context.Context, job string, seq, start, end int, category string) (int64, error) {
	if seq < 1 || start < 0 || end <= start || category == "" || len(category) > 32 {
		return 0, ErrInvalid
	}
	bodies, err := e.bodies(job)
	if err != nil {
		return 0, ErrNotFound
	}
	var batch string
	var unit extract.Unit
	var text string
	if err = e.ws.Commit(ctx, func(tx store.Tx) error {
		m, err := getMember(tx, job)
		if err != nil {
			return err
		}
		batch = m.Batch
		attempt, ok, err := e.units.Sealed(tx, job)
		if err != nil || !ok {
			return ErrNotReady
		}
		us, err := e.units.Units(tx, attempt, seq-1, 1)
		if err != nil || len(us) == 0 || us[0].Seq != seq {
			return ErrInvalid
		}
		unit = us[0]
		text, err = bodies.Get(unit.Body)
		return err
	}); err != nil {
		return 0, err
	}
	rs, re := utf16ToRune(text, start), utf16ToRune(text, end)
	if rs < 0 || re <= rs {
		return 0, ErrInvalid
	}
	var n int
	e.ws.Commit(ctx, func(tx store.Tx) error { n, _ = next(tx, "manual"); return nil })
	id, _, err := e.revs.AddManual(ctx, review.ManualRequest{Request: fmt.Sprintf("m%d", n), Batch: batch, Job: job, Unit: unit.UnitID, Version: 1,
		Start: rs, End: re, Category: category, Value: []byte(string([]rune(text)[rs:re]))})
	if err != nil {
		return 0, err
	}
	return id, e.apply(ctx, job, jobs.Event{Kind: jobs.EvDecisionsChange}, true)
}

// Unfinished lists the batches that are not finished, parked ones included, as a status each.
func (e *Engine) Unfinished(ctx context.Context) ([]BatchStatus, error) {
	var ids []string
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		bs, err := e.jobs.Unfinished(tx)
		for _, b := range bs {
			ids = append(ids, b.ID)
		}
		return err
	}); err != nil {
		return nil, err
	}
	var out []BatchStatus
	for _, id := range ids {
		st, err := e.Status(ctx, id, 200)
		if errors.Is(err, ErrNotFound) {
			continue // a draft without members cannot be resumed
		}
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// recordStart stores the start that is asked for, unless the batch is already started. It does not depend on
// the caller's context ending: the record is what survives an interrupt.
func (e *Engine) recordStart(ctx context.Context, r StartRequest) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return mapErr(e.ws.Commit(context.WithoutCancel(ctx), func(tx store.Tx) error {
		run, err := getRun(tx, r.Batch)
		if err != nil {
			return ErrNotFound
		}
		if _, err := e.jobs.SnapshotVersion(tx, r.Batch); err == nil {
			return nil // started already
		}
		run.Pending = string(raw)
		return putRun(tx, run)
	}))
}

func (e *Engine) startPending(ctx context.Context, batch string) bool {
	var p bool
	e.ws.Commit(ctx, func(tx store.Tx) error { r, err := getRun(tx, batch); p = err == nil && r.Pending != ""; return nil })
	return p
}

// finishPendingStart carries out a start that was interrupted while the files were being inspected.
func (e *Engine) finishPendingStart(batch string) {
	var run Run
	var started bool
	e.ws.Commit(context.Background(), func(tx store.Tx) error {
		run, _ = getRun(tx, batch)
		_, err := e.jobs.SnapshotVersion(tx, batch)
		started = err == nil
		return nil
	})
	if run.Pending == "" || started {
		return
	}
	var r StartRequest
	if json.Unmarshal([]byte(run.Pending), &r) != nil {
		return
	}
	go func() {
		if err := e.Start(context.Background(), r); err != nil {
			// it cannot be carried out (the files were refused, say): the request is dropped, so that nothing
			// waits for it, and what the files are in shows
			e.ws.Commit(context.Background(), func(tx store.Tx) error {
				run, err := getRun(tx, batch)
				if err != nil {
					return nil
				}
				run.Pending = ""
				return putRun(tx, run)
			})
			e.changed(batch)
		}
	}()
}

// strictItem is the one confirmation strict mode asks of each file (policy step 9).
func strictItem(job string) string {
	return exceptions.ItemIDFor(job, exceptions.StrictConfirm, "file")
}

func awaitingConfirm(tx store.Tx, job string) bool {
	rows, err := (exceptions.Repository{}).Items(tx, exceptions.Filter{Jobs: []string{job}, Type: exceptions.StrictConfirm, OnlyUndecided: true}, "", 1)
	return err == nil && len(rows) > 0
}

// ConfirmFile records the confirmation of one file of a strict batch: what was decided for it is to be
// exported. A file that does not wait for one (not strict, not yet resolved, confirmed already) is refused.
// Decisions taken on the file afterwards do not withdraw it; the plan follows them as usual.
func (e *Engine) ConfirmFile(ctx context.Context, job string) error {
	var n int
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		if _, err := getMember(tx, job); err != nil {
			return ErrNotFound
		}
		if !awaitingConfirm(tx, job) { // a decided item could be decided again; a confirmation is given once
			return ErrNotAllowed
		}
		var err error
		if n, err = next(tx, "decision"); err != nil {
			return err
		}
		if _, err = (exceptions.Repository{}).Decide(tx, fmt.Sprintf("c%d", n), []string{job}, []string{strictItem(job)}, exceptions.ConfirmExport,
			func(exceptions.Row) exceptions.Context { return exceptions.Context{} }); err != nil {
			return ErrNotAllowed
		}
		return nil
	})
	if err != nil {
		return err
	}
	return e.apply(ctx, job, jobs.Event{Kind: jobs.EvDecisionsChange}, true)
}

// Skip leaves a waiting file out: one that failed, waits for review or confirmation, is blocked or waits for
// its password. A file that is being worked on or has ended is refused. No password is kept for a file in
// these states (Unlock hands it straight to the preflight that takes it). The batch ends when this was its
// last unsettled file.
func (e *Engine) Skip(ctx context.Context, job string) error {
	var j jobs.Job
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		cur, err := e.jobs.Get(tx, job)
		if err != nil {
			return ErrNotFound
		}
		if j, err = e.jobs.Apply(tx, job, cur.Version, jobs.Event{Kind: jobs.EvSkip}); errors.Is(err, jobs.ErrInvalidTransition) {
			return ErrNotAllowed
		}
		return err
	})
	if err != nil {
		return err
	}
	e.afterState(ctx, j)
	e.changed(j.Batch)
	return nil
}

// WithdrawManual is Core: the manual finding and its decision are taken back in one commit, then the file
// is redone from resolution, as after any change of decision.
func (e *Engine) WithdrawManual(ctx context.Context, job string, finding int64) error {
	var n int
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		if _, err := getMember(tx, job); err != nil {
			return ErrNotFound
		}
		var err error
		n, err = next(tx, "withdraw")
		return err
	}); err != nil {
		return err
	}
	if err := e.revs.WithdrawManual(ctx, fmt.Sprintf("w%d", n), job, finding); err != nil {
		if errors.Is(err, review.ErrInvalid) {
			return ErrNotAllowed
		}
		return err
	}
	return e.apply(ctx, job, jobs.Event{Kind: jobs.EvDecisionsChange}, true)
}
