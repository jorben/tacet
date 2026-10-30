package exceptions

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// The protocol schema is the single source of the exception codes plugins
// may send; the kernel table must list exactly the same codes.
func TestTableMatchesProtocolSchema(t *testing.T) {
	raw, err := os.ReadFile("../../protocol/schema/v1/common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	want := s.Defs["ExceptionType"].Enum
	var got []string
	for c := range Table {
		got = append(got, string(c))
	}
	sort.Strings(want)
	sort.Strings(got)
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("type table %v does not match schema %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("type table %v does not match schema %v", got, want)
		}
	}
}
