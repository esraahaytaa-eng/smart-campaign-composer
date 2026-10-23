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
- **History:** 2026-10-22 (Milestone D review). Each message records when its delivery is uncertain (an attempt without a clear answer, or a worker that died mid-attempt). An attempt interrupted by a worker shutdown is recorded as `interrupted`, does not count toward the attempt limit, and the message is retried at once with the same key.

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
- **History:** 2026-10-22. The seed creates a single demo login, `marketer@marketer.com` (password from `DEMO_MARKETER_PASSWORD`, default documented in the README), at the engineer's request. The schema still supports several marketers. 2026-10-22: session lifetime of 12 hours approved (was an open question): covers a working day without frequent logins, and a stolen cookie stays useful for at most half a day. 2026-10-23 (roadmap review): kept as is. "Data isolation" means protecting customer data (masking, no customer data to the AI, the provider's own database role, nothing personal in logs or errors), not restricting which campaigns a marketer sees.

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
- **Decision:** Every response is checked against the schema, the catalog (D14), the variables (D12) and the length limits. If invalid, Claude is asked once more with the validation errors. If it is still invalid, or the call times out or the API fails, nothing is saved, a clear error is shown, and the marketer can rephrase or build the rules by hand. Claude replies `unsupported` with a reason for off-topic or impossible requests, and when any part of the requested audience cannot be expressed: conditions are never dropped, approximated or added. The mock LLM follows the same rule and understands only the example briefs listed in the README. Claude never receives user records, only the request, catalog and allowlist. The brief is limited to 1,000 characters: a longer one is rejected before Claude is called, never truncated. If Claude refuses a request, the marketer sees it as unsupported; no fallback model is used. Streamed content is validated once the stream completes. Tests use a mock LLM.
- **Why:** Recovers from most formatting mistakes, never acts on unvalidated output, and keeps personal data away from the LLM.
- **Rejected:** *Failing on the first error:* more failures reach the user. *Keeping the valid parts:* can silently change the audience.
- **Trade-offs:** The retry adds latency and cost. Streamed content is confirmed valid only at the end.
- **History:** 2026-10-21 (planning). `unsupported` made mandatory for briefs that are only partly understood, and applied to the mock: sending to the wrong users is unacceptable. 2026-10-22: brief limit of 1,000 characters approved (was an open question): about 150 words, far more than a brief needs, while keeping AI cost and the space for hidden instructions small. 2026-10-22: the beta server-side refusal fallback was removed at the engineer's request; only stable API features are used.

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
- **History:** 2026-10-22 (Milestone D review). A resend also excludes anyone whose delivery on that channel is uncertain (a message still sending, or an attempt that timed out, failed on the network or was interrupted), so a cancelled resend followed by a new one never sends twice. Cost: a recipient whose message really was lost is not resent. Engineer's choice (option A).

### D32 — AI replies in the request's language
- **Status:** Accepted · 2026-10-21
- **Decision:** Claude and the mock LLM write content in the language of the request, Turkish included.
- **Why:** Marketers write the brief in the language they want to send.
- **Rejected:** *English-only mock:* breaks Turkish demos. *Language per recipient:* optional feature, still deferred.
- **Trade-offs:** Mixed-language briefs depend on Claude's judgement.

### D45 — Recipient details are part of T18
- **Status:** Accepted · 2026-10-23
- **Decision:** T18 includes the design's Recipient details screen (board 5), in English and Turkish (D44). It is backed by a new paginated, filterable API that returns each recipient's delivery status, failure or skip reason and attempt history, with privacy protection for customer data.
- **Why:** Engineer's choice: the counts alone don't show who was affected or why, and the design depends on this screen.
- **Rejected:** *Deferring it (counts and reason tables only):* CASE.md doesn't require it, but the delivery picture would be incomplete.
- **Trade-offs:** A new API that returns customer data, so it needs masking, limits and tests; T18 grows by about one task.

