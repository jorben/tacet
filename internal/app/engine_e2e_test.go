//go:build darwin

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jorben/tacet/internal/lifecycle"
	"github.com/jorben/tacet/internal/platform"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/jorben/tacet/internal/keys"
)

type memKey struct{ b []byte }

func (m *memKey) Load() ([]byte, error) {
	if m.b == nil {
		return nil, keys.ErrKeyNotFound
	}
	return append([]byte(nil), m.b...), nil
}
func (m *memKey) Save(k []byte) error { m.b = append([]byte(nil), k...); return nil }
func (m *memKey) Delete() error       { m.b = nil; return nil }

func root(t *testing.T) (string, string) {
	t.Helper()
	r, _ := filepath.Abs(filepath.Join("..", ".."))
	py := filepath.Join(r, "python", ".venv", "bin", "python")
	if _, err := os.Stat(py); err != nil {
		t.Skip("needs uv sync --all-packages")
	}
	return r, py
}

func builtin(t *testing.T, r, dir, module string, formats ...string) Builtin {
	t.Helper()
	m, err := os.ReadFile(filepath.Join(r, "python", "plugins", dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return Builtin{Module: module, Manifest: m, Formats: formats}
}

func openEngine(t *testing.T, dir string, key *memKey, create bool) *Engine {
	t.Helper()
	return openEngineWith(t, dir, key, create, nil)
}

// openEngineWith opens an engine; tune may adjust the options (the model, say) before opening.
func openEngineWith(t *testing.T, dir string, key *memKey, create bool, tune func(*Options)) *Engine {
	t.Helper()
	r, py := root(t)
	pack, err := os.ReadFile(filepath.Join(r, "resources", "rules", "cn-office-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Dir: dir, UserKey: key, Python: py, EnvDigest: "sha256:" + strings.Repeat("e", 64), RulePack: pack, Create: create, Platform: "darwin-arm64",
		Plugins: []Builtin{builtin(t, r, "format-text", "tacet_format_text", "txt"), builtin(t, r, "format-csv", "tacet_format_csv", "csv"),
			builtin(t, r, "format-json", "tacet_format_json", "json"), builtin(t, r, "format-docx", "tacet_format_docx", "docx"),
			builtin(t, r, "format-pdf", "tacet_format_pdf", "pdf"), builtin(t, r, "format-xlsx", "tacet_format_xlsx", "xlsx")}}
	if tune != nil {
		tune(&opts)
	}
	e, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// modelOption is the fixed model when its files are here (the M0 resource folder); the test is skipped otherwise.
func modelOption(t *testing.T) *ModelOption {
	t.Helper()
	r, _ := root(t)
	var o Options
	locateModel(r, &o)
	if o.Model == nil {
		t.Skip("needs the fixed M0 model resource")
	}
	return o.Model
}

func TestBatchEndToEnd(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	key := &memKey{}
	e := openEngine(t, ws, key, true)
	defer e.Close()
	files := map[string]string{"a.txt": "联系人 13812345678 结束\n无敏感\n", "b.csv": "name,phone\n张三,13900000000\n", "c.txt": "无任何敏感内容\n"}
	var paths []string
	for _, n := range []string{"a.txt", "b.csv", "c.txt"} {
		p := filepath.Join(in, n)
		os.WriteFile(p, []byte(files[n]), 0o600)
		paths = append(paths, p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: paths, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	cur, _ := e.Status(ctx, st.ID, 50)
	t.Logf("before start: %+v", cur)
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	st, err = e.Wait(ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", st)
	for _, f := range st.Files {
		t.Logf("%+v", f)
	}
	entries, _ := os.ReadDir(out)
	for _, en := range entries {
		b, _ := os.ReadFile(filepath.Join(out, en.Name()))
		t.Logf("%s: %q", en.Name(), b[:min(len(b), 200)])
		if strings.Contains(string(b), "13812345678") || strings.Contains(string(b), "13900000000") {
			t.Fatalf("%s still holds the number", en.Name())
		}
	}
	if st.ExitCode != 0 {
		t.Fatalf("exit %d", st.ExitCode)
	}
	if !errors.Is(nil, nil) {
		t.Fatal()
	}
}

func TestReviewDecisionsLetTheFileContinue(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	p := filepath.Join(in, "r.txt")
	os.WriteFile(p, []byte("证件 110101199003074519 与 110101199003074518\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	f := st.Files[0]
	if f.State != "awaiting_review" || f.Pending != 2 || st.ExitCode != 2 {
		t.Fatalf("%+v", st)
	}
	// a second quick task cannot start while this one is unfinished
	if _, err = e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{p}, OutputDir: out}); err == nil || err.Error() != CodeQuickTaskActive {
		t.Fatalf("second quick task: %v", err)
	}
	fs, err := e.Findings(ctx, f.ID, 0, 50)
	if err != nil || len(fs) != 2 {
		t.Fatal(fs, err)
	}
	doc, err := e.Document(ctx, f.ID, 0, 10)
	if err != nil || len(doc) != 1 || len(doc[0].Findings) != 2 {
		t.Fatalf("document view: %+v %v", doc, err)
	}
	for _, d := range doc[0].Findings {
		u16 := utf16.Encode([]rune(doc[0].Text))
		if got := string(utf16.Decode(u16[d.Start:d.End])); len(got) != 18 || got[:6] != "110101" {
			t.Fatalf("finding %d points at %q", d.ID, got)
		}
	}
	if _, err = e.Document(ctx, f.ID, 0, 101); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unbounded page must be refused: %v", err)
	}
	// keep the first, redact the second
	if err = e.DecideFinding(ctx, f.ID, fs[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err = e.DecideFinding(ctx, f.ID, fs[1].ID, false); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.ExitCode != 0 || st.Files[0].State != "completed" {
		t.Fatalf("%+v", st)
	}
	raw, err := os.ReadFile(filepath.Join(out, st.Files[0].Output))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); st.Files[0].Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("the manifest digest %q is not the delivered file's", st.Files[0].Digest)
	}
	if !strings.Contains(string(raw), "110101199003074519") || strings.Contains(string(raw), "110101199003074518") {
		t.Fatalf("decisions not applied: %q", raw)
	}
}

const encryptedDocx = `
import sys
sys.path.insert(0, sys.argv[1])
from test_docx_extract import package
from test_docx_unlock import encrypted, sized
open(sys.argv[2], 'wb').write(encrypted(sized(package('<w:p><w:r><w:t>电话 13812345678</w:t></w:r></w:p>')), 'correct horse'))
`

func docxText(t *testing.T, path string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("%s is not a package: %v", path, err)
	}
	defer zr.Close()
	var all bytes.Buffer
	for _, f := range zr.File {
		rc, _ := f.Open()
		all.ReadFrom(rc)
		rc.Close()
	}
	return all.String()
}

func TestPasswordProtectedFileWaitsThenContinuesAfterUnlock(t *testing.T) {
	r, py := root(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	locked := filepath.Join(in, "enc.docx")
	if o, err := exec.Command(py, "-X", "utf8", "-c", encryptedDocx, filepath.Join(r, "python", "tests"), locked).CombinedOutput(); err != nil {
		t.Skipf("cannot build the fixture: %v %s", err, o)
	}
	plain := filepath.Join(in, "p.txt")
	os.WriteFile(plain, []byte("电话 13900000000\n"), 0o600)
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{locked, plain}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.Files[0].State != "blocked" || st.Files[0].Detail != "password_required" || st.Files[1].State != "completed" || st.ExitCode != 2 {
		t.Fatalf("%+v", st)
	}
	job := st.Files[0].ID
	if err = e.Unlock(ctx, job, []byte("wrong")); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.Files[0].Status != "blocked" || st.Files[0].Detail != "PASSWORD_INCORRECT" {
		t.Fatalf("a wrong password must leave the file waiting: %+v", st.Files[0])
	}
	pw := []byte("correct horse")
	if err = e.Unlock(ctx, job, pw); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pw, make([]byte, len(pw))) {
		t.Fatal("the caller's password was not wiped")
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.ExitCode != 0 || st.Files[0].State != "completed" {
		t.Fatalf("%+v", st)
	}
	text := docxText(t, filepath.Join(out, st.Files[0].Output))
	if strings.Contains(text, "13812345678") || !strings.Contains(text, "[手机号]") {
		t.Fatalf("the unlocked file was not redacted")
	}
	// the password is nowhere in the workspace
	filepath.Walk(ws, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("correct horse")) {
				t.Errorf("password found in %s", p)
			}
		}
		return nil
	})
	if err = e.Unlock(ctx, job, []byte("x")); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("unlocking a finished file: %v", err)
	}
}

const pdfFixture = `
import sys
sys.path.insert(0, sys.argv[1])
from pdf_fixtures import build
open(sys.argv[2], 'wb').write(build(['Call 13812345678 today', 'Second 13900000000 page'], info={'Title': 'Quarterly'}))
`

const xlsxFixture = `
import sys
sys.path.insert(0, sys.argv[1])
from test_xlsx_adapter import cell, CORE, CORE_REL
from test_xlsx_value_subset import book
rows = '<row r="1">' + cell('A1', 's', 0) + cell('B1', inline='备用 13900000000') + cell('C1', 's', 1) + '</row>'
extra = {CORE[0]: (b'<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"/>', CORE[1])}
open(sys.argv[2], 'wb').write(book([('One', 'visible', rows)], ['<t>电话 13812345678</t>', '<t>无敏感</t>'], extra=extra, root_rels=CORE_REL))
`

const plainDocx = `
import sys, zipfile
NS = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
with zipfile.ZipFile(sys.argv[1], 'w') as z:
    z.writestr('[Content_Types].xml', '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
    z.writestr('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="m" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
    z.writestr('word/document.xml', '<w:document xmlns:w="%s"><w:body><w:p><w:r><w:t>电话 13812345678 结束</w:t></w:r></w:p></w:body></w:document>' % NS)
`

func TestEveryBuiltinFormatRunsThroughTheEngine(t *testing.T) {
	r, py := root(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	fix := filepath.Join(r, "python", "tests")
	build := func(name, script string, withTests bool) string {
		p := filepath.Join(in, name)
		args := []string{"-X", "utf8", "-c", script}
		if withTests {
			args = append(args, fix)
		}
		if o, err := exec.Command(py, append(args, p)...).CombinedOutput(); err != nil {
			t.Skipf("cannot build %s: %v %s", name, err, o)
		}
		return p
	}
	var paths []string
	for _, f := range []struct {
		n, s string
		w    bool
	}{{"a.docx", plainDocx, false}, {"b.pdf", pdfFixture, true}, {"c.xlsx", xlsxFixture, true}} {
		paths = append(paths, build(f.n, f.s, f.w))
	}
	js := filepath.Join(in, "d.json")
	os.WriteFile(js, []byte(`{"a":"13812345678 x","b":["y"]}`), 0o600)
	paths = append(paths, js)

	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: paths, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	for _, f := range st.Files {
		t.Logf("%+v", f)
		if f.State != "completed" {
			t.Errorf("%s (%s) ended %s %s", f.Name, f.Format, f.State, f.Detail)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(out, f.Output))
		if err != nil || len(raw) == 0 {
			t.Errorf("%s: no output (%v)", f.Name, err)
		}
	}
	if st.ExitCode != 0 {
		t.Fatalf("exit %d", st.ExitCode)
	}
	if t.Failed() {
		return
	}
	// the text formats and the package formats no longer hold the number
	for _, f := range st.Files {
		if f.Format == "pdf" {
			continue // rasterised: no text layer to search
		}
		p := filepath.Join(out, f.Output)
		text := ""
		if f.Format == "docx" || f.Format == "xlsx" {
			text = docxText(t, p)
		} else {
			b, _ := os.ReadFile(p)
			text = string(b)
		}
		if strings.Contains(text, "13812345678") || strings.Contains(text, "13900000000") {
			t.Errorf("%s still holds a number", f.Output)
		}
	}
}

func TestNothingIsPublishedUntilTheExportIsAuthorized(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	p := filepath.Join(in, "a.txt")
	os.WriteFile(p, []byte("电话 13812345678 邮箱 a@b.cn\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID}); err != nil { // no authorization yet
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	f := st.Files[0]
	if f.State != "ready_to_publish" || f.Output != "" || st.ExitCode != 2 {
		t.Fatalf("the file must wait for the authorization: %+v", st)
	}
	if f.Categories["phone"] != 1 || f.Categories["email"] != 1 {
		t.Fatalf("categories %v", f.Categories)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("something was written before the authorization: %v", entries)
	}
	// a decision while waiting redoes the file and it waits again
	fs, _ := e.Findings(ctx, f.ID, 0, 10)
	if err = e.DecideFinding(ctx, f.ID, fs[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("%+v %v", st, err)
	}
	later := t.TempDir() // the folder is chosen with the authorization
	if err = e.Publish(ctx, st.ID, false, "relative/dir"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a relative output folder must be refused: %v", err)
	}
	if err = e.Publish(ctx, st.ID, false, later); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.ExitCode != 0 || st.Files[0].State != "completed" {
		t.Fatalf("%+v", st)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("the original folder must stay empty: %v", entries)
	}
	out = later
	raw, _ := os.ReadFile(filepath.Join(out, st.Files[0].Output))
	kept := fs[0].Category
	t.Logf("kept %s: %q", kept, raw)
	if strings.Contains(string(raw), "13812345678") == (kept != "phone") && strings.Contains(string(raw), "a@b.cn") == (kept != "email") {
		t.Fatalf("the kept candidate must stay and the other be redacted: %q", raw)
	}
}

func TestManualRedactionAndBatchDecisions(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("😀张三 电话 13812345678 与 110101199003074519\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "awaiting_review" {
		t.Fatalf("%+v %v", st, err)
	}
	job := st.Files[0].ID
	doc, err := e.Document(ctx, job, 0, 10)
	if err != nil || len(doc) != 1 {
		t.Fatal(doc, err)
	}
	// "张三" sits after the emoji: UTF-16 offsets 2..4
	if _, err = e.AddManual(ctx, job, doc[0].Seq, 2, 4, "person"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.AddManual(ctx, job, doc[0].Seq, 3, 3, "person"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty selection: %v", err)
	}
	if _, err = e.AddManual(ctx, job, doc[0].Seq, 1, 3, "person"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a selection that splits a surrogate pair: %v", err)
	}
	fs, _ := e.Findings(ctx, job, 0, 50)
	var ds []FindingDecision
	for _, f := range fs {
		ds = append(ds, FindingDecision{ID: f.ID, Keep: f.Category == "id_number"})
	}
	if err = e.DecideFindings(ctx, job, ds); err != nil {
		t.Fatal(err)
	}
	if err = e.Publish(ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "completed" {
		t.Fatalf("%+v %v", st, err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, st.Files[0].Output))
	s := string(raw)
	if !strings.Contains(s, "[姓名]") {
		t.Fatalf("a manual person selection is replaced by the placeholder: %q", s)
	}
	if strings.Contains(s, "张三") || strings.Contains(s, "13812345678") || !strings.Contains(s, "110101199003074519") || !strings.HasPrefix(s, "😀") {
		t.Fatalf("manual selection or decisions not applied: %q", s)
	}
}

func TestFilesArePagedInImportOrder(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	var paths []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(in, string(rune('a'+i))+".txt")
		os.WriteFile(p, []byte("电话 13812345678\n"), 0o600)
		paths = append(paths, p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: paths, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Preflight(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	page, err := e.Files(ctx, st.ID, 2, 2)
	if err != nil || len(page) != 2 || page[0].Name != "f0003" || page[1].Name != "f0004" {
		t.Fatalf("%+v %v", page, err)
	}
	if page, _ = e.Files(ctx, st.ID, 4, 10); len(page) != 1 || page[0].Name != "f0005" {
		t.Fatalf("%+v", page)
	}
	if _, err = e.Files(ctx, st.ID, -1, 10); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = e.Files(ctx, "nope", 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

// Between the stored inspection of an unlocked file and its join of the running batch the file is
// "preflighted". Wait must not call that finished: the file is still on its way.
func TestWaitDoesNotReturnWhileAnUnlockedFileIsAboutToJoinTheBatch(t *testing.T) {
	r, py := root(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	locked := filepath.Join(in, "enc.docx")
	if o, err := exec.Command(py, "-X", "utf8", "-c", encryptedDocx, filepath.Join(r, "python", "tests"), locked).CombinedOutput(); err != nil {
		t.Skipf("cannot build the fixture: %v %s", err, o)
	}
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{locked}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].Status != "blocked" {
		t.Fatalf("%+v %v", st, err)
	}
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	defer open() // runs before e.Close, so a failing test never leaves the hook blocked
	e.beforeConfirm = func() { close(reached); <-release }
	if err = e.Unlock(ctx, st.Files[0].ID, []byte("correct horse")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(30 * time.Second):
		t.Fatal("the unlocked file never finished its inspection")
	}
	returned := make(chan BatchStatus, 1)
	go func() { s, _ := e.Wait(ctx, st.ID); returned <- s }()
	select {
	case s := <-returned:
		t.Fatalf("Wait returned while the file was still joining the batch: %+v", s.Files)
	case <-time.After(600 * time.Millisecond):
	}
	open()
	select {
	case s := <-returned:
		if s.Files[0].State != "completed" {
			t.Fatalf("%+v", s.Files)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("Wait did not return after the file finished")
	}
}

func TestPDFPagePreviewShowsCandidatesAndRefusesOtherStates(t *testing.T) {
	r, py := root(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	pdf := filepath.Join(in, "a.pdf")
	if o, err := exec.Command(py, "-X", "utf8", "-c", pdfFixture, filepath.Join(r, "python", "tests"), pdf).CombinedOutput(); err != nil {
		t.Skipf("cannot build the fixture: %v %s", err, o)
	}
	txt := filepath.Join(in, "b.txt")
	os.WriteFile(txt, []byte("电话 13812345678\n"), 0o600)
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{pdf, txt}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PagePreview(ctx, "j000001", 1, 100); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("before the file has been read: %v", err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("%+v %v", st, err)
	}
	pp, err := e.PagePreview(ctx, st.Files[0].ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	png1, ok := e.PreviewImage(pp.Image)
	if !ok || len(png1) < 100 || string(png1[1:4]) != "PNG" {
		t.Fatalf("image %q ok=%v", pp.Image, ok)
	}
	if pp.Pages != 2 || pp.Page != 1 || len(pp.Boxes) == 0 || pp.Boxes[0].Category != "phone" || len(pp.Unmapped) != 0 {
		t.Fatalf("%+v", pp)
	}
	for _, b := range pp.Boxes { // one candidate on the page; its line may come as several boxes
		if b.Finding != pp.Boxes[0].Finding {
			t.Fatalf("%+v", pp)
		}
	}
	for _, bx := range pp.Boxes {
		if b := bx.Rect; b[2] > pp.Width || b[3] > pp.Height || b[2] <= b[0] || b[3] <= b[1] {
			t.Fatalf("box %v outside %dx%d", b, pp.Width, pp.Height)
		}
	}
	// the same page again is served from the cache; a decision shows in the box without a new render
	again, err := e.PagePreview(ctx, st.Files[0].ID, 1, 100)
	if err != nil || again.Image != pp.Image {
		t.Fatalf("%+v %v", again, err)
	}
	if _, err = e.PagePreview(ctx, st.Files[0].ID, 3, 100); err == nil {
		t.Fatal("a page beyond the document must fail")
	}
	if _, err = e.PagePreview(ctx, st.Files[0].ID, 1, 99); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dpi: %v", err)
	}
	if _, err = e.PagePreview(ctx, st.Files[1].ID, 1, 100); !errors.Is(err, ErrPreviewUnsupported) {
		t.Fatalf("a text file has no pages: %v", err)
	}
	if _, ok = e.PreviewImage("nope"); ok {
		t.Fatal("unknown token")
	}
	// a preview leaves no resource behind and does not disturb the file
	if err = e.DecideFinding(ctx, st.Files[0].ID, pp.Boxes[0].Finding, true); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("%+v %v", st, err)
	}
	pp2, err := e.PagePreview(ctx, st.Files[0].ID, 1, 100)
	if err != nil || len(pp2.Boxes) == 0 || pp2.Boxes[0].Decision != "keep" {
		t.Fatalf("%+v %v", pp2, err)
	}
}

// A batch interrupted by closing the engine is found again after reopening the same workspace,
// paused where it was; resuming finishes it, and a finished batch is no longer listed.
func TestUnfinishedBatchesSurviveARestartAndResume(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	key := &memKey{}
	p := filepath.Join(in, "r.txt")
	os.WriteFile(p, []byte("证件 110101199003074519 与 110101199003074518\n"), 0o600)
	q := filepath.Join(in, "q.txt")
	os.WriteFile(q, []byte("电话 13812345678\n"), 0o600)
	e := openEngine(t, ws, key, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if bs, err := e.Unfinished(ctx); err != nil || len(bs) != 0 {
		t.Fatalf("a new workspace: %v %v", bs, err)
	}
	quick, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: quick.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Wait(ctx, quick.ID); err != nil { // waits for the review
		t.Fatal(err)
	}
	if _, err = e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{q}, OutputDir: out}); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}

	e = openEngine(t, ws, key, false)
	defer e.Close()
	bs, err := e.Unfinished(ctx)
	if err != nil || len(bs) != 2 {
		t.Fatalf("%+v %v", bs, err)
	}
	kinds := map[string]BatchStatus{}
	for _, b := range bs {
		kinds[b.Kind] = b
	}
	if kinds["quick"].Files[0].State != "awaiting_review" && kinds["quick"].Files[0].State != "paused" {
		t.Fatalf("quick: %+v", kinds["quick"])
	}
	if !kinds["quick"].Started || kinds["quick"].Authorized || kinds["batch"].Started {
		t.Fatalf("started/authorized facts: %+v %+v", kinds["quick"], kinds["batch"])
	}
	if kinds["batch"].Files[0].Name != "f0001" || kinds["batch"].Files[0].State != "paused" {
		t.Fatalf("regular: %+v", kinds["batch"])
	}
	// the interrupted review is resumed, decided and published
	if err = e.Resume(ctx, kinds["quick"].ID); err != nil {
		t.Fatal(err)
	}
	st, err := e.Wait(ctx, kinds["quick"].ID)
	if err != nil || st.Files[0].State != "awaiting_review" || st.Files[0].Pending != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	fs, _ := e.Findings(ctx, st.Files[0].ID, 0, 10)
	if err = e.DecideFindings(ctx, st.Files[0].ID, []FindingDecision{{ID: fs[0].ID}, {ID: fs[1].ID, Keep: true}}); err != nil {
		t.Fatal(err)
	}
	if err = e.Publish(ctx, kinds["quick"].ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, kinds["quick"].ID); err != nil || st.Files[0].State != "completed" {
		t.Fatalf("%+v %v", st, err)
	}
	if bs, _ = e.Unfinished(ctx); len(bs) != 1 || bs[0].Kind != "batch" {
		t.Fatalf("a finished batch is no longer unfinished: %+v", bs)
	}
}

func withModel(m *ModelOption) func(*Options) { return func(o *Options) { o.Model = m } }

// The model preset runs the real AstrLink Guard through the detection plugin: it finds a person that no rule
// can, the policy redacts it with the placeholder, and the delivered file no longer holds the name.
func TestModelPresetFindsWhatRulesCannotAndRedactsIt(t *testing.T) {
	m := modelOption(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(m))
	defer e.Close()
	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out}) // no preset named: the model one is the default
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	f := st.Files[0]
	if f.State != "completed" || f.Categories["person"] < 1 || f.Categories["phone"] < 1 {
		t.Fatalf("%+v", f)
	}
	raw, _ := os.ReadFile(filepath.Join(out, f.Output))
	s := string(raw)
	if strings.Contains(s, "张伟") || strings.Contains(s, "13812345678") || strings.Contains(s, "zhangwei@example.com") || !strings.Contains(s, "[姓名]") {
		t.Fatalf("not redacted: %q", s)
	}
	// the rules-only preset, asked for by name, leaves the person alone (and needs no model worker)
	p2 := filepath.Join(in, "r.txt")
	os.WriteFile(p2, []byte("请联系张伟，电话 13912345678。\n"), 0o600)
	st2, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p2}, OutputDir: out, Preset: PresetID})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st2.ID, Preset: PresetID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st2, err = e.Wait(ctx, st2.ID); err != nil || st2.Files[0].Categories["person"] != 0 || st2.Files[0].State != "completed" {
		t.Fatalf("%+v %v", st2, err)
	}
}

// A model that fails its integrity check fails the stage; it never turns into "nothing found" and a delivered file.
func TestAModelThatFailsVerificationFailsTheFileInsteadOfFindingNothing(t *testing.T) {
	m := modelOption(t)
	raw, err := os.ReadFile(m.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var man map[string]any
	json.Unmarshal(raw, &man)
	man["files"].(map[string]any)["tokenizer_config.json"].(map[string]any)["sha256"] = strings.Repeat("0", 64)
	bad := filepath.Join(t.TempDir(), "manifest.json")
	data, _ := json.Marshal(man)
	os.WriteFile(bad, data, 0o600)
	tampered := *m
	tampered.ManifestPath = bad
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(&tampered))
	defer e.Close()
	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("请联系张伟，电话 13812345678。\n"), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.Files[0].State == "completed" || st.ExitCode == 0 {
		t.Fatalf("a model that cannot be trusted must not deliver: %+v", st)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("nothing may be delivered: %v", entries)
	}
}

// Two batches delivered to the same folder do not collide on a name.
func TestBatchesDeliveredToOneFolderKeepDistinctNames(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var names []string
	for i := 0; i < 2; i++ {
		p := filepath.Join(in, "a.txt")
		os.WriteFile(p, []byte("电话 13812345678\n"), 0o600)
		st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
			t.Fatal(err)
		}
		if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "completed" {
			t.Fatalf("round %d: %+v %v", i, st, err)
		}
		names = append(names, st.Files[0].Output)
	}
	if names[0] == names[1] {
		t.Fatalf("the same name twice: %v", names)
	}
}

func TestConfigObjectsAreListedEditedVersionedAndUsedByTheNextRun(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sets, err := e.ConfigList(ctx, "ruleset")
	if err != nil || len(sets) != 1 || !sets[0].ReadOnly || sets[0].ID != RuleSetID {
		t.Fatalf("%+v %v", sets, err)
	}
	if _, err = e.ConfigList(ctx, "nonsense"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = e.ConfigSave(ctx, RuleSetID, 1, "x", "", `{"rules":[]}`); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("built-in objects are read-only: %v", err)
	}
	// a rule set of the person's own: employee numbers, which no built-in rule knows
	rs, err := e.ConfigCreate(ctx, "ruleset", "工号", "", `{"rules":[{"id":"emp","category":"employee","pattern":"HZ\\d{6}"}]}`)
	if err != nil || rs.Version != 1 {
		t.Fatalf("%+v %v", rs, err)
	}
	if _, err = e.ConfigCreate(ctx, "ruleset", "工号", "", `{"rules":[]}`); err == nil {
		t.Fatal("a name used twice must be refused")
	}
	if _, err = e.ConfigCreate(ctx, "ruleset", "坏的", "", `{"rules":[{"id":"x","category":"c","pattern":"(?<=a)b"}]}`); err == nil || err.Error() != "RULE_LOOKAROUND_UNSUPPORTED" {
		t.Fatalf("look-behind is refused with the rule engine's own code: %v", err)
	}
	sch, err := e.ConfigCreate(ctx, "scheme", "含工号", "", `{"category_actions":{"phone":"placeholder","employee":"placeholder"},"image_default":"remove"}`)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := e.ConfigCreate(ctx, "preset", "自定义", "", `{"ruleset_id":"`+RuleSetID+`","extra_ruleset_ids":["`+rs.ID+`"],"scheme_id":"`+sch.ID+`","mode":"guided","rules_only":true,"memory_bytes":1073741824,"dpi":200,"style":"black","retention_days":0}`)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(in, "e.txt")
	os.WriteFile(p, []byte("工号 HZ123456 电话 13812345678\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out, Preset: pre.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: pre.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "completed" || st.Files[0].Categories["employee"] != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, st.Files[0].Output))
	if strings.Contains(string(raw), "HZ123456") || strings.Contains(string(raw), "13812345678") {
		t.Fatalf("both the custom and the built-in rule must act: %q", raw)
	}
	// versions: a stale save is a conflict, a changed one is v2, a rename alone is not a new version
	if _, err = e.ConfigSave(ctx, rs.ID, 7, "工号", "", rs.Content); !errors.Is(err, ErrVersion) {
		t.Fatalf("stale: %v", err)
	}
	v2, err := e.ConfigSave(ctx, rs.ID, 1, "工号", "", `{"rules":[{"id":"emp","category":"employee","pattern":"HZ\\d{7}"}]}`)
	if err != nil || v2.Version != 2 {
		t.Fatalf("%+v %v", v2, err)
	}
	same, err := e.ConfigSave(ctx, rs.ID, 2, "工号二", "备注", v2.Content)
	if err != nil || same.Version != 2 || same.Name != "工号二" {
		t.Fatalf("rename only: %+v %v", same, err)
	}
	hist, err := e.ConfigHistory(ctx, rs.ID)
	if err != nil || len(hist) != 2 || hist[0].Number != 2 {
		t.Fatalf("%+v %v", hist, err)
	}
	back, err := e.ConfigRollback(ctx, rs.ID, 2, 1)
	if err != nil || back.Version != 3 || back.Content != rs.Content {
		t.Fatalf("%+v %v", back, err)
	}
	cp, err := e.ConfigCopy(ctx, rs.ID, "工号 副本")
	if err != nil || cp.ID == rs.ID || cp.Version != 1 {
		t.Fatalf("%+v %v", cp, err)
	}
	if err = e.ConfigDelete(ctx, rs.ID, 3); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a rule set a preset uses cannot go: %v", err)
	}
	if err = e.ConfigDelete(ctx, pre.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, rs.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, RuleSetID, 1); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("built-in: %v", err)
	}
	left, _ := e.ConfigList(ctx, "ruleset")
	if len(left) != 2 {
		t.Fatalf("%+v", left)
	}
}

// A rule that is switched off stays in the rule set but takes no part in detection and is no detection source.
func TestADisabledRuleDetectsNothingAndCoversNoCategory(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rs, err := e.ConfigCreate(ctx, "ruleset", "工号", "", `{"rules":[{"id":"emp","category":"employee","pattern":"HZ\\d{6}","disabled":true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	sch := `{"category_actions":{"phone":"placeholder","employee":"placeholder"},"image_default":"remove"}`
	scheme, err := e.ConfigCreate(ctx, "scheme", "s", "", sch)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := e.ConfigCreate(ctx, "preset", "p", "", `{"ruleset_id":"`+RuleSetID+`","extra_ruleset_ids":["`+rs.ID+`"],"scheme_id":"`+scheme.ID+`","mode":"guided","rules_only":true,"memory_bytes":1073741824,"dpi":200,"style":"black","retention_days":0}`)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(in, "e.txt")
	os.WriteFile(p, []byte("工号 HZ123456\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out, Preset: pre.ID})
	if err != nil {
		t.Fatal(err)
	}
	// "employee" is in the scheme but its only rule is off: there is no detection source, so the run is refused
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: pre.ID, Authorize: true}); err == nil || !errors.Is(err, ErrNotReady) {
		t.Fatalf("a category nothing detects must block the start: %v", err)
	}
	// switched on again, the same preset detects it
	on, err := e.ConfigSave(ctx, rs.ID, 1, "工号", "", `{"rules":[{"id":"emp","category":"employee","pattern":"HZ\\d{6}"}]}`)
	if err != nil || on.Version != 2 {
		t.Fatalf("%+v %v", on, err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: pre.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].Categories["employee"] != 1 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestSettingsSurviveAReopenAndRefuseWhatIsNotOffered(t *testing.T) {
	ws, key := filepath.Join(t.TempDir(), "ws"), &memKey{}
	e := openEngine(t, ws, key, true)
	ctx := context.Background()
	got, err := e.SettingsGet(ctx)
	if err != nil || got.CloseBehavior != "tray" || got.RedactStyle != "black" || got.RetentionDays != 7 || got.AutoUpdate {
		t.Fatalf("%+v %v", got, err)
	}
	want := Settings{CloseBehavior: "quit", Theme: "dark", RedactStyle: "plaid", ResourceTier: "std", RetentionDays: 0, AutoUpdate: true}
	if got, err = e.SettingsSave(ctx, want); err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	bad := want
	bad.RedactStyle = "neon"
	if _, err = e.SettingsSave(ctx, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	e.Close()
	e = openEngine(t, ws, key, false)
	defer e.Close()
	if got, err = e.SettingsGet(ctx); err != nil || got != want {
		t.Fatalf("after reopen: %+v %v", got, err)
	}
}

func reviewFile(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "r.txt")
	os.WriteFile(p, []byte("证件 110101199003074519 与 110101199003074518\n"), 0o600)
	return p
}

func settingsWith(days int, tier string) Settings {
	return Settings{CloseBehavior: "tray", Theme: "system", RedactStyle: "black", ResourceTier: tier, RetentionDays: days}
}

func dirEntries(dir string) int {
	n := 0
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

// With "do not keep" chosen, unfinished work does not survive a restart: its data and key are gone, the
// batch is closed (so the slot is free), and what was shown about it stays readable by neutral name.
func TestNoRetentionRemovesUnfinishedWorkAtTheNextStart(t *testing.T) {
	ws, in, out, key := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir(), &memKey{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := openEngine(t, ws, key, true)
	if _, err := e.SettingsSave(ctx, settingsWith(0, "comfort")); err != nil {
		t.Fatal(err)
	}
	st, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{reviewFile(t, in)}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "awaiting_review" {
		t.Fatalf("%+v %v", st, err)
	}
	job := st.Files[0].ID
	if dirEntries(filepath.Join(ws, "resources")) == 0 {
		t.Fatal("the work should have data before the restart")
	}
	e.Close()

	e = openEngine(t, ws, key, false)
	defer e.Close()
	if bs, err := e.Unfinished(ctx); err != nil || len(bs) != 0 {
		t.Fatalf("not kept, so not offered again: %+v %v", bs, err)
	}
	if now, err := e.Status(ctx, st.ID, 10); err != nil || now.Files[0].State != "cancelled" || now.Files[0].Name != "f0001" {
		t.Fatalf("%+v %v", now, err)
	}
	if fs, _ := e.Findings(ctx, job, 0, 10); len(fs) != 0 {
		t.Fatalf("findings left behind: %v", fs)
	}
	if n := dirEntries(filepath.Join(ws, "resources")); n != 0 {
		t.Fatalf("%d resource files left behind", n)
	}
	if n := dirEntries(filepath.Join(ws, "bodies")); n != 0 {
		t.Fatalf("%d body files left behind", n)
	}
	if _, err := e.ws.Keys().Get(keys.Task, job); err == nil {
		t.Fatal("the task key must be erased")
	}
	// the quick slot is free again
	if _, err = e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{reviewFile(t, in)}, OutputDir: out}); err != nil {
		t.Fatalf("slot: %v", err)
	}
}

// A kept batch is kept for the days chosen and not a day longer.
func TestRetentionKeepsWorkUntilTheDeadlineThenRemovesIt(t *testing.T) {
	ws, in, out, key := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir(), &memKey{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := openEngine(t, ws, key, true)
	if _, err := e.SettingsSave(ctx, settingsWith(7, "comfort")); err != nil {
		t.Fatal(err)
	}
	st, err := e.Import(ctx, ImportRequest{Kind: "quick", Files: []string{reviewFile(t, in)}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	e.Close()
	at := func(days float64) func(*Options) {
		return func(o *Options) {
			o.Now = func() time.Time { return time.Now().Add(time.Duration(days * 24 * float64(time.Hour))) }
		}
	}
	e = openEngineWith(t, ws, key, false, at(6.9))
	if bs, _ := e.Unfinished(ctx); len(bs) != 1 {
		t.Fatalf("still within 7 days: %+v", bs)
	}
	e.Close()
	e = openEngineWith(t, ws, key, false, at(7.1))
	defer e.Close()
	if bs, _ := e.Unfinished(ctx); len(bs) != 0 {
		t.Fatalf("past 7 days: %+v", bs)
	}
	if n := dirEntries(filepath.Join(ws, "resources")); n != 0 {
		t.Fatalf("%d resource files left behind", n)
	}
}

// Clean removes a finished batch's data and key, once, and never touches what other batches hold.
func TestCleanErasesTheDataAndKeyOfFinishedFilesOnly(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p := filepath.Join(in, "a.txt")
	os.WriteFile(p, []byte("电话 13812345678\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "completed" {
		t.Fatalf("%+v %v", st, err)
	}
	job := st.Files[0].ID
	if _, err = e.ws.Keys().Get(keys.Task, job); err != nil {
		t.Fatal(err)
	}
	if err = e.Clean(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ws.Keys().Get(keys.Task, job); err == nil {
		t.Fatal("the task key must be erased by Clean")
	}
	if n := dirEntries(filepath.Join(ws, "resources")); n != 0 {
		t.Fatalf("%d resource files left", n)
	}
	if ids, _ := e.liveJobs(ctx); len(ids) != 0 {
		t.Fatalf("a cleaned job is no longer live: %v", ids)
	}
	if again, err := e.Status(ctx, st.ID, 5); err != nil || again.Files[0].State != "completed" {
		t.Fatalf("the report of what happened stays: %+v %v", again, err)
	}
	// the delivered file is the person's and stays
	if _, err = os.Stat(filepath.Join(out, st.Files[0].Output)); err != nil {
		t.Fatal(err)
	}
}

func TestTheResourceTierDecidesTheBudgetAtTheNextStart(t *testing.T) {
	ws, key := filepath.Join(t.TempDir(), "ws"), &memKey{}
	e := openEngine(t, ws, key, true)
	if e.budget.MaxRunning != 1 || e.budget.MemoryBytes != 2<<30 {
		t.Fatalf("comfort: %+v", e.budget)
	}
	if _, err := e.SettingsSave(context.Background(), settingsWith(7, "std")); err != nil {
		t.Fatal(err)
	}
	if e.budget.MaxRunning != 1 {
		t.Fatal("a running queue is not resized under its tasks")
	}
	e.Close()
	e = openEngine(t, ws, key, false)
	defer e.Close()
	if e.budget.MaxRunning != 2 || e.budget.MemoryBytes != 4<<30 || e.budget.CPUSlots != 2 {
		t.Fatalf("standard: %+v", e.budget)
	}
}

// Going back to a preset version brings its scheme back with it, in one step; a failed step leaves both as they were.
func TestPresetRollbackRestoresTheSchemeItWasSavedWith(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	defer e.Close()
	ctx := context.Background()
	phone := `{"category_actions":{"phone":"placeholder"},"image_default":"remove"}`
	both := `{"category_actions":{"phone":"mask","email":"placeholder"},"image_default":"remove"}`
	sch, err := e.ConfigCreate(ctx, "scheme", "s", "", phone)
	if err != nil {
		t.Fatal(err)
	}
	presetJSON := func(rev uint64) string {
		return fmt.Sprintf(`{"ruleset_id":"%s","scheme_id":"%s","scheme_rev":%d,"mode":"guided","rules_only":true,"memory_bytes":1073741824,"dpi":200,"style":"black","retention_days":0}`, RuleSetID, sch.ID, rev)
	}
	pre, err := e.ConfigCreate(ctx, "preset", "p", "", presetJSON(sch.Version))
	if err != nil {
		t.Fatal(err)
	}
	// the scheme changes, the preset is saved with the scheme version it now runs with
	sch2, err := e.ConfigSave(ctx, sch.ID, sch.Version, "s", "", both)
	if err != nil || sch2.Version != 2 {
		t.Fatalf("%+v %v", sch2, err)
	}
	pre2, err := e.ConfigSave(ctx, pre.ID, pre.Version, "p", "", presetJSON(sch2.Version))
	if err != nil || pre2.Version != 2 {
		t.Fatalf("a scheme change must be a preset version: %+v %v", pre2, err)
	}
	back, err := e.ConfigRollback(ctx, pre.ID, 2, 1)
	if err != nil || back.Version != 3 {
		t.Fatalf("%+v %v", back, err)
	}
	items, _ := e.ConfigList(ctx, "scheme")
	var now ConfigItem
	for _, it := range items {
		if it.ID == sch.ID {
			now = it
		}
	}
	if !strings.Contains(now.Content, `"placeholder"`) || strings.Contains(now.Content, "email") || now.Version != 3 {
		t.Fatalf("the scheme must be back to the one of version 1: %+v", now)
	}
	if !strings.Contains(back.Content, `"scheme_rev":3`) {
		t.Fatalf("the preset records the scheme version it now runs with: %s", back.Content)
	}
	// a stale expected version changes neither
	if _, err = e.ConfigRollback(ctx, pre.ID, 2, 2); !errors.Is(err, ErrVersion) {
		t.Fatalf("%v", err)
	}
	items, _ = e.ConfigList(ctx, "scheme")
	for _, it := range items {
		if it.ID == sch.ID && it.Version != 3 {
			t.Fatalf("the scheme moved although the preset did not: %+v", it)
		}
	}
	// a version saved without a scheme record rolls back the preset alone
	old, err := e.ConfigCreate(ctx, "preset", "q", "", fmt.Sprintf(`{"ruleset_id":"%s","scheme_id":"%s","mode":"guided","rules_only":true,"memory_bytes":1073741824,"dpi":200,"style":"black","retention_days":0}`, RuleSetID, sch.ID))
	if err != nil {
		t.Fatal(err)
	}
	q2, err := e.ConfigSave(ctx, old.ID, 1, "q", "", fmt.Sprintf(`{"ruleset_id":"%s","scheme_id":"%s","mode":"guided","rules_only":true,"memory_bytes":1073741824,"dpi":150,"style":"black","retention_days":0}`, RuleSetID, sch.ID))
	if err != nil || q2.Version != 2 {
		t.Fatalf("%+v %v", q2, err)
	}
	if r, err := e.ConfigRollback(ctx, old.ID, 2, 1); err != nil || !strings.Contains(r.Content, `"dpi":200`) {
		t.Fatalf("%+v %v", r, err)
	}
	items, _ = e.ConfigList(ctx, "scheme")
	for _, it := range items {
		if it.ID == sch.ID && it.Version != 3 {
			t.Fatalf("a preset without a scheme record must not move the scheme: %+v", it)
		}
	}
}

func traced(t *testing.T, dir string, key *memKey, create bool) (*Engine, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	e := openEngineWith(t, dir, key, create, func(o *Options) {
		o.Trace = func(s lifecycle.Status) {
			if s.Done {
				mu.Lock()
				seen = append(seen, s.Step)
				mu.Unlock()
			}
		}
	})
	return e, &seen
}

// The engine starts and exits in the designed orders, and is only handed out once every step is done.
func TestTheEngineStartsAndExitsInTheDesignedOrder(t *testing.T) {
	e, seen := traced(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	want := []string{"workspace", "reap", "publish", "retention_sweep", "recover", "dispatch"}
	if !reflect.DeepEqual(*seen, want) {
		t.Fatalf("start: %v", *seen)
	}
	*seen = nil
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	exit := []string{"stop_dispatch", "cancel_to_boundary", "checkpoint", "reap", "publish_wait", "retention", "release_workspace"}
	if !reflect.DeepEqual(*seen, exit) {
		t.Fatalf("exit: %v", *seen)
	}
}

// A start-up that fails stops at the failing step, reports its cause and leaves the workspace free.
func TestAFailedStartStopsAtTheStepAndReleasesTheWorkspace(t *testing.T) {
	ws, key := filepath.Join(t.TempDir(), "ws"), &memKey{}
	openEngine(t, ws, key, true).Close()
	var steps []string
	r, py := root(t)
	pack, _ := os.ReadFile(filepath.Join(r, "resources", "rules", "cn-office-v1.json"))
	_, err := Open(context.Background(), Options{Dir: ws, UserKey: &memKey{}, Python: py, EnvDigest: "sha256:" + strings.Repeat("e", 64), RulePack: pack, Platform: "darwin-arm64",
		Plugins: []Builtin{builtin(t, r, "format-text", "tacet_format_text", "txt")},
		Trace: func(s lifecycle.Status) {
			state := "started"
			if s.Done {
				state = "done"
			} else if s.Error != nil {
				state = "failed"
			}
			steps = append(steps, s.Step+" "+state)
		}})
	if err == nil {
		t.Fatal("a wrong key must not open the workspace")
	}
	if want := []string{"workspace started", "workspace failed"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("only the first step may run, and it fails: %v", steps)
	}
	e := openEngine(t, ws, key, false) // the lock was released
	e.Close()
}

// A file published but not yet recorded when the run ended is not published twice.
func TestAPublishedFileIsNotPublishedAgainAfterItWasSettled(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p := filepath.Join(in, "a.txt")
	os.WriteFile(p, []byte("电话 13812345678\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "completed" {
		t.Fatalf("%+v %v", st, err)
	}
	f := st.Files[0]
	if !e.delivered(ctx, f.ID, out, f.Output, f.Digest) {
		t.Fatal("the delivery is recorded and must be recognised")
	}
	for name, c := range map[string][3]string{"other folder": {t.TempDir(), f.Output, f.Digest}, "other name": {out, "x.txt", f.Digest}, "other content": {out, f.Output, "sha256:00"}} {
		if e.delivered(ctx, f.ID, c[0], c[1], c[2]) {
			t.Fatalf("%s must not count as delivered", name)
		}
	}
}

const sleeperEnv = "TACET_APP_TEST_SLEEPER"

// TestSleeper is the process the reclaim test starts: this test binary, whose environment the kernel lets
// other processes read (a system binary's it does not).
func TestSleeper(t *testing.T) {
	if os.Getenv(sleeperEnv) == "" {
		t.Skip("helper process only")
	}
	time.Sleep(300 * time.Second)
}

// A worker an earlier run left behind is reclaimed at the next start; one of another workspace is not.
func TestStartReclaimsWorkersOfAnEarlierRunOfThisWorkspaceOnly(t *testing.T) {
	ws, key := filepath.Join(t.TempDir(), "ws"), &memKey{}
	openEngine(t, ws, key, true).Close()
	// a child that was killed stays a zombie until it is waited for, so "gone" is Wait returning
	spawn := func(marker string) <-chan struct{} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSleeper$")
		cmd.Env = []string{"PATH=/usr/bin:/bin", sleeperEnv + "=1"}
		if err := platform.Mark(cmd, marker); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		gone := make(chan struct{})
		go func() { cmd.Wait(); close(gone) }()
		t.Cleanup(func() { cmd.Process.Kill(); <-gone })
		return gone
	}
	isGone := func(c <-chan struct{}, within time.Duration) bool {
		select {
		case <-c:
			return true
		case <-time.After(within):
			return false
		}
	}
	mine := spawn(markerFor(ws, 1, 99999))
	other := spawn(markerFor(ws+"-other", 1, 99999))
	if isGone(mine, 300*time.Millisecond) || isGone(other, 0) {
		t.Fatal("setup: a helper is not running")
	}
	e := openEngine(t, ws, key, false)
	defer e.Close()
	if !isGone(mine, 5*time.Second) {
		t.Fatal("a worker of an earlier run of this workspace must be reclaimed at start")
	}
	if isGone(other, 500*time.Millisecond) {
		t.Fatal("a worker of another workspace must be left alone")
	}
}

// A start that ended while the files were still being inspected (an interrupt) is not lost: resuming the batch
// carries it out with the authorization that was given, and the file is delivered.
func TestAnInterruptedStartIsCarriedOutByResume(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p := filepath.Join(in, "a.txt")
	os.WriteFile(p, []byte("电话 13812345678\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	gone, stop := context.WithCancel(ctx)
	stop() // the call ends before it can wait for the inspection
	if err = e.Start(gone, StartRequest{Batch: st.ID, Authorize: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if err = e.Pause(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.Resume(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	var now BatchStatus
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if now, err = e.Wait(ctx, st.ID); err == nil && now.Files[0].State == "completed" {
			break
		}
	}
	if now.Files[0].State != "completed" || !now.Authorized || !now.Started {
		t.Fatalf("%+v", now)
	}
	raw, _ := os.ReadFile(filepath.Join(out, now.Files[0].Output))
	if strings.Contains(string(raw), "13812345678") {
		t.Fatalf("not redacted: %q", raw)
	}
}

// A file inspected after its batch started, and paused before it joined the batch, is moved on by Resume:
// nothing else would run for a file that is waiting at "inspected".
func TestResumeJoinsAnInspectedFileToItsStartedBatch(t *testing.T) {
	r, py := root(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	locked := filepath.Join(in, "enc.docx")
	if o, err := exec.Command(py, "-X", "utf8", "-c", encryptedDocx, filepath.Join(r, "python", "tests"), locked).CombinedOutput(); err != nil {
		t.Skipf("cannot build the fixture: %v %s", err, o)
	}
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{locked}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].Status != "blocked" {
		t.Fatalf("%+v %v", st, err)
	}
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	defer open()
	var first sync.Once
	e.beforeConfirm = func() { first.Do(func() { close(reached) }); <-release } // runs again when Resume joins the file
	if err = e.Unlock(ctx, st.Files[0].ID, []byte("correct horse")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(30 * time.Second):
		t.Fatal("the unlocked file never finished its inspection")
	}
	if err = e.Pause(ctx, st.ID); err != nil { // paused where it is "inspected"
		t.Fatal(err)
	}
	open()
	time.Sleep(300 * time.Millisecond) // the join that was about to happen is refused: the file is paused
	if err = e.Resume(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	var now BatchStatus
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if now, err = e.Wait(ctx, st.ID); err == nil && now.Files[0].State == "completed" {
			break
		}
	}
	if now.Files[0].State != "completed" {
		t.Fatalf("the file must be moved on by Resume: %+v", now.Files)
	}
}
