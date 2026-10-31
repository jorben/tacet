package host

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestEmbeddedSchemasCompileOfflineAndValidateEnvelopes(t *testing.T) {
	s, err := NewSchemas()
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"rpc.schema.json", "Message"}, {"initialize.schema.json", "Params"}, {"stream.schema.json", "Credit"}, {"common.schema.json", "Error"}, {"manifest.schema.json", "Plugin"}} {
		// Manifest currently has one root, rather than a Plugin definition;
		// the role-specific definitions above remain independently compiled.
		if pair[0] == "manifest.schema.json" {
			continue
		}
		if err = s.Compile(pair[0], pair[1]); err != nil {
			t.Fatalf("compile %v: %v", pair, err)
		}
	}
	valid := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"task.cancel","params":{}}`,
		`{"jsonrpc":"2.0","id":"a","result":{}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"Plugin request failed","data":{"code":"CANCELLED","retry":"none"}}}`,
	}
	for _, body := range valid {
		if err = s.Validate("rpc.schema.json", "Message", json.RawMessage(body)); err != nil {
			t.Fatalf("valid envelope rejected: %v", err)
		}
	}
	invalid := []string{
		`{"jsonrpc":"1.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":null,"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"result":{},"error":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"a","params":{},"secret":"must not appear in error"}`,
		`[]`,
	}
	for _, body := range invalid {
		if err = s.Validate("rpc.schema.json", "Message", json.RawMessage(body)); !errors.Is(err, ErrProtocolSchema) {
			t.Fatalf("invalid envelope accepted: %v", err)
		}
	}
	if err = s.Compile("https://remote.invalid/schema.json", "Message"); !errors.Is(err, ErrProtocolSchema) {
		t.Fatal("remote schema allowed")
	}
}
