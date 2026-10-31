package findings

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// Scale measurement for AR-11, run with TACET_SCALE=1 (it takes a few seconds
// and prints timings; it asserts only that queries stay bounded, not a speed).
func TestScaleOneHundredThousandFindings(t *testing.T) {
	if os.Getenv("TACET_SCALE") == "" {
		t.Skip("set TACET_SCALE=1")
	}
	f := setup(t)
	started := time.Now()
	const jobs, perJob = 200, 500
	for j := 0; j < jobs; j++ {
		var items []New
		for i := 0; i < perJob; i++ {
			cat := []string{"phone", "person", "email", "address", "account"}[i%5]
			items = append(items, item(fmt.Sprintf("u%d", i), i*5, cat, kernel.SourceRule, fmt.Sprintf("value-%d", (j*perJob+i)%2000)))
		}
		job, attempt := fmt.Sprintf("j%d", j), fmt.Sprintf("a%d", j)
		f.add(job, attempt, "b1", items...)
		f.seal(job, attempt, "b1")
	}
	t.Logf("inserted %d findings in %v", jobs*perJob, time.Since(started).Round(time.Millisecond))
	measure := func(name string, fn func() int) {
		s := time.Now()
		n := fn()
		t.Logf("%-34s %6d rows %v", name, n, time.Since(s).Round(time.Microsecond))
	}
	measure("page 200 from the start", func() int { return len(f.page(Filter{Batch: "b1"}, 0, 200)) })
	measure("page 200 deep (after id 90000)", func() int { return len(f.page(Filter{Batch: "b1"}, 90000, 200)) })
	measure("page 200 filtered phone", func() int { return len(f.page(Filter{Batch: "b1", Categories: []string{"phone"}}, 50000, 200)) })
	measure("page 200 of one job", func() int { return len(f.page(Filter{Job: "j17"}, 0, 200)) })
	measure("groups first 200", func() int {
		var out []Group
		f.do(func(tx store.Tx) (err error) {
			out, err = f.repo.Groups(tx, Filter{Batch: "b1"}, GroupKey{}, 200)
			return err
		})
		return len(out)
	})
	measure("groups 200 deep", func() int {
		var out []Group
		f.do(func(tx store.Tx) (err error) {
			out, err = f.repo.Groups(tx, Filter{Batch: "b1"}, GroupKey{Category: "person", Source: "rule", Field: "body", Template: "t1", Value: "8"}, 200)
			return err
		})
		return len(out)
	})
}
