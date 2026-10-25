# Smart Campaign Composer

AI-assisted SMS and email campaign tool. A marketer writes a brief, Claude turns it into audience rules and writes the messages, the backend validates everything, the marketer reviews and approves, and a worker sends to mock providers while the dashboard shows delivery live.

> **Status: feature-complete for delivery.** The core workflow, scheduled sending, messages in each customer's language, a delivery-only A/B test, CI and an OpenAPI reference are built. Manual QA: 77 of 78 cases passed; the one bug found is fixed ([`docs/qa/`](docs/qa/2026-10-24-manual-qa/README.md)).

| Document | What it holds |
|---|---|
| [`CASE.md`](CASE.md) | Requirements and business rules |
| [`DECISIONS.md`](DECISIONS.md) | Every approved decision (`D1`…) with its reasons and trade-offs |
| `http://localhost:8080/docs` | API reference (OpenAPI 3.1, Swagger UI); spec at `/docs/openapi.json` |
| [`docs/security-review.md`](docs/security-review.md) | Security review and its evidence |
| [`docs/performance.md`](docs/performance.md) | Performance targets, method and results |
| [`docs/qa/`](docs/qa/2026-10-24-manual-qa/README.md) | Manual QA cases, results and screenshots |
| [`docs/audit/`](docs/audit/2026-10-24-architecture-audit.md), [`docs/plans/`](docs/plans/) | Architecture audit and implementation plans |
| [`design/`](design/smart-campaign-composer-reference.html) | UI reference design (open the HTML in a browser) |
| [`ai-sessions/`](ai-sessions/) | Claude Code session exports |

## Setup

Requirements: Docker with Compose, and `make`. No local Go or Node toolchain is needed.

```sh
docker compose up
```

No `.env` is needed. To change settings, `cp .env.example .env` and edit it (every setting is documented there).

- `http://localhost:8080`: the app
- `http://localhost:8080/docs`: the API reference
- `http://localhost:8080/healthz`, `/readyz`, `/metrics`: liveness, database readiness, Prometheus metrics

The first run creates 3,000 fictional customers from a fixed seed, with dates relative to that run; later runs never add or change them. Relative audiences ("carted in the last 30 days") shrink as time passes; `docker compose down -v && docker compose up` regenerates the data for today and deletes all campaigns.

### Demo login

| Email | Password |
|---|---|
| `marketer@marketer.com` | `Demo1234!` |

For local use only. `DEMO_MARKETER_PASSWORD` in `.env` changes it, but only before the first `docker compose up`.

```sh
curl -c cookies.txt -X POST localhost:8080/api/login -d '{"email":"marketer@marketer.com","password":"Demo1234!"}'
curl -b cookies.txt localhost:8080/api/me
```

### AI mode

`LLM_MODE=mock` (the default) needs no API key, but understands only the briefs below and close variations (other cities, numbers, categories, amounts, dates). Anything else is answered **unsupported**: the AI never guesses or broadens an audience (D15). A brief may end with a tone sentence ("Keep the message short and friendly.") and a send-time sentence ("Send it tomorrow at 10:00.", "Send it in 2 hours.").

| English | Turkish |
|---|---|
| Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything. Keep the message short and friendly. | İstanbul'da son 30 günde sepetine ürün ekleyip satın almayan kullanıcılara %30 indirim hatırlatması gönder. Mesaj kısa ve samimi olsun. |
| Send a we-miss-you coupon to customers whose last order was more than 90 days ago. | Son siparişi 90 günden eski olan müşterilere seni özledik kuponu gönder. |
| Send a VIP thank-you gift to customers who spent at least 10,000 TL in total. | Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür hediyesi gönder. |
| Send a new arrivals note to customers who bought books in the last 30 days. | Son 30 günde kitap kategorisinden alışveriş yapan müşterilere yeni ürün duyurusu gönder. |
| Send a free shipping offer to users with at least 5,000 TL in their cart. | Sepetinde en az 5.000 TL ürün olan kullanıcılara ücretsiz kargo fırsatı gönder. |
| Send a welcome discount to users who signed up in the last 7 days. | Son 7 günde üye olan kullanıcılara hoş geldin indirimi gönder. |
| Send a loyalty reward to customers who spent more than 10,000 TL in the last 12 months with at least 4 orders in that period, no orders in the last 45 days, and who did not sign up in the last 7 days. | Son 12 ayda 10.000 TL'den fazla harcayan, bu sürede en az 4 sipariş veren, son 45 gündür sipariş vermeyen ve son 7 günde üye olmamış müşterilere sadakat ödülü gönder. |
| Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00. | Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. Yarın saat 10:00'da gönder. |
| Send a reminder to users who added products to their cart since 2026-10-01 but did not buy anything. | 2026-10-01 tarihinden beri sepetine ürün ekleyip satın almayan kullanıcılara hatırlatma gönder. |

`LLM_MODE=real` with `ANTHROPIC_API_KEY` in `.env` uses Claude for free-form briefs (the api refuses to start without the key). Tests never call Claude. Each call costs credits: `LLM_TIMEOUT` (default `60s`) limits each call, and each marketer gets at most `AI_CALLS_PER_HOUR` (default 10) calls, retries included (D63).

## Features

