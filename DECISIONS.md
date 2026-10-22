# Decisions

This file records approved decisions. Confirmed decisions and open questions are kept in separate sections. When a decision changes, it gets a dated history note. A replaced decision is marked `Superseded` and is never deleted.

Status values: `Accepted`, `Superseded`, `Deferred`.

Fixed by `CASE.md`: Go backend and Vue.js frontend. Stack and component choices: D17–D27, D33–D35.

## Requirements & Business Decisions

### D1 — Marketer chooses the channels for each campaign
- **Status:** Accepted · 2026-10-21
- **Decision:** Each campaign targets SMS, email or both. Claude may suggest channels. Content is generated only for the chosen channels, and per-channel consent decides who receives what.
- **Why:** Realistic and cheap. Avoids generating and reviewing content that will never be sent.
- **Rejected:** *Always both:* wasted effort when only one channel is wanted. *Claude decides:* one more AI output to validate, less predictable.
- **Trade-offs:** One extra input in the UI. The content schema depends on the chosen channels.

### D2 — SMS limit is 160 Unicode characters on the final message
- **Status:** Accepted · 2026-10-21
- **Decision:** At most 160 Unicode code points, measured after variables are filled in with recipient data. Turkish characters are kept. The UI shows the encoding (GSM-7 or UCS-2) and the estimated segment count as information only. "Character" means a code point in every layer.
- **Why:** The case limits characters, not segments. Turkish text stays intact and the marketer can still see the segment cost.
- **Rejected:** *Strict single segment (70 for UCS-2):* forces very short Turkish messages and goes beyond the case. *Counting the raw template:* the length sent would differ from the length validated.
- **Trade-offs:** A Turkish SMS can use up to 3 segments. Length must be counted the same way in the backend and the database.

### D3 — Character limits are enforced per message at send time
- **Status:** Accepted · 2026-10-21
- **Decision:** The worker checks each personalized message just before sending. An SMS over 160 characters or an email subject over 80 is not sent: it is marked `skipped` with reason `character_limit_exceeded`, and it is never retried or truncated. The dashboard shows the actual skipped counts and reasons. Before approval, a warning that does not block approval shows, per channel, how many messages would exceed the limit. It is computed with an aggregate query, without rendering messages in memory. Content whose fixed text alone (all variables empty) exceeds a limit is a validation error and blocks approval.
- **Why:** One rule in one place, and nothing is changed silently. Data can change after approval, so the check at send time is the one that counts.
- **Rejected:** *Blocking approval:* too strict for a few edge-case recipients. *A static fallback message:* extra content to generate, approve and test. *Rendering every message before approval:* does not scale.
- **Trade-offs:** Some recipients may miss the campaign until resent (D31). The warning count is an estimate.
- **History:** 2026-10-21. At first there was no check before approval. The warning count was added in the same session. 2026-10-21 (planning): warning shown per channel; fixed text over the limit became a validation error, since no recipient could receive it.

### D4 — Duplicate prevention per campaign, user and channel
- **Status:** Accepted · 2026-10-21
- **Decision:** One message record per (campaign, user, channel), enforced by a unique database constraint. The message ID is the stable idempotency key used on every attempt (D5, D6).
- **Why:** Retries, crashes and double approvals can never create a second message.
- **Rejected:** *Also deduplicating by phone or email address:* the seed data has no shared contact details. *A frequency cap across campaigns:* a business rule beyond the case.
- **Trade-offs:** The same user can receive different campaigns. The constraint alone cannot stop a resend after a crash; D5 and D6 cover that.

### D5 — At-least-once processing with provider-side idempotency
- **Status:** Accepted · 2026-10-21
- **Decision:** Delivery is at-least-once processing with provider-side idempotency, not exactly-once. Workers claim a message with a time-limited lease. When a lease expires, the message is picked up again and resent with the same key. States: `pending`, `sending`, `retry_scheduled`, `sent`, `failed`, `skipped`, `cancelled`.
- **Why:** A crash or timeout after the provider has accepted a message is unavoidable, and exactly-once delivery to an external system is not achievable.
- **Rejected:** *At-most-once:* messages would be lost. *Claiming exactly-once:* inaccurate.
- **Trade-offs:** Leases must be implemented and expired ones recovered. Depends on the provider's idempotency (D6).

