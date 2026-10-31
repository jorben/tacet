package gui

import (
	"context"
	"net/http"
	"strings"

	"github.com/jorben/tacet/internal/app"
)

// PreviewPath is where rendered pages are served. The token in the path is the one
// PagePreview returned; it is unguessable and only names a page the engine still holds.
const PreviewPath = "/tacet-preview/"

// PagePreview renders one page of a file waiting for review, with the place of each candidate.
func (s *Service) PagePreview(job string, page, dpi int) (app.PagePreview, error) {
	core, err := s.engine()
	if err != nil {
		return app.PagePreview{}, publicError(err)
	}
	pp, err := core.PagePreview(context.Background(), job, page, dpi)
	return pp, publicError(err)
}

func validToken(t string) bool {
	if len(t) != 24 {
		return false
	}
	for i := 0; i < len(t); i++ {
		if c := t[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// AssetHandler serves rendered pages under PreviewPath and everything else from next. A
// page is sent decrypted from memory, never cached by the webview (no-store), never
// sniffed, and only to GET requests; nothing about the document appears in the URL.
func (s *Service) AssetHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, PreviewPath) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, PreviewPath), ".png")
		s.mu.Lock()
		core := s.core
		s.mu.Unlock()
		src, ok := core.(app.PreviewSource)
		if !validToken(token) || !ok {
			http.Error(w, "", http.StatusNotFound)
			return
		}
		png, found := src.PreviewImage(token)
		if !found {
			http.Error(w, "", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	})
}
