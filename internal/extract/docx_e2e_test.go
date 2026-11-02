//go:build darwin

package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/testenv"
)

const (
	ctNS   = "http://schemas.openxmlformats.org/package/2006/content-types"
	relNS  = "http://schemas.openxmlformats.org/package/2006/relationships"
	wNS    = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	mainCT = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)

func docxBytes(t *testing.T, body string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	files := [][2]string{
		{"[Content_Types].xml", `<Types xmlns="` + ctNS + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="` + mainCT + `"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="` + relNS + `"><Relationship Id="m" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<w:document xmlns:w="` + wNS + `"><w:body>` + body + `</w:body></w:document>`},
	}
	for name, data := range extra {
		files = append(files, [2]string{name, data})
	}
	for _, f := range files {
		w, err := z.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f[1]))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A real plugin process reads an encrypted input through an inherited handle;
// the pipeline persists its units, findings and coverage with real rules.
func TestRealDOCXPluginThroughThePipelineEndToEnd(t *testing.T) {
	python := testenv.Python(t)
	e := setup(t, nil)
	body := `<w:p><w:r><w:t>联系人张三，电话 13812345678。</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>备用 13900000000</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>隐藏 13700000000</w:t></w:r></w:p>` +
		`<w:p/><w:p><w:r><w:t>无敏感内容</w:t></w:r></w:p>`
	doc := docxBytes(t, body, map[string]string{"custom/unknown.bin": "opaque"})
	key := make([]byte, 32)
	rand.Read(key)
	path := filepath.Join(t.TempDir(), "input")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.NewResourceWriter(f, key, "in1", 1<<24)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(doc)
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	worker, err := host.Start(host.Spec{Path: python, Args: []string{"-I", "-m", "tacet_format_docx"}, Dir: t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: []*os.File{in}, Generation: "gen_extract_e2e_test1"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = worker.Session.Initialize(ctx, "tacet.docx", "0.1.0", e.ws.Generation()); err != nil {
		t.Fatal(err)
	}
	r, err := e.p.Run(ctx, Request{Session: worker.Session, Attempt: e.att, Batch: "b1", Facet: rulesOnly(), Params: map[string]any{
		"input": map[string]any{"ref": "r_in1", "mode": "read", "handle": 3, "version": 1,
			"encryption": map[string]any{"version": 1, "resource_id": "in1", "key_hex": hex.EncodeToString(key)}}}})
	if err != nil {
		t.Fatal(err)
	}
	// 4 non-empty paragraphs (1 empty one is skipped), 3 parts + the unknown one
	if r.Units != 4 || r.Parts != 4 || r.Gaps != 1 || r.Candidates != 3 {
		t.Fatalf("%+v", r)
	}
	units := query(e, func(tx store.Tx) ([]Unit, error) { return e.p.Units.Units(tx, "a1", 0, 50) })
	vis := map[string]int{}
	for _, u := range units {
		vis[u.Visibility]++
		if u.Part != "word/document.xml" {
			t.Fatalf("unit of %s", u.Part)
		}
	}
	if vis["visible"] != 3 || vis["hidden"] != 1 {
		t.Fatalf("hidden text must be reported apart from visible text: %v", vis)
	}
	// ordinals count empty paragraphs, so the writer can address them
	last := units[len(units)-1]
	if last.Ordinal != 5 {
		t.Fatalf("last ordinal %d", last.Ordinal)
	}
	var phones []string
	for _, x := range e.visible() {
		if x.Category == "phone" {
			body, _ := e.bodies.Get(unitBody(units, x.Unit))
			phones = append(phones, string([]rune(body)[x.Start:x.End]))
		}
	}
	if strings.Join(phones, ",") != "13812345678,13900000000,13700000000" {
		t.Fatalf("phones %v", phones)
	}
	parts, done, err := func() ([]coverage.Part, bool, error) {
		var p []coverage.Part
		var c bool
		err := e.ws.Commit(ctx, func(tx store.Tx) (err error) { p, c, err = e.p.Coverage.Ledger(tx, "j1"); return err })
		return p, c, err
	}()
	rep, err := coverage.Assess(parts, done)
	if err != nil || len(rep.Unresolved) != 1 || rep.Unresolved[0].PartID != "custom/unknown.bin" {
		t.Fatalf("the unknown part must be the one gap: %+v %v", rep, err)
	}
}

func unitBody(units []Unit, id string) string {
	for _, u := range units {
		if u.UnitID == id {
			return u.Body
		}
	}
	return ""
}
