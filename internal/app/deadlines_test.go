package app

import (
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
)

func TestEachStageHasItsOwnDeadlineGrowingWithTheInputUpToACeiling(t *testing.T) {
	const mib = 1 << 20
	for _, c := range []struct {
		kind kernel.StageKind
		size int64
		want time.Duration
	}{
		{kernel.StagePreflight, 0, time.Minute}, {kernel.StagePreflight, 10 * mib, time.Minute + 20*time.Second},
		{kernel.StagePreflight, 1 << 40, 10 * time.Minute}, {kernel.StagePreflight, 270 * mib, 10 * time.Minute},
		{kernel.StagePreflight, 271 * mib, 10 * time.Minute}, {kernel.StagePreflight, 269 * mib, 9*time.Minute + 58*time.Second},
		{kernel.StageExtractDetect, 3 * mib, 3 * time.Minute}, {kernel.StageExport, 4 * mib, 4 * time.Minute},
		{kernel.StageValidate, mib - 1, 2 * time.Minute}, {kernel.StageExport, 1 << 40, 2 * time.Hour},
		{kernel.StageResolve, 50 * mib, 0}, {kernel.StagePublish, 50 * mib, 0},
	} {
		if got := stageDeadline(c.kind, c.size); got != c.want {
			t.Errorf("%s %d: %v, want %v", c.kind, c.size, got, c.want)
		}
	}
}
