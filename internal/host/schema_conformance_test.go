package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The same vector file is run by the Python SDK (tests/test_schema_conformance.py).
// A case that passes in one language and fails in the other is a contract bug.
func TestSharedSchemaConformanceVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "testdata", "schema-conformance-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name   string          `json:"name"`
			Schema string          `json:"schema"`
			Def    string          `json:"def"`
			Valid  bool            `json:"valid"`
			Value  json.RawMessage `json:"value"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &file); err != nil || len(file.Cases) < 50 {
		t.Fatalf("vectors: %d cases, %v", len(file.Cases), err)
	}
	schemas, err := NewSchemas()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range file.Cases {
		if err = schemas.Compile(c.Schema, c.Def); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
	}
	for _, c := range file.Cases {
		err := schemas.Validate(c.Schema, c.Def, c.Value)
		if (err == nil) != c.Valid {
			t.Errorf("Go verdict for %q: valid=%v, want %v (%v)", c.Name, err == nil, c.Valid, err)
		}
	}
}
