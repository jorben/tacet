package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/policy"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/validate"
)

type detectCache struct{ svc *detect.Service }

type nopLoader struct{}

func (nopLoader) Load(context.Context, kernel.ArtifactRef) (detect.Unit, error) {
	return detect.Unit{}, errors.New("not used")
}

// detector builds (once per rule set digest) the rules-only detection service of a snapshot.
func (e *Engine) detector(c snapshot.Content) (*detect.Service, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d, ok := e.detect[c.Detect.RuleSetDigest]; ok {
		return d.svc, nil
	}
	svc, err := e.buildDetector(c, nil)
	if err != nil {
		return nil, err
	}
	e.detect[c.Detect.RuleSetDigest] = &detectCache{svc}
	return svc, nil
}

func (e *Engine) buildDetector(c snapshot.Content, model detect.ModelRunner) (*detect.Service, error) {
	var rc config.RulesContent
	if err := jsonUnmarshal(c.Detect.RuleContent, &rc); err != nil {
		return nil, err
	}
	enabled := rc.Enabled()
	rs := make([]rules.Rule, len(enabled))
	for i, r := range enabled {
		rs[i] = rules.Rule{ID: r.ID, Category: r.Category, Pattern: r.Pattern, Terms: r.Terms, Boundary: r.Boundary, Validator: r.Validator,
			Suspect: r.Suspect, FoldCase: r.FoldCase, Context: r.Context, ContextWindow: r.Window}
	}
	return detect.New(detect.Config{Loader: nopLoader{}, Rules: rs, Model: model})
}

// modelRunner starts the detection plugin for one stage run. The model is loaded once per run and a
// worker never outlives it; one model worker runs at a time. The returned release ends the worker.
// id and exec are the model and build the snapshot froze (exec nil: the profile's own); a frozen build that
// is gone fails the stage, it is never replaced.
func (e *Engine) modelRunner(ctx context.Context, a kernel.AttemptRef, id string, exec *snapshot.ModelExecution) (detect.ModelRunner, func(), error) {
	m := e.modelByID(id)
	if m == nil {
		return nil, nil, errors.New("MODEL_UNAVAILABLE")
	}
	args, timeout, err := e.modelArgs(ctx, m, exec)
	if err != nil {
		return nil, nil, err
	}
	if err = e.takeModelSlot(ctx); err != nil {
		return nil, nil, err
	}
	w, err := e.launchWithin(ctx, m.plug, args, nil, timeout)
	if err != nil {
		<-e.modelSem
		return nil, nil, errors.New("MODEL_START_FAILED")
	}
	requested := "CPUExecutionProvider"
	if exec != nil {
		requested = exec.Backend
	}
	e.recordBackend(m, requested, w.Session)
	e.changed(e.batchOf(ctx, a.JobID))
	release := func() { w.Terminate(time.Second); <-e.modelSem }
	var n atomic.Uint64
	pm, err := detect.NewPluginModel(w.Session, func() kernel.AttemptRef {
		x := a
		x.AttemptID = fmt.Sprintf("%s_m%d", a.AttemptID, n.Add(1))
		return x
	})
	if err != nil {
		release()
		return nil, nil, err
	}
	return pm, release, nil
}

var launchSeq atomic.Uint64

var (
	errSpawn     = errors.New("PLUGIN_START_FAILED")
	errHandshake = errors.New("PLUGIN_HANDSHAKE_FAILED")
	// errHandshakeTimeout: the worker did not answer the handshake in time; errExitedEarly: it ended during it
	errHandshakeTimeout = errors.New("PLUGIN_HANDSHAKE_TIMEOUT")
	errExitedEarly      = errors.New("PLUGIN_EXITED_EARLY")
	errKilledEarly      = errors.New("PLUGIN_KILLED_EARLY")

	// handshakeTimeout bounds the start of a worker; tests shorten it.
	handshakeTimeout = 60 * time.Second
)

// launchFailed is the outcome of a stage whose worker could not be started. A stage that was told to stop
// (pause, cancel, shutdown) while its worker was starting did not fail: it was stopped. The two ways a
// start can fail have their own codes, so a failed file says which.
func launchFailed(ctx context.Context, err error) sched.StageOutcome {
	switch {
	case ctx.Err() != nil:
		return sched.StageOutcome{Kind: sched.Cancelled}
	case errors.Is(err, errHandshakeTimeout):
		return failed("PLUGIN_HANDSHAKE_TIMEOUT")
	case errors.Is(err, errKilledEarly):
		return failed("PLUGIN_KILLED_EARLY")
	case errors.Is(err, errExitedEarly):
		return failed("PLUGIN_EXITED_EARLY")
	case errors.Is(err, errHandshake):
		return failed("PLUGIN_HANDSHAKE_FAILED")
	}
	return failed("PLUGIN_START_FAILED")
}

