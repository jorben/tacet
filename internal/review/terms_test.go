package review

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/store"
)

type gcm struct{ keys map[string][]byte }

func (g gcm) aead(batch string) (cipher.AEAD, error) {
	k, ok := g.keys[batch]
	if !ok {
		return nil, errors.New("no key")
	}
	b, _ := aes.NewCipher(k)
	return cipher.NewGCM(b)
}

func (g gcm) Seal(batch string, plain []byte) ([]byte, error) {
	a, err := g.aead(batch)
	if err != nil {
		return nil, err
	}
	n := make([]byte, a.NonceSize())
	rand.Read(n)
	return a.Seal(n, n, plain, []byte(batch)), nil
}

func (g gcm) Open(batch string, blob []byte) ([]byte, error) {
	a, err := g.aead(batch)
	if err != nil || len(blob) < a.NonceSize() {
		return nil, errors.New("bad")
	}
	return a.Open(nil, blob[:a.NonceSize()], blob[a.NonceSize():], []byte(batch))
}

func withTerms(t *testing.T) *fx {
	f := setup(t)
	f.svc.Terms = gcm{keys: map[string][]byte{"b1": bytes.Repeat([]byte{1}, 32), "b2": bytes.Repeat([]byte{2}, 32)}}
	return f
}

func TestTermsAreVersionedBatchScopedEncryptedAndIdempotent(t *testing.T) {
	f := withTerms(t)
	v, n, err := f.svc.AddTerms(ctx(), "t1", "b1", []string{" 星河计划 ", "Project Nova", "星河计划"})
	if err != nil || v != 1 || n != 2 {
		t.Fatalf("%d %d %v", v, n, err)
	}
	if v, n, err = f.svc.AddTerms(ctx(), "t1", "b1", []string{"ignored on retry"}); err != nil || v != 1 || n != 0 {
		t.Fatalf("a retry must return the first answer: %d %d %v", v, n, err)
	}
	if v, n, err = f.svc.AddTerms(ctx(), "t2", "b1", []string{"Project Nova"}); err != nil || v != 1 || n != 0 {
		t.Fatalf("adding only known terms must not create a version: %d %d %v", v, n, err)
	}
	if v, n, err = f.svc.AddTerms(ctx(), "t3", "b1", []string{"北辰", "Project Nova"}); err != nil || v != 2 || n != 1 {
		t.Fatalf("%d %d %v", v, n, err)
	}
	got, list, err := f.svc.CurrentTerms(ctx(), "b1")
	if err != nil || got != 2 || strings.Join(list, ",") != "Project Nova,北辰,星河计划" {
		t.Fatalf("%d %v %v", got, list, err)
	}
	if got, list, _ = f.svc.CurrentTerms(ctx(), "b2"); got != 0 || len(list) != 0 {
		t.Fatalf("terms leaked across batches: %d %v", got, list)
	}
	var plain bool
	f.do(func(tx store.Tx) error {
		tt, _, _ := scope(tx)
		rows, _ := tt.Query("SELECT blob FROM review_terms")
		defer rows.Close()
		for rows.Next() {
			var b []byte
			rows.Scan(&b)
			plain = plain || bytes.Contains(b, []byte("Nova")) || bytes.Contains(b, []byte("星河"))
		}
		return nil
	})
	if plain {
		t.Fatal("a term is stored in plain text")
	}
}

func TestTermsRefuseBadInputMissingCipherAndOverflow(t *testing.T) {
	f := withTerms(t)
	many := make([]string, MaxTerms+1)
	for i := range many {
		many[i] = "t" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune(0x4e00+i))
	}
	for name, terms := range map[string][]string{
		"none": nil, "blank": {"   "}, "control": {"a\x00b"}, "newline": {"a\nb"}, "too long": {strings.Repeat("长", MaxTermRunes+1)},
		"invalid utf-8": {"a\xffb"}, "too many": many,
	} {
		if _, _, err := f.svc.AddTerms(ctx(), "r-"+name, "b1", terms); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := f.svc.AddTerms(ctx(), "ok", "b1", []string{strings.Repeat("长", MaxTermRunes)}); err != nil {
		t.Errorf("a term of exactly the maximum length: %v", err)
	}
	if _, _, err := f.svc.AddTerms(ctx(), "", "b1", []string{"a"}); !errors.Is(err, ErrInvalid) {
		t.Error("no request")
	}
	if _, _, err := f.svc.AddTerms(ctx(), "x", "", []string{"a"}); !errors.Is(err, ErrInvalid) {
		t.Error("no batch")
	}
	if _, _, err := f.svc.AddTerms(ctx(), "y", "unknown-batch", []string{"a"}); !errors.Is(err, ErrStore) {
		t.Errorf("a batch without a key must fail, not store plain: %v", err)
	}
	f.svc.Terms = nil
	if _, _, err := f.svc.AddTerms(ctx(), "z", "b1", []string{"a"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no cipher: %v", err)
	}
	if _, _, err := f.svc.CurrentTerms(ctx(), "b1"); !errors.Is(err, ErrInvalid) {
		t.Errorf("no cipher on read: %v", err)
	}
}

func TestTermsMayGrowToTheMaximumButNotPast(t *testing.T) {
	f := withTerms(t)
	mk := func(from, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "term" + string(rune(0x4e00+from+i))
		}
		return out
	}
	if _, _, err := f.svc.AddTerms(ctx(), "a", "b1", mk(0, MaxTerms)); err != nil {
		t.Fatalf("exactly the maximum: %v", err)
	}
	if _, _, err := f.svc.AddTerms(ctx(), "b", "b1", mk(MaxTerms, 1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one past the maximum: %v", err)
	}
	if v, l, _ := f.svc.CurrentTerms(ctx(), "b1"); v != 1 || len(l) != MaxTerms {
		t.Fatalf("a refused request changed the list: %d %d", v, len(l))
	}
	if v, n, err := f.svc.AddTerms(ctx(), "c", "b1", mk(0, 5)); err != nil || v != 1 || n != 0 {
		t.Fatalf("known terms at the maximum are still accepted: %d %d %v", v, n, err)
	}
}

func TestPurgingABatchRemovesItsTermsOnly(t *testing.T) {
	f := withTerms(t)
	f.svc.AddTerms(ctx(), "a", "b1", []string{"one"})
	f.svc.AddTerms(ctx(), "b", "b2", []string{"two"})
	if err := f.do(func(tx store.Tx) error { return PurgeBatchTerms(tx, "b1") }); err != nil {
		t.Fatal(err)
	}
	if v, l, _ := f.svc.CurrentTerms(ctx(), "b1"); v != 0 || len(l) != 0 {
		t.Fatal("b1 terms survived")
	}
	if v, l, _ := f.svc.CurrentTerms(ctx(), "b2"); v != 1 || len(l) != 1 {
		t.Fatal("b2 lost its terms")
	}
	if err := f.do(func(tx store.Tx) error { return PurgeBatchTerms(tx, "") }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
