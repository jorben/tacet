package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

var payloads = map[string][]byte{
	"config.json": []byte(`{"a":1}`),
	"model.onnx":  bytes.Repeat([]byte("0123456789abcdef"), 4096), // 64 KiB
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func manifestJSON(t *testing.T, files map[string][]byte) []byte {
	m := map[string]any{"schema_version": 1, "id": "test-model", "version": "1.0.0", "variant": "int8", "backend": "CPUExecutionProvider", "license": "Apache-2.0"}
	fs := map[string]any{}
	for n, b := range files {
		fs[n] = map[string]any{"sha256": sum(b), "size": len(b)}
	}
	m["files"] = fs
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type server struct {
	*httptest.Server
	mu      sync.Mutex
	etag    string
	cutAt   int           // close the connection after this many bytes of the first model.onnx response (0: never)
	cuts    int           // how many times to cut
	corrupt string        // file name to serve corrupted
	extra   int           // append this many bytes to model.onnx
	ranges  []string      // Range headers seen
	hold    chan struct{} // if set, model.onnx waits for it after the first half
	half    chan struct{}
	other   map[string][]byte
}

func newServer(t *testing.T) *server {
	s := &server{etag: `"v1"`}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		s.mu.Lock()
		body, ok := payloads[name]
		if o, has := s.other[name]; has {
			body, ok = o, true
		}
		etag, cut, corrupt, extra := s.etag, s.cutAt, s.corrupt, s.extra
		if r.Header.Get("Range") != "" {
			s.ranges = append(s.ranges, name+" "+r.Header.Get("Range"))
		}
		if cut > 0 && name == "model.onnx" && s.cuts > 0 {
			s.cuts--
		} else {
			cut = 0
		}
		hold, half := s.hold, s.half
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		body = append([]byte(nil), body...)
		if name == corrupt {
			body[0] ^= 1
		}
		if name == "model.onnx" && extra > 0 {
			body = append(body, bytes.Repeat([]byte("x"), extra)...)
		}
		w.Header().Set("ETag", etag)
		if cut > 0 {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Write(body[:cut])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		if hold != nil && name == "model.onnx" && r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Write(body[:len(body)/2])
			w.(http.Flusher).Flush()
			close(half)
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(s.Close)
	return s
}

type rig struct {
	t        *testing.T
	svc      *Service
	srv      *server
	gate     *netgate.Gate
	entry    Entry
	free     int64
	prepare  func(context.Context, string, Entry) error
	prepared []string
	commit   func(context.Context, func(store.Tx) error) error
	db       *store.DB
	root     string
	urlHost  string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, srv: newServer(t), gate: netgate.NewGate(), free: 1 << 30, root: t.TempDir()}
	e, err := ParseManifest(manifestJSON(t, payloads))
	if err != nil {
		t.Fatal(err)
	}
	r.entry = e
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "m.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	r.db, err = store.Open(context.Background(), raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.db.Close() })
	var mu sync.Mutex
	r.commit = func(ctx context.Context, fn func(store.Tx) error) error {
		mu.Lock()
		defer mu.Unlock()
		tx, err := r.db.Begin(ctx)
		if err != nil {
			return err
		}
		if err = fn(tx); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	r.prepare = func(context.Context, string, Entry) error { return nil }
	r.rebuild()
	return r
}

func (r *rig) rebuild() {
	r.t.Helper()
	tlsCfg := r.srv.Client().Transport.(*http.Transport).TLSClientConfig
	s, err := New(Config{Commit: r.commit, Root: r.root, Gate: r.gate, Hosts: []string{"127.0.0.1"}, TLS: tlsCfg,
		URL:  func(e Entry, f string) (string, error) { return r.srv.URL + "/" + f, nil },
		Free: func(string) (int64, error) { return r.free, nil },
		Prepare: func(ctx context.Context, dir string, e Entry) error {
			r.prepared = append(r.prepared, dir)
			return r.prepare(ctx, dir, e)
		},
		Catalog: []Entry{r.entry}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.svc = s
}

func (r *rig) status() Status {
	r.t.Helper()
	l, err := r.svc.List(context.Background())
	if err != nil || len(l) != 1 {
		r.t.Fatalf("%v %v", l, err)
	}
	return l[0]
}

var _ = tls.Config{}

func TestManifestIsParsedStrictlyAndItsDigestCoversEveryFile(t *testing.T) {
	good := manifestJSON(t, payloads)
	e, err := ParseManifest(good)
	if err != nil || len(e.Files) != 2 || e.Files[0].Name != "config.json" || !strings.HasPrefix(e.Digest, "sha256:") || e.Total() != int64(len(payloads["config.json"])+len(payloads["model.onnx"])) {
		t.Fatalf("%+v %v", e, err)
	}
	other := map[string][]byte{"config.json": payloads["config.json"], "model.onnx": append([]byte("x"), payloads["model.onnx"][1:]...)}
	e2, _ := ParseManifest(manifestJSON(t, other))
	if e2.Digest == e.Digest || e2.Key() == e.Key() {
		t.Fatal("a different file must be a different package")
	}
	again, _ := ParseManifest(good)
	if again.Digest != e.Digest {
		t.Fatal("digest not deterministic")
	}
	mut := func(f func(m map[string]any)) []byte {
		var m map[string]any
		json.Unmarshal(good, &m)
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	files := func(m map[string]any) map[string]any { return m["files"].(map[string]any) }
	big := map[string]any{}
	for i := 0; i <= maxFiles; i++ {
		big[fmt.Sprintf("f%d", i)] = map[string]any{"sha256": sum([]byte{1}), "size": 1}
	}
	for name, data := range map[string][]byte{
		"not json": []byte("x"), "wrong schema": mut(func(m map[string]any) { m["schema_version"] = 2 }),
		"bad id": mut(func(m map[string]any) { m["id"] = "../x" }), "no license": mut(func(m map[string]any) { m["license"] = "" }),
		"bad version": mut(func(m map[string]any) { m["version"] = "" }), "no files": mut(func(m map[string]any) { m["files"] = map[string]any{} }),
		"path in name":  mut(func(m map[string]any) { files(m)["../evil"] = files(m)["config.json"] }),
		"slash in name": mut(func(m map[string]any) { files(m)["a/b"] = files(m)["config.json"] }),
		"short hash":    mut(func(m map[string]any) { files(m)["config.json"] = map[string]any{"sha256": "abc", "size": 1} }),
		"upper hash": mut(func(m map[string]any) {
			files(m)["config.json"] = map[string]any{"sha256": strings.ToUpper(sum([]byte{1})), "size": 1}
		}),
		"zero size":      mut(func(m map[string]any) { files(m)["config.json"] = map[string]any{"sha256": sum([]byte{1}), "size": 0} }),
		"too many files": mut(func(m map[string]any) { m["files"] = big }),
		"too large": mut(func(m map[string]any) {
			files(m)["config.json"] = map[string]any{"sha256": sum([]byte{1}), "size": maxModelSize + 1}
		}),
		"total over the cap": mut(func(m map[string]any) {
			m["files"] = map[string]any{"a": map[string]any{"sha256": sum([]byte{1}), "size": maxModelSize/2 + 1}, "b": map[string]any{"sha256": sum([]byte{2}), "size": maxModelSize/2 + 1}}
		}),
		"bad backend": mut(func(m map[string]any) { m["backend"] = "a b" }),
	} {
		if _, err := ParseManifest(data); !errors.Is(err, ErrManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestADownloadVerifiesRegistersAndPreparesTheModel(t *testing.T) {
	r := newRig(t)
	if s := r.status(); s.State != StatePending || s.Downloaded != 0 {
		t.Fatalf("%+v", s)
	}
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	s := r.status()
	if s.State != StateReady || s.Downloaded != s.Total {
		t.Fatalf("%+v", s)
	}
	dir := filepath.Join(r.root, "installed", r.entry.Key())
	for n, b := range payloads {
		got, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("%s: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "staging", r.entry.Key())); err == nil {
		t.Fatal("staging was left behind")
	}
	if len(r.prepared) != 1 || r.prepared[0] != dir {
		t.Fatalf("the model must be prepared from the registered directory: %v", r.prepared)
	}
	if st := r.gate.State(); len(st) != 0 {
		t.Fatalf("the network lease was not released: %v", st)
	}
	b, err := r.svc.CheckBinding(context.Background(), "test-model", r.entry.Digest)
	if err != nil || !b.Ready || b.Dir != dir {
		t.Fatalf("%+v %v", b, err)
	}
	if err := r.svc.Download(context.Background(), r.entry.Key()); !errors.Is(err, ErrNotDownloadable) {
		t.Fatalf("a ready model is not downloaded again: %v", err)
	}
}

func TestAnInterruptedDownloadKeepsWhatArrivedAndResumesFromThere(t *testing.T) {
	r := newRig(t)
	r.srv.cutAt, r.srv.cuts = 10000, 1
	err := r.svc.Download(context.Background(), r.entry.Key())
	if err == nil {
		t.Fatal("a cut transfer reported success")
	}
	if s := r.status(); s.State != StateError || s.Code != CodeNetwork {
		t.Fatalf("%+v", s)
	}
	part := filepath.Join(r.root, "staging", r.entry.Key(), "model.onnx.part")
	if info, err := os.Stat(part); err != nil || info.Size() != 10000 {
		t.Fatalf("the partial file must be kept: %v %v", info, err)
	}
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if got := r.srv.ranges; len(got) != 1 || got[0] != "model.onnx bytes=10000-" {
		t.Fatalf("the second attempt must ask only for the rest: %v", got)
	}
	if s := r.status(); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
}

func TestAServerThatReplacedTheFileMakesTheDownloadRestartNotStitch(t *testing.T) {
	r := newRig(t)
	r.srv.cutAt, r.srv.cuts = 10000, 1
	r.svc.Download(context.Background(), r.entry.Key())
	r.srv.mu.Lock()
	r.srv.etag = `"v2"`
	r.srv.mu.Unlock()
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
	got, _ := os.ReadFile(filepath.Join(r.root, "installed", r.entry.Key(), "model.onnx"))
	if !bytes.Equal(got, payloads["model.onnx"]) {
		t.Fatal("the installed file is not the verified one")
	}
}

func TestBytesThatDoNotMatchTheManifestAreNeverRegisteredAndStagingIsCleared(t *testing.T) {
	for name, set := range map[string]func(*server){
		"corrupted bytes": func(s *server) { s.corrupt = "model.onnx" },
		"longer body":     func(s *server) { s.extra = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			set(r.srv)
			if err := r.svc.Download(context.Background(), r.entry.Key()); err == nil {
				t.Fatal("accepted")
			}
			if s := r.status(); s.State != StateError || s.Code != CodeVerify {
				t.Fatalf("%+v", s)
			}
			for _, d := range []string{"staging", "installed"} {
				if es, _ := os.ReadDir(filepath.Join(r.root, d)); len(es) != 0 {
					t.Fatalf("%s holds %v", d, es)
				}
			}
			if len(r.prepared) != 0 {
				t.Fatal("an unverified model was prepared")
			}
		})
	}
}

func TestAFailedRedownloadNeverReplacesAnInstalledModel(t *testing.T) {
	r := newRig(t)
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.root, "installed", r.entry.Key())
	// the installed copy is damaged, so a download is offered, and it fails verification
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("tampered"), 0o600)
	if _, err := r.svc.CheckBinding(context.Background(), "test-model", r.entry.Digest); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("%v", err)
	}
	if s := r.status(); s.State != StateError || s.Code != CodeChanged {
		t.Fatalf("%+v", s)
	}
	if err := r.svc.Download(context.Background(), r.entry.Key()); !errors.Is(err, ErrNotDownloadable) {
		t.Fatalf("installed files exist: download is not the repair, got %v", err)
	}
}

func TestNotEnoughDiskStopsBeforeAnyRequest(t *testing.T) {
	r := newRig(t)
	r.free = r.entry.Total() // no room for the reserve
	if err := r.svc.Download(context.Background(), r.entry.Key()); err == nil {
		t.Fatal("started")
	}
	if s := r.status(); s.State != StateError || s.Code != CodeDisk {
		t.Fatalf("%+v", s)
	}
	if len(r.srv.ranges) != 0 {
		t.Fatal("a request was made")
	}
	r.free = 1 << 30
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatalf("retry after freeing space: %v", err)
	}
}

func TestSpaceAlreadyHeldInStagingIsNotCountedAgain(t *testing.T) {
	r := newRig(t)
	r.srv.cutAt, r.srv.cuts = 60000, 1
	r.svc.Download(context.Background(), r.entry.Key())
	r.free = r.entry.Total() - 60000 + r.entry.Total()/10 // exactly the rest plus the reserve
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestADownloadWhileDocumentsAreProcessedDoesNotStartAndChangesNothing(t *testing.T) {
	r := newRig(t)
	lease, err := r.gate.Acquire(netgate.Processing)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	err = r.svc.Download(context.Background(), r.entry.Key())
	var busy *netgate.BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("%v", err)
	}
	if s := r.status(); s.State != StatePending || s.Code != "" {
		t.Fatalf("%+v", s)
	}
	if len(r.srv.ranges) != 0 {
		t.Fatal("a request was made")
	}
}

func TestOnlyTheAllowlistedHostIsReached(t *testing.T) {
	r := newRig(t)
	r.svc.cfg.URL = func(Entry, string) (string, error) { return "https://example.com/x", nil }
	if err := r.svc.Download(context.Background(), r.entry.Key()); !errors.Is(err, netgate.ErrNetworkTarget) {
		t.Fatalf("%v", err)
	}
}

func TestPauseKeepsTheTransferAndContinueFinishesIt(t *testing.T) {
	r := newRig(t)
	r.srv.hold, r.srv.half = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- r.svc.Download(context.Background(), r.entry.Key()) }()
	select {
	case <-r.srv.half:
	case <-time.After(10 * time.Second):
		t.Fatal("download did not start")
	}
	time.Sleep(100 * time.Millisecond)
	if err := r.svc.Pause(r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrPaused) {
		t.Fatalf("%v", err)
	}
	if s := r.status(); s.State != StatePaused || s.Downloaded == 0 {
		t.Fatalf("%+v", s)
	}
	if st := r.gate.State(); len(st) != 0 {
		t.Fatalf("a paused download holds the lease: %v", st)
	}
	r.srv.mu.Lock()
	r.srv.hold = nil
	r.srv.mu.Unlock()
	if err := r.svc.Download(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
	if err := r.svc.Pause(r.entry.Key()); !errors.Is(err, ErrNotDownloadable) {
		t.Fatalf("nothing is running: %v", err)
	}
}

func TestOnlyOneOperationPerModelRunsAtATime(t *testing.T) {
	r := newRig(t)
	r.srv.hold, r.srv.half = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- r.svc.Download(context.Background(), r.entry.Key()) }()
	<-r.srv.half
	if err := r.svc.Download(context.Background(), r.entry.Key()); !errors.Is(err, ErrBusy) {
		t.Fatalf("%v", err)
	}
	if err := r.svc.Discard(context.Background(), r.entry.Key()); !errors.Is(err, ErrBusy) {
		t.Fatalf("%v", err)
	}
	r.svc.PauseAll()
	<-done
}

func TestAFailedPrepareIsRecoverableAndNeverReady(t *testing.T) {
	r := newRig(t)
	r.prepare = func(context.Context, string, Entry) error { return errors.New("sample run failed") }
	if err := r.svc.Download(context.Background(), r.entry.Key()); !errors.Is(err, ErrFailed) {
		t.Fatalf("%v", err)
	}
	if s := r.status(); s.State != StateError || s.Code != CodePrepare {
		t.Fatalf("%+v", s)
	}
	if b, err := r.svc.CheckBinding(context.Background(), "test-model", r.entry.Digest); err != nil || b.Ready {
		t.Fatalf("the files are fine but the model was never loaded: %+v %v", b, err)
	}
	r.prepare = func(context.Context, string, Entry) error { return nil }
	if err := r.svc.Prepare(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
	if len(r.srv.ranges) != 0 {
		t.Fatal("preparing again must not download")
	}
}

func TestPrepareRefusesWhatIsNotInstalledAndFilesThatChanged(t *testing.T) {
	r := newRig(t)
	if err := r.svc.Prepare(context.Background(), r.entry.Key()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("%v", err)
	}
	r.svc.Download(context.Background(), r.entry.Key())
	os.WriteFile(filepath.Join(r.root, "installed", r.entry.Key(), "model.onnx"), bytes.Repeat([]byte("z"), len(payloads["model.onnx"])), 0o600)
	r.svc.SessionLost(context.Background(), r.entry.Key())
	n := len(r.prepared)
	if err := r.svc.Prepare(context.Background(), r.entry.Key()); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("%v", err)
	}
	if len(r.prepared) != n {
		t.Fatal("changed files were loaded")
	}
	if s := r.status(); s.State != StateError || s.Code != CodeChanged {
		t.Fatalf("%+v", s)
	}
	if err := r.svc.Prepare(context.Background(), "nope"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("%v", err)
	}
}

func TestALostSessionNeedsALoadAgainButNoDownload(t *testing.T) {
	r := newRig(t)
	r.svc.Download(context.Background(), r.entry.Key())
	if err := r.svc.SessionLost(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StateInstalled {
		t.Fatalf("%+v", s)
	}
	if b, err := r.svc.CheckBinding(context.Background(), "test-model", r.entry.Digest); err != nil || b.Ready {
		t.Fatalf("%+v %v", b, err)
	}
	if err := r.svc.SessionLost(context.Background(), r.entry.Key()); err != nil {
		t.Fatal(err)
	}
}

func TestBindingIsCheckedByIdAndDigestAndNeverFallsBack(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.svc.CheckBinding(ctx, "test-model", r.entry.Digest); !errors.Is(err, ErrBindingMissing) {
		t.Fatalf("not installed: %v", err)
	}
	r.svc.Download(ctx, r.entry.Key())
	for name, c := range map[string][2]string{"other id": {"another", r.entry.Digest}, "other digest": {"test-model", "sha256:" + strings.Repeat("0", 64)}, "empty": {"", ""}} {
		if _, err := r.svc.CheckBinding(ctx, c[0], c[1]); !errors.Is(err, ErrBindingMissing) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.svc.CheckBinding(ctx, "test-model", r.entry.Digest); err != nil {
		t.Fatal(err)
	}
	// a file removed
	os.Remove(filepath.Join(r.root, "installed", r.entry.Key(), "config.json"))
	if _, err := r.svc.CheckBinding(ctx, "test-model", r.entry.Digest); !errors.Is(err, ErrBindingMissing) {
		t.Fatalf("%v", err)
	}
}

func TestACachedVerificationIsRedoneWhenAFileChangesWithoutChangingSize(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.svc.Download(ctx, r.entry.Key())
	path := filepath.Join(r.root, "installed", r.entry.Key(), "model.onnx")
	if _, err := r.svc.CheckBinding(ctx, "test-model", r.entry.Digest); err != nil {
		t.Fatal(err)
	}
	b := append([]byte(nil), payloads["model.onnx"]...)
	b[100] ^= 1
	os.WriteFile(path, b, 0o600)
	later := time.Now().Add(time.Hour)
	os.Chtimes(path, later, later)
	if _, err := r.svc.CheckBinding(ctx, "test-model", r.entry.Digest); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("%v", err)
	}
}

func TestRecoverPausesInterruptedWorkOffersInstalledModelsAgainAndTouchesNoNetwork(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	key := r.entry.Key()
	set := func(st State) { r.svc.set(ctx, r.entry, st, "") }
	for _, c := range []struct{ from, to State }{{StateDownloading, StatePaused}, {StateVerifying, StatePaused}, {StatePaused, StatePaused}, {StatePending, StatePending}, {StateError, StateError}} {
		set(c.from)
		if err := r.svc.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if s := r.status(); s.State != c.to {
			t.Fatalf("%s -> %s, want %s", c.from, s.State, c.to)
		}
	}
	os.MkdirAll(filepath.Join(r.root, "installed", key), 0o700)
	for _, from := range []State{StateReady, StatePreparing} {
		set(from)
		r.svc.Recover(ctx)
		if s := r.status(); s.State != StateInstalled {
			t.Fatalf("%s -> %s", from, s.State)
		}
	}
	os.RemoveAll(filepath.Join(r.root, "installed", key))
	set(StateReady)
	r.svc.Recover(ctx)
	if s := r.status(); s.State != StateError || s.Code != CodeChanged {
		t.Fatalf("a registered model whose files vanished: %+v", s)
	}
	if len(r.srv.ranges) != 0 || len(r.gate.State()) != 0 {
		t.Fatal("recovery used the network")
	}
}

func TestListShowsAReadyStateOnlyForTheSessionThatPreparedIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.svc.Download(ctx, r.entry.Key())
	r.rebuild() // a new process over the same store and files
	if s := r.status(); s.State != StateInstalled {
		t.Fatalf("a new session must not show the old readiness: %+v", s)
	}
}

func TestDiscardClearsAPartialDownload(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.srv.cutAt, r.srv.cuts = 10000, 1
	r.svc.Download(ctx, r.entry.Key())
	if err := r.svc.Discard(ctx, r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StatePending || s.Downloaded != 0 {
		t.Fatalf("%+v", s)
	}
	if err := r.svc.Download(ctx, r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if len(r.srv.ranges) != 0 {
		t.Fatalf("a discarded partial file must not be resumed: %v", r.srv.ranges)
	}
	if err := r.svc.Discard(ctx, "nope"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.svc.Discard(ctx, r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StateInstalled {
		t.Fatalf("discarding staging must not drop an installed model: %+v", s)
	}
}

func TestStagingWithAFileTheManifestDoesNotListIsNotRegistered(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.srv.cutAt, r.srv.cuts = 10000, 1
	r.svc.Download(ctx, r.entry.Key())
	os.WriteFile(filepath.Join(r.root, "staging", r.entry.Key(), "evil.py"), []byte("import os"), 0o600)
	if err := r.svc.Download(ctx, r.entry.Key()); err == nil {
		t.Fatal("registered a staging directory with an extra file")
	}
	if s := r.status(); s.State != StateError || s.Code != CodeVerify {
		t.Fatalf("%+v", s)
	}
	if es, _ := os.ReadDir(filepath.Join(r.root, "installed")); len(es) != 0 {
		t.Fatal("something was registered")
	}
	if _, err := os.Stat(filepath.Join(r.root, "staging", r.entry.Key())); err == nil {
		t.Fatal("an unverified staging directory was kept")
	}
}

func TestAPartialFileWithoutAKnownEntityIsRestartedNotResumed(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.srv.cutAt, r.srv.cuts = 10000, 1
	r.svc.Download(ctx, r.entry.Key())
	r.svc.setEntity(ctx, r.entry, "", "") // the resume record is gone, the partial bytes are not
	if err := r.svc.Download(ctx, r.entry.Key()); err != nil {
		t.Fatalf("%v", err)
	}
	if len(r.srv.ranges) != 0 {
		t.Fatalf("a partial file whose version is unknown must not be resumed: %v", r.srv.ranges)
	}
	if s := r.status(); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
}

func TestAResumeRecordBoundToAnotherFileIsNotUsed(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.srv.cutAt, r.srv.cuts = 10000, 1
	r.svc.Download(ctx, r.entry.Key())
	r.svc.setEntity(ctx, r.entry, "config.json", `"v1"`) // a valid-looking entity, but for a different file
	if err := r.svc.Download(ctx, r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if len(r.srv.ranges) != 0 {
		t.Fatalf("%v", r.srv.ranges)
	}
}

func TestSessionLostLeavesAnErrorStateAlone(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.svc.Download(ctx, r.entry.Key())
	os.WriteFile(filepath.Join(r.root, "installed", r.entry.Key(), "config.json"), []byte("tampered"), 0o600)
	r.svc.CheckBinding(ctx, "test-model", r.entry.Digest) // -> error / files changed
	if err := r.svc.SessionLost(ctx, r.entry.Key()); err != nil {
		t.Fatal(err)
	}
	if s := r.status(); s.State != StateError || s.Code != CodeChanged {
		t.Fatalf("a lost session must not turn a damaged model into an installed one: %+v", s)
	}
}

func TestAServerFileOfAnotherSizeThanTheCatalogIsRefused(t *testing.T) {
	r := newRig(t)
	r.srv.other = map[string][]byte{"model.onnx": append([]byte("x"), payloads["model.onnx"]...)}
	if err := r.svc.Download(context.Background(), r.entry.Key()); err == nil {
		t.Fatal("accepted")
	}
	if s := r.status(); s.State != StateError || s.Code != CodeVerify {
		t.Fatalf("%+v", s)
	}
}

func TestConfigurationMustBeCompleteAndTheCatalogUnambiguous(t *testing.T) {
	r := newRig(t)
	ok := Config{Commit: r.commit, Root: r.root, Gate: r.gate, URL: r.svc.cfg.URL, Free: r.svc.cfg.Free, Prepare: r.svc.cfg.Prepare, Catalog: []Entry{r.entry}}
	edit := func(f func(*Config)) Config { c := ok; f(&c); return c }
	for name, c := range map[string]Config{
		"no commit": edit(func(c *Config) { c.Commit = nil }), "no gate": edit(func(c *Config) { c.Gate = nil }), "no url": edit(func(c *Config) { c.URL = nil }),
		"no free": edit(func(c *Config) { c.Free = nil }), "no prepare": edit(func(c *Config) { c.Prepare = nil }), "relative root": edit(func(c *Config) { c.Root = "rel" }),
		"empty catalog": edit(func(c *Config) { c.Catalog = nil }), "duplicate": edit(func(c *Config) { c.Catalog = []Entry{r.entry, r.entry} }),
		"entry without digest": edit(func(c *Config) { c.Catalog = []Entry{{ID: "x"}} }),
	} {
		if _, err := New(c); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := New(ok); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Download(context.Background(), "nope"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
}

func TestAManifestIsInterpretedOnlyAfterItsSignatureVerified(t *testing.T) {
	data := manifestJSON(t, payloads)
	sig := []byte("sig")
	calls := 0
	ok := func(p, s []byte) error {
		calls++
		if !bytes.Equal(p, data) || !bytes.Equal(s, sig) {
			return errors.New("bad")
		}
		return nil
	}
	e, err := ParseSigned(data, sig, ok)
	if err != nil || e.ID != "test-model" || calls != 1 {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := ParseSigned(data, []byte("other"), ok); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("%v", err)
	}
	if _, err := ParseSigned(data, sig, nil); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("no verifier: %v", err)
	}
	// a valid signature does not excuse a malformed manifest, and a bad one never reaches the parser
	garbage := []byte("not a manifest")
	if _, err := ParseSigned(garbage, sig, func([]byte, []byte) error { return nil }); !errors.Is(err, ErrManifest) {
		t.Fatalf("%v", err)
	}
	if _, err := ParseSigned(garbage, sig, func([]byte, []byte) error { return errors.New("x") }); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("%v", err)
	}
}
