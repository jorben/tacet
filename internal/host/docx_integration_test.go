//go:build darwin

package host

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/testenv"
)

func TestRealDOCXInventoryOverEncryptedInheritedHandle(t *testing.T) {
	python := testenv.Python(t)
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Generated public text</w:t></w:r></w:p></w:body></w:document>`,
		"unknown/orphan.bin":  "GENERATED_MARKER",
	}
	for name, body := range parts {
		part, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(part, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	path := filepath.Join(t.TempDir(), "resource")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer, err := store.NewResourceWriter(file, key, "docx-input", 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	// Inherit a genuinely read-only handle; plaintext/candidate paths never
	// appear in plugin parameters. The worker receives only fd3 and the key.
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-m", "tacet_format_docx")
	cmd.ExtraFiles = []*os.File{file}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	session, err := NewSession(&pipeTransport{read: stdout, write: stdin})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err = session.Initialize(ctx, "tacet.docx", "0.1.0", 7); err != nil {
		t.Fatal(err)
	}
	schemas, err := NewSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if err = schemas.Compile("format.schema.json", "InventoryItem"); err != nil {
		t.Fatal(err)
	}
	seen, relations, complete, unknown := 0, 0, false, false
	result, err := session.Stream(ctx, "format.inventory", kernel.AttemptRef{
		JobID: "j", AttemptID: "a", OwnerGeneration: 7, JobVersion: 1,
	}, map[string]any{"input": map[string]any{
		"ref": "r1", "mode": "read", "handle": 3, "version": 1,
		"encryption": map[string]any{"version": 1, "resource_id": "docx-input", "key_hex": hex.EncodeToString(key)},
	}}, func(ctx context.Context, body json.RawMessage) error {
		if err := schemas.Validate("format.schema.json", "InventoryItem", body); err != nil {
			return err
		}
		var item struct {
			Kind      string `json:"kind"`
			Part      string `json:"part_id"`
			Class     string `json:"classification"`
			Parts     int    `json:"parts"`
			Relations int    `json:"relations"`
		}
		if err := json.Unmarshal(body, &item); err != nil {
			return err
		}
		switch item.Kind {
		case "part":
			seen++
			if item.Part == "unknown/orphan.bin" && item.Class == "uninspected" {
				unknown = true
			}
		case "relation":
			relations++
		case "inventory_complete":
			complete = item.Parts == seen && item.Relations == relations
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || seen != 4 || relations != 1 || !complete || !unknown {
		t.Fatalf("inventory incomplete: outcome=%+v parts=%d relations=%d complete=%v unknown=%v", result, seen, relations, complete, unknown)
	}
}
