//go:build darwin

package platform

import (
	"bytes"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/testenv"
)

// These tests use the real login Keychain with a unique service name and remove
// the item afterwards, so they run only with TACET_REAL_KEYCHAIN=1 (CI sets it).
// A session without Keychain access fails them on purpose: the production code
// must not fall back, so the test must not either.
func testSecret(t *testing.T) *Secret {
	t.Helper()
	testenv.UseRealKeychain(t)
	s, err := NewSecret("tacet-test-"+strconv.FormatInt(time.Now().UnixNano(), 36), "user-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Delete() })
	return s
}

func TestSecretRoundTripNeverOverwritesAndDeleteIsIdempotent(t *testing.T) {
	s := testSecret(t)
	if _, err := s.Load(); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing secret: %v", err)
	}
	key := bytes.Repeat([]byte{0x5a, 0x00, 0xff}, 11)[:32]
	if err := s.Save(key); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load()
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("load: %v equal=%v", err, bytes.Equal(got, key))
	}
	if err = s.Save(bytes.Repeat([]byte{1}, 32)); !errors.Is(err, ErrSecretExists) {
		t.Fatalf("second save must refuse: %v", err)
	}
	if again, _ := s.Load(); !bytes.Equal(again, key) {
		t.Fatal("a refused save changed the stored key")
	}
	if err = s.Delete(); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err = s.Load(); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestSecretsAreIndependentPerServiceAndAccount(t *testing.T) {
	a := testSecret(t)
	b := &Secret{service: a.service, account: "other"}
	t.Cleanup(func() { b.Delete() })
	if err := a.Save([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Load(); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("another account must not see the secret: %v", err)
	}
}

func TestSecretRejectsBadNames(t *testing.T) {
	for _, bad := range [][2]string{{"", "a"}, {"a", ""}, {"a/b", "c"}, {"a", "b\\c"}, {"a b", "c"}, {"a", "b:c"}, {"a\x00", "c"}} {
		if _, err := NewSecret(bad[0], bad[1]); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestSecretRejectsAnEmptyValue(t *testing.T) {
	if err := testSecret(t).Save(nil); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("empty value: %v", err)
	}
}
