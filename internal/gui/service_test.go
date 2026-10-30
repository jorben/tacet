package gui

import "testing"

func TestCloseHandlerReady(t *testing.T) {
	s := NewService()
	if s.closeReady.Load() {
		t.Fatal("close must keep its native behaviour until the frontend is ready")
	}
	s.CloseHandlerReady()
	if !s.closeReady.Load() {
		t.Fatal("CloseHandlerReady must mark the frontend as ready")
	}
}

func TestAppInfo(t *testing.T) {
	info := NewService().AppInfo()
	if info.Name == "" || info.Version == "" {
		t.Fatalf("AppInfo must report name and version, got %+v", info)
	}
}
