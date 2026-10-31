package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrJobMissing      = errors.New("JOB_NOT_FOUND")
	ErrVersionConflict = errors.New("VERSION_CONFLICT")
	ErrBatchActive     = errors.New("BATCH_ACTIVE")
	ErrQuickActive     = errors.New("QUICK_TASK_ACTIVE")
	ErrJobRecord       = errors.New("JOB_RECORD_INVALID")
	ErrLateOutcome     = errors.New("JOB_STALE_OUTCOME")
)

// Module registers only jobs-owned tables. Other entities (snapshots, findings,
// candidates, decisions and grants) remain in their owning repositories.
func Module() store.Module {
	return store.Module{Name: "jobs", Prefix: "jobs_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE jobs_batch(id TEXT PRIMARY KEY, kind TEXT NOT NULL, finished INTEGER NOT NULL DEFAULT 0, confirmed INTEGER NOT NULL DEFAULT 0)`,
		`CREATE UNIQUE INDEX jobs_one_active_kind ON jobs_batch(kind) WHERE finished=0`,
		`CREATE TABLE jobs_job(id TEXT PRIMARY KEY, batch TEXT NOT NULL REFERENCES jobs_batch(id), ordinal INTEGER NOT NULL, version INTEGER NOT NULL, attempt TEXT NOT NULL, position BLOB NOT NULL, UNIQUE(batch,ordinal))`,
		`CREATE INDEX jobs_batch_members ON jobs_job(batch,ordinal)`,
		`CREATE TABLE jobs_stage(attempt TEXT PRIMARY KEY, job TEXT NOT NULL REFERENCES jobs_job(id), stage TEXT NOT NULL, generation INTEGER NOT NULL, job_version INTEGER NOT NULL, status TEXT NOT NULL, artifacts BLOB NOT NULL)`,
		`CREATE INDEX jobs_running_stage ON jobs_stage(status,job)`,
	}}, {Version: 2, SQL: []string{
		`ALTER TABLE jobs_batch ADD COLUMN snapshot_version INTEGER NOT NULL DEFAULT 0`,
	}}, {Version: 3, SQL: []string{
		// output: the job version a completed run produced (lineage, INV-04);
		// reads: what the run consumed, as JSON (see StageReads).
		`ALTER TABLE jobs_stage ADD COLUMN output INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs_stage ADD COLUMN reads BLOB NOT NULL DEFAULT x'7b7d'`,
	}}, {Version: 4, SQL: []string{
		// One in-stage checkpoint per job and stage: a reference to what the
		// plugin saved, the committed sequence and what the run read.
		`CREATE TABLE jobs_checkpoint(job TEXT NOT NULL REFERENCES jobs_job(id), stage TEXT NOT NULL, attempt TEXT NOT NULL, seq INTEGER NOT NULL, ref BLOB NOT NULL, reads BLOB NOT NULL, PRIMARY KEY(job,stage))`,
	}}}}

}

// Job holds neutral identifiers and a state-machine snapshot, never text or
// filenames. Large artifacts stay references, not content.
type Job struct {
	ID        string
	Batch     string
	Ordinal   int
	Version   uint64
	AttemptID string
	Position  Snapshot
}

// Dispatch is a durable task/stage identifier notification after commit. It
// never bypasses scheduler budget admission or starts plugins itself.
type Dispatch func(jobID string, stage kernel.StageKind)

type Repository struct {
	dispatch Dispatch
	limits   Limits
}

func NewRepository(dispatch Dispatch, limits Limits) *Repository {
	return &Repository{dispatch: dispatch, limits: limits}
}

func transaction(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	scope, err := t.Module("jobs")
	return t, scope, err
}

func emit(tx store.Tx, id string, version uint64) {
	tx.Emit(store.Event{Type: "JobStateChanged", Subject: kernel.ArtifactRef{Kind: "job", ID: id, Version: version}})
}

func (r *Repository) notify(tx store.Tx, id string, stage kernel.StageKind) {
	if r.dispatch != nil && stage != kernel.StageNone {
		tx.AfterCommit(func() { r.dispatch(id, stage) })
	}
}

