//go:build darwin

package detect

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/snapshot"
)

// Per-category detection evaluation on the synthetic corpus of
// tools/m0_eval_corpus.py. Runs only with TACET_EVAL_CORPUS (input JSONL) and
// TACET_EVAL_REPORT (output JSON): it starts the real model plugin and takes
// minutes. The numbers describe an easy synthetic corpus, not real documents.

type goldEntity struct {
	Category string `json:"category"`
	Subtype  string `json:"subtype"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type evalDoc struct {
	ID       string       `json:"id"`
	Split    string       `json:"split"`
	Kind     string       `json:"kind"`
	Language string       `json:"language"`
	Text     string       `json:"text"`
	Entities []goldEntity `json:"entities"`
}

type interval struct {
	cat        string
	start, end int
}

func wilson(k, n int) [2]float64 {
	if n == 0 {
		return [2]float64{0, 0}
	}
	const z = 1.959964
	p := float64(k) / float64(n)
	d := 1 + z*z/float64(n)
	c := p + z*z/(2*float64(n))
	m := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	return [2]float64{math.Max(0, (c-m)/d), math.Min(1, (c+m)/d)}
}

type counts struct {
	Entities, ExactMatched, Covered int // recall side
	Candidates, ExactCorrect        int // precision side
}

type metric struct {
	Entities     int        `json:"entities"`
	Candidates   int        `json:"candidates"`
	ExactP       float64    `json:"exact_precision"`
	ExactPCI     [2]float64 `json:"exact_precision_ci95"`
	ExactR       float64    `json:"exact_recall"`
	ExactRCI     [2]float64 `json:"exact_recall_ci95"`
	CoverageR    float64    `json:"complete_coverage_recall"`
	CoverageRCI  [2]float64 `json:"complete_coverage_recall_ci95"`
	Insufficient bool       `json:"fewer_than_500_instances"`
}

func (c counts) metric() metric {
	m := metric{Entities: c.Entities, Candidates: c.Candidates, Insufficient: c.Entities < 500}
	if c.Candidates > 0 {
		m.ExactP = float64(c.ExactCorrect) / float64(c.Candidates)
		m.ExactPCI = wilson(c.ExactCorrect, c.Candidates)
	}
	if c.Entities > 0 {
		m.ExactR = float64(c.ExactMatched) / float64(c.Entities)
		m.ExactRCI = wilson(c.ExactMatched, c.Entities)
		m.CoverageR = float64(c.Covered) / float64(c.Entities)
		m.CoverageRCI = wilson(c.Covered, c.Entities)
	}
	return m
}

func covered(e goldEntity, cands []interval) bool {
	need := make([]bool, e.End-e.Start)
	for _, c := range cands {
		for i := max(c.start, e.Start); i < min(c.end, e.End); i++ {
			need[i-e.Start] = true
		}
	}
	for _, ok := range need {
		if !ok {
			return false
		}
	}
	return true
}

func score(doc evalDoc, cands []interval, into func(key string) *counts, keys func(e goldEntity) []string, candKeys func(c interval, ok bool) []string) (docMissed, docFalse bool) {
	seen := map[interval]bool{}
	var uniq []interval
	for _, c := range cands {
		if !seen[c] {
			seen[c] = true
			uniq = append(uniq, c)
		}
	}
	gold := map[interval]bool{}
	for _, e := range doc.Entities {
		gold[interval{cat: e.Category, start: e.Start, end: e.End}] = true
	}
	for _, e := range doc.Entities {
		exact := seen[interval{cat: e.Category, start: e.Start, end: e.End}]
		cov := covered(e, uniq)
		for _, k := range keys(e) {
			c := into(k)
			c.Entities++
			if exact {
				c.ExactMatched++
			}
			if cov {
				c.Covered++
			}
		}
		if !cov {
			docMissed = true
		}
	}
	for _, c := range uniq {
		ok := gold[c]
		for _, k := range candKeys(c, ok) {
			n := into(k)
			n.Candidates++
			if ok {
				n.ExactCorrect++
			}
		}
	}
	docFalse = len(doc.Entities) == 0 && len(uniq) > 0
	return docMissed, docFalse
}

func TestEvaluateDetectionOnTheSyntheticCorpus(t *testing.T) {
	corpus, report := os.Getenv("TACET_EVAL_CORPUS"), os.Getenv("TACET_EVAL_REPORT")
	if corpus == "" || report == "" {
		t.Skip("set TACET_EVAL_CORPUS and TACET_EVAL_REPORT to run the evaluation")
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := filepath.Join(root, "python", ".venv", "bin", "python")
	modelDir := filepath.Join(root, ".pi", "m0-resources", "astrlink-guard-int8")
	manifest := filepath.Join(root, "resources", "models", "astrlink-guard-int8.json")
	data, err := os.ReadFile(filepath.Join(root, "resources", "rules", "cn-office-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, packRules, err := rules.ParsePack(data)
	if err != nil {
		t.Fatal(err)
	}
	packRules = append(packRules, rules.Rule{ID: "projects", Category: "project", Terms: []string{"曙光计划", "青云项目", "北斗二期", "Project Falcon", "Project Aurora", "蓝鲸专项"}})

	file, err := os.Open(corpus)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var docs []evalDoc
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	for scanner.Scan() {
		var d evalDoc
		if err = json.Unmarshal(scanner.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, d)
	}
	if limit, _ := strconv.Atoi(os.Getenv("TACET_EVAL_LIMIT")); limit > 0 && limit < len(docs) {
		step := len(docs) / limit
		var pick []evalDoc
		for i := 0; i < len(docs) && len(pick) < limit; i += step {
			pick = append(pick, docs[i])
		}
		docs = pick
	}
	for _, d := range docs { // the gold must index the text it claims to
		r := []rune(d.Text)
		for _, e := range d.Entities {
			if e.Start < 0 || e.End > len(r) || e.Start >= e.End {
				t.Fatalf("%s: gold span outside the text", d.ID)
			}
		}
	}

	worker, err := host.Start(host.Spec{Path: python,
		Args: []string{"-I", "-m", "tacet_detect_astrlink", "--model-dir", modelDir, "--manifest", manifest},
		Dir:  t.TempDir(), Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, Generation: "gen_detect_eval_run_01"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	if err = worker.Session.Initialize(ctx, "tacet.astrlink", "0.1.0", 9); err != nil {
		t.Fatal(err)
	}
	model, err := NewPluginModel(worker.Session, nextAttempt(9))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]evalDoc, len(docs))
	for _, d := range docs {
		byID[d.ID] = d
	}
	ld := loaderFunc(func(_ context.Context, ref kernel.ArtifactRef) (Unit, error) {
		d, ok := byID[ref.ID]
		if !ok {
			return Unit{}, ErrUnit
		}
		return Unit{Ref: ref, Span: kernel.Span{UnitID: d.ID, TextVersion: 1}, Body: d.Text}, nil
	})
	svc, err := New(Config{Loader: ld, Rules: packRules, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	f := snapshot.DetectFacet{Digest: "eval", Content: snapshot.DetectContent{
		ModelID: "astrlink-guard-int8", AdapterFloor: 0.5, Calibrated: 0.8,
		LabelMap: map[string]string{"private_person": "person", "private_address": "address", "phone": "phone", "email": "email",
			"payment_card": "payment_card", "ip_address": "ip_address", "url": "url", "common_secret": "secret", "account": "account", "private_date": "date"},
		Categories: []string{"person", "address", "phone", "email", "payment_card", "ip_address", "url", "secret", "account", "date", "id_number", "project"}}}
	const threshold = 0.8 // the upstream-recommended minimum span score; policy applies it later

	type modeKey = string
	modes := []modeKey{"rules_only", "model_only", "combined"}
	byMode := map[modeKey]map[string]*counts{}
	docMiss := map[modeKey]map[string]*[2]int{} // split -> {docs with entities, missed}
	falseDocs := map[modeKey]*[2]int{}          // negative docs, with a candidate
	for _, m := range modes {
		byMode[m] = map[string]*counts{}
		docMiss[m] = map[string]*[2]int{}
		falseDocs[m] = &[2]int{}
	}
	var perDocMs []int64
	samples := map[string][]string{} // combined mode: wrong candidates and missed entities, for diagnosis
	for _, d := range docs {
		started := time.Now()
		res, err := svc.DetectUnit(ctx, kernel.ArtifactRef{Kind: "text_unit", ID: d.ID, Version: 1}, f)
		if err != nil {
			t.Fatalf("%s: %v", d.ID, err)
		}
		perDocMs = append(perDocMs, time.Since(started).Milliseconds())
		for _, m := range modes {
			var cands []interval
			for _, c := range res.Candidates {
				fromModel := c.Source == kernel.SourceModel
				if fromModel && c.Score < threshold {
					continue
				}
				if (m == "rules_only" && fromModel) || (m == "model_only" && !fromModel) {
					continue
				}
				cands = append(cands, interval{cat: c.Category, start: c.Span.Start, end: c.Span.End})
			}
			counter := byMode[m]
			into := func(k string) *counts {
				if counter[k] == nil {
					counter[k] = &counts{}
				}
				return counter[k]
			}
			keys := func(e goldEntity) []string {
				ks := []string{"category:" + e.Category, "subtype:" + e.Category + "/" + e.Subtype, "language:" + e.Category + "/" + d.Language, "split:" + d.Split + "/" + e.Category}
				return ks
			}
			candKeys := func(c interval, ok bool) []string {
				return []string{"category:" + c.cat, "language:" + c.cat + "/" + d.Language, "split:" + d.Split + "/" + c.cat}
			}
			missed, fp := score(d, cands, into, keys, candKeys)
			if m == "combined" {
				r := []rune(d.Text)
				gold := map[interval]bool{}
				for _, e := range d.Entities {
					gold[interval{cat: e.Category, start: e.Start, end: e.End}] = true
				}
				seenC := map[interval]bool{}
				for _, c := range cands {
					if !seenC[c] && !gold[c] && len(samples["false_positive:"+c.cat]) < 8 {
						samples["false_positive:"+c.cat] = append(samples["false_positive:"+c.cat], d.ID+" "+string(r[c.start:c.end]))
					}
					seenC[c] = true
				}
				for _, e := range d.Entities {
					if !covered(e, cands) && len(samples["missed:"+e.Category+"/"+e.Subtype]) < 8 {
						samples["missed:"+e.Category+"/"+e.Subtype] = append(samples["missed:"+e.Category+"/"+e.Subtype], d.ID+" "+string(r[e.Start:e.End]))
					}
				}
			}
			if len(d.Entities) > 0 {
				dm := docMiss[m][d.Split]
				if dm == nil {
					dm = &[2]int{}
					docMiss[m][d.Split] = dm
				}
				dm[0]++
				if missed {
					dm[1]++
				}
			} else {
				falseDocs[m][0]++
				if fp {
					falseDocs[m][1]++
				}
			}
		}
	}
	sort.Slice(perDocMs, func(i, j int) bool { return perDocMs[i] < perDocMs[j] })

	out := map[string]any{
		"corpus":    "synthetic, tools/m0_eval_corpus.py seed 20261031; easier than real documents",
		"documents": len(docs), "model": "AstrLink Guard int8 7216b6a3 via the plugin protocol", "model_score_threshold": threshold,
		"adapter_floor": 0.5, "rule_pack": "cn-office-v1 + evaluation-only project dictionary",
		"coverage_definition":    "every character of the entity lies inside some candidate of any category; exact = same category, start and end",
		"combined_error_samples": samples,
		"per_document_ms_p50":    perDocMs[len(perDocMs)/2], "per_document_ms_p95": perDocMs[len(perDocMs)*95/100],
	}
	for _, m := range modes {
		sorted := make([]string, 0, len(byMode[m]))
		for k := range byMode[m] {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		table := map[string]metric{}
		for _, k := range sorted {
			table[k] = byMode[m][k].metric()
		}
		miss := map[string]any{}
		for split, v := range docMiss[m] {
			miss[split] = map[string]any{"documents_with_entities": v[0], "documents_missing_an_entity": v[1], "miss_rate": float64(v[1]) / float64(v[0])}
		}
		out[m] = map[string]any{"metrics": table, "document_miss": miss,
			"negative_documents": falseDocs[m][0], "negative_documents_with_a_candidate": falseDocs[m][1]}
	}
	encoded, _ := json.MarshalIndent(out, "", "  ")
	if err = os.WriteFile(report, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("evaluated %d documents; report written", len(docs))
}

type loaderFunc func(context.Context, kernel.ArtifactRef) (Unit, error)

func (f loaderFunc) Load(ctx context.Context, ref kernel.ArtifactRef) (Unit, error) {
	return f(ctx, ref)
}
