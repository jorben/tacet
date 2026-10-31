package policy

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/review"
)

// TACET_SCALE=N runs one resolution over N findings spread across N/10 units
// with a batch decision over half of them, and reports the time. It is a
// measurement, not a gate: the numbers go into the evidence.
func TestScaleOfOneResolution(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("TACET_SCALE"))
	if n == 0 {
		t.Skip("set TACET_SCALE=<findings>")
	}
	f := ssetup(t)
	f.rev.Chunk = findings.MaxPage
	t0 := time.Now()
	for done := 0; done < n; {
		var specs []findings.New
		for i := 0; i < 5000 && done < n; i++ {
			unit := "u" + strconv.Itoa(done/10)
			specs = append(specs, findings.New{Unit: unit, TextVersion: 1, Start: (done % 10) * 8, End: (done%10)*8 + 4, Category: "phone", Source: kernel.SourceRule, Score: 1, Field: "b", Template: "t", Value: []byte("v" + strconv.Itoa(done))})
			done++
		}
		f.detect("j1", specs...)
	}
	f.ledger("j1", textPart("doc"))
	t.Logf("seeded %d findings in %v", n, time.Since(t0))
	t0 = time.Now()
	if _, _, err := f.rev.DecideBulk(context.Background(), review.BulkRequest{Request: "bulk", Batch: "b1", Keep: true,
		Scope: review.ScopeSpec{Kind: review.ScopeFilter, Filter: findings.Filter{Job: "j1", Categories: []string{"phone"}, Units: nil}}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("bulk decision over %d: %v", n, time.Since(t0))
	t0 = time.Now()
	r, err := f.svc.Run(context.Background(), req("j1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resolve: %v, %d dispositions, attempts %d", time.Since(t0), r.Dispositions, r.Attempts)
}
