//go:build darwin

package app

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// TestARealDOCXSample runs a DOCX the person supplied (TACET_REAL_DOCX) through the real engine with the
// default policy. It is a measurement against real files, not a regression test: it reports what happened
// (closed codes and counts only, never content) and fails only when the file is not delivered in full.
func TestARealDOCXSample(t *testing.T) {
	src := os.Getenv("TACET_REAL_DOCX")
	if src == "" {
		t.Skip("set TACET_REAL_DOCX")
	}
	ws, outDir := filepath.Join(t.TempDir(), "ws"), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{src}, OutputDir: outDir})
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
	t.Logf("state=%s status=%s detail=%s findings=%d categories=%v pending=%d", f.State, f.Status, f.Detail, f.Findings, f.Categories, f.Pending)
	if f.State != "completed" {
		t.Fatalf("not delivered in full")
	}
	// the values found, read from the document (kept in memory only, never printed)
	var doc []DocUnit
	for after := 0; ; {
		page, err := e.Document(ctx, f.ID, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		doc, after = append(doc, page...), page[len(page)-1].Seq
	}
	var values []string
	for _, u := range doc {
		units := utf16.Encode([]rune(u.Text))
		for _, fd := range u.Findings {
			if fd.Decision != "keep" {
				values = append(values, string(utf16.Decode(units[fd.Start:fd.End])))
			}
		}
	}
	in, out := zipEntries(t, src), zipEntries(t, filepath.Join(outDir, f.Output))
	for name, body := range out {
		for _, v := range values {
			if strings.Contains(body, v) {
				t.Fatalf("a redacted value reached %s", name)
			}
		}
	}
	kept, removed := 0, 0
	for name, body := range in {
		switch got, ok := out[name]; {
		case strings.HasPrefix(name, "docProps/"):
			if ok {
				t.Fatalf("%s is removed by default", name)
			}
			removed++
		case name == "word/document.xml":
			for _, tag := range []string{"<w:shd ", "<w:color ", "<w:sz ", "<w:tbl>", "<w:tr ", "<w:tc>", "<w:vAlign ", "<w:tblCellMar>", "<w:trHeight "} {
				if strings.Count(got, tag) != strings.Count(body, tag) {
					t.Fatalf("formatting %s changed: %d -> %d", tag, strings.Count(body, tag), strings.Count(got, tag))
				}
			}
		case strings.HasPrefix(name, "word/") && !strings.HasPrefix(name, "word/_rels/"):
			if got != body {
				t.Fatalf("%s is not kept as it was", name)
			}
			kept++
		}
	}
	t.Logf("values redacted=%d structure parts kept byte for byte=%d docProps removed=%d", len(values), kept, removed)
}

func zipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	m := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		m[f.Name] = string(b)
	}
	return m
}
