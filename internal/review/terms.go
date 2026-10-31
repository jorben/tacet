package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// TermCipher seals and opens a batch's term list under that batch's key.
type TermCipher interface {
	Seal(batch string, plain []byte) ([]byte, error)
	Open(batch string, blob []byte) ([]byte, error)
}

const (
	MaxTerms     = 2000
	MaxTermRunes = 64
)

func cleanTerm(t string) (string, bool) {
	t = strings.TrimSpace(t)
	n := utf8.RuneCountInString(t)
	if n < 1 || n > MaxTermRunes || !utf8.ValidString(t) {
		return "", false
	}
	for _, r := range t {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return t, true
}

// AddTerms adds supplement terms to a batch (MD-18). The list is batch-scoped and
// versioned; it never touches the frozen rule set (INV-03), and "save to rule
// set" is a separate config action. A request that adds nothing new creates no
// version, and a retried request returns the version it created. The caller
// then has jobs dispatch a rules-only supplement scan on the affected members.
func (s *Service) AddTerms(ctx context.Context, request, batch string, terms []string) (version uint64, added int, err error) {
	if !s.ok() || s.Terms == nil || !validRequest(request) || batch == "" || len(terms) == 0 || len(terms) > MaxTerms {
		return 0, 0, ErrInvalid
	}
	in := map[string]bool{}
	for _, t := range terms {
		c, ok := cleanTerm(t)
		if !ok {
			return 0, 0, ErrInvalid
		}
		in[c] = true
	}
	err = s.Commit(ctx, func(tx store.Tx) error {
		t, sc, err := scope(tx)
		if err != nil {
			return err
		}
		var prev, cnt int
		if t.QueryRow("SELECT version, count FROM review_terms WHERE request=?", request).Scan(&prev, &cnt) == nil {
			version, added = uint64(prev), 0 // a retry: the version it made, nothing new
			return nil
		}
		cur, list, err := s.loadTerms(t, batch)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, x := range list {
			have[x] = true
		}
		for x := range in {
			if !have[x] {
				have[x] = true
				list = append(list, x)
				added++
			}
		}
		if added == 0 {
			version = cur
			return nil
		}
		if len(list) > MaxTerms {
			return ErrInvalid
		}
		sort.Strings(list)
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		blob, err := s.Terms.Seal(batch, raw)
		clear(raw)
		if err != nil || len(blob) == 0 {
			return ErrStore
		}
		version = cur + 1
		if _, err = sc.Exec(`INSERT INTO review_terms(batch, version, request, blob, count) VALUES(?,?,?,?,?)`, batch, version, request, blob, len(list)); err != nil {
			return err
		}
		tx.Emit(store.Event{Type: "SupplementTermsChanged", Subject: kernel.ArtifactRef{Kind: "terms", ID: batch, Version: version}})
		return nil
	})
	switch {
	case err == nil:
		return version, added, nil
	case errors.Is(err, ErrInvalid):
		return 0, 0, err
	}
	return 0, 0, ErrStore
}

func (s *Service) loadTerms(t *store.Txn, batch string) (uint64, []string, error) {
	var v uint64
	var blob []byte
	switch err := t.QueryRow("SELECT version, blob FROM review_terms WHERE batch=? ORDER BY version DESC LIMIT 1", batch).Scan(&v, &blob); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil, nil
	case err != nil:
		return 0, nil, err
	}
	raw, err := s.Terms.Open(batch, blob)
	if err != nil {
		return 0, nil, ErrStore
	}
	defer clear(raw)
	var list []string
	if err = json.Unmarshal(raw, &list); err != nil {
		return 0, nil, ErrStore
	}
	return v, list, nil
}

// CurrentTerms returns the batch's current supplement terms and their version (0 and
// nil when there are none).
func (s *Service) CurrentTerms(ctx context.Context, batch string) (uint64, []string, error) {
	if !s.ok() || s.Terms == nil || batch == "" {
		return 0, nil, ErrInvalid
	}
	var v uint64
	var list []string
	err := s.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		v, list, err = s.loadTerms(t, batch)
		return err
	})
	if err != nil {
		return 0, nil, ErrStore
	}
	return v, list, nil
}

// PurgeBatchTerms deletes a batch's terms (retention of the batch).
func PurgeBatchTerms(tx store.Tx, batch string) error {
	if batch == "" {
		return ErrInvalid
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	_, err = sc.Exec("DELETE FROM review_terms WHERE batch=?", batch)
	return err
}
