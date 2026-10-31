package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
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

	"github.com/jorben/tacet/internal/netgate"
)

var pkg = bytes.Repeat([]byte("package-bytes-"), 5000)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func releaseJSON(version string, edit func(map[string]any)) []byte {
	m := map[string]any{"schema_version": 1, "version": version, "platform": "darwin-arm64", "channel": "stable", "python_abi": "cp313",
		"package": map[string]any{"name": "tacet-" + version + ".pkg", "size": len(pkg), "sha256": sha(pkg)}}
	if edit != nil {
		edit(m)
	}
	b, _ := json.Marshal(m)
	return b
}

type tasks struct {
	review, recoverable int
	voided, reexec      int
	fail                bool
}

func (t *tasks) Pending(context.Context) (int, int, error) { return t.review, t.recoverable, nil }
func (t *tasks) Void(context.Context) error                { t.voided++; t.review, t.recoverable = 0, 0; return nil }
func (t *tasks) Reexecute(context.Context) error {
	if t.fail {
		return errors.New("x")
	}
	t.reexec++
	return nil
}

type quitter struct {
	order *[]string
	err   error
}

func (q quitter) Quit(context.Context) error { *q.order = append(*q.order, "quit"); return q.err }

type installer struct {
	gate  *netgate.Gate
	held  map[netgate.LeaseKind]uint64
	order *[]string
	path  string
	err   error
}

func (i *installer) Install(_ context.Context, p string, _ Release) error {
	*i.order = append(*i.order, "install")
	if i.gate != nil {
		i.held = i.gate.State()
	}
	i.path = p
	return i.err
}

