package rules

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// loadBuiltin reads the shipped pack through the production parser.
func loadBuiltin(t testing.TB) []Rule {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "rules", "cn-office-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	id, out, err := ParsePack(data)
	if err != nil || id != "cn-office-v1" {
		t.Fatalf("%q %v", id, err)
	}
	return out
}

func TestParsePackRefusesUnknownMembersAndTrailingData(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown rule member": `{"id":"p","rules":[{"id":"a","category":"c","pattern":"x","script":"os.exit()"}]}`,
		"unknown top member":  `{"id":"p","rules":[],"hooks":[]}`,
		"no id":               `{"rules":[]}`,
		"trailing document":   `{"id":"p","rules":[]}{"id":"q"}`,
		"not json":            `nope`,
	} {
		if _, _, err := ParsePack([]byte(doc)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if id, rs, err := ParsePack([]byte(`{"id":"p","rules":[{"id":"a","category":"c","terms":["x"],"fold_case":true}]}`)); err != nil || id != "p" || len(rs) != 1 || !rs[0].FoldCase {
		t.Fatalf("%v %v", rs, err)
	}
}

func TestBuiltinChinesePackCompilesWithinTheDefaultBudget(t *testing.T) {
	if _, err := Compile(loadBuiltin(t), DefaultBudget()); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinPackFindsEachFormatWithExactSpans(t *testing.T) {
	set, err := Compile(loadBuiltin(t), DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ text, want, category string }{
		{"电话13812345678结束", "13812345678", "phone"},
		{"电话 138-1234-5678。", "138-1234-5678", "phone"},
		{"手机+86 138 1234 5678，", "+86 138 1234 5678", "phone"},
		{"座机010-12345678转123", "010-12345678转123"[:len("010-12345678")], "phone"},
		{"邮箱zhangwei@example.com。", "zhangwei@example.com", "email"},
		{"身份证号11010519491231002X结束", "11010519491231002X", "id_number"},
		{"卡号6222021234567890128。", "6222021234567890128", "payment_card"},
		{"卡号6222 0212 3456 7890", "6222 0212 3456 7890", "payment_card"},
		{"服务器10.0.3.17上", "10.0.3.17", "ip_address"},
		{"见https://example.com/a/b?x=1。", "https://example.com/a/b?x=1", "url"},
		{"key=sk-abcdefghijklmnopqrstuvwxyz012345", "sk-abcdefghijklmnopqrstuvwxyz012345", "secret"},
		{"AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE", "secret"},
	}
	for _, c := range cases {
		hits, err := set.Match(context.Background(), c.text)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, h := range hits {
			if h.Category == c.category && string([]rune(c.text)[h.Start:h.End]) == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %q as %s, got %+v", c.text, c.want, c.category, hits)
		}
	}
}
