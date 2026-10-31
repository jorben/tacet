package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/preview"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/store"
)

// ErrPreviewUnsupported says the file's format has no page preview.
var ErrPreviewUnsupported = errors.New("PREVIEW_UNSUPPORTED")

const (
	previewCacheEntries = 24
	previewCacheBytes   = 96 << 20
)

// previewState is what the engine keeps for page previews: which jobs have a preview
// running (for admission) and the bounded cache of rendered pages. Pages live in memory
// only and are served once decrypted by the host, never written to a user-visible place.
type previewState struct {
	mu      sync.Mutex
	running map[string]string // job -> attempt id of the preview in progress
	byKey   map[string]string // cache key -> token
	images  map[string]*cached
	order   []string // tokens, oldest first
	bytes   int
	pager   *preview.Pager
	jobLock map[string]*sync.Mutex
}

type cached struct {
	key  string
	png  []byte
	page PagePreview
}

func newPreviewState() (*previewState, error) {
	p, err := preview.NewPager()
	if err != nil {
		return nil, err
	}
	return &previewState{running: map[string]string{}, byKey: map[string]string{}, images: map[string]*cached{}, pager: p, jobLock: map[string]*sync.Mutex{}}, nil
}

// versions is the workspace's view of which attempt of a job is current. A page preview is
// not a stage, so it has no record of its own: while one runs and no stage of the job is
// running, it is the current attempt; a stage that starts, or any change of the job, makes
// it stale at once and the plugin's references stop being admitted.
type versions struct{ e *Engine }

func (v versions) Current(tx store.Tx, job string) (string, uint64, bool) {
	attempt, version, ok := v.e.jobs.Current(tx, job)
	if ok || v.e.pv == nil {
		return attempt, version, ok // a stage is running (or the job is unknown): that is the attempt
	}
	v.e.pv.mu.Lock()
	pv, running := v.e.pv.running[job]
	v.e.pv.mu.Unlock()
	if !running {
		return attempt, version, ok
	}
	j, err := v.e.jobs.Get(tx, job)
	if err != nil || j.AttemptID != "" {
		return attempt, version, ok
	}
	return pv, j.Version, true
}

func (p *previewState) lock(job string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.jobLock[job]
	if !ok {
		m = &sync.Mutex{}
		p.jobLock[job] = m
	}
	return m
}

func (p *previewState) put(c *cached) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok, ok := p.byKey[c.key]; ok {
		return tok
	}
	var r [12]byte
	rand.Read(r[:])
	tok := hex.EncodeToString(r[:])
	p.images[tok], p.byKey[c.key] = c, tok
	p.order = append(p.order, tok)
	p.bytes += len(c.png)
	for len(p.order) > previewCacheEntries || (p.bytes > previewCacheBytes && len(p.order) > 1) {
		old := p.order[0]
		p.order = p.order[1:]
		if o := p.images[old]; o != nil {
			p.bytes -= len(o.png)
			delete(p.byKey, o.key)
			delete(p.images, old)
		}
	}
	return tok
}

// dropJob forgets the pages of one job (its data was cleaned).
func (p *previewState) dropJob(job string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	keep := p.order[:0]
	for _, tok := range p.order {
		if c := p.images[tok]; c != nil && c.page.Job == job {
			p.bytes -= len(c.png)
			delete(p.byKey, c.key)
			delete(p.images, tok)
			continue
		}
		keep = append(keep, tok)
	}
	p.order = keep
}

// PreviewImage returns a rendered page by the token PagePreview gave.
func (e *Engine) PreviewImage(token string) ([]byte, bool) {
	e.pv.mu.Lock()
	defer e.pv.mu.Unlock()
	c, ok := e.pv.images[token]
	if !ok {
		return nil, false
	}
	return c.png, true
}

func allowedPreviewDPI(d int) bool { return d == 72 || d == 100 || d == 150 }