### D6 — Mock provider stores idempotency records in the existing database
- **Status:** Accepted · 2026-10-21
- **Decision:** Providers keep idempotency records in their own table or schema on the database server we already use. The app and the providers never read each other's tables, and no extra service is added. The key is inserted before the send is simulated. A repeated key that is still in progress gets a retryable response. A repeated key that has finished gets the stored result back.
- **Why:** Duplicate protection survives provider restarts. Inserting the key first stops two concurrent requests with the same key from both being sent.
- **Rejected:** *In memory:* keys are lost on restart. *A separate store such as a cache service:* an extra service. *Keys only in the app:* the app cannot know whether the provider accepted the message.
- **Trade-offs:** The provider shares a database server with the app, so the boundary relies on convention and a separate schema or database user.
- **History:** 2026-10-21. Replaced the in-memory store proposed at first.

### D7 — Retry policy
- **Status:** Accepted · 2026-10-21
- **Decision:** At most 5 attempts (configurable). Exponential backoff with jitter, capped at about 30s. `Retry-After` is honoured on 429. Retried: 429, 5xx, timeouts and network errors. Other 4xx responses → `failed`. Character limit or missing consent → `skipped`, never retried. After the last attempt → `failed` with the last error. Retries count toward the 100 messages/s per-channel limit.
- **Why:** Predictable behaviour. Jitter avoids retry waves, and counting retries keeps the rate limit honest.
- **Rejected:** *Fixed-interval retries:* they hammer a struggling provider. *Unlimited retries:* messages would never reach a final state.
- **Trade-offs:** Each message needs a next-attempt time, and the worst case adds a delay of a few minutes.

### D8 — Audience frozen at approval, consent re-checked before sending
- **Status:** Superseded by D30 · 2026-10-21
- **Decision:** On approval, the rules are evaluated once and message records are created for each chosen channel. Users who match later are not added. The worker checks consent again before each send, and revoked consent → `skipped` with reason `no_consent`.
- **Why:** The marketer approves a known audience, and opt-outs are always respected.
- **Rejected:** *Evaluating at send time:* the approved count could change, and dedup and progress tracking get harder. *Freezing at preview:* stale if the review takes long.
- **Trade-offs:** Users who match after approval are missed.
- **History:** 2026-10-21 (planning). Superseded by D30 once scheduled sending (D29) was added: days can pass between approval and sending. The consent re-check is kept in D30.

### D9 — Audience rules shown as readable, editable chips
- **Status:** Accepted · 2026-10-21
- **Decision:** Rules are shown as plain-language chips. The marketer can change values, remove a rule, or add one from the catalog (D14). Edits go through the same validation as AI output, and the count is refreshed.
- **Why:** Claude can misread the intent (for example, the time window for "no purchase"). Clear accountability, and small fixes don't need a new prompt.
- **Rejected:** *Re-prompting only:* frustrating, and results vary each time. *A raw JSON editor:* unfriendly to marketers and easy to break.
- **Trade-offs:** More UI work than a read-only view.

### D10 — Multiple marketers with a single role
- **Status:** Accepted · 2026-10-21
- **Decision:** Seeded demo marketer accounts, all with the same role. Every marketer can create, edit, approve, send and cancel campaigns, and sees all campaigns. `created_by` and `approved_by` are recorded with timestamps. Passwords are hashed, and the demo login details are for local use only. Approval checks the campaign's version, so a campaign changed since it was last viewed cannot be approved. Sessions expire 12 hours after login, with no sliding renewal. Health and metrics endpoints are open. No RBAC, registration or user-management UI.
- **Why:** Prevents unauthenticated sends and wasted API credits, and records who approved what, at minimal cost.
- **Rejected:** *No authentication:* anyone could trigger sends. *Roles such as editor and approver:* beyond the case.
- **Trade-offs:** No four-eyes approval. The demo login details are not suitable for production.
- **History:** 2026-10-22. The seed creates a single demo login, `marketer@marketer.com` (password from `DEMO_MARKETER_PASSWORD`, default documented in the README), at the engineer's request. The schema still supports several marketers. 2026-10-22: session lifetime of 12 hours approved (was an open question): covers a working day without frequent logins, and a stolen cookie stays useful for at most half a day.

