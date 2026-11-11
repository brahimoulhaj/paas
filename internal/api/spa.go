package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"panel/internal/brand"
)

// spa serves the embedded web app. Unknown paths fall back to index.html so that
// client-side routes survive a page reload.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && !strings.HasPrefix(path.Base(name), ".") {
			if info, err := fs.Stat(s.web, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") { // content-hashed by Vite
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		if path.Ext(name) != "" { // a missing file, not a client-side route
			http.NotFound(w, r)
			return
		}
		index, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, brand.Name+": the web app is not built into this binary. Run `make web` and rebuild.",
				http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
