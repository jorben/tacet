package gui

import "github.com/jorben/tacet/internal/app"

// Info describes the running application.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Service is bound to the frontend. It exposes app commands and query reads
// only; plugin methods are never bound.
type Service struct{}

// NewService returns the bound service.
func NewService() *Service { return &Service{} }

// AppInfo returns the product name and version.
func (s *Service) AppInfo() Info { return Info{Name: app.Name, Version: app.Version} }
