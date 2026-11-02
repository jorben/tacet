//go:build darwin

package detect

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/testenv"
)

var attemptSeq atomic.Int64

func nextAttempt(generation uint64) func() kernel.AttemptRef {
	return func() kernel.AttemptRef {
		n := attemptSeq.Add(1)
		return kernel.AttemptRef{JobID: "j", AttemptID: "a" + string(rune('a'+n%26)) + strings.Repeat("x", int(n/26)), OwnerGeneration: generation, JobVersion: 1}
	}
}

// fakePlugin answers initialize and one detect.run with scripted items.
func fakePlugin(t *testing.T, script func(send func(map[string]any), id any, stream string)) *host.Session {
	t.Helper()
	local, remote := net.Pipe()
	s, err := host.NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); remote.Close() })
	read := func() map[string]any {
		remote.SetReadDeadline(time.Now().Add(5 * time.Second))
		body, err := host.ReadFrame(remote)
		if err != nil {
			return nil
		}
		var m map[string]any
		json.Unmarshal(body, &m)
		return m
	}
	send := func(m map[string]any) {
		body, _ := json.Marshal(m)
		remote.SetWriteDeadline(time.Now().Add(5 * time.Second))
		host.WriteFrame(remote, body)
	}
	go func() {
		req := read()
		if req == nil {
			return
		}
		send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{
			"protocol": "1.0", "plugin": map[string]any{"id": "tacet.astrlink", "version": "0.1.0"}, "roles": []string{"detect"}}})
		run := read()
		if run == nil {
			return
		}
		stream := run["params"].(map[string]any)["stream"].(map[string]any)["id"].(string)
		script(send, run["id"], stream)
		for read() != nil { // drain credit messages
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = s.Initialize(ctx, "tacet.astrlink", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
	return s
}

func item(stream string, seq int, body map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": stream, "seq": seq, "item": body}}
}
func end(id any, stream string, last int, status string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"type": "stream.end", "stream": stream, "last_seq": last, "status": status}}
}
func span(label string, s, e int, score float64) map[string]any {
	return map[string]any{"label": label, "start": s, "end": e, "score": score}
}

func runFake(t *testing.T, script func(send func(map[string]any), id any, stream string)) ([]ModelSpan, error) {
	t.Helper()
	m, err := NewPluginModel(fakePlugin(t, script), nextAttempt(3))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.Run(ctx, "联系张伟")
}

func TestPluginModelCollectsBatchesAndChecksTheDoneCount(t *testing.T) {
	spans, err := runFake(t, func(send func(map[string]any), id any, st string) {
		send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{span("private_person", 2, 4, 0.99)}}))
		send(item(st, 2, map[string]any{"kind": "spans", "spans": []any{span("phone", 5, 8, 0.8), span("phone", 9, 12, 0.7)}}))
		send(item(st, 3, map[string]any{"kind": "unit_done", "spans": 3}))
		send(end(id, st, 3, "completed"))
	})
	if err != nil || len(spans) != 3 || spans[0].Label != "private_person" || spans[2].Score != 0.7 {
		t.Fatalf("%+v %v", spans, err)
	}
	spans, err = runFake(t, func(send func(map[string]any), id any, st string) {
		send(item(st, 1, map[string]any{"kind": "unit_done", "spans": 0}))
		send(end(id, st, 1, "completed"))
	})
	if err != nil || len(spans) != 0 {
		t.Fatalf("a genuine no-hit result: %+v %v", spans, err)
	}
}

