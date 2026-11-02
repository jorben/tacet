// Command tacet-desktop is the Tacet desktop application (Wails v3).
package main

import (
	"context"
	_ "embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/jorben/tacet/frontend"
	"github.com/jorben/tacet/internal/app"
	"github.com/jorben/tacet/internal/gui"
)

// wmEndSession is WM_ENDSESSION of winuser.h.
const wmEndSession = 0x0016

// The transparent black mask is a macOS template; Windows uses black or white
// ink to contrast with the taskbar. Both PNGs share the same alpha channel.
//
//go:embed assets/tray-template.png
var trayTemplate []byte

//go:embed assets/tray-dark.png
var trayDark []byte

//go:embed assets/tray-active-template.png
var trayActiveTemplate []byte

//go:embed assets/tray-active-dark.png
var trayActiveDark []byte

func main() {
	assets, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	host := &gui.WailsHost{}
	// The engine opens on first use, so a busy workspace or an unavailable key
	// store is reported inside the window instead of ending the program.
	svc := gui.NewService(func(ctx context.Context) (app.Core, error) {
		ws, err := app.DefaultWorkspace("desktop")
		if err != nil {
			return nil, err
		}
		// For verification runs only: a separate workspace, and with it a separate credential entry
		// (the entry's account names the workspace), so a test never touches the person's own.
		if dir := os.Getenv("TACET_DESKTOP_WORKSPACE"); dir != "" && filepath.IsAbs(dir) {
			ws = dir
		}
		return app.OpenWith(ctx, app.Config{Workspace: ws})
	}, host)
	var trayAnimation *gui.TrayAnimation
	a := application.New(application.Options{
		Name:        app.Name,
		Description: "Local document redaction",
		OnShutdown: func() {
			if trayAnimation != nil {
				trayAnimation.Close()
			}
			gui.Shutdown(svc)
		},
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: gui.AssetHandler(svc, application.AssetFileServerFS(assets)),
		},
		// One GUI instance per OS user and build mode (architecture 6.2).
		// Development and production use separate instance identities.
		SingleInstance: &application.SingleInstanceOptions{UniqueID: bundleID},
		Windows: application.WindowsOptions{
			// WM_ENDSESSION with wParam TRUE: the session really ends (a vetoed one sends FALSE); it comes
			// before Wails quits, so the exit that follows takes the short order.
			WndProcInterceptor: func(_ uintptr, msg uint32, wParam, _ uintptr) (uintptr, bool) {
				if msg == wmEndSession && wParam != 0 {
					gui.SystemEnding(svc)
				}
				return 0, false
			},
		},
		Mac: application.MacOptions{
			// The window can be hidden to the tray with work going on; quitting is the tray's or the
			// window's explicit choice (architecture 6.2).
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	win := a.Window.NewWithOptions(windowOptions())
	app.OnSystemEnding(func() { gui.SystemEnding(svc) }) // logout, restart, shutdown (macOS)
	host.Bind(a, win)
	gui.InterceptClose(a, win, svc)

	// The tray: what runs and what waits, "打开" and "退出" (architecture 6.2).
	menu := application.NewMenu()
	status := menu.Add("没有进行中的任务").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("打开 Tacet").OnClick(func(*application.Context) { win.Show(); win.Focus() })
	menu.Add("退出").OnClick(func(*application.Context) { win.Show(); gui.RequestQuit(a, svc) })
	tray := a.SystemTray.New().SetMenu(menu)
	tray.SetTooltip(app.Name)
	showTrayFrame := func(frame int) {
		light, dark := trayTemplate, trayDark
		if frame == 1 {
			light, dark = trayActiveTemplate, trayActiveDark
		}
		if goruntime.GOOS == "darwin" {
			tray.SetTemplateIcon(light)
		} else {
			tray.SetIcon(light).SetDarkModeIcon(dark)
		}
	}
	showTrayFrame(0)
	trayAnimation = gui.NewTrayAnimation(application.InvokeAsync, showTrayFrame)
	tray.OnClick(func() { win.Show(); win.Focus() })
	host.BindTray(tray, menu, status, trayAnimation)
	// Woken from sleep (NSWorkspaceDidWakeNotification / PBT_APMRESUMEAUTOMATIC): read the state again.
	a.Event.OnApplicationEvent(events.Common.SystemDidWake, func(*application.ApplicationEvent) { go gui.Woke(svc) })
	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}

// windowOptions is the main window. The frontend draws the title bar and marks it draggable
// (--wails-draggable); only the system traffic lights are native, so the two must agree on height.
func windowOptions() application.WebviewWindowOptions {
	titleBar := application.MacTitleBarHiddenInset
	// AppKit places the traffic lights by the toolbar style, not by the page: the default (unified)
	// centres them 26px from the top, below the middle of the bar the frontend draws for macOS
	// (host.css). Unified compact centres them at 20px, 12px from the left edge.
	titleBar.ToolbarStyle = application.MacToolbarStyleUnifiedCompact
	return application.WebviewWindowOptions{
		Title:     app.Name,
		Width:     1180,
		Height:    760,
		MinWidth:  960,
		MinHeight: 600,
		Mac: application.MacWindow{
			TitleBar: titleBar,
		},
		EnableFileDrop:   true,
		BackgroundColour: application.NewRGB(250, 250, 248),
		URL:              "/",
	}
}
