package rules

import (
	"context"
	"errors"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Stable error codes. Messages never include rule content or matched text.
var (
	ErrBudget           = errors.New("RULE_BUDGET_EXCEEDED")
	ErrSyntax           = errors.New("RULE_SYNTAX_UNSUPPORTED")
	ErrLookaround       = errors.New("RULE_LOOKAROUND_UNSUPPORTED")
	ErrValidatorUnknown = errors.New("RULE_VALIDATOR_UNREGISTERED")
	ErrBoundaryUnknown  = errors.New("RULE_BOUNDARY_UNKNOWN")
	ErrRuleInvalid      = errors.New("RULE_INVALID")
	ErrInputInvalid     = errors.New("RULE_INPUT_INVALID_UTF8")
	ErrDuplicateRuleID  = errors.New("RULE_ID_DUPLICATE")
)

// Rule is declarative data; there is no code or script field (architecture 8.1).
type Rule struct {
	ID        string
	Category  string
	Pattern   string   // RE2 syntax; ^ $ \b \B are rejected, use Boundary
	Terms     []string // literal dictionary terms; mutually exclusive with Pattern
	Boundary  string   // "", "digit", "word", "cjk": hit is rejected when adjacent to such a character
	Validator string   // registered deterministic validator name
	// Suspect keeps validator failures as validator_mismatch hits instead of dropping them.
	Suspect bool
	// FoldCase makes Terms case-insensitive (Unicode simple folding).
	FoldCase bool
	// Context keeps a hit only if one of these keywords (case-insensitive)
	// occurs within ContextWindow code points before the hit on the same line.
	// It is how a value with a weak format ("E75182837") is tied to its label
	// ("工号：") without lookbehind.
	Context       []string
	ContextWindow int // 0 means DefaultContextWindow when Context is set
}

const (
	DefaultContextWindow = 16
	MaxContextWindow     = 64
	maxContextKeywords   = 64
	maxContextKeywordLen = 64
)

// Budget bounds compile and match cost (INV-05, INV-02). All fields must be positive.
type Budget struct {
	MaxRules        int
	MaxPatternBytes int
	MaxTermBytes    int // total dictionary bytes
	MaxTermLen      int
	MaxProgSize     int // total compiled regexp instructions
	MaxUnitBytes    int
	// MaxWorkPerByte scales the per-unit match work allowance: work <= unit length * factor + base.
	MaxWorkFactor  int
	MaxWorkBase    int
	MaxHitsPerUnit int
}

func DefaultBudget() Budget {
	return Budget{MaxRules: 5000, MaxPatternBytes: 2048, MaxTermBytes: 8 << 20, MaxTermLen: 256,
		MaxProgSize: 2_000_000, MaxUnitBytes: 1 << 20, MaxWorkFactor: 64, MaxWorkBase: 1 << 16, MaxHitsPerUnit: 100_000}
}

func (b Budget) valid() bool {
	return b.MaxRules > 0 && b.MaxPatternBytes > 0 && b.MaxTermBytes > 0 && b.MaxTermLen > 0 &&
		b.MaxProgSize > 0 && b.MaxUnitBytes > 0 && b.MaxWorkFactor > 0 && b.MaxWorkBase >= 0 && b.MaxHitsPerUnit > 0
}

// Validator is a deterministic check on matched text.
type Validator func(text string) bool

var registry = map[string]Validator{
	"luhn":  ValidLuhn,
	"cn_id": ValidChineseID,
}

// Hit is a match in Unicode code points [Start, End) of the input unit.
type Hit struct {
	RuleID            string
	Category          string
	Start, End        int
	ValidatorMismatch bool
}

type compiled struct {
	rule     Rule
	re       *regexp.Regexp
	check    Validator
	keywords []string // lower-cased Context
	window   int
}

// Set is an immutable compiled rule set, safe for concurrent Match calls.
type Set struct {
	rules  []compiled
	budget Budget
}

var lookaround = []string{"(?=", "(?!", "(?<=", "(?<!"}

func checkTree(re *syntax.Regexp) error {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return ErrSyntax
	}
	for _, sub := range re.Sub {
		if err := checkTree(sub); err != nil {
			return err
		}
	}
	return nil
}

