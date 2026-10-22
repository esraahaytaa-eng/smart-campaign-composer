package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/dbtest"
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
