package preview

import (
	"errors"
	"strings"
	"testing"
)

func facts(m map[string]Facts) Lookup {
	return func(u string) (Facts, bool) { f, ok := m[u]; return f, ok }
}

var world = facts(map[string]Facts{
	"u1":    {Job: "j1", TextVersion: 3, Chars: 20, Part: "word/document.xml", Ordinal: 4, Reliable: true},
	"flaky": {Job: "j1", TextVersion: 1, Chars: 20, Reliable: false},
	"other": {Job: "j2", TextVersion: 1, Chars: 20, Reliable: true},
})

func TestValidSelectionMapsToTheUnitRangeAndItsSourcePosition(t *testing.T) {
	r, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: 2, End: 20}, world, nil)
	if err != nil || r != (Range{Unit: "u1", Start: 2, End: 20, Part: "word/document.xml", Ordinal: 4}) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSelectionsAreRefusedWhenStaleOutOfRangeForeignOrUnreliable(t *testing.T) {
	for name, c := range map[string]struct {
		sel  Selection
		want error
	}{
		"older text version":   {Selection{Job: "j1", Unit: "u1", TextVersion: 2, Start: 0, End: 5}, ErrStale},
		"newer text version":   {Selection{Job: "j1", Unit: "u1", TextVersion: 4, Start: 0, End: 5}, ErrStale},
		"past the end":         {Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: 15, End: 21}, ErrOutOfRange},
		"empty":                {Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: 5, End: 5}, ErrInvalid},
		"reversed":             {Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: 9, End: 5}, ErrInvalid},
		"negative":             {Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: -1, End: 5}, ErrInvalid},
		"unknown unit":         {Selection{Job: "j1", Unit: "nope", TextVersion: 3, Start: 0, End: 5}, ErrNotFound},
		"another job's unit":   {Selection{Job: "j1", Unit: "other", TextVersion: 1, Start: 0, End: 5}, ErrDenied},
		"unreliable positions": {Selection{Job: "j1", Unit: "flaky", TextVersion: 1, Start: 0, End: 5}, ErrUnreliable},
		"no job":               {Selection{Unit: "u1", TextVersion: 3, Start: 0, End: 5}, ErrInvalid},
		"no unit":              {Selection{Job: "j1", TextVersion: 3, Start: 0, End: 5}, ErrInvalid},
		"no version":           {Selection{Job: "j1", Unit: "u1", Start: 0, End: 5}, ErrInvalid},
	} {
		if _, err := Validate(c.sel, world, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: %v want %v", name, err, c.want)
		}
	}
	if _, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: 0, End: 5}, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("no lookup: %v", err)
	}
}

func TestOwnershipIsCheckedBeforeVersionSoAForeignUnitDoesNotRevealItsState(t *testing.T) {
	// a stale version on another job's unit must read as denied, not as stale
	if _, err := Validate(Selection{Job: "j1", Unit: "other", TextVersion: 99, Start: 0, End: 5}, world, nil); !errors.Is(err, ErrDenied) {
		t.Fatalf("%v", err)
	}
}

func TestCoordinateSelectionsGoThroughTheMapperAndItsAnswerIsCheckedLikeAnyRange(t *testing.T) {
	rect := &Rect{X0: 0, Y0: 0, X1: 10, Y1: 5}
	ok := func(string, Rect) (int, int, error) { return 3, 8, nil }
	r, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Coords: rect}, world, ok)
	if err != nil || r.Start != 3 || r.End != 8 {
		t.Fatalf("%+v %v", r, err)
	}
	for name, m := range map[string]Mapper{
		"beyond the unit": func(string, Rect) (int, int, error) { return 10, 25, nil },
		"empty answer":    func(string, Rect) (int, int, error) { return 5, 5, nil },
		"negative answer": func(string, Rect) (int, int, error) { return -2, 4, nil },
	} {
		if _, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Coords: rect}, world, m); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("%s: %v", name, err)
		}
	}
	fail := func(string, Rect) (int, int, error) { return 0, 0, errors.New("plugin said no") }
	if _, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Coords: rect}, world, fail); !errors.Is(err, ErrMapperFails) {
		t.Errorf("%v", err)
	}
	if _, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Coords: rect}, world, nil); !errors.Is(err, ErrMapperFails) {
		t.Errorf("no mapper: %v", err)
	}
	for name, bad := range map[string]*Rect{"zero area": {X0: 1, Y0: 1, X1: 1, Y1: 5}, "inverted": {X0: 5, Y0: 5, X1: 1, Y1: 1}} {
		if _, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Coords: bad}, world, ok); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a stale coordinate selection never reaches the plugin
	called := false
	spy := func(string, Rect) (int, int, error) { called = true; return 0, 1, nil }
	if _, err := Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 2, Coords: rect}, world, spy); !errors.Is(err, ErrStale) || called {
		t.Errorf("stale=%v called=%v", err, called)
	}
	// coordinates win over any text range sent along
	r, _ = Validate(Selection{Job: "j1", Unit: "u1", TextVersion: 3, Start: 0, End: 1, Coords: rect}, world, ok)
	if r.Start != 3 {
		t.Errorf("%+v", r)
	}
}

func TestSafeTextKeepsPlainTextAndDropsWhatCouldMislead(t *testing.T) {
	cases := map[string]string{
		"plain 文本 テキスト 🔐":           "plain 文本 テキスト 🔐",
		"a\nb\tc":                   "a\nb\tc",
		"a\x00b\x1bc\x7fd":          "abcd",
		"x\u202Eevil\u202C":         "xevil",
		"x\u2066y\u2069":            "xy",
		"l\u200Er\u200F":            "lr",
		"<script>alert(1)</script>": "<script>alert(1)</script>", // text, rendered as text
		"a\u0085b":                  "ab",
	}
	for in, want := range cases {
		if got := SafeText(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	long := strings.Repeat("字", MaxText+10)
	if n := len([]rune(SafeText(long))); n != MaxText {
		t.Errorf("length %d", n)
	}
	if SafeText("") != "" {
		t.Error("empty")
	}
}
