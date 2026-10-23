# Smart Campaign Composer

AI-assisted SMS and email campaign tool: Claude suggests the audience and content, the backend validates, the marketer approves, the system sends to mock providers. Requirements: [`CASE.md`](CASE.md). Decisions: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/2026-10-21-implementation-plan.md`](docs/plans/2026-10-21-implementation-plan.md).

> **Status: work in progress.** Done: backend T1–T14 (login, audience rules, AI interpretation and content with a mock and Claude, drafts, approval, cancel and resend, mock providers, sending worker, metrics and the live status stream) plus the draft endpoints the frontend needs (D37–D40), the frontend app shell with sign-in (T15), the composer's audience step (T16), its content and review-and-approve steps (T17), and English/Turkish UI (D44), and for T18 so far the delivery groups and recipient API (D46, D47) and the dashboard and campaign list. Not yet built: the campaign page after approval, live delivery, recipient details and resend (T18).

## Requirements

Docker with Compose, and `make`. No local Go or Node toolchain is needed.

## Run

```sh
docker compose up
```

No `.env` is needed. To change settings, `cp .env.example .env` and edit it.

- `http://localhost:8080`: the app (sign in with the demo login below)
- `http://localhost:8080/healthz`: liveness
- `http://localhost:8080/readyz`: database reachable (503 otherwise)
- `http://localhost:8080/metrics`: Prometheus metrics

`LLM_MODE=mock` is the default. With `LLM_MODE=real`, `ANTHROPIC_API_KEY` must be set in `.env`, otherwise the api exits on startup.

### Demo login

| Email | Password |
|---|---|
| `marketer@marketer.com` | `Demo1234!` |

For local use only. The password can be changed with `DEMO_MARKETER_PASSWORD` in `.env`, but only before the first `docker compose up`: the seed runs once and never changes existing rows. Sign in on the app's login page, or use the API directly:

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
| `GET /api/catalog` | The rule language for building rules by hand (D39): every field with its operators and value type (`cities`, `window` = `{"last_days"}` or `{"from", "to"?}`, `categories_window`, `count`, `amount`), the current cities and categories, and the rule limits (`max_count` applies to `order_count`, `max_amount` to `total_spend` and `cart_value`). Rules the catalog cannot describe come back as field errors, e.g. `last_order` `before` takes no `to` date. |
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
| `GET /api/campaigns/{id}/events` | Live status as a `text/event-stream` (D16, D36): a `status` snapshot at once and then whenever it changes (checked every second), with the campaign's status and times, `approval_estimate`, and per channel the actual audience, counts by message status, `groups` (the seven display groups, which add up to the audience: sent, uncertain, failed, skipped, cancelled, retrying, pending; D46), `reasons` (reason codes per group, such as `provider_rejected`, `retries_exhausted`, `character_limit_exceeded`, `timeout`), retries, 429s (`rate_limited` in total, `rate_limited_recent` in the last 5 minutes), skip reasons, how many a resend would reach now and any active resend. `retries` counts attempts after a message's first. Once the campaign is completed or cancelled and no message is in flight, the final snapshot is followed by `end` and the stream closes; close the `EventSource` then, or the browser reconnects. On a server shutdown or a database error the stream closes without an event, and the browser's reconnect picks it up again. A resend created after `end` is not streamed; fetch the campaign again. |
| `GET /api/campaigns/{id}/recipients?group=&channel=&q=&page=` | The messages in one display group (`group` required: sent, uncertain, failed, skipped, cancelled, retrying, pending; D46), optionally for one `channel` and a name search `q` (≤ 50 characters, matches first and last name, Turkish letters and case folded). 50 rows per `page` (1–1000), newest attempt first. Returns `as_of`, the group counts per channel, the reason counts for the group, `total`, and per row: name, masked contact (`+90 555 ••• •• 17`, `ays•••@example.com`), channel, reason code, the length over the limit for a character-limit skip, the next attempt time when retrying, and the attempts (time, outcome, HTTP status). No full phone or email, city, message text or error text is returned, and the response is `Cache-Control: no-store` (D47). Rows can move between pages while a campaign is sending. |
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

## Frontend

Vue 3, Vite, TypeScript and vue-router (D25), with Reka UI for accessible dialogs and menus and our own CSS (D41), in `web/`. The api serves the built app from `WEB_DIR` (`/srv/web` in the image): page URLs get `index.html` so the router can handle them, hashed assets are cached for good, and a missing asset is a 404. `/api/` routes are unaffected.

