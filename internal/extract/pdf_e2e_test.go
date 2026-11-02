//go:build darwin

package extract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/testenv"
)

const pdfFixture = `
import sys
sys.path.insert(0, sys.argv[1])
from pdf_fixtures import build
image = ('<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8', b'\x80')
content = ('<< ', b'BT /F1 12 Tf 20 100 Td (Call 13812345678 today) Tj ET q 50 0 0 50 100 20 cm /Im0 Do Q')
raw = build(['x', 'Second 13900000000'], extra={40: image, 10 + 1: content}, resources='/XObject << /Im0 40 0 R >>', info={'Title': 'T'})
open(sys.argv[2], 'wb').write(raw)
`

// A real PDF plugin process reads a text PDF through an encrypted inherited
// handle; units, findings and the coverage gaps (image, metadata) are persisted.
func TestRealPDFPluginThroughThePipelineEndToEnd(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	e := setup(t, nil)
	file := filepath.Join(t.TempDir(), "in.pdf")
	if out, err := exec.Command(python, "-X", "utf8", "-c", pdfFixture, filepath.Join(root, "python", "tests"), file).CombinedOutput(); err != nil {
		testenv.Missing(t, "the fixture, which the plugin interpreter builds: %v %s", err, out)
	}
	doc, _ := os.ReadFile(file)
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
	worker, err := host.Start(host.Spec{Path: python, Args: []string{"-I", "-m", "tacet_format_pdf"}, Dir: t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: []*os.File{in}, Generation: "gen_extract_pdf_test1"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = worker.Session.Initialize(ctx, "tacet.pdf", "0.1.0", e.ws.Generation()); err != nil {
		t.Fatal(err)
	}
	r, err := e.p.Run(ctx, Request{Session: worker.Session, Attempt: e.att, Batch: "b1", Facet: rulesOnly(), Params: map[string]any{
		"input": map[string]any{"ref": "r_in1", "mode": "read", "handle": 3, "version": 1,
			"encryption": map[string]any{"version": 1, "resource_id": "in1", "key_hex": hex.EncodeToString(key)}}}})
	if err != nil {
		t.Fatal(err)
	}
	// 2 page units; parts: page/1 + page/1/images + page/2 + document/metadata
	if r.Units != 2 || r.Parts != 4 || r.Gaps != 2 || r.Candidates != 2 {
		t.Fatalf("%+v", r)
	}
	units := query(e, func(tx store.Tx) ([]Unit, error) { return e.p.Units.Units(tx, "a1", 0, 50) })
	var phones []string
	for _, x := range e.visible() {
		if x.Category == "phone" {
			body, _ := e.bodies.Get(unitBody(units, x.Unit))
			phones = append(phones, string([]rune(body)[x.Start:x.End]))
		}
	}
	if strings.Join(phones, ",") != "13812345678,13900000000" {
		t.Fatalf("phones %v", phones)
	}
	var parts []coverage.Part
	var done bool
	if err = e.ws.Commit(ctx, func(tx store.Tx) (err error) { parts, done, err = e.p.Coverage.Ledger(tx, "j1"); return err }); err != nil {
		t.Fatal(err)
	}
	rep, err := coverage.Assess(parts, done)
	if err != nil {
		t.Fatal(err)
	}
	gaps := map[string]bool{}
	for _, g := range rep.Unresolved {
		gaps[g.PartID] = true
	}
	if len(gaps) != 2 || !gaps["page/1/images"] || !gaps["document/metadata"] {
		t.Fatalf("the image and the metadata must be the gaps: %+v", rep)
	}
}
