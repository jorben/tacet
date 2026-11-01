// Command tacet-desktop is the Tacet desktop application (Wails v3).
package main

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"

	"github.com/jorben/tacet/frontend"
	"github.com/jorben/tacet/internal/app"
	"github.com/jorben/tacet/internal/gui"
)

// wmEndSession is WM_ENDSESSION of winuser.h.
const wmEndSession = 0x0016

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
	a := application.New(application.Options{
		Name:        app.Name,
		Description: "Local document redaction",
		OnShutdown:  func() { gui.Shutdown(svc) },
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: svc.AssetHandler(application.AssetFileServerFS(assets)),
		},
		// One GUI instance per OS user (architecture 6.2). Handing files dropped
		// on a second launch to this instance comes with the lifecycle module.
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "com.jorben.tacet"},
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
	win := a.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     app.Name,
		Width:     1180,
		Height:    760,
		MinWidth:  960,
		MinHeight: 600,
		// The frontend draws the title bar and marks it draggable (--wails-draggable);
		// only the system traffic lights are native.
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
		EnableFileDrop:   true,
		BackgroundColour: application.NewRGB(250, 250, 248),
		URL:              "/",
	})
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
	if goruntime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight).SetDarkModeIcon(icons.SystrayDark)
	}
	tray.OnClick(func() { win.Show(); win.Focus() })
	host.BindTray(tray, menu, status)
	// Woken from sleep (NSWorkspaceDidWakeNotification / PBT_APMRESUMEAUTOMATIC): read the state again.
	a.Event.OnApplicationEvent(events.Common.SystemDidWake, func(*application.ApplicationEvent) { go gui.Woke(svc) })
	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
