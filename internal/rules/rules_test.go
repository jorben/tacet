package rules

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func mustCompile(t *testing.T, rules ...Rule) *Set {
	t.Helper()
	set, err := Compile(rules, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func spans(hits []Hit) [][3]any {
	var out [][3]any
	for _, h := range hits {
		out = append(out, [3]any{h.RuleID, h.Start, h.End})
	}
	return out
}

// idWith builds a synthetic ID number with a correct check character.
func idWith(prefix17 string) string {
	sum := 0
	for i := 0; i < 17; i++ {
		sum += int(prefix17[i]-'0') * idWeights[i]
	}
	return prefix17 + string(idCheck[sum%11])
}

func TestCompileRejectsUnsupportedContentInsteadOfTruncating(t *testing.T) {
	b := DefaultBudget()
	cases := []struct {
		name string
		rule Rule
		want error
	}{
		{"lookahead", Rule{ID: "a", Category: "c", Pattern: `\d+(?!\d)`}, ErrLookaround},
		{"lookbehind", Rule{ID: "a", Category: "c", Pattern: `(?<!\d)\d+`}, ErrLookaround},
		{"backreference", Rule{ID: "a", Category: "c", Pattern: `(a)\1`}, ErrSyntax},
		{"line anchor", Rule{ID: "a", Category: "c", Pattern: `^\d+`}, ErrSyntax},
		{"word boundary", Rule{ID: "a", Category: "c", Pattern: `\bfoo`}, ErrSyntax},
		{"empty match", Rule{ID: "a", Category: "c", Pattern: `\d*`}, ErrSyntax},
		{"invalid", Rule{ID: "a", Category: "c", Pattern: `([`}, ErrSyntax},
		{"unknown validator", Rule{ID: "a", Category: "c", Pattern: `\d+`, Validator: "eval"}, ErrValidatorUnknown},
		{"unknown boundary", Rule{ID: "a", Category: "c", Pattern: `\d+`, Boundary: "latin"}, ErrBoundaryUnknown},
		{"pattern and terms", Rule{ID: "a", Category: "c", Pattern: `\d+`, Terms: []string{"x"}}, ErrRuleInvalid},
		{"neither", Rule{ID: "a", Category: "c"}, ErrRuleInvalid},
		{"long pattern", Rule{ID: "a", Category: "c", Pattern: strings.Repeat("a", b.MaxPatternBytes+1)}, ErrBudget},
		{"long term", Rule{ID: "a", Category: "c", Terms: []string{strings.Repeat("a", b.MaxTermLen+1)}}, ErrBudget},
		{"empty term", Rule{ID: "a", Category: "c", Terms: []string{""}}, ErrRuleInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Compile([]Rule{c.rule}, b); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
	if _, err := Compile([]Rule{{ID: "a", Category: "c", Pattern: "x"}, {ID: "a", Category: "c", Pattern: "y"}}, b); !errors.Is(err, ErrDuplicateRuleID) {
		t.Fatal("duplicate id accepted")
	}
	if _, err := Compile(nil, b); !errors.Is(err, ErrRuleInvalid) {
		t.Fatal("empty set accepted")
	}
	small := b
	small.MaxRules = 1
	if _, err := Compile([]Rule{{ID: "a", Category: "c", Pattern: "x"}, {ID: "b", Category: "c", Pattern: "y"}}, small); !errors.Is(err, ErrBudget) {
		t.Fatal("rule count budget ignored")
	}
	small = b
	small.MaxProgSize = 3
	if _, err := Compile([]Rule{{ID: "a", Category: "c", Pattern: `[a-z]{20}`}}, small); !errors.Is(err, ErrBudget) {
		t.Fatal("program size budget ignored")
	}
	if _, err := Compile([]Rule{{ID: "a", Category: "c", Pattern: "x"}}, Budget{}); !errors.Is(err, ErrBudget) {
		t.Fatal("zero budget accepted")
	}
}

func TestBoundaryPropertiesMatchLookaroundSemantics(t *testing.T) {
	// Equivalent of (?<!\d)1\d{10}(?!\d) for a mobile number.
	set := mustCompile(t, Rule{ID: "phone", Category: "phone", Pattern: `1\d{10}`, Boundary: "digit"})
	cases := []struct {
		text string
		want [][3]any
	}{
		{"电话13812345678。", [][3]any{{"phone", 2, 13}}},
		{"123812345678901", nil}, // inside a longer digit run
		{"x113812345678", nil},   // preceded by a digit at every possible start
		{"13812345678 13987654321", [][3]any{{"phone", 0, 11}, {"phone", 12, 23}}},
		{"138123456789", nil}, // trailing digit rejects
	}
	for _, c := range cases {
		hits, err := set.Match(context.Background(), c.text)
		if err != nil {
			t.Fatal(err)
		}
		if got := spans(hits); len(got) != len(c.want) || (len(got) > 0 && !equal(got, c.want)) {
			t.Fatalf("%q: got %v want %v", c.text, got, c.want)
		}
	}
}

func equal(a, b [][3]any) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return len(a) == len(b)
}

func TestWordAndCJKBoundaries(t *testing.T) {
	word := mustCompile(t, Rule{ID: "k", Category: "c", Pattern: `key`, Boundary: "word"})
	hits, _ := word.Match(context.Background(), "key monkey key_1 (key)")
	if got := spans(hits); !equal(got, [][3]any{{"k", 0, 3}, {"k", 18, 21}}) {
		t.Fatalf("word boundary: %v", got)
	}
	cjk := mustCompile(t, Rule{ID: "k", Category: "c", Pattern: `张三`, Boundary: "cjk"})
	hits, _ = cjk.Match(context.Background(), "张三丰 与张三 张三,")
	if got := spans(hits); !equal(got, [][3]any{{"k", 8, 10}}) {
		// "与张三" has a Han neighbour, "张三丰" has one; only the spaced mention remains.
		t.Fatalf("cjk boundary: %v", got)
	}
}

func TestCodePointOffsetsWithAstralAndCombiningCharacters(t *testing.T) {
	set := mustCompile(t, Rule{ID: "t", Category: "c", Terms: []string{"秘密"}})
	hits, err := set.Match(context.Background(), "😀😀a\u0301 秘密 😀秘密")
	if err != nil {
		t.Fatal(err)
	}
	if got := spans(hits); !equal(got, [][3]any{{"t", 5, 7}, {"t", 9, 11}}) {
		t.Fatalf("offsets: %v", got)
	}
}

func TestDictionaryLongestAndFoldCase(t *testing.T) {
	set := mustCompile(t, Rule{ID: "d", Category: "c", Terms: []string{"Project", "Project Alpha", "a.b"}, FoldCase: true})
	hits, _ := set.Match(context.Background(), "PROJECT ALPHA and project, axb, A.B")
	if got := spans(hits); !equal(got, [][3]any{{"d", 0, 13}, {"d", 18, 25}, {"d", 32, 35}}) {
		t.Fatalf("dictionary: %v", got)
	}
}

func TestValidatorsAndSuspectMismatch(t *testing.T) {
	good := idWith("11010519900307123")
	bad := good[:17] + "0"
	if bad == good {
		bad = good[:17] + "1"
	}
	strict := mustCompile(t, Rule{ID: "id", Category: "id", Pattern: `\d{17}[\dXx]`, Boundary: "digit", Validator: "cn_id"})
	suspect := mustCompile(t, Rule{ID: "id", Category: "id", Pattern: `\d{17}[\dXx]`, Boundary: "digit", Validator: "cn_id", Suspect: true})
	text := "证件 " + good + " 与 " + bad
	hits, _ := strict.Match(context.Background(), text)
	if len(hits) != 1 || hits[0].ValidatorMismatch {
		t.Fatalf("strict: %+v", hits)
	}
	hits, _ = suspect.Match(context.Background(), text)
	if len(hits) != 2 || hits[0].ValidatorMismatch || !hits[1].ValidatorMismatch {
		t.Fatalf("suspect: %+v", hits)
	}
	card := mustCompile(t, Rule{ID: "card", Category: "card", Pattern: `\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{4}`, Boundary: "digit", Validator: "luhn"})
	hits, _ = card.Match(context.Background(), "4111 1111 1111 1111 和 4111 1111 1111 1112")
	if len(hits) != 1 || hits[0].Start != 0 {
		t.Fatalf("luhn: %+v", hits)
	}
	for text, want := range map[string]bool{"4111111111111111": true, "4111-1111-1111-1111": true, "411111111111": false, "4111x111111111111": false} {
		if ValidLuhn(text) != want {
			t.Fatalf("luhn %q", text)
		}
	}
	for text, want := range map[string]bool{good: true, strings.ToLower(good): true, bad: false, "110105199002301234": false} {
		if ValidChineseID(text) != want {
			t.Fatalf("id %q want %v", text, want)
		}
	}
	// 29 February only exists in leap years.
	if ValidChineseID(idWith("11010519010229123")) || !ValidChineseID(idWith("11010520000229123")) {
		t.Fatal("leap-day validation")
	}
}

func TestWorkBudgetStopsQuadraticBoundaryRetriesInsteadOfReturningPartialHits(t *testing.T) {
	set := mustCompile(t, Rule{ID: "n", Category: "c", Pattern: `\d+`, Boundary: "word"})
	// Every start matches to the end of the digit run and is then rejected by
	// the trailing letter, so honest retrying would be quadratic.
	text := strings.Repeat("1", 50_000) + "a"
	started := time.Now()
	hits, err := set.Match(context.Background(), text)
	if !errors.Is(err, ErrBudget) || hits != nil {
		t.Fatalf("expected budget failure without partial hits: hits=%d err=%v", len(hits), err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("budget did not bound runtime")
	}
	// The same pattern on benign text is unaffected.
	hits, err = set.Match(context.Background(), " 12 345 ")
	if err != nil || len(hits) != 2 {
		t.Fatalf("benign text: %v %v", hits, err)
	}
}

func TestInputAndHitLimitsAndCancellation(t *testing.T) {
	b := DefaultBudget()
	b.MaxUnitBytes = 8
	b.MaxHitsPerUnit = 2
	set, err := Compile([]Rule{{ID: "x", Category: "c", Pattern: `x`}}, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = set.Match(context.Background(), "xxxxxxxxx"); !errors.Is(err, ErrBudget) {
		t.Fatal("unit size ignored")
	}
	if _, err = set.Match(context.Background(), "xxx"); !errors.Is(err, ErrBudget) {
		t.Fatal("hit limit ignored")
	}
	if _, err = set.Match(context.Background(), "\xff"); !errors.Is(err, ErrInputInvalid) {
		t.Fatal("invalid UTF-8 accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = set.Match(ctx, "xx"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
}

func TestLargeHitCountOffsetsAreLinear(t *testing.T) {
	set := mustCompile(t, Rule{ID: "x", Category: "c", Pattern: `秘`})
	text := strings.Repeat("秘😀", 50_000)
	started := time.Now()
	hits, err := set.Match(context.Background(), text)
	if err != nil || len(hits) != 50_000 || hits[49_999].Start != 99_998 {
		t.Fatalf("hits=%d err=%v", len(hits), err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("offset conversion is not linear")
	}
}

func contextRule(window int, keywords ...string) Rule {
	return Rule{ID: "staff", Category: "account", Pattern: `E[0-9]{6,10}`, Context: keywords, ContextWindow: window}
}

func ctxHits(t *testing.T, r Rule, text string) []string {
	t.Helper()
	set, err := Compile([]Rule{r}, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	hits, err := set.Match(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	runes := []rune(text)
	for _, h := range hits {
		out = append(out, string(runes[h.Start:h.End]))
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestContextKeywordMustPrecedeTheHitWithinTheWindowOnTheSameLine(t *testing.T) {
	r := contextRule(0, "工号", "Employee ID")
	for name, tc := range map[string]struct {
		text string
		want []string
	}{
		"label directly before":      {"工号：E75182837", []string{"E75182837"}},
		"English label, any case":    {"EMPLOYEE Id: E1234567", []string{"E1234567"}},
		"no label":                   {"订单 E75182837", nil},
		"label after the value":      {"E75182837 工号", nil},
		"label on the previous line": {"工号\nE75182837", nil},
		"label with CR":              {"工号\rE75182837", nil},
		"two values, one labelled":   {"工号：E1111111，这是另外一个没有标签的值 E2222222", []string{"E1111111"}},
		"label inside the value":     {"E工号1234567", nil},
	} {
		if got := ctxHits(t, r, tc.text); !eq(got, tc.want) {
			t.Errorf("%s: %q -> %v, want %v", name, tc.text, got, tc.want)
		}
	}
}

func TestContextWindowCountsCodePointsAndIsExactAtTheEdge(t *testing.T) {
	r := contextRule(5, "工号")
	// keyword start is exactly 5 code points before the hit: "工号" + 3 filler
	if got := ctxHits(t, r, "工号abcE1234567"); !eq(got, []string{"E1234567"}) {
		t.Fatalf("inside the window: %v", got)
	}
	// one more filler pushes the keyword's first character out of the window
	if got := ctxHits(t, r, "工号abcdE1234567"); got != nil {
		t.Fatalf("outside the window: %v", got)
	}
	// astral characters are one code point each, not four bytes
	if got := ctxHits(t, r, "工号😀😀😀E1234567"); !eq(got, []string{"E1234567"}) {
		t.Fatalf("astral filler counted in bytes: %v", got)
	}
	if got := ctxHits(t, contextRule(0, "工号"), "工号"+strings.Repeat("字", 14)+"E1234567"); !eq(got, []string{"E1234567"}) {
		t.Fatalf("default window: %v", got)
	}
	if got := ctxHits(t, contextRule(0, "工号"), "工号"+strings.Repeat("字", 15)+"E1234567"); got != nil {
		t.Fatalf("default window exceeded: %v", got)
	}
}

func TestRejectedContextHitDoesNotHideLaterHits(t *testing.T) {
	r := contextRule(8, "工号")
	if got := ctxHits(t, r, "E1111111 无标签；工号：E2222222；E3333333"); !eq(got, []string{"E2222222"}) {
		t.Fatalf("%v", got)
	}
}

func TestInvalidContextConfigurationIsRefused(t *testing.T) {
	many := make([]string, 65)
	for i := range many {
		many[i] = "k"
	}
	for name, r := range map[string]Rule{
		"window without keywords": {ID: "x", Category: "c", Pattern: "E[0-9]+", ContextWindow: 8},
		"window too large":        contextRule(65, "工号"),
		"negative window":         contextRule(-1, "工号"),
		"empty keyword":           contextRule(8, ""),
		"newline in keyword":      contextRule(8, "工\n号"),
		"invalid utf-8 keyword":   contextRule(8, "\xff"),
		"over-long keyword":       contextRule(8, strings.Repeat("字", 40)),
		"too many keywords":       contextRule(8, many...),
	} {
		if _, err := Compile([]Rule{r}, DefaultBudget()); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := Compile([]Rule{contextRule(64, "工号")}, DefaultBudget()); err != nil {
		t.Fatalf("maximum window: %v", err)
	}
}

func TestContextChecksAreChargedToTheWorkBudget(t *testing.T) {
	b := DefaultBudget()
	b.MaxWorkFactor, b.MaxWorkBase = 1, 100 // scanning alone fits; the context look-backs must push it over
	text := strings.Repeat(strings.Repeat("a", 60)+"E1", 20)
	plain, err := Compile([]Rule{{ID: "n", Category: "c", Pattern: `E[0-9]`}}, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plain.Match(context.Background(), text); err != nil {
		t.Fatalf("control without context must fit the budget: %v", err)
	}
	withContext, err := Compile([]Rule{{ID: "n", Category: "c", Pattern: `E[0-9]`, Context: []string{"zzz"}, ContextWindow: 64}}, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = withContext.Match(context.Background(), text); !errors.Is(err, ErrBudget) {
		t.Fatalf("rejected hits with a long look-back must exhaust the budget, got %v", err)
	}
}