### D11 — Campaign lifecycle with cancel only
- **Status:** Accepted · 2026-10-21
- **Decision:** `draft` → `approved` → `sending` → `completed` | `cancelled`. Approved content cannot be changed. Cancelling is allowed while `approved` or `sending`: `pending` and `retry_scheduled` messages → `cancelled`, and attempts already in progress finish. No pause or resume.
- **Why:** An essential safety stop (for example, the wrong discount) at low cost.
- **Rejected:** *Pause and resume:* edge cases around leases and rate limits. *No control after approval:* no way to stop a mistake.
- **Trade-offs:** Sent messages cannot be recalled, and a cancelled campaign cannot be resumed.
- **History:** 2026-10-21 (planning). An approved campaign waits until its send time (D29); cancelling before sending starts leaves no messages to cancel (D30).

### D12 — Allowlist of personalization variables
- **Status:** Accepted · 2026-10-21
- **Decision:** `{{first_name}}`, `{{last_name}}` and `{{city}}`, each mapped to a user field and given to Claude in its prompt. An unknown variable or invalid syntax is a validation error before approval. An empty value at send time becomes an empty string, and the whitespace is tidied.
- **Why:** A small, known set is easy to validate and makes D3's length estimate possible.
- **Rejected:** *`first_name` only:* too limiting. *A default value per variable:* an extra UI field and validation path.
- **Trade-offs:** A message can read awkwardly when a value is missing.

### D13 — Plain-text email body with a length cap
- **Status:** Accepted · 2026-10-21
- **Decision:** Plain text with line breaks kept, escaped in the preview, at most about 2,000 characters. Validated before approval and again after personalization at send time.
- **Why:** No HTML injection or XSS risk, and easy to stream and validate. The mock providers do not render email.
- **Rejected:** *Limited Markdown:* needs a sanitizer. *Full HTML:* the largest security risk for little value in a demo.
- **Trade-offs:** Emails are visually basic.

### D14 — Audience rule language: flat AND over a fixed catalog
- **Status:** Accepted · 2026-10-21
- **Decision:** A flat list of conditions that must all match. Catalog: city IN [...], cart item added within N days, no order within N days, order count (>=, <=), total spend (>=, <=), last order before or after N days, category ordered or carted within N days, cart value (>=), signed up within N days, and language (reserved for later). Time conditions use either the last N days, counted when sending starts (D30), or fixed dates. Consent is applied automatically from the chosen channels. At least one rule is required: an empty list is a validation error, never "everyone". No OR or nesting. Validated rules become parameterized queries, and SQL produced by AI is never run. The seed data covers every catalog field.
- **Why:** Covers the case example and typical campaigns, and is easy to validate, display, test and keep safe.
- **Rejected:** *Nested AND/OR groups:* harder UI and validation, and harder for marketers to read. *Only the fields in the case example:* too narrow for the variety of campaigns the case asks for.
- **Trade-offs:** Some audiences, such as "(A and B) or C", cannot be expressed.
- **History:** 2026-10-21 (planning). Category, cart value and signup conditions added with the event tables (D33). Fixed dates added so a cutoff named in a brief does not drift when sending starts later (D30). 2026-10-22: empty rule lists rejected, so a missing AI result or deleted chips cannot target the whole user base (D15).

