//go:build darwin

package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/plan"
)

const xlsxInput = `
import sys
sys.path.insert(0, sys.argv[1])
from test_xlsx_adapter import cell, CORE, CORE_REL
from test_xlsx_value_subset import book
rows = '<row r="1">' + cell('A1', 's', 0) + cell('B1', inline='备用 13900000000') + cell('C1', 's', 1) + '</row>'
extra = {CORE[0]: (b'<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"/>', CORE[1])}
open(sys.argv[2], 'wb').write(book([('One', 'visible', rows)], ['<t>电话 13812345678</t>', '<t>无敏感</t>'], extra=extra, root_rels=CORE_REL))
`

func xlsxFixture(t *testing.T) []byte {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := filepath.Join(root, "python", ".venv", "bin", "python")
	if _, err := os.Stat(python); err != nil {
		t.Skip("needs uv sync --all-packages")
	}
	file := filepath.Join(t.TempDir(), "in.xlsx")
	if out, err := exec.Command(python, "-X", "utf8", "-c", xlsxInput, filepath.Join(root, "python", "tests"), file).CombinedOutput(); err != nil {
		t.Skipf("cannot build the fixture: %v %s", err, out)
	}
	raw, _ := os.ReadFile(file)
	return raw
}

type textCase struct {
	name, module, plugin string
	input                func(t *testing.T) []byte
	extra                []plan.Spec // operations besides the findings' (property removal)
	wantFindings         int
}

var textCases = []textCase{
	{"txt", "tacet_format_text", "tacet.text", func(*testing.T) []byte {
		return []byte("联系人 13812345678 结束\n无敏感\r\n电话 13900000000\n")
	}, nil, 2},
	{"csv", "tacet_format_csv", "tacet.csv", func(*testing.T) []byte {
		return []byte("name,phone\n张三,13812345678\n李四,\"call 13900000000 now\"\n")
	}, nil, 2},
	{"json", "tacet_format_json", "tacet.json", func(*testing.T) []byte {
		return []byte(`{"a":"13812345678 x","b":["y","13900000000"]}`)
	}, nil, 2},
	{"xlsx", "tacet_format_xlsx", "tacet.xlsx", xlsxFixture,
		[]plan.Spec{{Kind: plan.PartOp, Unit: "docProps/core.xml", Action: "remove_part"}}, 2},
}

func (c textCase) rig(t *testing.T) *rig {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(root, "python", ".venv", "bin", "python")); err != nil {
		t.Skip("needs uv sync --all-packages")
	}
	r := newRigFor(t, c.input(t), c.module, c.plugin)
	r.extractInput()
	if len(r.found) != c.wantFindings {
		t.Fatalf("%s: input findings %+v", c.name, r.found)
	}
	return r
}

func (c textCase) planOf(r *rig, which []int) plan.Plan {
	p := r.planFor(which, "placeholder")
	if len(c.extra) == 0 {
		return p
	}
	p, err := plan.Build("in", "res", "r", append(specsOf(p), c.extra...))
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

// Every admitted text-family plugin takes a real file through the whole chain.
func TestRealTextFamilyPluginsRedactAndValidate(t *testing.T) {
	for _, c := range textCases {
		t.Run(c.name, func(t *testing.T) {
			r := c.rig(t)
			p := c.planOf(r, []int{0, 1})
			cand := r.exportPlan(p, fixed("[手机号]"))
			res, err := r.validate(cand, p, fixed("[手机号]"), true)
			if err != nil {
				t.Fatal(err)
			}
			if res.Conclusion != Passed || res.Mismatches != 0 || res.Residuals != 0 || res.NewFindings != 0 || res.Generated != 0 {
				t.Fatalf("%+v", res)
			}
		})
	}
}

func TestRealTextFamilyNewHitIsLocatedAtItsInputPosition(t *testing.T) {
	for _, c := range textCases {
		t.Run(c.name, func(t *testing.T) {
			r := c.rig(t)
			p := c.planOf(r, []int{0}) // the second number stays in the output
			cand := r.exportPlan(p, fixed("[手机号]"))
			res, err := r.validate(cand, p, fixed("[手机号]"), false)
			if err != nil {
				t.Fatal(err)
			}
			if res.Conclusion != NewFindings || res.NewFindings != 1 || res.Generated != 0 {
				t.Fatalf("%+v", res)
			}
			post, second := r.post(), r.found[1]
			if len(post) != 1 || post[0].Unit != second.Unit || post[0].Start != second.Start || post[0].End != second.End || post[0].Group != second.Group {
				t.Fatalf("the new finding must sit where the number is in the input: %+v want %+v", post, second)
			}
		})
	}
}

func TestRealTextFamilyIncompleteCandidateLeavesResiduals(t *testing.T) {
	for _, c := range textCases {
		t.Run(c.name, func(t *testing.T) {
			r := c.rig(t)
			partial := c.planOf(r, []int{0})
			cand := r.exportPlan(partial, fixed("[手机号]"))
			full := c.planOf(r, []int{0, 1})
			st, err := export.Stage(r.refs, r.att("e2"), "in1", full, fixed("[手机号]"), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			res, err := r.validateWith(cand, full, fixed("[手机号]"), true, st.PlanResource, false)
			if err != nil {
				t.Fatal(err)
			}
			if res.Conclusion != Widened || res.Residuals != 1 || res.Status != StatusFailed {
				t.Fatalf("%+v", res)
			}
		})
	}
}

// A number stored as a number cannot become text without authorisation: the export fails and names the reason.
func TestRealXLSXNumberCellFindingFailsTheExportInsteadOfBeingIgnored(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := filepath.Join(root, "python", ".venv", "bin", "python")
	if _, err := os.Stat(python); err != nil {
		t.Skip("needs uv sync --all-packages")
	}
	script := `
import sys
sys.path.insert(0, sys.argv[1])
from test_xlsx_adapter import cell
from test_xlsx_value_subset import book
open(sys.argv[2], 'wb').write(book([('One', 'visible', '<row r="1">' + cell('A1', v=13812345678) + '</row>')], []))
`
	file := filepath.Join(t.TempDir(), "n.xlsx")
	if out, err := exec.Command(python, "-X", "utf8", "-c", script, filepath.Join(root, "python", "tests"), file).CombinedOutput(); err != nil {
		t.Skipf("%v %s", err, out)
	}
	raw, _ := os.ReadFile(file)
	r := newRigFor(t, raw, "tacet_format_xlsx", "tacet.xlsx")
	r.extractInput()
	if len(r.found) != 1 {
		t.Fatalf("a phone number stored as a number must be found: %+v", r.found)
	}
	p := r.planFor([]int{0}, "placeholder")
	a := r.att("e1")
	st, err := export.Stage(r.refs, a, "in1", p, fixed("[手机号]"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	w := r.launch(st.Files)
	ctx, cancel := bg()
	defer cancel()
	_, failed, err := r.exp.Export(ctx, w.Session, a, st, p)
	if err == nil || len(failed) != 1 || failed[0].Reason != "PART_OPERATION_UNSUPPORTED" {
		t.Fatalf("%v %+v", err, failed)
	}
}
