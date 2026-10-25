# Smart Campaign Composer

A marketer describes a campaign in plain Turkish or English. Claude turns the brief into audience rules and writes SMS and email messages. The Go backend validates every AI answer, the marketer reviews, edits and explicitly approves, and a queue sends to mock SMS and email providers while the dashboard shows delivery live. Frontend: Vue 3 + TypeScript. Backend: Go. Storage and queue: Postgres.

**Status:** every mandatory requirement in [`CASE.md`](CASE.md) and all four optional features are built (scope and limits below). Manual QA: 77 of 78 cases passed in the initial run; the one failing case, a low-severity bug, was then fixed with regression tests (PR #15). CI runs the tests, lint, a secret scan and the browser suite.

## Setup

Requirements: Docker with Compose, and `make`. No local Go or Node toolchain is needed.

**Option A: mock AI, no API key.** This is the default and needs no `.env`:

```sh
docker compose up --build
```

**Option B: real Claude.** Copy the example settings, set the mode and key, then start (or restart) the stack:

```sh
cp .env.example .env
# in .env: LLM_MODE=real and ANTHROPIC_API_KEY=<your key>
docker compose up --build
```

With `LLM_MODE=real` the api refuses to start without `ANTHROPIC_API_KEY`. `ANTHROPIC_MODEL` (default `claude-opus-5-5`), `LLM_TIMEOUT` (default `60s` per call) and `AI_CALLS_PER_HOUR` (default 10 per marketer, retries included, D63) control cost. [`.env.example`](.env.example) documents every setting the stack reads, including the mock provider's failure rates and the worker's retry limits.

Then open:

| URL | |
|---|---|
| `http://localhost:8080` | The app. Demo login: `marketer@marketer.com` / `Demo1234!` (local only; `DEMO_MARKETER_PASSWORD` changes it before the first start) |
| `http://localhost:8080/docs` | API reference: OpenAPI 3.1 in Swagger UI; the spec is at `/docs/openapi.json` |
| `/healthz`, `/readyz`, `/metrics` | Liveness, database readiness, Prometheus metrics |

**The database sets itself up.** `docker compose up --build` runs the one-shot `migrate` service before the api and worker start. It applies the migrations, then seeds the demo login and 3,000 fictional customers in one transaction, so no manual seed command is needed. The seed is generated from a fixed random seed (`20261021`), so every fresh start produces the same data:

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

## API reference and error handling

The full reference is at [`/docs`](http://localhost:8080/docs) (Swagger UI) and [`/docs/openapi.json`](http://localhost:8080/docs/openapi.json) (OpenAPI 3.1). Every `/api` route except `POST /api/login` needs the session cookie set by login. Successful calls answer `200`, `201` (campaign or resend created) or `204` (draft deleted, logged out). Errors are JSON: `{"error": "..."}`, with `code`, `field_errors`, `unsupported` or `retry_after_seconds` where noted.

| HTTP | `code` | Meaning | Expected behavior |
|---|---|---|---|
| 400 | — | The body isn't one JSON object of the documented shape: unknown fields, trailing data, or over 64 KiB (4 KiB for login). At login: email or password missing | Fix the request |
| 401 | — | No valid session (`login required`); at login, a wrong email or password (one answer for both) | Sign in again |
| 403 | — | A POST, PUT or DELETE whose `Origin` is another site | Call from the app's own origin |
| 404 | — | The campaign doesn't exist (or the id isn't a positive integer); the campaign has no text in that language | Reload the list |
| 409 | — | The draft changed since it was loaded (stale `version`); the status doesn't allow it (only drafts are edited, deleted or approved; only approved or sending campaigns are cancelled); the text exists and `replace` wasn't set; resend conflicts | Reload, or confirm the replacement |
| 422 | — | Invalid values: rules (`field_errors` with the rule's index), content (`field_errors` with the field, e.g. `en.sms.body`), channels, time zone, send time, query parameters, missing `version`, missing text or approvals, an empty audience | Correct the input |
| 422 | — | The AI can't express the brief with the rule catalog: `"unsupported": true` and the AI's reason. Nothing is saved | Rephrase the brief or build the rules by hand |
| 429 | — | Too many failed sign-ins: 5 per 15 minutes per email and per client address; `Retry-After` header | Wait, then retry |
| 429 | `ai_call_limit` | Real AI only: the marketer's `AI_CALLS_PER_HOUR` limit is reached; `Retry-After` and `retry_after_seconds` | Wait; editing by hand still works |
| 500 | — | Unexpected failure: `internal error`, details logged only | Retry later |
| 502 | `ai_invalid` | Creating a draft or re-interpreting a brief: the AI's answer failed validation twice. Nothing is saved | Rephrase, or build the rules by hand |
| 502 | `ai_unavailable` | Creating a draft or re-interpreting a brief: the AI call failed (for example a rejected key or an outage) | Retry later, or build the rules by hand |
| 504 | — | Creating a draft or re-interpreting a brief: the AI didn't answer within `LLM_TIMEOUT` | Retry |

Message generation (`POST /api/campaigns/{id}/content`) answers the checks above (400, 404, 409, 422, 429) before streaming. After that, failures arrive as an `error` event: `{"message", "code"?}`, with `ai_invalid` or `ai_unavailable` as above. A refusal, a timeout, the AI call limit reached during a retry, a draft changed meanwhile or an internal error carries a message without a code. Nothing is saved unless the answer is valid.

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

**Why PostgreSQL and no Redis, RabbitMQ or Kafka.** The `messages` table is the queue (D18). A message's status, lease, attempt count and next retry time live in one row, so state and queue can't disagree: no dual writes, no outbox, no extra service. At most 200 messages/s is light load for Postgres. Live status (D59) and rate limits (D69) stay in process for the same reason. They would become useful as the project grows:

- **Redis:** several api replicas sharing sign-in and AI call limits, a cache, or pub/sub for live status.
- **RabbitMQ or NATS:** several independent sending services, per-provider queues or priorities, push delivery instead of polling.
- **Kafka:** a durable stream of delivery events (and later opens, clicks or orders) for analytics and A/B outcomes, or volumes far beyond one database's write rate.

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
| [Manual QA](docs/qa/2026-10-24-manual-qa/README.md) | 78 cases in a real browser, including 3 paid real-Claude calls: 77 passed initially, the one bug was fixed afterwards; results and screenshots |
| [Architecture audit](docs/audit/2026-10-24-architecture-audit.md) | Independent audit of the delivery path and the code; the approved findings were fixed (D69) |
| [Security review](docs/security-review.md) | Customer data, authorization, API security and dependencies, each with evidence |
| [Performance](docs/performance.md) | Performance targets, method and measured results |

Frontend development with hot reload, against the api from `docker compose up`:

```sh
docker run --rm -it -p 5173:5173 -v "$PWD/web":/web -v scc-web-node-modules:/web/node_modules -w /web \
  -e API_URL=http://host.docker.internal:8080 node:22-alpine sh -c "npm ci && npx vite --host 0.0.0.0"
```

## Project documentation

- **[`DECISIONS.md`](DECISIONS.md):** 73 numbered decisions (`D1`…), split into requirements and business rules, and architecture and technical choices. Each records the decision, why, the rejected alternatives and the trade-offs. Changes over time are kept as dated history notes, and replaced decisions are marked *Superseded* with a pointer to the new one. Open questions and deferred items are listed at the end. README sections cite these IDs.
- **[`ai-sessions/`](ai-sessions/):** exports of the Claude Code sessions, reviewed for keys and personal data before commit. The project was built in stages, with one export per stage: requirement analysis (01), architecture design (02), implementation planning (03), the foundation milestone (04), campaign development (05), live monitoring and the frontend (06), quality engineering (07), final development, QA and delivery (08), and final cleanup, the API reference and documentation (09). Separate exports make each stage easier to follow and audit. They keep the AI-assisted process, not just the final code: the questions asked, the decisions and the alternatives weighed, reviews, test failures and fixes, and the iterations in between.
- **[`docs/plans/`](docs/plans/):** implementation plans per milestone. **[`design/`](design/smart-campaign-composer-reference.html):** the reference UI design (open in a browser).

## Security

Details in [`docs/security-review.md`](docs/security-review.md). Contacts are masked by the server and never cached. The AI never receives customer data. Every API route except login needs a session (HttpOnly, SameSite=Lax cookie; only the token's hash is stored). Failed sign-ins are limited, writes from other sites are refused, and the app has a strict Content Security Policy. Secrets and personal data are scanned before every commit and in CI. The cookie isn't `Secure` on plain `http://localhost`.

## Known limitations

- **A/B test:** delivery data only: no clicks, conversions, statistical significance or winner (D68). The 50/50 split is exact per language over unique recipients; per channel it can differ slightly, since consent and contact details differ. Version B's over-limit estimate covers the whole audience, so it is an upper bound.
- **Languages:** Turkish and English only. A customer whose language has no text gets the campaign's default language. A further language needs a migration, seed data and mock texts (see Bonus features).
- **Mock AI** understands only the documented briefs; free-form briefs need real Claude.
- **Scalability:** sized for the case (3,000 customers, about 6,000 messages per campaign).
  - One worker sends at a time: the 100/s per-channel limit lives in its process, and a second worker waits on a database lock (D19, D69).
  - Sign-in limits, the AI call limit and metrics are in memory per api process and reset on restart; behind a proxy, all clients share its address.
  - Status streams share one snapshot per campaign (D59), but nothing limits how many are open.
  - Recipient details page by offset, so rows can move while a campaign sends (D47).
  - Performance was measured on one shared laptop, with results and their variance in [`docs/performance.md`](docs/performance.md).
- **`/docs`** loads Swagger UI from the jsDelivr CDN (pinned and integrity-checked), so it needs internet access; `/docs/openapi.json` works offline. A test keeps the spec's routes in step with the server, but not every field.

## Future technical improvements

- Bundle the Inter font from the design; the app uses Inter when it is installed and the system font otherwise.
- Add a frontend linter (such as ESLint). Today the frontend is checked by the TypeScript type check (`vue-tsc`), the Vitest tests and the i18n coverage check, all run by `make test`.

## Project completion summary

| Scope | Item | Status |
|---|---|---|
| Mandatory | AI audience rules from a brief, backend validation, no AI-written SQL, matching counts | Done |
| Mandatory | SMS and email generation, streaming, 160 / 80 character limits, variables, preview and edit | Done |
| Mandatory | Review, edit and explicit approval before sending | Done |
| Mandatory | Asynchronous queue, ≤ 100 messages/s per channel, no duplicates, retries | Done |
| Mandatory | Separate SMS and email permissions, checked again before every send | Done |
| Mandatory | Mock SMS and email providers: success, delays, temporary errors, 429 | Done |
| Mandatory | Live delivery status, metrics and health endpoints | Done |
| Mandatory | Structured AI JSON, validation, safe handling of invalid output, mock LLM for tests | Done |
| Mandatory | 3,000 fictional users seeded automatically, no duplicates on restart | Done |
| Mandatory | Vue.js frontend, Go backend, `docker compose up`, `make test`, README, `DECISIONS.md`, `ai-sessions/` | Done |
| Bonus | Scheduled sending | Done: send now, or 1 minute to 30 days ahead; can be cancelled before and during sending |
| Bonus | Messages in the user's language | Done for Turkish and English; others need a migration, seed data and mock texts |
| Bonus | A/B testing | Done as delivery-only: deterministic 50/50 split and delivery per version; no clicks, conversions, significance or winner |
| Bonus | CI pipeline | Done: tests, lint and secret scan on every push and PR; browser suite on PRs, `main` and nightly |
