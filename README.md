# Smart Campaign Composer

A marketer describes a campaign in plain Turkish or English. Claude turns the brief into audience rules and writes SMS and email messages. The Go backend validates every AI answer, the marketer reviews, edits and explicitly approves, and a queue sends to mock SMS and email providers while the dashboard shows delivery live. Frontend: Vue 3 + TypeScript. Backend: Go. Storage and queue: Postgres.

**Status:** every mandatory requirement in [`CASE.md`](CASE.md) and all four optional features are built (scope and limits below). Manual QA: 77 of 78 cases passed; the one bug found is fixed. CI runs the tests, lint, a secret scan and the browser suite.

## Setup

Requirements: Docker with Compose, and `make`. No local Go or Node toolchain is needed.

**Option A: mock AI, no API key.** This is the default and needs no `.env`:

```sh
docker compose up
```

**Option B: real Claude.** Copy the example settings, set the mode and key, then start (or restart) the stack:

```sh
cp .env.example .env
# in .env: LLM_MODE=real and ANTHROPIC_API_KEY=<your key>
docker compose up
```

With `LLM_MODE=real` the api refuses to start without `ANTHROPIC_API_KEY`. `ANTHROPIC_MODEL` (default `claude-opus-5-5`), `LLM_TIMEOUT` (default `60s` per call) and `AI_CALLS_PER_HOUR` (default 10 per marketer, retries included, D63) control cost. [`.env.example`](.env.example) documents every setting the stack reads, including the mock provider's failure rates and the worker's retry limits.

Then open:

| URL | |
|---|---|
| `http://localhost:8080` | The app. Demo login: `marketer@marketer.com` / `Demo1234!` (local only; `DEMO_MARKETER_PASSWORD` changes it before the first start) |
| `http://localhost:8080/docs` | API reference: OpenAPI 3.1 in Swagger UI; the spec is at `/docs/openapi.json` |
| `/healthz`, `/readyz`, `/metrics` | Liveness, database readiness, Prometheus metrics |

**The database sets itself up.** `docker compose up` runs the one-shot `migrate` service before the api and worker start. It applies the migrations, then seeds the demo login and 3,000 fictional customers in one transaction, so no manual seed command is needed. The seed is generated from a fixed random seed (`20261021`), so every fresh start produces the same data:

| Seeded | Count |
|---|---|
| Customers (names, email, phone) | 3,000 across 16 cities |
| Language | 2,672 Turkish, 328 English |
| Channel permissions | 2,096 SMS, 2,095 email (1,650 both, 459 neither) |
| Orders | 6,496 from 1,658 customers, in 6 product categories |
| Cart items | 2,608 from 1,487 customers |

A marker row makes the seed run once: restarts never add, change or duplicate customers (D28). Dates are relative to the first start, so "carted in the last 30 days" shrinks as time passes; `docker compose down -v && docker compose up` reseeds for today and deletes all campaigns.

## CASE requirements

