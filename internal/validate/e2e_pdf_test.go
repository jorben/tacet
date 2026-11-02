//go:build darwin

package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/testenv"
)

const pdfInput = `
import sys
sys.path.insert(0, sys.argv[1])
from pdf_fixtures import build
open(sys.argv[2], 'wb').write(build(['Call 13812345678 today', 'Second 13900000000 page'], info={'Title': 'Quarterly'}))
`

func pdfRig(t *testing.T) *rig {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	file := filepath.Join(t.TempDir(), "in.pdf")
	if out, err := exec.Command(python, "-X", "utf8", "-c", pdfInput, filepath.Join(root, "python", "tests"), file).CombinedOutput(); err != nil {
		testenv.Missing(t, "the fixture, which the plugin interpreter builds: %v %s", err, out)
	}
	raw, _ := os.ReadFile(file)
	r := newRigFor(t, raw, "tacet_format_pdf", "tacet.pdf")
	r.noText = true
	return r
}

// A real PDF goes through the whole chain: extraction and detection, a plan from the findings plus the
// removal of the metadata, the rasterising write, the pixel-level validation and the re-detection of
// a candidate that has no text layer.
func TestRealPDFIsRedactedRasterisedAndValidated(t *testing.T) {
	r := pdfRig(t)
	r.extractInput()
	if len(r.found) != 2 {
		t.Fatalf("input findings: %+v", r.found)
	}
	for _, f := range r.found {
		if u := r.units[f.Unit]; !strings.HasPrefix(u.Part, "page/") {
			t.Fatalf("%+v", u)
		}
	}
	p := r.planFor([]int{0, 1}, "placeholder")
	meta, err := plan.Build("in", "res", "r", append(specsOf(p), plan.Spec{Kind: plan.PartOp, Unit: "document/metadata", Action: "remove_part"}))
	if err != nil {
		t.Fatal(err)
	}
	c := r.exportPlan(meta, fixed("[手机号]"))
	res, err := r.validate(c, meta, fixed("[手机号]"), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conclusion != Passed || res.Mismatches != 0 || res.Residuals != 0 || res.NewFindings != 0 {
		t.Fatalf("%+v", res)
	}
	// what cannot be checked on an image-only output is said, not hidden
	var images int
	for _, u := range res.Unverified {
		if strings.HasSuffix(u.Scope, "/images") && u.Reason == "CANDIDATE_PART_NOT_RESCANNED" {
			images++
		}
	}
	if images != 2 {
		t.Fatalf("the raster pages' image content must be listed as not rescanned: %+v", res.Unverified)
	}
	// the very same candidate checked against the untouched input's plan of nothing is not a pass:
	// the numbers are in the plan, and a candidate made without them would show them
	bare, err := plan.Build("in", "res", "r", nil)
	if err != nil {
		t.Fatal(err)
	}
	c0 := r.exportPlan(bare, fixed(""))
	full, err := export.Stage(r.refs, r.att("e2"), "in1", p, fixed("[手机号]"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	res0, err := r.validateWith(c0, p, fixed("[手机号]"), true, full.PlanResource, false)
	if err != nil {
		t.Fatal(err)
	}
	if res0.Conclusion != Widened || res0.Residuals != 2 {
		t.Fatalf("a rasterised page with nothing painted must show its residuals: %+v", res0)
	}
}

func specsOf(p plan.Plan) []plan.Spec {
	var out []plan.Spec
	for _, o := range p.Operations {
		out = append(out, o.Spec)
	}
	return out
}
