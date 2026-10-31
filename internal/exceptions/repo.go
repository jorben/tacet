package exceptions

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrInvalid     = errors.New("EXCEPTION_INVALID")
	ErrNotFound    = errors.New("EXCEPTION_ITEM_NOT_FOUND")
	ErrUnavailable = errors.New("EXCEPTION_ACTION_UNAVAILABLE")
	ErrNoGroup     = errors.New("EXCEPTION_GROUP_DECISION_REFUSED")
	ErrScope       = errors.New("SCOPE_CHANGED")
)

// MaxGroup bounds one group decision; larger groups are decided in parts.
const MaxGroup = 10000

// MaxPage bounds one page of items.
const MaxPage = 200

// Module owns exception items and the decision history. Rows hold ids, type
// codes, coarse reasons and positions, never document content.
func Module() store.Module {
	return store.Module{Name: "exceptions", Prefix: "exceptions_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE exceptions_item(id TEXT PRIMARY KEY, job TEXT NOT NULL, type TEXT NOT NULL, scope TEXT NOT NULL, reason TEXT NOT NULL,
			post_export INTEGER NOT NULL DEFAULT 0, resolved INTEGER NOT NULL DEFAULT 0, basis TEXT NOT NULL DEFAULT '', seen TEXT NOT NULL)`,
		`CREATE INDEX exceptions_by_job ON exceptions_item(job, resolved, type, reason, id)`,
		`CREATE TABLE exceptions_decision(seq INTEGER PRIMARY KEY AUTOINCREMENT, request TEXT NOT NULL, item TEXT NOT NULL REFERENCES exceptions_item(id),
			action TEXT NOT NULL, consequence TEXT NOT NULL, revokes INTEGER, UNIQUE(request, item))`,
		`CREATE INDEX exceptions_decision_item ON exceptions_decision(item, seq)`,
		`CREATE INDEX exceptions_decision_revokes ON exceptions_decision(revokes)`,
		`CREATE TABLE exceptions_version(job TEXT PRIMARY KEY, version INTEGER NOT NULL)`,
	}}}}
}

// Draft is an exception the policy could not decide. ScopeKey carries ids and
// positions only. PostExport marks one found after export (stage post_export).
type Draft struct {
	Type       Type
	ScopeKey   string
	Reason     string
	PostExport bool
}

type Repository struct{}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("exceptions")
	return t, s, err
}

// SyncResult counts what a Sync changed; a repeated Sync changes nothing.
type SyncResult struct{ Added, Reopened, Resolved, Updated int }

// Sync reconciles a job's exceptions with the drafts of one resolution.
// Identity is (type, scope key): an exception that is still present keeps its id
// and with it its decisions and review position; a new one is added; one that no
// longer appears is marked resolved with the resolution version as its basis.
// Running it twice with the same drafts is a no-op (module design 7.2).
func (Repository) Sync(tx store.Tx, job string, drafts []Draft, resolution string) (SyncResult, error) {
	var res SyncResult
	if job == "" || resolution == "" || len(resolution) > 128 {
		return res, ErrInvalid
	}
	want := map[string]Draft{}
	for _, d := range drafts {
		if _, ok := Table[d.Type]; !ok || d.ScopeKey == "" || len(d.ScopeKey) > 1024 || len(d.Reason) > 128 {
			return res, ErrInvalid
		}
		want[ItemIDFor(job, d.Type, d.ScopeKey)] = d
	}
	t, sc, err := scope(tx)
	if err != nil {
		return res, err
	}
	rows, err := t.Query("SELECT id, reason, post_export, resolved FROM exceptions_item WHERE job=?", job)
	if err != nil {
		return res, err
	}
	type have struct {
		reason   string
		post     bool
		resolved bool
	}
	existing := map[string]have{}
	for rows.Next() {
		var id string
		var h have
		if err = rows.Scan(&id, &h.reason, &h.post, &h.resolved); err != nil {
			rows.Close()
			return res, err
		}
		existing[id] = h
	}
	rows.Close()
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := want[id]
		h, ok := existing[id]
		switch {
		case !ok:
			if _, err = sc.Exec(`INSERT INTO exceptions_item(id, job, type, scope, reason, post_export, seen) VALUES(?,?,?,?,?,?,?)`,
				id, job, string(d.Type), d.ScopeKey, d.Reason, boolInt(d.PostExport), resolution); err != nil {
				return res, err
			}
			res.Added++
		case h.resolved:
			if _, err = sc.Exec("UPDATE exceptions_item SET resolved=0, basis='', reason=?, post_export=?, seen=? WHERE id=?",
				d.Reason, boolInt(d.PostExport), resolution, id); err != nil {
				return res, err
			}
			res.Reopened++
		case h.reason != d.Reason || (!h.post && d.PostExport):
			if _, err = sc.Exec("UPDATE exceptions_item SET reason=?, post_export=?, seen=? WHERE id=?", d.Reason, boolInt(h.post || d.PostExport), resolution, id); err != nil {
				return res, err
			}
			res.Updated++
		}
	}
	gone := make([]string, 0)
	for id, h := range existing {
		// items found after export are resolved by the validation that found them (SyncPostExport)
		if _, still := want[id]; !still && !h.resolved && !h.post {
			gone = append(gone, id)
		}
	}
	sort.Strings(gone)
	for _, id := range gone {
		if _, err = sc.Exec("UPDATE exceptions_item SET resolved=1, basis=? WHERE id=?", resolution, id); err != nil {
			return res, err
		}
		res.Resolved++
	}
	return res, nil
}

// SyncPostExport reconciles the exceptions found after export (module design 8.2
// step 4) with those of the latest validation: new ones are added, ones that are
// still found keep their identity and decisions, and ones the latest validation
// no longer finds are resolved. Only validation calls it; Sync leaves these rows
// alone, so a re-resolution cannot silently close what validation reported.
func (Repository) SyncPostExport(tx store.Tx, job string, drafts []Draft, validation string) (SyncResult, error) {
	var res SyncResult
	if job == "" || validation == "" || len(validation) > 128 {
		return res, ErrInvalid
	}
	want := map[string]Draft{}
	for _, d := range drafts {
		if _, ok := Table[d.Type]; !ok || d.ScopeKey == "" || len(d.ScopeKey) > 1024 || len(d.Reason) > 128 {
			return res, ErrInvalid
		}
		d.PostExport = true
		want[ItemIDFor(job, d.Type, d.ScopeKey)] = d
	}
	t, sc, err := scope(tx)
	if err != nil {
		return res, err
	}
	rows, err := t.Query("SELECT id, resolved, post_export FROM exceptions_item WHERE job=?", job)
	if err != nil {
		return res, err
	}
	type have struct{ resolved, post bool }
	existing := map[string]have{}
	for rows.Next() {
		var id string
		var h have
		if err = rows.Scan(&id, &h.resolved, &h.post); err != nil {
			rows.Close()
			return res, err
		}
		existing[id] = h
	}
	rows.Close()
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := want[id]
		switch h, ok := existing[id]; {
		case !ok:
			if _, err = sc.Exec(`INSERT INTO exceptions_item(id, job, type, scope, reason, post_export, seen) VALUES(?,?,?,?,?,1,?)`,
				id, job, string(d.Type), d.ScopeKey, d.Reason, validation); err != nil {
				return res, err
			}
			res.Added++
		case h.resolved:
			if _, err = sc.Exec("UPDATE exceptions_item SET resolved=0, basis='', reason=?, post_export=1, seen=? WHERE id=?", d.Reason, validation, id); err != nil {
				return res, err
			}
			res.Reopened++
		}
	}
	gone := []string{}
	for id, h := range existing {
		if _, still := want[id]; !still && h.post && !h.resolved {
			gone = append(gone, id)
		}
	}
	sort.Strings(gone)
	for _, id := range gone {
		if _, err = sc.Exec("UPDATE exceptions_item SET resolved=1, basis=? WHERE id=?", validation, id); err != nil {
			return res, err
		}
		res.Resolved++
	}
	return res, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Version is the job's exception-decision version: it rises with every decision
// or revocation that records something, and the resolve stage counts it into the
// decision version it commits against.
func (Repository) Version(tx store.Tx, job string) (uint64, error) {
	if job == "" {
		return 0, ErrInvalid
	}
	t, _, err := scope(tx)
	if err != nil {
		return 0, err
	}
	var v uint64
	switch err = t.QueryRow("SELECT version FROM exceptions_version WHERE job=?", job).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, err
	}
	return v, nil
}

func bumpJobs(tx store.Tx, jobs map[string]bool) error {
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(jobs))
	for j := range jobs {
		names = append(names, j)
	}
	sort.Strings(names)
	for _, j := range names {
		if _, err = sc.Exec(`INSERT INTO exceptions_version(job, version) VALUES(?,1) ON CONFLICT(job) DO UPDATE SET version=version+1`, j); err != nil {
			return err
		}
		var v uint64
		if err = t.QueryRow("SELECT version FROM exceptions_version WHERE job=?", j).Scan(&v); err != nil {
			return err
		}
		tx.Emit(store.Event{Type: "DecisionsChanged", Subject: kernel.ArtifactRef{Kind: "decisions", ID: j, Version: v}})
	}
	return nil
}

// Row is a stored exception with its current decision state.
type Row struct {
	ID         string
	Job        string
	Type       Type
	ScopeKey   string
	Reason     string
	PostExport bool
	Resolved   bool
	Decided    bool
}

// Filter selects items. Jobs is required: an unscoped query is refused.
type Filter struct {
	Jobs            []string
	Type            Type
	Reason          string
	IncludeResolved bool
	OnlyUndecided   bool
}

const effectiveDecision = `EXISTS (SELECT 1 FROM exceptions_decision d WHERE d.item=i.id AND d.revokes IS NULL
	AND NOT EXISTS (SELECT 1 FROM exceptions_decision r WHERE r.revokes=d.seq))`

func (f Filter) where() (string, []any, error) {
	if len(f.Jobs) == 0 || len(f.Jobs) > 500 {
		return "", nil, ErrInvalid
	}
	w := " WHERE i.job IN (?" + strings.Repeat(",?", len(f.Jobs)-1) + ")"
	var args []any
	for _, j := range f.Jobs {
		if j == "" {
			return "", nil, ErrInvalid
		}
		args = append(args, j)
	}
	if f.Type != "" {
		if _, ok := Table[f.Type]; !ok {
			return "", nil, ErrUnknownType
		}
		w += " AND i.type=?"
		args = append(args, string(f.Type))
	}
	if f.Reason != "" {
		w += " AND i.reason=?"
		args = append(args, f.Reason)
	}
	if !f.IncludeResolved {
		w += " AND i.resolved=0"
	}
	if f.OnlyUndecided {
		w += " AND NOT " + effectiveDecision
	}
	return w, args, nil
}

// Items pages items in id order (keyset), so the queue does not jump.
func (Repository) Items(tx store.Tx, f Filter, after string, limit int) ([]Row, error) {
	if limit < 1 || limit > MaxPage {
		return nil, ErrInvalid
	}
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT i.id, i.job, i.type, i.scope, i.reason, i.post_export, i.resolved, `+effectiveDecision+`
		FROM exceptions_item i`+where+` AND i.id>? ORDER BY i.id LIMIT ?`, append(args, after, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var it Row
		var ty string
		if err = rows.Scan(&it.ID, &it.Job, &ty, &it.ScopeKey, &it.Reason, &it.PostExport, &it.Resolved, &it.Decided); err != nil {
			return nil, err
		}
		it.Type = Type(ty)
		out = append(out, it)
	}
	return out, rows.Err()
}

