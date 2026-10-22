# Smart Campaign Composer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans. Steps use `- [ ]` checkboxes. Every task follows TDD: failing test → run (fail) → minimal code → run (pass) → commit.

**Goal:** Deliver the core flow of CASE.md (request → audience → streamed content → review/approve → queued sending → live dashboard) with one-command `docker compose up` and `make test`.

**Architecture:** One Go module / one image run as `migrate`, `api`, `worker`, `mock-provider` (D20); Postgres 16 is DB and queue (D17, D18); Vue 3 + PrimeVue served by `api` (D25). Claude behind an `LLM` interface with a mock as default (D22, D24).

**Tech stack:** Go 1.22+ stdlib `net/http`, pgx/v5, goose, anthropic-sdk-go, x/time/rate, x/crypto/bcrypt, prometheus/client_golang · Vue 3, Vite, TS, vue-router, PrimeVue, Vitest.

**Spec:** `CASE.md` + `DECISIONS.md` (D1–D27). Plan argues from both; executors read both.

## Context

Requirements (session 01) and architecture (session 02) are recorded as D1–D27; planning decisions (session 03) as D28–D35. This plan breaks delivery into small ordered tasks so each can be reviewed and approved.

## Global Constraints

- Never run AI-generated SQL; rules → parameterized SQL only (CASE §3, D14).
- SMS ≤ 160 code points after personalization; email subject ≤ 80; email body ≤ 2,000 (D2, D3, D13).
- ≤ 100 provider requests/s per channel incl. retries, burst 1 (D19).
- One message per (campaign, user, channel) (D4); at-least-once + provider idempotency, never claim exactly-once (D5, D6).
- Variables allowlist: `{{first_name}}`, `{{last_name}}`, `{{city}}` (D12).
- `LLM_MODE=mock` default; `real` requires `ANTHROPIC_API_KEY` or API exits (D24). Model `claude-opus-5-5`, override `ANTHROPIC_MODEL` (D23).
- Claude never receives user records (D15).
- `docker compose up` seeds exactly 3,000 users with varied event histories; deterministic (fixed RNG seed, fixed reference date) and idempotent on rerun (CASE §8 as amended in T0).
- `make test` runs in Docker; integration tests fail, never skip, without a DB (D27).
- English for code, comments, commits, docs. No secrets committed.

## Layout (decomposition locked here)

```
cmd/scc/main.go            subcommands: migrate | api | worker | mock-provider
internal/config            env parsing per process
internal/db                pool, embedded goose migrations, seed
internal/auth              login/logout, sessions, middleware
internal/audience          rule types, validation, SQL builder, counts
internal/content           template parse/render, limits, over-limit estimate
internal/llm               LLM interface, mock, anthropic impl, partial-JSON field streamer
internal/campaign          campaign service + HTTP handlers (create, rules, content, approve, cancel, status SSE)
internal/worker            claim/lease repo, dispatcher, retry policy, provider client
internal/provider          mock provider server + idempotency store
internal/obs               /healthz, /readyz, /metrics, HTTP metrics middleware
web/                       Vue app
Dockerfile, docker-compose.yml, Makefile, .env.example
```

## Data model (D33)

Conventions: `bigint` identity PKs, `timestamptz`, money as `numeric(10,2)`, statuses as `text` + `CHECK` (easier to extend than PG enums), FKs everywhere.

