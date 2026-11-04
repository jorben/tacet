//go:build darwin || windows

package jobs

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/jorben/tacet/internal/store"
)

// put makes the batch's members be in the given states, as the pipeline would have left them, and confirms it.
func (f *fixture) put(t *testing.T, batch string, confirmed bool, states ...State) {
	t.Helper()
	f.create(t, batch, RegularBatch)
	if err := f.commit(func(tx store.Tx) error {
		_, scope, err := transaction(tx)
		if err != nil {
			return err
		}
		for i, s := range states {
			id := batch + "-" + string(rune('a'+i))
			if err := f.repo.AddMember(tx, batch, id, i); err != nil {
				return err
			}
			data, _ := json.Marshal(Snapshot{State: s})
			if _, err := scope.Exec("UPDATE jobs_job SET position=? WHERE id=?", data, id); err != nil {
				return err
			}
		}
		if confirmed {
			_, err = scope.Exec("UPDATE jobs_batch SET confirmed=1 WHERE id=?", batch)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) park(t *testing.T, auto bool) string {
	t.Helper()
	var got string
	if err := f.commit(func(tx store.Tx) error {
		var err error
		got, err = f.repo.ParkHolder(tx, RegularBatch, func(string) (bool, error) { return auto, nil })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOnlyABatchWithNothingLeftToDoWithoutAPersonIsParked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		draft  bool
		auto   bool
		states []State
		parks  bool
	}{
		{"awaiting review", false, false, []State{AwaitingReview}, true},
		{"ready, not authorized", false, false, []State{ReadyToPublish}, true},
		{"paused", false, false, []State{Paused}, true},
		{"blocked", false, false, []State{Blocked}, true},
		{"failed", false, false, []State{Failed}, true},
		{"locked", false, false, []State{Locked}, true},
		{"waiting beside ended files", false, false, []State{Completed, Skipped, Unprocessable, Cancelled, Partial, AwaitingReview}, true},
		{"ready and publishing by itself", false, true, []State{ReadyToPublish}, false},
		{"waiting beside a queued file", false, false, []State{AwaitingReview, Queued}, false},
		{"preflighting", false, false, []State{Preflighting}, false},
		{"preflighted after unlocking", false, false, []State{Preflighted}, false},
		{"queued", false, false, []State{Queued}, false},
		{"extracting", false, false, []State{ExtractingDetecting}, false},
		{"resolving", false, false, []State{Resolving}, false},
		{"exporting", false, false, []State{Exporting}, false},
		{"validating", false, false, []State{Validating}, false},
		{"publishing", false, false, []State{Publishing}, false},
		{"cancelling", false, false, []State{Cancelling}, false},
		{"a draft that was inspected", true, false, []State{Preflighted}, false},
		{"a paused draft", true, false, []State{Paused}, false},
		{"a draft awaiting nothing but a password", true, false, []State{Locked}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixtureDB(t)
			f.put(t, "b", !tc.draft, tc.states...)
			got := f.park(t, tc.auto)
			next := f.commit(func(tx store.Tx) error { return f.repo.CreateDraft(tx, "next", RegularBatch) })
			if tc.parks {
				if got != "b" || next != nil {
					t.Fatalf("parked %q, next draft %v", got, next)
				}
			} else if got != "" || !errors.Is(next, ErrBatchActive) {
				t.Fatalf("parked %q, next draft %v", got, next)
			}
			if err := f.commit(func(tx store.Tx) error {
				parked, err := f.repo.Parked(tx, "b")
				if parked != tc.parks {
					t.Errorf("Parked = %v", parked)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A busy file past the first page of members still keeps the batch from being parked.
func TestParkingLooksAtEveryMember(t *testing.T) {
	f := fixtureDB(t)
	states := make([]State, 250)
	for i := range states {
		states[i] = AwaitingReview
	}
	f.put(t, "control", true, states...)
	if got := f.park(t, false); got != "control" {
		t.Fatalf("all waiting: parked %q", got)
	}
	states[249] = Queued
	f.put(t, "b", true, states...)
	if got := f.park(t, false); got != "" {
		t.Fatalf("parked %q with a queued file on the last page", got)
	}
}

func TestParkedBatchesAddUpAndStayListedWhileOneHoldsTheSlot(t *testing.T) {
	f := fixtureDB(t)
	for _, id := range []string{"b1", "b2", "b3"} {
		f.put(t, id, true, AwaitingReview)
		if got := f.park(t, false); got != id {
			t.Fatalf("parked %q, want %s", got, id)
		}
	}
	f.create(t, "holder", RegularBatch)
	f.create(t, "quick", QuickBatch) // the other kind has its own slot
	if err := f.commit(func(tx store.Tx) error {
		bs, err := f.repo.Unfinished(tx)
		if err != nil {
			return err
		}
		if len(bs) != 5 {
			t.Errorf("unfinished %+v", bs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// the holder is still alone in the slot: parking did not open it to a second unparked batch
	if err := f.commit(func(tx store.Tx) error { return f.repo.CreateDraft(tx, "second", RegularBatch) }); !errors.Is(err, ErrBatchActive) {
		t.Fatalf("second draft: %v", err)
	}
}

// Parking moves nothing: state, version and attempt of a member stay, and nothing is dispatched.
func TestParkingDoesNotTouchTheFiles(t *testing.T) {
	f := fixtureDB(t)
	f.put(t, "b", true, AwaitingReview, ReadyToPublish)
	before := []Job{f.get(t, "b-a"), f.get(t, "b-b")}
	dispatched := len(f.dispatched)
	if got := f.park(t, false); got != "b" {
		t.Fatalf("parked %q", got)
	}
	for i, id := range []string{"b-a", "b-b"} {
		if after := f.get(t, id); after.Version != before[i].Version || after.Position != before[i].Position || after.AttemptID != before[i].AttemptID {
			t.Errorf("%s changed: %+v -> %+v", id, before[i], after)
		}
	}
	if len(f.dispatched) != dispatched {
		t.Fatal("parking dispatched a stage")
	}
}

// A parked batch still ends as any other: FinishBatch refuses while a file is open, then releases it.
func TestAParkedBatchFinishesLikeAnyOther(t *testing.T) {
	f := fixtureDB(t)
	f.put(t, "b", true, AwaitingReview)
	if got := f.park(t, false); got != "b" {
		t.Fatalf("parked %q", got)
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.FinishBatch(tx, "b") }); !errors.Is(err, ErrBatchActive) {
		t.Fatalf("a waiting parked batch finished: %v", err)
	}
	job := f.get(t, "b-a")
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, job.ID, job.Version, Event{Kind: EvSkip})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.FinishBatch(tx, "b") }); err != nil {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error {
		bs, err := f.repo.Unfinished(tx)
		if len(bs) != 0 {
			t.Errorf("still listed: %+v", bs)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAnErrorReadingWhoPublishesKeepsTheHolder(t *testing.T) {
	f := fixtureDB(t)
	f.put(t, "b", true, AwaitingReview)
	boom := errors.New("no run")
	err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.ParkHolder(tx, RegularBatch, func(string) (bool, error) { return false, boom })
		return err
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error {
		if parked, _ := f.repo.Parked(tx, "b"); parked {
			t.Error("parked")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
