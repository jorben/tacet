package findings

import (
	"errors"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

func manual(unit string, start int) New {
	return New{Unit: unit, TextVersion: 1, Start: start, End: start + 3, Category: "custom", Source: kernel.SourceManual, Score: 1, Value: []byte("abc")}
}

func TestAddManualRaisesTheVersionAtOnceIsVisibleAndIdempotentPerRequest(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"))
	f.seal("j1", "a1", "b1")
	var id1, id2 int64
	var v1, v2 uint64
	if err := f.do(func(tx store.Tx) (err error) {
		id1, v1, err = f.repo.AddManual(tx, "j1", "b1", "req1", manual("u2", 5))
		return
	}); err != nil {
		t.Fatal(err)
	}
	if v1 != 2 {
		t.Fatalf("version %d", v1)
	}
	if err := f.do(func(tx store.Tx) (err error) {
		id2, v2, err = f.repo.AddManual(tx, "j1", "b1", "req1", manual("u2", 5))
		return
	}); err != nil {
		t.Fatal(err)
	}
	if id1 != id2 || v2 != 2 {
		t.Fatalf("a retried request must return the same finding: %d %d v%d", id1, id2, v2)
	}
	got := f.page(Filter{Job: "j1", Sources: []kernel.FindingSource{kernel.SourceManual}}, 0, 10)
	if len(got) != 1 || got[0].ID != id1 || got[0].Stage != StageManual || got[0].Score != 1 || got[0].Group == "" {
		t.Fatalf("%+v", got)
	}
	if n := len(f.page(Filter{Job: "j1"}, 0, 10)); n != 2 {
		t.Fatalf("the retry added a finding: %d", n)
	}
	// a second request is a second finding and a second version
	if err := f.do(func(tx store.Tx) (err error) {
		_, v2, err = f.repo.AddManual(tx, "j1", "b1", "req2", manual("u2", 9))
		return
	}); err != nil || v2 != 3 {
		t.Fatalf("%v v%d", err, v2)
	}
}

func TestAddManualRefusesWrongSourceMissingFieldsAndAnotherJobsRequest(t *testing.T) {
	f := setup(t)
	bad := map[string]func() error{
		"rule source": func() error {
			return f.do(func(tx store.Tx) error {
				n := manual("u", 0)
				n.Source = kernel.SourceRule
				_, _, err := f.repo.AddManual(tx, "j1", "b1", "r", n)
				return err
			})
		},
		"no request": func() error {
			return f.do(func(tx store.Tx) error { _, _, err := f.repo.AddManual(tx, "j1", "b1", "", manual("u", 0)); return err })
		},
		"no job": func() error {
			return f.do(func(tx store.Tx) error { _, _, err := f.repo.AddManual(tx, "", "b1", "r", manual("u", 0)); return err })
		},
		"request id too long": func() error {
			return f.do(func(tx store.Tx) error {
				_, _, err := f.repo.AddManual(tx, "j1", "b1", strings.Repeat("r", 129), manual("u", 0))
				return err
			})
		},
		"empty span": func() error {
			return f.do(func(tx store.Tx) error {
				n := manual("u", 4)
				n.End = 4
				_, _, err := f.repo.AddManual(tx, "j1", "b1", "r", n)
				return err
			})
		},
	}
	for name, call := range bad {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.do(func(tx store.Tx) error {
		_, _, err := f.repo.AddManual(tx, "j1", "b1", "same", manual("u", 0))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.do(func(tx store.Tx) error {
		_, _, err := f.repo.AddManual(tx, "j2", "b1", "same", manual("u", 0))
		return err
	}); !errors.Is(err, ErrMismatch) {
		t.Errorf("a request id of another job: %v", err)
	}
	if n := len(f.page(Filter{Job: "j1"}, 0, 10)); n != 1 {
		t.Errorf("rejected calls left findings: %d", n)
	}
}

func TestByIDsReturnsOnlyVisibleFindingsOfThatJob(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"), item("u1", 9, "email", kernel.SourceRule, "x@y.com"))
	f.seal("j1", "a1", "b1")
	f.add("j1", "a2", "b1", item("u1", 20, "phone", kernel.SourceRule, "13900000000")) // not sealed
	f.add("j2", "a3", "b1", item("u1", 0, "phone", kernel.SourceRule, "13700000000"))
	f.seal("j2", "a3", "b1")
	all := f.page(Filter{Job: "j1"}, 0, 10)
	if len(all) != 2 {
		t.Fatalf("%d", len(all))
	}
	var unsealed, other int64
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		t.QueryRow("SELECT id FROM findings_finding WHERE attempt='a2'").Scan(&unsealed)
		t.QueryRow("SELECT id FROM findings_finding WHERE attempt='a3'").Scan(&other)
		return nil
	})
	var got []Finding
	if err := f.do(func(tx store.Tx) (err error) {
		got, err = f.repo.ByIDs(tx, "j1", []int64{all[1].ID, all[0].ID, unsealed, other, 9999})
		return
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != all[0].ID || got[1].ID != all[1].ID || got[0].Group == "" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range [][]int64{nil, make([]int64, MaxPage+1)} {
		if err := f.do(func(tx store.Tx) error { _, err := f.repo.ByIDs(tx, "j1", bad); return err }); !errors.Is(err, ErrPageLimit) {
			t.Errorf("%d ids: %v", len(bad), err)
		}
	}
}

func TestCountUsesTheSamePredicateAsPage(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"), item("u1", 9, "email", kernel.SourceModel, "x@y.com"), item("u1", 20, "phone", kernel.SourceModel, "13900000000"))
	f.add("j1", "a2", "b1", item("u1", 30, "phone", kernel.SourceRule, "13700000000")) // never sealed
	f.seal("j1", "a1", "b1")
	hi, lo := 0.5, 0.95
	for name, flt := range map[string]Filter{
		"job": {Job: "j1"}, "category": {Job: "j1", Categories: []string{"phone"}}, "source": {Job: "j1", Sources: []kernel.FindingSource{kernel.SourceModel}},
		"min score": {Job: "j1", MinScore: &hi}, "max score": {Job: "j1", MaxScore: &lo}, "none": {Job: "j1", Categories: []string{"zzz"}}, "other job": {Job: "j2"},
	} {
		var n int
		if err := f.do(func(tx store.Tx) (err error) { n, err = f.repo.Count(tx, flt); return }); err != nil {
			t.Fatal(name, err)
		}
		if want := len(f.page(flt, 0, 100)); n != want {
			t.Errorf("%s: count %d, page %d", name, n, want)
		}
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Count(tx, Filter{}); return err }); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unscoped count: %v", err)
	}
}

func TestFilterByUnitsRestrictsPageCountAndGroups(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"), item("u2", 0, "phone", kernel.SourceRule, "13900000000"), item("u3", 0, "email", kernel.SourceRule, "x@y.com"))
	f.seal("j1", "a1", "b1")
	flt := Filter{Job: "j1", Units: []string{"u1", "u3"}}
	got := f.page(flt, 0, 10)
	if len(got) != 2 || got[0].Unit != "u1" || got[1].Unit != "u3" {
		t.Fatalf("%+v", got)
	}
	var n int
	f.do(func(tx store.Tx) (err error) { n, err = f.repo.Count(tx, flt); return })
	if n != 2 {
		t.Fatalf("count %d", n)
	}
	if len(f.page(Filter{Job: "j1", Units: []string{"nope"}}, 0, 10)) != 0 {
		t.Fatal("an unknown unit matched")
	}
}

// Paging with a category or source filter once sorted every match for every page
// (the planner chose the category index), which made a walk over 100k findings
// take 25 s instead of 0.2 s. The plan, not the clock, is what is asserted.
func TestFilteredPagesAreServedInIdOrderWithoutSorting(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"))
	f.seal("j1", "a1", "b1")
	hi := 0.5
	for name, flt := range map[string]Filter{
		"none": {Job: "j1"}, "category": {Job: "j1", Categories: []string{"phone"}}, "categories": {Job: "j1", Categories: []string{"phone", "email"}},
		"source": {Job: "j1", Sources: []kernel.FindingSource{kernel.SourceRule}}, "both": {Job: "j1", Categories: []string{"phone"}, Sources: []kernel.FindingSource{kernel.SourceModel}},
		"batch": {Batch: "b1", Categories: []string{"phone"}}, "score": {Job: "j1", Categories: []string{"phone"}, MinScore: &hi},
	} {
		where, args, err := flt.where()
		if err != nil {
			t.Fatal(name, err)
		}
		var plan string
		if err := f.do(func(tx store.Tx) error {
			tt, _, _ := scope(tx)
			rows, err := tt.Query("EXPLAIN QUERY PLAN "+pageSQL(where), append(args, 0, 10)...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					return err
				}
				plan += detail + "\n"
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(name, err)
		}
		if strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s: the page query sorts:\n%s", name, plan)
		}
	}
}

// FR-22 undo: a withdrawn manual finding disappears from every reading and the result version rises; it
// is withdrawn once, and only manual findings of the same job can be withdrawn.
func TestAWithdrawnManualFindingIsNoLongerVisible(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"))
	f.seal("j1", "a1", "b1")
	var id int64
	if err := f.do(func(tx store.Tx) (err error) {
		id, _, err = f.repo.AddManual(tx, "j1", "b1", "req1", manual("u2", 5))
		return
	}); err != nil {
		t.Fatal(err)
	}
	rule := f.page(Filter{Job: "j1", Sources: []kernel.FindingSource{kernel.SourceRule}}, 0, 10)[0].ID
	for name, try := range map[string]func(tx store.Tx) error{
		"a rule finding": func(tx store.Tx) error { _, err := f.repo.WithdrawManual(tx, "j1", rule); return err },
		"another job":    func(tx store.Tx) error { _, err := f.repo.WithdrawManual(tx, "j2", id); return err },
		"no such id":     func(tx store.Tx) error { _, err := f.repo.WithdrawManual(tx, "j1", id+100); return err },
	} {
		if err := f.do(try); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var v uint64
	if err := f.do(func(tx store.Tx) (err error) { v, err = f.repo.WithdrawManual(tx, "j1", id); return }); err != nil || v != 3 {
		t.Fatalf("%v v%d", err, v)
	}
	if got := f.page(Filter{Job: "j1"}, 0, 10); len(got) != 1 || got[0].ID != rule {
		t.Fatalf("%+v", got)
	}
	var byID []Finding
	var n int
	var rv uint64
	f.do(func(tx store.Tx) (err error) {
		byID, _ = f.repo.ByIDs(tx, "j1", []int64{id})
		n, _ = f.repo.Count(tx, Filter{Job: "j1"})
		rv, err = f.repo.ResultVersion(tx, "j1")
		return
	})
	if len(byID) != 0 || n != 1 || rv != 3 {
		t.Fatalf("byID=%v count=%d version=%d", byID, n, rv)
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.WithdrawManual(tx, "j1", id); return err }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("withdrawn twice: %v", err)
	}
}
