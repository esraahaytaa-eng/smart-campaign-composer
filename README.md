# Smart Campaign Composer

AI-assisted SMS and email campaign tool: Claude suggests the audience and content, the backend validates, the marketer approves, the system sends to mock providers. Requirements: [`CASE.md`](CASE.md). Decisions: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/2026-10-21-implementation-plan.md`](docs/plans/2026-10-21-implementation-plan.md).

> **Status: work in progress.** Done: project skeleton (T1), database schema with fictional seed data (T2), login API (T3), audience rules (T4), AI brief interpretation with a mock and Claude (T5), campaign draft API (T6), message templates and over-limit estimate (T7), streamed content generation (T8), approval, cancel and resend (T9), mock SMS/email provider (T10), sending queue (T11), sending worker with retries and the 100/s limit (T12), end-to-end send tests (T13). Not yet built: worker, mock providers, frontend.

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
| `GET /api/catalog` | The rule language for building rules by hand (D39): every field with its operators and value type (`cities`, `window` = `{"last_days"}` or `{"from", "to"?}`, `categories_window`, `count`, `amount`), the current cities and categories, and the rule limits. |
| `POST /api/campaigns` `{"request", "channels", "time_zone"}` | The AI reads the brief and a **draft** is saved with its audience rules, current per-channel counts, and a proposed send time if the brief named one (only a proposal; nothing is scheduled). Add `"rules": [...]` to build the audience by hand without the AI. |
| `DELETE /api/campaigns/{id}` `{"version"}` | Delete a draft and its content for good (204). Deleting an abandoned resend draft lets that channel be resent again. Approved, sending, completed and cancelled campaigns are never deleted (409); stop a send with cancel (D37). |
| `PUT /api/campaigns/{id}/request` `{"request", "version"}` | Edit a draft's brief and let the AI read it again (D40). On success the brief, rules and proposed send time are replaced (rules edited by hand are overwritten) and the content is kept, so review or regenerate it before approval. A stale version is refused before the AI is called. Failures return the same errors as create and leave the draft unchanged. Not for resends (409). |
| `PUT /api/campaigns/{id}/rules` `{"rules", "version"}` | Replace the draft's rules; they are validated and recounted. |
| `PUT /api/campaigns/{id}/channels` `{"channels", "version"}` | Change a draft's channels, validated as on create; counts are refreshed. A removed channel's content is deleted; an added channel has no content until it is written or generated, and approval waits for it (D38). A resend's channel is fixed (409). |
| `PUT /api/campaigns/{id}/schedule` `{"send_at", "version"}` | Change or clear (`null`) the proposed send time: RFC 3339 with offset, between now and 30 days ahead. |
| `GET /api/campaigns`, `GET /api/campaigns/{id}` | List (newest 200) / one campaign; drafts include current counts, a single campaign its content. |
| `POST /api/campaigns/{id}/content` `{"version"}` | The AI writes the messages for the chosen channels, in the brief's language. The response is a `text/event-stream`: `delta` `{field, text}` events while it writes (fields `sms.body`, `email.subject`, `email.body`), `retry` `{errors}` if the first answer was invalid, then `done` `{content, version}` or `error` `{message}`. Content is saved only when valid and only if the client is still connected. |
| `PUT /api/campaigns/{id}/content` `{"content", "version"}` | Save messages edited by hand: `{"sms": {"body"}, "email": {"subject", "body"}}`. |
| `GET /api/campaigns/{id}/estimate` | Current audience and over-limit counts per channel, labelled as estimates (draft or approved). |
| `POST /api/campaigns/{id}/approve` `{"version", "send"}` | `send` is required: `"now"` or `{"at": "<RFC 3339>"}` (1 minute to 30 days ahead); a proposed time is never used implicitly. Checks content and a non-empty audience, freezes rules and content, and stores the current counts as `approval_estimate`. No messages are created yet: the audience is evaluated again when sending starts (D30). |
| `POST /api/campaigns/{id}/cancel` | Stops an approved or sending campaign; messages still waiting are cancelled. Drafts are deleted instead. |
| `GET /api/campaigns/{id}/events` | Live status as a `text/event-stream` (D16, D36): a `status` snapshot at once and then whenever it changes (checked every second), with the campaign's status and times, `approval_estimate`, and per channel the actual audience, counts by message status, retries, 429s, skip reasons, the 10 most frequent failure reasons, uncertain deliveries, how many a resend would reach now and any active resend. `retries` counts attempts after a message's first. Once the campaign is completed or cancelled and no message is in flight, the final snapshot is followed by `end` and the stream closes; close the `EventSource` then, or the browser reconnects. On a server shutdown or a database error the stream closes without an event, and the browser's reconnect picks it up again. A resend created after `end` is not streamed; fetch the campaign again. |
| `POST /api/campaigns/{id}/resend` `{"channel"}` | After a campaign completes or is cancelled: a draft follow-up for the recipients skipped for the character limit on that channel, minus anyone who may already have it (sent, still sending, or with an uncertain delivery). One active resend per campaign and channel. |

Messages may use only `{{first_name}}`, `{{last_name}}` and `{{city}}` and need some fixed text; text that is over the limit even with every variable empty, and control characters other than tabs and line breaks, are rejected. Values are inserted as they are stored: a value that itself contains a variable token (e.g. a name saved as `{{city}}`) is expanded too, in the same way when sending and in the over-limit estimate. `channels` is `["sms"]`, `["email"]` or both; `time_zone` is the browser's IANA zone (e.g. `Europe/Istanbul`). Edits send the `version` they were based on; a stale version or a non-draft campaign returns 409. Invalid input returns 422 (with `field_errors` for rules); an "unsupported" brief returns 422 with `"unsupported": true` and the AI's reason, and nothing is saved. For `POST /api/campaigns`, if the AI answers invalidly twice, times out or is unavailable, the API returns 502/504 and nothing is saved; content generation reports the same failures as an `error` event in its stream.

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

## Mock provider

The `mock-provider` service plays the SMS and email provider; nothing is really sent. `POST /sms/send` `{"to", "body"}` and `POST /email/send` `{"to", "subject", "body"}` require an `Idempotency-Key` header (the message ID). Per channel it adds a delay and fails a share of requests, with the defaults from D20: 20–200 ms delay, 5% 5xx, 3% 429 with `Retry-After: 1`, 1% requests that hang past the worker's 5 s timeout and are then accepted (a lost response). Change them with the `SMS_*` / `EMAIL_*` settings in `.env.example`; `PROVIDER_SEED` makes runs repeatable.

The key is stored before anything is simulated. A repeated key gets the stored result for a finished request, or a retryable 503 with `Retry-After` while it is still in progress; a 5xx or 429 releases the key so a retry is processed. Keys live in the `provider` schema, which the provider reaches through its own database role (`scc_provider`, password `PROVIDER_DB_PASSWORD`) that cannot read the app's tables (D6).

## Sending

The `worker` service starts approved campaigns when their send time comes, evaluating the audience at that moment (D30), and sends each message through the provider with its message ID as `Idempotency-Key`. Before every request it re-checks that the campaign is still sending and the recipient still allows the channel, and it personalizes and length-checks the text: over-limit messages are skipped (`character_limit_exceeded`) and can be resent later (D31). 200 → `sent`; 429, 5xx, timeouts and network errors are retried with exponential backoff and jitter (honouring `Retry-After`) up to `MAX_ATTEMPTS` (5); other 4xx → `failed`. Every attempt is recorded in `delivery_attempts`.

A request without a clear answer (a timeout or a network error) may still have been delivered, so the message is marked `delivery_uncertain`; the retry uses the same key and gets the provider's stored result instead of a second send. The same holds when a worker died mid-attempt (its expired lease is reclaimed). If the campaign is cancelled or the recipient opts out before that retry, the message is recorded as cancelled or skipped with a note that delivery is uncertain, and a later resend leaves them out (D31). Stopping the worker interrupts in-flight requests: they are recorded as `interrupted`, do not count toward `MAX_ATTEMPTS`, and the message is retried at once; messages still waiting for a rate-limit slot go straight back to the queue.

Per channel at most 100 provider requests leave in any second, retries included: a rate limiter spaces them 10 ms apart and a hard cap counts actual departures (D19). The cap is exact on the worker's side; at the provider, a request delayed by more than 50 ms between leaving the queue and arriving could in theory still bunch with later ones.

## Metrics

Every process serves Prometheus metrics at `/metrics` (D26), alongside the standard Go and process metrics:

| Metric | Process | Labels |
|---|---|---|
| `scc_http_requests_total`, `scc_http_request_duration_seconds` | all | `route` (the matched pattern, e.g. `GET /api/campaigns/{id}`, or `unmatched`), `code` |
| `scc_send_attempts_total` | worker | `channel`, `outcome` (`accepted`, `rate_limited` = HTTP 429, `server_error`, `timeout`, `network_error`, `rejected`, `interrupted`) |
| `scc_send_retries_total` | worker | `channel`: messages scheduled for another attempt after a failed one (an interrupted attempt is not counted, unlike the stream's `retries`) |
| `scc_provider_request_duration_seconds` | worker | `channel` |
| `scc_llm_calls_total` | api | `operation` (`interpret`, `content`), `result` (`ok`, `invalid` = failed validation and retried once, `error`, `cancelled` = the client left) |

Only the api's port is published. The worker and mock provider serve the same endpoints on port 8080 inside their containers, e.g. `docker compose exec worker wget -qO- localhost:8080/metrics`. Counts are per process since it started; the dashboard's numbers come from the database instead.

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

Integration tests fail, never skip, when no database is available. `internal/e2e` runs the API, the worker and the mock provider together on a seeded database: a send through a provider that fails, rate-limits and loses responses, a scheduled send, and an over-limit send followed by a resend. Its lifecycle tests cover timeouts and replays, worker shutdowns and crashes, cancellation races, consent changes, length skips, resends after a cancelled resend, retry limits and two workers on one queue.

The first run compiles all dependencies and takes a few minutes; compiled packages are kept in the `gobuildcache` volume (and in BuildKit cache mounts for image builds), so later runs take seconds. `docker compose down -v` also clears these caches.

## Architecture (current)

One Go module (`cmd/scc`) built into one image and run as separate Compose services (D20): `postgres`, `migrate` (one-shot: migrations + seed), `api`, `worker`, `mock-provider`. `internal/config` reads environment variables, `internal/obs` serves health and metrics and counts HTTP requests, `internal/db` holds the embedded migrations and the seed, `internal/auth` the login, logout and session check. `internal/audience` validates audience rules against a fixed catalog (D14) and turns them into parameterized SQL; rule values only ever reach Postgres as query arguments. `internal/provider` is the mock provider. `internal/worker` holds the queue (the messages table, claimed with `FOR UPDATE SKIP LOCKED` and a 30 s lease, D5, D18) and the dispatcher. `internal/content` validates message templates (only `{{first_name}}`, `{{last_name}}`, `{{city}}`; SMS ≤ 160, subject ≤ 80, email body ≤ 2,000 characters after personalization) and estimates over-limit messages per channel in SQL.

## Future improvements

- **Archiving campaigns:** hide finished campaigns from the list. Not built: deleting drafts removes most of the clutter, and archiving can be added later without changing how deletion works (D37).

## Known limitations

- Each open status stream polls the database about once a second (around five small queries, more when a resend count is shown), and nothing limits how many streams are open. Fine for a demo; a shared poller per campaign is the upgrade path.
- If no worker is running, a cancelled campaign's stream stays open until its in-flight messages are reclaimed.
- Metrics count per process since it started.
