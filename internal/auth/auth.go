// Package auth handles marketer login, logout and session checks (D10, D22).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName = "scc_session"
	// Proposed default (DECISIONS.md Open Questions), not yet confirmed.
	sessionTTL   = 12 * time.Hour
	maxLoginBody = 4 << 10
)

const errInvalidLogin = "invalid email or password"

// Routes registers POST /api/login and guards every other /api/ path with
// Require. It returns the guarded mux, where later tasks add their routes.
func Routes(mux *http.ServeMux, pool *pgxpool.Pool, llmMode string) *http.ServeMux {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/me", me(pool, llmMode))
	api.HandleFunc("POST /api/logout", logout(pool))

	mux.HandleFunc("POST /api/login", login(pool))
	mux.Handle("/api/", Require(pool, api))
	return api
}

type ctxKey struct{}

// MarketerID returns the logged-in marketer set by Require.
func MarketerID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKey{}).(int64)
	return id, ok
}

// Require rejects requests without a valid, unexpired session with 401 and
// puts the marketer ID into the request context.
func Require(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		var id int64
		err = pool.QueryRow(r.Context(), `SELECT marketer_id FROM sessions WHERE token_hash = $1 AND expires_at > now()`,
			hashToken(c.Value)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		if err != nil {
			serverError(w, "session lookup", err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func login(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&in); err != nil ||
			strings.TrimSpace(in.Email) == "" || in.Password == "" {
			writeError(w, http.StatusBadRequest, "email and password are required")
			return
		}

		var m marketer
		var hash string
		err := pool.QueryRow(r.Context(), `SELECT id, email, name, password_hash FROM marketers WHERE lower(email) = lower($1)`,
			strings.TrimSpace(in.Email)).Scan(&m.ID, &m.Email, &m.Name, &hash)
		if errors.Is(err, pgx.ErrNoRows) {
			// Compare anyway so an unknown email takes as long as a wrong password.
			bcrypt.CompareHashAndPassword(dummyHash(), []byte(in.Password))
			writeError(w, http.StatusUnauthorized, errInvalidLogin)
			return
		}
		if err != nil {
			serverError(w, "marketer lookup", err)
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
			writeError(w, http.StatusUnauthorized, errInvalidLogin)
			return
		}

		token := rand.Text()
		if _, err := pool.Exec(r.Context(), `INSERT INTO sessions (token_hash, marketer_id, expires_at)
			VALUES ($1, $2, now() + make_interval(secs => $3))`, hashToken(token), m.ID, sessionTTL.Seconds()); err != nil {
			serverError(w, "create session", err)
			return
		}
		setCookie(w, token, int(sessionTTL.Seconds()))
		writeJSON(w, http.StatusOK, m)
	}
}

func logout(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(CookieName) // present: Require already checked it
		if _, err := pool.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, hashToken(c.Value)); err != nil {
			serverError(w, "delete session", err)
			return
		}
		setCookie(w, "", -1)
		w.WriteHeader(http.StatusNoContent)
	}
}

type marketer struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

func me(pool *pgxpool.Pool, llmMode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := MarketerID(r.Context())
		var out struct {
			marketer
			LLMMode string `json:"llm_mode"` // drives the "Mock AI" banner (D24)
		}
		out.LLMMode = llmMode
		if err := pool.QueryRow(r.Context(), `SELECT id, email, name FROM marketers WHERE id = $1`, id).
			Scan(&out.ID, &out.Email, &out.Name); err != nil {
			serverError(w, "load marketer", err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// setCookie writes the session cookie; maxAge < 0 deletes it.
func setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// shortcut: not Secure because the demo runs on plain http://localhost; set Secure behind HTTPS.
	})
}

// hashToken is what the database stores: never the token itself.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// dummyHash has the same cost as real password hashes (bcrypt.DefaultCost).
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("no-such-user"), bcrypt.DefaultCost)
	return h
})

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
