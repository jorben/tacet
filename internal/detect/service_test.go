package detect

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/snapshot"
)

type loader map[string]Unit

func (l loader) Load(_ context.Context, ref kernel.ArtifactRef) (Unit, error) {
	u, ok := l[ref.ID]
	if !ok {
		return Unit{}, errors.New("missing")
	}
	u.Ref = ref
	if u.Span.UnitID == "" {
		u.Span.UnitID = ref.ID
	}
	return u, nil
}

type model func(ctx context.Context, text string) ([]ModelSpan, error)

func (m model) Run(ctx context.Context, text string) ([]ModelSpan, error) { return m(ctx, text) }

func ref(id string) kernel.ArtifactRef {
	return kernel.ArtifactRef{Kind: "text_unit", ID: id, Version: 1}
}

var phoneRule = rules.Rule{ID: "phone", Category: "phone", Pattern: `1[3-9][0-9]{9}`, Boundary: "digit"}
var cardRule = rules.Rule{ID: "card", Category: "payment_card", Pattern: `[0-9]{16}`, Boundary: "digit", Validator: "luhn", Suspect: true}
var termRule = rules.Rule{ID: "terms", Category: "project", Terms: []string{"曙光计划"}}

func facet(mod func(*snapshot.DetectContent)) snapshot.DetectFacet {
	c := snapshot.DetectContent{ModelID: "astrlink", LabelMap: map[string]string{"private_person": "person", "phone": "phone"},
		AdapterFloor: 0.5, Calibrated: 0.9, Categories: []string{"person", "phone", "payment_card", "project", "address"}}
	if mod != nil {
		mod(&c)
	}
	return snapshot.DetectFacet{Digest: "d", Content: c}
}

