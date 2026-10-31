package findings

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const marker = "TEST_SECRET_MARK_13812345678"

type fixture struct {
	t    *testing.T
	db   *store.DB
	repo *Repository
	keys map[string][]byte
}

func setup(t *testing.T) *fixture {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "f.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{t: t, db: db, keys: map[string][]byte{"b1": bytes.Repeat([]byte{1}, 32), "b2": bytes.Repeat([]byte{2}, 32)}}
	f.repo, err = NewRepository(func(batch string) ([]byte, error) {
		k, ok := f.keys[batch]
		if !ok {
			return nil, errors.New("no key")
		}
		return append([]byte(nil), k...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) do(fn func(store.Tx) error) error {
	f.t.Helper()
	tx, err := f.db.Begin(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func att(job, attempt string) kernel.AttemptRef {
	return kernel.AttemptRef{JobID: job, AttemptID: attempt, OwnerGeneration: 1, JobVersion: 1}
}

func item(unit string, start int, cat string, src kernel.FindingSource, value string) New {
	n := New{Unit: unit, TextVersion: 1, Start: start, End: start + 4, Category: cat, Source: src, Score: 0.9, Field: "body", Template: "t1"}
	if value != "" {
		n.Value = []byte(value)
	}
	return n
}

func (f *fixture) add(job, attempt, batch string, items ...New) {
	f.t.Helper()
	if err := f.do(func(tx store.Tx) error {
		_, err := f.repo.Add(tx, att(job, attempt), batch, StageDetect, items)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) seal(job, attempt, batch string) uint64 {
	f.t.Helper()
	var v uint64
	if err := f.do(func(tx store.Tx) (err error) {
		v, err = f.repo.Seal(tx, att(job, attempt), batch, StageDetect)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return v
}

func (f *fixture) page(filter Filter, after int64, limit int) []Finding {
	f.t.Helper()
	var out []Finding
	if err := f.do(func(tx store.Tx) (err error) { out, err = f.repo.Page(tx, filter, after, limit); return err }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestFindingsAreInvisibleUntilTheAttemptIsSealed(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"))
	if got := f.page(Filter{Job: "j1"}, 0, 10); len(got) != 0 {
		t.Fatalf("a half-finished stage is visible: %+v", got)
	}
	if v := f.seal("j1", "a1", "b1"); v != 1 {
		t.Fatalf("version %d", v)
	}
	got := f.page(Filter{Job: "j1"}, 0, 10)
	if len(got) != 1 || got[0].Category != "phone" || got[0].Version != 1 || got[0].Stage != StageDetect || got[0].Attempt != "a1" || got[0].Group == "" {
		t.Fatalf("%+v", got)
	}
}

func TestResultVersionRisesOnlyWhenAStageAddedFindingsAndSealingIsIdempotent(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"))
	if f.seal("j1", "a1", "b1") != 1 || f.seal("j1", "a1", "b1") != 1 {
		t.Fatal("replayed completion changed the version")
	}
	if v := f.seal("j1", "a2", "b1"); v != 1 { // supplementary scan found nothing: no bump, but it is a complete stage
		t.Fatalf("empty stage bumped the version to %d", v)
	}
	f.add("j1", "a3", "b1", item("u2", 5, "email", kernel.SourceModel, "x@example.com"))
	if v := f.seal("j1", "a3", "b1"); v != 2 {
		t.Fatalf("version %d", v)
	}
	var rv uint64
	f.do(func(tx store.Tx) (err error) { rv, err = f.repo.ResultVersion(tx, "j1"); return err })
	if rv != 2 {
		t.Fatalf("result version %d", rv)
	}
	if v := f.seal("j2", "b1", "b1"); v != 0 {
		t.Fatalf("an empty job starts at version %d", v)
	}
	for _, x := range f.page(Filter{Job: "j1"}, 0, 10) {
		if x.Attempt == "a1" && x.Version != 1 || x.Attempt == "a3" && x.Version != 2 {
			t.Fatalf("finding carries the wrong result version: %+v", x)
		}
	}
	events, _ := f.db.Pending(context.Background(), "q", 100)
	n := 0
	for _, e := range events {
		if e.Event.Type == "FindingsChanged" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d FindingsChanged events, want one per bump", n)
	}
}

func TestDiscardRemovesAnOpenAttemptButNeverASealedOne(t *testing.T) {
	f := setup(t)
	f.add("j1", "dead", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"))
	if err := f.do(func(tx store.Tx) error { return f.repo.Discard(tx, "dead") }); err != nil {
		t.Fatal(err)
	}
	f.add("j1", "dead", "b1", item("u1", 9, "phone", kernel.SourceRule, "13812345678")) // the id may be reused after discard
	f.seal("j1", "dead", "b1")
	if got := f.page(Filter{Job: "j1"}, 0, 10); len(got) != 1 || got[0].Start != 9 {
		t.Fatalf("discarded rows came back: %+v", got)
	}
	if err := f.do(func(tx store.Tx) error { return f.repo.Discard(tx, "dead") }); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed attempt discarded: %v", err)
	}
	if err := f.do(func(tx store.Tx) error { return f.repo.Discard(tx, "never-added") }); err != nil {
		t.Fatalf("discarding an unknown attempt must be a no-op: %v", err)
	}
	if err := f.do(func(tx store.Tx) error {
		_, err := f.repo.Add(tx, att("j1", "dead"), "b1", StageDetect, []New{item("u", 0, "phone", kernel.SourceRule, "")})
		return err
	}); !errors.Is(err, ErrSealed) {
		t.Fatalf("a sealed attempt accepted more findings: %v", err)
	}
}

func TestInvalidInputRejectsTheWholeCallAndStoresNothing(t *testing.T) {
	f := setup(t)
	good := item("u1", 0, "phone", kernel.SourceRule, "13812345678")
	bad := map[string]New{
		"no unit":        {Start: 0, End: 2, Category: "c", Source: kernel.SourceRule},
		"negative start": {Unit: "u", Start: -1, End: 2, Category: "c", Source: kernel.SourceRule},
		"empty span":     {Unit: "u", Start: 3, End: 3, Category: "c", Source: kernel.SourceRule},
		"reversed":       {Unit: "u", Start: 5, End: 3, Category: "c", Source: kernel.SourceRule},
		"no category":    {Unit: "u", Start: 0, End: 2, Source: kernel.SourceRule},
		"unknown source": {Unit: "u", Start: 0, End: 2, Category: "c", Source: "oracle"},
		"score above 1":  {Unit: "u", Start: 0, End: 2, Category: "c", Source: kernel.SourceRule, Score: 1.5},
		"negative score": {Unit: "u", Start: 0, End: 2, Category: "c", Source: kernel.SourceRule, Score: -0.1},
	}
	for name, b := range bad {
		if err := f.do(func(tx store.Tx) error {
			_, err := f.repo.Add(tx, att("j1", "a1"), "b1", StageDetect, []New{good, b})
			return err
		}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	nan := good
	nan.Score = nanValue()
	if err := f.do(func(tx store.Tx) error {
		_, err := f.repo.Add(tx, att("j1", "a1"), "b1", StageDetect, []New{nan})
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("NaN: %v", err)
	}
	for _, c := range []struct{ job, attempt, batch, stage string }{{"", "a", "b", StageDetect}, {"j", "", "b", StageDetect}, {"j", "a", "", StageDetect}, {"j", "a", "b", "other"}} {
		if err := f.do(func(tx store.Tx) error {
			_, err := f.repo.Add(tx, att(c.job, c.attempt), c.batch, c.stage, nil)
			return err
		}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	f.seal("j1", "a1", "b1")
	if got := f.page(Filter{Job: "j1"}, 0, 10); len(got) != 0 {
		t.Fatalf("a rejected call stored %+v", got)
	}
}

func TestAnAttemptCannotChangeJobBatchOrStage(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, ""))
	for name, call := range map[string]func(tx store.Tx) error{
		"other job":   func(tx store.Tx) error { _, err := f.repo.Add(tx, att("j2", "a1"), "b1", StageDetect, nil); return err },
		"other batch": func(tx store.Tx) error { _, err := f.repo.Add(tx, att("j1", "a1"), "b2", StageDetect, nil); return err },
		"other stage": func(tx store.Tx) error {
			_, err := f.repo.Add(tx, att("j1", "a1"), "b1", StageSupplement, nil)
			return err
		},
		"seal as other job": func(tx store.Tx) error { _, err := f.repo.Seal(tx, att("j2", "a1"), "b1", StageDetect); return err },
	} {
		if err := f.do(call); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNoOriginalValueIsStoredAndTheHMACIsKeyedPerBatch(t *testing.T) {
	f := setup(t)
	value := []byte(marker)
	n := item("u1", 0, "phone", kernel.SourceRule, "")
	n.Value = value
	f.add("j1", "a1", "b1", n, func() New { m := item("u2", 0, "phone", kernel.SourceRule, ""); m.Value = []byte(marker); return m }())
	if !bytes.Equal(value, make([]byte, len(value))) {
		t.Fatal("the caller's value buffer was not cleared")
	}
	f.add("j2", "a2", "b2", item("u3", 0, "phone", kernel.SourceRule, marker))
	f.seal("j1", "a1", "b1")
	f.seal("j2", "a2", "b2")
	// every column of every row, as text and as hex, must be free of the value
	if err := f.do(func(tx store.Tx) error {
		c, _ := store.InTransaction(tx)
		rows, err := c.Query("SELECT * FROM findings_finding")
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			for _, v := range vals {
				if bytes.Contains([]byte(fmt.Sprint(v)), []byte(marker)) || bytes.Contains([]byte(fmt.Sprint(v)), []byte("13812345678")) {
					t.Fatalf("original value stored: %v", v)
				}
				if b, ok := v.([]byte); ok && len(b) != 32 {
					t.Fatalf("unexpected blob of %d bytes", len(b))
				}
			}
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	one := f.page(Filter{Job: "j1"}, 0, 10)
	two := f.page(Filter{Job: "j2"}, 0, 10)
	if one[0].Group != one[1].Group {
		t.Fatal("the same value in one batch must group together")
	}
	if one[0].Group == two[0].Group {
		t.Fatal("the same value in another batch must not be comparable")
	}
	if err := f.do(func(tx store.Tx) error {
		_, err := f.repo.Add(tx, att("j9", "a9"), "unknown-batch", StageDetect, []New{item("u", 0, "c", kernel.SourceRule, "v")})
		return err
	}); err == nil {
		t.Fatal("a value was accepted without a batch key")
	}
}

func TestPagingFiltersAndLimits(t *testing.T) {
	f := setup(t)
	var items []New
	for i := 0; i < 25; i++ {
		cat, src, score := "phone", kernel.SourceRule, 0.0
		if i%5 == 0 {
			cat, src, score = "person", kernel.SourceModel, float64(i)/40
		}
		n := item(fmt.Sprintf("u%d", i), i, cat, src, fmt.Sprintf("v%d", i%3))
		n.Score = score
		items = append(items, n)
	}
	f.add("j1", "a1", "b1", items...)
	f.seal("j1", "a1", "b1")
	var seen []int64
	var after int64
	for rounds := 0; ; rounds++ {
		if rounds > 10 {
			t.Fatal("paging does not advance past the cursor")
		}
		page := f.page(Filter{Job: "j1"}, after, 7)
		if len(page) == 0 {
			break
		}
		for _, x := range page {
			seen = append(seen, x.ID)
			after = x.ID
		}
	}
	if len(seen) != 25 {
		t.Fatalf("paging lost or repeated rows: %d", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatal("pages are not in id order")
		}
	}
	if got := f.page(Filter{Job: "j1", Categories: []string{"person"}}, 0, 50); len(got) != 5 {
		t.Fatalf("category filter: %d", len(got))
	}
	if got := f.page(Filter{Job: "j1", Sources: []kernel.FindingSource{kernel.SourceModel}}, 0, 50); len(got) != 5 {
		t.Fatalf("source filter: %d", len(got))
	}
	lo, hi := 0.2, 0.4 // candidates in a score band, how below-threshold ones are reviewed (MD-12)
	if got := f.page(Filter{Job: "j1", Sources: []kernel.FindingSource{kernel.SourceModel}, MinScore: &lo, MaxScore: &hi}, 0, 50); len(got) != 2 {
		t.Fatalf("score band: %d", len(got))
	}
	group := f.page(Filter{Job: "j1"}, 0, 1)[0].Group
	if got := f.page(Filter{Job: "j1", Group: group}, 0, 50); len(got) == 0 || len(got) >= 25 {
		t.Fatalf("group filter: %d", len(got))
	}
	if got := f.page(Filter{Batch: "b1", Field: "body", Template: "t1"}, 0, 50); len(got) != 25 {
		t.Fatalf("batch filter: %d", len(got))
	}
	if got := f.page(Filter{Batch: "other"}, 0, 50); len(got) != 0 {
		t.Fatal("another batch is visible")
	}
	for _, bad := range []func() error{
		func() error {
			return f.do(func(tx store.Tx) error { _, err := f.repo.Page(tx, Filter{Job: "j1"}, 0, 0); return err })
		},
		func() error {
			return f.do(func(tx store.Tx) error { _, err := f.repo.Page(tx, Filter{Job: "j1"}, 0, MaxPage+1); return err })
		},
		func() error {
			return f.do(func(tx store.Tx) error { _, err := f.repo.Page(tx, Filter{Job: "j1"}, -1, 5); return err })
		},
		func() error {
			return f.do(func(tx store.Tx) error { _, err := f.repo.Page(tx, Filter{}, 0, 5); return err })
		},
		func() error {
			return f.do(func(tx store.Tx) error { _, err := f.repo.Page(tx, Filter{Job: "j1", Group: "zz"}, 0, 5); return err })
		},
	} {
		if err := bad(); err == nil {
			t.Fatal("an out-of-bounds or unscoped query was accepted")
		}
	}
}

func TestGroupsAggregateAcrossFilesWithStableRepresentativesAndKeysetPaging(t *testing.T) {
	f := setup(t)
	// the same phone number in two files, another number once, a person name in each
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "13812345678"), item("u2", 4, "phone", kernel.SourceRule, "13900000000"),
		item("u3", 8, "person", kernel.SourceModel, "张伟"), item("u1", 20, "phone", kernel.SourceRule, "13812345678"))
	f.add("j2", "a2", "b1", item("u4", 0, "phone", kernel.SourceRule, "13812345678"), item("u5", 4, "person", kernel.SourceModel, "张伟"))
	f.seal("j1", "a1", "b1")
	f.seal("j2", "a2", "b1")
	groups := func(after GroupKey, limit int) []Group {
		var out []Group
		if err := f.do(func(tx store.Tx) (err error) {
			out, err = f.repo.Groups(tx, Filter{Batch: "b1"}, after, limit)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := groups(GroupKey{}, 10)
	if len(all) != 3 {
		t.Fatalf("%d groups: %+v", len(all), all)
	}
	var shared *Group
	for i := range all {
		if all[i].Category == "phone" && all[i].Findings == 3 {
			shared = &all[i]
		}
	}
	if shared == nil || shared.Jobs != 2 { // three occurrences in two files
		t.Fatalf("the shared number must be one group of 3 findings over 2 files: %+v", all)
	}
	if rep := f.page(Filter{Batch: "b1", Group: shared.Value}, 0, 1)[0].ID; shared.Representative != rep {
		t.Fatalf("representative %d, lowest id %d", shared.Representative, rep)
	}
	// later findings never move the representative or reorder existing groups
	f.add("j3", "a3", "b1", item("u6", 0, "phone", kernel.SourceRule, "13812345678"), item("u7", 0, "address", kernel.SourceModel, "北京"))
	f.seal("j3", "a3", "b1")
	again := groups(GroupKey{}, 10)
	for _, g := range again {
		if g.Category == "phone" && g.Value == shared.Value && g.Representative != shared.Representative {
			t.Fatal("the representative jumped after a refresh")
		}
	}
	if len(again) != 4 {
		t.Fatalf("%d groups", len(again))
	}
	// keyset paging yields each group once, in the same order
	var paged []Group
	var key GroupKey
	for {
		page := groups(key, 1)
		if len(page) == 0 {
			break
		}
		paged = append(paged, page[0])
		key = GroupKey{page[0].Category, string(page[0].Source), page[0].Field, page[0].Template, page[0].Value}
	}
	if len(paged) != len(again) {
		t.Fatalf("paged %d, whole %d", len(paged), len(again))
	}
	for i := range paged {
		if paged[i] != again[i] {
			t.Fatalf("group %d differs between paged and whole listing", i)
		}
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Groups(tx, Filter{Batch: "b1"}, GroupKey{}, 0); return err }); !errors.Is(err, ErrPageLimit) {
		t.Fatalf("zero limit: %v", err)
	}
}

func TestPurgeRemovesAJobsFindingsAndVersion(t *testing.T) {
	f := setup(t)
	f.add("j1", "a1", "b1", item("u1", 0, "phone", kernel.SourceRule, "1"))
	f.add("j2", "a2", "b1", item("u2", 0, "phone", kernel.SourceRule, "1"))
	f.seal("j1", "a1", "b1")
	f.seal("j2", "a2", "b1")
	if err := f.do(func(tx store.Tx) error { return f.repo.Purge(tx, "j1") }); err != nil {
		t.Fatal(err)
	}
	if len(f.page(Filter{Job: "j1"}, 0, 10)) != 0 || len(f.page(Filter{Job: "j2"}, 0, 10)) != 1 {
		t.Fatal("purge removed the wrong rows")
	}
	var v uint64
	f.do(func(tx store.Tx) (err error) { v, err = f.repo.ResultVersion(tx, "j1"); return err })
	if v != 0 {
		t.Fatalf("version survived the purge: %d", v)
	}
}

func TestRepositoryNeedsABatchKeySource(t *testing.T) {
	if _, err := NewRepository(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
