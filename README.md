# Smart Campaign Composer

AI-assisted SMS and email campaign tool: Claude suggests the audience and content, the backend validates, the marketer approves, the system sends to mock providers. Requirements: [`CASE.md`](CASE.md). Decisions: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/2026-10-21-implementation-plan.md`](docs/plans/2026-10-21-implementation-plan.md).

> **Status: work in progress.** Done: backend T1–T14 (login, audience rules, AI interpretation and content with a mock and Claude, drafts, approval, cancel and resend, mock providers, sending worker, metrics and the live status stream) plus the draft endpoints the frontend needs (D37–D40), the frontend app shell with sign-in (T15), the composer's audience step (T16), its content and review-and-approve steps (T17), and English/Turkish UI (D44), and for T18 so far the delivery groups and recipient API (D46, D47), the dashboard and campaign list, and the campaign page after approval with live delivery, the recipient details drawer and resend (T18 done), T19 (verification, security, CI and performance), T20 messages in each customer's language (D64–D67), and T21 the optional A/B test (D68). Audit remediation (D69) done. Still to do: the exploratory QA (T19 step 8), after the feature work.

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
| `GET /api/catalog` | The rule language for building rules by hand (D39): every field with its operators and value type (`cities`, `window` = `{"last_days"}`, `{"last_months"}` (calendar months, D70) or `{"from", "to"?}`, `categories_window`, `count`, `amount`, and `amount_window` / `count_window` = `{"amount"}` / `{"count"}` plus a window, for `spend_in_window` and `orders_in_window`), the current cities and categories, the rule limits (`max_count` applies to order counts, `max_amount` to spend and cart value, `max_days` and `max_months` to windows). Spend and order counts compare with `gte`, `lte`, `gt` or `lt` (cart value: `gte`, `gt`); `signed_up` takes `within` or `before` (D70), and the content `languages` (code and own name: `tr` Türkçe, `en` English; D64). Rules the catalog cannot describe come back as field errors, e.g. `last_order` `before` takes no `to` date. |
| `POST /api/campaigns` `{"request", "channels", "time_zone"}` | The AI reads the brief and a **draft** is saved with its audience rules, current per-channel counts, and a proposed send time if the brief named one (only a proposal; nothing is scheduled). Add `"rules": [...]` to build the audience by hand without the AI. |
| `DELETE /api/campaigns/{id}` `{"version"}` | Delete a draft and its content for good (204). Deleting an abandoned resend draft lets that channel be resent again. Approved, sending, completed and cancelled campaigns are never deleted (409); stop a send with cancel (D37). |
| `PUT /api/campaigns/{id}/request` `{"request", "version"}` | Edit a draft's brief and let the AI read it again (D40). On success the brief, rules and proposed send time are replaced (rules edited by hand are overwritten); the content is kept unchanged and every language is marked as needing review again, "may be outdated" (D66). A stale version is refused before the AI is called. Failures return the same errors as create and leave the draft unchanged. Not for resends (409). |
| `PUT /api/campaigns/{id}/rules` `{"rules", "version"}` | Replace the draft's rules; they are validated and recounted. |
| `PUT /api/campaigns/{id}/ab` `{"enabled", "version"}` | Turn a draft's A/B test on or off (D68). Off by default; not for resends (409) or while text without a recorded language exists (422). Turning it off keeps version B's text, not sent; turning it on again, each language with that text needs review. |
| `PUT /api/campaigns/{id}/channels` `{"channels", "version"}` | Change a draft's channels, validated as on create; counts are refreshed. A removed channel's text is kept, not sent, and comes back when the channel is chosen again, with each language that has it needing review again (D67); a newly added channel has no content until it is written, and approval waits for it. A resend's channel is fixed (409). |
| `PUT /api/campaigns/{id}/schedule` `{"send_at", "version"}` | Change or clear (`null`) the proposed send time: RFC 3339 with offset, between now and 30 days ahead. |
| `GET /api/campaigns?status=&q=&page=` | The campaign list (D54): 25 per page, newest first, optionally one `status` and a search `q` in the brief (≤ 50 characters, ignoring case and Turkish letters; `%` and `_` are literal). Returns `campaigns` (a resend also carries its original's brief, `parent_request_text`), the `total` for the filter, and `counts` per status over all campaigns. |
| `GET /api/dashboard` | The status `counts` and the six campaigns that need attention first: sending, drafts, scheduled by send time, then the rest, most recently changed first (D54). |
| `GET /api/campaigns/{id}` | One campaign with `ab_test` and, when written, `content_b` (version B per language, D68), its `content` per language (including kept text of channels not chosen, D67), `default_language` and each language's review state (`languages`: `approved_at`, `outdated`). A draft also has its current counts (with `by_language`), the suggested languages (`recommendation`, never a restriction) and `delivery`: per channel and language, how many get that text as their own language and how many as the default (`own`, `fallback`). |
| `POST /api/campaigns/{id}/content` `{"version", "languages", "default_language"?, "replace", "variant"?}` | The AI writes the messages for the chosen channels in each listed language, in one call, whatever language the brief is in (D64). With `"variant": "b"` it writes an A/B test's version B only, as an alternative to each language's version A (422 without a test or without version A; D68). Other languages are left as they are. A language that already has text is rewritten only with `"replace": true` (409 otherwise, D66). The response is a `text/event-stream`: `delta` `{field, text}` events while it writes (fields such as `en.sms.body`, `tr.email.subject`), `retry` `{errors}` if the first answer was invalid, then `done` `{content, version}` or `error` `{message}`. Content is saved only when valid and only if the client is still connected. |
| `PUT /api/campaigns/{id}/content` `{"content", "default_language", "version"}` | Save messages edited by hand, per language: `{"tr": {"sms": {"body"}, "email": {"subject", "body"}}, "en": {...}}`. The set replaces the saved one (a language left out is removed); the default must be among them and complete. A language whose text changed needs review again; one saved unchanged keeps its approval (D66). With an A/B test, `content_b` (same shape, only languages that have version A) is version B's set; left out, version B is unchanged (D68). |
| `POST /api/campaigns/{id}/languages/{lang}/approve` `{"version"}` | The marketer approves one language's text as it is, with no AI call and no new version (D66). Only valid text can be approved. |
| `GET /api/campaigns/{id}/estimate` | Current audience and over-limit counts per channel and per language (`by_language`), with `delivery` and `recommendation` as above, labelled as estimates (draft or approved). With an A/B test, `over_limit_b` measures version B's text over the whole audience (about half gets it). |
| `POST /api/campaigns/{id}/approve` `{"version", "send"}` | `send` is required: `"now"` or `{"at": "<RFC 3339>"}` (1 minute to 30 days ahead); a proposed time is never used implicitly. Checks content, that every language is approved (D66) and a non-empty audience, freezes rules and content, and stores the current counts as `approval_estimate`. No messages are created yet: the audience is evaluated again when sending starts (D30). |
| `POST /api/campaigns/{id}/cancel` | Stops an approved or sending campaign; messages still waiting are cancelled. Drafts are deleted instead. |
| `GET /api/campaigns/{id}/events` | Live status as a `text/event-stream` (D16, D36): a `status` snapshot at once and then whenever it changes (checked every second), with the campaign's status and times, `approval_estimate`, and per channel the actual audience, counts by message status, `groups` (the seven display groups, which add up to the audience: sent, uncertain, failed, skipped, cancelled, retrying, pending; D46), `reasons` (reason codes per group, such as `provider_rejected`, `retries_exhausted`, `character_limit_exceeded`, `timeout`), retries, 429s (`rate_limited` in total, `rate_limited_recent` in the last 5 minutes), skip reasons, how many a resend would reach now and any active resend, and for an A/B test `by_variant` (per version: audience, groups and skip reasons; delivery data only, D68). `retries` counts attempts after a message's first. Once the campaign is completed or cancelled and no message is in flight, the final snapshot is followed by `end` and the stream closes; close the `EventSource` then, or the browser reconnects. On a server shutdown or a database error the stream closes without an event, and the browser's reconnect picks it up again. A resend created after `end` is not streamed; fetch the campaign again. |
| `GET /api/campaigns/{id}/recipients?group=&channel=&language=&variant=&q=&page=` | The messages in one display group (`group` required: sent, uncertain, failed, skipped, cancelled, retrying, pending; D46), optionally for one `channel`, one `language`, one A/B test version (`a` or `b`) and a name search `q` (≤ 50 characters, matches first and last name, Turkish letters and case folded). 50 rows per `page` (1–1000), newest attempt first. Returns `as_of`, the group counts per channel, the reason counts for the group, `total`, and per row: name, masked contact (`+90 555 ••• •• 17`, `ays•••@example.com`), channel, the language whose text it got (`und` for messages from before languages were recorded), its A/B test version (`variant`, else null), reason code, the length over the limit for a character-limit skip (measured on that language's and version's text), the next attempt time when retrying, and the attempts (time, outcome, HTTP status). No full phone or email, city, message text or error text is returned, and the response is `Cache-Control: no-store` (D47). Rows can move between pages while a campaign is sending. |
| `POST /api/campaigns/{id}/resend` `{"channel"}` | After a campaign completes or is cancelled: a draft follow-up for the recipients skipped for the character limit on that channel, minus anyone who may already have it (sent, still sending, or with an uncertain delivery). One active resend per campaign and channel. |

Messages may use only `{{first_name}}`, `{{last_name}}` and `{{city}}` and need some fixed text; text that is over the limit even with every variable empty, and control characters other than tabs and line breaks, are rejected. Values are inserted as one line (control characters become spaces, the ends are trimmed), and a space an empty value leaves before `,` `.` `!` `?` is dropped, so "Merhaba {{first_name}}," with no name reads "Merhaba," (D72). A value that itself contains a variable token (e.g. a name saved as `{{city}}`) is expanded too; sending, the over-limit estimate and the preview all render the same way. AI-written SMS and email bodies must greet the recipient with `{{first_name}}` (otherwise the answer is retried once); the marketer's own text has no such rule. `channels` is `["sms"]`, `["email"]` or both; `time_zone` is the browser's IANA zone (e.g. `Europe/Istanbul`). Edits send the `version` they were based on; a stale version or a non-draft campaign returns 409. Invalid input returns 422 (with `field_errors` for rules); an "unsupported" brief returns 422 with `"unsupported": true` and the AI's reason, and nothing is saved. For `POST /api/campaigns`, if the AI answers invalidly twice, times out or is unavailable, the API returns 502/504 and nothing is saved; a 502 carries `code` `ai_invalid` (the answer failed validation twice) or `ai_unavailable` (the provider failed, e.g. a rejected API key or an outage), so the UI tells them apart. Content generation reports the same failures as an `error` event in its stream, with the same `code`.

```sh
curl -b cookies.txt -X POST localhost:8080/api/campaigns -d '{"request":"Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.","channels":["sms"],"time_zone":"Europe/Istanbul"}'
```

### Trying the real AI (manual check)

Tests never call Claude. To check real mode by hand: set `LLM_MODE=real` and `ANTHROPIC_API_KEY` in `.env`, run `docker compose up --build`, log in as above, and post a free-form brief (e.g. "Send a thank-you note to customers in Izmir who spent at least 2,000 TL"). Expect a draft with matching rules, or `"unsupported": true` with a reason for a brief the rule catalog can't express (e.g. one that mentions age). `LLM_TIMEOUT` (default `60s`) limits each AI call. Each call costs real API credits. Each marketer can make at most `AI_CALLS_PER_HOUR` (default 10) AI calls in any hour, retries included (D63).

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
| Send a loyalty reward to customers who spent more than 10,000 TL in the last 12 months with at least 4 orders in that period, no orders in the last 45 days, and who did not sign up in the last 7 days. | Son 12 ayda 10.000 TL'den fazla harcayan, bu sürede en az 4 sipariş veren, son 45 gündür sipariş vermeyen ve son 7 günde üye olmamış müşterilere sadakat ödülü gönder. |
| Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00. | Ankara'daki tüm kullanıcılara hafta sonu indirimi duyurusu gönder. Yarın saat 10:00'da gönder. |
| Send a reminder to users who added products to their cart since 2026-10-01 but did not buy anything. | 2026-10-01 tarihinden beri sepetine ürün ekleyip satın almayan kullanıcılara hatırlatma gönder. |

The real AI (`LLM_MODE=real`) follows the same rule: if part of an audience cannot be expressed with the rule catalog, it answers "unsupported" instead of guessing.

## Mock provider

The `mock-provider` service plays the SMS and email provider; nothing is really sent. `POST /sms/send` `{"to", "body"}` and `POST /email/send` `{"to", "subject", "body"}` require an `Idempotency-Key` header (the message ID). Per channel it adds a delay and fails a share of requests, with the defaults from D20: 20–200 ms delay, 5% 5xx, 3% 429 with `Retry-After: 1`, 1% requests that hang past the worker's 5 s timeout and are then accepted (a lost response). Change them with the `SMS_*` / `EMAIL_*` settings in `.env.example`; `PROVIDER_SEED` makes runs repeatable.

The key is stored before anything is simulated. A repeated key gets the stored result for a finished request, or a retryable 503 with `Retry-After` while it is still in progress; a 5xx or 429 releases the key so a retry is processed. Keys live in the `provider` schema, which the provider reaches through its own database role (`scc_provider`, password `PROVIDER_DB_PASSWORD`) that cannot read the app's tables (D6).

## Sending

The `worker` service starts approved campaigns when their send time comes, evaluating the audience at that moment (D30), and sends each message through the provider with its message ID as `Idempotency-Key`. Before every request it re-checks that the campaign is still sending and the recipient still allows the channel, and it personalizes and length-checks the text: over-limit messages are skipped (`character_limit_exceeded`) and can be resent later (D31). Each message uses the text of the language fixed for it when the audience was built (D64). 200 → `sent`; 429, 5xx, timeouts and network errors are retried with exponential backoff and jitter (honouring `Retry-After`) up to `MAX_ATTEMPTS` (5); other 4xx → `failed`. Every attempt is recorded in `delivery_attempts`.

A request without a clear answer (a timeout or a network error) may still have been delivered, so the message is marked `delivery_uncertain`; the retry uses the same key and gets the provider's stored result instead of a second send. The same holds when a worker died mid-attempt (its expired lease is handed back by the running worker within a second of expiring). If the campaign is cancelled or the recipient opts out before that retry, the message is recorded as cancelled or skipped with a note that delivery is uncertain, and a later resend leaves them out (D31). Stopping the worker interrupts in-flight requests: they are recorded as `interrupted`, do not count toward `MAX_ATTEMPTS`, and the message is retried at once; messages still waiting for a rate-limit slot go straight back to the queue.

Per channel at most 100 provider requests leave in any second, retries included: a rate limiter spaces them 10 ms apart and a hard cap counts each departure after the last database check, right before the request leaves (D19, D69). The tests check both the recorded departures and what the provider received. Network delays of more than 50 ms between the worker and the provider could in theory still bunch requests at the provider.

The limit lives in the worker process, so only one worker sends: it holds a Postgres advisory lock (D69). A second worker (`docker compose up --scale worker=2`) logs that it is waiting and takes over when the first stops. A worker that takes the lock waits about a second before its first send, so its first second and the previous worker's last one can't add up to more than 100 requests. If the lock's connection fails, the worker stops sending within about 1–3 s and takes the lock again before it continues; if its host vanished instead, the database frees the lock in about 25 s. A message that can't be read before sending (a failed query, or shutdown) goes back to the queue, not flagged uncertain; if the database stays unreachable, the result can't be written either, and the message is flagged uncertain when its lease expires, as after a crash.

## Metrics

Every process serves Prometheus metrics at `/metrics` (D26), alongside the standard Go and process metrics:

| Metric | Process | Labels |
|---|---|---|
| `scc_http_requests_total`, `scc_http_request_duration_seconds` | all | `route` (the matched pattern, e.g. `GET /api/campaigns/{id}`, or `unmatched`), `code` |
| `scc_send_attempts_total` | worker | `channel`, `outcome` (`accepted`, `rate_limited` = HTTP 429, `server_error`, `timeout`, `network_error`, `rejected`, `interrupted`) |
| `scc_send_retries_total` | worker | `channel`: messages scheduled for another attempt after a failed one (an interrupted attempt is not counted, unlike the stream's `retries`) |
| `scc_provider_request_duration_seconds` | worker | `channel` |
| `scc_llm_calls_total` | api | `operation` (`interpret`, `content`), `result` (`ok`, `invalid` = failed validation and retried once, `error`, `cancelled` = the client left) |
| `scc_llm_call_duration_seconds` | api | `operation`: every AI call's duration, failed calls included (D72) |
| `scc_llm_first_text_seconds` | api | `operation` (`content`): the wait until the first streamed text |

With `LLM_MODE=real`, each Claude call also writes a `claude call` log line (`docker compose logs api`) with `seconds`, `input_tokens`, `output_tokens`, `stop_reason` and, for content, `first_text_seconds` and `text_chars`; output tokens include thinking, so comparing them with the text length shows how long the AI thought before writing. The prompt and the answer are never logged.

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

The composer (T16): **+ New campaign** takes a brief and the channels, and the AI turns the brief into a draft (an unsupported brief is explained and kept for rephrasing; nothing is saved). The draft's audience step shows the rules as plain-language chips ("Added to cart in the last 30 days (counted from send time)", "Ordered books since 1 Oct 2026"): click a chip to edit it in a dialog built from `GET /api/catalog`, switch a time rule between "last N days" and a fixed date, add rules from the catalog, or remove them; the consent rule is always applied. The estimated audience per channel updates after each change. The channels and rules save automatically, one change at a time with the draft's version; if someone else changed the draft, the page says so and asks to reload. The page title is the start of the brief (D43); the full brief is in the brief card. Re-interpreting the brief and deleting the draft each ask for confirmation first; removing a channel keeps its text (D67) and says so. The content step (T17): **Write with AI** streams the SMS and email into the editors as the AI writes them (Stop keeps the earlier messages; rewriting asks first, since it replaces edits). The messages are edited by hand with `{{first_name}}`, `{{last_name}}` and `{{city}}` buttons and save automatically once every message is filled in: when a field loses focus, after a short pause in typing, and before leaving the step (Back, a link, another step). If they can't be saved (an empty message, a failed save), leaving asks first, and closing or reloading the tab brings up the browser's own leave prompt. Writing with the AI asks before replacing any text, saved or not. If someone else changes the draft while the AI is writing, the page says so and asks to reload instead of overwriting their change. Each field counts characters with a long fictional name filled in; the preview shows the message for two clearly fictional customers (no real customer data is shown), with the SMS encoding and segments as information. The backend's estimate of how many messages would be skipped for length is shown per channel.

**Messages in the customer's language** (T20, D64–D67). Each customer gets the messages in their own language (`users.language`) if the campaign has that language, and the default language otherwise. Turkish and English are supported.

- **Audience step:** customers per language and channel, and the suggested languages (any language with at least 5% of the audience, the largest as the default). This is a suggestion only: the marketer can pick any supported language.
- **One step to the drafts (D71):** for a new draft, the languages are ticked on the audience step (the suggestion pre-ticked). **Write the messages with AI →** opens the content step and starts writing those languages once, by itself; reloading, Back or a later visit never starts it again, and it never replaces text. **Write them myself →** opens the content step with those languages ticked and no AI call. A resend or a draft with text keeps the single **Write the messages →** link.
- **Content step:** one tab per language. **Write with AI** writes only the ticked languages that have no text yet, in one call; adding a language later writes that language alone. Each tab has its own review state, **Approve** (no AI call), **Rewrite with AI** (asks first) and **Make default**. After the brief changes, every language is marked "may be outdated" until it is approved again, as it is or rewritten. Text is never cleared or rewritten without the marketer asking.
- **Review:** who gets each language's text (in their own language, or as the default), each language's state, a preview per language and the over-limit estimate per language. Approval needs every language approved.
- **Sending:** each recipient's language is fixed when the audience is built (`messages.language`). Retries keep it; a resend fixes its own recipients' languages from its own languages.
- **Results:** the recipient details show each message's language, with a filter.
- **Campaigns from before T20:** their content shows as "Language not recorded", is sent to everyone as before, and can be given a language.

**A/B test** (T21, D68). Optional, off by default; not on resends.

- **Content step:** *Compare two versions (A/B test)* adds an **A | B** switch inside each language tab. Version B is written by hand, started with **Copy version A**, or written with **Write version B with AI** (only for languages without one, never touching A). Approving a language approves both versions; editing either one needs review again.
- **Split:** when the audience is built, each language's unique recipients are ordered by `md5(campaign:user)` and alternated A, B, so the split is 50/50 within one recipient per language, reproducible, and the same on every channel. The version is stored on each message (`messages.variant`); retries reuse it.
- **Text after approval:** an approved campaign's content can't be edited, so every attempt, retries included, sends the approved text of the recipient's language and version.
- **Review:** versions A and B side by side per language, each with its over-limit estimate.
- **Results:** per channel, a table per version (recipients, sent, failed, skipped, too long, waiting or retrying), and a version filter in the recipient details. The page says it has delivery data only: the app records no opens, clicks or orders, so it can't say which version works better, and it names no winner.

The review step (T17): rules, the estimated audience, the messages and the over-limit estimate in one place, and the send choice. **Sending always needs an explicit decision:** *Send now* or *Schedule* (a date and time in the browser's zone, sent as UTC; between 1 minute and 30 days ahead), a tick that the audience and messages were checked, and a second confirmation that repeats the estimates. A time suggested from the brief is preselected only if it is still usable, labelled as a suggestion with the brief's own words; otherwise nothing is selected. Until everything is in place, the approve button lists what is missing.

The dashboard and the campaign list (T18): the dashboard counts campaigns by status (each card opens the list filtered by it) and lists six campaigns, live and waiting ones first, each with its next step. The campaign list filters by status and searches the brief (ignoring case and Turkish letters); both are kept in the address, so a reload or Back keeps them. Each row shows the channels, status, creation day and send time, links a resend to its original campaign, and offers what the status allows: delete a draft or cancel an approved or sending campaign (each asks first), or open it. The server filters, searches and pages the list (25 per page) and counts the statuses over all campaigns (D54).

After approval, the campaign's page shows its delivery (T18), all from the live status stream (D36):

- **Scheduled:** when sending starts, the audience rules and the estimate made at approval (the actual audience is calculated when sending starts, D30), the approved messages (locked), and *Cancel campaign…*.
- **Sending, completed or cancelled:** estimate vs actual audience; totals for sent, not sent, uncertain and in progress; per channel a progress bar and the seven groups with their reasons (D46), retries and 429s (in the last 5 minutes and in total). While sending, the counts update live with the time they were taken; *Live · pause* stops and resumes updates, and *Cancel campaign…* stops what hasn't been sent. A finished campaign shows read-only results and closes the stream. A campaign cancelled before sending says no messages were created.
- A resend links to its original campaign, and the original links to an active resend.
- **Recipient details** (D47): every count on the delivery page opens a side panel listing the recipients in that group, with tabs for the seven groups and their counts, a channel switch, a name search (ignoring case and Turkish letters), the reasons and what they mean, and per recipient the masked contact, the reason (with the length for a character-limit skip), the attempt count and an expandable attempt history (time, HTTP status, outcome). 50 rows per page, newest attempt first. It shows a snapshot taken when it opens or a filter changes, with that time. The group, channel, search and page are in the address, so Back closes it, Escape returns focus to the count that opened it, and a link opens it directly. Contacts are masked by the server; no full phone number or email reaches the browser.
- **Resend** (D31, D48): once a campaign has finished, each channel with recipients skipped for the character limit offers *Resend to ~N…* (also on the recipient panel's Skipped tab). The dialog shows how many were skipped, how many can be reached now (an estimate, checked again when the resend starts) and who is never included: consent revoked, uncertain deliveries (they may already have it) and failures. *Create the resend draft* opens the follow-up's content step, where the original message can be shown; it is approved on the review step like any campaign. The original then links to the resend, the resend and the campaign list link back, and a second resend isn't offered while one is active. If someone else created it meanwhile, the dialog says so and the page shows the existing one.

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

- **"Uncertain"** (D46): in the backend it is a flag on a message (D5), not a status. The screens show it as its own group: a message that ended without being sent (failed, skipped or cancelled) after an attempt that may have reached the provider. A sent message counts as sent.
- **429 counts:** shown for the last 5 minutes, as designed, and in total.
- **Recipient details** (D47): contacts are masked by the server, and the search covers names only, never phone numbers or emails. The panel is a snapshot with its time, as designed.
- **Resend** (D48): the design edits and approves the shorter SMS in one dialog. Here the dialog shows who gets the resend and who doesn't, then creates a draft that goes through the content and review steps like any campaign (D31), with the original message viewable. Offered on any channel with length skips once the campaign has finished.
- **Dashboard rows** don't mention resend opportunities; the campaign's page does. The campaign list searches the brief, since there are no names (D43); search, filters and paging run on the server (D54).
- **Archive:** not built (deferred; see Future improvements).

## Test

```sh
make test         # in Docker: throwaway Postgres + Go tests, with Vitest and the frontend type check alongside
make test-local   # needs local Go and DATABASE_URL pointing to a Postgres (Go tests only)
make lint         # in Docker: gofmt check + go vet
make e2e          # browser tests (Playwright) on their own throwaway stack
```

`make test` runs the Go tests and the frontend checks at the same time; the frontend's output is printed after Go's. Each Go test gets its own database, copied from a template that is migrated once per run (much faster than migrating each one), and the `db` and `e2e` packages run their tests in parallel. Frontend packages are reinstalled only when the lockfile changes. On this project's reference machine (Docker Desktop, 8 CPUs) a run takes about a minute; see `docs/performance.md`.

`make e2e` runs the browser suite in `e2e/` in Microsoft's Playwright image (pinned to 1.64.1; nothing is added to the app) on **its own stack** (D53): a separate Compose project (`scc-e2e`, `docker-compose.e2e.yml`) with a fresh, seeded database, mock AI only and a 2-second cap on retry waits (`RETRY_BACKOFF_MAX`; the real 30-second cap stays covered by the Go tests). It starts the stack, runs the checks, and removes the stack, so your local app and its data are never touched and nothing has to be running first. The dashboard check runs first and alone (it compares counts over all campaigns); then the checks that send nothing and the checks that send run side by side, the sending ones one after another so they don't share the provider's 100/s limit. Each file's time is printed at the end.

What it covers: sign-in and the login guard, the audience step, autosave races (slowed and failing saves, switching campaigns, changes made elsewhere), English and Turkish, the content and review steps (streaming and Stop, no lost edits when leaving, conflicts during generation, explicit approval), the dashboard and campaign list (status counts, filters and search in the address, paging across several pages with a filter and a search, delete and cancel with a confirmation), layouts at desktop, tablet and phone widths (no sideways scrolling, the account menu in reach, campaign rows readable in English and Turkish) and signing out with the keyboard, the campaign page after approval (a scheduled campaign and its cancel; live delivery to completion, with counts checked against the API), recipient details (tabs, search, paging, attempt history, masked contacts, Back, Escape and focus, Turkish), resend (the dialog's numbers, the draft through content and review, an audience of exactly the eligible skipped recipients, links both ways, a resend created elsewhere), a send watched mid-way (counts moving, pause and resume) and cancelled while sending (sent messages stay sent, the rest are cancelled, nothing is sent afterwards), and the fixes from the PR #6 review. Several checks really send campaigns through the mock SMS provider of that stack. A full run takes under two minutes. Set `SHOTS=/e2e/shots` to keep screenshots.

`make perf` measures the performance targets (D57) on another throwaway stack (`scc-perf`, `docker-compose.perf.yml`, default settings): a full send watched by 20 live screens, 10 marketers using the API for 2 minutes, and cold loads and interactions in a browser on a desktop and a throttled phone profile. It prints each target with what was measured and takes about 5 minutes. Results and method: `docs/performance.md`.

### Continuous integration (D52)

GitHub Actions (`.github/workflows/`):

| Workflow | When | What |
|---|---|---|
| **CI** (`ci.yml`) | every push and every pull request, and on request | `make test`, `make lint`, and the scan: `make scan-selftest`, the files in the repository, and the new commits |
| **Browser checks** (`e2e.yml`) | every pull request, every push to `main`, nightly at 02:30 UTC, and on request (Actions → Browser checks → Run workflow) | `make e2e` on its own stack; screenshots of failing files are kept for 14 days |

Nothing runs twice for the same commit: a push to a branch with an open pull request is left to the pull request's run, which tests the commit merged with `main`, and a newer commit cancels the older run. The jobs run the same make targets as a developer's machine, in Docker. In CI the Go and npm caches live in the workspace (`.github/ci/compose.cache.yml`), so later runs reuse them. Actions are pinned to commits. The repository is private, so the runs use the account's Actions minutes (about 4 runner-minutes for CI and 4 for the browser checks per pull request update).

### Scanning for secrets and personal data (D51)

```sh
make scan           # the files Git sees and the commits not pushed yet
make scan-selftest  # proves the rules on planted keys, emails, phone numbers and a .env file
make hooks          # once per clone: scan the staged changes before every commit
```

`make scan` runs [gitleaks](https://github.com/gitleaks/gitleaks) (pinned Docker image, nothing installed) with gitleaks' own rules plus the project's rules in `.gitleaks.toml`: Anthropic keys, email addresses, phone numbers and committed `.env` files. Only the project's fictional seed and test data, placeholders, the documented demo login and masked contacts are allowed; anything else blocks the commit, with the value redacted in the output. Session exports in `ai-sessions/` are reviewed before they are committed and genuine secrets or private details are redacted; the owner's public GitHub username and profile links are kept. Exports may quote the scanner self-test's planted fictional contacts, which are allowed only there. `scripts/scan.sh history` scans every commit, as a report.

Integration tests fail, never skip, when no database is available. `internal/e2e` runs the API, the worker and the mock provider together on a seeded database: a send through a provider that fails, rate-limits and loses responses, a scheduled send, and an over-limit send followed by a resend. Its lifecycle tests cover timeouts and replays, worker shutdowns and crashes, cancellation races, consent changes, length skips, resends after a cancelled resend, retry limits and two workers on one queue (the second waits for the sender lock).

Performance of the recipient API and the status stream on 3,000 customers, with the benchmark that measures them and the query plans behind the changes: `docs/performance.md`.

The first run compiles all dependencies and takes a few minutes; compiled packages are kept in the `gobuildcache` volume (and in BuildKit cache mounts for image builds), so later runs take seconds. `docker compose down -v` also clears these caches.

## Architecture (current)

One Go module (`cmd/scc`) built into one image and run as separate Compose services (D20): `postgres`, `migrate` (one-shot: migrations + seed), `api`, `worker`, `mock-provider`. `internal/config` reads environment variables, `internal/obs` serves health and metrics and counts HTTP requests, `internal/db` holds the embedded migrations and the seed, `internal/auth` the login, logout and session check. `internal/audience` validates audience rules against a fixed catalog (D14) and turns them into parameterized SQL; rule values only ever reach Postgres as query arguments. `internal/provider` is the mock provider. `internal/worker` holds the queue (the messages table, claimed with `FOR UPDATE SKIP LOCKED` and a 30 s lease, D5, D18) and the dispatcher. `internal/spa` serves the built frontend. `internal/content` validates message templates (only `{{first_name}}`, `{{last_name}}`, `{{city}}`; SMS ≤ 160, subject ≤ 80, email body ≤ 2,000 characters after personalization) and estimates over-limit messages per channel and language in SQL; it also holds the language catalog (D64).

## Optional features

- **Scheduled sending:** built (D29).
- **Messages in the customer's language:** built (T20, D64–D67); see the content step above.
- **A/B testing:** built as a delivery-only comparison (T21, D68, superseding D65); see the content step above.
- **CI pipeline:** built (D52).

## Future improvements

- **More languages and AI cost:** the design already supports languages beyond Turkish and English: the catalog, per-language content, counts and review. Adding one is a catalog entry, a migration and mock texts. Before expanding, weigh the AI cost: each language adds a full set of messages to the AI's answer, and output tokens are most of the cost (`docs/plans/2026-10-24-t20-ai-cost.md`). Options: keep generating only the selected languages, reuse approved content across campaigns, record tokens per generation, and use a cheaper model for content after a quality check.

- **A/B outcomes:** the stored version per message is what a real outcome would join on. The cheapest is orders placed within an attribution window after `sent_at` (the `orders` table already exists; needs a decision on the window), then tracked links for clicks. A winner needs one of these plus a significance rule (D68).
- **AI-generated campaign titles:** the AI could suggest a short title in the same answer that interprets the brief, with the brief's start as the fallback and manual edits kept. Deferred to keep the scope small (D43).
- **Archiving campaigns:** hide finished campaigns from the list. Not built: deleting drafts removes most of the clutter, and archiving can be added later without changing how deletion works (D37).

## Security

The security review (T19) with its evidence is in `docs/security-review.md`. In short: customer contacts are masked by the server and never cached; the AI never receives customer data; every API route needs a session; failed sign-ins are limited (5 per 15 minutes per email and per address); requests from other sites are refused; the app is served with a strict Content Security Policy; secrets and personal data are scanned before every commit and in CI.

## Known limitations

- The streams of a campaign share one status snapshot (D59): its database cost grows with the campaigns being watched, not with the screens (20 screens on one send: about 25 ms of database time per second, see `docs/performance.md`). Nothing limits how many streams are open, and the sharing is per api process.
- If no worker is running, a cancelled campaign's stream stays open until its in-flight messages are reclaimed.
- Metrics count per process since it started.
- A/B report: delivery data only, so it can't show which version works better (D68). The split is exact per language over unique recipients; per channel it can differ slightly, since consent and contact details differ. Content is fixed by the API once approved; a direct database edit would change what retries send.
- A/B estimate: version B's over-limit count before sending covers the whole audience, though about half gets B, so it is an upper bound. Recipient details (P5) miss the 50 ms p95 target back to back: 55.4 ms in one run after the audit fixes (D69), on a host the test saturates. One request costs about 9 ms; the rest is waiting behind the whole request mix, where the campaign list's queries use the most database time. With a 1 s pause it measured 45 ms in T19, not re-measured since (see `docs/performance.md`, Q6).
- Recipient details page by offset (D47): while a campaign sends, rows can move between pages, and the panel shows a snapshot with its time. Sorting by the stored last attempt (D69) is a top-N sort of the group, about 5 ms for a 5,000-message group; an index on it is the upgrade path.
- After a campaign's status stream has ended, a resend created in another tab is noticed when the page is opened again or a resend fails (D36).
- The Inter font from the design is not bundled: the app uses Inter when it is installed and the system font otherwise. There is no frontend linter yet; `make test` runs the TypeScript type check.
- Sign-in limits are kept in memory per api process and reset on restart; behind a proxy, all clients share its address.
- Real AI calls are limited to `AI_CALLS_PER_HOUR` (default 10) per marketer in any hour, retries included (D63); over the limit the API answers `429` with `Retry-After`. The limit is kept in memory per api process and resets on restart; the Anthropic console's spend limit is the real cost ceiling. Mock AI is unlimited.
