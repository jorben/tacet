package review

import (
	"context"
	"errors"

	"github.com/jorben/tacet/internal/store"
)

// Position is where the reviewer was: the filter, the grouping and the cursor,
// restored when the review is reopened (BUX-13). It holds no content.
type Position struct {
	Filter string // opaque to review (the UI's serialised filter)
	Group  string
	Cursor string
}

const maxPosition = 4096

func validKey(k string) bool { return k != "" && len(k) <= 256 }

func (p Position) valid() bool {
	return len(p.Filter) <= maxPosition && len(p.Group) <= maxPosition && len(p.Cursor) <= maxPosition
}

// SavePosition stores the position under key (a batch or job scope). The
// latest save wins.
func (s *Service) SavePosition(ctx context.Context, key string, p Position) error {
	if !s.ok() || !validKey(key) || !p.valid() {
		return ErrInvalid
	}
	if err := s.Commit(ctx, func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO review_position(key, filter, grp, cursor) VALUES(?,?,?,?)
			ON CONFLICT(key) DO UPDATE SET filter=excluded.filter, grp=excluded.grp, cursor=excluded.cursor`, key, p.Filter, p.Group, p.Cursor)
		return err
	}); err != nil {
		return ErrStore
	}
	return nil
}

// LoadPosition returns the saved position; the second result is false when none
// was saved.
func (s *Service) LoadPosition(ctx context.Context, key string) (Position, bool, error) {
	if !s.ok() || !validKey(key) {
		return Position{}, false, ErrInvalid
	}
	var p Position
	found := false
	err := s.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		switch err = t.QueryRow("SELECT filter, grp, cursor FROM review_position WHERE key=?", key).Scan(&p.Filter, &p.Group, &p.Cursor); {
		case err == nil:
			found = true
		case errors.Is(err, errNoRows):
		default:
			return err
		}
		return nil
	})
	if err != nil {
		return Position{}, false, ErrStore
	}
	return p, found, nil
}
