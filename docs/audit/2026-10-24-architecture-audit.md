# Architecture and engineering audit — 2026-10-24

Independent audit of `main` at `cfe8599` (after PR #9, T21 A/B). Audit only: no application code, tests, migrations, config or project docs were changed. Evidence was produced with focused commands against the test Postgres (`test-db`) and a throwaway scratch database that was dropped afterwards.

## 1. Executive summary

The system is a small, well-reasoned modular monolith: one Go image run as `api`, `worker`, `mock-provider` and `migrate`, with Postgres as both store and queue. The delivery path is the strongest part. It has leases with fencing tokens, provider-side idempotency, an "uncertain" outcome, re-checks of cancel and consent right before each send, and a hard per-channel cap. All of this is backed by real-Postgres tests, crash and shutdown scenarios among them. Security, AI-output validation and the decision log are above what this kind of case usually gets.

I found **no Critical bugs and no confirmed correctness bug in the send path**. One mandatory deliverable is incomplete: there are no session exports for T20, T21 or this session. The main engineering risk is the **queue claim query**: its cost grows linearly with the number of due messages, so it is what breaks first if audiences grow. I confirmed and measured the P5 root cause, and it has a small fix. The rate-limit evidence (P2 and its unit test) is partly circular.

**Verdict: fit for delivery at the case's scale (3,000 users) once the session exports are added. F1, F3 and F4 are worth a small fix before delivery. The rest can be deferred.**

## 2. Findings

| ID | Title | Severity | Class |
|---|---|---|---|
| F1 | Claim query cost grows with the due backlog (no index order, OR branch) | Medium | Potential risk (cost reproduced) |
| F2 | `ai-sessions/` is missing the T20, T21 and current session exports | High | Confirmed (deliverable gap) |
| F3 | P5 root cause: a last-attempt subquery per row of the group, plus extra statements | Medium | Confirmed (measured) |
| F4 | 100/s evidence is circular: the worker stamps departure times before a DB round trip, and the provider's arrivals are only logged | Medium | Potential risk |
| F5 | Nothing stops a second worker replica, which would double the rate | Medium | Potential risk |
| F6 | A DB error before the send leaves the message `sending`. On reclaim it is wrongly flagged uncertain, and a persistent error loops forever | Low | Confirmed (code path) |
| F7 | DB pool size is implicit (`max(4, NumCPU)`): 41 worker goroutines share ≤ 8 connections | Low | Potential risk |
| F8 | Worker shutdown aborts in-flight provider requests instead of draining them | Low | Potential risk |
| F9 | `Retry-After` slows only the one message; the channel keeps sending at 100/s | Low | Justified trade-off |
| F10 | No AI token or cost telemetry. `LLM_TIMEOUT` 60 s with Opus, high effort and thinking can abort calls that are already paid | Low | Potential risk |
| F11 | Per-process state (AI limit, sign-in throttle, snapshot cache) changes behaviour when the API scales out | Low | Justified trade-off |
| F12 | The approval estimate records version A's over-limit count only; B's is dropped | Low | Confirmed (code) |
| F13 | Content immutability after approval is enforced only in handlers, not in the DB | Low | Justified trade-off |
| F14 | The per-email sign-in throttle lets anyone lock the demo account out for 15 minutes | Low | Justified trade-off |
| F15 | Planned T21 deliverables (OpenAPI/Swagger, final UI audit, D50) and a frontend linter are absent | Low | Confirmed (not mandatory) |
| F16 | Two rate mechanisms for one limit (`rate.Limiter` and `windowCap`) | Low | Justified trade-off |
| F17 | The rate test ignores a query error. A failing query would yield zero departures and a pass | Low | Confirmed (test weakness) |

### F1 — The claim query's cost grows with the due backlog (Medium, potential risk)
- **Code:** `internal/worker/queue.go:138-145` (Claim), `internal/db/migrations/00001_init.sql:154` (`messages_due_idx (channel, next_attempt_at)`), `internal/worker/dispatch.go:159` (20 claim loops per channel, 1 message per claim).
- **Evidence:** `EXPLAIN ANALYZE` of the exact Claim statement on a scratch DB with 1.2 M messages:
  - **3,000 due SMS messages:** `Bitmap Heap Scan … rows=3000` → `Sort` → `Limit 1`, **11.8 ms** per claim.
  - **33,000 due:** `rows=33000`, sort 2.9 MB, **27–33 ms** per claim.
  - **Cause:** the `OR` with the expired-lease branch forces a BitmapOr, and `ORDER BY next_attempt_at, id` cannot use index order because `id` is not in the index. `buildOne` gives a whole campaign the same `next_attempt_at`, so every row ties.
  - **With a partial index on `(channel, next_attempt_at, id)` and without the OR branch:** `Index Scan … rows=1`, **0.10 ms**.
- **Impact:** about 200 claims/s × 11 ms ≈ 2.2 s of Postgres time per second already at today's full send. At roughly 10× today's audience, claims would use 6 or more cores, and the 100/s target would be missed long before the API feels anything. D18's "light load for Postgres" holds only for small backlogs. History size does not matter (the partial index handles that); the backlog does.
- **Recommendation (S):**
  - Add the partial index `(channel, next_attempt_at, id) WHERE status IN ('pending','retry_scheduled')`.
  - Move expired-lease recovery into its own statement: a second `UNION ALL` branch with its own `LIMIT`, or a periodic reclaim in the 1 s campaign loop.
  - Optionally claim small batches.
  - Add a benchmark with about 30k due messages.

### F2 — Session exports incomplete (High, confirmed)
- **Evidence:** `ls ai-sessions` lists `01`–`07`. The last is `07-quality-engineering.txt`, from commit `f67727d`. T20 (PR #8, D64–D67) and T21 (PR #9, D68) have no export.
- **Impact:** CASE §8 requires exports of *all* sessions, checked for keys and PII.
- **Recommendation (S):** export the T20, T21 and audit sessions, redact them, run `make scan`, then commit.

### F3 — P5 recipient-details root cause (Medium, confirmed)
- **Code:** `internal/campaign/recipients.go:209` (page query), `:131-135` (two content reads outside the transaction, on every request), `:204` (total count).
- **Evidence:** `go test -run '^$' -bench Recipients -benchtime 20x -v ./internal/campaign/`:
  - **Sent first page:** 20.8 ms per request. The page statement takes 13.9 ms of that, of which about 10.5 ms is `SubPlan … loops=5100` (a `max(started_at)` lookup for every row of the group before `LIMIT 50`).
  - **The other ~7 statements:** about 0.3–2.5 ms each, mostly planning and round trips (the group count is 2.5 ms, the total count 1.7 ms).
  - **Scratch DB comparison:** sorting on a stored `last_attempt_at` column cut the same statement from 25.4 ms to 9.4 ms.
  - **Under back-to-back load:** the latency is CPU queueing, so cutting CPU per request is the lever (docs/performance.md agrees).
- **Recommendation (S–M):** in priority order:
  1. Add `messages.last_attempt_at`, set in `Complete`'s existing UPDATE (one SET clause), with a backfill migration, and sort on it.
  2. Load the A and B content only for the `skipped` group, where it is used.
  3. Reuse the group count as `total` when there is no `q`, language or variant filter.

  Together these should bring the request to about 8–10 ms and meet P5 back to back. Keyset paging is not needed.

### F4 — The rate-limit verification is circular (Medium, potential risk)
- **Code:**
  - **Departure stamp:** `dispatch.go:242`, taken before the second `load()` DB round trip at `:248`; `call` then records `StartedAt: departure` (`:341`).
  - **Test:** `dispatch_test.go:341` asserts on those stamps. The provider-side arrival count is only `t.Logf`.
  - **Perf check:** P2 in `internal/perf/perf_test.go:173-191` also reads `delivery_attempts.started_at`.
- **Reasoning:** the hard CASE limit is proven against the cap's own bookkeeping. The real request leaves after an unbounded DB wait (made worse by F7). Delays above the 50 ms slack (`capWindow`) can bunch real requests into more than 100 in a second at the provider. Not reproduced.
- **Recommendation (S):**
  - Re-check cancel and consent after the limiter but before `caps.wait`, then call immediately, so the stamp is the departure.
  - In the test, assert the fake provider's arrival times (≤ 100 in any 1.05 s window) instead of only logging them.

### F5 — No guard against a second worker (Medium, potential risk)
- **Code:** `docker-compose.yml:52` (a comment only), `cmd/scc/main.go` `runWorker`.
- **Reasoning:** `docker compose up --scale worker=2` or a second deploy starts a second in-process limiter, and the limit becomes 200/s per channel. Correctness holds (`TestLifecycle_TwoWorkers_NoDoubleSend`); the rate limit silently does not.
- **Recommendation (S):** at worker start, take `pg_try_advisory_lock(<const>)` on a dedicated connection. A second worker logs the conflict and waits or exits. This makes D19 enforceable without a shared limiter.

### F6 — A failed pre-send load leaves a phantom "uncertain" (Low, confirmed by code)
- **Code:** `dispatch.go:224` and `:248` (`load` error → `return err`, no `Complete`), `queue.go:141` (reclaim sets `delivery_uncertain` whenever the old status was `sending`).
- **Impact:** a transient DB error before any HTTP call marks a never-sent message uncertain 30 s later. If it is later skipped or cancelled, it shows as "uncertain / no_response_recorded" and D31 excludes it from resends forever.
  - **Persistent error** (missing content row after a manual DB edit): the message is reclaimed every 30 s indefinitely. No attempt is ever recorded, so `MaxAttempts` never fires, and the campaign never completes.
- **Recommendation (S):** on a pre-send failure, call `Complete` with `retry_scheduled` (or release the lease without the uncertain flag), and fail the message after N claims without an attempt.

### F7 — Implicit DB pool size (Low, potential risk)
- **Code:** `cmd/scc/main.go:54` (`pgxpool.New` with defaults).
- **Reasoning:**
  - **Worker:** about 41 goroutines (20 per channel plus the campaign loop) each need a connection for Claim, two loads and Complete. On a 2-CPU host the pool has 4 connections. With F1's claim cost, connection waits add send latency and feed F4.
  - **API:** the read-only RR transactions of SSE and recipients compete for the same few connections.
- **Recommendation (S):** set `pool_max_conns` explicitly per process (about 16 for the worker, so it doesn't depend on the host's CPU count) and expose pgxpool stats as metrics.

### F8 — Shutdown aborts in-flight sends (Low)
- **Code:** `dispatch.go:342` (the request uses the worker's ctx).
- **Impact:** every deploy turns up to 40 in-flight requests into `interrupted`/uncertain, although the provider finishes them. The retry is deduplicated by the idempotency key, so this is cosmetic unless the message is later cancelled.
- **Recommendation:** use a context with a drain deadline (≤ provider timeout) for the HTTP call. Defer.

### F9 — 429 is per message (Low, justified)
`dispatch.go:375`. Fine for a mock whose 429s are random. A real provider's 429 means "slow down": pause the channel's limiter for `Retry-After` when real providers are added.

### F10 — AI cost controls lack telemetry (Low)
- **What exists:** the controls are good. D63 counts every call including the retry, the SDK makes no retries (`anthropic.go:23`), AI runs only on explicit actions (D66), the mock is the default, and the brief is capped at 1,000 characters.
- **What is missing:**
  - Token usage per call is neither recorded nor exported (`msg.Usage` is ignored).
  - Interpretation uses Opus with `effort: high`, adaptive thinking and `MaxTokens` 16000 under a 60 s timeout. A slow answer is aborted after it is already billed, and the D63 retry rule then spends a second call.
- **Recommendation (S):** add a `scc_llm_tokens_total{operation,kind}` counter and log usage per call. Measure the p95 interpretation time before keeping 60 s.

### F11 — Per-process limits (Low, justified)
`ai_limit.go`, `auth/throttle.go`, `snapshotCache`. This is documented (D55, D59, D63) and right for one API replica. With N replicas the limits multiply. Revisit when the API scales out (move them to Postgres).

### F12 — The approval estimate ignores version B (Low, confirmed)
- **Code:** `approve.go:183` estimates `cont` (version A) only. `approval_estimate` therefore stores no `over_limit_b`, while `GET …/estimate` (`:336`ff) shows it.
- **Impact:** the "estimate vs actual" view after sending understates the expected too-long skips for A/B campaigns.
- **Recommendation:** store B's estimate too (S), or document it.

### F13 — Immutability lives only in handlers (Low, justified)
Every content write is guarded by `status = 'draft'` (e.g. `content.go:352`). The README admits that a direct DB edit changes what retries send. Acceptable here. A trigger that rejects `campaign_content` changes for non-draft campaigns would be cheap if operators get DB access.

### F14 — Lockout of the demo account (Low, justified)
`auth/auth.go` counts attempts per email before checking the password. Anyone can lock the single demo login for 15 minutes. This is the standard trade-off; it is fine for a local demo.

### F15 — Planned but absent extras (Low)
D50 plans OpenAPI and Swagger UI, a final UI/UX audit and fresh-clone delivery checks for T21. None of them are in the repository, and there is no frontend linter (the README says so). None is required by CASE.md. Either do them or move them to *Future improvements*.

### F16 — Complexity (Low, justified)
- **Rate mechanisms:** `rate.Limiter` plus `windowCap` enforce one limit. The cap alone would satisfy the requirement, and the limiter only smooths the flow. It is kept for a measured reason (D19 history), so I would keep both.
- **Elsewhere:** I found no dead abstractions. Dependencies are minimal (stdlib HTTP, pgx, goose, the Anthropic SDK, prometheus, x/time, x/crypto, Reka UI).
- **Candidates to simplify later:**
  - The lazy-loaded Turkish dictionary (D58), which saves 32 kB.
  - The heuristic i18n scan test.
  - `ContentView.vue` (876 lines), which would benefit from splitting.

### F17 — A test that can pass on error (Low, confirmed)
At `dispatch_test.go:333`, `rows, _ := f.pool.Query(...)` discards the error. A broken query yields no departures, so `mostInOneSecond` returns 0 and the assertion passes. Check the error and assert the expected count of departures.

## 3. Requirements coverage (CASE.md)

| Requirement | Status | Evidence |
|---|---|---|
| Claude creates audience rules from the brief | Met | `campaign/interpret.go`, `llm/anthropic.go` (JSON schema), `interpret_test.go` |
| Backend validates rules; never runs AI SQL | Met | `audience/rules.go`, `audience/sql.go` (all values via `b.arg`), `validate_test.go` |
| Show the matching-user count | Met | `audience.Count`, `approve.go` estimate, `count_test.go` |
| SMS and email consent checked separately; never sent without consent | Met | `audience/sql.go:55` consent column per channel at build, re-checked at send `dispatch.go` `skip()`, `TestLifecycle_ConsentRevokedBetweenRetries` |
| Claude generates SMS and email content | Met | `llm/anthropic_content.go`, `campaign/content.go` |
| Content streamed to the frontend | Met | SSE `generateContent`, `llm/fieldstream.go` (D34), `web/src/campaign/stream.test.ts` |
| SMS ≤ 160, subject ≤ 80 | Met | `content/template.go` (draft), `dispatch.go overLimit` (send time, D3) |
| `{{first_name}}`-style variables | Met | `content.Parse/Render`, `template_test.go` |
| Preview and edit before approval | Met | `ContentView.vue`, `MessagePreview.vue`, `PUT /content`, `edit_test.go` |
| Asynchronous queue | Met | messages table as queue (D18), `worker/queue.go` |
| ≤ 100 msg/s per channel | Met (single worker); evidence weak | `dispatch.go` limiter and cap; P2 peaks 98–100. See F4, F5 |
| No duplicate messages to a user | Met | `UNIQUE (campaign_id,user_id,channel)`, provider idempotency (`provider.go claim`), lifecycle tests |
| Retry strategy | Met | `Backoff`, `TestBackoff_*`, `TestProcess_Classification` |
| Mock SMS and email providers | Met | `internal/provider`, compose `mock-provider` |
| Live delivery status on the dashboard | Met | SSE `events.go`, `DeliveryStatus.vue`, `events_test.go` |
| Metrics endpoints | Met | `/metrics` on every process (`internal/obs`) |
| Health endpoints | Met | `/healthz`, `/readyz`, compose healthchecks |
| Claude understands requests; structured JSON | Met | JSON-schema output config, strict decoding |
| Backend validates AI output; invalid output handled safely | Met | D15 retry-once, `parseContent`, `interpret_test.go`, `content_test.go` |
| Tests work without an API key (mock LLM) | Met | `llm/mock.go`, `LLM_MODE=mock` default |
| Key read from `ANTHROPIC_API_KEY`; `.env.example` present | Met | `config.go`, `.env.example` (placeholder value) |
| 3,000 fictional users with name, contact, city, history, consents | Met | `db/seed.go`, `seed_test.go` |
| Data supports varied audience rules | Met | event tables (D33), seed covers every catalog field |
| Mock providers: success, delays, temporary errors, 429 | Met | `provider.go draw`, `TestFailureInjection_IsDeterministic` |
| Vue frontend, Go backend | Met | `web/`, `cmd/scc` |
| Git repository | Met | GitHub, PRs #1–#9 |
| One-command `docker compose up` | Met (not re-verified from a fresh clone) | `docker-compose.yml`; migrate → api/worker/provider |
| DB setup and seed in the setup; no duplicates on rerun | Met | `migrate` service, advisory lock, `seed_meta`, `seed_test.go` |
| Mock providers in Compose | Met | `mock-provider` service |
| Working `make test` | Met (not run in this audit, per instructions) | `Makefile`, CI `make test` |
| README with setup and architecture | Met | `README.md` |
| DECISIONS.md | Met | D1–D68 |
| `ai-sessions/` with all sessions, checked for keys and PII | **Partial** | 01–07 only; T20, T21 and audit missing (F2) |
| Optional: scheduled sending | Met | D29, `buildOne` `send_at <= now`, `TestBuildAudience_OnlyWhenDue` |
| Optional: A/B testing | Met (delivery-only report, by design) | D68, `queue.go` split, `variant_test.go`, `ab_test.go` |
| Optional: messages in the user's language | Met (tr, en; `und` legacy) | D64, `language_test.go`, `languages_test.go` |
| Optional: CI pipeline | Met | `.github/workflows/ci.yml`, `e2e.yml` |
| Unfinished work explained in the README | Met | README *Known limitations*, *Future improvements* |

## 4. Architecture decisions assessment

| Decision | Still appropriate? | Why / revisit trigger |
|---|---|---|
| Single worker replica with an in-process limiter (D19) | Yes, with caveats | Simplest exact enforcement of 100/s; the worker is not the bottleneck at this scale. **Caveat:** add the advisory-lock guard (F5). Revisit when the target exceeds about 1 replica's I/O (several hundred/s) or HA is needed. Then a shared limiter (Postgres token row or per-replica split), plus leader election for `BuildAudience`. |
| Postgres as the queue (D18) | Yes, with caveats | No dual write, and leases, fencing and dedup live in one table. Fix the claim plan (F1); without the fix, the backlog size breaks it first. Revisit above ~10⁵ due messages or ~1k msg/s, or when partitioning or archiving `messages` becomes necessary. |
| SSE status stream with a per-process snapshot cache (D21, D36, D59) | Yes | Measured (P6: 13 ms/s of DB time for 20 screens). Revisit with more than one API replica (the cache is per process) or hundreds of concurrent campaigns: move to a single poller or LISTEN/NOTIFY on campaign completion only. |
| AI calls synchronous in the API, no AI worker | Yes | Interactive and streaming by nature. Calls hold no DB connection and are separate from sending (D20). A queue would add latency and a polling UI for no gain. Revisit for batch generation (many campaigns or languages unattended) or AI calls over 60 s: then use a job table plus SSE progress. |
| Multilingual model (D64, D66, D67) | Yes | The language is fixed per message at build, there is one catalog, approval is per language, and nothing is overwritten silently. Clean, and well tested. Revisit when there are more than about 3 languages: generation cost, and a languages table instead of CHECK lists in three migrations. |
| A/B design (D68) | Yes | md5-ordered alternation per language is exact and reproducible; one version per recipient on all channels; stored on the message. Honest about having no outcomes. Revisit when outcome data (orders after `sent_at`) exists; F12 should be fixed first. |
| Auth: DB sessions, SameSite=Lax, Origin check, single role (D10, D55, D56) | Yes | Revocable, only hashed tokens stored, CSRF covered in two layers. Revisit for production: RBAC and four-eyes approval, proxy-aware throttling, `Secure` cookies by default. |
| One image and process split (D20) | Yes | Good fault isolation for little cost. |
| Hand-written SQL and validation, minimal libraries (D22) | Yes | Auditable, which matters for the "never run AI SQL" rule. The cost is verbose handlers (`http.go` 698 lines). |
| Frontend: Vue 3, composables, Reka UI, own i18n (D25, D41, D44) | Yes, with caveats | Small dependency surface; no Pinia is right at this size. Large views (`ContentView.vue` 876 lines) and no ESLint are the debt. Revisit when the team grows or more screens are added. |
| Audience evaluated when sending starts (D30) | Yes | Correct for scheduled sends; the estimate vs actual difference is shown. |
| Resend excludes uncertain deliveries (D31) | Yes (product call) | It prefers never double-sending to recovering every recipient. F6 widens the excluded set needlessly. |

## 5. Test coverage assessment

**Strengths**
- About 330 Go tests run against a real Postgres (D27), with the uncomfortable cases covered by tests, not only by comments:
  - concurrent claimers;
  - a stale `Complete` after lease loss;
  - a crash after acceptance;
  - shutdown mid-attempt;
  - a timeout followed by a cancelled resend;
  - two workers with no double send;
  - consent revoked between retries.
- The provider's idempotency is tested for concurrency and restarts. The provider role is tested to be unable to read app tables.
- Migrations are tested up and down with legacy data. There is a consistency test for the language catalog.
- 33 Vitest files, a Playwright suite in CI on every PR, a perf harness with explicit targets, and gitleaks with a self-test.

**Gaps**
1. **Rate limit (F4, F17):** asserted on worker-side stamps; provider arrivals are not asserted.
2. **Claim performance with a large backlog (F1):** no benchmark. The recipients benchmark exists, but there is none for the queue.
3. **Pre-send failure paths (F6):** no test that a `load` error neither marks the message uncertain nor loops forever.
4. **Pool exhaustion and slow DB:** no test covers worker behaviour when DB latency rises.
5. **Approval estimate for A/B (F12):** no test asserts that `approval_estimate` contains B's over-limit count.
6. **Real Anthropic path:** covered only by HTTP-stubbed tests (`anthropic_test.go`). This is fine for CI, but nothing records a real end-to-end run; the README says it is a manual check.
7. **Frontend:** unit tests focus on logic modules (rules, saver, stream, router). Large views such as `ContentView.vue` and `ReviewView.vue` rely on the e2e suite.

## 6. Prioritized remediation plan

| # | Action | Effort | Findings | When |
|---|---|---|---|---|
| 1 | Export, redact and scan the T20, T21 and audit sessions into `ai-sessions/` | S | F2 | **Before delivery** (mandatory) |
| 2 | Claim: partial index `(channel, next_attempt_at, id)`, expired-lease reclaim as a separate statement, and a benchmark with ~30k due messages | S | F1 | Before delivery (recommended) |
| 3 | Re-order `Process` so the cap stamp is the real departure; assert provider arrivals in the rate test; check the ignored query error | S | F4, F17 | Before delivery (recommended) |
| 4 | Advisory-lock guard so only one worker runs | S | F5 | Before delivery (recommended, ~20 lines) |
| 5 | Recipients: `messages.last_attempt_at` written in `Complete`, content read only for `skipped`, total reused from the group count | S–M | F3 | Before delivery if P5 must be met; otherwise defer |
| 6 | Release the lease without the uncertain flag on a pre-send error; give up after N attempt-less claims | S | F6 | Defer |
| 7 | Explicit `pool_max_conns` per process and pool metrics | S | F7 | Defer |
| 8 | Token-usage metric and log; measure interpretation p95 against `LLM_TIMEOUT` | S | F10 | Defer |
| 9 | Store version B's over-limit count in `approval_estimate` | S | F12 | Defer |
| 10 | Drain in-flight sends on shutdown; pause the channel on 429 for real providers | S | F8, F9 | Defer |
| 11 | OpenAPI/Swagger, ESLint, split `ContentView.vue`, or list them as future work | M | F15, F16 | Defer |

## 7. What I did not check

- I did not run the full `make test`, `make e2e` or `make perf` (per the instructions), so I rely on the documented results for P1, P2, P4 and P6 and the browser numbers. The focused runs were the recipients benchmark and the EXPLAIN experiments on the scratch DB (since dropped).
- I did not verify a fresh clone with `docker compose up`.
- I did not run the real Anthropic API.
- I did not read the `ai-sessions/` contents for secrets or PII.
- I did not review the frontend views line by line, nor accessibility or the UI/UX against `design/`.
- I did not review the seed generator's data distributions in detail.
- I did not review `docs/security-review.md`'s individual claims beyond the auth and header code.
- I did not run a load test of F1 under the real worker; only plan costs were measured. The throughput collapse is an extrapolation.
