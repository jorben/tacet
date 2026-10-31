package plan

import (
	"errors"
	"strings"
	"testing"
)

func TestLocatorRoundTripsAndRejectsMalformedOnes(t *testing.T) {
	for _, c := range []struct {
		part string
		n    int
	}{{"word/document.xml", 4}, {"page/2", 1}, {"odd#name.xml", 7}} {
		p, n, ok := ParseLocator(Locator(c.part, c.n))
		if !ok || p != c.part || n != c.n {
			t.Errorf("%+v -> %q %d %v", c, p, n, ok)
		}
	}
	for _, bad := range []string{"", "nohash", "#3", "part#", "part#0", "part#-1", "part#x", "part# 3"} {
		if _, _, ok := ParseLocator(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func textSpec(unit, locator string, start, end int, action string) Spec {
	return Spec{Kind: TextOp, Unit: unit, Start: start, End: end, Action: action, Category: "phone", Source: []Segment{{locator}}, ContainerEnd: 100}
}

func TestEncodeWritesOneLinePerOperationInPlanOrderWithTheSourceSplit(t *testing.T) {
	p, err := Build("in", "res", "r", []Spec{textSpec("u1", Locator("word/document.xml", 3), 2, 13, "placeholder"), {Kind: PartOp, Unit: "docProps/core.xml", Action: "remove_part"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(p, func(o Operation) (string, error) { return "[手机号]", nil })
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("%q", data)
	}
	var text, part string
	for _, l := range lines {
		if strings.Contains(l, `"kind":"text"`) {
			text = l
		} else {
			part = l
		}
	}
	for _, want := range []string{`"part":"word/document.xml"`, `"ordinal":3`, `"start":2`, `"end":13`, `"replacement":"[手机号]"`} {
		if !strings.Contains(text, want) {
			t.Errorf("%s missing in %s", want, text)
		}
	}
	if !strings.Contains(part, `"kind":"part"`) || !strings.Contains(part, `"part":"docProps/core.xml"`) || strings.Contains(part, "start") || strings.Contains(part, "replacement") {
		t.Errorf("%s", part)
	}
	again, _ := Encode(p, func(o Operation) (string, error) { return "[手机号]", nil })
	if string(again) != string(data) {
		t.Fatal("encoding is not deterministic")
	}
}

func TestADeletionAtOffsetZeroKeepsItsStartAndEmptyReplacement(t *testing.T) {
	p, _ := Build("in", "res", "r", []Spec{textSpec("u1", Locator("p", 1), 0, 5, "delete")})
	data, err := Encode(p, func(Operation) (string, error) { return "", nil })
	// PlanLine requires "replacement" on a text line: an empty one must be written, not dropped
	if err != nil || !strings.Contains(string(data), `"start":0`) || !strings.Contains(string(data), `"replacement":""`) {
		t.Fatalf("%s %v", data, err)
	}
}

func TestEncodeRefusesWhatThePluginCouldNotTrust(t *testing.T) {
	ok := func(Operation) (string, error) { return "x", nil }
	two := textSpec("u1", Locator("p", 1), 0, 3, "mask")
	two.Source = append(two.Source, Segment{Locator("p", 2)})
	bare, _ := Build("in", "res", "r", []Spec{textSpec("u1", "garbage", 0, 3, "mask")})
	pTwo, _ := Build("in", "res", "r", []Spec{two})
	region, _ := Build("in", "res", "r", []Spec{{Kind: RegionOp, Unit: "page:1", Action: "fill", Rect: &Rect{0, 0, 5, 5}}})
	one, _ := Build("in", "res", "r", []Spec{textSpec("u1", Locator("p", 1), 0, 3, "mask")})
	for name, c := range map[string]struct {
		p    Plan
		f    func(Operation) (string, error)
		want error
	}{
		"two fragments":  {pTwo, ok, ErrNoReliableSource},
		"unparseable":    {bare, ok, ErrNoReliableSource},
		"region":         {region, ok, ErrUnsupportedKind},
		"replacer fails": {one, func(Operation) (string, error) { return "", errors.New("no") }, ErrEncode},
		"invalid utf-8":  {one, func(Operation) (string, error) { return "a\xffb", nil }, ErrEncode},
		"too long":       {one, func(Operation) (string, error) { return strings.Repeat("字", MaxReplacementRunes+1), nil }, ErrEncode},
	} {
		if _, err := Encode(c.p, c.f); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Encode(one, func(Operation) (string, error) { return strings.Repeat("字", MaxReplacementRunes), nil }); err != nil {
		t.Errorf("exactly the limit: %v", err)
	}
}

func TestRendererProducesDeleteMaskAndPlaceholderText(t *testing.T) {
	r := Renderer{Placeholders: map[string]string{"phone": "[手机号]"}, Fallback: "[已处理]", MaskHead: 3, MaskTail: 4, MaskChar: '*'}
	for _, c := range []struct{ action, cat, orig, want string }{
		{"delete", "phone", "13812345678", ""},
		{"placeholder", "phone", "13812345678", "[手机号]"},
		{"placeholder", "other", "x", "[已处理]"},
		{"mask", "phone", "13812345678", "138****5678"},
		{"mask", "phone", "1234567", "*******"}, // too short to keep both ends
		{"mask", "name", "张三丰", "***"},
		{"mask", "x", "12345678", "********"}, // 8 characters would leave only one covered
		{"mask", "x", "1234567890", "123***7890"},
		{"mask", "id", "11010519491231002X", "110***********002X"},
	} {
		got, err := r.Text(c.action, c.cat, c.orig)
		if err != nil || got != c.want {
			t.Errorf("%+v -> %q %v", c, got, err)
		}
	}
	for name, f := range map[string]func() (string, error){
		"unknown action": func() (string, error) { return r.Text("shred", "phone", "x") },
		"no fallback":    func() (string, error) { return Renderer{}.Text("placeholder", "x", "y") },
		"no mask char":   func() (string, error) { return Renderer{MaskHead: 1}.Text("mask", "x", "abcdefghijk") },
		"empty original": func() (string, error) { return r.Text("mask", "x", "") },
		"bad utf-8":      func() (string, error) { return r.Text("mask", "x", "a\xff") },
		"negative keep":  func() (string, error) { return Renderer{MaskHead: -1, MaskChar: '*'}.Text("mask", "x", "abc") },
	} {
		if _, err := f(); !errors.Is(err, ErrEncode) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