type rig struct {
	t      *testing.T
	svc    *Service
	srv    *httptest.Server
	gate   *netgate.Gate
	key    ed25519.PrivateKey
	tasks  *tasks
	order  []string
	inst   *installer
	mu     sync.Mutex
	served map[string][]byte
	seen   []string
	cut    bool
	free   int64
	dir    string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := &rig{t: t, gate: netgate.NewGate(), key: priv, tasks: &tasks{}, served: map[string][]byte{}, free: 1 << 30, dir: t.TempDir()}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		r.seen = append(r.seen, q.URL.RequestURI())
		body, ok := r.served[q.URL.Path]
		cut := r.cut
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, q)
			return
		}
		if cut && strings.HasSuffix(q.URL.Path, ".pkg") {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Write(body[:len(body)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	ver, err := NewVerifier(map[string]ed25519.PublicKey{"k1": pub})
	if err != nil {
		t.Fatal(err)
	}
	r.inst = &installer{order: &r.order, gate: r.gate}
	r.svc, err = New(Config{Dir: r.dir, Gate: r.gate, Verifier: ver, Hosts: []string{"127.0.0.1"}, TLS: r.srv.Client().Transport.(*http.Transport).TLSClientConfig,
		ManifestURL: func(v, p, c string) (string, string, error) {
			q := "?v=" + v + "&p=" + p + "&c=" + c
			return r.srv.URL + "/release.json" + q, r.srv.URL + "/release.sig" + q, nil
		},
		PackageURL: func(rel Release) (string, error) { return r.srv.URL + "/" + rel.Name, nil },
		Version:    "1.0.0", Platform: "darwin-arm64", Channel: "stable",
		Free:  func(string) (int64, error) { return r.free, nil },
		Tasks: r.tasks, Quitter: quitter{order: &r.order}, Installer: r.inst})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) publish(version string, edit func(map[string]any)) (manifest, sig []byte) {
	manifest = releaseJSON(version, edit)
	sig = Sign(r.key, "k1", PurposeRelease, manifest)
	r.mu.Lock()
	r.served["/release.json"], r.served["/release.sig"], r.served["/tacet-"+version+".pkg"] = manifest, sig, pkg
	r.mu.Unlock()
	return
}

func TestSignaturesVerifyOnlyForTheirPurposeKeyAndPayload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	v, _ := NewVerifier(map[string]ed25519.PublicKey{"k1": pub})
	payload := []byte("payload")
	good := Sign(priv, "k1", PurposeRelease, payload)
	if err := v.Verify(PurposeRelease, payload, good); err != nil {
		t.Fatal(err)
	}
	wrongKey, _ := NewVerifier(map[string]ed25519.PublicKey{"k1": pub2})
	flip := []byte(string(good))
	flip[len(flip)-8] ^= 1
	for name, c := range map[string]struct {
		v       *Verifier
		p       Purpose
		payload []byte
		sig     []byte
	}{
		"another purpose": {v, PurposeModels, payload, good}, "another payload": {v, PurposeRelease, []byte("payload!"), good}, "another key": {wrongKey, PurposeRelease, payload, good},
		"unknown key id": {v, PurposeRelease, payload, Sign(priv, "k9", PurposeRelease, payload)}, "damaged signature": {v, PurposeRelease, payload, flip},
		"empty payload": {v, PurposeRelease, nil, good}, "not json": {v, PurposeRelease, payload, []byte("x")}, "empty signature": {v, PurposeRelease, payload, nil},
		"extra field": {v, PurposeRelease, payload, []byte(strings.Replace(string(good), "}", `,"x":1}`, 1))}, "trailing data": {v, PurposeRelease, payload, append(append([]byte(nil), good...), []byte(`{}`)...)},
		"oversized": {v, PurposeRelease, payload, bytes.Repeat([]byte("a"), 5000)}, "nil verifier": {nil, PurposeRelease, payload, good},
		"short signature": {v, PurposeRelease, payload, []byte(`{"key_id":"k1","sig":"AAAA"}`)},
	} {
		if err := c.v.Verify(c.p, c.payload, c.sig); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := NewVerifier(nil); err == nil {
		t.Error("a verifier without keys")
	}
	if _, err := NewVerifier(map[string]ed25519.PublicKey{"k": pub[:5]}); err == nil {
		t.Error("a short key")
	}
	if _, err := NewVerifier(map[string]ed25519.PublicKey{"": pub}); err == nil {
		t.Error("an empty key id")
	}
	both, _ := NewVerifier(map[string]ed25519.PublicKey{"k1": pub, "k2": pub2})
	if err := both.Verify(PurposeRelease, payload, good); err != nil {
		t.Errorf("a rotated-in second key must not disturb the first: %v", err)
	}
}

func TestReleaseDescriptionsAreParsedStrictlyAndVersionsCompareNumerically(t *testing.T) {
	r, err := ParseRelease(releaseJSON("1.2.3", nil))
	if err != nil || r.Version != "1.2.3" || r.PythonABI != "cp313" || r.Size != int64(len(pkg)) {
		t.Fatalf("%+v %v", r, err)
	}
	for name, edit := range map[string]func(map[string]any){
		"schema": func(m map[string]any) { m["schema_version"] = 2 }, "version": func(m map[string]any) { m["version"] = "1.2" }, "leading zero": func(m map[string]any) { m["version"] = "1.02.3" },
		"platform": func(m map[string]any) { m["platform"] = "Mac OS" }, "channel": func(m map[string]any) { m["channel"] = "../x" },
		"name": func(m map[string]any) { m["package"].(map[string]any)["name"] = "../evil" }, "sha": func(m map[string]any) { m["package"].(map[string]any)["sha256"] = "abc" },
		"size": func(m map[string]any) { m["package"].(map[string]any)["size"] = 0 }, "huge": func(m map[string]any) { m["package"].(map[string]any)["size"] = MaxPackage + 1 },
		"min_from": func(m map[string]any) { m["min_from"] = "x" }, "unknown field": func(m map[string]any) { m["run"] = "rm -rf /" },
		"long abi": func(m map[string]any) { m["python_abi"] = strings.Repeat("a", 33) },
	} {
		if _, err := ParseRelease(releaseJSON("1.2.3", edit)); !errors.Is(err, ErrManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ParseRelease([]byte(`{}{}`)); err == nil {
		t.Error("trailing document")
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.10.0", "1.9.9", true}, {"1.0.0", "1.0.0", false}, {"2.0.0", "10.0.0", false}, {"1.0.1", "1.0.0", true}, {"0.9.9", "1.0.0", false}, {"1.1.0", "1.0.9", true}} {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("%s > %s", c.a, c.b)
		}
	}
}

func TestACheckRequestCarriesOnlyVersionPlatformAndChannel(t *testing.T) {
	r := newRig(t)
	r.publish("1.1.0", nil)
	rel, err := r.svc.Check(context.Background())
	if err != nil || rel.Version != "1.1.0" {
		t.Fatalf("%+v %v", rel, err)
	}
	for _, q := range r.seen {
		if !strings.HasSuffix(q, "?v=1.0.0&p=darwin-arm64&c=stable") {
			t.Fatalf("a request carried more than version, platform and channel: %s", q)
		}
	}
	if len(r.gate.State()) != 0 {
		t.Fatalf("the lease was not released: %v", r.gate.State())
	}
}

func TestCheckRefusesWhatIsUnsignedWrongOrNotNewer(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	m, _ := r.publish("1.1.0", nil)
	r.served["/release.sig"] = Sign(r.key, "k1", PurposeModels, m) // right key, wrong purpose
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrSignature) {
		t.Fatalf("%v", err)
	}
	r.served["/release.json"] = releaseJSON("9.9.9", nil) // payload replaced after signing
	r.served["/release.sig"] = Sign(r.key, "k1", PurposeRelease, m)
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrSignature) {
		t.Fatalf("%v", err)
	}
	r.publish("1.0.0", nil)
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("the running version is current: %v", err)
	}
	r.publish("0.9.0", nil)
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("a downgrade: %v", err)
	}
	r.publish("1.2.0", func(m map[string]any) { m["platform"] = "windows-amd64" })
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("%v", err)
	}
	r.publish("1.2.0", func(m map[string]any) { m["channel"] = "beta" })
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("%v", err)
	}
	r.publish("3.0.0", func(m map[string]any) { m["min_from"] = "2.0.0" })
	if _, err := r.svc.Check(ctx); !errors.Is(err, ErrTooOld) {
		t.Fatalf("%v", err)
	}
	r.publish("1.3.0", func(m map[string]any) { m["min_from"] = "1.0.0" })
	if _, err := r.svc.Check(ctx); err != nil {
		t.Fatalf("a release that allows this version: %v", err)
	}
	delete(r.served, "/release.sig")
	if _, err := r.svc.Check(ctx); err == nil {
		t.Fatal("a missing signature was accepted")
	}
}