// CreateDraft permits one unfinished quick task and one unfinished regular
// batch, enforced by SQLite as well as by the readable error (FR-28).
func (r *Repository) CreateDraft(tx store.Tx, id string, kind BatchKind) error {
	if id == "" || len(id) > 128 || (kind != QuickBatch && kind != RegularBatch) {
		return ErrJobRecord
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	var count int
	if err = t.QueryRow("SELECT COUNT(*) FROM jobs_batch WHERE kind=? AND finished=0", string(kind)).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		if kind == QuickBatch {
			return ErrQuickActive
		}
		return ErrBatchActive
	}
	if _, err = scope.Exec("INSERT INTO jobs_batch(id,kind) VALUES(?,?)", id, string(kind)); err != nil {
		return err
	}
	tx.Emit(store.Event{Type: "BatchChanged", Subject: kernel.ArtifactRef{Kind: "batch", ID: id, Version: 1}})
	return nil
}

func (r *Repository) AddMember(tx store.Tx, batch, id string, ordinal int) error {
	if id == "" || len(id) > 128 || ordinal < 0 {
		return ErrJobRecord
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	var finished, confirmed int
	if err = t.QueryRow("SELECT finished,confirmed FROM jobs_batch WHERE id=?", batch).Scan(&finished, &confirmed); err != nil || finished != 0 || confirmed != 0 {
		return ErrJobRecord
	}
	data, _ := json.Marshal(Snapshot{State: Preflighting})
	if _, err = scope.Exec("INSERT INTO jobs_job(id,batch,ordinal,version,attempt,position) VALUES(?,?,?,1,'',?)", id, batch, ordinal, data); err != nil {
		return err
	}
	emit(tx, id, 1)
	r.notify(tx, id, kernel.StagePreflight)
	return nil
}

func (r *Repository) Get(tx store.Tx, id string) (Job, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return Job{}, err
	}
	var job Job
	var data []byte
	err = t.QueryRow("SELECT id,batch,ordinal,version,attempt,position FROM jobs_job WHERE id=?", id).Scan(&job.ID, &job.Batch, &job.Ordinal, &job.Version, &job.AttemptID, &data)
	if err != nil {
		return Job{}, ErrJobMissing
	}
	if json.Unmarshal(data, &job.Position) != nil {
		return Job{}, ErrJobRecord
	}
	return job, nil
}

// Current is workspace.Versions, reading through the current transaction.
func (r *Repository) Current(tx store.Tx, id string) (string, uint64, bool) {
	job, err := r.Get(tx, id)
	return job.AttemptID, job.Version, err == nil && job.AttemptID != ""
}

func save(tx store.Tx, scope *store.Scope, job Job, expected uint64) error {
	data, err := json.Marshal(job.Position)
	if err != nil {
		return ErrJobRecord
	}
	result, err := scope.Exec("UPDATE jobs_job SET version=?,attempt=?,position=? WHERE id=? AND version=?", job.Version, job.AttemptID, data, job.ID, expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrVersionConflict
	}
	emit(tx, job.ID, job.Version)
	return nil
}