func pattern(r Rule, b Budget) (string, error) {
	if (r.Pattern == "") == (len(r.Terms) == 0) {
		return "", ErrRuleInvalid
	}
	if r.Pattern != "" {
		if len(r.Pattern) > b.MaxPatternBytes {
			return "", ErrBudget
		}
		for _, marker := range lookaround {
			if strings.Contains(r.Pattern, marker) {
				return "", ErrLookaround
			}
		}
		return r.Pattern, nil
	}
	parts := make([]string, 0, len(r.Terms))
	for _, term := range r.Terms {
		if term == "" || !utf8.ValidString(term) {
			return "", ErrRuleInvalid
		}
		if len(term) > b.MaxTermLen {
			return "", ErrBudget
		}
		parts = append(parts, regexp.QuoteMeta(term))
	}
	expr := strings.Join(parts, "|")
	if r.FoldCase {
		expr = "(?i:" + expr + ")"
	}
	// Compile applies leftmost-longest semantics, so overlapping terms do not
	// depend on their order in the dictionary.
	return expr, nil
}

// Compile rejects unsupported or over-budget content instead of truncating it.
func Compile(rules []Rule, budget Budget) (*Set, error) {
	if !budget.valid() {
		return nil, ErrBudget
	}
	if len(rules) == 0 || len(rules) > budget.MaxRules {
		if len(rules) == 0 {
			return nil, ErrRuleInvalid
		}
		return nil, ErrBudget
	}
	seen := make(map[string]bool, len(rules))
	set := &Set{budget: budget}
	termBytes, prog := 0, 0
	for _, r := range rules {
		if r.ID == "" || r.Category == "" || len(r.ID) > 128 || len(r.Category) > 64 {
			return nil, ErrRuleInvalid
		}
		if seen[r.ID] {
			return nil, ErrDuplicateRuleID
		}
		seen[r.ID] = true
		switch r.Boundary {
		case "", "digit", "word", "cjk":
		default:
			return nil, ErrBoundaryUnknown
		}
		var keywords []string
		window := 0
		if len(r.Context) > 0 {
			if len(r.Context) > maxContextKeywords {
				return nil, ErrBudget
			}
			window = r.ContextWindow
			if window == 0 {
				window = DefaultContextWindow
			}
			if window < 1 || window > MaxContextWindow {
				return nil, ErrBudget
			}
			for _, k := range r.Context {
				if k == "" || !utf8.ValidString(k) || strings.ContainsAny(k, "\r\n") {
					return nil, ErrRuleInvalid
				}
				if len(k) > maxContextKeywordLen {
					return nil, ErrBudget
				}
				keywords = append(keywords, strings.ToLower(k))
			}
		} else if r.ContextWindow != 0 {
			return nil, ErrRuleInvalid // a window without keywords would silently mean nothing
		}
		var check Validator
		if r.Validator != "" {
			check = registry[r.Validator]
			if check == nil {
				return nil, ErrValidatorUnknown
			}
		}
		for _, term := range r.Terms {
			termBytes += len(term)
			if termBytes > budget.MaxTermBytes {
				return nil, ErrBudget
			}
		}
		expr, err := pattern(r, budget)
		if err != nil {
			return nil, err
		}
		tree, err := syntax.Parse(expr, syntax.Perl)
		if err != nil {
			// Back-references and other non-RE2 constructs fail here.
			return nil, ErrSyntax
		}
		if err = checkTree(tree); err != nil {
			return nil, err
		}
		program, err := syntax.Compile(tree.Simplify())
		if err != nil {
			return nil, ErrSyntax
		}
		prog += len(program.Inst)
		if prog > budget.MaxProgSize {
			return nil, ErrBudget
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, ErrSyntax
		}
		if re.MatchString("") {
			return nil, ErrSyntax // would match empty text
		}
		re.Longest()
		set.rules = append(set.rules, compiled{rule: r, re: re, check: check, keywords: keywords, window: window})
	}
	return set, nil
}

func inClass(r rune, class string) bool {
	switch class {
	case "digit":
		return unicode.IsDigit(r)
	case "word":
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
	case "cjk":
		return unicode.Is(unicode.Han, r)
	}
	return false
}