So far (T15): sign-in with field validation and a show/hide password toggle; every other page requires a session, and a page opened while signed out returns there after sign-in (only paths on this site are accepted as the return address). An API call that finds the session expired sends the marketer to sign in again. The sidebar shows the AI mode from `GET /api/me` and has a user menu with log out.

The composer (T16): **+ New campaign** takes a brief and the channels, and the AI turns the brief into a draft (an unsupported brief is explained and kept for rephrasing; nothing is saved). The draft's audience step shows the rules as plain-language chips ("Added to cart in the last 30 days (counted from send time)", "Ordered books since 1 Oct 2026"): click a chip to edit it in a dialog built from `GET /api/catalog`, switch a time rule between "last N days" and a fixed date, add rules from the catalog, or remove them; the consent rule is always applied. The estimated audience per channel updates after each change. The channels and rules save automatically, one change at a time with the draft's version; if someone else changed the draft, the page says so and asks to reload. The page title is the start of the brief (D43); the full brief is in the brief card. Removing a channel that has content, re-interpreting the brief and deleting the draft each ask for confirmation first. The content step (T17): **Write with AI** streams the SMS and email into the editors as the AI writes them (Stop keeps the earlier messages; rewriting asks first, since it replaces edits). The messages are edited by hand with `{{first_name}}`, `{{last_name}}` and `{{city}}` buttons and save automatically once every message is filled in: when a field loses focus, after a short pause in typing, and before leaving the step (Back, a link, another step). If they can't be saved (an empty message, a failed save), leaving asks first, and closing or reloading the tab brings up the browser's own leave prompt. Writing with the AI asks before replacing any text, saved or not. If someone else changes the draft while the AI is writing, the page says so and asks to reload instead of overwriting their change. Each field counts characters with a long fictional name filled in; the preview shows the message for two clearly fictional customers (no real customer data is shown), with the SMS encoding and segments as information. The backend's estimate of how many messages would be skipped for length is shown per channel. After the brief is re-interpreted, the step warns that the messages may no longer match it.