### D15 — Handling invalid or failed AI output
- **Status:** Accepted · 2026-10-21
- **Decision:** Every response is checked against the schema, the catalog (D14), the variables (D12) and the length limits. If invalid, Claude is asked once more with the validation errors. If it is still invalid, or the call times out or the API fails, nothing is saved, a clear error is shown, and the marketer can rephrase or build the rules by hand. Claude replies `unsupported` with a reason for off-topic or impossible requests, and when any part of the requested audience cannot be expressed: conditions are never dropped, approximated or added. The mock LLM follows the same rule and understands only the example briefs listed in the README. Claude never receives user records, only the request, catalog and allowlist. The brief is limited to 1,000 characters: a longer one is rejected before Claude is called, never truncated. Streamed content is validated once the stream completes. Tests use a mock LLM.
- **Why:** Recovers from most formatting mistakes, never acts on unvalidated output, and keeps personal data away from the LLM.
- **Rejected:** *Failing on the first error:* more failures reach the user. *Keeping the valid parts:* can silently change the audience.
- **Trade-offs:** The retry adds latency and cost. Streamed content is confirmed valid only at the end.
- **History:** 2026-10-21 (planning). `unsupported` made mandatory for briefs that are only partly understood, and applied to the mock: sending to the wrong users is unacceptable. 2026-10-22: brief limit of 1,000 characters approved (was an open question): about 150 words, far more than a brief needs, while keeping AI cost and the space for hidden instructions small.

### D16 — Delivery status reflects provider acceptance only
- **Status:** Accepted · 2026-10-21
- **Decision:** `sent` means the provider accepted the message. Final states: `sent`, `failed` (with the last error), `skipped` (with a reason) and `cancelled`; `pending` and `retry_scheduled` are in progress. The dashboard shows live per-campaign and per-channel counts and progress, retry and 429 counts, and skip and failure reasons.
- **Why:** Matches what the mock providers can actually report.
- **Rejected:** *Simulated delivery callbacks:* need a webhook endpoint, callback authentication and out-of-order update handling.
- **Trade-offs:** "Sent" does not mean the message reached the device or inbox.

### D28 — Seed: 3,000 users with generated shopping histories
- **Status:** Accepted · 2026-10-21
- **Decision:** The seed generates 3,000 users with varied order and cart histories, using a fixed random seed. Dates are relative to the first setup. Once seeded, it does nothing on later runs and never changes existing data.
- **Why:** Enough variety for different campaigns. Deterministic and idempotent, as `CASE.md` §8 requires.
- **Rejected:** *5,000 users:* no added value (`CASE.md` amended). *Shifting dates forward on each start:* modifies existing data.
- **Trade-offs:** Relative-date audiences shrink as real time passes. A fresh reseed resets them but deletes campaigns.

### D29 — Scheduled sending, with a suggested time from the brief
- **Status:** Accepted · 2026-10-21
- **Decision:** At approval the marketer chooses to send now or at a time up to 30 days ahead. Claude may suggest a time from the brief; it is shown for review and never applied without explicit confirmation. Approved campaigns wait in the existing queue until their send time.
- **Why:** A common marketing need, and the queue (D18) already handles due times, so no extra service is needed.
- **Rejected:** *Manual date only:* ignores timing written in the brief. *Applying the suggestion automatically:* the marketer must confirm when messages go out. *A separate scheduler process:* an extra component.
- **Trade-offs:** No rescheduling: cancel and create a new campaign.

### D30 — Audience evaluated once when sending starts
- **Status:** Accepted · 2026-10-21
- **Decision:** Rules and content are fixed at approval. The audience is evaluated once when sending starts, and the message records are created then. Approval shows the current count per channel with a notice that it may change; the dashboard shows the estimate and the actual count. Consent is still re-checked before each send. Replaces D8.
- **Why:** Scheduled sends can wait days. Users who bought or newly matched in the meantime are handled correctly.
- **Rejected:** *Freezing at approval (D8):* stale for scheduled sends. *A per-campaign snapshot option:* a second mode with no current need. *Relative rules only:* a fixed cutoff in a brief would drift (D14).
- **Trade-offs:** The approved count is an estimate. Users who stop matching after sending starts are not removed.