// boundaryOK checks the characters adjacent to text[start:end].
func boundaryOK(text string, start, end int, class string) bool {
	if class == "" {
		return true
	}
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if inClass(r, class) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if inClass(r, class) {
			return false
		}
	}
	return true
}

// Match returns hits sorted by position for one text unit. Exceeding the work
// or hit budget, or a cancelled context, is an error: partial hits are never
// returned as if the unit had been fully checked (INV-02).
func (s *Set) Match(ctx context.Context, text string) ([]Hit, error) {
	if len(text) > s.budget.MaxUnitBytes {
		return nil, ErrBudget
	}
	if !utf8.ValidString(text) {
		return nil, ErrInputInvalid
	}
	allowance := len(text)*s.budget.MaxWorkFactor + s.budget.MaxWorkBase
	work := 0
	type byteHit struct {
		compiledIndex int
		start, end    int
		mismatch      bool
	}
	var byteHits []byteHit
	for index, c := range s.rules {
		pos := 0
		for pos < len(text) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			rest := text[pos:]
			loc := c.re.FindStringIndex(rest)
			// RE2 scans up to the end of the match it returns, or the whole
			// remainder when nothing matches. Charging that real scan keeps
			// many cheap hits linear, while a pattern whose long matches keep
			// being rejected by the boundary (and retried) becomes visible.
			if loc == nil {
				work += len(rest)
			} else {
				work += loc[1] + 1
			}
			if work > allowance {
				return nil, ErrBudget
			}
			if loc == nil {
				break
			}
			start, end := pos+loc[0], pos+loc[1]
			_, size := utf8.DecodeRuneInString(text[start:])
			if !boundaryOK(text, start, end, c.rule.Boundary) {
				pos = start + size // retry from the next character, as lookaround would
				continue
			}
			if len(c.keywords) > 0 {
				ok, cost := contextOK(text, start, c.keywords, c.window)
				if work += cost; work > allowance {
					return nil, ErrBudget
				}
				if !ok {
					pos = end
					continue
				}
			}
			mismatch := false
			if c.check != nil && !c.check(text[start:end]) {
				if !c.rule.Suspect {
					pos = end
					continue
				}
				mismatch = true
			}
			byteHits = append(byteHits, byteHit{index, start, end, mismatch})
			if len(byteHits) > s.budget.MaxHitsPerUnit {
				return nil, ErrBudget
			}
			pos = end
		}
	}
	if len(byteHits) == 0 {
		return nil, nil
	}
	sort.Slice(byteHits, func(a, b int) bool {
		x, y := byteHits[a], byteHits[b]
		if x.start != y.start {
			return x.start < y.start
		}
		if x.end != y.end {
			return x.end < y.end
		}
		return s.rules[x.compiledIndex].rule.ID < s.rules[y.compiledIndex].rule.ID
	})
	// Hits are sorted by byte start, so code-point positions advance a single
	// cursor: total cost is linear in the unit, not hits x unit length.
	hits := make([]Hit, 0, len(byteHits))
	cursorByte, cursorRune := 0, 0
	for _, h := range byteHits {
		cursorRune += utf8.RuneCountInString(text[cursorByte:h.start])
		cursorByte = h.start
		c := s.rules[h.compiledIndex].rule
		hits = append(hits, Hit{RuleID: c.ID, Category: c.Category, Start: cursorRune,
			End: cursorRune + utf8.RuneCountInString(text[h.start:h.end]), ValidatorMismatch: h.mismatch})
	}
	return hits, nil
}

// contextOK looks at most window code points back from start, stopping at a
// line break, and reports whether any keyword occurs there. cost is the number
// of bytes inspected, charged to the work budget.
func contextOK(text string, start int, keywords []string, window int) (bool, int) {
	from := start
	for n := 0; n < window && from > 0; n++ {
		r, size := utf8.DecodeLastRuneInString(text[:from])
		if r == '\n' || r == '\r' {
			break
		}
		from -= size
	}
	around := strings.ToLower(text[from:start])
	for _, k := range keywords {
		if strings.Contains(around, k) {
			return true, start - from
		}
	}
	return false, start - from
}
