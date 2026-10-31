//go:build darwin || windows

package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/verdict"
)

const secret = "13812345678"
const digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func file() File {
	return File{ID: "f0001", AppVersion: "0.1.0", Components: map[string]string{"extract": "tacet.docx@0.1.0", "write": "tacet.docx@0.1.0"},
		ModelVersion: "astrlink-guard@1", RuleSetVersion: "cn-office-v1", RulePackVersions: []string{"b@2", "a@1"}, PolicyDigest: "ab12", Status: verdict.CompletedPerPolicy,
		Stats: Stats{Auto: 5, HumanItem: 2, HumanBulk: 3, BelowMinConfidence: 4, MinConfidence: 0.8}, Coverage: Coverage{Inspected: 3, Gaps: 1, Retained: 1, OutOfScope: 2, OutOfScopeCategories: []string{"z", "a"}},
		Validation:  Validation{Status: "passed_within_declared_scope", Conclusion: "passed", Unverified: []string{"word/theme.xml", "custom/x.bin"}, Limits: []string{"RASTER_OUTPUT_HAS_NO_TEXT_LAYER"}},
		AutoWidened: 1, ErrorCodes: []string{"E2", "E1"}, SourceEncrypted: true, NoSandbox: true}
}

func TestTheGuardRefusesPathsAndMarkersAndNeverEchoesThem(t *testing.T) {
	g := NewGuard(secret, "")
	for _, bad := range []string{"/Users/jorben/a.docx", "see /home/x/y", `C:\Users\x\a.docx`, "d:/data/a", `\\server\share\a`, "file:///tmp/a", "in /tmp/x", "x " + secret + " y", "bad\xff"} {
		err := g.Check(bad)
		if !errors.Is(err, ErrSensitive) || strings.Contains(err.Error(), "jorben") || strings.Contains(err.Error(), secret) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	for _, ok := range []string{"word/document.xml", "tacet.docx@0.1.0", "passed_within_declared_scope", "a/b", "文档", "50/50", "http-like text", ""} {
		if err := g.Check(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	if NewGuard().Check("anything") != nil {
		t.Error("a guard without markers refuses plain text")
	}
	if err := g.Check("fine", "also fine", "oops "+secret); err == nil {
		t.Error("only the first string was checked")
	}
}

func TestTheJSONReportIsDeterministicSortedAndStatesItsLimits(t *testing.T) {
	g := NewGuard(secret)
	a, err := file().JSON(g)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := file().JSON(g)
	if string(a) != string(b) {
		t.Fatal("not deterministic")
	}
	s := string(a)
	for _, want := range []string{`"schema": "tacet.report/1"`, `"min_confidence": 0.8`, `"below_min_confidence": 4`, `"retained_by_decision": 1`, `"SOURCE_ENCRYPTED_OUTPUT_PLAIN"`, `"OS_SANDBOX_NOT_ENABLED"`,
		`"PARTIAL_RESULT_UNCHECKED_CONTENT_KEPT"`, `"RASTER_OUTPUT_HAS_NO_TEXT_LAYER"`, `"auto_widened": 1`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Index(s, "a@1") > strings.Index(s, "b@2") || strings.Index(s, `"E1"`) > strings.Index(s, `"E2"`) || strings.Index(s, "custom/x.bin") > strings.Index(s, "word/theme.xml") {
		t.Error("lists are not sorted")
	}
	if strings.Contains(s, secret) || strings.Contains(s, "/Users") {
		t.Error("sensitive text in the report")
	}
	f := file()
	f.SourceEncrypted, f.NoSandbox, f.Coverage.Retained, f.Status = false, false, 0, verdict.Delivered
	c, _ := f.JSON(g)
	if strings.Contains(string(c), "SOURCE_ENCRYPTED") || strings.Contains(string(c), "OS_SANDBOX") || strings.Contains(string(c), "PARTIAL_RESULT") {
		t.Errorf("notices that do not apply: %s", c)
	}
	f.Status = verdict.PartialResult
	if d, _ := f.JSON(g); !strings.Contains(string(d), "PARTIAL_RESULT") {
		t.Error("a partial result without its notice")
	}
}

func TestTheReportRefusesWhatCouldLeakOrWhatIsIncomplete(t *testing.T) {
	g := NewGuard(secret)
	for name, edit := range map[string]func(*File){
		"source name as id":     func(f *File) { f.ID = "report of 张三.docx" },
		"path as id":            func(f *File) { f.ID = "/Users/x/a" },
		"no id":                 func(f *File) { f.ID = "" },
		"no status":             func(f *File) { f.Status = "" },
		"no version":            func(f *File) { f.AppVersion = "" },
		"marker in a scope":     func(f *File) { f.Validation.Unverified = []string{"x " + secret} },
		"path in a limit":       func(f *File) { f.Validation.Limits = []string{"cannot open /Users/x/a.docx"} },
		"path in an error code": func(f *File) { f.ErrorCodes = []string{`C:\x\y`} },
		"marker in a component": func(f *File) { f.Components = map[string]string{"extract": secret} },
		"marker in a category":  func(f *File) { f.Coverage.OutOfScopeCategories = []string{secret} },
	} {
		f := file()
		edit(&f)
		if _, err := f.JSON(g); err == nil {
			t.Errorf("%s: JSON accepted", name)
		}
		if _, err := f.Summary(g); err == nil {
			t.Errorf("%s: summary accepted", name)
		}
	}
}

func TestTheSummaryNeverCallsAKeptGapCheckedOrFilteredCandidatesHarmless(t *testing.T) {
	s, err := file().Summary(NewGuard(secret))
	if err != nil {
		t.Fatal(err)
	}
	out := string(s)
	for _, want := range []string{"1 kept by decision and therefore still unchecked", "4 candidates below it were not handled (this is not a statement that they are not sensitive)",
		"not verified: custom/x.bin", "limit: RASTER_OUTPUT_HAS_NO_TEXT_LAYER", "automatic widening applied: 1", "notice: SOURCE_ENCRYPTED_OUTPUT_PLAIN", "error: E1", "automatic 5, one by one 2, in bulk 3, sampled 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	f := file()
	f.Stats.MinConfidence, f.AutoWidened, f.ErrorCodes = 0, 0, nil
	s, _ = f.Summary(NewGuard())
	if strings.Contains(string(s), "minimum confidence") || strings.Contains(string(s), "widening") || strings.Contains(string(s), "error:") {
		t.Errorf("lines that do not apply:\n%s", s)
	}
}

func entries() []Entry {
	return []Entry{
		{ID: "f2", OutputRelPath: "out/b.docx", OutputDigest: digest, Status: verdict.Delivered},
		{ID: "f1", OutputRelPath: "out/a.docx", OutputDigest: digest, Status: verdict.PartialResult, Partial: true},
		{ID: "f3", Status: verdict.Blocked},
		{ID: "f4", Status: verdict.Unprocessable},
	}
}

func TestTheManifestListsEveryFileByNeutralIdWithRelativeOutputsOnly(t *testing.T) {
	b, err := Manifest("batch1", entries(), NewGuard(secret))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Index(s, `"f1"`) > strings.Index(s, `"f2"`) || strings.Index(s, `"f3"`) > strings.Index(s, `"f4"`) {
		t.Error("not sorted by id")
	}
	for _, want := range []string{`"summary": "1/4 delivered"`, `"partial": true`, `"status": "blocked"`, `"output": "out/a.docx"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
	again, _ := Manifest("batch1", entries(), NewGuard(secret))
	if string(again) != s {
		t.Error("not deterministic")
	}
}

func TestTheManifestRefusesAbsolutePathsDuplicatesAndInconsistentRows(t *testing.T) {
	with := func(edit func([]Entry) []Entry) error {
		_, err := Manifest("batch1", edit(entries()), NewGuard(secret))
		return err
	}
	for name, edit := range map[string]func([]Entry) []Entry{
		"absolute output":      func(e []Entry) []Entry { e[0].OutputRelPath = "/Users/x/out.docx"; return e },
		"backslash root":       func(e []Entry) []Entry { e[0].OutputRelPath = `\\srv\share\a`; return e },
		"drive output":         func(e []Entry) []Entry { e[0].OutputRelPath = `C:\out.docx`; return e },
		"parent escape":        func(e []Entry) []Entry { e[0].OutputRelPath = "../x.docx"; return e },
		"delivered no output":  func(e []Entry) []Entry { e[0].OutputRelPath = ""; return e },
		"delivered no digest":  func(e []Entry) []Entry { e[0].OutputDigest = ""; return e },
		"malformed digest":     func(e []Entry) []Entry { e[0].OutputDigest = "sha256:xyz"; return e },
		"blocked with output":  func(e []Entry) []Entry { e[2].OutputRelPath = "out/c.docx"; return e },
		"blocked with digest":  func(e []Entry) []Entry { e[2].OutputDigest = digest; return e },
		"duplicate id":         func(e []Entry) []Entry { e[1].ID = "f2"; return e },
		"source name as id":    func(e []Entry) []Entry { e[0].ID = "张三的合同.docx"; return e },
		"no status":            func(e []Entry) []Entry { e[0].Status = ""; return e },
		"marker in the output": func(e []Entry) []Entry { e[0].OutputRelPath = "out/" + secret + ".docx"; return e },
	} {
		if err := with(edit); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := Manifest("bad batch/id", entries(), NewGuard()); err == nil {
		t.Error("batch id")
	}
	if _, err := Manifest("b", nil, NewGuard()); err != nil {
		t.Errorf("an empty batch is a valid manifest: %v", err)
	}
}

func TestReportsAreWrittenOnceThroughThePublishWriterAndReplayIsHarmless(t *testing.T) {
	dir := t.TempDir()
	w := publish.Artifacts{Dir: dir}
	names, err := Write(context.Background(), w, file(), NewGuard(secret))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(names[0], ".report.json") || !strings.HasSuffix(names[1], ".report.txt") || !strings.HasPrefix(names[0], "tacet-f0001-") {
		t.Fatalf("%v", names)
	}
	again, err := Write(context.Background(), w, file(), NewGuard(secret))
	if err != nil || again != names {
		t.Fatalf("replay: %v %v", again, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 2 {
		t.Fatalf("%d files", len(es))
	}
	data, _ := os.ReadFile(filepath.Join(dir, names[1]))
	if !strings.Contains(string(data), "status: completed_per_policy") {
		t.Fatalf("%s", data)
	}
	// a changed report is a new file, never an overwrite
	f := file()
	f.Stats.Auto = 99
	other, err := Write(context.Background(), w, f, NewGuard(secret))
	if err != nil || other[0] == names[0] {
		t.Fatalf("%v %v", other, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 4 {
		t.Fatalf("%d files", len(es))
	}
	// a sensitive report writes nothing
	bad := file()
	bad.Validation.Limits = []string{secret}
	before, _ := os.ReadDir(dir)
	if _, err = Write(context.Background(), w, bad, NewGuard(secret)); err == nil {
		t.Fatal("sensitive report written")
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Fatal("a refused report left a file")
	}
	if _, err = Write(context.Background(), nil, file(), NewGuard()); err == nil {
		t.Error("no writer")
	}
	if _, err = ArtifactName("report.exe", "f1", nil); err == nil {
		t.Error("unknown kind")
	}
}

func TestEveryFileTheReportsWriteIsFreeOfTheInjectedMarker(t *testing.T) {
	dir := t.TempDir()
	w := publish.Artifacts{Dir: dir}
	Write(context.Background(), w, file(), NewGuard(secret))
	m, _ := Manifest("batch1", entries(), NewGuard(secret))
	n, _ := ArtifactName("manifest.json", "batch1", m)
	if err := w.WriteArtifact(context.Background(), n, m); err != nil {
		t.Fatal(err)
	}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), secret) || strings.Contains(string(b), dir) || strings.Contains(string(b), "/Users/") {
			t.Errorf("%s leaks", e.Name())
		}
	}
}