### D31 — Resend to recipients skipped for the character limit
- **Status:** Accepted · 2026-10-21
- **Decision:** After a campaign ends, the marketer can create a linked follow-up campaign for one channel. Its audience is exactly the recipients skipped with `character_limit_exceeded` on that channel, minus anyone already sent. It gets new, shorter content and its own approval. One active resend per campaign and channel. No alternative templates at approval.
- **Why:** Recovers missed recipients without ever resending to anyone else.
- **Rejected:** *An alternative template at approval:* more content to write and review up front. *Reusing the original messages:* approved content would change (D11).
- **Trade-offs:** Skipped recipients get their message later.

### D32 — AI replies in the request's language
- **Status:** Accepted · 2026-10-21
- **Decision:** Claude and the mock LLM write content in the language of the request, Turkish included.
- **Why:** Marketers write the brief in the language they want to send.
- **Rejected:** *English-only mock:* breaks Turkish demos. *Language per recipient:* optional feature, still deferred.
- **Trade-offs:** Mixed-language briefs depend on Claude's judgement.

## Architecture & Technical Decisions

### D17 — PostgreSQL 16
- **Status:** Accepted · 2026-10-21
- **Decision:** PostgreSQL 16 is the only database, used by the app and (in a separate schema) by the mock providers.
- **Why:** `FOR UPDATE SKIP LOCKED` for leases (D5), `char_length` counts code points (D2), schemas and roles separate the providers (D6), `ON CONFLICT` covers D4 and the idempotent seed, mature Go driver (pgx).
- **Rejected:** *MySQL 8:* weaker schema/role separation and `RETURNING` support. *SQLite:* not a server (D6), single writer.
- **Trade-offs:** Heavier to run than SQLite.

### D18 — The messages table is the queue
- **Status:** Accepted · 2026-10-21
- **Decision:** No separate broker. Workers claim batches with `FOR UPDATE SKIP LOCKED`: messages that are `pending` or `retry_scheduled` and due, or `sending` with an expired lease. Result updates only apply while the worker still holds the lease, so a stale worker cannot overwrite a newer result.
- **Why:** The lease, attempt count and next-attempt time (D5, D7) already live in this table. No dual write between a database and a broker, no outbox, no extra service. At most 200 messages/s is light load for Postgres.
- **Rejected:** *Redis + asynq:* extra service and dual writes. *RabbitMQ or NATS JetStream:* extra service, dual writes, awkward delayed retries. *River:* its job table duplicates our message state machine.
- **Trade-offs:** The worker polls when idle. A reviewer may expect a broker, so the reason is documented here.

### D19 — One worker with an in-process rate limiter per channel
- **Status:** Accepted · 2026-10-21
- **Decision:** Exactly one worker replica. Each channel has a `golang.org/x/time/rate` limiter with a limit of 100/s and a burst of 1, so attempts start at least 10 ms apart and no 1-second window holds more than 100 provider requests, bursts included. Every attempt takes a token right before the HTTP call: first sends, retries (D7) and resends after an expired lease (D5). A bounded goroutine pool per channel handles provider delays; the limiter, not the pool, sets the rate. A test checks that no 1-second window of recorded attempt times exceeds 100. The API runs as a separate process (D20) and can scale on its own.
- **Why:** The simplest way to enforce the limit exactly and test it.
- **Rejected:** *Shared token bucket in Postgres:* a database round-trip per message and more code. *Static split (100/N per replica):* the config must match the replica count, and capacity is wasted when a replica is idle.
- **Trade-offs:** The worker cannot scale out without a shared limiter.

### D20 — Component boundaries
- **Status:** Accepted · 2026-10-21
- **Decision:** One Go module and one Docker image, run as separate Compose services:
  - `postgres` with a healthcheck.
  - `migrate`: one-shot migrations and the deterministic, idempotent seed. The other services wait for it to finish successfully.
  - `api`: campaigns, approval, Claude calls, SSE, auth. It also serves the built Vue app, so the frontend and API share an origin.
  - `worker`: sending only (D18, D19).
  - `mock-provider`: one service with `/sms/send` and `/email/send`. Delays, 5xx, 429 with `Retry-After` and timeouts are configured per channel through environment variables. Its idempotency table lives in a `provider` schema with its own database role (D6).
  - The API and the worker communicate only through the database.
