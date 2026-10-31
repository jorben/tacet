package store

import (
	"errors"
	"regexp"
	"strings"
)

var ErrNotOwner = errors.New("STORE_TABLE_NOT_OWNED")

// A write statement names exactly one target table. This is a guard against
// accidents (MD-01), not a SQL parser and not a security boundary: modules are
// trusted code in one process.
var writeTarget = regexp.MustCompile(`(?is)^\s*(?:insert\s+(?:or\s+\w+\s+)?into|replace\s+into|update(?:\s+or\s+\w+)?|delete\s+from|create\s+(?:unique\s+)?(?:table|index)(?:\s+if\s+not\s+exists)?|drop\s+(?:table|index)(?:\s+if\s+exists)?|alter\s+table)\s+("?[\w]+"?)`)
var createIndexOn = regexp.MustCompile(`(?is)^\s*create\s+(?:unique\s+)?index(?:\s+if\s+not\s+exists)?\s+\S+\s+on\s+("?\w+"?)`)

func checkOwned(m Module, stmt string) error {
	trimmed := strings.TrimSpace(stmt)
	trimmed = strings.TrimSuffix(trimmed, ";")
	if strings.Contains(trimmed, ";") {
		return ErrNotOwner // one statement per call so the target is unambiguous
	}
	target := writeTarget.FindStringSubmatch(trimmed)
	if target == nil {
		return ErrNotOwner // not a recognised write statement
	}
	name := strings.Trim(target[1], `"`)
	if idx := createIndexOn.FindStringSubmatch(trimmed); idx != nil {
		name = strings.Trim(idx[1], `"`)
	}
	if !strings.HasPrefix(strings.ToLower(name), m.Prefix) {
		return ErrNotOwner
	}
	return nil
}
