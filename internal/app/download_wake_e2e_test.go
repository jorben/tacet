//go:build darwin

package app

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/sched"
)

const stalledModel = "astrlink-guard-int8"

// stalledHub serves one model file: half of it, then it waits for the test to say how the transfer ends. From reached
// until then the download holds its network lease.
type stalledHub struct {
	body    []byte
	reached chan struct{}
	proceed chan struct{}
	cut     bool // set before goOn
	reach   sync.Once
	goes    sync.Once
}

func (h *stalledHub) goOn() { h.goes.Do(func() { close(h.proceed) }) }

func (h *stalledHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	half := len(h.body) / 2
	w.Header().Set("ETag", `"stalled"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(h.body)))
	w.Write(h.body[:half])
	w.(http.Flusher).Flush()
	h.reach.Do(func() { close(h.reached) })
	select {
	case <-h.proceed:
	case <-r.Context().Done():
		return
	}
	if h.cut {
		panic(http.ErrAbortHandler)
	}
	w.Write(h.body[half:])
}

// downloadRig is a workspace with a model to download from a stalledHub. Its manifest lists one small file, so what
// follows the transfer is real up to the load, which fails: there is no model in it.
func downloadRig(t *testing.T) (*parkRig, *stalledHub) {
	t.Helper()
	hub := &stalledHub{body: []byte(strings.Repeat("model bytes ", 64)), reached: make(chan struct{}), proceed: make(chan struct{})}
	srv := httptest.NewTLSServer(hub)
	t.Cleanup(srv.Close)
	t.Cleanup(hub.goOn) // before the server closes, which waits for the handler (cleanups run last first)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	src := &ModelSource{Base: srv.URL, Hosts: []string{"127.0.0.1"}, TLS: &tls.Config{RootCAs: pool}}

	r, _ := root(t)
	raw, err := os.ReadFile(filepath.Join(r, "resources", "models", stalledModel+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(hub.body)
	doc["files"] = map[string]any{"model.bin": map[string]any{"sha256": hex.EncodeToString(sum[:]), "size": len(hub.body)}}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), stalledModel+".json")
	if err = os.WriteFile(manifest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	model := &ModelOption{ManifestPath: manifest, Plugin: builtin(t, r, "detect-astrlink", "tacet_detect_astrlink")}

	rig := &parkRig{t: t, ws: filepath.Join(t.TempDir(), "ws"), in: t.TempDir(), out: t.TempDir(), key: &memKey{}, ctx: t.Context()}
	rig.e = openEngineWith(t, rig.ws, rig.key, true, func(o *Options) {
		o.Models, o.ModelsRoot, o.ModelSource = []*ModelOption{model}, t.TempDir(), src
	})
	t.Cleanup(func() { rig.e.Close() })
	return rig, hub
}

// waitQueue waits for the queue to reach a condition. The limit only keeps a broken scheduler from taking the rest of
// the package down with it (go test -timeout would): getting there takes a wake-up, no plugin.
func waitQueue(t *testing.T, e *Engine, what string, reached func(sched.QueueState) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		s := e.queue.State()
		if reached(s) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: pending=%d running=%d", what, s.Pending, s.Running)
		}
		time.Sleep(time.Millisecond)
	}
}

// What a person does in the model page's minutes: review goes on, and what it asks for waits for the network lease. When
// the download ends, however it ends, that work runs, with no pause and resume (#43). Nothing here notifies the queue:
// the download's own end has to.
func TestWorkQueuedBehindADownloadRunsWhenTheDownloadEnds(t *testing.T) {
	finished := func(_ *parkRig, hub *stalledHub) { hub.goOn() }
	cut := func(_ *parkRig, hub *stalledHub) { hub.cut = true; hub.goOn() }
	paused := func(r *parkRig, _ *stalledHub) {
		if err := r.e.PauseModel(r.ctx, stalledModel); err != nil {
			r.t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name         string
		end          func(*parkRig, *stalledHub)
		export       bool // the file is ready and the person exports it; otherwise they decide a finding
		state, model string
		code         string
	}{
		{name: "a finished download, a decision", end: finished, state: "awaiting_review", model: "error", code: "MODEL_PREPARE_FAILED"},
		{name: "a paused download, a decision", end: paused, state: "awaiting_review", model: "paused"},
		{name: "a failed download, a decision", end: cut, state: "awaiting_review", model: "error", code: "MODEL_NETWORK"},
		{name: "a finished download, an export", end: finished, export: true, state: "completed", model: "error", code: "MODEL_PREPARE_FAILED"},
		{name: "a paused download, an export", end: paused, export: true, state: "completed", model: "paused"},
		{name: "a failed download, an export", end: cut, export: true, state: "completed", model: "error", code: "MODEL_NETWORK"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, hub := downloadRig(t)
			st, err := r.e.Import(r.ctx, ImportRequest{Kind: "quick", Files: []string{r.file(needsReview)}, OutputDir: r.out, Preset: PresetID})
			if err != nil {
				t.Fatal(err)
			}
			if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Preset: PresetID}); err != nil {
				t.Fatal(err)
			}
			if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
				t.Fatal(err)
			}
			job := st.Files[0].ID
			fs, err := r.e.Findings(r.ctx, job, 0, 50)
			if err != nil || len(fs) != 2 || st.Files[0].State != "awaiting_review" {
				t.Fatalf("%+v %+v %v", st, fs, err)
			}
			if c.export {
				for _, f := range fs {
					if err = r.e.DecideFinding(r.ctx, job, f.ID, false); err != nil {
						t.Fatal(err)
					}
				}
				if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
					t.Fatalf("%+v %v", st, err)
				}
			}

			if err = r.e.DownloadModel(r.ctx, stalledModel); err != nil {
				t.Fatal(err)
			}
			<-hub.reached
			if c.export {
				err = r.e.Publish(r.ctx, st.ID, false, "")
			} else {
				err = r.e.DecideFinding(r.ctx, job, fs[0].ID, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			waitQueue(t, r.e, "the work never reached the queue", func(s sched.QueueState) bool { return s.Pending == 1 })
			// It is the lease that holds the work back, which this asks directly: Take admits nothing while it is held.
			if ticket, why := r.e.queue.Take(); ticket != nil || why != sched.WaitActivity {
				t.Fatalf("with the download running the queue says %q", why)
			}

			c.end(r, hub)
			waitQueue(t, r.e, "the download ended and the work still waits", func(s sched.QueueState) bool { return s.Pending == 0 })
			if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != c.state {
				t.Fatalf("%+v %v", st, err)
			}
			if c.export {
				out, err := os.ReadDir(r.out)
				if err != nil {
					t.Fatal(err)
				}
				text := ""
				for _, entry := range out {
					if data, err := os.ReadFile(filepath.Join(r.out, entry.Name())); err == nil && strings.HasSuffix(entry.Name(), ".txt") {
						text += string(data)
					}
				}
				if !strings.Contains(text, "证件") || strings.Contains(text, "110101199003074519") {
					t.Fatalf("what was delivered: %q (%v)", text, out)
				}
			}
			// The test's three ways of ending are the ones it says they are, and the model's state tells them apart.
			m := waitModel(t, r.e, stalledModel, func(m ModelRuntime) bool { return m.State == c.model })
			if m.Code != c.code {
				t.Fatalf("%+v", m)
			}
		})
	}
}
