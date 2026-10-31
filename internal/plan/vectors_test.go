package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The same vectors drive the Python PDF plugin's geometry: preview overlays and
// write-back use one geometry (MD-15), so the two implementations are held to
// identical rectangles.
func TestRegionVectorsSharedWithThePythonPlugin(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "protocol", "testdata", "regions-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name   string       `json:"name"`
			Boxes  [][4]float64 `json:"boxes"`
			W, H   float64      `json:"-"`
			PageW  float64      `json:"page_w"`
			PageH  float64      `json:"page_h"`
			DPI    float64      `json:"dpi"`
			Margin float64      `json:"margin_points"`
			AA     int          `json:"antialias_pixels"`
			Rects  [][4]int     `json:"rects"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &f); err != nil || len(f.Cases) < 8 {
		t.Fatalf("%v %d", err, len(f.Cases))
	}
	for _, c := range f.Cases {
		var bs []Box
		for _, b := range c.Boxes {
			bs = append(bs, Box{b[0], b[1], b[2], b[3]})
		}
		rs, err := Regions(bs, Page{W: c.PageW, H: c.PageH}, Params{DPI: c.DPI, MarginPoints: c.Margin, AntialiasPixels: c.AA})
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		got := [][4]int{}
		for _, r := range rs {
			got = append(got, [4]int{r.L, r.T, r.R, r.B})
		}
		if !reflect.DeepEqual(got, c.Rects) {
			t.Errorf("%s: %v want %v", c.Name, got, c.Rects)
		}
	}
}
