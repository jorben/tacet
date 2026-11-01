//go:build darwin

package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func utf16Slice(text string, start, end int) string {
	u := utf16.Encode([]rune(text))
	return string(utf16.Decode(u[start:end]))
}

// A rules-only preset reports what its rules find, each with the rule that matched, in UTF-16 offsets.
func TestPresetTestRulesOnly(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	defer e.Close()
	ctx := context.Background()
	text := "😀联系 13812345678，邮箱 li.lei@example.com。"
	res, err := e.TestPreset(ctx, e.defaultPreset(ctx), text)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RulesOnly || res.Model != "" || res.MinConfidence != 0 {
		t.Fatalf("%+v", res)
	}
	got := map[string]TestHit{}
	for _, h := range res.Hits {
		if h.Source == "model" || h.Score != 0 {
			t.Fatalf("model hit in a rules-only preset: %+v", h)
		}
		got[h.Category] = h
	}
	phone, email := got["phone"], got["email"]
	if phone.Start != 5 || utf16Slice(text, phone.Start, phone.End) != "13812345678" || phone.Source != "rule" || phone.Rule == "" || phone.RuleName == "" {
		t.Fatalf("phone: %+v", phone)
	}
	if utf16Slice(text, email.Start, email.End) != "li.lei@example.com" {
		t.Fatalf("email: %+v", email)
	}
	// nothing is kept: the workspace holds no batch afterwards
	if open, err := e.Unfinished(ctx); err != nil || len(open) != 0 {
		t.Fatalf("%+v %v", open, err)
	}
}

func TestPresetTestRefusesBadRequests(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	defer e.Close()
	ctx := context.Background()
	preset := e.defaultPreset(ctx)
	for _, c := range []struct {
		preset, text string
		want         error
	}{
		{preset, "", ErrInvalid},
		{"", "13812345678", ErrInvalid},
		{preset, strings.Repeat("字", MaxTestText+1), ErrInvalid},
		{preset, "\xff\xfe", ErrInvalid},
		{"no-such-preset", "13812345678", ErrNotFound},
	} {
		if _, err := e.TestPreset(ctx, c.preset, c.text); !errors.Is(err, c.want) {
			t.Fatalf("%q: %v, want %v", c.preset, err, c.want)
		}
	}
	if _, err := e.TestPreset(ctx, preset, strings.Repeat("字", MaxTestText)); err != nil {
		t.Fatal(err)
	}
}

// A preset with a model runs it: its candidates carry the model as source, a score, and whether they fall
// below the preset's minimum confidence; the rules still run beside it.
func TestPresetTestRunsTheModel(t *testing.T) {
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(modelOption(t)))
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	text := "联系人王小明，电话 13812345678，住在北京市海淀区中关村大街27号。"
	res, err := e.TestPreset(ctx, e.defaultPreset(ctx), text)
	if err != nil {
		t.Fatal(err)
	}
	if res.RulesOnly || res.Model == "" || res.MinConfidence <= 0 {
		t.Fatalf("%+v", res)
	}
	var model, rule bool
	for _, h := range res.Hits {
		switch h.Source {
		case "model":
			model = true
			if h.Score <= 0 || h.Rule != "" || h.Below != (h.Score < res.MinConfidence) {
				t.Fatalf("%+v", h)
			}
		case "rule":
			rule = rule || (h.Category == "phone" && utf16Slice(text, h.Start, h.End) == "13812345678")
		}
	}
	if !model || !rule {
		t.Fatalf("model %v rule %v: %+v", model, rule, res.Hits)
	}
}
