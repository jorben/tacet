package gui

import (
	"sync/atomic"

	"github.com/jorben/tacet/internal/app"
)

// Info describes the running application.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Service is bound to the frontend. It exposes app commands and query reads
// only; plugin methods are never bound.
type Service struct {
	closeReady atomic.Bool
}

// NewService returns the bound service.
func NewService() *Service { return &Service{} }

// AppInfo returns the product name and version.
func (s *Service) AppInfo() Info { return Info{Name: app.Name, Version: app.Version} }

// CloseHandlerReady is called by the frontend once it listens for
// EventCloseRequested. Until then a user-initiated window close keeps its
// native behaviour, so a frontend that failed to load can never trap the window.
func (s *Service) CloseHandlerReady() { s.closeReady.Store(true) }