| Requirement | How it is met |
|---|---|
| Claude creates audience rules; the backend validates them; never run Claude's SQL | Claude returns rules as JSON against a fixed catalog (D14). Go validates field, operator and value, and builds parameterized SQL itself; rule values reach Postgres only as query arguments. |
| Show matching users; SMS and email permissions checked separately | Drafts show the audience per channel, counted from `sms_opt_in` / `email_opt_in`. The worker re-checks the recipient's permission for that channel before every send. |
| Claude writes SMS and email, streamed to the UI | Server-sent events stream the text into the editors as it is written (D21). |
| SMS ≤ 160, subject ≤ 80, variables, preview and edit before approval | Limits are checked after personalization with `{{first_name}}`, `{{last_name}}`, `{{city}}`; recipients over the limit are skipped and counted. The marketer edits, previews and approves each language. |
| Asynchronous queue, ≤ 100 messages/s per channel | The `messages` table is the queue, claimed with `FOR UPDATE SKIP LOCKED` and leases (D5, D18). Per channel, a rate limiter plus a hard cap over each 1-second window, both counting retries (D19, D69). |
| No duplicate messages | One message per campaign, user and channel (unique constraint). The message ID is the provider's idempotency key, so a retry after a lost response is not sent twice. |
| Retry strategy | 429, 5xx, timeouts and network errors are retried with exponential backoff and jitter, honouring `Retry-After`, up to `MAX_ATTEMPTS` (5); other 4xx fail. Each attempt is recorded. |
| Mock SMS and email providers with success, delays, temporary errors and 429 | The `mock-provider` service: 20–200 ms delay, 5% 5xx, 3% 429, 1% lost responses by default, configurable per channel (D20). |
| Live dashboard status, metrics, health checks | A per-campaign status stream over SSE (D21, D36); `/metrics` (D26), `/healthz` and `/readyz` on every process. |
| Structured AI JSON, validated, invalid output handled safely | See [Reliable AI design](#reliable-ai-design). |
| Tests without an API key; key from `ANTHROPIC_API_KEY` in `.env`; `.env.example` | Every test uses the mock LLM or a fake local API server. The key is read only from the environment. |
| 3,000 fictional users with names, email, phone, city, orders, cart, permissions | Seeded automatically by the `migrate` service on the first `docker compose up`; restarts never duplicate them (D28). See [Setup](#setup) for the counts. |
| Vue.js frontend, Go backend | Vue 3 + TypeScript + Vite (D25); one Go module (D20). |
| Deliverables: `docker compose up`, `make test`, README, `DECISIONS.md`, `ai-sessions/` | All present. Session exports are reviewed for keys and personal data before they are committed (D51). |

## Bonus features

- **Scheduled sending (D29):** approval requires an explicit *Send now* or a time between 1 minute and 30 days ahead. A time from the brief is only a proposal. The worker starts the campaign at that time and builds the audience then (D30), and it can be cancelled until then.
- **Messages in the customer's language (D64–D67):** **Turkish and English are implemented.** Each customer gets the text in their own language if the campaign has it, and the default language otherwise. Each language is reviewed and approved on its own; a changed brief marks the text "may be outdated". The language list is one catalog in `internal/content/languages.go` that drives the API, validation, the AI's answer schema and the UI. A further language would still need a migration widening the database's language checks, seed data and mock texts; a test fails while the catalog and the database checks disagree. No other language works today.
- **A/B test (D68):** optional and off by default. Version B per language is written by hand or by the AI. Each language's recipients are split 50/50 by `md5(campaign:user)`, which is deterministic and the same on every channel, and the version is stored on each message. Results show delivery per version (recipients, sent, failed, skipped, too long, waiting or retrying). There are **no clicks, conversions, statistical significance or winner selection**: the app records no outcomes after delivery.
- **CI (D52):** GitHub Actions runs `make test`, `make lint` and the secret scan on every push and pull request, and the browser suite on pull requests, on pushes to `main` and nightly. Actions are pinned to commits.

## Example briefs

**Mock mode understands only the briefs below** and close variations (other cities, numbers, dates, categories, offers). Each may end with a tone sentence and one send-time sentence ("Send it tomorrow at 10:00.", "Send it in 2 hours."). Any other brief, including one of these with an extra condition, is answered **unsupported**: the mock never guesses or broadens an audience (D15). A test keeps this table in step with the mock.

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

**Free-form briefs need real Claude.** For example:

> Son 12 ayda toplam 10.000 TL'den fazla harcama yapmış, en az 4 sipariş vermiş ancak son 45 gündür sipariş oluşturmamış müşterileri hedeflemek istiyorum. Son 7 gün içinde kayıt olanlar bu kampanyaya dahil edilmesin. Bu müşterilere %25 geri dönüş indirimi sunalım. SMS ve e-posta kullanılsın. Türkçe ve İngilizce içerikler profesyonel ama sıcak olsun.

Mock mode answers this brief as unsupported (checked). With real Claude this segmentation was tested manually in the app and produced the intended four rules: `spend_in_window gt 10,000 / 12 months`, `orders_in_window gte 4 / 12 months`, `no_order within 45 days` and `signed_up before 7 days`. The controlled real-Claude call for the same segment during D70 (`ai-sessions/08`) gave the same four rules.

What has been verified with real Claude:

| Case | Result |
|---|---|
| The Turkish segmentation brief above (manual test in the app; controlled call in D70) | The four intended rules; nothing dropped or approximated |
| A realistic Turkish brief: İstanbul, cart abandoned in 30 days, send Friday 18:00, 15% off (QA AI1) | City and cart rules, the send time proposed in the campaign's zone, the discount kept for the content |
| Turkish and English messages for that campaign (QA AI2) | Both greet with `{{first_name}}`; SMS within 160 characters |
| A prompt-injection brief asking to ignore the rules and target everyone (QA AI3) | Unsupported; no campaign created, no rules invented |

Briefs that are **refused by design** (tested with the mock; real Claude follows the same rule in its prompt): an unknown attribute ("users in Istanbul who are older than 30"), a supported brief plus an extra condition ("… and who like hiking", "Only women."), and a vague one ("Send something nice to our best customers").

## Reliable AI design

- **Structured JSON:** both calls use Anthropic structured output with a JSON schema. Field and operator are fixed lists from the catalog; content has one object per requested language and channel.
- **Strict validation in Go:** the answer is decoded strictly (unknown fields and trailing data rejected). Rules are checked against the catalog and its limits and normalized. Content must cover exactly the requested languages and channels, use only known variables, and fit the length limits with every variable empty; per-recipient lengths are checked again at send time. AI-written bodies must greet with `{{first_name}}` (D72).
- **Safe SQL:** Claude never writes SQL. Go builds each rule's SQL from a fixed template, with values passed only as query arguments.
- **Controlled retries:** an invalid answer is retried **once**, with the validation errors sent back. A second failure returns `502 ai_invalid`; provider errors return `502 ai_unavailable`, timeouts `504`. Only a valid answer is saved, and streamed content only while the client is still connected (D15). Real calls are capped per marketer (D63).
- **Unsupported handling:** the prompt and the mock both answer `unsupported` with a reason when any part of the audience can't be expressed. The brief is treated as data inside `<brief>` tags, never as instructions. Nothing is saved, and the marketer can rephrase.
- **Mock LLM:** tests and the default mode use a deterministic mock, with the same interface and validation path as Claude. The Claude client is tested against a fake local API server.

## Architecture

One Go module (`cmd/scc`), one image, run as Compose services (D20): `postgres`, `migrate` (one-shot migrations and seed), `api` (also serves the built Vue app), `worker` and `mock-provider`.

| Package | Responsibility |
|---|---|
| `internal/auth` | Login, session cookie, sign-in throttling, same-origin check on writes |
| `internal/campaign` | Campaign API: drafts, content, approval, estimates, live status, recipients, resend |
| `internal/audience` | Rule catalog, validation, parameterized SQL |
| `internal/content` | Templates, length limits, over-limit estimates, language catalog |
| `internal/llm` | Claude client, mock AI, answer schemas |
| `internal/worker` | Queue claims with leases, rate limits, dispatch, retries; one sender via an advisory lock (D69) |
| `internal/provider` | Mock SMS and email provider with its own database role (D6) |
| `internal/db` | Embedded migrations and seed |
| `internal/apidocs` | OpenAPI spec and Swagger UI page (D73) |
| `internal/obs`, `internal/spa`, `internal/config` | Health and metrics, serving the frontend, settings |

## Testing

```sh
make test         # Go tests on a throwaway Postgres, with Vitest and the TypeScript check alongside (Docker)
make lint         # gofmt and go vet (Docker)
make e2e          # Playwright browser suite on its own throwaway stack (D53)
make perf         # performance targets on their own stack (D57)
make scan         # gitleaks: secrets and personal data in files and unpushed commits (D51)
make hooks        # once per clone: scan staged changes before every commit
```

- **Go:** integration tests run against Postgres and fail, never skip, without one. `internal/e2e` runs the api, worker and mock provider together: sends through a provider that fails, rate-limits and loses responses; scheduled sends; worker crashes and shutdowns; cancellation races; consent changes; length skips and resends; retry limits; two workers on one queue. A test checks that the OpenAPI spec lists exactly the registered routes.
- **Browser (`make e2e`, `e2e/`):** sign-in and the login guard; the audience, content and review steps (streaming and Stop, autosave races, edit conflicts, explicit approval); one-step creation; English and Turkish; dashboard, list, search and paging; phone, tablet and desktop layouts; live delivery to completion with counts checked against the API; pause, cancel while sending; recipient details; resend; the A/B test. Several checks really send campaigns through the mock providers.

Reports in `docs/`:

| Report | Contents |
|---|---|
| [Manual QA](docs/qa/2026-10-24-manual-qa/README.md) | 78 cases in a real browser, including 3 paid real-Claude calls; results, screenshots and the one bug (fixed) |
| [Architecture audit](docs/audit/2026-10-24-architecture-audit.md) | Independent audit of the delivery path and the code; the approved findings were fixed (D69) |
| [Security review](docs/security-review.md) | Customer data, authorization, API security and dependencies, each with evidence |
| [Performance](docs/performance.md) | Targets P1–P7, method and measured results; P5 is still missed back to back |

Frontend development with hot reload, against the api from `docker compose up`:

```sh
docker run --rm -it -p 5173:5173 -v "$PWD/web":/web -v scc-web-node-modules:/web/node_modules -w /web \
  -e API_URL=http://host.docker.internal:8080 node:22-alpine sh -c "npm ci && npx vite --host 0.0.0.0"
```

## Project documentation

- **[`DECISIONS.md`](DECISIONS.md):** 73 numbered decisions (`D1`…), split into requirements and business rules, and architecture and technical choices. Each records the decision, why, the rejected alternatives and the trade-offs. Changes over time are kept as dated history notes, and replaced decisions are marked *Superseded* with a pointer to the new one. Open questions and deferred items are listed at the end. README sections cite these IDs.
- **[`ai-sessions/`](ai-sessions/):** exports of the Claude Code sessions, from requirement analysis through delivery, reviewed for keys and personal data before commit.
- **[`docs/plans/`](docs/plans/):** implementation plans per milestone. **[`design/`](design/smart-campaign-composer-reference.html):** the reference UI design (open in a browser).

## Security

Details in [`docs/security-review.md`](docs/security-review.md). Contacts are masked by the server and never cached. The AI never receives customer data. Every API route except login needs a session (HttpOnly, SameSite=Lax cookie; only the token's hash is stored). Failed sign-ins are limited, writes from other sites are refused, and the app has a strict Content Security Policy. Secrets and personal data are scanned before every commit and in CI. The cookie isn't `Secure` on plain `http://localhost`.

## Known limitations

- **Not built:** the final UI/UX audit and the product roadmap planned for T21 (D50); AI-generated campaign titles (D43); archiving campaigns (D37). See the A/B and language scope above.
- **Mock AI** understands only the documented briefs; free-form briefs need real Claude.
- **`/docs`** loads Swagger UI from the jsDelivr CDN (pinned and integrity-checked), so it needs internet access; `/docs/openapi.json` works offline. A test keeps the spec's routes in step with the server, but not every field.
- **Per-process state:** sign-in limits and the AI call limit are in memory per api process and reset on restart. Behind a proxy, all clients share its address. Metrics count per process.
- **Status streams** share one snapshot per campaign (D59), but nothing limits how many are open.
- **Recipient details** page by offset, so rows can move while a campaign sends (D47). With 10 marketers back to back, P5 measured 55.4 ms p95 against a 50 ms target; with a 1 s pause it measured 45 ms in T19, not re-measured since.
- **A/B:** version B's over-limit estimate covers the whole audience, so it is an upper bound (D68).
- **Frontend:** the Inter font isn't bundled; there is no frontend linter beyond the TypeScript check.
