// Package spa serves the built frontend (web/dist) from the api (T15).
package spa

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Handler serves files from dir, and index.html for every other path so the
// frontend router can handle page URLs. Missing files under /assets/ are 404s:
// index.html served as a script would hide a stale build. Mount it at "/" so
// /api/ routes stay more specific.
func Handler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		p := path.Clean("/" + r.URL.Path) // never leaves dir
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && !info.IsDir() {
			if strings.HasPrefix(p, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // names carry a content hash
			}
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/assets/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
