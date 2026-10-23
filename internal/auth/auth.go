// Package auth handles marketer login, logout and session checks (D10, D22).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName = "scc_session"
	// Fixed lifetime from login, no sliding renewal (D10).
	sessionTTL   = 12 * time.Hour
	maxLoginBody = 4 << 10
)

const errInvalidLogin = "invalid email or password"

// Failed sign-ins allowed per email and per client address (D55).
const (
	maxFailures   = 5
	failureWindow = 15 * time.Minute
)

// Routes registers POST /api/login and guards every other /api/ path with
// Require. It returns the guarded mux, where later tasks add their routes.
func Routes(mux *http.ServeMux, pool *pgxpool.Pool, llmMode string) *http.ServeMux {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/me", me(pool, llmMode))
	api.HandleFunc("POST /api/logout", logout(pool))

	mux.Handle("POST /api/login", secure(login(pool, newThrottle(maxFailures, failureWindow, time.Now))))
	mux.Handle("/api/", secure(Require(pool, api)))
	return api
}

// secure adds the API's security headers (D56) and refuses writes from other
// sites' pages: SameSite=Lax already keeps the cookie off them; this is a
// second layer. Browsers always send Origin on POST, PUT and DELETE; clients
// without one (curl, scripts) are not browsers and carry no ambient cookie.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store") // answers can hold customer data
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the request's Origin, if any, is this host.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
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
		inner := r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
		next.ServeHTTP(w, inner)
		r.Pattern = inner.Pattern // the guarded route, for the metrics middleware outside (obs.Instrument)
	})
}

func login(pool *pgxpool.Pool, th *throttle) http.HandlerFunc {
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
		// Checked before the password, so a locked-out guesser learns nothing.
		emailKey, addrKey := "email:"+strings.ToLower(strings.TrimSpace(in.Email)), "addr:"+clientAddr(r)
		for _, k := range []string{emailKey, addrKey} {
			if ok, wait := th.allowed(k); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				writeError(w, http.StatusTooManyRequests, "too many failed sign-ins; try again later")
				return
			}
		}
		fail := func() {
			th.failed(emailKey)
			th.failed(addrKey)
			writeError(w, http.StatusUnauthorized, errInvalidLogin)
		}

		var m marketer
		var hash string
		err := pool.QueryRow(r.Context(), `SELECT id, email, name, password_hash FROM marketers WHERE lower(email) = lower($1)`,
			strings.TrimSpace(in.Email)).Scan(&m.ID, &m.Email, &m.Name, &hash)
		if errors.Is(err, pgx.ErrNoRows) {
			// Compare anyway so an unknown email takes as long as a wrong password.
			bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
			fail()
			return
		}
		if err != nil {
			serverError(w, "marketer lookup", err)
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
			fail()
			return
		}
		th.reset(emailKey)
		th.reset(addrKey)

		token := rand.Text()
		// Expired sessions can't be used; removing them here keeps the table small.
		if _, err := pool.Exec(r.Context(), `WITH gone AS (DELETE FROM sessions WHERE expires_at <= now())
			INSERT INTO sessions (token_hash, marketer_id, expires_at)
			VALUES ($1, $2, now() + make_interval(secs => $3))`, hashToken(token), m.ID, sessionTTL.Seconds()); err != nil {
			serverError(w, "create session", err)
			return
		}
		setCookie(w, r, token, int(sessionTTL.Seconds()))
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
		setCookie(w, r, "", -1)
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

// setCookie writes the session cookie; maxAge < 0 deletes it. It is Secure
// when the request came over HTTPS (directly or through a proxy that says so);
// the local demo runs on plain http://localhost, where a Secure cookie would
// never be sent back.
func setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

// clientAddr is the connection's address, without the port. Forwarding
// headers are not trusted: anyone could set them to dodge the limit.
// shortcut: behind a proxy every client shares its address (and its limit);
// trust the proxy's X-Forwarded-For there.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// hashToken is what the database stores: never the token itself.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// dummyHash has the same cost as real password hashes (bcrypt.DefaultCost).
// Built at startup, so even the first unknown-email login takes normal time.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no-such-user"), bcrypt.DefaultCost)

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