// marker tags one worker and its descendants. Terminate reaps by this value
// across the whole machine, so it must be unique per workspace, ownership
// generation and launch: two engines (or two workspaces) never share a marker.
func (e *Engine) marker() string { return markerFor(e.opt.Dir, e.ws.Generation(), launchSeq.Add(1)) }

func markerFor(dir string, generation, seq uint64) string {
	return fmt.Sprintf("%sg%d_%d", workspacePrefix(dir), generation, seq)
}

func workspacePrefix(dir string) string { return "tacet_" + hexSum([]byte(dir))[:10] + "_" }

// launch starts one plugin worker with the given inherited files and initialises
// its session. The caller terminates it.
func (e *Engine) launch(ctx context.Context, p plugin, files []*os.File) (*host.Worker, error) {
	return e.launchWith(ctx, p, nil, files)
}

// launchWith adds launch arguments (the detection plugin's model paths): they are arguments, never protocol data.
func (e *Engine) launchWith(ctx context.Context, p plugin, args []string, files []*os.File) (*host.Worker, error) {
	return e.launchWithin(ctx, p, args, files, handshakeTimeout)
}

// launchWithin starts a worker whose handshake may take up to timeout (a model build that compiles first).
func (e *Engine) launchWithin(ctx context.Context, p plugin, args []string, files []*os.File, timeout time.Duration) (*host.Worker, error) {
	w, err := e.spawn(p, args, files)
	if err != nil {
		return nil, err
	}
	if err = e.handshake(ctx, w, p, timeout); err != nil {
		return nil, err
	}
	return w, nil
}

// spawn starts the process of one worker of p, marked as this run's.
func (e *Engine) spawn(p plugin, args []string, files []*os.File) (*host.Worker, error) {
	spec := host.Spec{Path: e.opt.Python, Args: append([]string{"-I", "-m", p.module}, args...), Dir: os.TempDir(), ExtraFiles: files,
		Env:        []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"},
		Generation: e.marker()}
	w, err := host.Start(spec)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSpawn, err)
	}
	return w, nil
}

// handshake initialises a started worker within timeout; one that does not answer is ended.
func (e *Engine) handshake(ctx context.Context, w *host.Worker, p plugin, timeout time.Duration) error {
	ictx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := w.Session.Initialize(ictx, p.id.ID, p.id.Version, e.ws.Generation()); err != nil {
		// which of the three it was is all that is said: the worker's diagnostics are discarded (they may
		// hold document values), so the reason code is what makes an occasional failure traceable
		cause := errHandshake
		switch {
		case errors.Is(err, host.ErrHandshakeMismatch):
			// it answered, as something else; it ends because the host closed the session
		case ictx.Err() != nil && ctx.Err() == nil:
			cause = errHandshakeTimeout
		default:
			select {
			case <-w.Done(): // its pipe closed because it ended
				cause = errExitedEarly
				if w.Signalled() {
					cause = errKilledEarly // something ended it: the system, or a reclaim
				}
			case <-time.After(200 * time.Millisecond):
			}
		}
		w.Terminate(time.Second)
		return fmt.Errorf("%w: %v", cause, err)
	}
	return nil
}

func (e *Engine) pluginFor(format string) (plugin, bool) {
	p, ok := e.plugins[format]
	return p, ok
}

// build creates the services that depend on the open workspace.
func (e *Engine) build() error {
	var err error
	e.revs = &review.Service{Commit: e.ws.Commit, Findings: e.fr, Chunk: 200}
	e.pol = &policy.Service{Commit: e.ws.Commit, Findings: e.fr}
	e.pub = &publish.Service{Commit: e.ws.Commit, Admit: e.ws.AdmitTx, CurrentKey: e.currentKey}
	w, err := export.NewWriter()
	if err != nil {
		return err
	}
	e.exp = &export.Service{WS: e.ws, Refs: e.refs, W: w}
	if e.runner, err = validate.NewRunner(); err != nil {
		return err
	}
	budget := e.opt.Budget
	if budget.MemoryBytes == 0 {
		budget = budgetFor(e.tier(context.Background())) // the tier chosen in the settings, as of this start
	}
	e.budget = budget
	if err = e.buildRetention(); err != nil {
		return err
	}
	e.queue, err = sched.NewQueue(budget, e.gate, e.runners()...)
	if err != nil {
		return err
	}
	e.disp = sched.NewDispatcher(e.queue)
	return nil
}

// markerPrefix starts every worker marker of this workspace: markers are unique per workspace, so a
// prefix names exactly this workspace's workers and no one else's.
func (e *Engine) markerPrefix() string { return workspacePrefix(e.opt.Dir) }