- **Why:** AI generation and campaign management stay independent of sending, a worker crash does not affect the UI, and the setup has the fewest services. Same origin means no CORS.
- **Rejected:** *Frontend in an nginx container:* one more service, plus proxy settings so SSE isn't buffered. *Separate SMS and email provider services:* two services that do the same thing. *API and worker in one process:* sending would share failures with the UI and AI calls.
- **Trade-offs:** Multi-stage Docker build (Node, then Go). During development, the Vite dev server proxies to the API.

### D21 — Live updates with SSE
- **Status:** Accepted · 2026-10-21
- **Decision:** Content generation is a POST whose streamed response the browser reads with `fetch`. The dashboard uses `EventSource`: the API queries the per-campaign counts about once a second and pushes them only when they change.
- **Why:** One streaming mechanism for both features, automatic reconnect, no extra library. It works across processes because the counts come from the database.
- **Rejected:** *WebSocket:* two-way communication is not needed, and it needs an extra library. *Client polling:* a second mechanism, and less live. *Postgres LISTEN/NOTIFY:* floods the API with notifications at 200 messages/s.
- **Trade-offs:** About 1 s of delay. One aggregate query per open dashboard per second.

### D22 — Backend libraries
- **Status:** Accepted · 2026-10-21
- **Decision:**
  - stdlib `net/http` with Go 1.22 routing.
  - `pgx/v5` and `pgxpool` with hand-written SQL.
  - `goose` with embedded migrations.
  - The official `anthropic-sdk-go` (structured JSON output, streaming for content), behind a small `LLM` interface that also has a mock implementation.
  - Hand-written validation (D12–D15).
  - `x/crypto/bcrypt`, plus a random session token in an HttpOnly SameSite cookie, with only its hash stored in the database.
  - `x/time/rate`, `log/slog`, configuration from environment variables.
  - Seed data from hand-written Turkish name and city lists, with a fixed random seed.
- **Why:** Few dependencies, and the SQL stays explicit and easy to audit (D14).
- **Rejected:** *gin, echo or chi:* no feature we need. *GORM:* hides the SQL. *sqlc:* a code-generation step, and it cannot build dynamic WHERE clauses. *Postgres init scripts:* only run on a fresh volume. *Raw HTTP to Claude:* reimplements the SDK. *A JSON Schema library:* the catalog checks need custom code anyway. *JWT:* hard to revoke, no benefit here. *viper or zap:* not needed. *gofakeit:* an extra dependency, and its data isn't Turkish.
- **Trade-offs:** More hand-written SQL and validation code.

### D23 — Default Claude model
- **Status:** Accepted · 2026-10-21
- **Decision:** `claude-opus-5-5` by default, overridable with `ANTHROPIC_MODEL`.
- **Why:** The most reliable at understanding campaign requests correctly.
- **Rejected:** *`claude-sonnet-5-5`:* cheaper and faster, slightly higher risk of misreading a request. *`claude-haiku-5-5`:* more D15 retries.
- **Trade-offs:** Higher price per token and slower streaming than Sonnet. Actual cost depends on token usage.

### D24 — Mock LLM by default, real mode opt-in
- **Status:** Accepted · 2026-10-21
- **Decision:** `LLM_MODE=mock` is the default, so `docker compose up` runs the full campaign flow without an API key, and the UI shows a visible "Mock AI" banner. The mock returns schema-valid output that goes through the same validation (D15). `LLM_MODE=real` uses `ANTHROPIC_API_KEY`. If the key is missing, the API exits on startup with a clear error naming the variable.
- **Why:** Anyone can try the whole flow with one command, and real mode is an explicit choice that fails fast when misconfigured.
- **Rejected:** *Real mode by default, mock opt-in:* trying the flow needs a key or an extra step. *Falling back to the mock automatically when no key is set:* hides misconfiguration.
- **Trade-offs:** The default run shows canned AI output. `.env.example` and README must explain how to switch to real mode.
- **History:** 2026-10-21. First approved as "clear error, opt-in mock". Changed to mock by default in the same session.

