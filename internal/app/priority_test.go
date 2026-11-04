package app

import (
	"testing"

	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/kernel"
)

// Architecture 5.1: a person waits for a quick task and for a file they just decided on, in every stage; the rest of a
// batch, its inspection included, is background work.
func TestWhatAStageIsScheduledAs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		position jobs.Snapshot
		want     kernel.Priority
	}{
		{"a quick task", "quick", jobs.Snapshot{State: jobs.Preflighting}, kernel.Interactive},
		{"a quick task, later on", "quick", jobs.Snapshot{State: jobs.Queued, Cursor: kernel.StageExtractDetect}, kernel.Interactive},
		{"a file of a batch", "batch", jobs.Snapshot{State: jobs.Queued, Cursor: kernel.StageExtractDetect}, kernel.Background},
		{"the inspection of a batch", "batch", jobs.Snapshot{State: jobs.Preflighting}, kernel.Background},
		{"a file of a batch sent back by a person", "batch", jobs.Snapshot{State: jobs.Resolving, Reviewed: true}, kernel.Interactive},
		{"and on its way to publishing", "batch", jobs.Snapshot{State: jobs.ReadyToPublish, Reviewed: true}, kernel.Interactive},
	} {
		if got := stagePriority(tc.kind, tc.position); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// What makes tasks of a kind run side by side is not more workers for the model: the limit stays where it was.
func TestTheBudgetsKeepOneModelWorker(t *testing.T) {
	comfort, std := budgetFor("comfort"), budgetFor("std")
	if comfort.Workers["detect"] != 1 || std.Workers["detect"] != 1 {
		t.Fatalf("detect workers %d %d", comfort.Workers["detect"], std.Workers["detect"])
	}
	if comfort.MaxRunning != 1 || comfort.CPUSlots != 1 || comfort.Workers["format"] != 1 || std.MaxRunning != 2 || std.CPUSlots != 2 || std.Workers["format"] != 2 {
		t.Fatalf("the tiers changed: %+v %+v", comfort, std)
	}
	// and the largest imports fit side by side: every one of their files can wait for a stage at one time
	if comfort.MaxQueued < 2*maxBatchFiles || std.MaxQueued < 2*maxBatchFiles {
		t.Fatalf("queue bounds %d %d for imports of %d files", comfort.MaxQueued, std.MaxQueued, maxBatchFiles)
	}
}
