package spa_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smart-campaign-composer/internal/spa"
)

func site(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<!doctype html>app")
	write("assets/app-1a2b.js", "console.log(1)")
	write("favicon.svg", "<svg/>") // no compressed copy
	// The image build's compressed copies (D60).
	write("index.html.gz", gz(t, "<!doctype html>app"))
	write("assets/app-1a2b.js.gz", gz(t, "console.log(1)"))
	// A secret next to the site must stay unreachable.
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", spa.Handler(dir))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "api", http.StatusTeapot) })
	return mux
}

func gz(t *testing.T, s string) string {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func get(h http.Handler, method, path string, header ...string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	h.ServeHTTP(rec, req)
	return rec
}

// D60: a browser that accepts gzip gets the precompressed copy, with the
// original file's type (nosniff would block a script typed as gzip) and the
// same caching and security headers.
func TestHandler_ServesCompressedCopies(t *testing.T) {
	h := site(t)
	for _, c := range []struct{ path, body, typ, cache string }{
		{"/assets/app-1a2b.js", "console.log(1)", "javascript", "immutable"},
		{"/campaigns/7", "<!doctype html>app", "text/html", "no-cache"},
	} {
		rec := get(h, http.MethodGet, c.path, "Accept-Encoding", "br, gzip")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: want the gzip copy, got %d %q", c.path, rec.Code, rec.Header().Get("Content-Encoding"))
		}
		r, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if body, _ := io.ReadAll(r); string(body) != c.body {
			t.Errorf("%s: decompressed to %q", c.path, body)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), c.typ) || !strings.Contains(rec.Header().Get("Cache-Control"), c.cache) {
			t.Errorf("%s: want type %s and cache %s, got %q, %q", c.path, c.typ, c.cache, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Content-Security-Policy") != csp || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: want Vary, the CSP and nosniff on the compressed copy", c.path)
		}
	}
}

func TestHandler_PlainWhenGzipIsNotAccepted(t *testing.T) {
	h := site(t)
	for _, accept := range []string{"", "br", "gzip;q=0", "x-gzip-not"} {
		rec := get(h, http.MethodGet, "/assets/app-1a2b.js", "Accept-Encoding", accept)
		if rec.Body.String() != "console.log(1)" || rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("Accept-Encoding %q: want the plain file, got %q %q", accept, rec.Header().Get("Content-Encoding"), rec.Body)
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("Accept-Encoding %q: a shared cache must keep the two versions apart (Vary)", accept)
		}
	}
	// A file without a compressed copy is sent as it is.
	if rec := get(h, http.MethodGet, "/favicon.svg", "Accept-Encoding", "gzip"); rec.Body.String() != "<svg/>" || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("favicon: want it plain, got %q", rec.Header().Get("Content-Encoding"))
	}
}

// D56: only the app's own files may run, load or be connected to; no inline
// scripts or styles, no plugins, no other site may frame it (clickjacking).
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func TestHandler_PageRoutesGetTheApp(t *testing.T) {
	h := site(t)
	for _, path := range []string{"/", "/login", "/campaigns/7", "/campaigns/7/content"} {
		rec := get(h, http.MethodGet, path)
		if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html>app" {
			t.Errorf("%s: want the app, got %d %q", path, rec.Code, rec.Body)
		}
		if rec.Header().Get("Content-Security-Policy") != csp || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: want the strict CSP and no referrer (D56), got %q / %q", path, rec.Header().Get("Content-Security-Policy"), rec.Header().Get("Referrer-Policy"))
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: index.html must be revalidated so a new build is picked up, got %q", path, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestHandler_ServesFiles(t *testing.T) {
	h := site(t)

	rec := get(h, http.MethodGet, "/assets/app-1a2b.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("want the script, got %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("hashed assets can be cached for good, got %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("want nosniff")
	}
	if rec.Header().Get("Content-Security-Policy") != csp {
		t.Error("want the strict CSP on every file (D56)")
	}
	if rec := get(h, http.MethodGet, "/favicon.svg"); rec.Code != http.StatusOK || rec.Body.String() != "<svg/>" {
		t.Fatalf("want the favicon, got %d", rec.Code)
	}
}

// A missing script must be a 404, not index.html served as JavaScript.
func TestHandler_MissingAssetIs404(t *testing.T) {
	if rec := get(site(t), http.MethodGet, "/assets/old-9z9z.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %q", rec.Code, rec.Body)
	}
}

func TestHandler_StaysInsideItsDirectory(t *testing.T) {
	h := site(t)
	for _, path := range []string{"/../secret.txt", "/assets/../../secret.txt", "/%2e%2e/secret.txt"} {
		// The mux may redirect an unclean path; the file's content must never come back.
		if rec := get(h, http.MethodGet, path); rec.Body.String() == "secret" {
			t.Errorf("%s: served a file outside the site", path)
		}
	}
}

func TestHandler_OnlyReads(t *testing.T) {
	h := site(t)
	if rec := get(h, http.MethodPost, "/campaigns"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: want 405, got %d", rec.Code)
	}
	if rec := get(h, http.MethodHead, "/"); rec.Code != http.StatusOK {
		t.Fatalf("HEAD: want 200, got %d", rec.Code)
	}
	if rec := get(h, http.MethodGet, "/api/anything"); rec.Code != http.StatusTeapot {
		t.Fatalf("/api/ must stay with the API, got %d", rec.Code)
	}
}