### D46 — Delivery outcomes shown in seven groups, with reason codes
- **Status:** Accepted · 2026-10-23
- **Decision:** The UI shows messages in seven groups that add up to the audience: Sent (`sent`), Uncertain (`failed`, `skipped` or `cancelled` with an uncertain delivery, D5), Failed, Skipped, Cancelled (the rest of those statuses), Retrying (`retry_scheduled`), Pending (`pending` and `sending`). Reasons are codes the UI translates (D44): failed `provider_rejected` / `retries_exhausted`; skipped `character_limit_exceeded` (with the rendered length) / `no_consent`; uncertain by the latest attempt without a clear answer (`timeout`, `network_error`, `interrupted`, or `no_response_recorded` when a worker died mid-attempt); retrying by the last attempt's outcome, or `requeued` when the latest claim was handed back unsent. The status stream (D36) gains the groups, the reason codes and the 429s of the last 5 minutes; its raw error strings and the separate `uncertain` count are removed (the Uncertain group replaces it).
- **Why:** Matches the design's vocabulary; one definition for the live screen and recipient details; provider text is neither translated nor safe to show.
- **Rejected:** *Uncertain as a separate overlapping count:* groups wouldn't add up. *Showing `last_error`:* untranslatable, may contain provider data.
- **Trade-offs:** A sent message whose earlier attempt timed out counts only as Sent; changes the T14 stream's fields.
- **History:** 2026-10-23 (PR #6 review). The reasons are worded as implemented; a retry handed back unsent after an earlier attempt now reads `requeued` instead of that attempt's outcome.

### D47 — Recipient API: paged, name search only, masked contacts
- **Status:** Accepted · 2026-10-23
- **Decision:** `GET /api/campaigns/{id}/recipients?group=&channel=&q=&page=` returns 50 rows per page sorted by last attempt, with group and reason counts, `as_of`, and each row's attempt history (time, outcome, HTTP status). Contacts are masked on the server (`+90 555 ••• •• 17`, first 3 characters of an email's local part + `•••@domain`); search matches names only (Turkish-folded, ≤ 50 characters); no message text, error text or other customer fields; `Cache-Control: no-store`. The drawer is a snapshot.
- **Why:** Implements D45 with the least customer data that still explains each outcome; no contact search means the API can't confirm whether someone is a customer.
- **Rejected:** *Keyset paging:* more code for at most ~6,000 messages per campaign. *Live updates in the drawer:* rows would move under the reader. *Search by phone or email:* enumeration risk.
- **Trade-offs:** Rows can shift between pages while a campaign is sending; offset paging gets slower for much larger campaigns.

### D48 — Resend goes through the composer's content and review steps
- **Status:** Accepted · 2026-10-23
- **Decision:** "Resend" opens a dialog with who gets the resend and who is not included, then creates the follow-up (D31) and opens its content step, with the original message viewable; it is approved on the normal review step. Offered on any channel with length skips once the campaign has finished.
- **Why:** A resend is already a draft with its own approval in the backend; reusing T17 keeps one approval flow.
- **Rejected:** *The design's one-page edit-and-approve:* a second approval flow to build and test.
- **Trade-offs:** One more click than the design.

### D49 — Campaign list filtered in the browser; dashboard without resend counts
- **Status:** Superseded by D54 · 2026-10-23
- **Decision:** The campaigns page filters by status and searches the brief (Turkish-folded) in the browser over the newest 200 campaigns the API returns. The dashboard shows status counts and the six most relevant campaigns; resend offers appear on a campaign's page, not on dashboard rows.
- **Why:** No backend change is needed at this size.
- **Rejected:** *Server-side filtering:* not needed for 200 rows. *Per-row resend counts:* a query per campaign.
- **Trade-offs:** Campaigns beyond the newest 200 aren't listed; the dashboard doesn't point out resend opportunities.
- **History:** 2026-10-23 (T19 plan). Superseded by D54: the list, search, filters and counts move to the server. The dashboard still shows no resend offers (kept in D54).