**Customers & shopping (public schema)**
```
categories   (code text PK, name text)                         -- ~6 rows: electronics, fashion, home, beauty, sports, books
users        (id, first_name, last_name NULL, email UNIQUE, phone UNIQUE, city, language,
              sms_opt_in bool, email_opt_in bool, signed_up_at)
orders       (id, user_id FK, ordered_at, total_amount)          idx (user_id, ordered_at)
order_items  (id, order_id FK, category FK, quantity, unit_price) idx (order_id), (category)
cart_items   (id, user_id FK, category FK, quantity, unit_price, added_at)  idx (user_id, added_at)
seed_meta    (id=1 PK, rng_seed, reference_time, seeded_at)
```
**Campaigns & auth**
```
marketers        (id, email UNIQUE, name, password_hash, created_at)
sessions         (token_hash bytea PK, marketer_id FK, created_at, expires_at)
campaigns        (id, request_text, channels text[], rules jsonb, status, version int,
                  send_at timestamptz NULL,                       -- draft: proposed time (from brief or marketer); fixed at approval
                  schedule_note text NULL,                        -- draft: the brief's phrase / why a suggestion was not usable
                  time_zone text,                                 -- IANA zone from the browser; resolves absolute dates and schedule display
                  parent_campaign_id FK NULL,                     -- set only on a resend (one channel, rules NULL);
                                                                  -- partial unique (parent_campaign_id, channels) WHERE status <> 'cancelled'
                  approval_estimate jsonb NULL,                   -- per-channel counts shown at approval
                  audience_built_at NULL,                         -- when sending started and messages were created
                  created_by FK, created_at, updated_at,
                  approved_by FK NULL, approved_at, cancelled_by FK NULL, cancelled_at, completed_at)
campaign_content (campaign_id FK, channel, subject NULL, body, PK(campaign_id, channel))
```
**Delivery tracking**
```
messages          (id, campaign_id FK, user_id FK, channel, status, skip_reason NULL, last_error NULL,
                   attempt_count, next_attempt_at, lease_token uuid NULL, lease_expires_at NULL,
                   sent_at NULL, created_at, updated_at, UNIQUE(campaign_id, user_id, channel))
                   idx: due work (channel, next_attempt_at) WHERE status IN ('pending','retry_scheduled');
                        expired leases (lease_expires_at) WHERE status='sending'; dashboard (campaign_id, status)
delivery_attempts (id, message_id FK, attempt_no, started_at, finished_at, outcome, http_status NULL,
                   error NULL, UNIQUE(message_id, attempt_no))
                   outcome ∈ accepted | rate_limited | server_error | timeout | network_error | rejected
```
**Mock provider (schema `provider`, own role, no FKs to public)**
```
provider.idempotency_keys (key text PK, channel, state in_progress|completed, http_status, response jsonb,
                           created_at, completed_at)
```