// GroupRow is one queue row: a cause, not an item (BUX-03).
type GroupRow struct {
	Type    Type
	Reason  string
	Items   int
	Files   int
	Decided int
}

// Groups aggregates open items by type and reason, in a stable order.
func (Repository) Groups(tx store.Tx, f Filter) ([]GroupRow, error) {
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT i.type, i.reason, COUNT(*), COUNT(DISTINCT i.job), SUM(`+effectiveDecision+`)
		FROM exceptions_item i`+where+` GROUP BY i.type, i.reason ORDER BY i.type, i.reason`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupRow
	for rows.Next() {
		var g GroupRow
		var ty string
		if err = rows.Scan(&ty, &g.Reason, &g.Items, &g.Files, &g.Decided); err != nil {
			return nil, err
		}
		g.Type = Type(ty)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Decision is the effective decision of one item.
type Decision struct {
	Seq         int64
	Item        string
	Action      ActionKind
	Consequence Consequence
}

// ContextFor supplies the facts that change what an item offers (partial-result
// eligibility, the used reprocess, the policy's answer for grouping).
type ContextFor func(Row) Context

// Decide records the action for the given items, one history row each. Every item
// must be open, belong to one of jobs and offer the action as available; with
// more than one item the type must allow group decisions. Repeating a request id
// records nothing new.
func (Repository) Decide(tx store.Tx, request string, jobs []string, ids []string, a ActionKind, ctxFor ContextFor) (int, error) {
	if request == "" || len(request) > 128 || len(ids) == 0 || len(ids) > MaxGroup || len(jobs) == 0 || ctxFor == nil {
		return 0, ErrInvalid
	}
	items, err := loadOpen(tx, jobs, ids)
	if err != nil {
		return 0, err
	}
	return record(tx, request, items, a, ctxFor)
}

func loadOpen(tx store.Tx, jobs, ids []string) ([]Row, error) {
	seen := map[string]bool{}
	var uniq []string
	for _, id := range ids {
		if id == "" {
			return nil, ErrInvalid
		}
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	var out []Row
	r := Repository{}
	jobSet := map[string]bool{}
	for _, j := range jobs {
		jobSet[j] = true
	}
	sort.Strings(uniq)
	for len(uniq) > 0 {
		n := min(len(uniq), 400)
		t, _, err := scope(tx)
		if err != nil {
			return nil, err
		}
		args := []any{}
		for _, id := range uniq[:n] {
			args = append(args, id)
		}
		rows, err := t.Query(`SELECT i.id, i.job, i.type, i.scope, i.reason, i.post_export, i.resolved, `+effectiveDecision+`
			FROM exceptions_item i WHERE i.id IN (?`+strings.Repeat(",?", n-1)+`) ORDER BY i.id`, args...)
		if err != nil {
			return nil, err
		}
		got := 0
		for rows.Next() {
			var it Row
			var ty string
			if err = rows.Scan(&it.ID, &it.Job, &ty, &it.ScopeKey, &it.Reason, &it.PostExport, &it.Resolved, &it.Decided); err != nil {
				rows.Close()
				return nil, err
			}
			it.Type = Type(ty)
			if !jobSet[it.Job] || it.Resolved {
				rows.Close()
				return nil, ErrNotFound
			}
			out = append(out, it)
			got++
		}
		rows.Close()
		if got != n {
			return nil, ErrNotFound
		}
		uniq = uniq[n:]
	}
	_ = r
	return out, nil
}

func record(tx store.Tx, request string, items []Row, a ActionKind, ctxFor ContextFor) (int, error) {
	_, sc, err := scope(tx)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		c := ctxFor(it)
		if len(items) > 1 {
			ok, err := CanDecideGroup(it.Type, c)
			if err != nil {
				return 0, err
			}
			if !ok {
				return 0, ErrNoGroup
			}
		}
		offer, err := Actions(it.Type, c)
		if err != nil {
			return 0, err
		}
		found := false
		for _, o := range offer {
			if o.Kind == a && o.Available {
				found = true
			}
		}
		if !found {
			return 0, ErrUnavailable
		}
	}
	n := 0
	changed := map[string]bool{}
	for _, it := range items {
		offer, _ := Actions(it.Type, ctxFor(it))
		var cons Consequence
		for _, o := range offer {
			if o.Kind == a {
				cons = o.Consequence
			}
		}
		res, err := sc.Exec(`INSERT OR IGNORE INTO exceptions_decision(request, item, action, consequence) VALUES(?,?,?,?)`, request, it.ID, string(a), string(cons))
		if err != nil {
			return 0, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
			changed[it.Job] = true
		}
	}
	return n, bumpJobs(tx, changed)
}

// DecideGroup decides every open, undecided item the filter matches. expect is
// the count the user was shown: if the group has changed since, nothing is
// recorded and ErrScope is returned (frozen scope, BUX-06).
func (Repository) DecideGroup(tx store.Tx, request string, f Filter, expect int, a ActionKind, ctxFor ContextFor) (int, error) {
	if request == "" || len(request) > 128 || expect < 1 || expect > MaxGroup || ctxFor == nil {
		return 0, ErrInvalid
	}
	f.IncludeResolved, f.OnlyUndecided = false, true
	where, args, err := f.where()
	if err != nil {
		return 0, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return 0, err
	}
	rows, err := t.Query(`SELECT i.id FROM exceptions_item i`+where+` ORDER BY i.id LIMIT ?`, append(args, MaxGroup+1)...)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != expect {
		return 0, ErrScope
	}
	items, err := loadOpen(tx, f.Jobs, ids)
	if err != nil {
		return 0, err
	}
	return record(tx, request, items, a, ctxFor)
}

// Revoke withdraws the effective decision of an item with a new history row; the
// original stays. Revoking an item without an effective decision is a no-op.
func (Repository) Revoke(tx store.Tx, request string, jobs []string, id string) (bool, error) {
	if request == "" || len(request) > 128 || id == "" || len(jobs) == 0 {
		return false, ErrInvalid
	}
	t, sc, err := scope(tx)
	if err != nil {
		return false, err
	}
	var job string
	if err = t.QueryRow("SELECT job FROM exceptions_item WHERE id=?", id).Scan(&job); err != nil {
		return false, ErrNotFound
	}
	ok := false
	for _, j := range jobs {
		ok = ok || j == job
	}
	if !ok {
		return false, ErrNotFound
	}
	var seq int64
	switch err = t.QueryRow(`SELECT d.seq FROM exceptions_decision d WHERE d.item=? AND d.revokes IS NULL
		AND NOT EXISTS (SELECT 1 FROM exceptions_decision r WHERE r.revokes=d.seq) ORDER BY d.seq DESC LIMIT 1`, id).Scan(&seq); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	res, err := sc.Exec(`INSERT OR IGNORE INTO exceptions_decision(request, item, action, consequence, revokes)
		SELECT ?, item, action, consequence, seq FROM exceptions_decision WHERE seq=?`, request, seq)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	return true, bumpJobs(tx, map[string]bool{job: true})
}

// Decisions returns the effective decision of every item of a job; for an item
// decided several times the latest wins.
func (Repository) Decisions(tx store.Tx, job string) (map[string]Decision, error) {
	if job == "" {
		return nil, ErrInvalid
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT d.seq, d.item, d.action, d.consequence FROM exceptions_decision d JOIN exceptions_item i ON i.id=d.item
		WHERE i.job=? AND d.revokes IS NULL AND NOT EXISTS (SELECT 1 FROM exceptions_decision r WHERE r.revokes=d.seq) ORDER BY d.seq`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Decision{}
	for rows.Next() {
		var d Decision
		var a, c string
		if err = rows.Scan(&d.Seq, &d.Item, &a, &c); err != nil {
			return nil, err
		}
		d.Action, d.Consequence = ActionKind(a), Consequence(c)
		out[d.Item] = d
	}
	return out, rows.Err()
}

// Open counts a job's unresolved items that have no effective decision. A job
// with any is not ready for delivery.
func (Repository) Open(tx store.Tx, job string) (int, error) {
	if job == "" {
		return 0, ErrInvalid
	}
	t, _, err := scope(tx)
	if err != nil {
		return 0, err
	}
	var n int
	err = t.QueryRow(`SELECT COUNT(*) FROM exceptions_item i WHERE i.job=? AND i.resolved=0 AND NOT `+effectiveDecision, job).Scan(&n)
	return n, err
}

// Purge deletes a job's items and their decision history.
func (Repository) Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrInvalid
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM exceptions_decision WHERE item IN (SELECT id FROM exceptions_item WHERE job=?)",
		"DELETE FROM exceptions_item WHERE job=?", "DELETE FROM exceptions_version WHERE job=?"} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}
