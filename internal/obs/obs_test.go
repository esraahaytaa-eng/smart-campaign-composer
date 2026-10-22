package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/obstest"
)

func get(t *testing.T, pool *pgxpool.Pool, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Routes(mux, pool)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz_Returns200WithoutDB(t *testing.T) {
	if rec := get(t, nil, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestReadyz_DBDown_Returns503(t *testing.T) {
	// Nothing listens on port 1, so every ping fails.
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if rec := get(t, pool, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestReadyz_DBUp_Returns200(t *testing.T) {
	pool := dbtest.Pool(t)

	if rec := get(t, pool, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
}

func TestMetrics_ServesPrometheusText(t *testing.T) {
	rec := get(t, nil, "/metrics")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Fatalf("want prometheus output, got %d", rec.Code)
	}
}

func TestInstrument_CountsByRoutePatternAndCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("the wrapped writer must still flush (SSE)")
		}
		w.WriteHeader(http.StatusTeapot)
	})
	h := Instrument(mux)
	labels := []string{"route", "GET /items/{id}", "code", "418"}
	before := obstest.Value(t, "scc_http_requests_total", labels...)
	unmatched := obstest.Value(t, "scc_http_requests_total", "route", "unmatched", "code", "404")

	for _, path := range []string{"/items/1", "/items/2", "/nowhere"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	if got := obstest.Value(t, "scc_http_requests_total", labels...) - before; got != 2 {
		t.Errorf("want 2 requests for the pattern (not one series per id), got %v", got)
	}
	if got := obstest.Value(t, "scc_http_requests_total", "route", "unmatched", "code", "404") - unmatched; got != 1 {
		t.Errorf("want 1 unmatched request, got %v", got)
	}
	if obstest.Value(t, "scc_http_request_duration_seconds", "route", "GET /items/{id}") == 0 {
		t.Error("want request durations recorded")
	}
}

func TestInstrument_ImplicitOK(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plain", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) })
	before := obstest.Value(t, "scc_http_requests_total", "route", "GET /plain", "code", "200")

	Instrument(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/plain", nil))

	if got := obstest.Value(t, "scc_http_requests_total", "route", "GET /plain", "code", "200") - before; got != 1 {
		t.Errorf("want 1 request with code 200, got %v", got)
	}
}