Scenario coverage (all as parameterized subqueries, no new tables needed):
cart abandonment (cart within N, no order since) · win-back (last order > N days) · VIP (spend/order count ≥ X) ·
category affinity (ordered/carted category X within N) · high-value cart (cart value ≥ X) · new customers (signed_up within N) · city.
Every time-based rule takes either a **relative** window (`{"last_days": 30}`, counted from when sending starts) or an **absolute** one (`{"from": "2026-10-01", "to": "2026-10-31"|null}`, dates in the campaign's time zone). Claude uses the absolute form when the brief names a date or a cutoff.

## Scheduled sending flow (D29, D30)

0. **Suggestion from the brief:** the request is sent together with the browser's IANA time zone. Claude gets the current time and that zone, and returns `suggested_send_at` (RFC 3339 with offset) or `null`, plus `schedule_note` (the phrase it read, e.g. "yarın saat 10:00"). Vague timing ("sometime next week") → `null` with a note. The backend validates it: a bad format is invalid output and triggers the D15 retry. A parseable time outside the window (in the past, or more than 30 days ahead) isn't prefilled; the note explains why ("'yesterday 10:00' is in the past") and the rules are still used. A valid suggestion is stored as the draft's proposed `send_at`. It's advisory only; nothing is scheduled yet.
1. **Review and confirm:** the review screen shows "Suggested from your brief: Tue 22 Oct, 10:00 (Europe/Istanbul)", with *Schedule* preselected and the time editable, or *Send now*. Approve always sends an explicit `send_at` (or `now`) from what's on screen, so a suggestion is never accepted implicitly. A draft changed since it was viewed fails the version check (D10).
2. **Approve:** the marketer chooses *Send now* or *Schedule* (date and time in browser local time with the zone shown, stored in UTC). In one transaction (T9): check version, status `draft`, valid content, audience > 0, and `send_at`: *now* → `now()`; scheduled → at least now + 1 min and at most now + 30 days; an absolute rule window starting after `send_at` → 422 (it would match nobody). Rules and content are frozen and the current per-channel counts are stored as `approval_estimate`. **No messages are created yet.** The screen shows: *"412 recipients now (SMS 380 · Email 301). The audience is recalculated when sending starts; the final number may differ."* The per-channel over-limit warning (D3) is shown the same way, as an estimate. The campaign gets `status = approved` and `send_at`.
3. **Waiting:** no new status and no scheduler process. The UI shows "Scheduled for <time>". Rules, content and time can't be changed. **Cancel** sets `approved` → `cancelled`; there are no messages to touch yet. Rescheduling isn't supported: cancel and create a new campaign.
4. **Sending starts:** the worker's `BuildAudience` step finds approved campaigns with `send_at <= now()` (`FOR UPDATE SKIP LOCKED`). In one transaction it evaluates the rules (relative windows counted from now), inserts messages per channel with consent (`ON CONFLICT DO NOTHING`), and sets `audience_built_at` and `status = sending`. "Send now" takes the same path within about 1 s. If the audience is now empty → `completed` with 0 messages, shown on the dashboard. From then on the normal path runs: consent and cancel re-check, rendering, length check (`skipped/character_limit_exceeded`), rate limiter and retries (D7, D19). It ends as `completed` when every message is final. The dashboard shows "Estimated 412 at approval → 398 at send".
5. **Late start:** if the worker is down at `send_at`, sending starts when it comes back, and relative windows are counted from that moment. The dashboard shows the scheduled time and the actual start (`audience_built_at`).
- Known trade-off: the approved count is an estimate. Once a send has started, users who stop matching aren't removed; only consent is re-checked.

## Resend to skipped recipients (D31)

- The dashboard shows skipped counts per reason and channel. For `character_limit_exceeded` it offers **"Resend to 37 skipped (SMS)"**, enabled once the original campaign is `completed` or `cancelled`.
- This creates a draft **follow-up campaign** with `parent_campaign_id`, a single channel and no rules. Its audience is the fixed list of the parent's messages on that channel with `skip_reason = 'character_limit_exceeded'`, minus any user with a `sent` message on that channel in the parent. It is never recalculated from rules.
- The marketer writes (or generates, T8) a shorter template, sees the over-limit count for that list, and approves now or scheduled. Sending uses the same path: consent re-checked, rendered, length-checked, skipped again if still too long. It can itself be resent.
- At most one non-cancelled resend per (parent, channel) at a time (partial unique index), so two marketers can't resend twice.

## Tasks

Dependencies in brackets. Milestone = a natural review/approval stop.

### Milestone A — Foundation

**T0 Docs baseline** [—]
- CLAUDE.md, under Project Documentation → `DECISIONS.md`: add a permanent rule: *"Keep DECISIONS.md short and focused. Record only important decisions, each with a brief reason. No implementation details, test cases, repetition or long explanations; those belong in the plan or the code."* Save the same preference to memory.
- CASE.md §5 and §8: user count changed from 5,000 to 3,000 (§5) and "a few thousand" (§8); idempotency sentence kept.
- DECISIONS.md: record only the decisions approved with this plan, each with its decision and essential reasoning in the existing short format. No implementation details or test cases there; those stay in this plan. Entries: seed size and generation rules, event-table schema and catalog extension (incl. relative/absolute time windows), backend field streaming, scheduled sending incl. brief suggestion (removed from Deferred; dated note on D11), audience recalculated at send start (D8 → `Superseded`, pointing to the new ID), resend to skipped recipients (removed from Deferred), fixed-text over-limit rule and per-channel warning (dated note on D3), reply in the request's language, Git workflow.
- Save this plan to `docs/plans/2026-10-21-implementation-plan.md`. Commit the docs on `main` as the initial commit (`docs: add case, decisions and implementation plan`).
- Accept: no "5,000" left in CASE.md; each new D-entry has status, date, reasoning and rejected alternatives.

**T1 Skeleton, Compose, `make test`** [—]
- Files: `go.mod`, `cmd/scc/main.go`, `internal/config`, `internal/obs`, `Dockerfile`, `docker-compose.yml` (postgres+healthcheck, migrate, api; `test` profile), `Makefile` (`test`, `test-local`), `.env.example`, `.gitignore`.
- Produces: `config.Load(process string) (Config, error)`; `obs.Routes(mux, pool)` registering `/healthz`, `/readyz`, `/metrics`.
- Tests: `TestLoadConfig_RealModeWithoutKeyFails` (error names `ANTHROPIC_API_KEY`); `TestReadyz_DBDown_Returns503`; `TestDBRequired` — integration helper calls `t.Fatal` (not Skip) when `DATABASE_URL` unset.
- Accept: `docker compose up` → postgres, migrate (exit 0), api healthy; `make test` green from a clean clone with only Docker+make.

**T2 Schema + deterministic seed** [T1]
- Files: `internal/db/migrations/*.sql`, `internal/db/seed.go`, name/city lists.
- Produces: `db.Migrate(ctx, pool) error`, `db.Seed(ctx, pool, seed int64, now time.Time) error` (`now` injected so tests control the first-run time); demo marketers from env (`DEMO_MARKETER_PASSWORD`, bcrypt-hashed).
- **Generation (D28):** a fixed RNG seed randomly generates 3,000 users and their shopping histories. Every date is an offset before `reference_time`, which the first setup stores as `now()` in `seed_meta`. Varied histories come from randomly assigning each user a profile with seeded weights (e.g. cart abandoner, recent buyer, lapsed buyer, VIP, new signup, browsing-only, inactive). Each profile draws its event offsets from its own range, so the 7/30/60/90-day windows, categories and cities all get non-trivial counts.
- **Idempotent, never modifies existing data:** if `seed_meta` exists, the seed does nothing. The first run inserts everything in one transaction (a crash leaves no partial seed), with `ON CONFLICT DO NOTHING` on natural keys (`users.email`) as a second guard. Restarts never update, shift or delete rows.
- Known limit: as real time passes, relative-date audiences (e.g. "last 30 days") shrink. The README documents `docker compose down -v && docker compose up` to start fresh relative to today (this wipes campaigns too).
- Tests: `TestSeed_Idempotent` (run twice → 3,000 users, same order/cart event counts, marketer count unchanged); `TestSeed_Deterministic` (two fresh DBs seeded with different `now` values, e.g. 2026-01-01 and 2026-10-21 → identical checksum). The checksum helper `seedFingerprint(ctx, pool)` hashes user fields and every event as `(reference_time - event_time)` plus its other columns, ordered by natural key (email, then offset). It never hashes absolute timestamps or generated IDs, so runs at different times compare consistently; `TestSeed_RerunChangesNothing` (same DB: seed with `now`, rerun with `now + 7 days` → raw row dump including absolute timestamps and `reference_time` unchanged); `TestSeed_NoFutureDates`; `TestSeed_ProfileWindows` (each profile has ≥ 50 users, and each 7/30/60/90-day window and each category returns > 0 for its scenario rule); `TestSeed_CoversCatalog` (≥1 user per history profile and per catalog rule: İstanbul, cart ≤30d & no order ≤30d, sms-only, email-only, no consent, zero orders, lapsed buyer, high spender, NULL last_name for D12); `TestSeed_CaseExampleMatches` (case example audience > 0); `TestMessagesUniqueConstraint`; `TestCharLengthCountsCodePoints` (`'şğİ'` → 3).
- Accept: fresh `docker compose down -v && up` → exactly 3,000 users; second `up` → identical data.

**T3 Auth** [T2]
- Produces: `POST /api/login`, `POST /api/logout`, `GET /api/me`, `auth.Require(next) http.Handler` putting marketer ID in context.
- Tests: wrong password → 401 with same message as unknown email; cookie HttpOnly+SameSite=Lax; DB stores only SHA-256 of token; expired session → 401; logout invalidates.
- Accept: all `/api/*` except login return 401 without a session; health/metrics open.

### Milestone B — Audience

**T4 Rule model, validation, SQL builder** [T2]
- Produces: `audience.Rule{Field string; Op string; Value json.RawMessage}`, `audience.Validate([]Rule) []FieldError`, `audience.Build(rules, channel) (where string, args []any)`, `audience.Count(ctx, pool, rules, channels) (Counts{Total, SMS, Email int}, error)`.
- Catalog = D14 + approved extensions: ordered category IN [...] within N days, carted category IN [...] within N days, cart value ≥ X, signed up within N days. Categories validated against the `categories` table.
- Time rules: `{"last_days": N}` or `{"from": date, "to": date|null}`. `Build` takes `now time.Time` and the campaign time zone, so relative windows are counted from the moment of evaluation and absolute dates resolve in that zone.
- Values pinned: N days 1–365; absolute `from` ≤ `to`, not before 2000-01-01; amounts 0–1,000,000; city must be in the seeded city list (case/diacritic-normalized lookup, stored canonical e.g. `İstanbul`); ≤20 rules; no duplicate field+op; `language` rejected as "reserved" (D14).
- Tests: table-driven `TestValidate_*` (unknown field, bad op, N=0, N=366, unknown city, `"Istanbul"` → `İstanbul`); `TestBuild_NoValueInterpolated` (value `"'; DROP TABLE users;--"` appears only in args); `TestCount_RelativeWindowMovesWithNow` (same rules, `now` +5 days → different users); `TestCount_AbsoluteWindowFixed` (same rules, `now` +5 days → same users); `from > to` → error; `TestCount_MatchesHandComputed` on a fixture of ~10 users (one per scenario: cart abandoner, win-back, VIP, category buyer, high-value cart, new customer); unknown category → error; `TestCount_ConsentPerChannel`.

**T5 LLM interface + audience interpretation** [T4]
- Produces in `internal/llm`: `type LLM interface { InterpretRequest(ctx, InterpretInput) (Interpretation, error); StreamContent(ctx, ContentInput, func(Delta)) (Content, error) }`; `NewMock()`; `NewAnthropic(cfg)`. `InterpretInput{Text string; Now time.Time; TimeZone string; Catalog …}`. `Interpretation{Unsupported bool; Reason string; Channels []string; Rules []audience.Rule; SuggestedSendAt *time.Time; ScheduleNote string}`.
- Language: real prompts tell Claude to reply in the language of the request (both calls). The mock detects Turkish (Turkish letters or common words such as "gönder", "sepet", "yarın") and returns Turkish content, otherwise English.
- **The mock never invents rules (safety rule).** It understands only a fixed set of documented brief patterns (English and Turkish, about 8). Each is a regex that must match the **whole** brief, with slots for city, N days, category, amount, ISO cutoff date ("since 2026-10-01" → absolute window) and schedule phrase ("tomorrow/yarın at/saat HH(:MM)", "today/bugün HH:MM", "in N hours/N saat sonra"), plus optional tone words ("short", "friendly", "kısa", "samimi"). A brief that matches no pattern, or matches one but has extra unrecognized conditions, gets `unsupported` with the reason *"Mock AI only understands the example briefs listed in the README. Use LLM_MODE=real for free-form requests, or build the rules by hand."* No rules are returned and nothing is saved. The patterns are listed in the README and shown as examples in the UI's Mock AI banner.
- **Same rule in real mode:** the prompt tells Claude to return `unsupported` with a reason when any part of the requested audience can't be expressed with the catalog, and never to drop, approximate or add conditions. Validation can't detect a dropped condition, so the marketer's review of the rule chips (D9) and the approval step remain the last guard.
- `campaign.Interpret(ctx, llm, text)` applies D15: validate → one retry with errors → else `ErrAIInvalid`. Request text 1–1,000 chars. Timeout configurable (default 60s).
- Anthropic impl: `output_config.format` JSON schema, adaptive thinking, explicit effort, refusal `stop_reason` → treated as unsupported. Load `claude-api` skill before writing; confirm Go bindings by compiling.
- Real prompt: use the absolute window when the brief names a date or cutoff ("since 1 October", "Black Friday 24–28 Nov"), otherwise relative.
- Tests (mock LLM scripted per call): `TestMock_UnknownBrief_Unsupported` ("Send something nice to our best customers" → unsupported, zero rules, draft not created); `TestMock_ExtraCondition_Unsupported` (the case example plus "who like hiking" → unsupported, not the case-example rules); `TestMock_DocumentedPatterns` (each README pattern in EN and TR → its expected rules, table-driven from the same list the README is generated from); brief with "since 2026-10-01" → absolute window; suggested time with a bad format → D15 retry; suggested time in the past or > 30 days → not prefilled, explanatory note, rules kept; vague timing → null; prompt contains now + zone; mock: Turkish brief → Turkish content containing `{{first_name}}`, English brief → English; "yarın saat 10:00" with zone Europe/Istanbul → next day 07:00Z; valid first try; invalid→retry→valid (2 calls, second prompt contains the errors); invalid twice → `ErrAIInvalid`, nothing saved; timeout → error; `unsupported` passthrough; prompt contains catalog + allowlist and no user data.
- Real-mode smoke test: manual only, documented in README, not in `make test`.

**T6 Campaign draft API** [T3, T5]
- Produces: `POST /api/campaigns {request, channels, time_zone}` → draft with rules, counts, proposed `send_at` and `schedule_note`; `PUT /api/campaigns/{id}/schedule {send_at|null, version}` lets the marketer change the proposal in the draft; `PUT /api/campaigns/{id}/rules {rules, version}` → revalidate + recount, bumps version; `GET /api/campaigns`, `GET /api/campaigns/{id}`.
- Tests: brief with a schedule phrase → draft has proposed `send_at`, campaign not scheduled (no messages); invalid IANA zone → 422; create stores `created_by`; invalid manual rules → 422 with field errors; stale version → 409; edit on non-draft → 409; empty channels → 422.

### Milestone C — Content and approval

**T7 Template validation and rendering** [T2]
- Produces in `internal/content`: `Parse(tpl string) ([]Var, error)`, `Render(tpl string, u UserVars) string` (empty → "", collapse repeated spaces, trim), `ValidateDraft(Content, channels) []FieldError`, `EstimateOverLimit(ctx, pool, audienceSQL, content) (OverLimit{SMS, Email int}, error)`: per channel, over the current audience (rules, or a resend's fixed list). It's computed in SQL with the same replace-and-tidy rendering as `Render` plus `char_length`, with no rendering in memory (D3).
- Pinned: only exact `{{first_name}}|{{last_name}}|{{city}}`; any other `{{`/`}}` → error; body ≤ 2,000; subject must be single-line.
- Tests: `{{ first_name }}`, `{{FIRST_NAME}}`, `{{email}}`, `{first_name}`, unclosed `{{first_name` → errors; Turkish render length; empty last_name tidy; `TestValidateDraft_FixedTextOverLimit` (172-char SMS without variables → error; 150 chars + `{{first_name}}` → valid); `TestEstimate_MatchesGoRender` (SQL over-limit count per channel equals counting Go `Render` results for all 3,000 seeded users, with templates near the limits).

**T8 Streamed content generation** [T6, T7] — backend extracts fields from partial JSON (D34): `llm.FieldStreamer` turns JSON chunks into `delta{field,text}`.
- Produces: `POST /api/campaigns/{id}/content` streaming `text/event-stream` events `delta{field,text}`, `retry{errors}`, `done{content}`, `error{message}`; `PUT /api/campaigns/{id}/content {content, version}` for manual edits.
- Final content validated with T7 after stream ends; one retry (D15); nothing saved on failure or client disconnect.
- Tests: mock streams deltas → client receives ordered deltas + `done`; invalid then valid → `retry` then `done`; invalid twice → `error`, DB unchanged; context cancel mid-stream → DB unchanged; partial-JSON streamer unit tests (escaped quotes, `\n`, `İ` split across chunks).

**T9 Approve (now or scheduled) and cancel** [T7, T8]
- Produces: `POST /api/campaigns/{id}/approve {version, send: "now" | {"at": RFC3339}}`. `send` is required, so the proposed draft time never applies implicitly: in one transaction, lock the campaign, check version, status `draft`, valid content, current audience > 0 and the `send_at` window (see Scheduled sending flow). Store `approval_estimate` (per-channel counts) and set `approved_by/at, send_at`. No messages are created. `POST /api/campaigns/{id}/cancel`. `GET /api/campaigns/{id}/estimate` returns the current per-channel audience and over-limit counts, labelled as estimates.
- Resend: `POST /api/campaigns/{id}/resend {channel}` → draft follow-up (see Resend section). 409 if the parent isn't `completed`/`cancelled`, there are no `character_limit_exceeded` skips on that channel, or an active resend exists.
- Takes `now` from an injectable clock so tests are deterministic.
- Tests: approve without `send` → 422 even when the draft has a proposed time; concurrent double approve → one 200, one 409; approve creates no messages and stores the estimate; stale version → 409; zero audience → 422; `send_at` in the past → 422; `send_at` > 30 days → 422; absolute window starting after `send_at` → 422; cancel a scheduled campaign → `cancelled`; cancel while `sending` moves only `pending`/`retry_scheduled`; editing approved content → 409; resend audience = exactly the skipped users minus anyone sent; second concurrent resend → 409; resend of a parent still sending → 409.

### Milestone D — Sending

**T10 Mock provider** [T1, T2]
- Produces: `mock-provider` serving `POST /sms/send`, `POST /email/send` with header `Idempotency-Key`; env per channel: `*_DELAY_MS_MIN/MAX`, `*_ERROR_RATE` (5xx), `*_RATE_LIMIT_RATE` (429 + `Retry-After`), `*_TIMEOUT_RATE`; malformed body → 400.
- Defaults (proposed): delay 20–200ms, 5% 5xx, 3% 429 (Retry-After 1s), 1% hang past client timeout.
- Tests: key inserted before simulating; concurrent same key → one processed, other 409/retryable; finished key → stored result replayed; restart keeps keys (DB-backed); failure injection deterministic with seeded RNG.

**T11 Queue repository (claim, lease, results)** [T9]
- Produces in `internal/worker`: `BuildAudience(ctx, now time.Time) (int, error)`: due approved campaigns (`FOR UPDATE SKIP LOCKED`) → one transaction per campaign that inserts messages (from rules via `audience.Build`, or a resend's fixed list) and sets `audience_built_at` and `sending`; empty → `completed`. `Claim(ctx, channel, n, now time.Time, lease time.Duration) ([]Job, error)` (`FOR UPDATE SKIP LOCKED`, due `pending|retry_scheduled` or expired `sending`); `Complete(ctx, job, Result) error` guarded by `lease_token`; `FinishCampaigns(ctx)` marks `completed` when no non-final messages.
- Each attempt inserts a `delivery_attempts` row (outcome, HTTP status, timings).
- Tests: `BuildAudience` does nothing at `send_at - 1s` and builds at `send_at`; a user who orders between approval and send is excluded and a new matching user is included; an approved-then-cancelled campaign is never built; two concurrent `BuildAudience` calls → messages created once; crash mid-build (tx rollback) → rerun builds cleanly; empty audience → `completed`; two concurrent claimers never get the same row; expired lease reclaimed with same message ID; stale-lease update affects 0 rows; retry after cancel → `cancelled`; completion transition.

**T12 Dispatcher, retry, rate limit** [T10, T11]
- Produces: `worker.Run(ctx, deps)`; per channel: claim → re-check consent + campaign status (revoked → `skipped/no_consent`; cancelled → `cancelled`) → render + length check (`skipped/character_limit_exceeded`) → `limiter.Wait` → HTTP → classify (D7) → `Complete`. `Backoff(attempt int, retryAfter time.Duration, rnd) time.Duration` (exp+jitter, cap 30s, honours Retry-After). Lease 30s, provider timeout 5s, pool size 20/channel (proposed).
- Tests: `TestBackoff_*`; classification table (200 sent, 429/5xx/timeout retry, 400 failed, 5th failure → failed with last error); skip paths take no limiter token; `TestRateLimit_NoWindowOver100` — 1,000 messages to an instant fake provider, sliding 1s window over recorded start times ≤100 per channel.

**T13 End-to-end send test** [T12]
- One Go integration test: create (mock LLM) → content → approve → worker against real mock-provider handler with failure injection → all messages final; no duplicate provider keys accepted; counts match.
- Second case: approve with `send_at = now + 2s` → no provider calls before `send_at`, campaign completes after.
- Third case: a template that's too long for some users → those are skipped with `character_limit_exceeded`; resend with a shorter template → only those users get a provider call, and nobody gets two sent messages on that channel.

### Milestone E — Monitoring

**T14 Metrics + live status SSE** [T12]
- Produces: metric set from D26 in each process; `GET /api/campaigns/{id}/events` (EventSource) pushing per-channel counts by status, retries, 429s, skip/failure reasons, approval estimate vs actual audience, and resendable skip counts, only on change, ~1s.
- Tests: counts query on fixture; SSE handler emits once for unchanged counts, again after a change; stops on client disconnect.

### Milestone F — Frontend (can start after T6 using the API contract)

**T15 App shell** [T3] — Vite+TS+PrimeVue, router, login page, 401 → login, "Mock AI" banner from `GET /api/me` config flag, Vite proxy, multi-stage Dockerfile serving `web/dist` from api. Vitest: auth guard redirect.
**T16 Composer: request + rule chips** [T6, T15] — request box, channel picker, chips editor (D9) with catalog add/edit/remove, live counts, field errors. Time chips read "in the last 30 days (counted from send time)" or "since 1 Oct 2026", and the marketer can switch the form. Vitest: chip ↔ rule mapping for both forms.
**T17 Content + review + approve** [T8, T9, T16] — fetch-SSE reader, live fields, editors, preview with sample user (escaped plain text), SMS counter with GSM-7/UCS-2 + segments (info only), current audience and over-limit counts per channel with the notice that the audience is recalculated when sending starts, approve dialog with *Send now* / *Schedule* (date-time picker, local zone shown, sent as UTC ISO string), version included. A suggestion from the brief is preselected with the label "Suggested from your brief" and its note. An unusable suggestion shows the note and leaves both options unselected, so the marketer must choose. Vitest: suggestion prefill and the explicit-choice requirement. Vitest: `smsInfo(text)` (GSM-7 160/153, UCS-2 70/67, Turkish `ş` → UCS-2); SSE parser across chunk boundaries.
**T18 Dashboard** [T14, T15, T17] — campaign list with "Scheduled for …" state and resend links to the parent, live per-channel progress via EventSource, scheduled vs actual start time, estimate vs actual audience, skipped counts and reasons table, "Resend to N skipped (channel)" opening the T17 content/approve screen for the follow-up, cancel with confirm. Vitest: status aggregation view model, including the scheduled state and when resend is offered.

### Milestone G — Delivery

**T19 Docs and final verification** [all]
- README (setup, real-mode switch, architecture, tests, limitations, manual real-LLM smoke), `.env.example` complete, DECISIONS.md updated.
- Checklist: `docker compose down -v && docker compose up` from clean clone; full manual flow in browser; `make test` green; `/metrics` and `/readyz` on all processes; grep `ai-sessions/` for `sk-ant`, emails, phone numbers; CASE §8 deliverables ticked.

## Review Focus (input classes most likely to bite; each pinned by a test in the owning task)

1. Claude writes `Istanbul` / `istanbul` while data says `İstanbul` → normalized match, not silent 0 users (T4).
2. Double-click approve or two marketers approving at once → exactly one approval, no duplicate messages (T9).
3. Cancel while messages wait for a rate-limit token → no new provider call after cancel commits (T12 re-check).
4. Malformed variables (`{{ first_name }}`, `{{FIRST_NAME}}`, unclosed) in AI output or manual edits → validation error before approval (T7).
5. Stream dies mid-generation (API error, browser closes tab) → draft unchanged, clear error (T8).
6. Schedule time picked in a non-UTC browser zone or near the "now + 1 min" edge → stored as the intended instant; server clock decides validity (T9, T17).
7. Brief says "yesterday at 10" or "next month" → no prefilled time, visible note, marketer must choose explicitly (T5, T17).
8. Brief with a fixed cutoff ("since 1 October") scheduled days ahead → window stays at 1 October, doesn't slide (T4, T5).
9. Resend clicked twice, or by two marketers → one follow-up; no recipient gets the channel twice (T9, T13).
10. Brief the mock doesn't fully understand → `unsupported` with a clear reason, never a guessed or broader audience (T5).

## Technical risks

| Risk | Impact | Mitigation |
|---|---|---|
| Structured output streams as raw JSON text; live per-field display needs partial parsing | T8 core UX | Backend field streamer (D34); unit tests with chunk splits mid-escape |
| anthropic-sdk-go exact bindings for `output_config.format` not documented in skill | T5/T8 time | Isolated behind `LLM` interface; compile-fix loop; mock keeps tests independent |
| Rate-limit test flaky under slow Docker | false failures | Assert on recorded attempt timestamps (sliding window), not throughput |
| Lease shorter than batch wait + provider timeout → duplicate resend | duplicate sends (provider dedups) | Lease 30s ≫ 50 msgs/100/s + 5s timeout; idempotency key covers the rest |
| Seed dates are fixed at first setup, so "last 30 days" audiences shrink as real time passes | demo audiences get smaller weeks later | Accepted: data is never modified on restart. Profiles also spread events over 0–180 days so wider windows stay populated; README documents a fresh reseed |
| Scope: 19 tasks + frontend | delivery date | Milestone order keeps a working backend early; optional features stay deferred |
| Session exports may contain keys/PII | leak | T19 scan; reminder after each milestone |
| Counts shown before sending (audience, over-limit) are estimates of a future send | marketer surprised by the final numbers | Clear "may change" notice at approval; dashboard shows estimate vs actual |
| SQL over-limit rendering drifts from Go `Render` | wrong warning counts | `TestEstimate_MatchesGoRender` over all seeded users |

## Planning decisions

Recorded in DECISIONS.md: seed (D28), scheduled sending with suggestion from the brief (D29), audience evaluated when sending starts (D30, supersedes D8), resend to skipped recipients (D31), reply in the request's language (D32), event tables (D33), backend field streaming (D34), Git workflow (D35). Updated: D3 (per-channel warning, fixed text over limit is a validation error), D11, D14 (catalog extension, relative and fixed dates), D15 (`unsupported` instead of guessing, mock included).

Git details (D35): branches named `milestone-a-foundation`, …; commit prefixes `feat:`, `test:`, `docs:`; at the end of each milestone I show the summary and diff before asking to merge. No worktrees.

**Still open, confirm before T10/T12:** default values for mock-provider failure rates (5% 5xx, 3% 429 with Retry-After 1s, 1% timeout, 20–200 ms delay), lease 30s, provider timeout 5s, 20 workers per channel, request text ≤ 1,000 chars, session TTL 12h.

## Verification (end-to-end)

- `make test` (Docker) green; `docker compose down -v && docker compose up` → login → case-example request → rules + count → streamed content → edit → approve → dashboard reaches completed with retries/429s visible → `/metrics` shows attempts. Repeat with *Schedule* 2 min ahead → "Scheduled for …" shown, sending starts on time; schedule another and cancel it → nothing sent. Write a too-long SMS → skipped count shown → resend with a shorter template → only the skipped users receive it.
- `LLM_MODE=real` without key → api exits with message naming `ANTHROPIC_API_KEY`.

## Execution

Approved 2026-10-21. Execution method: inline (superpowers:executing-plans), chosen 2026-10-22; approval before each task.
