package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/jorben/tacet/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrProtocolSchema = errors.New("PROTOCOL_SCHEMA_INVALID")

const schemaBase = "https://github.com/jorben/tacet/protocol/schema/v1/"

type offlineSchemas struct{}

func (offlineSchemas) Load(url string) (any, error) { return nil, ErrProtocolSchema }

// Schemas compiles the embedded local contract once. No file/network URL
// resolution is allowed. Errors deliberately omit input data and raw validator
// diagnostics, which may otherwise include sensitive fragment values.
type Schemas struct {
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
}

func NewSchemas() (*Schemas, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineSchemas{})
	entries, err := protocol.Schemas.ReadDir("schema/v1")
	if err != nil {
		return nil, ErrProtocolSchema
	}
	for _, entry := range entries {
		data, err := protocol.Schemas.ReadFile(path.Join("schema/v1", entry.Name()))
		if err != nil {
			return nil, ErrProtocolSchema
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, ErrProtocolSchema
		}
		if err = compiler.AddResource(schemaBase+entry.Name(), doc); err != nil {
			return nil, ErrProtocolSchema
		}
	}
	return &Schemas{compiler: compiler, compiled: make(map[string]*jsonschema.Schema)}, nil
}

// Compile is a wiring-time operation. Do not call concurrently or accept
// untrusted schema paths. Validate can be used concurrently after preparation.
func (s *Schemas) Compile(file, definition string) error {
	if strings.ContainsAny(file, "/\\#") || strings.ContainsAny(definition, "/\\#") {
		return ErrProtocolSchema
	}
	key := file + "#/$defs/" + definition
	compiled, err := s.compiler.Compile(schemaBase + key)
	if err != nil {
		return ErrProtocolSchema
	}
	s.compiled[key] = compiled
	return nil
}

func (s *Schemas) Validate(file, definition string, body json.RawMessage) error {
	compiled := s.compiled[file+"#/$defs/"+definition]
	if compiled == nil {
		return ErrProtocolSchema
	}
	if len(body) > MaxMessageBytes {
		return ErrProtocolLimit
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return ErrProtocolSchema
	}
	if err = compiled.Validate(value); err != nil {
		return ErrProtocolSchema
	}
	return nil
}