- **Brief → audience:** the AI maps the brief to rules from a fixed catalog (city, cart and order history, spend, categories, sign-up date; D14, D70), or says it can't. Rules are validated and shown as editable chips with live counts per channel; they can also be built by hand.
- **Content:** the AI streams SMS and email text per language (Turkish, English) into editors; text is personalised with `{{first_name}}`, `{{last_name}}`, `{{city}}` and length-checked after personalisation (SMS 160, subject 80, email body 2,000). Each language is reviewed and approved on its own (D64–D67). Edits save automatically with optimistic locking.
- **Approval:** nothing is sent without an explicit *Send now* or *Schedule* (1 minute to 30 days ahead) and a second confirmation; estimates are labelled as estimates (D30).
- **Sending:** the worker builds the audience at send time, re-checks consent and cancellation before each request, sends with an idempotency key, retries 429/5xx/timeouts with backoff, and stays within 100 requests per second per channel (D19, D69). Over-limit messages are skipped and can be resent (D31).
- **Monitoring:** a live status stream per campaign (sent, uncertain, failed, skipped, cancelled, retrying, pending, with reasons), recipient details with masked contacts (D46, D47), cancel and resend.
- **A/B test (optional):** version B per language, a reproducible 50/50 split, delivery results per version; no winner is named, since no outcome data exists (D68).
- **Dashboard and list:** status counts, server-side search, filters and paging (D54). English and Turkish UI (D44).

## Architecture

One Go module (`cmd/scc`) built into one image and run as Compose services (D20): `postgres`, `migrate` (one-shot migrations and seed), `api`, `worker` and `mock-provider`. The Vue 3 + TypeScript frontend (`web/`) is built into the same image and served by the api.

| Package | Responsibility |
|---|---|
| `internal/auth` | Login, logout, session cookie check, sign-in throttling, same-origin check on writes |
| `internal/campaign` | The campaign API: drafts, content, approval, estimates, live status, recipients, resend |
| `internal/audience` | Rule catalog and validation; rules become parameterized SQL (values only ever reach Postgres as arguments) |
| `internal/content` | Message templates, length limits, over-limit estimates, language catalog |
| `internal/llm` | Claude client and the mock AI; answers are validated against a schema, never trusted |
| `internal/worker` | Queue (`FOR UPDATE SKIP LOCKED` with leases, D5, D18), rate limits, dispatch; one sender via an advisory lock |
| `internal/provider` | Mock SMS and email provider with delays, failures, 429s and idempotency keys (D20); its own database role (D6) |
| `internal/db` | Embedded migrations and the seed |
| `internal/apidocs` | The OpenAPI spec and Swagger UI page (D73) |
| `internal/obs`, `internal/spa`, `internal/config` | Health and metrics, serving the frontend, environment settings |

The provider's failure rates and delays are set with the `SMS_*` / `EMAIL_*` settings in `.env.example` (defaults: 20–200 ms, 5% 5xx, 3% 429, 1% lost responses).

## Testing

```sh
make test         # in Docker: Go tests on a throwaway Postgres, Vitest and the TypeScript check alongside
make lint         # in Docker: gofmt and go vet
make e2e          # Playwright browser tests on their own throwaway stack (D53)
make perf         # performance targets on their own stack (D57, docs/performance.md)
make scan         # gitleaks: secrets and personal data in files and unpushed commits (D51)
make hooks        # once per clone: scan staged changes before every commit
```

Integration tests need a database and fail, never skip, without one. `internal/e2e` runs the api, worker and mock provider together, including provider failures, crashes, cancellations and resends. A test checks that every API route is in the OpenAPI spec.

**CI** (GitHub Actions, D52): `ci.yml` runs `make test`, `make lint` and the scan on every push and pull request; `e2e.yml` runs `make e2e` on pull requests, pushes to `main` and nightly.

Frontend development with hot reload, against the api from `docker compose up`:

```sh
docker run --rm -it -p 5173:5173 -v "$PWD/web":/web -v scc-web-node-modules:/web/node_modules -w /web \
  -e API_URL=http://host.docker.internal:8080 node:22-alpine sh -c "npm ci && npx vite --host 0.0.0.0"
```

UI text lives in `web/src/i18n/en.ts` and `tr.ts` (the type check fails if a key is missing in one); `make test` flags phrases not passed through `t()`.

## Security

Details in [`docs/security-review.md`](docs/security-review.md). Contacts are masked by the server and never cached; the AI never receives customer data; every API route except login needs a session (HttpOnly, SameSite=Lax cookie; only the token's hash is stored); failed sign-ins are limited; writes from other sites are refused; the app has a strict Content Security Policy; secrets and personal data are scanned before every commit and in CI. The session cookie isn't `Secure` on plain `http://localhost`.

## Known limitations

- **Not built:** the final UI/UX audit and the product roadmap planned for T21 (D50); AI-generated campaign titles (D43); archiving campaigns (D37); A/B outcomes such as orders or clicks (D68).
- **`/docs`** loads Swagger UI from the jsDelivr CDN (pinned, integrity-checked), so it needs internet access in the browser; the spec at `/docs/openapi.json` works offline. The spec is written by hand: a test keeps its routes in step with the server, not every field.
- **Per-process state:** sign-in limits and the AI call limit live in memory per api process and reset on restart; behind a proxy, all clients share its address. Metrics count per process.
- **Status streams** share one snapshot per campaign (D59), but nothing limits how many are open. Without a running worker, a cancelled campaign's stream stays open until in-flight messages are reclaimed. A resend created in another tab after a stream ended shows on reload (D36).
- **Recipient details** page by offset, so rows can move while a campaign sends (D47). Under back-to-back load they measured 55.4 ms p95 against a 50 ms target (`docs/performance.md`, Q6).
- **A/B:** delivery data only; version B's over-limit estimate covers the whole audience, so it is an upper bound (D68).
- **Frontend:** the Inter font isn't bundled; there is no frontend linter beyond the TypeScript check.
