package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// The screens show FileStatus.Categories as the places handled ("N 涂抹", "命中 N 处"). A number the rules and
// the model both report is two candidates but one place in the output, so it counts once. The model's
// candidate is stored directly, so the test needs no model files.
func TestANumberTheRuleAndTheModelBothReportCountsOnce(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	p := filepath.Join(in, "a.txt")
	os.WriteFile(p, []byte("电话 13812345678 邮箱 a@b.cn\n"), 0o600)
	ctx := t.Context()
	st, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID}); err != nil { // no authorization: the file stays ready
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	job := st.Files[0].ID
	fs, err := e.Findings(ctx, job, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var rule *Finding
	for i := range fs {
		if fs[i].Category == "phone" && fs[i].Source == string(kernel.SourceRule) {
			rule = &fs[i]
		}
	}
	if rule == nil || st.Files[0].Categories["phone"] != 1 {
		t.Fatalf("the rules must report the number once: %+v %+v", fs, st.Files[0])
	}
	// the model reports the same number
	a := kernel.AttemptRef{JobID: job, AttemptID: "model-" + job}
	if err = e.ws.Commit(ctx, func(tx store.Tx) error {
		if _, err := e.fr.Add(tx, a, st.ID, findings.StageSupplement, []findings.New{{Unit: rule.Unit, Start: rule.Start, End: rule.End,
			Category: "phone", Source: kernel.SourceModel, Score: 0.97}}); err != nil {
			return err
		}
		_, err := e.fr.Seal(tx, a, st.ID, findings.StageSupplement)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fs, err = e.Findings(ctx, job, 0, 10); err != nil || len(fs) != 3 {
		t.Fatalf("the number must now be two candidates (and the address one): %+v %v", fs, err)
	}
	rows, err := e.Files(ctx, st.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if f := rows[0]; f.Categories["phone"] != 1 || f.Categories["email"] != 1 || f.Findings != 2 {
		t.Fatalf("one number and one address are two places: phone %d, email %d, findings %d", f.Categories["phone"], f.Categories["email"], f.Findings)
	}
}
