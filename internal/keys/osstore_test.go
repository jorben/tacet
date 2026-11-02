//go:build darwin

package keys

import (
	"bytes"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/testenv"
)

func TestKeychainBackedManagerSurvivesReopenAndRefusesSilentRecreate(t *testing.T) {
	testenv.UseRealKeychain(t)
	store, err := NewOSUserKeyStore("tacet-test-"+strconv.FormatInt(time.Now().UnixNano(), 36), "user-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Delete() })
	wrapped := newWrapped()
	if _, err = Open(store, wrapped, false); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("CLI open without a key must not create one: %v", err)
	}
	first, err := Open(store, wrapped, true)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := first.Create(Workspace, "w1")
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	second, err := Open(store, wrapped, false) // a new process would do exactly this
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	again, err := second.Get(Workspace, "w1")
	if err != nil || !bytes.Equal(again, ws) {
		t.Fatalf("workspace key after reopen: %v", err)
	}
	if err = store.Save(bytes.Repeat([]byte{7}, 32)); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("an existing user key must not be replaced: %v", err)
	}
}
