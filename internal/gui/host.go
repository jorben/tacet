package gui

import (
	"errors"
	"io"
	"os"
	"sync"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// WailsHost is the Host over a running Wails application. It is created before
// the application exists and bound once the application and window are up.
type WailsHost struct {
	mu  sync.Mutex
	app *application.App
	win *application.WebviewWindow
}

// Bind connects the host to the running application and forwards files dropped
// on the window (which carry real paths, unlike a browser drop) as an event.
func (h *WailsHost) Bind(a *application.App, win *application.WebviewWindow) {
	h.mu.Lock()
	h.app, h.win = a, win
	h.mu.Unlock()
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		if files := e.Context().DroppedFiles(); len(files) > 0 {
			h.Emit(EventFilesDropped, files)
		}
	})
}

func (h *WailsHost) bound() (*application.App, *application.WebviewWindow, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.app == nil {
		return nil, nil, errors.New("HOST_UNAVAILABLE")
	}
	return h.app, h.win, nil
}

func (h *WailsHost) PickFiles(title string) ([]string, error) {
	a, win, err := h.bound()
	if err != nil {
		return nil, err
	}
	files, err := a.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title: title, CanChooseFiles: true, AllowsMultipleSelection: true, Window: win,
		Filters: []application.FileFilter{{DisplayName: "文档", Pattern: "*.pdf;*.docx;*.txt;*.md;*.log;*.csv;*.json;*.jsonl;*.xlsx"}},
	}).PromptForMultipleSelection()
	if err != nil {
		return nil, errors.New("DIALOG_FAILED")
	}
	return files, nil
}

func (h *WailsHost) PickFolder(title string) (string, error) {
	a, win, err := h.bound()
	if err != nil {
		return "", err
	}
	dir, err := a.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title: title, CanChooseDirectories: true, CanCreateDirectories: true, Window: win,
	}).PromptForSingleSelection()
	if err != nil {
		return "", errors.New("DIALOG_FAILED")
	}
	return dir, nil
}

func (h *WailsHost) SaveText(title, suggested, text string) (bool, error) {
	a, win, err := h.bound()
	if err != nil {
		return false, err
	}
	path, err := a.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title: title, Filename: suggested, Window: win, CanCreateDirectories: true,
		Filters: []application.FileFilter{{DisplayName: "Tacet 配置", Pattern: "*.json"}},
	}).PromptForSingleSelection()
	if err != nil {
		return false, errors.New("DIALOG_FAILED")
	}
	if path == "" {
		return false, nil
	}
	// a file the person names is never overwritten silently by this call: the dialog asks for that itself
	if err = os.WriteFile(path, []byte(text), 0o600); err != nil {
		return false, errors.New("WRITE_FAILED")
	}
	return true, nil
}

func (h *WailsHost) PickText(title string, limit int64) (string, bool, error) {
	a, win, err := h.bound()
	if err != nil {
		return "", false, err
	}
	path, err := a.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title: title, CanChooseFiles: true, Window: win,
		Filters: []application.FileFilter{{DisplayName: "Tacet 配置", Pattern: "*.json"}},
	}).PromptForSingleSelection()
	if err != nil {
		return "", false, errors.New("DIALOG_FAILED")
	}
	if path == "" {
		return "", false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, errors.New("READ_FAILED")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit || !utf8.Valid(data) {
		return "", false, errors.New("FILE_INVALID")
	}
	return string(data), true, nil
}

func (h *WailsHost) Emit(event string, data any) {
	if a, _, err := h.bound(); err == nil {
		a.Event.Emit(event, data)
	}
}
