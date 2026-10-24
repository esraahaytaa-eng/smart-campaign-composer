// Package spa serves the built frontend (web/dist) from the api (T15).
package spa

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Handler serves files from dir, and index.html for every other path so the
// frontend router can handle page URLs. Missing files under /assets/ are 404s:
// index.html served as a script would hide a stale build. Mount it at "/" so
// /api/ routes stay more specific.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func Handler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// D56: only the app's own files may run, load or be connected to (no
		// inline scripts or styles), and other sites may not frame it
		// (clickjacking of approve and send).
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		p := path.Clean("/" + r.URL.Path) // never leaves dir
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && !info.IsDir() {
			if strings.HasPrefix(p, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // names carry a content hash
			}
			serve(w, r, filepath.Join(dir, filepath.FromSlash(p)))
			return
		}
		if strings.HasPrefix(p, "/assets/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		serve(w, r, filepath.Join(dir, "index.html"))
	})
}

// serve sends the file, or its precompressed copy (name.gz, written by the
// image build) when the browser accepts gzip (D60).
func serve(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Vary", "Accept-Encoding")
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		if f, err := os.Open(name + ".gz"); err == nil {
			defer f.Close()
			if info, err := f.Stat(); err == nil {
				w.Header().Set("Content-Encoding", "gzip")
				// The original's type: with nosniff, a script typed as gzip would not run.
				w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
				http.ServeContent(w, r, name, info.ModTime(), f)
				return
			}
		}
	}
	http.ServeFile(w, r, name)
}

// acceptsGzip reads an Accept-Encoding header: gzip listed, and not with q=0.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q, found := strings.CutPrefix(strings.TrimSpace(params), "q=")
		if !found {
			return true
		}
		v, err := strconv.ParseFloat(q, 64)
		return err == nil && v > 0
	}
	return false
}
