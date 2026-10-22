package spa_test

import (
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
	write("favicon.svg", "<svg/>")
	// A secret next to the site must stay unreachable.
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", spa.Handler(dir))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "api", http.StatusTeapot) })
	return mux
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHandler_PageRoutesGetTheApp(t *testing.T) {
	h := site(t)
	for _, path := range []string{"/", "/login", "/campaigns/7", "/campaigns/7/content"} {
		rec := get(h, http.MethodGet, path)
		if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html>app" {
			t.Errorf("%s: want the app, got %d %q", path, rec.Code, rec.Body)
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
