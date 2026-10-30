// Command tacet-desktop is the Tacet desktop application (Wails v3).
package main

import (
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/jorben/tacet/frontend"
	"github.com/jorben/tacet/internal/app"
	"github.com/jorben/tacet/internal/gui"
)

func main() {
	assets, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	svc := gui.NewService()
	a := application.New(application.Options{
		Name:        app.Name,
		Description: "Local document redaction",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		// One GUI instance per OS user (architecture 6.2). Handing files dropped
		// on a second launch to this instance comes with the lifecycle module.
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "com.jorben.tacet"},
		Mac: application.MacOptions{
			// Until the tray exists, closing the last window quits.
			ApplicationShouldTerminateAfterLastWindowClosed: true,
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
		BackgroundColour: application.NewRGB(250, 250, 248),
		URL:              "/",
	})
	gui.InterceptClose(a, win, svc)
	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
