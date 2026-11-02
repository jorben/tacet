package refs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/store"
)

type fakeAdmit struct {
	mu      sync.Mutex
	gen     uint64
	retired map[string]bool
}

func (f *fakeAdmit) admit(a kernel.AttemptRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.OwnerGeneration != f.gen || f.retired[a.AttemptID] {
		return errors.New("WORKSPACE_STALE_RESULT")
	}
	return nil
}

func taskKey(job string) ([]byte, error) {
	k := bytes.Repeat([]byte{7}, 32)
	copy(k, job)
	return k, nil
}

func setup(t *testing.T) (*Manager, *fakeAdmit, string) {
	t.Helper()
	dir := t.TempDir()
	fa := &fakeAdmit{gen: 4, retired: map[string]bool{}}
	m, err := New(dir, fa.admit, taskKey)
	if err != nil {
		t.Fatal(err)
	}
	return m, fa, dir
}

func att(job, attempt string) kernel.AttemptRef {
	return kernel.AttemptRef{JobID: job, AttemptID: attempt, OwnerGeneration: 4, JobVersion: 1}
}

func writeCiphertext(t *testing.T, g *Grant, files []*os.File, w Wire, payload string) {
	t.Helper()
	key, _ := hex.DecodeString(w.Encryption.KeyHex)
	wr, err := store.NewResourceWriter(files[0], key, w.Encryption.ResourceID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	wr.Write([]byte(payload))
	if err = wr.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEndWriteFreezeThenReadWithTheIssuedKey(t *testing.T) {
	m, _, dir := setup(t)
	g, err := m.IssueWrite(att("j1", "a1"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	files, wires, err := Handoff(g)
	if err != nil || wires[0].Handle != 3 || wires[0].Mode != Write || wires[0].Digest != "" {
		t.Fatalf("handoff: %+v %v", wires, err)
	}
	// Windows has no mode bits; the workspace directory's ACL keeps the file private
	if info, _ := os.Stat(filepath.Join(dir, g.ResourceID())); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	writeCiphertext(t, g, files, wires[0], "synthetic private content")
	if _, err = m.Digest(g.ResourceID()); !errors.Is(err, ErrNotReadable) {
		t.Fatal("an unfinished candidate must not be visible")
	}
	if _, err = m.IssueRead(att("j1", "a2"), g.ResourceID()); !errors.Is(err, ErrNotReadable) {
		t.Fatalf("read of an unfinished resource: %v", err)
	}
	digest, err := g.Freeze()
	if err != nil || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		t.Fatalf("freeze: %q %v", digest, err)
	}
	// Windows can only mark a file read-only, which shows as 0444
	if perm := statPerm(t, g.rec.path); perm&0o222 != 0 || (runtime.GOOS != "windows" && perm != 0o400) {
		t.Fatalf("frozen file is writable: %v", perm)
	}
	// A later attempt reads the very bytes with the same derived key.
	rg, err := m.IssueRead(att("j1", "a2"), g.ResourceID())
	if err != nil {
		t.Fatal(err)
	}
	rfiles, rwires, _ := Handoff(rg)
	if rwires[0].Digest != digest || rwires[0].Version != 1 || rwires[0].Mode != Read {
		t.Fatalf("%+v", rwires[0])
	}
	if _, err = rfiles[0].Write([]byte("x")); err == nil {
		t.Fatal("read handle is writable")
	}
	key, _ := hex.DecodeString(rwires[0].Encryption.KeyHex)
	info, _ := rfiles[0].Stat()
	r, err := store.OpenResource(rfiles[0], info.Size(), key, rwires[0].Encryption.ResourceID)
	if err != nil || r.Verify() != nil {
		t.Fatalf("open: %v", err)
	}
	if err = m.Verify(g.ResourceID()); err != nil {
		t.Fatal(err)
	}
}

func TestWireMatchesTheProtocolSchemaAndLeaksNoPath(t *testing.T) {
	m, _, dir := setup(t)
	g, _ := m.IssueWrite(att("j1", "a1"), 100)
	_, w1, _ := Handoff(g)
	path := filepath.Join(dir, "in")
	os.WriteFile(path, []byte("existing"), 0o600)
	if err := m.Adopt("j1", "input-1", path); err != nil {
		t.Fatal(err)
	}
	r, _ := m.IssueRead(att("j1", "a1"), "input-1")
	_, w2, _ := Handoff(g, r)
	s, err := host.NewSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Compile("common.schema.json", "ResourceRef"); err != nil {
		t.Fatal(err)
	}
	for _, w := range append(w1, w2...) {
		body, _ := json.Marshal(w)
		if err = s.Validate("common.schema.json", "ResourceRef", body); err != nil {
			t.Errorf("schema rejects %s: %v", body, err)
		}
		if strings.Contains(string(body), dir) || strings.Contains(string(body), "/") {
			t.Errorf("wire reference leaks a path: %s", body)
		}
	}
	if w2[0].Handle != 3 || w2[1].Handle != 4 {
		t.Fatalf("handles: %d %d", w2[0].Handle, w2[1].Handle)
	}
}

func TestReadIsRestrictedToFinishedResourcesOfTheSameJobAndLiveAttempts(t *testing.T) {
	m, fa, dir := setup(t)
	path := filepath.Join(dir, "in")
	os.WriteFile(path, []byte("data"), 0o600)
	m.Adopt("j1", "in1", path)
	if _, err := m.IssueRead(att("j2", "a"), "in1"); !errors.Is(err, ErrForeign) {
		t.Fatalf("other job's resource: %v", err)
	}
	if _, err := m.IssueRead(att("j1", "a"), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown id")
	}
	old := att("j1", "a")
	old.OwnerGeneration = 3
	if _, err := m.IssueRead(old, "in1"); err == nil {
		t.Fatal("old generation got access")
	}
	fa.retired["a"] = true
	if _, err := m.IssueRead(att("j1", "a"), "in1"); err == nil {
		t.Fatal("replaced attempt got access")
	}
	if _, err := m.IssueWrite(att("j1", "a"), 10); err == nil {
		t.Fatal("replaced attempt can create a write")
	}
	if _, err := m.IssueWrite(att("j1", "b"), 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("write without quota")
	}
	if err := m.Adopt("j1", "in1", path); !errors.Is(err, ErrInvalid) {
		t.Fatal("resource id re-registered")
	}
	if err := m.Adopt("j1", "x", filepath.Join(dir, "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing file adopted")
	}
}

func TestEachWriteGetsAFreshResourceAndKey(t *testing.T) {
	m, _, _ := setup(t)
	a, _ := m.IssueWrite(att("j1", "a"), 10)
	b, _ := m.IssueWrite(att("j1", "b"), 10)
	_, wa, _ := Handoff(a)
	_, wb, _ := Handoff(b)
	if a.ResourceID() == b.ResourceID() || wa[0].Encryption.KeyHex == wb[0].Encryption.KeyHex || wa[0].Ref == wb[0].Ref {
		t.Fatal("a rewrite reused identity or key")
	}
}

func TestRevokeDiscardsUnfinishedWritesKeepsFrozenAndStopsFreezing(t *testing.T) {
	m, _, dir := setup(t)
	done, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	files, wires, _ := Handoff(done)
	writeCiphertext(t, done, files, wires[0], "kept")
	done.Freeze()
	half, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	hf, hw, _ := Handoff(half)
	writeCiphertext(t, half, hf, hw[0], "partial")
	if n := m.Revoke(att("j1", "a1")); n != 2 {
		t.Fatalf("revoked %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, half.ResourceID())); !os.IsNotExist(err) {
		t.Fatal("partial output of a revoked attempt stayed on disk")
	}
	if _, err := os.Stat(done.rec.path); err != nil {
		t.Fatal("a finished resource was deleted")
	}
	if _, err := half.Freeze(); !errors.Is(err, ErrRevoked) {
		t.Fatalf("freeze after revoke: %v", err)
	}
	if _, err := half.Size(); !errors.Is(err, ErrRevoked) {
		t.Fatal("size after revoke")
	}
	if _, _, err := Handoff(half); !errors.Is(err, ErrRevoked) {
		t.Fatal("handoff of a revoked grant")
	}
	if _, err := m.IssueRead(att("j1", "a2"), half.ResourceID()); !errors.Is(err, ErrNotReadable) {
		t.Fatalf("abandoned resource readable: %v", err)
	}
	if _, err := m.IssueRead(att("j1", "a2"), done.ResourceID()); err != nil {
		t.Fatalf("frozen resource lost: %v", err)
	}
	if m.Revoke(att("j1", "a1")) != 0 {
		t.Fatal("revoke is not idempotent")
	}
	if !allZero(done.key) || !allZero(half.key) {
		t.Fatal("keys not zeroed")
	}
}

func TestRevokeOnlyTouchesTheNamedAttempt(t *testing.T) {
	m, _, _ := setup(t)
	a, _ := m.IssueWrite(att("j1", "a1"), 10)
	b, _ := m.IssueWrite(att("j1", "a2"), 10)
	m.Revoke(att("j1", "a1"))
	if _, err := b.Size(); err != nil {
		t.Fatalf("other attempt affected: %v", err)
	}
	if _, err := a.Size(); !errors.Is(err, ErrRevoked) {
		t.Fatal("named attempt still live")
	}
}

func TestFreezeRefusesLateResultsQuotaAndEmptyOutput(t *testing.T) {
	m, fa, dir := setup(t)
	// Attempt replaced while the plugin was still running: its result is dropped.
	late, _ := m.IssueWrite(att("j1", "late"), 1<<20)
	lf, lw, _ := Handoff(late)
	writeCiphertext(t, late, lf, lw[0], "x")
	fa.retired["late"] = true
	if _, err := late.Freeze(); err == nil {
		t.Fatal("late result frozen")
	}
	if _, err := os.Stat(filepath.Join(dir, late.ResourceID())); !os.IsNotExist(err) {
		t.Fatal("late result kept")
	}
	// Over quota: discarded, never published as a smaller thing.
	big, _ := m.IssueWrite(att("j1", "big"), 50)
	bf, bw, _ := Handoff(big)
	writeCiphertext(t, big, bf, bw[0], "0123456789")
	if size, _ := big.Size(); size <= 50 {
		t.Fatalf("setup: size %d", size)
	}
	if _, err := big.Freeze(); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
	empty, _ := m.IssueWrite(att("j1", "empty"), 50)
	if _, err := empty.Freeze(); err == nil {
		t.Fatal("empty output frozen")
	}
	reader, _ := m.IssueWrite(att("j1", "r"), 50)
	_ = reader
	rd := &Grant{m: m, mode: Read, rec: &record{}}
	if _, err := rd.Freeze(); !errors.Is(err, ErrNotWritable) {
		t.Fatal("a read grant was frozen")
	}
}

func TestHandleKeptAcrossFreezeCannotMutateFrozenResource(t *testing.T) {
	m, _, dir := setup(t)
	g, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	files, wires, _ := Handoff(g)
	writeCiphertext(t, g, files, wires[0], "honest")
	// The plugin kept its own descriptor, which the host cannot close for it. It
	// shares the host handle's open mode, which lets the host delete the file.
	sneaky, err := platform.OpenShared(filepath.Join(dir, g.ResourceID()), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sneaky.Close()
	if _, err = g.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err = m.Verify(g.ResourceID()); err != nil {
		t.Fatalf("before the late write: %v", err)
	}
	if _, err = sneaky.Write([]byte("late")); err != nil {
		t.Fatal(err)
	}
	if err = m.Verify(g.ResourceID()); err != nil {
		t.Fatalf("old handle changed sealed resource: %v", err)
	}
	if _, err = m.IssueRead(att("j1", "a2"), g.ResourceID()); err != nil {
		t.Fatalf("sealed resource lost after late write: %v", err)
	}
	// Storage modification to the actual sealed file must still be detected.
	if err = os.Chmod(g.rec.path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(g.rec.path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = m.Verify(g.ResourceID()); !errors.Is(err, ErrTampered) {
		t.Fatalf("storage modification undetected: %v", err)
	}
	if _, err = m.Digest(g.ResourceID()); !errors.Is(err, ErrNotReadable) {
		t.Fatal("digest of a tampered resource")
	}
}

func TestSizeChangeIsCaughtAtIssueTimeToo(t *testing.T) {
	m, _, dir := setup(t)
	path := filepath.Join(dir, "in")
	os.WriteFile(path, []byte("data"), 0o600)
	m.Adopt("j1", "in1", path)
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	f.Write([]byte("more"))
	f.Close()
	if _, err := m.IssueRead(att("j1", "a"), "in1"); !errors.Is(err, ErrTampered) {
		t.Fatalf("%v", err)
	}
}

func TestNewRejectsInvalidSetupAndKeyFailureStopsIssue(t *testing.T) {
	dir := t.TempDir()
	ok := func(kernel.AttemptRef) error { return nil }
	if _, err := New(filepath.Join(dir, "missing"), ok, taskKey); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing dir")
	}
	if _, err := New(dir, nil, taskKey); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil admit")
	}
	failing, _ := New(dir, ok, func(string) ([]byte, error) { return nil, errors.New("KEY_NOT_FOUND") })
	if _, err := failing.IssueWrite(att("j", "a"), 10); err == nil {
		t.Fatal("write issued without a key")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("a file was left behind: %d", len(entries))
	}
}

func TestRevocationBindsCompleteAttemptIdentity(t *testing.T) {
	m, _, _ := setup(t)
	first := att("j1", "same")
	otherJob := att("j2", "same")
	otherVersion := first
	otherVersion.JobVersion++
	firstGrant, _ := m.IssueWrite(first, 1024)
	jobGrant, _ := m.IssueWrite(otherJob, 1024)
	versionGrant, _ := m.IssueWrite(otherVersion, 1024)
	if count := m.Revoke(first); count != 1 {
		t.Fatalf("revoked %d grants, want 1", count)
	}
	if _, _, err := Handoff(firstGrant); !errors.Is(err, ErrRevoked) {
		t.Fatalf("original attempt still usable: %v", err)
	}
	for _, grant := range []*Grant{jobGrant, versionGrant} {
		if _, _, err := Handoff(grant); err != nil {
			t.Fatalf("foreign attempt revoked: %v", err)
		}
	}
	if m.Revoke(otherJob) != 1 || m.Revoke(otherVersion) != 1 {
		t.Fatal("revoking one attempt lost another's index")
	}
	if _, err := m.IssueWrite(first, 1024); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked attempt issued a new grant: %v", err)
	}
}

func TestFreezeAuthenticatesCiphertextBeforeRegistration(t *testing.T) {
	for _, corruption := range []string{"plaintext", "truncated", "segment_tampered"} {
		t.Run(corruption, func(t *testing.T) {
			m, _, dir := setup(t)
			grant, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
			files, wires, _ := Handoff(grant)
			if corruption == "plaintext" {
				files[0].Write([]byte("not an authenticated resource"))
			} else {
				writeCiphertext(t, grant, files, wires[0], strings.Repeat("marker", 12000))
				path := filepath.Join(dir, grant.ResourceID())
				if corruption == "truncated" {
					info, _ := os.Stat(path)
					os.Truncate(path, info.Size()-1)
				} else {
					file, err := os.OpenFile(path, os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					file.WriteAt([]byte{encoded[100] ^ 1}, 100)
					file.Close()
				}
			}
			if _, err := grant.Freeze(); !errors.Is(err, ErrTampered) {
				t.Fatalf("corrupt resource frozen: %v", err)
			}
			if _, err := m.Digest(grant.ResourceID()); !errors.Is(err, ErrNotReadable) {
				t.Fatalf("corrupt candidate registered: %v", err)
			}
			remaining, _ := os.ReadDir(dir)
			if len(remaining) != 0 {
				t.Fatalf("failed freeze leaked %d files", len(remaining))
			}
		})
	}
}

func TestHandoffAndRevokeSynchronizeKeyAccess(t *testing.T) {
	m, _, _ := setup(t)
	attempt := att("j1", "a1")
	grant, _ := m.IssueWrite(attempt, 1024)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 50; j++ {
				_, _, err := Handoff(grant)
				if err != nil && !errors.Is(err, ErrRevoked) {
					t.Error(err)
				}
			}
		}()
	}
	m.Revoke(attempt)
	group.Wait()
}

func TestWriteHandleIsAppendOnly(t *testing.T) {
	m, _, dir := setup(t)
	g, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	files, _, _ := Handoff(g)
	files[0].Write([]byte("AAAA"))
	// Seeking back and writing must not overwrite what is already there: an
	// encrypted segment, once written, is never rewritten (4.4).
	files[0].Seek(0, 0)
	files[0].Write([]byte("ZZ"))
	data, _ := os.ReadFile(filepath.Join(dir, g.ResourceID()))
	if string(data) != "AAAAZZ" {
		t.Fatalf("overwrite possible: %q", data)
	}
	if _, err := files[0].Read(make([]byte, 1)); err == nil {
		t.Fatal("write handle is readable")
	}
}

func TestHostWriterProducesAResourceThatLaterReadsDecryptWithTheIssuedKey(t *testing.T) {
	m, _, _ := setup(t)
	g, err := m.IssueWrite(att("j1", "a1"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	w, err := g.HostWriter(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("operation plan lines")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Freeze(); err != nil {
		t.Fatal(err)
	}
	rg, err := m.IssueRead(att("j1", "a2"), g.ResourceID())
	if err != nil {
		t.Fatal(err)
	}
	files, wires, _ := Handoff(rg)
	key, _ := hex.DecodeString(wires[0].Encryption.KeyHex)
	info, _ := files[0].Stat()
	r, err := store.OpenResource(files[0], info.Size(), key, wires[0].Encryption.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, r.Size())
	if _, err = r.ReadAt(buf, 0); err != nil && string(buf) != "operation plan lines" {
		t.Fatal(err)
	}
	if string(buf) != "operation plan lines" {
		t.Fatalf("%q", buf)
	}
}

func TestHostWriterRefusesReadGrantsRevokedGrantsAndBadLimits(t *testing.T) {
	m, _, dir := setup(t)
	src := filepath.Join(dir, "in")
	os.WriteFile(src, []byte("x"), 0o600)
	if err := m.Adopt("j1", "in1", src); err != nil {
		t.Fatal(err)
	}
	rg, _ := m.IssueRead(att("j1", "a1"), "in1")
	if _, err := rg.HostWriter(10); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("a read grant: %v", err)
	}
	g, _ := m.IssueWrite(att("j1", "a1"), 100)
	for name, limit := range map[string]uint64{"zero": 0, "over the grant's limit": 101} {
		if _, err := g.HostWriter(limit); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m.Revoke(att("j1", "a1"))
	if _, err := g.HostWriter(10); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked grant: %v", err)
	}
}

func TestPlaintextReadsBackWhatTheWriterEncryptedAndRefusesTampering(t *testing.T) {
	m, _, _ := setup(t)
	g, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	w, _ := g.HostWriter(1 << 20)
	payload := strings.Repeat("candidate bytes ", 20000) // several segments
	w.Write([]byte(payload))
	w.Close()
	if _, err := g.Freeze(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Plaintext(); !errors.Is(err, ErrNotReadable) {
		t.Fatalf("a write grant: %v", err)
	}
	rg, err := m.IssueRead(att("j1", "a2"), g.ResourceID())
	if err != nil {
		t.Fatal(err)
	}
	r, n, err := rg.Plaintext()
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("%d %v", n, err)
	}
	var sb strings.Builder
	buf := make([]byte, 7777)
	for {
		k, err := r.Read(buf)
		sb.Write(buf[:k])
		if err != nil {
			break
		}
	}
	if sb.String() != payload {
		t.Fatal("plaintext differs from what was written")
	}
	m.Revoke(att("j1", "a2"))
	if _, _, err := rg.Plaintext(); !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked grant: %v", err)
	}
}

func TestPlaintextOfAResourceWhoseFileWasAlteredFailsInsteadOfYieldingOtherBytes(t *testing.T) {
	m, _, _ := setup(t)
	g, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	w, _ := g.HostWriter(1 << 20)
	w.Write([]byte("candidate bytes"))
	w.Close()
	if _, err := g.Freeze(); err != nil {
		t.Fatal(err)
	}
	path := g.rec.path
	os.Chmod(path, 0o600)
	data, _ := os.ReadFile(path)
	data[0] ^= 0xFF // the magic of the resource header
	os.WriteFile(path, data, 0o600)
	rg, err := m.IssueRead(att("j1", "a2"), g.ResourceID())
	if err != nil {
		t.Skipf("the altered file is already refused at issue: %v", err)
	}
	if _, _, err = rg.Plaintext(); !errors.Is(err, ErrTampered) {
		t.Fatalf("%v", err)
	}
}

func TestReadBackReturnsAnOpenWritesContentOnceAndLeavesNothingAfterRevoke(t *testing.T) {
	m, _, dir := setup(t)
	g, err := m.IssueWrite(att("j1", "a1"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	files, wires, _ := Handoff(g)
	writeCiphertext(t, g, files, wires[0], "page bytes")
	got, err := g.ReadBack(1 << 20)
	if err != nil || string(got) != "page bytes" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err = g.ReadBack(4); !errors.Is(err, ErrQuota) {
		t.Fatalf("a result over the limit must be refused: %v", err)
	}
	if _, err = g.ReadBack(0); !errors.Is(err, ErrQuota) {
		t.Fatalf("no limit means no read: %v", err)
	}
	m.Revoke(att("j1", "a1"))
	if _, err = g.ReadBack(1 << 20); !errors.Is(err, ErrNotWritable) && !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the transient resource must not survive its attempt: %v", entries)
	}
	// a read grant and a frozen write are not transient
	g2, _ := m.IssueWrite(att("j1", "a2"), 1<<20)
	w, _ := g2.HostWriter(1 << 20)
	w.Write([]byte("x"))
	w.Close()
	if _, err = g2.Freeze(); err != nil {
		t.Fatal(err)
	}
	if _, err = g2.ReadBack(1 << 20); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("a frozen resource: %v", err)
	}
}

func TestReadBackRefusesAnAlteredFile(t *testing.T) {
	m, _, dir := setup(t)
	g, _ := m.IssueWrite(att("j1", "a1"), 1<<20)
	files, wires, _ := Handoff(g)
	writeCiphertext(t, g, files, wires[0], strings.Repeat("z", 1000))
	entries, _ := os.ReadDir(dir)
	p := dir + "/" + entries[0].Name()
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 0xff
	os.WriteFile(p, b, 0o600)
	if _, err := g.ReadBack(1 << 20); !errors.Is(err, ErrTampered) {
		t.Fatalf("%v", err)
	}
}

func statPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