func TestPluginModelRefusesIncompleteOrMalformedPluginOutput(t *testing.T) {
	good := span("phone", 0, 2, 0.9)
	for name, script := range map[string]func(func(map[string]any), any, string){
		"no unit_done (a lost tail looks like no hits)": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{good}}))
			send(end(id, st, 1, "completed"))
		},
		"done count differs from spans received": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{good}}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 2}))
			send(end(id, st, 2, "completed"))
		},
		"items after unit_done": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "unit_done", "spans": 0}))
			send(item(st, 2, map[string]any{"kind": "spans", "spans": []any{good}}))
			send(end(id, st, 2, "completed"))
		},
		"unknown item kind": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "debug", "text": "TEST_SECRET"}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 0}))
			send(end(id, st, 2, "completed"))
		},
		"score above 1": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{span("phone", 0, 2, 1.5)}}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 1}))
			send(end(id, st, 2, "completed"))
		},
		"label with upper case": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{span("PHONE", 0, 2, 0.9)}}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 1}))
			send(end(id, st, 2, "completed"))
		},
		"extra span member (a leaked text)": func(send func(map[string]any), id any, st string) {
			bad := span("phone", 0, 2, 0.9)
			bad["text"] = "13812345678"
			send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{bad}}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 1}))
			send(end(id, st, 2, "completed"))
		},
		"empty batch": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "spans", "spans": []any{}}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 0}))
			send(end(id, st, 2, "completed"))
		},
		"completed with no items at all": func(send func(map[string]any), id any, st string) {
			send(end(id, st, 0, "completed"))
		},
		"span reaching a second span count mismatch is not enough": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "unit_done", "spans": 5}))
			send(end(id, st, 1, "completed"))
		},
		"a second unit_done whose count happens to match": func(send func(map[string]any), id any, st string) {
			send(item(st, 1, map[string]any{"kind": "unit_done", "spans": 0}))
			send(item(st, 2, map[string]any{"kind": "unit_done", "spans": 0}))
			send(end(id, st, 2, "completed"))
		},
		"stream ended failed": func(send func(map[string]any), id any, st string) {
			send(end(id, st, 0, "failed"))
		},
		"stream ended cancelled": func(send func(map[string]any), id any, st string) {
			send(end(id, st, 0, "cancelled"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			if spans, err := runFake(t, script); err == nil || spans != nil {
				t.Fatalf("accepted: %+v", spans)
			}
		})
	}
}

func TestPluginModelRequiresASessionAndAttemptSource(t *testing.T) {
	if _, err := NewPluginModel(nil, nextAttempt(1)); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	if _, err := NewPluginModel(&host.Session{}, nil); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
}

