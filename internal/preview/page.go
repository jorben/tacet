package preview

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

var (
	ErrPageProtocol = errors.New("PREVIEW_PROTOCOL_VIOLATION")
	ErrPageFailed   = errors.New("PREVIEW_FAILED")
)

// MaxPNG bounds one page image the host accepts.
const MaxPNG = 64 << 20

// Mark is a range of one unit chunk whose place on the page is wanted.
type Mark struct {
	ID      string `json:"id"`
	Ordinal int    `json:"ordinal"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

// Box is a pixel rectangle on the page image: left, top, right, bottom.
type Box struct {
	ID   string
	Rect [4]int
}

// Page is one rendered page and where the marks lie on it.
type Page struct {
	Page, Pages       int
	WidthPx, HeightPx int
	Boxes             []Box
	Unmapped          []string
}

// Pager calls format.preview. The image itself is read back by the caller from
// the write reference it handed over; only the description travels in the stream.
type Pager struct{ Schemas *host.Schemas }

func NewPager() (*Pager, error) {
	s, err := host.NewSchemas()
	if err != nil {
		return nil, err
	}
	for _, d := range []string{"PreviewItem"} {
		if err = s.Compile("format.schema.json", d); err != nil {
			return nil, err
		}
	}
	return &Pager{Schemas: s}, nil
}

type pageItem struct {
	Kind     string `json:"kind"`
	Page     int    `json:"page"`
	Pages    int    `json:"pages"`
	WidthPx  int    `json:"width_px"`
	HeightPx int    `json:"height_px"`
	Image    string `json:"image"`
	Bytes    int64  `json:"bytes"`
	Rects    []struct {
		ID   string `json:"id"`
		Rect []int  `json:"rect"`
	} `json:"rects"`
	Unmapped []string `json:"unmapped"`
}

// Render asks for one page. It is strict: exactly one description, for the page that was
// asked, whose rectangles name only the marks that were sent, lie inside the image and are
// not also reported as unmapped, and whose byte count is the size of the image written.
func (p *Pager) Render(ctx context.Context, s *host.Session, a kernel.AttemptRef, input, output refs.Wire, page, dpi int, marks []Mark, written func() (int64, error)) (Page, error) {
	if s == nil || input.Ref == "" || output.Ref == "" || page < 1 || len(marks) > 4096 {
		return Page{}, ErrPageProtocol
	}
	sent := map[string]bool{}
	for _, m := range marks {
		if m.ID == "" || sent[m.ID] {
			return Page{}, ErrPageProtocol
		}
		sent[m.ID] = true
	}
	params := map[string]any{"input": input, "output": output, "page": page, "dpi": dpi}
	if len(marks) > 0 {
		params["marks"] = marks
	}
	var it *pageItem
	out, err := s.Stream(ctx, "format.preview", a, params, func(_ context.Context, body json.RawMessage) error {
		if it != nil || p.Schemas.Validate("format.schema.json", "PreviewItem", body) != nil {
			return ErrPageProtocol
		}
		var x pageItem
		if json.Unmarshal(body, &x) != nil {
			return ErrPageProtocol
		}
		it = &x
		return nil
	})
	if err != nil {
		return Page{}, ErrPageProtocol
	}
	if out.Status != "completed" {
		return Page{}, ErrPageFailed
	}
	if it == nil || it.Page != page || it.Page > it.Pages {
		return Page{}, ErrPageProtocol
	}
	n, err := written()
	if err != nil || n != it.Bytes {
		return Page{}, ErrPageProtocol
	}
	res := Page{Page: it.Page, Pages: it.Pages, WidthPx: it.WidthPx, HeightPx: it.HeightPx}
	reported := map[string]bool{}
	for _, u := range it.Unmapped {
		if !sent[u] || reported[u] {
			return Page{}, ErrPageProtocol
		}
		reported[u] = true
		res.Unmapped = append(res.Unmapped, u)
	}
	located := map[string]bool{}
	for _, r := range it.Rects {
		if !sent[r.ID] || reported[r.ID] || len(r.Rect) != 4 {
			return Page{}, ErrPageProtocol
		}
		l, t, rr, b := r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3]
		if l < 0 || t < 0 || rr <= l || b <= t || rr > it.WidthPx || b > it.HeightPx {
			return Page{}, ErrPageProtocol
		}
		located[r.ID] = true
		res.Boxes = append(res.Boxes, Box{ID: r.ID, Rect: [4]int{l, t, rr, b}})
	}
	for id := range sent {
		if !located[id] && !reported[id] {
			return Page{}, ErrPageProtocol // every mark is either placed or said to be unplaceable
		}
	}
	return res, nil
}
