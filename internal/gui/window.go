package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// EventCloseRequested is emitted to the frontend when the user asks to close
// the main window. The frontend decides what happens next (minimise, confirm
// unfinished tasks, quit); it must keep in sync with src/services/wails.ts.
const EventCloseRequested = "tacet:close-requested"

// InterceptClose hands user-initiated closes of win to the frontend once it has
// announced itself with Service.CloseHandlerReady. The close is cancelled here;
// the frontend ends the application through the runtime when it is done.
func InterceptClose(app *application.App, win *application.WebviewWindow, svc *Service) {
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !svc.closeReady.Load() {
			return
		}
		e.Cancel()
		app.Event.Emit(EventCloseRequested)
	})
}
