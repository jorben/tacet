package plan

import (
	"errors"
	"math"
	"sort"
)

var ErrGeometry = errors.New("PLAN_GEOMETRY_INVALID")

// Box is a PDF character box in page points: origin bottom-left, y upwards,
// as returned by PDFium (left, bottom, right, top).
type Box struct{ L, B, R, T float64 }

// Rect is a pixel rectangle of the rendered page, half-open: [L,R) x [T,B).
type Rect struct{ L, T, R, B int }

// Page describes the rendered page. W and H are the unrotated size in points;
// Rotation is the clockwise rotation applied when rendering (0, 90, 180, 270).
type Page struct {
	W, H     float64
	Rotation int
}

// Params control the expansion. MarginPoints is the plugin-declared outward
// expansion of the target area; AntialiasPixels covers the soft edge of
// rendered glyphs so no partial pixels of a removed character survive.
type Params struct {
	DPI             float64
	MarginPoints    float64
	AntialiasPixels int
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// Regions is the single geometry used by preview overlays and by the plugin's
// write-back (MD-15): boxes are merged line by line, expanded by the margin,
// converted to pixels at the given DPI with the page rotation applied, and
// clipped to the page. Invalid input is an error; nothing is guessed (INV-09).
func Regions(boxes []Box, page Page, p Params) ([]Rect, error) {
	if !finite(page.W, page.H, p.DPI, p.MarginPoints) || page.W <= 0 || page.H <= 0 || p.DPI <= 0 ||
		p.MarginPoints < 0 || p.AntialiasPixels < 0 || p.AntialiasPixels > 16 {
		return nil, ErrGeometry
	}
	switch page.Rotation {
	case 0, 90, 180, 270:
	default:
		return nil, ErrGeometry
	}
	for _, b := range boxes {
		if !finite(b.L, b.B, b.R, b.T) || b.R < b.L || b.T <= b.B {
			return nil, ErrGeometry
		}
	}
	lines := mergeLines(boxes)
	scale := p.DPI / 72
	wpx, hpx := page.W*scale, page.H*scale
	outW, outH := wpx, hpx
	if page.Rotation == 90 || page.Rotation == 270 {
		outW, outH = hpx, wpx
	}
	width, height := int(math.Ceil(outW)), int(math.Ceil(outH))
	var out []Rect
	for _, l := range lines {
		l = Box{l.L - p.MarginPoints, l.B - p.MarginPoints, l.R + p.MarginPoints, l.T + p.MarginPoints}
		// Unrotated pixel space: x grows right, y grows down.
		u0, u1 := l.L*scale, l.R*scale
		v0, v1 := (page.H-l.T)*scale, (page.H-l.B)*scale
		x0, x1, y0, y1 := rotate(u0, v0, u1, v1, wpx, hpx, page.Rotation)
		r := Rect{
			L: int(math.Floor(x0)) - p.AntialiasPixels, T: int(math.Floor(y0)) - p.AntialiasPixels,
			R: int(math.Ceil(x1)) + p.AntialiasPixels, B: int(math.Ceil(y1)) + p.AntialiasPixels,
		}
		r.L, r.T = max(r.L, 0), max(r.T, 0)
		r.R, r.B = min(r.R, width), min(r.B, height)
		if r.R > r.L && r.B > r.T {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].T != out[j].T {
			return out[i].T < out[j].T
		}
		return out[i].L < out[j].L
	})
	return out, nil
}

// rotate maps a rectangle in unrotated pixel space to the rendered image,
// rotated clockwise, returning its bounding box.
func rotate(u0, v0, u1, v1, wpx, hpx float64, rot int) (x0, x1, y0, y1 float64) {
	switch rot {
	case 90:
		return hpx - v1, hpx - v0, u0, u1
	case 180:
		return wpx - u1, wpx - u0, hpx - v1, hpx - v0
	case 270:
		return v0, v1, wpx - u1, wpx - u0
	}
	return u0, u1, v0, v1
}

// mergeLines joins boxes that sit on one text line: their vertical overlap is
// more than half of the shorter box, and the horizontal gap is at most 1.5
// times the taller box. Two columns on a shared baseline therefore stay as two
// regions instead of one rectangle that would cover the text between them.
func mergeLines(boxes []Box) []Box {
	sorted := append([]Box(nil), boxes...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.B != b.B {
			return a.B > b.B
		}
		return a.L < b.L
	})
	var lines []Box
	for _, b := range sorted {
		merged := false
		for i, l := range lines {
			overlap := math.Min(l.T, b.T) - math.Max(l.B, b.B)
			shorter := math.Min(l.T-l.B, b.T-b.B)
			tall := math.Max(l.T-l.B, b.T-b.B)
			gap := math.Max(l.L, b.L) - math.Min(l.R, b.R)
			if overlap > shorter/2 && gap <= tall*1.5 {
				lines[i] = Box{math.Min(l.L, b.L), math.Min(l.B, b.B), math.Max(l.R, b.R), math.Max(l.T, b.T)}
				merged = true
				break
			}
		}
		if !merged {
			lines = append(lines, b)
		}
	}
	return lines
}