func TestCheckDoesNotRunWhileDocumentsAreProcessed(t *testing.T) {
	r := newRig(t)
	r.publish("1.1.0", nil)
	l, _ := r.gate.Acquire(netgate.Processing)
	defer l.Release()
	var busy *netgate.BusyError
	if _, err := r.svc.Check(context.Background()); !errors.As(err, &busy) {
		t.Fatalf("%v", err)
	}
	if len(r.seen) != 0 {
		t.Fatal("a request was made")
	}
	if err := r.svc.Download(context.Background(), Release{Version: "1.1.0", Platform: "darwin-arm64", Channel: "stable", Name: "tacet-1.1.0.pkg", Size: int64(len(pkg)), SHA256: sha(pkg)}); !errors.As(err, &busy) {
		t.Fatalf("%v", err)
	}
}

func (r *rig) download(version string) Release {
	r.t.Helper()
	r.publish(version, nil)
	rel, err := r.svc.Check(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.svc.Download(context.Background(), rel); err != nil {
		r.t.Fatal(err)
	}
	return rel
}

func TestADownloadedPackageIsRegisteredOnlyIfItIsTheDescribedOne(t *testing.T) {
	r := newRig(t)
	rel := r.download("1.1.0")
	got, err := os.ReadFile(filepath.Join(r.dir, "ready", rel.Version+"-"+rel.SHA256[:16], rel.Name))
	if err != nil || !bytes.Equal(got, pkg) {
		t.Fatalf("%v", err)
	}
	if es, _ := os.ReadDir(filepath.Join(r.dir, "staging", rel.Version+"-"+rel.SHA256[:16])); len(es) != 0 {
		t.Fatalf("staging holds %v", es)
	}
	// the server now sends other bytes of the right length
	r2 := newRig(t)
	r2.publish("1.1.0", nil)
	rel, _ = r2.svc.Check(context.Background())
	bad := append([]byte(nil), pkg...)
	bad[0] ^= 1
	r2.served["/tacet-1.1.0.pkg"] = bad
	if err := r2.svc.Download(context.Background(), rel); !errors.Is(err, ErrDigest) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(r2.dir, "ready", rel.Version+"-"+rel.SHA256[:16])); err == nil {
		t.Fatal("a tampered package was registered")
	}
	for name, body := range map[string][]byte{"longer": append(append([]byte(nil), pkg...), 'x'), "shorter": pkg[:len(pkg)-1]} {
		r2.served["/tacet-1.1.0.pkg"] = body
		if err := r2.svc.Download(context.Background(), rel); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAnInterruptedDownloadLeavesNoPackageAndCanBeRepeated(t *testing.T) {
	r := newRig(t)
	r.publish("1.1.0", nil)
	rel, _ := r.svc.Check(context.Background())
	r.cut = true
	if err := r.svc.Download(context.Background(), rel); err == nil {
		t.Fatal("a cut transfer succeeded")
	}
	if _, err := os.Stat(filepath.Join(r.dir, "ready", rel.Version+"-"+rel.SHA256[:16])); err == nil {
		t.Fatal("an incomplete package is ready")
	}
	if len(r.gate.State()) != 0 {
		t.Fatalf("%v", r.gate.State())
	}
	r.cut = false
	if err := r.svc.Download(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	n := len(r.seen)
	if err := r.svc.Download(context.Background(), rel); err != nil || len(r.seen) != n {
		t.Fatalf("a package that is already ready is not fetched again: %v %d %d", err, n, len(r.seen))
	}
}

func TestDownloadChecksSpaceAndTheReleaseBeforeAnyRequest(t *testing.T) {
	r := newRig(t)
	r.publish("1.1.0", nil)
	rel, _ := r.svc.Check(context.Background())
	n := len(r.seen)
	r.free = int64(len(pkg))
	if err := r.svc.Download(context.Background(), rel); err == nil || len(r.seen) != n {
		t.Fatalf("%v", err)
	}
	r.free = 1 << 30
	old := rel
	old.Version = "0.5.0"
	if err := r.svc.Download(context.Background(), old); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("%v", err)
	}
	other := rel
	other.Platform = "windows-amd64"
	if err := r.svc.Download(context.Background(), other); !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("%v", err)
	}
}

func TestOfflinePackagesGoThroughTheSameChecks(t *testing.T) {
	r := newRig(t)
	m, sig := r.publish("1.1.0", nil)
	file := filepath.Join(t.TempDir(), "update.pkg")
	os.WriteFile(file, pkg, 0o600)
	rel, err := r.svc.ImportOffline(m, sig, file)
	if err != nil || rel.Version != "1.1.0" {
		t.Fatalf("%+v %v", rel, err)
	}
	if len(r.seen) != 0 {
		t.Fatal("an offline import used the network")
	}
	if err := r.svc.Apply(context.Background(), rel, ChoiceNone); err != nil {
		t.Fatal(err)
	}
	// bad inputs
	bad := filepath.Join(t.TempDir(), "bad.pkg")
	os.WriteFile(bad, append([]byte("x"), pkg[1:]...), 0o600)
	r2 := newRig(t)
	m, sig = r2.publish("1.1.0", nil)
	for name, call := range map[string]func() error{
		"tampered package": func() error { _, err := r2.svc.ImportOffline(m, sig, bad); return err },
		"bad signature": func() error {
			_, err := r2.svc.ImportOffline(m, Sign(r2.key, "k1", PurposeModels, m), file)
			return err
		},
		"missing package": func() error { _, err := r2.svc.ImportOffline(m, sig, filepath.Join(t.TempDir(), "none")); return err },
		"manifest edited": func() error { _, err := r2.svc.ImportOffline(releaseJSON("9.0.0", nil), sig, file); return err },
		"downgrade": func() error {
			m0 := releaseJSON("0.1.0", nil)
			_, err := r2.svc.ImportOffline(m0, Sign(r2.key, "k1", PurposeRelease, m0), file)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if es, _ := os.ReadDir(filepath.Join(r2.dir, "ready")); len(es) != 0 {
		t.Fatalf("something is ready: %v", es)
	}
}

func TestApplyStopsForPendingTasksUntilTheUserChoosesAndHonoursTheChoice(t *testing.T) {
	r := newRig(t)
	rel := r.download("1.1.0")
	r.tasks.review, r.tasks.recoverable = 2, 3
	ctx := context.Background()
	err := r.svc.Apply(ctx, rel, ChoiceNone)
	var pe *PendingError
	if !errors.As(err, &pe) || !errors.Is(err, ErrTasksPending) || pe.Review != 2 || pe.Recoverable != 3 {
		t.Fatalf("%v", err)
	}
	if err := r.svc.Apply(ctx, rel, ChoiceFinish); !errors.Is(err, ErrTasksPending) {
		t.Fatalf("finishing first postpones the update: %v", err)
	}
	if len(r.order) != 0 || r.tasks.voided+r.tasks.reexec != 0 {
		t.Fatalf("something ran: %v", r.order)
	}
	if err := r.svc.Apply(ctx, rel, ChoiceReexecute); err != nil {
		t.Fatal(err)
	}
	if r.tasks.reexec != 1 || r.tasks.voided != 0 || strings.Join(r.order, ",") != "quit,install" {
		t.Fatalf("%+v %v", r.tasks, r.order)
	}
	r.order, r.tasks.review, r.tasks.recoverable = nil, 1, 0
	if err := r.svc.Apply(ctx, rel, ChoiceVoid); err != nil {
		t.Fatal(err)
	}
	if r.tasks.voided != 1 || strings.Join(r.order, ",") != "quit,install" {
		t.Fatalf("%+v %v", r.tasks, r.order)
	}
	if r.inst.path != filepath.Join(r.dir, "ready", rel.Version+"-"+rel.SHA256[:16], rel.Name) {
		t.Fatalf("%s", r.inst.path)
	}
}

func TestTheInstallerRunsUnderTheMaintenanceLeaseAndNotOnTheNetwork(t *testing.T) {
	r := newRig(t)
	rel := r.download("1.1.0")
	if err := r.svc.Apply(context.Background(), rel, ChoiceNone); err != nil {
		t.Fatal(err)
	}
	if r.inst.held[netgate.Maintenance] != 1 || r.inst.held[netgate.Network] != 0 {
		t.Fatalf("%v", r.inst.held)
	}
}

func TestApplyWithNothingPendingNeedsNoChoiceAndLeavesTasksAlone(t *testing.T) {
	r := newRig(t)
	rel := r.download("1.1.0")
	if err := r.svc.Apply(context.Background(), rel, ChoiceNone); err != nil {
		t.Fatal(err)
	}
	if r.tasks.voided+r.tasks.reexec != 0 {
		t.Fatalf("%+v", r.tasks)
	}
}

func TestApplyRefusesAMissingOrChangedPackageAndSpendsNothingOnIt(t *testing.T) {
	r := newRig(t)
	r.publish("1.1.0", nil)
	rel, _ := r.svc.Check(context.Background())
	if err := r.svc.Apply(context.Background(), rel, ChoiceNone); !errors.Is(err, ErrNoPackage) {
		t.Fatalf("not downloaded: %v", err)
	}
	r.svc.Download(context.Background(), rel)
	path := filepath.Join(r.dir, "ready", rel.Version+"-"+rel.SHA256[:16], rel.Name)
	b := append([]byte(nil), pkg...)
	b[7] ^= 1
	os.Chmod(path, 0o600)
	os.WriteFile(path, b, 0o600)
	r.tasks.review = 1
	if err := r.svc.Apply(context.Background(), rel, ChoiceVoid); !errors.Is(err, ErrNoPackage) {
		t.Fatalf("a package changed after verification: %v", err)
	}
	if r.tasks.voided != 0 || len(r.order) != 0 {
		t.Fatal("tasks were voided for an update that could not happen")
	}
}

func TestApplyDoesNotRunWhileProcessingAndFailuresStopTheChain(t *testing.T) {
	r := newRig(t)
	rel := r.download("1.1.0")
	r.tasks.review = 1
	l, _ := r.gate.Acquire(netgate.Processing)
	var busy *netgate.BusyError
	if err := r.svc.Apply(context.Background(), rel, ChoiceVoid); !errors.As(err, &busy) {
		t.Fatalf("%v", err)
	}
	if r.tasks.voided != 0 {
		t.Fatal("tasks were voided although the update could not start")
	}
	l.Release()
	r.tasks.fail = true
	if err := r.svc.Apply(context.Background(), rel, ChoiceReexecute); !errors.Is(err, ErrApply) || len(r.order) != 0 {
		t.Fatalf("%v %v", err, r.order)
	}
	r.tasks.fail = false
	r.svc.cfg.Quitter = quitter{order: &r.order, err: errors.New("busy")}
	if err := r.svc.Apply(context.Background(), rel, ChoiceReexecute); !errors.Is(err, ErrApply) || strings.Contains(strings.Join(r.order, ","), "install") {
		t.Fatalf("an install must not follow a failed exit: %v %v", err, r.order)
	}
	r.svc.cfg.Quitter = quitter{order: &r.order}
	r.inst.err = errors.New("replace failed")
	if err := r.svc.Apply(context.Background(), rel, ChoiceReexecute); !errors.Is(err, ErrApply) {
		t.Fatalf("%v", err)
	}
	if len(r.gate.State()) != 0 {
		t.Fatalf("the maintenance lease leaked: %v", r.gate.State())
	}
}

func TestFirstRunMarksEnvironmentsThroughTheInjectedFunction(t *testing.T) {
	var gotArch, gotABI string
	out, err := FirstRun(func(a, b string) ([]string, error) { gotArch, gotABI = a, b; return []string{"d1"}, nil }, "darwin-arm64", "cp314")
	if err != nil || len(out) != 1 || gotArch != "darwin-arm64" || gotABI != "cp314" {
		t.Fatalf("%v %v", out, err)
	}
	for _, c := range [][3]any{{nil, "a", "b"}, {func(string, string) ([]string, error) { return nil, nil }, "", "b"}, {func(string, string) ([]string, error) { return nil, nil }, "a", ""}} {
		f, _ := c[0].(func(string, string) ([]string, error))
		if _, err := FirstRun(f, c[1].(string), c[2].(string)); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestConfigurationMustBeCompleteAndAbsolute(t *testing.T) {
	r := newRig(t)
	ok := r.svc.cfg
	edit := func(f func(*Config)) Config { c := ok; f(&c); return c }
	for name, c := range map[string]Config{"relative dir": edit(func(c *Config) { c.Dir = "x" }), "no gate": edit(func(c *Config) { c.Gate = nil }), "no verifier": edit(func(c *Config) { c.Verifier = nil }),
		"no manifest url": edit(func(c *Config) { c.ManifestURL = nil }), "no package url": edit(func(c *Config) { c.PackageURL = nil }), "bad version": edit(func(c *Config) { c.Version = "1" }),
		"bad platform": edit(func(c *Config) { c.Platform = "x" }), "bad channel": edit(func(c *Config) { c.Channel = "A b" }), "no free": edit(func(c *Config) { c.Free = nil }),
		"no tasks": edit(func(c *Config) { c.Tasks = nil }), "no quitter": edit(func(c *Config) { c.Quitter = nil }), "no installer": edit(func(c *Config) { c.Installer = nil })} {
		if _, err := New(c); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