### D50 — Remaining work in three milestones: T19–T21
- **Status:** Accepted · 2026-10-23
- **Decision:** The plan's single T19 is replaced by three milestones:
  - **T19 Quality & Engineering:** test speed-ups and CI, customer-data and API security, server-side campaign paging, search and filtering, UX and responsive work, performance testing against agreed targets, and independent exploratory QA.
  - **T20 Product bonuses:** messages in the recipient's language (TR, EN, DE, English fallback); A/B testing evaluated for measurable value and complexity, and built only if approved.
  - **T21 Documentation & Delivery:** the final independent UI/UX audit, OpenAPI and Swagger UI, product analysis and roadmap, README and architecture, fresh-clone delivery checks.
  - **Standing rule:** every commit and push is scanned for keys, credentials, secrets and personal data first; already-pushed sensitive data is reported before any correction.
- **Why:** CASE.md puts the core workflow first. Quality and CI guard the send path before T20 changes it, and the audit and docs come last so they describe the final product.
- **Rejected:** *A/B and language together, unconditionally:* they multiply the content model with no outcome metric for A/B. *The UI/UX audit in T19:* T20 adds screens it would miss. *CI as a bonus:* it protects every later change.
- **Trade-offs:** More milestones and PRs. A/B may not ship. Personal details already pushed are left as is for now (user's choice): the GitHub username and first name in `ai-sessions/04`, and the author email in commit metadata.
- **History:** 2026-10-23 (T19 step 1). Corrected: the session exports never contained the email address; what was counted as the email was the GitHub username in the repository's PR links. The one item the history scan found, a made-up German mobile number in a test fixture (`internal/campaign/mask_test.go`, commit `236761d`), was reported and replaced going forward; history was not rewritten.

### D54 — Campaign list paged, searched and counted on the server
- **Status:** Accepted · 2026-10-23
- **Decision:** `GET /api/campaigns?status=&q=&page=` returns 25 campaigns per page, newest first, with the total and the status counts over all campaigns. `q` searches the brief, Turkish-folded, with the recipient search's limits and LIKE escaping (D47). The campaigns page and the dashboard cards use these counts; the dashboard's "needs attention" rows come from the server. The dashboard still shows no resend offers (from D49). Replaces D49.
- **Why:** Counts and search were only right for the newest 200 campaigns.
- **Rejected:** *Keep D49 and add only server-side counts:* search and paging would still miss older campaigns.
- **Trade-offs:** More API surface and tests; a round trip per filter change.
- **History:** 2026-10-23 (T19 step 5). Built as `GET /api/campaigns?status=&q=&page=` and `GET /api/dashboard`; at 10,000 campaigns every request takes under 20 ms (`docs/performance.md`).

### D57 — Performance targets for T19
- **Status:** Accepted · 2026-10-23
- **Decision:** T19 is measured against the targets P1–P7 and T1–T3 in `docs/plans/2026-10-23-t19-plan.md`: send completion, the 100/s per-channel limit (a hard requirement), API latency under 10 concurrent marketers, the campaign list at 10,000 campaigns, recipient details, 20 live screens, first-load size, test wall time and no flaky runs. Baselines are measured first; a missed target is reported with numbers, never adjusted silently.
- **Why:** "Performance testing" needs a measurable finish line.
- **Rejected:** *No fixed targets, only measurements:* nothing to decide pass or fail.
- **Trade-offs:** Numbers are for this machine (Docker Desktop, 8 CPUs, 8 GB); other machines differ.

### D62 — A second pager above the campaign list
- **Status:** Accepted · 2026-10-23
- **Decision:** The campaign list shows a compact pager ("1–25 of 300", Previous, Next) above the rows as well as the one below them.
- **Why:** 25 rows push the lower pager below the fold on a desktop screen.
- **Rejected:** *Fewer rows per page:* more paging for everyone. *A pager that sticks to the screen bottom:* covers rows and the page note.
- **Trade-offs:** Two sets of the same buttons; both carry the same labels.

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
- **Decision:** Exactly one worker replica. Each channel has a `golang.org/x/time/rate` limiter with a limit of 100/s and a burst of 1, so attempts start at least 10 ms apart and no 1-second window holds more than 100 provider requests, bursts included. Every attempt takes a token right before the HTTP call: first sends, retries (D7) and resends after an expired lease (D5). A bounded pool of 20 concurrent requests per channel handles provider delays; the limiter, not the pool, sets the rate. A claimed message is leased for 30 s and each provider request times out after 5 s. A test checks that no 1-second window of recorded attempt times exceeds 100. The API runs as a separate process (D20) and can scale on its own.
- **Why:** The simplest way to enforce the limit exactly and test it.
- **Rejected:** *Shared token bucket in Postgres:* a database round-trip per message and more code. *Static split (100/N per replica):* the config must match the replica count, and capacity is wasted when a replica is idle.
- **Trade-offs:** The worker cannot scale out without a shared limiter.
- **History:** 2026-10-22. Pool size (20 per channel), lease (30 s) and provider timeout (5 s) approved; they were open questions. 2026-10-22 (T12): the limiter alone allowed up to 110 requests in some 1 s windows, because goroutines woke late after their reservation; a hard cap now allows at most 100 departures in any window of just over 1 s, counted when each request leaves, and the provider client reuses connections. Approved by the engineer.

### D20 — Component boundaries
- **Status:** Accepted · 2026-10-21
- **Decision:** One Go module and one Docker image, run as separate Compose services:
  - `postgres` with a healthcheck.
  - `migrate`: one-shot migrations and the deterministic, idempotent seed. The other services wait for it to finish successfully.
  - `api`: campaigns, approval, Claude calls, SSE, auth. It also serves the built Vue app, so the frontend and API share an origin.
  - `worker`: sending only (D18, D19).
  - `mock-provider`: one service with `/sms/send` and `/email/send`. Delays, 5xx, 429 with `Retry-After` and timeouts are configured per channel through environment variables; defaults: 20–200 ms delay, 5% 5xx, 3% 429 with `Retry-After: 1`, 1% requests that hang past the client timeout. Its idempotency table lives in a `provider` schema with its own database role (D6).
  - The API and the worker communicate only through the database.
- **Why:** AI generation and campaign management stay independent of sending, a worker crash does not affect the UI, and the setup has the fewest services. Same origin means no CORS.
- **Rejected:** *Frontend in an nginx container:* one more service, plus proxy settings so SSE isn't buffered. *Separate SMS and email provider services:* two services that do the same thing. *API and worker in one process:* sending would share failures with the UI and AI calls.
- **Trade-offs:** Multi-stage Docker build (Node, then Go). During development, the Vite dev server proxies to the API.
- **History:** 2026-10-22. Mock provider default behaviour approved (was an open question).

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
- **Status:** Accepted, UI library superseded by D41 · 2026-10-21
- **Decision:** Vue 3, Vite, TypeScript, vue-router and ~~PrimeVue~~ (see D41). Native `fetch` and `EventSource`. Composables for state, no Pinia. Vitest for unit tests.
- **Why:** PrimeVue provides accessible chips (D9), tables, progress bars and toasts, which speeds up the editors and the dashboard.
- **Rejected:** *No UI library:* more hand-built components, and accessibility is entirely our responsibility. *Tailwind CSS:* components are still built by hand. *Pinia:* not needed for a few screens.
- **Trade-offs:** A large dependency that is only partly used.
- **History:** 2026-10-22: PrimeVue 5 moved to a commercial licence that needs a license key; the UI library is replaced by D41. The rest of D25 stands.

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

### D36 — Live status stream ends with the campaign
- **Status:** Accepted · 2026-10-22
- **Decision:** `GET /api/campaigns/{id}/events` sends a status snapshot when the counts change (polled ~1s, D16). Once the campaign is `completed` or `cancelled` and no message is still in flight (after a cancel, messages a worker holds finish later), it sends the final snapshot, then an `end` event, and closes.
- **Why:** A finished campaign never changes, so no database polling for open dashboards of old campaigns.
- **Rejected:** *Keep the stream open until the client leaves:* one query per second per open tab for nothing.
- **Trade-offs:** The frontend must close its `EventSource` on `end`, or the browser reconnects every few seconds. A resend created after `end` changes the parent's resend fields without a new event, so the frontend fetches the campaign again after a resend.

### D37 — Drafts can be deleted; nothing else can
- **Status:** Accepted · 2026-10-22
- **Decision:** `DELETE /api/campaigns/{id}` with the draft's `version` hard-deletes a `draft` and its content in one transaction. Anything approved or later is never deleted and is still stopped only by cancel (D11, unchanged). Deleting a resend draft frees its channel for a new resend (D31). No archiving (see Deferred).
- **Why:** An abandoned resend draft blocked resends on that channel forever, because drafts cannot be cancelled. Abandoned drafts also clutter the list. A draft has no messages, so no delivery history is lost.
- **Rejected:** *Cancelling drafts:* mixes "stopped a send" with "never sent", and the drafts stay in the list. *Deleting sent campaigns:* messages and attempts are the audit trail and the basis for duplicate prevention (D4, D31).
- **Trade-offs:** A deleted draft cannot be restored. A content stream still running for it fails to save.

### D38 — Channels can be changed on a draft
- **Status:** Accepted · 2026-10-22
- **Decision:** `PUT /api/campaigns/{id}/channels` `{channels, version}` on drafts only, validated as on create. A removed channel's content is deleted in the same transaction; an added channel starts without content, so approval waits for it. Resend drafts keep their channel (D31). The frontend asks for confirmation before removing a channel that has content.
- **Why:** Changing the channel picker otherwise needs a new draft and a new AI call. Approval rejects content for a channel that is not chosen, so leftover content would block it.
- **Rejected:** *Keeping the removed channel's content:* blocks approval and shows text that will not be sent.
- **Trade-offs:** Content for a removed channel is lost; re-adding the channel means writing it again.

### D39 — The rule catalog is served by the API
- **Status:** Accepted · 2026-10-22
- **Decision:** `GET /api/catalog` returns every rule field with its operators and value type, the current cities and categories, and the rule limits, from the same definitions the server validates against (D14).
- **Why:** The chip editor (D9) cannot drift from the server's rule language.
- **Rejected:** *Copying the fields into the frontend:* two definitions that drift apart.
- **Trade-offs:** One more endpoint to keep in step with the rule language.

### D40 — The brief can be edited and re-interpreted on a draft
- **Status:** Accepted · 2026-10-22
- **Decision:** `PUT /api/campaigns/{id}/request` `{request, version}` on drafts only (not resends). The version is checked before the AI call and again when saving. On success the brief, rules and proposed send time with its note are replaced; manual rule edits are overwritten, and the frontend confirms first. On failure (unsupported, invalid twice, timeout, unavailable) the draft is unchanged, with the same errors as create (D15). The content is kept; the frontend warns clearly that it may no longer match the new brief and should be reviewed or regenerated before approval.
- **Why:** Rephrasing should not need a new draft. Clearing the content would silently drop manual edits, and it is reviewed before approval anyway.
- **Rejected:** *Clearing the content:* loses hand edits. *Flagging stale content on the server:* the warning is a UI concern.
- **Trade-offs:** Content can briefly contradict the brief; the warning and the review before approval cover it.

### D41 — Headless UI components (Reka UI) with our own styles
- **Status:** Accepted · 2026-10-22
- **Decision:** Reka UI (MIT) provides the accessible behaviour of dialogs, menus, toasts, comboboxes and tag inputs; all styling is our own CSS, built from the colours and spacing of the design reference in `design/` (README, *UI/UX design*). Tables are plain semantic HTML. Replaces PrimeVue in D25.
- **Why:** PrimeVue 5 needs a license key, or it shows a licence notice; a key can't be committed, so every reviewer would see the notice. The last MIT release (4.5.5) is frozen. The design is fully custom, so a headless library fits it better than a themed one.
- **Rejected:** *PrimeVue 4.5.5:* a frozen release line, and heavy theming to match the design. *PrimeVue 5 with a Community key:* a licence notice or a key for every reviewer. *No component library:* accessibility of dialogs and menus entirely hand-built (D25's concern).
- **Trade-offs:** More CSS to write; no ready-made data table or chart components.

### D42 — Campaigns have an editable name
- **Status:** Superseded by D43 · 2026-10-22
- **Decision:** Every campaign has a `name`, 1–100 characters. On create it defaults to the brief's first sentence, cut at a word to at most 60 characters with "…"; a resend defaults to its parent's name plus "· resend (SMS)" or "· resend (Email)". `PUT /api/campaigns/{id}/name` `{name, version}` renames a draft (version-checked, D10). Re-interpreting the brief (D40) updates a name that is still the old brief's default and keeps a name the marketer chose. A campaign stored without a name (rows from before this change) shows the default.
- **Why:** The design names campaigns everywhere; a sentence-long brief is hard to scan in lists.
- **Rejected:** *A title derived from the brief only:* long, sentence-like and not editable. *Renaming after approval:* everything about an approved campaign stays fixed (D11).
- **Trade-offs:** One more field to keep in step with the brief.
- **History:** 2026-10-22: removed before release in favour of brief-based titles (D43). Migration 00005 drops the column added by 00004.

### D43 — Campaigns are identified by their brief
- **Status:** Accepted · 2026-10-22
- **Decision:** No separate campaign name. Lists show the brief's first ~60 characters, cut at a word with "…"; the campaign's own page shows the full brief. Replaces D42. AI-generated titles are deferred (README, Future improvements).
- **Why:** The name only helps marketers recognise a campaign in a list, and the brief already does that; a separate field adds an endpoint, a migration and rules for keeping it in step with the brief.
- **Rejected:** *Editable name (D42):* more scope than the case needs. *AI-suggested name in the interpretation answer:* better titles at almost no cost, but it grows the AI answer format; revisit if time permits.
- **Trade-offs:** Titles read like the start of a sentence, and two campaigns with similar briefs look alike in a list.

### D44 — The UI in English and Turkish
- **Status:** Accepted · 2026-10-23
- **Decision:** The interface is available in English (default) and Turkish, with a switch on the sign-in page and in the sidebar; the choice is kept in the browser (localStorage) and sets `<html lang>`. A small typed module in `web/src/i18n` holds two dictionaries with the same keys, fills `{placeholders}` and formats numbers, amounts, dates and lists with `Intl` (en-GB, tr-TR). Only UI text is translated: briefs, customer and seed data (names, cities, categories) and AI output (message content, an unsupported brief's reason) are shown exactly as stored; rule chips phrase a rule in the UI language but never change its values (cities, categories, numbers, dates). Errors are localized by situation in the frontend (unreachable, 401, 404, 502/504, 5xx, and a screen's own known cases such as a stale version); everything else, including other 409s, keeps the server's English text as details under a localized heading. A test scans components (text, user-facing attributes, template literals) and modules for phrases that don't come from the dictionaries, with a self-test of the patterns it must catch; developer-only messages are marked `i18n-ignore`.
- **Why:** The marketers work in Turkish and English; keeping data and AI output untouched means what is approved is exactly what is sent.
- **Rejected:** *vue-i18n:* a dependency for two languages and a handful of screens. *Error codes from the backend:* cleaner, but every handler and many tests change; revisit if more languages are added. *Storing the language on the server:* needs an endpoint and a column; per-browser is enough here.
- **Trade-offs:** Server validation details stay in English; the language is per browser, not per account; the scan is a heuristic (it catches phrases and capitalised words, not every possible string).
- **History:** 2026-10-23 (review): in Turkish the brief is "açıklama" and only the draft is "taslak"; 409 is no longer mapped to "changed elsewhere" by default; the scan also covers template literals, bound attributes and modules.

### D51 — Secret and personal-data scanning with gitleaks
- **Status:** Accepted · 2026-10-23
- **Decision:** `make scan` runs gitleaks from its official, pinned Docker image with the repository's own rules for emails and phone numbers, over the working tree, the staged changes and the commits not yet pushed. A git pre-commit hook (installed with `make hooks`) runs it on staged changes, and CI runs it on every push (D52). Fictional seed and test data, `@example.com`, and the documented demo login are allowed by the configuration. Session exports are redacted (names and emails replaced by roles) before they are committed.
- **Why:** Enforces the standing rule (D50) mechanically instead of relying on memory.
- **Rejected:** *A grep script:* misses key formats gitleaks knows. *A scanner as an app dependency:* not needed; it runs in Docker.
- **Trade-offs:** False positives need allow-list entries; the hook is per clone.

### D52 — CI with GitHub Actions
- **Status:** Accepted · 2026-10-23
- **Decision:** Tests, lint and the secret and personal-data scan (`make test`, `make lint`, the scan with its self-test) run on every push and every pull request. The browser suite runs on every pull request, every push to `main`, nightly and on request, on its own stack, keeping screenshots of failures. Runs are not duplicated: a push to a branch with an open pull request is left to the pull request's run (which tests the commit merged with `main`), and a newer commit cancels the older run of the same pull request or branch.
- **Why:** Guards every change from T19 on, especially T20's changes to the send path; the browser suite catches UI regressions before a merge.
- **Rejected:** *Both the push and the pull request run for the same commit:* twice the minutes for the same code. *The browser suite on every push to any branch:* minutes without a pull request to report to.
- **Trade-offs:** A small gate job checks for an open pull request on each branch push; the browser suite adds about 4 minutes to every pull request update.
- **History:** 2026-10-23 (T19 step 3). First version: the browser suite only nightly and on request, then also when a push changed the suite (GitHub can start a workflow by hand or on a schedule only once it is on `main`). Replaced the same day by the strategy above, at the user's request. `make lint` checks only the module's own packages, so CI's restored module cache is not linted.

### D53 — A separate, faster stack for the browser suite
- **Status:** Accepted · 2026-10-23
- **Decision:** `make e2e` starts its own Compose project (`scc-e2e`) with a throwaway database, shorter mock-provider delays and a lower retry backoff cap through a new `RETRY_BACKOFF_MAX` setting (default 30 s, unchanged in production).
- **Why:** Retry waits and local data growth made the suite slow and left test campaigns behind.
- **Rejected:** *Testing against the developer's running app:* slower, and it fills local data.
- **Trade-offs:** The suite no longer exercises the 30 s cap (the backend tests still do); one more Compose project to start.
- **History:** 2026-10-23 (T19 step 2). Shorter mock-provider delays were not added: sends are limited by the 100/s rate, so they brought no gain. The suite also runs in two lanes (checks that send, one after another; checks that don't, alongside), with the dashboard check alone first. Results in `docs/performance.md`.

### D55 — Failed sign-ins are throttled
- **Status:** Accepted · 2026-10-23
- **Decision:** After 5 failed sign-ins in 15 minutes for an email or from a client address, further attempts get `429` with `Retry-After`, in memory per api process (`golang.org/x/time/rate`, already used by the worker).
- **Why:** Nothing limited password guessing.
- **Rejected:** *Documenting it as a limitation:* the fix is small.
- **Trade-offs:** Limits reset on restart and are per process; a shared address can be throttled by someone else's failures.

### D56 — Security headers and no caching of customer data
- **Status:** Accepted · 2026-10-23
- **Decision:** The app is served with a strict Content Security Policy (`default-src 'self'`, no inline scripts, `frame-ancestors 'none'`), and every response gets `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. Every API response that carries customer data is `Cache-Control: no-store`.
- **Why:** Defense in depth for the screens that show customer data.
- **Rejected:** *Headers without a CSP:* leaves script injection unmitigated.
- **Trade-offs:** Any future inline script or third-party asset needs a CSP change.
- **History:** 2026-10-23 (T19 step 4). Also: state-changing API requests (and sign-in) from another site's page are refused by their `Origin` header, a second layer behind `SameSite=Lax`; the session cookie is `Secure` whenever the request came over HTTPS; idle connections close after 2 minutes. The review is in `docs/security-review.md`.

### D58 — The Turkish dictionary loads on demand
- **Status:** Accepted · 2026-10-23
- **Decision:** The English dictionary is part of the app; the Turkish one is loaded when Turkish is chosen (or remembered from a previous visit), before the page renders in it.
- **Why:** Both dictionaries made the largest download (51 kB gzipped) for everyone.
- **Rejected:** *Leave it:* the app is small, but this is the biggest single file.
- **Trade-offs:** A short wait when switching to Turkish for the first time.

### D59 — Live screens share one snapshot per campaign
- **Status:** Accepted · 2026-10-23
- **Decision:** Each status stream keeps its own loop (D36) but reads the campaign's snapshot through an in-process cache: a snapshot younger than half the stream interval is reused, and streams that ask at the same time wait for one database read. Errors are not cached; campaigns nobody reads are dropped.
- **Why:** 20 screens on one send cost 1.0–2.2 s of database time per second (P6); the cost should grow with campaigns, not screens.
- **Rejected:** *A poller per campaign that pushes to subscribers:* same gain, more code and start/stop races. *`LISTEN/NOTIFY` from the worker:* a worker change and notification storms. *Redis or Kafka:* new infrastructure for one process.
- **Trade-offs:** An update can be up to ~1.5 s old instead of ~1 s; per api process (one replica).

### D60 — Built assets are served precompressed
- **Status:** Accepted · 2026-10-23
- **Decision:** The image build writes a gzip copy of each built text file; the server sends it when the browser accepts gzip, with the original content type and `Vary: Accept-Encoding`. No further chunk splitting.
- **Why:** First visits downloaded 263–294 kB of JavaScript uncompressed (~95 kB gzipped).
- **Rejected:** *Compressing per request:* CPU on every request and a wrapped response writer. *Splitting Vue out:* Vue's runtime alone is ~43 kB, and splitting doesn't reduce bytes. *Brotli:* optional later.
- **Trade-offs:** A slightly larger image. The largest-chunk part of P7 (≤ 40 kB) stays missed and is reported.

### D61 — Database-backed Go tests run in parallel
- **Status:** Accepted · 2026-10-23
- **Decision:** Tests in `campaign`, `worker`, `auth`, `content` and `provider` that use their own database run in parallel (as `db` and `e2e` already do, D53); tests that share process state stay serial. An audit of redundant tests comes separately, each removal approved.
- **Why:** `make test` is as long as its slowest package, and `campaign` ran 82 tests one after another (32 s, 9 s of CPU); T1 is missed (median 76 s against 65 s).
- **Rejected:** *Removing tests for speed:* little gain, lost coverage.
- **Trade-offs:** More load on the test database at once; a shared-state test made parallel by mistake would be flaky, so 5 consecutive runs check it.
- **History:** 2026-10-24, measured. 138 tests run in parallel; 19 stay serial (4 read shared metrics, 15 depend on timing). One package at a time is much faster (`campaign` 15–20 s → 4–5 s), but the full `make test` is not: alternating old and new code gave 60–64 s both ways, because the full run already keeps the machine busy and `e2e` (already parallel) is now the longest package. The 76 s median before was host load; at a load around 10, both meet T1. 8 of 8 full runs passed.

## Open Questions

Proposed 2026-10-23 after the step 7 measurements (`docs/performance.md`). Q1, Q2, Q5 and the first half of Q3 were approved as D59–D62.

- **Q3 (second half) — A test audit.** A report of redundant, overlapping or low-value tests and browser checks that could be faster integration tests; each removal or move needs approval.
- **Q4 — Desktop first.** Desktop speed and usability come first; phone layouts keep the step 6 checks, and phone-only performance work (list re-render 400–480 ms, language switch) is deferred.
- **Q6 — How P3–P5 are read.** Report both loads: realistic (10 marketers, a 1 s pause per request: met) and back to back (about 14× that: missed, published as capacity). Targets stay unchanged; which load counts for "met" is yours to decide.

## Deferred

- **Optional features from `CASE.md`:** A/B testing, messages in the user's language (a `language` field will be seeded now), CI pipeline. Scheduled sending moved into scope (D29). Resend to skipped recipients moved into scope (D31).
- **Archiving campaigns** (2026-10-22): hiding finished campaigns from the list. Deleting drafts (D37) removes most of the clutter; it can be added later without changing D37.
