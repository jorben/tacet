package jobs

import (
	"encoding/json"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/lineage"
	"github.com/jorben/tacet/internal/store"
)

// Checkpoint is what a large-file plugin saved inside a stage (module design
// 5.3, FR-05). Ref names the artifact; Seq is the committed sequence it covers.
type Checkpoint struct {
	Ref kernel.ArtifactRef
	Seq uint64
}

// SaveCheckpoint records a checkpoint for the running attempt a. Only the
// current attempt of the current job version may save, so a discarded or late
// attempt cannot overwrite what a newer one relies on, and the sequence must
// advance: going backwards would re-deliver committed work.
func (r *Repository) SaveCheckpoint(tx store.Tx, a kernel.AttemptRef, ref kernel.ArtifactRef, seq uint64) error {
	if seq == 0 || ref.ID == "" {
		return ErrJobRecord
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	job, err := r.Get(tx, a.JobID)
	if err != nil {
		return err
	}
	if job.Version != a.JobVersion || job.AttemptID != a.AttemptID {
		return ErrLateOutcome
	}
	var stage kernel.StageKind
	var gen uint64
	var reads []byte
	if err = t.QueryRow("SELECT stage, generation, reads FROM jobs_stage WHERE attempt=? AND job=? AND status='running'", a.AttemptID, a.JobID).Scan(&stage, &gen, &reads); err != nil || gen != a.OwnerGeneration {
		return ErrLateOutcome
	}
	var have uint64
	err = t.QueryRow("SELECT seq FROM jobs_checkpoint WHERE job=? AND stage=?", a.JobID, string(stage)).Scan(&have)
	if err == nil && seq <= have {
		return ErrJobRecord
	}
	data, err := json.Marshal(ref)
	if err != nil {
		return ErrJobRecord
	}
	_, err = scope.Exec(`INSERT INTO jobs_checkpoint(job, stage, attempt, seq, ref, reads) VALUES(?,?,?,?,?,?)
		ON CONFLICT(job, stage) DO UPDATE SET attempt=excluded.attempt, seq=excluded.seq, ref=excluded.ref, reads=excluded.reads`,
		a.JobID, string(stage), a.AttemptID, seq, data, reads)
	return err
}

// TakeCheckpoint returns the checkpoint a new attempt of stage may resume from.
// It is returned only if the stage would still read the same input and the same
// facets as when the checkpoint was saved; otherwise it is deleted and the
// stage starts from the beginning. A stale checkpoint is never handed out.
func (r *Repository) TakeCheckpoint(tx store.Tx, job string, stage kernel.StageKind, now StageReads) (Checkpoint, bool, error) {
	t, scope, err := transaction(tx)
	if err != nil {
		return Checkpoint{}, false, err
	}
	var seq uint64
	var ref, raw []byte
	if err = t.QueryRow("SELECT seq, ref, reads FROM jobs_checkpoint WHERE job=? AND stage=?", job, string(stage)).Scan(&seq, &ref, &raw); err != nil {
		return Checkpoint{}, false, nil
	}
	var saved StageReads
	valid := json.Unmarshal(raw, &saved) == nil && saved.Input == now.Input
	for _, f := range lineage.Table[stage].Facets {
		if want, ok := now.Facets[f]; !ok || saved.Facets[f] != want {
			valid = false
		}
	}
	var c Checkpoint
	if !valid || json.Unmarshal(ref, &c.Ref) != nil {
		_, err = scope.Exec("DELETE FROM jobs_checkpoint WHERE job=? AND stage=?", job, string(stage))
		return Checkpoint{}, false, err
	}
	c.Seq = seq
	return c, true, nil
}