// Apply records a user/control event under optimistic version checking. The
// caller must run this in workspace.Commit. It never reuses an old attempt.
func (r *Repository) Apply(tx store.Tx, id string, expected uint64, event Event) (Job, error) {
	if event.Kind == EvStageOutcome || event.Kind == EvDispatch {
		return Job{}, ErrJobRecord
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return Job{}, err
	}
	job, err := r.Get(tx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Version != expected {
		return Job{}, ErrVersionConflict
	}
	if event.Kind == EvConfirm {
		if _, err = scope.Exec("UPDATE jobs_batch SET confirmed=1 WHERE id=?", job.Batch); err != nil {
			return Job{}, err
		}
	}
	next, err := Transition(job.Position, event, r.limits)
	if err != nil {
		return Job{}, err
	}
	if job.AttemptID != "" {
		if _, err = scope.Exec("UPDATE jobs_stage SET status='discarded' WHERE attempt=? AND status='running'", job.AttemptID); err != nil {
			return Job{}, err
		}
	}
	if next.Snapshot.State == Cancelled {
		if _, err = scope.Exec("DELETE FROM jobs_checkpoint WHERE job=?", id); err != nil {
			return Job{}, err
		}
	}
	job.Version++
	job.AttemptID = ""
	job.Position = next.Snapshot
	if err = save(tx, scope, job, expected); err != nil {
		return Job{}, err
	}
	dispatch := next.Dispatch
	if dispatch == kernel.StageNone && job.Position.State == Queued {
		dispatch = job.Position.Cursor
	}
	r.notify(tx, id, dispatch)
	return job, nil
}

// BeginStage durably registers an attempt before scheduler execution. A caller
// checks workspace ownership and resource admission before invoking it.
func (r *Repository) BeginStage(tx store.Tx, id string, expected, generation uint64) (kernel.AttemptRef, error) {
	if generation == 0 {
		return kernel.AttemptRef{}, ErrJobRecord
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return kernel.AttemptRef{}, err
	}
	job, err := r.Get(tx, id)
	if err != nil {
		return kernel.AttemptRef{}, err
	}
	if job.Version != expected || job.AttemptID != "" {
		return kernel.AttemptRef{}, ErrVersionConflict
	}
	position := job.Position
	if position.State == Queued || position.State == ReadyToPublish {
		next, err := Transition(position, Event{Kind: EvDispatch}, r.limits)
		if err != nil {
			return kernel.AttemptRef{}, err
		}
		position = next.Snapshot
	}
	stage, ok := stageOf[position.State]
	if !ok {
		return kernel.AttemptRef{}, ErrInvalidTransition
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return kernel.AttemptRef{}, ErrJobRecord
	}
	attemptID := "a_" + hex.EncodeToString(random[:])
	job.Version++
	job.AttemptID = attemptID
	job.Position = position
	attempt := kernel.AttemptRef{JobID: id, AttemptID: attemptID, OwnerGeneration: generation, JobVersion: job.Version}
	if _, err = scope.Exec("INSERT INTO jobs_stage(attempt,job,stage,generation,job_version,status,artifacts) VALUES(?,?,?,?,?,'running',?)", attemptID, id, string(stage), generation, job.Version, []byte("[]")); err != nil {
		return kernel.AttemptRef{}, err
	}
	if err = save(tx, scope, job, expected); err != nil {
		return kernel.AttemptRef{}, err
	}
	return attempt, nil
}

// CompleteStage writes the complete marker, state transition, outbox and next
// dispatch registration in one transaction (MD-10). Replay/late attempts are
// rejected without changing the job or producing duplicate events. The outer
// workspace.AdmitTx additionally verifies the current owner generation.
func (r *Repository) CompleteStage(tx store.Tx, a kernel.AttemptRef, out sched.StageOutcome) (Job, error) {
	t, scope, err := transaction(tx)
	if err != nil {
		return Job{}, err
	}
	job, err := r.Get(tx, a.JobID)
	if err != nil {
		return Job{}, err
	}
	if job.Version != a.JobVersion || job.AttemptID != a.AttemptID {
		return Job{}, ErrLateOutcome
	}
	var stage kernel.StageKind
	var gen, version uint64
	var status string
	if err = t.QueryRow("SELECT stage,generation,job_version,status FROM jobs_stage WHERE attempt=? AND job=?", a.AttemptID, a.JobID).Scan(&stage, &gen, &version, &status); err != nil {
		return Job{}, ErrLateOutcome
	}
	if gen != a.OwnerGeneration || version != a.JobVersion || status != "running" {
		return Job{}, ErrLateOutcome
	}
	next, err := Transition(job.Position, Event{Kind: EvStageOutcome, Stage: stage, Outcome: out}, r.limits)
	if err != nil {
		return Job{}, err
	}
	if next.Discard {
		return Job{}, ErrLateOutcome
	}
	artifacts, err := json.Marshal(out.Artifacts)
	if err != nil {
		return Job{}, ErrJobRecord
	}
	var produced uint64 // only a completed run counts as a result later stages may consume
	if out.Kind == sched.Completed {
		produced = job.Version + 1
	}
	if _, err = scope.Exec("UPDATE jobs_stage SET status=?,artifacts=?,output=? WHERE attempt=?", string(out.Kind), artifacts, produced, a.AttemptID); err != nil {
		return Job{}, err
	}
	if out.Kind == sched.Completed {
		// the stage has its result; a checkpoint of it would only be a stale way back in
		if _, err = scope.Exec("DELETE FROM jobs_checkpoint WHERE job=? AND stage=?", job.ID, string(stage)); err != nil {
			return Job{}, err
		}
	}
	previous := job.Version
	job.Version++
	job.AttemptID = ""
	job.Position = next.Snapshot
	if err = save(tx, scope, job, previous); err != nil {
		return Job{}, err
	}
	r.notify(tx, job.ID, next.Dispatch)
	return job, nil
}

// ConfirmBatch transitions the whole frozen membership in one transaction.
// app must freeze a compatible snapshot and obtain authorizations in this same
// workspace commit before calling it. A member still being probed rejects the
// command; the caller rolls back, so earlier members cannot start on half of a
// batch confirmation. Locked members become blocked; preflight rejections stay
// terminal and remain in the requested delivery scope.
func (r *Repository) ConfirmBatch(tx store.Tx, batch string) (int, error) {
	t, scope, err := transaction(tx)
	if err != nil {
		return 0, err
	}
	var finished, confirmed int
	if err = t.QueryRow("SELECT finished,confirmed FROM jobs_batch WHERE id=?", batch).Scan(&finished, &confirmed); err != nil || finished != 0 || confirmed != 0 {
		return 0, ErrJobRecord
	}
	after, count := -1, 0
	for {
		members, err := r.Members(tx, batch, after, 100)
		if err != nil {
			return 0, err
		}
		if len(members) == 0 {
			break
		}
		for _, job := range members {
			after = job.Ordinal
			count++
			if job.Position.State == Unprocessable {
				continue
			}
			if job.AttemptID != "" {
				return 0, ErrJobRecord
			}
			switch job.Position.State {
			case Preflighted, Locked:
			default:
				return 0, ErrInvalidTransition
			}
			next, err := Transition(job.Position, Event{Kind: EvConfirm}, r.limits)
			if err != nil {
				return 0, err
			}
			previous := job.Version
			job.Version++
			job.Position = next.Snapshot
			if err = save(tx, scope, job, previous); err != nil {
				return 0, err
			}
			if job.Position.State == Queued {
				r.notify(tx, job.ID, job.Position.Cursor)
			}
		}
	}
	if count == 0 {
		return 0, ErrJobRecord
	}
	if _, err = scope.Exec("UPDATE jobs_batch SET confirmed=1 WHERE id=?", batch); err != nil {
		return 0, err
	}
	tx.Emit(store.Event{Type: "BatchChanged", Subject: kernel.ArtifactRef{Kind: "batch", ID: batch, Version: 2}})
	return count, nil
}

// BindSnapshot pins the batch's exact snapshot version in the same transaction
// as Repository.Save and ConfirmBatch. Changing a confirmed batch later must be
// an explicit versioned command with lineage invalidation; configuration edits
// alone never change this reference.
func (r *Repository) BindSnapshot(tx store.Tx, batch string, expected, version uint64) error {
	if version == 0 || version <= expected {
		return ErrJobRecord
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	var current uint64
	if err = t.QueryRow("SELECT snapshot_version FROM jobs_batch WHERE id=? AND finished=0", batch).Scan(&current); err != nil {
		return ErrJobRecord
	}
	if current != expected {
		return ErrVersionConflict
	}
	result, err := scope.Exec("UPDATE jobs_batch SET snapshot_version=? WHERE id=? AND snapshot_version=? AND finished=0", version, batch, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrVersionConflict
	}
	tx.Emit(store.Event{Type: "BatchChanged", Subject: kernel.ArtifactRef{Kind: "batch", ID: batch, Version: version}})
	return nil
}

// SnapshotVersion is the frozen reference; jobs read this rather than assuming
// that the latest config/snapshot record belongs to their running execution.
func (r *Repository) SnapshotVersion(tx store.Tx, batch string) (uint64, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return 0, err
	}
	var version uint64
	if err = t.QueryRow("SELECT snapshot_version FROM jobs_batch WHERE id=?", batch).Scan(&version); err != nil {
		return 0, ErrJobRecord
	}
	if version == 0 {
		return 0, ErrJobRecord
	}
	return version, nil
}

// Members is a bounded keyset query; callers never load a whole batch's state
// into the UI or memory to make a page. Ordinals are stable within the batch.
func (r *Repository) Members(tx store.Tx, batch string, afterOrdinal, limit int) ([]Job, error) {
	if limit <= 0 || limit > 200 || afterOrdinal < -1 {
		return nil, ErrJobRecord
	}
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query("SELECT id,batch,ordinal,version,attempt,position FROM jobs_job WHERE batch=? AND ordinal>? ORDER BY ordinal LIMIT ?", batch, afterOrdinal, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []Job
	for rows.Next() {
		var job Job
		var data []byte
		if err := rows.Scan(&job.ID, &job.Batch, &job.Ordinal, &job.Version, &job.AttemptID, &data); err != nil {
			return nil, err
		}
		if json.Unmarshal(data, &job.Position) != nil {
			return nil, ErrJobRecord
		}
		members = append(members, job)
	}
	return members, rows.Err()
}

// RecoverMember abandons the interrupted attempt and persists a paused state.
// Lifecycle calls it page by page, after retention removed unauthorized data and
// the supervisor reclaimed old workers. No stage dispatch occurs on startup.
func (r *Repository) RecoverMember(tx store.Tx, id string) (Job, error) {
	_, scope, err := transaction(tx)
	if err != nil {
		return Job{}, err
	}
	job, err := r.Get(tx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Position.State.Terminal() || job.Position.State == Paused {
		return job, nil
	}
	if job.Position.State == Cancelling {
		job.Position.State = Cancelled
		if _, err = scope.Exec("DELETE FROM jobs_checkpoint WHERE job=?", id); err != nil {
			return Job{}, err
		}
	} else {
		job.Position.Resume = job.Position.State
		job.Position.State = Paused
	}
	previous := job.Version
	if job.AttemptID != "" {
		if _, err = scope.Exec("UPDATE jobs_stage SET status='discarded' WHERE attempt=? AND status='running'", job.AttemptID); err != nil {
			return Job{}, err
		}
	}
	// Keep the exact original state, block reason, counters and failed stage.
	// Resume redoes only the interrupted stage; completed results stay stored.
	job.AttemptID = ""
	job.Version++
	if err = save(tx, scope, job, previous); err != nil {
		return Job{}, err
	}
	return job, nil
}

// FinishBatch releases its active-kind slot only after every member is terminal.
// It reads bounded pages; a paused/review/failed/blocked member cannot disappear
// simply because another file was delivered.
func (r *Repository) FinishBatch(tx store.Tx, batch string) error {
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	after := -1
	seen := false
	for {
		page, err := r.Members(tx, batch, after, 100)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		for _, job := range page {
			seen = true
			if !job.Position.State.Terminal() {
				return ErrBatchActive
			}
			after = job.Ordinal
		}
	}
	if !seen {
		return ErrJobRecord
	}
	result, err := scope.Exec("UPDATE jobs_batch SET finished=1 WHERE id=? AND finished=0", batch)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		tx.Emit(store.Event{Type: "BatchChanged", Subject: kernel.ArtifactRef{Kind: "batch", ID: batch, Version: 2}})
	}
	return nil
}

// UnfinishedBatch names a batch whose active-kind slot is still held.
type UnfinishedBatch struct {
	ID   string
	Kind BatchKind
}

// Unfinished lists the batches that are not finished: at most one of each kind.
func (r *Repository) Unfinished(tx store.Tx) ([]UnfinishedBatch, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query("SELECT id,kind FROM jobs_batch WHERE finished=0 ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnfinishedBatch
	for rows.Next() {
		var b UnfinishedBatch
		var k string
		if err = rows.Scan(&b.ID, &k); err != nil {
			return nil, err
		}
		b.Kind = BatchKind(k)
		out = append(out, b)
	}
	return out, rows.Err()
}
