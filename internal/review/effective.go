package review

import (
	"strings"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/store"
)

// Applied is the decision that applies to one finding.
type Applied struct {
	Decision string
	Keep     bool // false: redact
	Bulk     bool // false: a single decision
	seq      int64
}

type target struct {
	job, unit  string
	version    uint64
	start, end int
	category   string
}

// Effective resolves, for a chunk of findings, the decision that applies to each
// (module design 7.3 items 3 and 5):
//
//   - a single decision beats a batch decision; among decisions of the same kind
//     the last committed one wins; pending decisions and revoked ones are ignored;
//   - a decision applies to a finding only if the finding's text unit version,
//     span and category equal those of a member recorded when the decision was
//     made. A finding at a new position, even with the same value, inherits
//     nothing (BUX-05), and a finding added after the decision is not a member
//     (BUX-06), while the same hit re-detected under a later result version keeps
//     its decision (INV-04).
//
// The map is keyed by finding id; findings without a decision are absent.
func Effective(tx store.Tx, fs []findings.Finding) (map[int64]Applied, error) {
	out := map[int64]Applied{}
	if len(fs) == 0 {
		return out, nil
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	byJob := map[string][]string{}
	seenUnit := map[[2]string]bool{}
	for _, f := range fs {
		if k := [2]string{f.Job, f.Unit}; !seenUnit[k] {
			seenUnit[k] = true
			byJob[f.Job] = append(byJob[f.Job], f.Unit)
		}
	}
	best := map[target]Applied{}
	for job, units := range byJob {
		for len(units) > 0 {
			n := min(len(units), 400)
			args := []any{job}
			for _, u := range units[:n] {
				args = append(args, u)
			}
			rows, err := t.Query(`SELECT m.unit, m.text_version, m.start, m.stop, m.category, d.id, d.kind, d.keep, d.seq
				FROM review_member m JOIN review_decision d ON d.id=m.decision
				WHERE m.job=? AND m.unit IN (?`+strings.Repeat(",?", n-1)+`) AND d.state='committed' AND d.revokes IS NULL
				AND NOT EXISTS (SELECT 1 FROM review_decision r WHERE r.revokes=d.id AND r.state='committed')`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var k target
				var e Applied
				var kind string
				var keep int
				k.job = job
				if err = rows.Scan(&k.unit, &k.version, &k.start, &k.end, &k.category, &e.Decision, &kind, &keep, &e.seq); err != nil {
					rows.Close()
					return nil, err
				}
				e.Keep, e.Bulk = keep != 0, kind == "bulk"
				if cur, ok := best[k]; !ok || beats(e, cur) {
					best[k] = e
				}
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
			units = units[n:]
		}
	}
	for _, f := range fs {
		if e, ok := best[target{f.Job, f.Unit, f.TextVersion, f.Start, f.End, f.Category}]; ok {
			out[f.ID] = e
		}
	}
	return out, nil
}

func beats(a, b Applied) bool {
	if a.Bulk != b.Bulk {
		return !a.Bulk
	}
	return a.seq > b.seq
}