// PagePreview renders one page of a file waiting for review or publication, with the place
// of every candidate on it. Only PDF has pages.
func (e *Engine) PagePreview(ctx context.Context, job string, page, dpi int) (PagePreview, error) {
	if page < 1 || page > 100000 {
		return PagePreview{}, ErrInvalid
	}
	if dpi == 0 {
		dpi = 100
	}
	if !allowedPreviewDPI(dpi) {
		return PagePreview{}, ErrInvalid
	}
	mu := e.pv.lock(job)
	mu.Lock()
	defer mu.Unlock()

	var m Member
	var j jobs.Job
	var fs []findings.Finding
	var eff map[int64]review.Applied
	var units []unitRef
	if err := e.ws.Commit(ctx, func(tx store.Tx) error {
		var err error
		if m, err = getMember(tx, job); err != nil {
			return ErrNotFound
		}
		if j, err = e.jobs.Get(tx, job); err != nil {
			return ErrNotFound
		}
		if j.Position.State != jobs.AwaitingReview && j.Position.State != jobs.ReadyToPublish {
			return fmt.Errorf("%w: the file is not waiting for review", ErrNotAllowed)
		}
		if m.Format != "pdf" {
			return ErrPreviewUnsupported
		}
		attempt, ok, err := e.units.Sealed(tx, job)
		if err != nil || !ok {
			return ErrNotReady
		}
		us, err := e.units.ByPart(tx, attempt, fmt.Sprintf("page/%d", page), 10000)
		if err != nil {
			return err
		}
		ids := make([]string, len(us))
		for i, u := range us {
			ids[i] = u.UnitID
			units = append(units, unitRef{id: u.UnitID, ordinal: u.Ordinal})
		}
		for from := int64(0); len(ids) > 0; {
			pg, err := e.fr.Page(tx, findings.Filter{Job: job, Units: ids}, from, findings.MaxPage)
			if err != nil {
				return err
			}
			if len(pg) == 0 {
				break
			}
			fs, from = append(fs, pg...), pg[len(pg)-1].ID
		}
		eff, err = review.Effective(tx, fs)
		return err
	}); err != nil {
		return PagePreview{}, err
	}
	ordinalOf := map[string]int{}
	for _, u := range units {
		ordinalOf[u.id] = u.ordinal
	}
	sort.Slice(fs, func(a, b int) bool { return fs[a].ID < fs[b].ID })
	marks := make([]preview.Mark, 0, len(fs))
	byMark := map[string]findings.Finding{}
	for _, f := range fs {
		id := fmt.Sprint(f.ID)
		marks = append(marks, preview.Mark{ID: id, Ordinal: ordinalOf[f.Unit], Start: f.Start, End: f.End})
		byMark[id] = f
	}
	if len(marks) > 4096 {
		marks = marks[:4096] // the page is still shown; the rest are not outlined
	}
	h := sha256.New()
	fmt.Fprint(h, job, "|", m.Input, "|", page, "|", dpi)
	for _, mk := range marks {
		fmt.Fprint(h, "|", mk.ID, ":", mk.Ordinal, ":", mk.Start, ":", mk.End)
	}
	key := hex.EncodeToString(h.Sum(nil))
	e.pv.mu.Lock()
	if tok, ok := e.pv.byKey[key]; ok {
		c := e.pv.images[tok]
		e.pv.mu.Unlock()
		out := c.page
		out.Image = tok
		out.Boxes = withDecisions(out.Boxes, byMark, eff)
		return out, nil
	}
	e.pv.mu.Unlock()

	attemptID := "pv_" + randomID()
	a := kernel.AttemptRef{JobID: job, AttemptID: attemptID, OwnerGeneration: e.ws.Generation(), JobVersion: j.Version}
	e.pv.mu.Lock()
	e.pv.running[job] = attemptID
	e.pv.mu.Unlock()
	defer func() {
		e.refs.Revoke(a)
		e.pv.mu.Lock()
		delete(e.pv.running, job)
		e.pv.mu.Unlock()
	}()
	in, err := e.refs.IssueRead(a, m.Input)
	if err != nil {
		return PagePreview{}, classifyPreview(err)
	}
	out, err := e.refs.IssueWrite(a, preview.MaxPNG)
	if err != nil {
		return PagePreview{}, classifyPreview(err)
	}
	files, wires, err := refs.Handoff(in, out)
	if err != nil {
		return PagePreview{}, classifyPreview(err)
	}
	plug, ok := e.plugins[m.Format]
	if !ok {
		return PagePreview{}, ErrPreviewUnsupported
	}
	rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	w, err := e.launch(rctx, plug, files)
	if err != nil {
		return PagePreview{}, errors.New("PLUGIN_START_FAILED")
	}
	defer w.Terminate(time.Second)
	var png []byte
	res, err := e.pv.pager.Render(rctx, w.Session, a, wires[0], wires[1], page, dpi, marks, func() (int64, error) {
		var rerr error
		png, rerr = out.ReadBack(preview.MaxPNG)
		return int64(len(png)), rerr
	})
	if err != nil {
		return PagePreview{}, err
	}
	pp := PagePreview{Job: job, Page: res.Page, Pages: res.Pages, Width: res.WidthPx, Height: res.HeightPx}
	for _, b := range res.Boxes {
		pp.Boxes = append(pp.Boxes, PreviewBox{Finding: mustInt(b.ID), Rect: b.Rect})
	}
	for _, u := range res.Unmapped {
		pp.Unmapped = append(pp.Unmapped, mustInt(u))
	}
	tok := e.pv.put(&cached{key: key, png: png, page: pp})
	pp.Image = tok
	pp.Boxes = withDecisions(pp.Boxes, byMark, eff)
	return pp, nil
}

type unitRef struct {
	id      string
	ordinal int
}

func withDecisions(boxes []PreviewBox, by map[string]findings.Finding, eff map[int64]review.Applied) []PreviewBox {
	out := make([]PreviewBox, len(boxes))
	for i, b := range boxes {
		b.Category = by[fmt.Sprint(b.Finding)].Category
		b.Decision = ""
		if a, ok := eff[b.Finding]; ok {
			b.Decision = "redact"
			if a.Keep {
				b.Decision = "keep"
			}
		}
		out[i] = b
	}
	return out
}

func mustInt(s string) int64 {
	var n int64
	fmt.Sscan(s, &n)
	return n
}

func randomID() string {
	var r [8]byte
	rand.Read(r[:])
	return hex.EncodeToString(r[:])
}

func classifyPreview(err error) error {
	switch {
	case errors.Is(err, refs.ErrQuota):
		return errors.New("RESOURCE_UNAVAILABLE")
	}
	return errors.New("PREVIEW_FAILED")
}
