//go:build darwin

package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/verdict"
)

func (r *rig) publisher() *publish.Service {
	return &publish.Service{Commit: r.ws.Commit, Admit: r.ws.AdmitTx, CurrentKey: func(store.Tx, string) (string, error) { return "key1", nil }}
}

// plaintext returns the validated candidate decrypted, and its digest.
func (r *rig) plaintext(a kernel.AttemptRef, resource string) (io.Reader, string) {
	r.t.Helper()
	read := func() io.Reader {
		g, err := r.refs.IssueRead(a, resource)
		if err != nil {
			r.t.Fatal(err)
		}
		rd, _, err := g.Plaintext()
		if err != nil {
			r.t.Fatal(err)
		}
		return rd
	}
	h := sha256.New()
	if _, err := io.Copy(h, read()); err != nil {
		r.t.Fatal(err)
	}
	return read(), "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func facts(res Result) verdict.Facts {
	return verdict.Facts{Phase: verdict.PhaseReady, Validated: res.Conclusion == Passed, InputVerified: true, PublishGranted: true}
}

// The whole point of the chain: a candidate that passed validation reaches the user's folder
// without replacing anything, and one that did not, does not.
func TestAValidatedRealCandidateIsPublishedAndAFailedOneIsNot(t *testing.T) {
	r := newRig(t, docx(t, body))
	r.extractInput()
	inputBefore, err := r.refs.Digest("in1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pub := r.publisher()
	ctx := context.Background()

	// 1. a candidate that leaves one number behind fails validation and is not delivered
	partial := r.planFor([]int{0}, "placeholder")
	bad := r.exportPlan(partial, fixed("[手机号]"))
	full := r.planFor([]int{0, 1}, "placeholder")
	st, err := export.Stage(r.refs, r.att("e2"), "in1", full, fixed("[手机号]"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	badRes, err := r.validateWith(bad, full, fixed("[手机号]"), true, st.PlanResource, false)
	if err != nil || badRes.Conclusion == Passed {
		t.Fatalf("%+v %v", badRes, err)
	}
	a := r.att("p1")
	if err = pub.Grant(ctx, "j1", "key1", publish.Explicit, false); err != nil {
		t.Fatal(err)
	}
	rd, digest := r.plaintext(a, bad.Resource)
	_, err = pub.Publish(ctx, publish.Request{Attempt: a, Key: "key1", Facts: facts(badRes), Candidate: rd, Digest: digest, Dir: dir, Name: "out-001.docx"})
	if !errors.Is(err, publish.ErrNotReady) {
		t.Fatalf("a candidate that failed validation was not refused: %v", err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("a failed candidate left files: %v", es)
	}

	// 2. the full candidate passes and is delivered, decrypted, without touching the original
	good := r.exportPlan(full, fixed("[手机号]"))
	goodRes, err := r.validate(good, full, fixed("[手机号]"), true)
	if err != nil || goodRes.Conclusion != Passed {
		t.Fatalf("%+v %v", goodRes, err)
	}
	a = r.att("p2")
	rd, digest = r.plaintext(a, good.Resource)
	d, err := pub.Publish(ctx, publish.Request{Attempt: a, Key: "key1", Facts: facts(goodRes), Candidate: rd, Digest: digest, Dir: dir, Name: "out-001.docx"})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Atomic || d.Digest != digest {
		t.Fatalf("%+v", d)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "out-001.docx"))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("the delivered file is not a package: %v", err)
	}
	var xml string
	for _, f := range z.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			xml = string(b)
		}
	}
	if strings.Contains(xml, "13812345678") || strings.Contains(xml, "13900000000") || !strings.Contains(xml, "[手机号]") || !strings.Contains(xml, "无敏感内容") {
		t.Fatalf("the delivered document is not the redacted one: %s", xml)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("temporary files left behind: %v", es)
	}
	if after, _ := r.refs.Digest("in1"); after != inputBefore {
		t.Fatal("the original changed")
	}
	if err = r.refs.Verify("in1"); err != nil {
		t.Fatal(err)
	}

	// 3. publishing again under the same name never replaces the delivered file
	a = r.att("p3")
	rd, digest = r.plaintext(a, good.Resource)
	_, err = pub.Publish(ctx, publish.Request{Attempt: a, Key: "key1", Facts: facts(goodRes), Candidate: rd, Digest: digest, Dir: dir, Name: "out-001.docx"})
	if !errors.Is(err, publish.ErrNameConflict) {
		t.Fatalf("%v", err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "out-001.docx"))
	if !bytes.Equal(again, raw) {
		t.Fatal("the delivered file was replaced")
	}
}