The review step (T17): rules, the estimated audience, the messages and the over-limit estimate in one place, and the send choice. **Sending always needs an explicit decision:** *Send now* or *Schedule* (a date and time in the browser's zone, sent as UTC; between 1 minute and 30 days ahead), a tick that the audience and messages were checked, and a second confirmation that repeats the estimates. A time suggested from the brief is preselected only if it is still usable, labelled as a suggestion with the brief's own words; otherwise nothing is selected. Until everything is in place, the approve button lists what is missing.

The dashboard and the campaign list (T18): the dashboard counts campaigns by status (each card opens the list filtered by it) and lists six campaigns, live and waiting ones first, each with its next step. The campaign list filters by status and searches the brief (ignoring case and Turkish letters); both are kept in the address, so a reload or Back keeps them. Each row shows the channels, status, creation day and send time, links a resend to its original campaign, and offers what the status allows: delete a draft or cancel an approved or sending campaign (each asks first), or open it. Search and filters work on the newest 200 campaigns the API returns (D49). The campaign page after approval, live delivery, recipient details and resend are still being built (T18).

Development with hot reload, against the api from `docker compose up`, without a local Node:

```sh
docker run --rm -it -p 5173:5173 -v "$PWD/web":/web -v scc-web-node-modules:/web/node_modules -w /web \
  -e API_URL=http://host.docker.internal:8080 node:22-alpine sh -c "npm ci && npx vite --host 0.0.0.0"
```

Then open `http://localhost:5173`. The Vite server proxies `/api` to the api, so the session cookie stays same-origin. On Linux, add `--add-host=host.docker.internal:host-gateway` to the `docker run` command (Docker Desktop provides that name itself; checked on macOS).

### Languages (D44)

The UI is in English by default and in Turkish on request: the switch is on the sign-in page and at the bottom of the sidebar, and the choice is kept in the browser. Numbers, amounts and dates follow the language (`10,000` / `10.000`, `1 Oct 2026` / `1 Eki 2026`). Only interface text is translated. Briefs, customer and seed data (names, cities, categories) and anything the AI wrote (messages, an unsupported brief's reason) are shown exactly as stored, in whatever language they were written; rule chips phrase a rule in the UI language without changing its values. Errors are explained in the UI language for the situations the UI knows; anything else (including most 409 conflicts) keeps the server's English details under a translated heading.

Adding UI text in any screen:

1. Add a key to `web/src/i18n/en.ts` and the same key to `web/src/i18n/tr.ts` (the type check fails if one is missing). Use `{name}` placeholders for values, and `{ one, other }` for text that depends on `{count}`.
2. In a component, `import { t } from '../i18n'` and write `{{ t('my.key', { name }) }}`; format with `formatNumber`, `formatAmount`, `formatDate`, `formatOr` and `formatAnd`. Show API errors with `errorText(err)` from `web/src/i18n/errors.ts`, and an unsupported brief's reason with `aiReason(err)`, unchanged.
3. Pass data through placeholders; never put it in a dictionary.

`make test` scans every component (text, user-facing attributes, literals in template expressions) and module for phrases that don't come from `t()` (`web/src/i18n/coverage.test.ts`, which also tests the scanner against known patterns). It is a heuristic: it catches phrases and capitalised words. A developer-only message, never shown to marketers, is marked with an `i18n-ignore` comment on its line.

## UI/UX design

The reference design is `design/smart-campaign-composer-reference.html`, a Claude Artifact (a self-contained HTML page; open it in a browser). It has ten boards: the journey overview with the status vocabulary, sign in, dashboard, campaigns, the composer's audience, content and review steps, live delivery, recipient details, and resend.

How it was made and is used:

1. **From requirements to a journey.** The flow follows `CASE.md` and the decisions: brief → editable audience rules → streamed content → explicit approval → monitoring. Numbers before sending starts are labelled estimates; numbers after it are actual message outcomes (D30).
2. **Prototype.** The screens were prototyped as an artifact with mock data. Actions the API might not support were marked "CONFIRM API".
3. **Review against the backend** (22 Oct 2026). Every screen was checked against the API. The gaps led to draft deletion, channel changes, the rule catalog and brief re-interpretation (D37–D40), and to the changes listed below.
4. **Implementation.** The colours, spacing, radii and type scale are the CSS tokens in `web/src/styles.css`. Each plan task builds its screens from the matching boards (T15 sign-in and shell; T16 audience; T17 content and review; T18 dashboard, live delivery and resend).

Kept throughout: estimate vs actual always labelled; nothing sends without an explicit choice of *Send now* or *Schedule* and a second confirmation; one status vocabulary; mock mode always visible; labelled fields with errors next to them, visible focus and keyboard-usable menus.

Changes from the design so far:

- **Sign in:** no demo email and password on the page and no "fill" button. The design's own notes say not to hard-code them, and the backend never exposes a password; the page points to this README instead. The badge mentions only the mock providers, because the AI mode is known only after sign-in.
- **User menu:** the email under the name instead of a role, since all marketers share one role (D10).
- **Language switch** (D44): the design has none; it sits above the sign-in form and at the bottom of the sidebar.
- **Preview people:** the design shows real-looking customers ("Shown as Ayşe Demir", "longest name in this group"); the preview uses two clearly labelled fictional customers instead, and the real impact of long names comes from the backend's over-limit estimate (no customer data on screen).
- **Approval:** the design's approve dialog and review page are one flow here: the choice of when and the review tick sit on the review page, then one confirmation dialog repeats the estimates.
- **Saving drafts:** every change saves itself through the API with the draft's version, so a "Saving… / All changes saved" indicator replaces the *Save draft* button and the unsaved-changes dialog. Only the brief text waits for *Re-interpret*.
- **Rule preview:** the design previews the count while a rule is edited; counting unsaved rules would need another endpoint, so the count updates when the rule is applied and shows the change ("−82 since your last edit").
- **Dropped notes:** "‘20% discount’ was used for the content, not the audience" (the AI gives no such explanation) and "N without consent" (the counts already exclude anyone without consent).
- **Campaign names:** there is no separate name (D43). Lists show the brief's first ~60 characters, cut at a word with "…", and a campaign's page shows the full brief.
- **Building rules without the AI** for a new campaign is not offered yet: an unreadable brief is rephrased.
- **Rule editor:** a modal dialog instead of the design's popover beside the chip. A dialog keeps focus inside it, closes with Escape and returns focus to the chip, and fits narrow screens.
- **Breadcrumb:** "Campaigns / New campaign" is replaced by a back button and the page title.
- **Rules help text** reads "Click a rule to edit it" rather than "Suggested by Mock AI", which stops being true once the marketer edits a rule.

Differences to settle when the later screens are built (not decided yet):

- **"Uncertain":** in the backend it is a flag on a message (D5), not a status; a message ends as sent, failed, skipped or cancelled, so the dashboard shows uncertain deliveries as a separate count (T18).
- **Recipient details:** no API lists recipients or their attempt history yet (T18).
- **429 counts:** the status stream has totals per channel, not "in the last 5 minutes" (T18).
- **Resend:** the design approves a shorter SMS in one dialog; in the backend a resend is a draft that goes through content and approval (D31), so it can reuse the T17 screens (T18).
- **Archive:** not built (deferred; see Future improvements).

## Test

```sh
make test         # in Docker: throwaway Postgres + Go tests, then Vitest and the frontend type check
make test-local   # needs local Go and DATABASE_URL pointing to a Postgres (Go tests only)
make lint         # in Docker: gofmt check + go vet
make e2e          # opt-in browser tests (Playwright) against the app from `docker compose up`
```

`make e2e` runs the browser suite in `e2e/` in Microsoft's Playwright image (pinned to 1.64.1; nothing is added to the app) against the running api, with the demo login: sign-in and the login guard, the audience step, autosave races (slowed and failing saves, switching campaigns, changes made elsewhere), English and Turkish, the content and review steps (streaming and Stop, no lost edits when leaving, conflicts during generation, explicit approval), and the dashboard and campaign list (status counts, filters and search in the address, delete and cancel with a confirmation). It is not part of `make test` because it needs the running app, in mock mode: its checks rely on the mock AI's answers, and it refuses to run against the real AI (which would cost API credits). It creates its own campaigns and cleans up: drafts are deleted and approved ones cancelled, so nothing is sent; the cancelled test campaigns stay in the list. Set `SHOTS=/e2e/shots` to keep screenshots.

Integration tests fail, never skip, when no database is available. `internal/e2e` runs the API, the worker and the mock provider together on a seeded database: a send through a provider that fails, rate-limits and loses responses, a scheduled send, and an over-limit send followed by a resend. Its lifecycle tests cover timeouts and replays, worker shutdowns and crashes, cancellation races, consent changes, length skips, resends after a cancelled resend, retry limits and two workers on one queue.

Performance of the recipient API and the status stream on 3,000 customers, with the benchmark that measures them and the query plans behind the changes: `docs/performance.md`.

The first run compiles all dependencies and takes a few minutes; compiled packages are kept in the `gobuildcache` volume (and in BuildKit cache mounts for image builds), so later runs take seconds. `docker compose down -v` also clears these caches.

## Architecture (current)

One Go module (`cmd/scc`) built into one image and run as separate Compose services (D20): `postgres`, `migrate` (one-shot: migrations + seed), `api`, `worker`, `mock-provider`. `internal/config` reads environment variables, `internal/obs` serves health and metrics and counts HTTP requests, `internal/db` holds the embedded migrations and the seed, `internal/auth` the login, logout and session check. `internal/audience` validates audience rules against a fixed catalog (D14) and turns them into parameterized SQL; rule values only ever reach Postgres as query arguments. `internal/provider` is the mock provider. `internal/worker` holds the queue (the messages table, claimed with `FOR UPDATE SKIP LOCKED` and a 30 s lease, D5, D18) and the dispatcher. `internal/spa` serves the built frontend. `internal/content` validates message templates (only `{{first_name}}`, `{{last_name}}`, `{{city}}`; SMS ≤ 160, subject ≤ 80, email body ≤ 2,000 characters after personalization) and estimates over-limit messages per channel in SQL.

## Future improvements

- **AI-generated campaign titles:** the AI could suggest a short title in the same answer that interprets the brief, with the brief's start as the fallback and manual edits kept. Deferred to keep the scope small (D43).
- **Archiving campaigns:** hide finished campaigns from the list. Not built: deleting drafts removes most of the clutter, and archiving can be added later without changing how deletion works (D37).

## Known limitations

- Each open status stream polls the database about once a second (around five small queries, more when a resend count is shown; about 20 ms of database time for a 6,000-message campaign, see `docs/performance.md`), and nothing limits how many streams are open. Fine for a demo; a shared poller per campaign is the upgrade path.
- If no worker is running, a cancelled campaign's stream stays open until its in-flight messages are reclaimed.
- Metrics count per process since it started.
- The Inter font from the design is not bundled: the app uses Inter when it is installed and the system font otherwise. There is no frontend linter yet; `make test` runs the TypeScript type check.
- AI calls are not rate-limited. Every create or brief edit can cost up to two AI calls (one retry), parallel edits of the same draft each make their own call before the version check at save rejects all but one, and an unsupported answer can be asked again at once. Mock mode costs nothing; in real mode every call uses API credits.