// The real AstrLink Guard candidate, started like production: through host.Start
// with a minimal environment and launch arguments, speaking the real protocol.
func TestRealAstrLinkModelThroughTheProtocolFindsEntitiesAndRulesAgree(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	modelDir := filepath.Join(root, ".pi", "m0-resources", "astrlink-guard-int8")
	manifest := filepath.Join(root, "resources", "models", "astrlink-guard-int8.json")
	if _, err := os.Stat(filepath.Join(modelDir, "model_int8.onnx")); err != nil {
		t.Skip("needs the fixed M0 model resource")
	}
	worker, err := host.Start(host.Spec{Path: python,
		Args:       []string{"-I", "-m", "tacet_detect_astrlink", "--model-dir", modelDir, "--manifest", manifest},
		Dir:        t.TempDir(),
		Env:        []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"},
		Generation: "gen_detect_real_model_t1"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err = worker.Session.Initialize(ctx, "tacet.astrlink", "0.1.0", 5); err != nil {
		t.Fatal(err)
	}
	model, err := NewPluginModel(worker.Session, nextAttempt(5))
	if err != nil {
		t.Fatal(err)
	}
	text := "请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。"
	s, err := New(Config{Loader: loader{"u": {Body: text}}, Rules: []rules.Rule{phoneRule}, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	f := facet(func(c *snapshot.DetectContent) {
		c.LabelMap = map[string]string{"private_person": "person", "phone": "phone", "email": "email"}
		c.Categories = []string{"person", "phone", "email"}
	})
	r, err := s.DetectUnit(ctx, ref("u"), f)
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(text)
	found := map[string]string{}
	var phoneSources []kernel.FindingSource
	for _, c := range r.Candidates {
		found[c.Category] = string(runes[c.Span.Start:c.Span.End])
		if c.Category == "phone" {
			phoneSources = append(phoneSources, c.Source)
		}
	}
	if found["person"] != "张伟" || found["phone"] != "13812345678" || found["email"] != "zhangwei@example.com" {
		t.Fatalf("found %v from %+v", found, r.Candidates)
	}
	if len(phoneSources) != 2 {
		t.Fatalf("the phone must be found by both the rule and the model: %v", phoneSources)
	}
	if r.Stats.Model < 3 || r.Stats.Rule != 1 {
		t.Fatalf("stats %+v", r.Stats)
	}
	// a second call on the same worker (new attempt) works too, and non-personal text yields nothing
	empty, err := s.DetectUnit(ctx, ref("u"), f)
	if err != nil || len(empty.Candidates) != len(r.Candidates) {
		t.Fatalf("repeat: %v %v", empty, err)
	}
}

// Position-mapping regression with the real model: every candidate must slice
// the body to exactly the sensitive value, whatever precedes it (astral
// characters, combining marks, full-width forms, CRLF) and whether or not
// synthetic context surrounds the body.
func TestRealModelCandidatesSliceTheBodyExactlyAcrossAwkwardText(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	modelDir := filepath.Join(root, ".pi", "m0-resources", "astrlink-guard-int8")
	manifest := filepath.Join(root, "resources", "models", "astrlink-guard-int8.json")
	if _, err := os.Stat(filepath.Join(modelDir, "model_int8.onnx")); err != nil {
		t.Skip("needs the fixed M0 model resource")
	}
	worker, err := host.Start(host.Spec{Path: python,
		Args: []string{"-I", "-m", "tacet_detect_astrlink", "--model-dir", modelDir, "--manifest", manifest},
		Dir:  t.TempDir(), Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, Generation: "gen_detect_positions_01"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err = worker.Session.Initialize(ctx, "tacet.astrlink", "0.1.0", 5); err != nil {
		t.Fatal(err)
	}
	model, err := NewPluginModel(worker.Session, nextAttempt(5))
	if err != nil {
		t.Fatal(err)
	}
	f := facet(func(c *snapshot.DetectContent) {
		c.LabelMap = map[string]string{"private_person": "person", "phone": "phone", "email": "email"}
		c.Categories = []string{"person", "phone", "email"}
	})
	cases := []struct {
		name         string
		prefix, body string
		suffix       string
		want         map[string]string
	}{
		{"astral prefix", "", "😀😀 联系 Jane Doe，邮箱 jane.doe@example.org", "", map[string]string{"email": "jane.doe@example.org"}},
		{"combining marks", "", "e\u0301e\u0301e\u0301 请联系张伟，电话 13812345678。", "", map[string]string{"phone": "13812345678"}},
		{"CRLF lines", "", "第一行\r\n第二行\r\n请联系张伟，邮箱 zhangwei@example.com。\r\n", "", map[string]string{"email": "zhangwei@example.com"}},
		{"full-width punctuation", "", "联系人：张伟；手机：13812345678；", "", map[string]string{"phone": "13812345678"}},
		{"synthetic prefix and suffix", "表头：电话 13900000000，邮箱 header@example.com\n", "请联系张伟，邮箱 zhangwei@example.com。", "\n备注：13700000000", map[string]string{"email": "zhangwei@example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(Config{Loader: loader{"u": {Prefix: c.prefix, Body: c.body, Suffix: c.suffix}}, Rules: []rules.Rule{phoneRule}, Model: model})
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.DetectUnit(ctx, ref("u"), f)
			if err != nil {
				t.Fatal(err)
			}
			body := []rune(c.body)
			got := map[string]bool{}
			for _, cand := range r.Candidates {
				if cand.Span.Start < 0 || cand.Span.End > len(body) || cand.Span.Start >= cand.Span.End {
					t.Fatalf("candidate outside the body: %+v", cand)
				}
				got[cand.Category+"="+string(body[cand.Span.Start:cand.Span.End])] = true
			}
			for cat, value := range c.want {
				if !got[cat+"="+value] {
					t.Errorf("missing %s=%q in %v", cat, value, got)
				}
			}
			for key := range got { // nothing from the synthetic context may appear
				if strings.Contains(key, "13900000000") || strings.Contains(key, "header@") || strings.Contains(key, "13700000000") {
					t.Errorf("a synthetic-context value became a candidate: %s", key)
				}
			}
		})
	}
}
