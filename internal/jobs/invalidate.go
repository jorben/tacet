package jobs

import (
	"encoding/json"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/lineage"
	"github.com/jorben/tacet/internal/store"
)

// StageReads is what a stage run reads, recorded when it starts so that a later
// change of configuration, decisions or input can be compared with it (INV-04).
// Facets are the snapshot facet digests the stage depends on.
type StageReads struct {
	Input           string                      `json:"input,omitempty"`
	Decisions       uint64                      `json:"decisions,omitempty"`
	SupplementTerms uint64                      `json:"terms,omitempty"`
	Facets          map[kernel.Facet]string     `json:"facets,omitempty"`
	Consumed        map[kernel.StageKind]uint64 `json:"consumed,omitempty"`
}

// BeginStageReading is BeginStage plus the lineage record of what the run
// reads. The upstream outputs it consumes are taken from the job's finished
// runs inside this transaction, so they cannot drift between the check and the
// start. A run begun with plain BeginStage records nothing and is treated as
// stale by any later comparison: lineage fails closed.
func (r *Repository) BeginStageReading(tx store.Tx, id string, stage kernel.StageKind, expected, generation uint64, reads StageReads) (kernel.AttemptRef, error) {
	attempt, err := r.BeginStage(tx, id, stage, expected, generation)
	if err != nil {
		return kernel.AttemptRef{}, err
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return kernel.AttemptRef{}, err
	}
	done, err := r.finished(tx, id)
	if err != nil {
		return kernel.AttemptRef{}, err
	}
	reads.Consumed = map[kernel.StageKind]uint64{}
	for _, up := range lineage.Table[stage].Upstream {
		if rec, ok := done[up]; ok {
			reads.Consumed[up] = rec.Output
		}
	}
	data, err := json.Marshal(reads)
	if err != nil {
		return kernel.AttemptRef{}, ErrJobRecord
	}
	if _, err = scope.Exec("UPDATE jobs_stage SET reads=? WHERE attempt=?", data, attempt.AttemptID); err != nil {
		return kernel.AttemptRef{}, err
	}
	return attempt, nil
}

// finished returns the latest completed run per stage as lineage records.
func (r *Repository) finished(tx store.Tx, id string) (map[kernel.StageKind]lineage.Record, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query("SELECT stage, output, reads FROM jobs_stage WHERE job=? AND status='completed' AND output>0 ORDER BY output", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[kernel.StageKind]lineage.Record{}
	for rows.Next() {
		var stage kernel.StageKind
		var output uint64
		var raw []byte
		if err = rows.Scan(&stage, &output, &raw); err != nil {
			return nil, err
		}
		var reads StageReads
		if err = json.Unmarshal(raw, &reads); err != nil {
			return nil, ErrJobRecord
		}
		out[stage] = lineage.Record{Stage: stage, Done: true, Output: output, Input: reads.Input,
			Decisions: reads.Decisions, SupplementTerms: reads.SupplementTerms, Facets: reads.Facets, Consumed: reads.Consumed}
	}
	return out, rows.Err()
}

// restartStage maps a lineage stage to the stage a task can be restarted at.
// The supplementary scan has no task state of its own, so a stale scan reruns
// from extraction, which is earlier and therefore safe.
func restartStage(stage kernel.StageKind) kernel.StageKind {
	if stage == kernel.StageSupplementScan {
		return kernel.StageExtractDetect
	}
	return stage
}

// ApplySnapshotChange binds a new snapshot version to the batch and, in the
// same transaction, sends every member back to the earliest stage whose result
// depended on a facet that changed (INV-03, INV-04). Input, decision and term
// versions are not compared here: only the configuration changed. Members in
// draft states, cancelling or finished are left alone, as are members with no
// finished stage. A running attempt is discarded by the transition, so its late
// result is rejected; the caller cancels the running work after commit.
// It returns the invalidated members and the stage each restarts from.
func (r *Repository) ApplySnapshotChange(tx store.Tx, batch string, expected, version uint64, facets map[kernel.Facet]string) (map[string]kernel.StageKind, error) {
	if err := r.BindSnapshot(tx, batch, expected, version); err != nil {
		return nil, err
	}
	changed := map[string]kernel.StageKind{}
	after := -1
	for {
		page, err := r.Members(tx, batch, after, 200)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return changed, nil
		}
		for _, job := range page {
			after = job.Ordinal
			switch job.Position.State {
			case Preflighting, Preflighted, Locked, Cancelling:
				continue
			}
			if job.Position.State.Terminal() {
				continue
			}
			done, err := r.finished(tx, job.ID)
			if err != nil {
				return nil, err
			}
			running, err := r.runningStale(tx, job, facets)
			if err != nil {
				return nil, err
			}
			if len(done) == 0 && running == kernel.StageNone {
				continue
			}
			cur := lineage.Current{Facets: facets}
			records := make([]lineage.Record, 0, len(done))
			for stage, rec := range done {
				records = append(records, rec)
				switch stage { // only the configuration changed: the other inputs stay as recorded
				case kernel.StageExtractDetect:
					cur.Input = rec.Input
				case kernel.StageSupplementScan:
					cur.SupplementTerms = rec.SupplementTerms
				case kernel.StageResolve:
					cur.Decisions = rec.Decisions
				}
			}
			stale := lineage.EarliestStale(records, cur)
			stage := kernel.StageNone
			if stale != kernel.StageNone {
				stage = restartStage(stale)
			}
			if running != kernel.StageNone && (stage == kernel.StageNone || stageRank[running] < stageRank[stage]) {
				stage = running
			}
			if stage == kernel.StageNone {
				continue
			}
			if _, err = r.Apply(tx, job.ID, job.Version, Event{Kind: EvInvalidate, Stage: stage}); err != nil {
				return nil, err
			}
			changed[job.ID] = stage
		}
	}
}

// runningStale reports the stage of a running attempt that began under facets
// other than the new ones, or StageNone. An attempt that recorded no reads
// (begun with plain BeginStage) cannot be shown to be current, so it counts as
// stale: lineage fails closed.
func (r *Repository) runningStale(tx store.Tx, job Job, facets map[kernel.Facet]string) (kernel.StageKind, error) {
	if job.AttemptID == "" {
		return kernel.StageNone, nil
	}
	t, _, err := transaction(tx)
	if err != nil {
		return kernel.StageNone, err
	}
	var stage kernel.StageKind
	var raw []byte
	if err = t.QueryRow("SELECT stage, reads FROM jobs_stage WHERE attempt=? AND status='running'", job.AttemptID).Scan(&stage, &raw); err != nil {
		return kernel.StageNone, nil
	}
	var reads StageReads
	if err = json.Unmarshal(raw, &reads); err != nil {
		return kernel.StageNone, ErrJobRecord
	}
	if stage == kernel.StagePreflight || stage == kernel.StagePublish {
		return kernel.StageNone, nil // neither depends on the frozen configuration in a way that can be redone safely mid-run
	}
	for _, f := range lineage.Table[stage].Facets {
		if want, ok := facets[f]; !ok || reads.Facets[f] != want {
			return restartStage(stage), nil
		}
	}
	return kernel.StageNone, nil
}
