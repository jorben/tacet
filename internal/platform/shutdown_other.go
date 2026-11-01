//go:build !darwin

package platform

// OnSystemEnding is not observed here: on Windows the desktop entry sees WM_QUERYENDSESSION and
// WM_ENDSESSION itself, through the window procedure Wails gives it.
func OnSystemEnding(func()) {}
