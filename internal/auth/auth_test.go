package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/dbtest"
	"smart-campaign-composer/internal/obs"
	"smart-campaign-composer/internal/obstest"
)

const (
	email    = "marketer@example.com"
	password = "correct-password"
)

type server struct {
	t    *testing.T
	pool *pgxpool.Pool
	mux  *http.ServeMux
}

func newServer(t *testing.T) *server {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.FreshDB(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO marketers (email, name, password_hash) VALUES ($1, 'Test Marketer', $2)`,
		email, string(hash)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	obs.Routes(mux, pool)
	auth.Routes(mux, pool, "mock")
	return &server{t: t, pool: pool, mux: mux}
}

func (s *server) do(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func (s *server) login(user, pass string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": user, "password": pass})
	return s.do(http.MethodPost, "/api/login", string(body))
}

// sessionCookie logs in and returns the session cookie.
func (s *server) sessionCookie() *http.Cookie {
	s.t.Helper()
	rec := s.login(email, password)
	if rec.Code != http.StatusOK {
		s.t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	s.t.Fatal("no session cookie")
	return nil
}

func (s *server) count(sql string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func TestLogin_SetsHttpOnlyLaxCookie(t *testing.T) {
	s := newServer(t)

	c := s.sessionCookie()

	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("cookie flags wrong: %+v", c)
	}
	if len(c.Value) < 26 { // crypto/rand.Text: 128 bits
		t.Fatalf("token too short: %d chars", len(c.Value))
	}
}

func TestLogin_EmailIsCaseInsensitive(t *testing.T) {
	s := newServer(t)

	if rec := s.login("Marketer@Example.COM", password); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestLogin_WrongPasswordSameAsUnknownEmail(t *testing.T) {
	s := newServer(t)

	wrongPass := s.login(email, "wrong-password")
	unknown := s.login("nobody@example.com", password)

	if wrongPass.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("want 401/401, got %d/%d", wrongPass.Code, unknown.Code)
	}
	if wrongPass.Body.String() != unknown.Body.String() {
		t.Fatalf("responses differ: %q vs %q", wrongPass.Body, unknown.Body)
	}
	if len(wrongPass.Result().Cookies()) != 0 {
		t.Fatal("failed login set a cookie")
	}
	if n := s.count(`SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("failed logins created %d sessions", n)
	}
}

func TestLogin_InvalidBody400(t *testing.T) {
	s := newServer(t)

	for _, body := range []string{`not json`, `{}`, `{"email":"","password":"x"}`, `{"email":"a@b.c"}`,
		`{"email":"a@b.c","password":"` + strings.Repeat("x", 5000) + `"}`} {
		if rec := s.do(http.MethodPost, "/api/login", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %.30q: want 400, got %d", body, rec.Code)
		}
	}
}

func TestSession_DBStoresOnlyTokenHash(t *testing.T) {
	s := newServer(t)
	c := s.sessionCookie()

	hash := sha256.Sum256([]byte(c.Value))
	if n := s.count(`SELECT count(*) FROM sessions WHERE token_hash = $1`, hash[:]); n != 1 {
		t.Fatalf("want 1 session stored by hash, got %d", n)
	}
	if n := s.count(`SELECT count(*) FROM sessions s WHERE token_hash = $1 OR position($2 IN s::text) > 0`, []byte(c.Value), c.Value); n != 0 {
		t.Fatal("raw token stored in sessions")
	}
}

func TestMe_ReturnsMarketerAndLLMMode(t *testing.T) {
	s := newServer(t)

	rec := s.do(http.MethodGet, "/api/me", "", s.sessionCookie())

	var me struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		LLMMode string `json:"llm_mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s (%v)", rec.Code, rec.Body, err)
	}
	if me.Email != email || me.Name != "Test Marketer" || me.LLMMode != "mock" {
		t.Fatalf("unexpected body: %+v", me)
	}
}

func TestRequire_ExpiredSession401(t *testing.T) {
	s := newServer(t)
	c := s.sessionCookie()
	if _, err := s.pool.Exec(context.Background(), `UPDATE sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}

	if rec := s.do(http.MethodGet, "/api/me", "", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequire_UnknownToken401(t *testing.T) {
	s := newServer(t)

	c := &http.Cookie{Name: auth.CookieName, Value: "forged-token"}
	if rec := s.do(http.MethodGet, "/api/me", "", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestLogout_InvalidatesSession(t *testing.T) {
	s := newServer(t)
	c := s.sessionCookie()

	rec := s.do(http.MethodPost, "/api/logout", "", c)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: want 204, got %d", rec.Code)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie, got %+v", cleared)
	}
	if n := s.count(`SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("%d sessions left after logout", n)
	}
	if rec := s.do(http.MethodGet, "/api/me", "", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie after logout: want 401, got %d", rec.Code)
	}
}

func TestAPI_RequiresSessionExceptLogin(t *testing.T) {
	s := newServer(t)

	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/me"},
		{http.MethodPost, "/api/logout"},
		{http.MethodGet, "/api/campaigns"}, // routes added later are covered by the /api/ guard
		{http.MethodGet, "/api/login"},
	} {
		if rec := s.do(r.method, r.path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d", r.method, r.path, rec.Code)
		}
	}
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		if rec := s.do(http.MethodGet, path, ""); rec.Code != http.StatusOK {
			t.Errorf("%s: want 200 without session, got %d", path, rec.Code)
		}
	}
}

func TestMarketerID_InContext(t *testing.T) {
	s := newServer(t)
	c := s.sessionCookie()

	var got int64
	h := auth.Require(s.pool, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.MarketerID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(c)
	h.ServeHTTP(httptest.NewRecorder(), req)

	want := int64(s.count(`SELECT id FROM marketers WHERE email = $1`, email))
	if got != want {
		t.Fatalf("want marketer %d in context, got %d", want, got)
	}
}

// The metrics middleware wraps the outer mux, so the guarded route's pattern
// must reach it, not the catch-all "/api/".
func TestRequire_GuardedRouteLabelsMetrics(t *testing.T) {
	s := newServer(t)
	cookie := s.sessionCookie()
	before := obstest.Value(t, "scc_http_requests_total", "route", "GET /api/me", "code", "200")

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(cookie)
	obs.Instrument(s.mux).ServeHTTP(httptest.NewRecorder(), req)

	if got := obstest.Value(t, "scc_http_requests_total", "route", "GET /api/me", "code", "200") - before; got != 1 {
		t.Fatalf("want 1 request labelled GET /api/me, got %v", got)
	}
}
