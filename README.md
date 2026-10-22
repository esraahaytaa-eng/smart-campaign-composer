# Smart Campaign Composer

AI-assisted SMS and email campaign tool: Claude suggests the audience and content, the backend validates, the marketer approves, the system sends to mock providers. Requirements: [`CASE.md`](CASE.md). Decisions: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/2026-10-21-implementation-plan.md`](docs/plans/2026-10-21-implementation-plan.md).

> **Status: work in progress.** Done: project skeleton (T1), database schema with fictional seed data (T2), login API (T3), audience rules (T4), AI brief interpretation with a mock and Claude (T5), campaign draft API (T6). Not yet built: content generation, approval, worker, mock providers, frontend.

## Requirements

Docker with Compose, and `make`. No local Go or Node toolchain is needed.

## Run

```sh
docker compose up
```

No `.env` is needed. To change settings, `cp .env.example .env` and edit it.

- `http://localhost:8080/healthz`: liveness
- `http://localhost:8080/readyz`: database reachable (503 otherwise)
- `http://localhost:8080/metrics`: Prometheus metrics

`LLM_MODE=mock` is the default. With `LLM_MODE=real`, `ANTHROPIC_API_KEY` must be set in `.env`, otherwise the api exits on startup.

### Demo login

| Email | Password |
|---|---|
| `marketer@marketer.com` | `Demo1234!` |

For local use only. The password can be changed with `DEMO_MARKETER_PASSWORD` in `.env`, but only before the first `docker compose up`: the seed runs once and never changes existing rows. The login page arrives with the frontend (T15); until then, use the API:

```sh
curl -c cookies.txt -X POST localhost:8080/api/login \
  -d '{"email":"marketer@marketer.com","password":"Demo1234!"}'
curl -b cookies.txt localhost:8080/api/me
```

Every `/api/*` endpoint except `POST /api/login` returns 401 without a session. Sessions last 12 hours and live in an HttpOnly, SameSite=Lax cookie; the database stores only the token's SHA-256 hash. The cookie is not marked `Secure` because the demo runs on plain `http://localhost`.

## Campaign draft API

All endpoints need a login session (see above).

| Endpoint | Purpose |
|---|---|
| `POST /api/campaigns` `{"request", "channels", "time_zone"}` | The AI reads the brief and a **draft** is saved with its audience rules, current per-channel counts, and a proposed send time if the brief named one (only a proposal; nothing is scheduled). Add `"rules": [...]` to build the audience by hand without the AI. |
| `PUT /api/campaigns/{id}/rules` `{"rules", "version"}` | Replace the draft's rules; they are validated and recounted. |
| `PUT /api/campaigns/{id}/schedule` `{"send_at", "version"}` | Change or clear (`null`) the proposed send time: RFC 3339 with offset, between now and 30 days ahead. |
| `GET /api/campaigns`, `GET /api/campaigns/{id}` | List (newest 200) / one campaign; drafts include current counts. |

`channels` is `["sms"]`, `["email"]` or both; `time_zone` is the browser's IANA zone (e.g. `Europe/Istanbul`). Edits send the `version` they were based on; a stale version or a non-draft campaign returns 409. Invalid input returns 422 (with `field_errors` for rules); an "unsupported" brief returns 422 with `"unsupported": true` and the AI's reason, and nothing is saved. If the AI answers invalidly twice, times out or is unavailable, the API returns 502/504 and nothing is saved.

```sh
curl -b cookies.txt -X POST localhost:8080/api/campaigns -d '{"request":"Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.","channels":["sms"],"time_zone":"Europe/Istanbul"}'
```

### Trying the real AI (manual check)

Tests never call Claude. To check real mode by hand: set `LLM_MODE=real` and `ANTHROPIC_API_KEY` in `.env`, run `docker compose up --build`, log in as above, and post a free-form brief (e.g. "Send a thank-you note to customers in Izmir who spent at least 2,000 TL"). Expect a draft with matching rules, or `"unsupported": true` with a reason for a brief the rule catalog can't express (e.g. one that mentions age). `LLM_TIMEOUT` (default `60s`) limits each AI call. Each call costs real API credits.

## Mock AI briefs

With `LLM_MODE=mock` (the default) no API key is needed, but the mock AI only understands the briefs below and close variations of them (other cities, numbers, categories, amounts, dates and offers). Anything else, including one of these briefs with an extra condition, is answered as **unsupported**: the mock never guesses or broadens an audience (D15). For free-form briefs, use `LLM_MODE=real`.

A brief may end with a tone sentence ("Keep the message short and friendly." / "Mesaj kısa ve samimi olsun.") and one send-time sentence ("Send it tomorrow at 10:00.", "Send it today at 18:30.", "Send it in 2 hours." / "Yarın saat 10:00'da gönder.", "3 saat sonra gönder.").

| English | Turkish |
|---|---|
| Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything. Keep the message short and friendly. | İstanbul'da son 30 günde sepetine ürün ekleyip satın almayan kullanıcılara %30 indirim hatırlatması gönder. Mesaj kısa ve samimi olsun. |
| Send a we-miss-you coupon to customers whose last order was more than 90 days ago. | Son siparişi 90 günden eski olan müşterilere seni özledik kuponu gönder. |
| Send a VIP thank-you gift to customers who spent at least 10,000 TL in total. | Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür hediyesi gönder. |
| Send a new arrivals note to customers who bought books in the last 30 days. | Son 30 günde kitap kategorisinden alışveriş yapan müşterilere yeni ürün duyurusu gönder. |
| Send a free shipping offer to users with at least 5,000 TL in their cart. | Sepetinde en az 5.000 TL ürün olan kullanıcılara ücretsiz kargo fırsatı gönder. |
| Send a welcome discount to users who signed up in the last 7 days. | Son 7 günde üye olan kullanıcılara hoş geldin indirimi gönder. |
| Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00. | Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. Yarın saat 10:00'da gönder. |
| Send a reminder to users who added products to their cart since 2026-10-01 but did not buy anything. | 2026-10-01 tarihinden beri sepetine ürün ekleyip satın almayan kullanıcılara hatırlatma gönder. |

The real AI (`LLM_MODE=real`) follows the same rule: if part of an audience cannot be expressed with the rule catalog, it answers "unsupported" instead of guessing.

## Seed data

The `migrate` service applies the schema and, on the first run, creates 3,000 fictional users with order and cart histories, generated from a fixed random seed. Every date is relative to that first run. Later runs do nothing, so restarting never creates duplicates or changes data.

Relative audiences such as "carted in the last 30 days" shrink as real time passes. To regenerate the data relative to today (this also deletes all campaigns):

```sh
docker compose down -v && docker compose up
```

## Test

```sh
make test         # in Docker: throwaway Postgres + Go tests
make test-local   # needs local Go and DATABASE_URL pointing to a Postgres
make lint         # in Docker: gofmt check + go vet
```

Integration tests fail, never skip, when no database is available.

## Architecture (current)

One Go module (`cmd/scc`) built into one image and run as separate Compose services (D20): `postgres`, `migrate` (one-shot: migrations + seed), `api`. `internal/config` reads environment variables, `internal/obs` serves health and metrics, `internal/db` holds the embedded migrations and the seed, `internal/auth` the login, logout and session check. `internal/audience` validates audience rules against a fixed catalog (D14) and turns them into parameterized SQL; rule values only ever reach Postgres as query arguments.