### D25 — Frontend stack
- **Status:** Accepted · 2026-10-21
- **Decision:** Vue 3, Vite, TypeScript, vue-router and PrimeVue. Native `fetch` and `EventSource`. Composables for state, no Pinia. Vitest for unit tests.
- **Why:** PrimeVue provides accessible chips (D9), tables, progress bars and toasts, which speeds up the editors and the dashboard.
- **Rejected:** *No UI library:* more hand-built components, and accessibility is entirely our responsibility. *Tailwind CSS:* components are still built by hand. *Pinia:* not needed for a few screens.
- **Trade-offs:** A large dependency that is only partly used.

### D26 — Metrics and health endpoints
- **Status:** Accepted · 2026-10-21
- **Decision:** Prometheus text format via `prometheus/client_golang` at `/metrics` on the API, the worker and the mock provider. Metrics: send attempts by channel and result, 429 and retry counts, provider latency histogram, Claude calls, failures and validation retries, and HTTP requests. Each process has `/healthz` (liveness) and `/readyz` (database ping), used by Compose healthchecks. Dashboard counts come from the database (D16). No Prometheus or Grafana service in Compose.
- **Why:** The standard format, readable by any monitoring tool, with histograms.
- **Rejected:** *Custom JSON:* a format no tool reads, and no histograms. *expvar:* no labels.
- **Trade-offs:** One more dependency.

### D27 — `make test` runs in Docker
- **Status:** Accepted · 2026-10-21
- **Decision:** `make test` uses a Compose `test` profile: a throwaway Postgres plus Go and Vitest test containers. `make test-local` is for development. Integration tests fail, never silently skip, when no database is available.
- **Why:** A reviewer needs only Docker and make, the same as for `docker compose up`. Leases, `SKIP LOCKED` and the unique constraints are tested against a real Postgres.
- **Rejected:** *Local toolchains:* the reviewer must install Go and Node. *testcontainers-go:* a heavy dependency that still needs Go on the host.
- **Trade-offs:** The first run is slower because images are built.

### D33 — Shopping history as event tables
- **Status:** Accepted · 2026-10-21
- **Decision:** `orders` with `order_items`, and `cart_items`. Each item has a category from a `categories` table. Rules compile to parameterized subqueries (D14). No products table. Each provider attempt is logged per message for retry and 429 counts (D16).
- **Why:** Supports varied campaigns (cart abandonment, win-back, VIP, category, new customers) with a small model.
- **Rejected:** *Summary columns on users:* no category or per-order data. *A products table:* more joins, and no rule needs product-level data.
- **Trade-offs:** No product-level targeting.

### D34 — Streamed content parsed in the backend
- **Status:** Accepted · 2026-10-21
- **Decision:** Claude streams structured JSON. The API extracts the SMS and email text from the partial JSON and streams it per field. The full JSON is validated when the stream ends (D15).
- **Why:** The frontend stays simple, and parsing and validation stay in one place.
- **Rejected:** *Parsing partial JSON in the browser:* the logic moves to the frontend. *No live text:* weakens the `CASE.md` streaming requirement.
- **Trade-offs:** A small custom parser that needs careful tests.

### D35 — Git workflow
- **Status:** Accepted · 2026-10-21
- **Decision:** A docs-only first commit on `main`. Then one branch per plan milestone with small commits per task, merged into `main` only after the engineer's approval. No push or history rewrite without approval.
- **Why:** Small, reviewable steps that match the plan's milestones.
- **Rejected:** *Committing directly to `main`:* no review point. *One branch for everything:* one large review at the end.
- **Trade-offs:** One merge approval per milestone.

## Open Questions

- **Operational defaults (confirm before T10/T12):** mock-provider behaviour (20–200 ms delay, 5% 5xx, 3% 429 with `Retry-After: 1`, 1% timeout), lease 30 s, provider timeout 5 s, 20 workers per channel. All proposed, not yet approved.

## Deferred

- **Optional features from `CASE.md`:** A/B testing, messages in the user's language (a `language` field will be seeded now), CI pipeline. Scheduled sending moved into scope (D29). Resend to skipped recipients moved into scope (D31).