func svc(t *testing.T, units loader, m ModelRunner, fr func(string) (string, bool)) *Service {
	t.Helper()
	s, err := New(Config{Loader: units, Rules: []rules.Rule{phoneRule, cardRule, termRule}, Model: m, FieldRules: fr})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func noModel(context.Context, string) ([]ModelSpan, error) { return nil, nil }

func TestRulesAndModelCandidatesAreNormalisedWithSourcesScoresAndLowConfidence(t *testing.T) {
	text := "联系张伟 13812345678，项目曙光计划，卡 4111111111111111"
	s := svc(t, loader{"u": {Body: text}}, model(func(context.Context, string) ([]ModelSpan, error) {
		return []ModelSpan{{"private_person", 2, 4, 0.97}, {"private_person", 0, 2, 0.6}, {"phone", 5, 16, 0.99},
			{"mystery_label", 0, 1, 0.99}, {"private_person", 4, 5, 0.3}}, nil
	}), nil)
	r, err := s.DetectUnit(context.Background(), ref("u"), facet(nil))
	if err != nil {
		t.Fatal(err)
	}
	by := map[kernel.FindingSource][]Candidate{}
	for _, c := range r.Candidates {
		by[c.Source] = append(by[c.Source], c)
	}
	if len(by[kernel.SourceModel]) != 3 || len(by[kernel.SourceRule]) != 2 || len(by[kernel.SourceDictionary]) != 1 {
		t.Fatalf("sources: %+v", by)
	}
	for _, c := range by[kernel.SourceModel] {
		if c.Category == "person" && c.Span.Start == 2 && (c.LowConf || c.Score != 0.97) {
			t.Fatalf("high score marked low: %+v", c)
		}
		if c.Category == "person" && c.Span.Start == 0 && !c.LowConf {
			t.Fatalf("score under the calibrated value not marked low: %+v", c)
		}
	}
	if r.Stats.UnmappedLabels != 1 || r.Stats.BelowAdapterFloor != 1 || r.Stats.Model != 3 || r.Stats.Rule != 3 {
		t.Fatalf("stats: %+v", r.Stats)
	}
	if got := string([]rune(text)[by[kernel.SourceDictionary][0].Span.Start:by[kernel.SourceDictionary][0].Span.End]); got != "曙光计划" {
		t.Fatalf("dictionary span %q", got)
	}
	for i := 1; i < len(r.Candidates); i++ { // deterministic order
		if r.Candidates[i-1].Span.Start > r.Candidates[i].Span.Start {
			t.Fatal("candidates not sorted by position")
		}
	}
}

func TestUnmappedLabelsAreCountedNeverTurnedIntoCandidates(t *testing.T) {
	s := svc(t, loader{"u": {Body: "abc"}}, model(func(context.Context, string) ([]ModelSpan, error) {
		return []ModelSpan{{"x", 0, 1, 0.99}, {"y", 1, 2, 0.99}}, nil
	}), nil)
	r, err := s.DetectUnit(context.Background(), ref("u"), facet(nil))
	if err != nil || len(r.Candidates) != 0 || r.Stats.UnmappedLabels != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSuspectRuleHitKeepsTheValidatorMismatchFlag(t *testing.T) {
	s := svc(t, loader{"u": {Body: "卡 4111111111111111 和 4111111111111112"}}, model(noModel), nil)
	r, err := s.DetectUnit(context.Background(), ref("u"), facet(nil))
	if err != nil {
		t.Fatal(err)
	}
	var valid, suspect int
	for _, c := range r.Candidates {
		if c.Category == "payment_card" {
			if c.ValidatorMismatch {
				suspect++
			} else {
				valid++
			}
			if c.RuleID != "card" {
				t.Fatalf("rule id %q", c.RuleID)
			}
		}
	}
	if valid != 1 || suspect != 1 {
		t.Fatalf("valid=%d suspect=%d", valid, suspect)
	}
}

func TestSyntheticContextNeverProducesCandidatesAndTheBodyKeepsItsOwnCoordinates(t *testing.T) {
	u := Unit{Prefix: "手机号 13900000000：", Body: "请拨 13812345678 联系", Suffix: "；备注 13700000000"}
	s := svc(t, loader{"u": u}, model(func(_ context.Context, text string) ([]ModelSpan, error) {
		// the model sees the whole text; it flags the prefix phone, the body phone and a span straddling the prefix edge
		pre := len([]rune(u.Prefix))
		return []ModelSpan{{"phone", 4, 15, 0.99}, {"phone", pre + 3, pre + 14, 0.99}, {"private_person", pre - 2, pre + 2, 0.99}}, nil
	}), nil)
	r, err := s.DetectUnit(context.Background(), ref("u"), facet(nil))
	if err != nil {
		t.Fatal(err)
	}
	body := []rune(u.Body)
	var phones, clipped int
	for _, c := range r.Candidates {
		if c.Span.End > len(body) || c.Span.Start < 0 {
			t.Fatalf("candidate outside the body: %+v", c)
		}
		if c.Category == "phone" {
			phones++
			if string(body[c.Span.Start:c.Span.End]) != "13812345678" {
				t.Fatalf("phone %q", string(body[c.Span.Start:c.Span.End]))
			}
		}
		if c.Category == "person" {
			clipped++
			if c.Span.Start != 0 || c.Span.End != 2 {
				t.Fatalf("clipped span %+v", c.Span)
			}
		}
	}
	// the rule finds the body phone, the model finds it too; the two context phones are gone
	if phones != 2 || clipped != 1 {
		t.Fatalf("phones=%d clipped=%d %+v", phones, clipped, r.Candidates)
	}
	if r.Stats.InContextOnly != 3 || r.Stats.ClippedToBody != 1 { // 2 context rule hits + 1 model span
		t.Fatalf("stats %+v", r.Stats)
	}
}

func TestFieldRuleCoversTheWholeValueWithoutRulesOrModel(t *testing.T) {
	calls := 0
	m := model(func(context.Context, string) ([]ModelSpan, error) { calls++; return nil, nil })
	fr := func(f string) (string, bool) { return "address", f == "customer.address" }
	s := svc(t, loader{"a": {Field: "customer.address", Body: "北京市海淀区 13812345678"}, "b": {Field: "customer.note", Body: "x 13812345678"},
		"empty": {Field: "customer.address", Body: ""}}, m, fr)
	r, err := s.DetectUnit(context.Background(), ref("a"), facet(nil))
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].Source != kernel.SourceFieldRule ||
		r.Candidates[0].Span.Start != 0 || r.Candidates[0].Span.End != len([]rune("北京市海淀区 13812345678")) || r.Stats.FieldRule != 1 || calls != 0 {
		t.Fatalf("%+v %v calls=%d", r, err, calls)
	}
	r, err = s.DetectUnit(context.Background(), ref("b"), facet(nil))
	if err != nil || calls != 1 || r.Stats.FieldRule != 0 || len(r.Candidates) != 1 || r.Candidates[0].Source != kernel.SourceRule {
		t.Fatalf("a non-matching field must be detected normally: %+v %v", r, err)
	}
	if r, err = s.DetectUnit(context.Background(), ref("empty"), facet(nil)); err != nil || len(r.Candidates) != 0 {
		t.Fatalf("empty value: %+v %v", r, err)
	}
	off := facet(func(c *snapshot.DetectContent) { c.Categories = []string{"phone"} })
	if r, err = s.DetectUnit(context.Background(), ref("a"), off); err != nil || len(r.Candidates) != 0 || r.Stats.DisabledCategory != 1 {
		t.Fatalf("disabled category: %+v %v", r, err)
	}
}

func TestOnlyEnabledCategoriesProduceCandidates(t *testing.T) {
	s := svc(t, loader{"u": {Body: "联系张伟 13812345678"}}, model(func(context.Context, string) ([]ModelSpan, error) {
		return []ModelSpan{{"private_person", 2, 4, 0.99}}, nil
	}), nil)
	r, err := s.DetectUnit(context.Background(), ref("u"), facet(func(c *snapshot.DetectContent) { c.Categories = []string{"phone"} }))
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].Category != "phone" || r.Stats.DisabledCategory != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRulesOnlyModeNeverCallsTheModel(t *testing.T) {
	calls := 0
	m := model(func(context.Context, string) ([]ModelSpan, error) { calls++; return nil, nil })
	s := svc(t, loader{"u": {Body: "13812345678"}}, m, nil)
	for _, mod := range []func(*snapshot.DetectContent){
		func(c *snapshot.DetectContent) { c.RulesOnly = true },
		func(c *snapshot.DetectContent) { c.ModelID = "" },
	} {
		r, err := s.DetectUnit(context.Background(), ref("u"), facet(mod))
		if err != nil || len(r.Candidates) != 1 || calls != 0 {
			t.Fatalf("%+v %v calls=%d", r, err, calls)
		}
	}
	// a model-mode run without a runner cannot silently degrade to rules-only
	noRunner := svc(t, loader{"u": {Body: "13812345678"}}, nil, nil)
	if _, err := noRunner.DetectUnit(context.Background(), ref("u"), facet(nil)); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("missing model: %v", err)
	}
}

