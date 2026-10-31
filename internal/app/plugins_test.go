//go:build darwin

package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPluginsListsEveryRegisteredBuiltinOnceWithItsFormats(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	defer e.Close()
	ps, err := e.Plugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, p := range ps {
		if !p.Builtin || !p.Enabled || p.Version == "" || len(p.Roles) == 0 {
			t.Fatalf("%+v", p)
		}
		if _, dup := got[p.ID]; dup {
			t.Fatalf("%s listed twice", p.ID)
		}
		got[p.ID] = p.Formats
	}
	if !reflect.DeepEqual(got["tacet.text"], []string{"txt"}) || !reflect.DeepEqual(got["tacet.docx"], []string{"docx"}) || len(got) != 6 {
		t.Fatalf("%v", got)
	}
	for i := 1; i < len(ps); i++ {
		if ps[i-1].ID >= ps[i].ID {
			t.Fatal("not in a fixed order")
		}
	}
}