// reapPrevious is the second step: worker processes an earlier run left behind (a crash, a kill) are gone
// before anything is recovered. The workspace lock is ours and no worker of this run exists yet.
func (e *Engine) reapPrevious(context.Context) error {
	_, err := e.reapWorkers()
	return err
}

// reapWorkers ends every worker process of this workspace and tells how many it ended.
func (e *Engine) reapWorkers() (int, error) { return platform.ReapMarkedPrefix(e.markerPrefix()) }

// settlePublish is the third step: a publish that was interrupted between its steps is settled from what is
// on disk (delivered, in conflict, or not started) before any task can run again.
func (e *Engine) settlePublish(ctx context.Context) error {
	_, err := e.pub.Recover(ctx)
	return err
}

// recoverAll is the fifth step: decisions that never reached "committed" are dropped and every unfinished
// file is reset to the start of its interrupted stage and paused.
func (e *Engine) recoverAll(ctx context.Context) error {
	if _, err := e.revs.DropPending(ctx); err != nil {
		return err
	}
	return e.recoverJobs(ctx)
}

// startDispatch is the last step: the scheduler runs and task operations open.
func (e *Engine) startDispatch(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel, e.life = cancel, ctx
	e.startProbe(ctx) // before any task can ask for the model slot, so every one of them can stop it
	go func() {
		defer close(e.done)
		e.disp.Run(ctx, e.complete)
	}()
	return nil
}

// dispatch is jobs.Dispatch: begin the stage durably, then hand it to the scheduler. A notification runs on its own
// and may land long after it was sent: one that no longer applies is dropped, and what moved the file since sent its
// own (BeginStage refuses the wrong stage, see below for the authorization).
func (e *Engine) dispatch(id string, stage kernel.StageKind) {
	done := make(chan struct{})
	e.nextStage.Store(id, done)
	go func() {
		defer func() {
			e.nextStage.CompareAndDelete(id, done)
			close(done)
		}()
		if e.beforeDispatch != nil {
			e.beforeDispatch(stage)
		}
		e.mu.Lock()
		closed := e.closed
		e.mu.Unlock()
		if closed || e.queue == nil {
			return
		}
		ctx := context.Background()
		var view sched.JobView
		// Begun and queued are one step to whoever stops the attempt: see stop.
		e.handOver.RLock()
		err := e.ws.Commit(ctx, func(tx store.Tx) error {
			j, err := e.jobs.Get(tx, id)
			if err != nil {
				return err
			}
			if j.AttemptID != "" {
				return errDuplicate // an earlier notification already began this stage
			}
			run, err := getRun(tx, j.Batch)
			if err != nil {
				return err
			}
			// A file that is ready leaves for publishing only on its authorization, which a decision may have taken
			// back since this was sent. Nothing is lost: Publish sends another. A file past this point, resuming
			// its publish, has no other notification coming and is checked by the publish itself.
			if j.Position.State == jobs.ReadyToPublish && !run.Grant {
				return jobs.ErrStaleDispatch
			}
			a, err := e.jobs.BeginStage(tx, id, stage, j.Version, e.ws.Generation())
			view = sched.JobView{Attempt: a, Stage: stage, Priority: stagePriority(run.Kind, j.Position), Group: j.Batch}
			return err
		})
		if err == nil {
			if e.beforeSubmit != nil {
				e.beforeSubmit()
			}
			err = e.queue.Submit(view)
		}
		e.handOver.RUnlock()
		// A dropped notification must not get as far as fail: by then the file may have begun the stage it is really
		// at, and fail would end that attempt.
		if err != nil && !errors.Is(err, errDuplicate) && !errors.Is(err, jobs.ErrStaleDispatch) {
			e.fail(id, stage, err)
		}
	}()
}

// stagePriority is what a stage is scheduled as (architecture 5.1): a person waits for a quick task and for a file
// they just decided on; the rest of a batch is background work.
func stagePriority(kind string, position jobs.Snapshot) kernel.Priority {
	if kind == "quick" || position.Reviewed {
		return kernel.Interactive
	}
	return kernel.Background
}

// fail records a dispatch that could not even start as a failed stage.
func (e *Engine) fail(id string, stage kernel.StageKind, cause error) {
	if e.dispatchFailed != nil {
		e.dispatchFailed()
	}
	var a kernel.AttemptRef
	e.ws.Commit(context.Background(), func(tx store.Tx) error {
		j, err := e.jobs.Get(tx, id)
		if err != nil || j.AttemptID == "" {
			return err
		}
		a = kernel.AttemptRef{JobID: id, AttemptID: j.AttemptID, OwnerGeneration: e.ws.Generation(), JobVersion: j.Version}
		return nil
	})
	if a.AttemptID != "" {
		e.complete(sched.JobView{Attempt: a, Stage: stage}, sched.StageOutcome{Kind: sched.Failed, Err: &kernel.ErrorCode{Code: "DISPATCH_FAILED"}})
	}
}