func TestEitherSourceFailingFailsTheUnitAndAllUnitsWithNoPartialResult(t *testing.T) {
	boom := errors.New("model crashed")
	good := loader{"u": {Body: "13812345678"}, "v": {Body: "13812345679"}}
	s := svc(t, good, model(func(_ context.Context, text string) ([]ModelSpan, error) {
		if strings.HasSuffix(text, "9") {
			return nil, boom
		}
		return nil, nil
	}), nil)
	if _, err := s.DetectUnits(context.Background(), []kernel.ArtifactRef{ref("u"), ref("v")}, facet(nil)); !errors.Is(err, boom) {
		t.Fatalf("model failure: %v", err)
	}
	if c, err := s.DetectUnits(context.Background(), []kernel.ArtifactRef{ref("u")}, facet(nil)); err != nil || len(c) != 1 {
		t.Fatalf("healthy unit: %v %v", c, err)
	}
	// a rule-engine failure (unit over budget) fails the unit even if the model succeeded
	small := rules.DefaultBudget()
	small.MaxUnitBytes = 8
	rs, err := New(Config{Loader: good, Rules: []rules.Rule{phoneRule}, Budget: small, Model: model(noModel)})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := rs.DetectUnits(context.Background(), []kernel.ArtifactRef{ref("u")}, facet(nil)); err == nil || c != nil {
		t.Fatalf("rule failure ignored: %v %v", c, err)
	}
	if _, err := s.DetectUnits(context.Background(), []kernel.ArtifactRef{ref("missing")}, facet(nil)); err == nil {
		t.Fatal("a unit that cannot be loaded produced a result")
	}
}

func TestMalformedModelOutputIsAProtocolErrorNotRepaired(t *testing.T) {
	text := "abcdef"
	for name, bad := range map[string]ModelSpan{
		"past the end": {"phone", 3, 99, 0.9},
		"negative":     {"phone", -1, 2, 0.9},
		"empty span":   {"phone", 2, 2, 0.9},
		"reversed":     {"phone", 4, 2, 0.9},
		"NaN score":    {"phone", 0, 2, math.NaN()},
		"infinite":     {"phone", 0, 2, math.Inf(1)},
	} {
		s := svc(t, loader{"u": {Body: text}}, model(func(context.Context, string) ([]ModelSpan, error) { return []ModelSpan{bad}, nil }), nil)
		if _, err := s.DetectUnit(context.Background(), ref("u"), facet(nil)); !errors.Is(err, ErrModelOutput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestInvalidUnitsAndConfigAreRejected(t *testing.T) {
	s := svc(t, loader{"bad": {Body: "a\xffb"}, "noid": {Span: kernel.Span{}, Body: "x"}}, model(noModel), nil)
	if _, err := s.DetectUnit(context.Background(), ref("bad"), facet(nil)); !errors.Is(err, ErrUnit) {
		t.Fatalf("invalid UTF-8: %v", err)
	}
	if _, err := New(Config{Rules: nil}); !errors.Is(err, ErrConfig) {
		t.Fatalf("no loader: %v", err)
	}
	if _, err := New(Config{Loader: loader{}, Rules: []rules.Rule{{ID: "x", Category: "c", Pattern: `(?=a)b`}}}); err == nil {
		t.Fatal("an unsupported rule was accepted")
	}
}

func TestCancellationStopsAHangingModelPromptly(t *testing.T) {
	s := svc(t, loader{"u": {Body: "13812345678"}}, model(func(ctx context.Context, _ string) ([]ModelSpan, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := s.DetectUnit(ctx, ref("u"), facet(nil)); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatalf("%v after %v", err, time.Since(started))
	}
}
