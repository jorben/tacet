package main

import (
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// trafficLightCentre is how far below the top of a hidden-inset window AppKit centres the traffic
// lights, by toolbar style. Measured with NSWindow.standardWindowButton on macOS 27.2.
var trafficLightCentre = map[application.MacToolbarStyle]int{
	application.MacToolbarStyleAutomatic:      26,
	application.MacToolbarStyleUnified:        26,
	application.MacToolbarStyleUnifiedCompact: 20,
}

// webViewOverhang is how far the web view reaches above the top of the window: Wails v3.0.0 gives
// the content view width-1 × height-1 and the WKWebView width × height at the same bottom-left origin
// (webview_window_darwin.go), so page y = window y + 1. Measured in a screenshot of the built app.
const (
	webViewOverhang = 1
	overhangWails   = "v3.0.0"
	titleBarBorder  = 1 // .titlebar border-bottom, shell.css
)

// The traffic lights sit on the middle line of the title bar the frontend draws for macOS: both the
// visible bar above its border and the content laid out inside it.
func TestTheTrafficLightsSitOnTheMiddleOfTheTitleBar(t *testing.T) {
	tb := windowOptions().Mac.TitleBar
	if !tb.AppearsTransparent || !tb.HideTitle || !tb.FullSizeContent || !tb.UseToolbar {
		t.Fatalf("the window is no longer hidden-inset (the page draws the title bar): %+v", tb)
	}
	centre, ok := trafficLightCentre[tb.ToolbarStyle]
	if !ok {
		t.Fatalf("toolbar style %d has no measured traffic-light position", tb.ToolbarStyle)
	}
	if v := wailsVersion(t); v != overhangWails {
		t.Fatalf("Wails is %s; the web view overhang (%dpx) was measured on %s: measure it again", v, webViewOverhang, overhangWails)
	}

	height, padTop := titleBar(t)
	// In window coordinates the bar runs from -overhang to height-overhang, its border last.
	if visible := height - webViewOverhang - titleBarBorder; visible != 2*centre {
		t.Errorf("traffic lights are centred %dpx from the top (toolbar style %d) but the bar above the border is %dpx: middle at %dpx",
			centre, tb.ToolbarStyle, visible, visible/2)
	}
	if mid := float64(padTop+height-titleBarBorder)/2 - webViewOverhang; mid != float64(centre) {
		t.Errorf("traffic lights are centred %dpx from the top but the title bar centres its content %gpx from the top", centre, mid)
	}
}

func wailsVersion(t *testing.T) string {
	t.Helper()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build info")
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/wailsapp/wails/v3" {
			if d.Replace != nil {
				return d.Replace.Version
			}
			return d.Version
		}
	}
	t.Fatal("Wails is not a dependency")
	return ""
}

// titleBar is the macOS title bar row and its top padding: the darwin overrides in host.css, or
// else the browser prototype's in shell.css.
func titleBar(t *testing.T) (height, padTop int) {
	t.Helper()
	read := func(path string) []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	px := func(re string, css []byte) (int, bool) {
		m := regexp.MustCompile(re).FindSubmatch(css)
		if m == nil {
			return 0, false
		}
		n, _ := strconv.Atoi(string(m[1]))
		return n, true
	}
	host, shell := read("../../frontend/src/styles/host.css"), read("../../frontend/src/styles/shell.css")
	height, ok := px(`:root\[data-host="darwin"\] \.win \{[^}]*grid-template-rows:\s*(\d+)px`, host)
	if !ok {
		if height, ok = px(`(?m)^\.win\{[^}]*grid-template-rows:(\d+)px`, shell); !ok {
			t.Fatal("no title bar height in host.css or shell.css")
		}
	}
	padTop, _ = px(`:root\[data-host="darwin"\] \.titlebar \{[^}]*padding-top:\s*(\d+)px`, host)
	return height, padTop
}
