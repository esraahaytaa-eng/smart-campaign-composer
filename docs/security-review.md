# Security review (T19 step 4)

Reviewed 2026-10-23 against the T19 checklist: customer data, authorization, API security and dependencies. Each item has its evidence (code or test) and its result. "Fixed" items were changed in step 4, each with a failing test first.

## Automated checks

| Check | Result |
|---|---|
| `govulncheck` (v1.1.4) over the Go module | No vulnerability in code the app calls. One advisory at module level (GO-2026-5932, `golang.org/x/crypto/openpgp`, unmaintained; no fix exists): the app only uses `bcrypt` from that module. |
| `npm audit` for `web/` and `e2e/`, including dev dependencies | 0 vulnerabilities |
| Secret and personal-data scan (`make scan`, D51) | Clean; runs before every commit and in CI |

## Customer data

| Item | Evidence | Result |
|---|---|---|
| Contacts masked whenever they leave the server | `internal/campaign/recipients.go` (`maskPhone`, `maskEmail`); `TestRecipients_MasksContacts` scans whole responses | OK (D47) |
| The status stream carries no customer fields | `statusSnapshot` in `internal/campaign/events.go`: counts and reason codes only | OK |
| No customer data sent to the AI | `llm.InterpretInput` and `llm.ContentInput` (`internal/llm/llm.go`) have no field for user records: brief, clock, zone, catalog, channels | OK (D15), by construction |
| Nothing personal in logs | Every `slog` call logs IDs, counts or error values; pgx errors carry the message, not row values (the detail with values is a separate field) | OK |
| The provider's database role sees only its own schema | `TestProviderRole_CannotReadAppTables` | OK (D6) |
| Customer data never cached by the browser | `Cache-Control: no-store` on every API response (`auth.secure`); `TestAPI_SecurityHeaders` | **Fixed** (D56) |

## Authorization

| Item | Evidence | Result |
|---|---|---|
| Every API route needs a session | `TestEveryAPIRouteRequiresLogin` reads the route patterns from the source and calls each without a session | OK; the test is new, so a future route is covered automatically |
| Unknown campaign IDs give 404 without other campaigns' data | `TestGet` (unknown IDs on the campaign routes), `TestEvents_UnknownCampaign404`, `TestRecipients_RejectsBadInput` | OK |
| Health and metrics open but without customer data | `internal/obs/obs.go`: health is a database ping; metrics are labelled by route pattern, never by ID | OK (D10) |
| One role, all marketers see all campaigns | D10, confirmed 2026-10-23 | By design |

## API security

| Item | Evidence | Result |
|---|---|---|
| Sessions | Random token (`crypto/rand`), stored only as a SHA-256 hash, new token at every sign-in (no fixation), `HttpOnly`, `SameSite=Lax`, fixed 12 h lifetime, deleted on logout | OK |
| Session cookie `Secure` | Set whenever the request came over HTTPS (directly or `X-Forwarded-Proto: https`); `TestLogin_SecureCookieOverHTTPS` | **Fixed** (was never Secure) |
| Failed sign-ins | 5 per 15 minutes per email and per client address, then `429` with `Retry-After`, checked before the password; `TestThrottle_*`, `TestLogin_Throttled*` | **Fixed** (D55) |
| Cross-site requests (CSRF) | `SameSite=Lax`, plus the server refuses state-changing requests whose `Origin` is another site, including sign-in; `TestAPI_RejectsCrossSiteWrites` | **Fixed** (second layer added) |
| Security headers on the app | Strict Content Security Policy (`default-src 'self'`, no inline scripts or styles, `frame-ancestors 'none'`), `nosniff`, `Referrer-Policy: no-referrer`; the browser suite fails on any CSP violation, proven with a planted inline style | **Fixed** (D56) |
| XSS | No `v-html` or `innerHTML`; AI output, briefs and server texts are rendered as text; the preview is escaped | OK |
| SQL | User values reach Postgres only as parameters; every SQL fragment joined into a query is a constant, a builder output with its own parameters, or a validated integer | OK |
| Input limits | Bodies capped (64 KB, 4 KB for sign-in), rules, briefs, search and paging validated | OK |
| Error responses | Unexpected errors answer `internal error`; details only in the server log | OK |
| Slow clients | Headers must arrive within 10 s; idle keep-alive connections close after 2 minutes | **Fixed** (idle timeout added) |
| Container | Runs as an unprivileged user (`scc`, uid 10001) | OK |
| Demo credentials | Local only, documented, overridable in `.env` | By design (D10) |

## Remaining risks (documented, not fixed)

- **Sign-in limits are per api process and in memory.** They reset on restart. Behind a proxy all clients share its address, unless the proxy's forwarded address is trusted.
- **No overall read timeout.** Go's `ReadTimeout` would also cut the streaming responses (live status, AI writing). Request bodies are size-capped instead.
- **AI calls are not rate-limited** (README, Known limitations): a cost risk in real mode, not a data risk.
- ~~**Expired sessions stay in the database**~~ Fixed in step 7.5: every sign-in removes expired sessions (`TestLogin_RemovesExpiredSessions`); they could never be used (`expires_at` is checked).