// complete is the dispatcher's completion callback: persist the outcome through
// workspace admission, then run the app-owned follow-ups.
func (e *Engine) complete(view sched.JobView, out sched.StageOutcome) error {
	ctx := context.Background()
	var job jobs.Job
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		if err := e.ws.AdmitTx(tx, view.Attempt); err != nil {
			return err
		}
		out, err := e.deliveryBehind(tx, view.Attempt.JobID, out)
		if err != nil {
			return err
		}
		job, err = e.jobs.CompleteStage(tx, view.Attempt, out)
		if err != nil {
			return err
		}
		// the reason code is what the front ends show next to a stopped file
		if out.Err != nil || out.Detail != "" {
			m, merr := getMember(tx, view.Attempt.JobID)
			if merr != nil {
				return merr
			}
			m.Detail = out.Detail
			if out.Err != nil {
				m.Detail = out.Err.Code
			}
			return putMember(tx, m)
		}
		return nil
	})
	if err != nil {
		// A late or replaced attempt: nothing to persist. If a cancel replaced it, this was what it waited for.
		id := view.Attempt.JobID
		e.settleCancelled(ctx, e.batchOf(ctx, id), id)
		return nil
	}
	e.afterState(ctx, job)
	e.changed(job.Batch)
	// A person waits for this file: its next stage is queued before the dispatcher, which is what runs this, takes
	// another, so no background stage comes between its stages (architecture 5.1).
	if view.Priority == kernel.Interactive {
		if next, ok := e.nextStage.Load(view.Attempt.JobID); ok {
			<-next.(chan struct{})
		}
	}
	return nil
}

// deliveryBehind holds a stage outcome to what makes a file delivered: a publish stage is only done if a delivery is on
// record. Whatever else reports it done, the file fails: reading as delivered with no document is the one thing the
// state of a file must never say. The record is the publish module's, so the jobs state machine cannot check this.
func (e *Engine) deliveryBehind(tx store.Tx, job string, out sched.StageOutcome) (sched.StageOutcome, error) {
	if out.Kind != sched.Completed {
		return out, nil
	}
	j, err := e.jobs.Get(tx, job)
	if err != nil || j.Position.State != jobs.Publishing {
		return out, err
	}
	ds, _, err := publish.Deliveries(tx, job)
	if err != nil {
		return out, err
	}
	if len(ds) == 0 {
		return failed("DELIVERY_MISSING"), nil
	}
	return out, nil
}

var (
	errAbort     = errors.New("abort")
	errDuplicate = errors.New("duplicate dispatch")
)

// currentKey is the job's disposition key inside tx.
func (e *Engine) currentKey(tx store.Tx, job string) (string, error) {
	st, ok, err := policy.Resolution(tx, job)
	if err != nil || !ok {
		return "", errAbort
	}
	return st.Key, nil
}

func (e *Engine) createKeys(batch string, jobIDs ...string) error {
	if _, err := e.ws.Keys().Create(keys.Batch, batch); err != nil && !errors.Is(err, keys.ErrKeyExists) {
		return err
	}
	for _, id := range jobIDs {
		if _, err := e.ws.Keys().Create(keys.Task, id); err != nil && !errors.Is(err, keys.ErrKeyExists) {
			return err
		}
	}
	return nil
}

func (e *Engine) bodies(job string) (extract.FileBodies, error) {
	return extract.JobBodies(filepath.Join(e.opt.Dir, "bodies"), job, func(j, id string) ([]byte, error) {
		tk, err := e.taskKey(j)
		if err != nil {
			return nil, err
		}
		defer keys.Zero(tk)
		return keys.ResourceKey(tk, id)
	})
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// recoverJobs runs at open: every unfinished member of an unfinished run is reset
// to the start of its interrupted stage and left paused, so nothing runs until the
// user resumes (module design 4.3 step 7).
func (e *Engine) recoverJobs(ctx context.Context) error {
	var ids []string
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		rows, err := t.Query(`SELECT m.job FROM app_member m JOIN app_run r ON r.batch=m.batch ORDER BY m.job`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, id := range ids {
		err := e.ws.Commit(ctx, func(tx store.Tx) error { _, err := e.jobs.RecoverMember(tx, id); return err })
		// an import that stopped between its transactions named this file and never added it: nothing to recover,
		// and it must not keep the workspace from closing or opening
		if err != nil && !errors.Is(err, jobs.ErrJobMissing) {
			return err
		}
	}
	return nil
}
