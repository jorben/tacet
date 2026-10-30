package lineage

import "testing"

// Every upstream stage must itself be in the table, so staleness can be
// followed from any stage back to the input.
func TestTableUpstreamsAreKnown(t *testing.T) {
	for stage, d := range Table {
		for _, up := range d.Upstream {
			if _, ok := Table[up]; !ok {
				t.Errorf("%s depends on %s, which has no entry", stage, up)
			}
		}
	}
}
