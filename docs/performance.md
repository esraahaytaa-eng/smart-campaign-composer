# Performance: recipient details and the live status stream

Measured 2026-10-23 for T18 (D46, D47).

## Method

`BenchmarkRecipients` in `internal/campaign/recipients_bench_test.go`:

- The real seed (3,000 customers) and a campaign that reached **every** customer on both channels: 6,000 messages, the worst case at this size. Mix per 100 messages: 85 sent (10% after a 429), 3 skipped for length, 2 skipped for consent, 1 failed after 5 attempts, 1 uncertain (timeout), 3 retrying, 5 pending. About 6,200 attempts.
- Nine older campaigns of the same size (62,000 attempts in total), and a second run with 29 (186,000 attempts), to see whether cost grows with history.
- Each case calls the handler's query function directly (no HTTP), 30 iterations, 3 runs. With `-v` it prints `EXPLAIN (ANALYZE, BUFFERS)` for every statement the handler ran, captured with a pgx tracer.
- Docker Desktop on a 4-core laptop (i5-1038NG7), Postgres 16 in a container, warm cache.

```sh
docker compose --profile test run --rm go-test \
  go test -run '^$' -bench Recipients -benchtime 30x -v ./internal/campaign/
# more history: add -e BENCH_CAMPAIGNS=30 after `run --rm`
```

## Results (ms per request, median of 3 runs)

| Case | Before | After | After, 3× history |
|---|---:|---:|---:|
| Sent, first page (5,100 rows in the group) | 103 | 19 | 20 |
| Sent, last page | 110 | 17 | 18 |
| Sent, name search "ay" | 58 | 19 | 21 |
| Failed, SMS (30 rows) | 12 | 13 | 12 |
| Skipped, with the over-limit length | 19 | 14 | 14 |
| Pending (never attempted) | 22 | 10 | 10 |
| Status snapshot (every second per open live screen) | 24 (45 with 3× history) | 19 | 22 |

About 8–10 ms of every request is fixed: 7–8 statements in one read-only transaction, each with a round trip and 0.2–1 ms of planning.

## What the plans showed and what changed

1. **Attempt lookups for every row of the group.** The reason code needs the message's latest attempt (two `LATERAL` lookups). The reason count, the total and the page query all joined them, so the "sent" group did about 30,000 index probes per request although sent messages have no reason. Now:
   - the reason count runs only for groups that have reasons;
   - the total counts messages without the lookups (users are joined only for a name search);
   - the page first picks its 50 ids, and only those 50 get the lookups.
2. **The snapshot's retry and 429 counts read the whole attempts table.** The planner chose a full scan of `delivery_attempts` joined by hash (13.6 ms at 62,000 attempts, 18 ms at 186,000), so every open live screen got slower as campaigns accumulated. They are now counted per message through the existing index, skipping sent messages claimed once: `attempt_count` counts claims, so such a message had exactly one attempt, the accepted one, which adds neither a retry nor a 429. That leaves about 15% of a typical campaign: 4.8 ms, and it no longer grows with history.

## What remains, and the upgrade paths

- **Sorting by last attempt** costs one small index lookup per message in the group: about 15 ms of the 19 ms for 5,100 sent messages. A `messages.last_attempt_at` column written by the worker (with an index on campaign and that column) would remove it. Not done: it is a migration and a worker change for 15 ms on a panel opened by hand.
- **Name search** filters the 3,000 customers with `translate`/`lower` (about 4 ms). A trigram index would only matter with far more customers.
- **Offset paging** reads the whole group to sort it, so cost grows with the campaign's size (about 3 ms per 1,000 messages in the group), not with the page number. Keyset paging on the stored last-attempt time is the upgrade if campaigns grow well beyond 3,000 customers.
- **The status stream** still runs its queries once a second for each open live screen (about 20 ms of database time at this size). A shared poller per campaign is the upgrade path (README, Known limitations).

# Test suite speed (T19 step 2, D57 targets T1–T3)

Measured 2026-10-23 on the same machine. Each number is a full wall-clock run of the make target.

## Baseline (before any change)

| | Time | Coverage |
|---|---|---|
| `make test` | 132 s, 128 s | 341 Go tests (with subtests), 81.5% of statements; 333 Vitest tests |
| `make e2e` (against the running app) | 147 s | 181 browser checks |

Where the time went: Go tests ran one after another inside each package, so a package took the sum of its tests (`db` 77 s, `campaign` 71 s, `e2e` 69 s). Every test migrated its own database (~125 ms each), and the frontend half reinstalled its packages on every run (~28 s of its 45 s).

## What changed, each measured

| Change | Effect |
|---|---|
| Each test database copied from a template migrated once (`dbtest.MigratedDB`) | Go half 85 → 78 s; `campaign` 71 → 35 s, `worker` 43 → 28 s |
| `fsync`, `synchronous_commit`, `full_page_writes` off on the test database | No measurable gain (it is on tmpfs); not kept |
| `db` and `e2e` tests in parallel (each has its own database) | Go half 83 → 62 s |
| Frontend packages reinstalled only when the lockfile changes; Go and frontend at the same time | Frontend half 45 → about 22 s when the machine is quiet |
| `go vet` left to `make lint` (`-vet=off` in the test run); 2 s retry cap in `TestEndToEnd` | `make test` 79–93 s → 62–68 s |
| The browser suite on its own stack, run in two lanes, one shared send | Suite 147 → 67–82 s |
| Shorter mock-provider delays (planned in D53) | Not done: sends are limited by the 100/s rate, not by the provider's 20–200 ms answers |
| Faster health checks on the browser-suite stack | No measurable gain; not kept |

A fair comparison of `make test`, alternating the old code (`main`) and the new code three times each under the same conditions, gave medians of 111 s (old) and 90 s (new) before the last two changes above.

## Results

| Target | Baseline | Now (5 consecutive runs) | Met |
|---|---|---|---|
| T1 `make test` ≤ 50% | 130 s | 83, 74, 61, 61, 62 s (median 62 s, 48%) | yes |
| T2 `make e2e` ≤ 50% | 147 s | suite 96, 82, 67, 71, 67 s (median 71 s, 48%); whole target including starting and removing its stack 139, 115, 91, 98, 90 s (median 98 s, 67%) | the suite yes; with stack startup, no |
| T3 no flaky failures in 5 consecutive full runs | — | 10 of 10 runs passed | yes |

Coverage is preserved: all 341 baseline Go tests still run and pass, 4 new ones were added, statement coverage 81.5% → 82.1%; Vitest 333 → 333; browser checks 181 → 181.

Flaky tests found and fixed on the way (each showed up under load; the first one also failed on `main`):
- the provider role's password set by two packages at once ("tuple concurrently updated"); now serialized, with a regression test that failed every time before the fix;
- a worker test whose 400 ms window could expire during setup; now 3 s, and it fails with a clear message if the worker never waited;
- a provider test that slept 400 ms instead of waiting for the stored result;
- the scheduled-send check in `TestEndToEnd`, now with a 2 s margin and a database-clock check that it ran before the send time.

Timings on a laptop vary a lot with other load (the same `make test` took 75–112 s in one series); compare runs made back to back.

# Campaign list at 10,000 campaigns (T19 step 5, target P4)

`BenchmarkCampaignList` in `internal/campaign/list_bench_test.go`: 10,000 campaigns across all statuses, the handler called directly, 50 runs each (`go test -run '^$' -bench CampaignList -benchtime 50x -v ./internal/campaign/` also prints the query plans).

| Case | ms per request |
|---|---:|
| First page | 5.7 |
| Status filter | 5.8 |
| Search and filter (`status=draft&q=istanbul`) | 17.5 |
| Last page (page 400) | 12.3 |
| Dashboard (counts and six rows) | 15.0 |

All well under the 100 ms target. The search folds Turkish letters on every row (no index can serve `LIKE '%…%'` on folded text); at this size that costs about 12 ms. A trigram index on the folded brief is the upgrade path if campaigns grow by orders of magnitude.

# Performance targets (T19 step 7, D57)

Measured 2026-10-23 with `make perf` (below) on the same laptop: 4 cores / 8 threads, Docker Desktop given all 8 threads and 8 of 16 GB. **The machine was not quiet**: the host's 1-minute load average was 6–44 during the runs (browser, editor, a spell-check service at ~70% of a core, and the local app stack, whose idle worker and Postgres use ~0.3 CPU). The load generator runs in the same Docker VM as the stack it measures. Numbers are from single runs unless a range is given; they move by ±30% between runs.

## Method

`make perf` starts its own stack (Compose project `scc-perf`, `docker-compose.perf.yml`) with the **default settings**: the 3,000-customer seed, mock AI, mock providers at their default failure rates, 5 attempts, 30 s retry cap. Only measuring is added: `pg_stat_statements`, and the api signs in to Postgres as its own role (`scc_api`, same rights) so its database time is separate from the worker's. Then:

1. **Full send** (`internal/perf`, build tag `perf`): a campaign to every consenting customer on both channels, approved to send now, while 20 live screens follow its status stream and a poller reads the database every 100 ms. P1 from each message's final time, P2 from the recorded attempt times (sliding 1 s window), P6 from the api's database time and from each screen update's delay against the poller.
2. **Marketers**: 10,000 campaigns and a finished 6,000-message campaign are inserted, then 10 marketers (own sessions, own drafts) repeat a round of 12 requests for 2 minutes: list with search and filter (twice), campaign, recipients (twice), estimate, list page, dashboard, catalog, save messages, save rules. `PERF_MARKETERS`, `PERF_THINK` (pause after each request) and `PERF_DURATION` change the load; `PERF_ONLY=marketers` skips the send.
3. **Browser** (`e2e/frontend-perf.mjs`): cold loads (new context, empty cache) of sign-in, dashboard and the campaign list, and five interactions, on a desktop and on a phone profile (Lighthouse's mobile defaults: DevTools "Slow 4G" and a 4× slower CPU). Medians of 3.

It prints each target with what was measured and fails if one is missed.

## Results

| # | Target | Measured | Met |
|---|---|---|---|
| P1 | Full send (4,191 messages): 95% final ≤ 35 s, all ≤ 75 s | 4 runs: 95% at 25.8–34.6 s, all at 42.6–52.9 s. Every message ended sent. | yes, with little margin under load |
| P2 | ≤ 100 provider requests per channel in any 1 s window | peaks 98–100 per channel over ~2,320 attempts per channel | yes |
| P3 | 10 marketers: p95 reads ≤ 200 ms, writes ≤ 300 ms, 0 server errors | with a 1 s pause per request (10 req/s in all): reads 40 ms, writes 18 ms, 0 errors. Back to back (144 req/s): reads 208 ms, writes 118 ms, 0 errors | depends on the reading (below) |
| P4 | Campaign list, 10,000 campaigns, search and filter: p95 ≤ 100 ms | 44 ms (1 s pause), 58 ms (one marketer back to back), 270 ms (10 back to back) | same |
| P5 | Recipient details, 6,000 messages: p95 ≤ 50 ms | 45 ms (1 s pause), 62 ms (one marketer back to back), 276 ms (10 back to back) | only with pauses, and barely |
| P6 | 20 live screens: db time ≤ 400 ms/s, each update ≤ 2 s behind | 4 runs: 1,005–2,178 ms/s; updates behind by p95 0.8–1.4 s, max 1.1–3.9 s; every screen got the end | no |
| P7 | First load ≤ 100 kB gzipped; largest chunk ≤ 40 kB gzipped | sign-in 93.6 kB, dashboard 98.4 kB; largest chunk 43.9 kB (Vue's runtime alone is ~43 kB). **But nothing is served compressed**: browsers download 263–294 kB | first part only on paper |
| T1 | `make test` ≤ 50% of 130 s | 5 runs: 69–99 s, median 76 s (58%) | no |
| T2 | `make e2e` suite ≤ 50% of 147 s | 5 runs: 74–84 s, median 77 s (52%), now with 298 checks instead of 181 | no, narrowly |
| T3 | No flaky failures in 5 consecutive full runs | `make test` 5/5, `make e2e` 5/5 (298/298 each) | yes |

### Marketers in detail (ms)

| Request | 10 marketers, 1 s pause: p50 / p95 | 1 marketer, back to back: p50 / p95 | 10 marketers, back to back: p50 / p95 |
|---|---:|---:|---:|
| list search and filter | 30 / 44 | 30 / 58 | 104 / 270 |
| recipients | 29 / 45 | 30 / 62 | 96 / 276 |
| dashboard | 21 / 38 | 19 / 38 | 67 / 195 |
| list page | 17 / 28 | 15 / 27 | 53 / 156 |
| estimate | 5 / 11 | 6 / 12 | 23 / 73 |
| campaign | 3 / 7 | 3 / 6 | 12 / 39 |
| save messages / rules | 10 / 20, 7 / 16 | 10 / 18, 10 / 17 | 36 / 106, 42 / 126 |

Where the time goes (`pg_stat_statements`, api role): the list's rows and its count (each ~10–12 ms alone: every row's brief is folded for the search), the recipients page (~12 ms: one last-attempt lookup per message in the group), and the status counts over all campaigns (~4 ms, run by both the list and the dashboard). Back to back, the same statements take ~40 ms: ten of them compete for the CPUs, so the latency is queueing, not a slower plan.

### Browser (medians of 3 cold loads, ms)

| Profile, page | First paint | Usable | JS run | Style + layout | All main-thread work | Blocking (TBT) | JS downloaded |
|---|---:|---:|---:|---:|---:|---:|---:|
| Desktop, sign-in | 184 | 195 | 13 | 21 | 135 | 0 | 263 kB, uncompressed |
| Desktop, campaign list | 248 | 329 | 15 | 38 | 280 | 10 | 294 kB |
| Phone, sign-in | 4,636 | 4,841 | 167 | 168 | 2,233 | 613 | 263 kB |
| Phone, dashboard | 5,244 | 5,905 | 244 | 176 | 2,348 | 1,001 | 276 kB |
| Phone, campaign list | 5,532 | 6,646 | 140 | 260 | 2,341 | 1,108 | 294 kB |

A trace of the phone sign-in: compiling and running all JavaScript ~350 ms (4× CPU), style and layout ~220 ms, the rest is the throttled renderer's own task overhead. The 43.9 kB chunk is a bit under half of the code, so at most ~150 ms of main-thread time on the phone profile; splitting it would not reduce the bytes or the round trips, which dominate (~4.6 s to first paint on slow 4G).

Interactions (slowest event, as INP measures it; Google rates ≤ 200 ms good, ≤ 500 ms needs improvement):

| Interaction | Desktop | Phone |
|---|---:|---:|
| Status filter (list re-renders) | 56 | 400–480 |
| Typing a search | 56 | 264–296 |
| Türkçe, first time (downloads 32 kB) | 32–64 | 64–72 (result on screen 1.4 s) |
| Back to English (whole page re-renders) | 40–64 | 336–432 |
| Opening a campaign | 24–32 | 88 |

## Test durations: why they vary

- **Each `make test` is as long as its slowest package**, and that package runs its tests one after another: `campaign` (82 tests, 32 s alone, 70–95 s inside the full run) because all 13 packages run at once against one Postgres and the same CPUs. A test's own setup is small (database copy ~40 ms, sign-in and fixtures ~50 ms, drop ~20 ms); most of the time is the tests' own requests.
- **Host load decides the rest.** The same command took 69–115 s today with the host's load average between 5 and 98 (one spike during a run). Step 2's 62 s median was measured on a quieter machine.
- **The suites grew only a little since step 2** (233 → 248 Go tests, 181 → 298 browser checks), so growth explains the browser suite's few extra seconds, not `make test`'s.

## After the fixes (D59–D62), 2026-10-24

Same machine and method; the host was quieter than before (load average 5–13 instead of 6–44), so compare the changes that the fixes explain, not every number.

| Fix | Before | After |
|---|---|---|
| **D59, live screens share a snapshot** (P6) | 1,005–2,178 ms of api database time per second; 5,291–6,491 statements; slowest update 1.1–3.9 s behind | 23–29 ms/s; 263–275 statements; slowest update 0.98–1.48 s behind (3 runs). **P6 met.** |
| **D60, precompressed assets** (P7, loading) | 263–294 kB of JavaScript per first visit | 96–109 kB. Phone profile: sign-in first paint 4.6 → 2.5 s, dashboard 5.2 → 3.3 s, campaign list 5.5 → 4.0 s. Desktop unchanged (~0.2 s, a local connection). |
| **D61, parallel database tests** (T1) | `make test` 76 s median (busy host) | Alternating old and new code: 60–64 s both ways, so no gain for the full run; one package alone is faster (`campaign` 15–20 → 4–5 s). At this load both meet T1; 9 of 9 full runs passed. |
| **D62, pager above the list** | Below the fold at 1440×900 | 155 px from the top; checked in the browser |

D59 needed two corrections on the way, both measured: a new stream's first snapshot is read fresh (the browser suite caught a page opened again right after a resend that still showed none), and a snapshot is reused for a quarter of the poll interval instead of half (with half, the slowest update was 1.4–2.1 s behind, at the edge of 2 s).

Not changed by these fixes, and still as above: P4 and P5 under back-to-back load (list search p95 112–161 ms, recipients 120–170 ms; met with realistic pauses), and the largest-chunk part of P7 (43.9 kB gzipped; Vue's runtime alone is ~43 kB). The phone interactions measured faster after (status filter 120–128 ms, back to English 128–136 ms) with no rendering change, so that is the quieter host, not a fix.

## With two content languages (T20), 2026-10-24

`make perf` now writes Turkish and English content for the full send and the marketers' drafts (D64), so the estimate, the send and recipient details run over two languages. Same machine; load average 4–10 during the runs.

| Target | Measured (last run; range over runs 1, 3 and 4: run 1 had the 500 below, run 2 ran at load 13–17 and is left out) | Met |
|---|---|---|
| P1 full send | 95% at 24.2 s, all at 39.3 s (all at 38.0–42.5 s) | yes |
| P2 rate | peaks 100 per channel | yes |
| P3 10 marketers back to back | reads p95 55.6 ms, writes 20.2 ms, 0 server errors | yes |
| P4 list search | p95 60.7 ms (44–61 ms) | yes |
| P5 recipient details | p95 72.5 ms (51–73 ms) | no, as before T20: back-to-back only (was 120–170 ms); realistic pauses still meet it |
| P6 live screens | 14 ms/s of database time, slowest update 0.99 s behind (0.99–2.06 s over the runs) | yes |
| Estimate with two languages | p50 / p95 6.9 / 12.7 ms (was 5 / 11 with one) | — |
| P7 first load | 96.5 / 103.7 / 109.8 kB gzipped (sign-in / dashboard / list), as after D60 (96–109 kB) | unchanged |

The first run found a 500 in recipient details: a finished campaign with no language anywhere (the perf fixture inserts one directly, without content) made the row's language NULL. Real campaigns can't reach that state, but the row now reads "not recorded" (`und`) instead (`TestRecipients_NoLanguageAnywhere`).

## With an A/B test (T21), 2026-10-24

`make perf` now runs the full send as a two-language A/B campaign (D68): the build also splits each language's recipients 50/50 and the live screens count per version. One run, same machine, load average 7–9.

| Target | Measured | Met |
|---|---|---|
| P1 full send | 4,191 messages: 95% at 24.2 s, all at 41.7 s (T20: all at 38.0–42.5 s) | yes |
| P2 rate | peaks 100 per channel | yes |
| P3 10 marketers back to back | reads p95 57.1 ms, writes 23.3 ms, 0 server errors | yes |
| P4 list search | p95 64.7 ms | yes |
| P5 recipient details | p95 78.9 ms (T20: 51–73 ms) | no, back-to-back only, as before; each request now also reads version B's text for the over-limit detail (one more small query). One run, so the 6 ms over T20's last run isn't shown to be more than noise |
| P6 live screens | 13 ms/s of database time, slowest update 1.13 s behind | yes |
| P7 first load | 96.8 / 104.1 / 110.2 kB gzipped (sign-in / dashboard / list), about 0.3 kB more than T20 (the A/B text) | unchanged |

## Audit remediation (D69), 2026-10-24

Measured before and after each change, on the same schema and data. The queue numbers come from `BenchmarkQueue` (`internal/worker/queue_bench_test.go`): 3,000 customers, 60,000 finished messages of history, and a due backlog. It runs the worker's real database path per message: claim, two loads, complete (7 statements).

| Queue | Before | After |
|---|---|---|
| Claim, 6,000 due (`EXPLAIN ANALYZE`) | 1.69 ms: Bitmap scan of 3,000 rows, then a sort | 0.056 ms: Index Scan, 1 row |
| Claim, 30,000 due | 7.55 ms: 15,000 rows sorted | 0.057 ms |
| One message at a time, 6,000 / 30,000 due | 4.73 / 8.92 ms | 3.68 / 3.65 ms |
| 20 senders per channel, 6,000 / 30,000 due | 1,127 / 524 msgs/s | 1,481 / 1,523 msgs/s |

The claim's cost no longer grows with the backlog. The database ceiling is about 7× the 200 msgs/s the two channels need. What remains per message are its 7 round trips; `Complete` alone is 4 (begin, attempt insert, update, commit). One statement could do it, but it isn't needed at this rate.

Recipient details (`BenchmarkRecipients`, the old and new code on the same data, two runs each):

| Case | Before | After | Statements |
|---|---|---|---|
| sent, first page | 23.1 ms | 8.8 ms | 9 → 6 |
| sent, last page | 23.1 ms | 8.3 ms | 8 → 5 |
| sent, name search | 26.3 ms | 22.2 ms | 9 → 7 |
| failed (SMS) | 15.9 ms | 12.0 ms | 10 → 7 |
| skipped, with detail | 17.7 ms | 14.2 ms | 10 → 9 |
| status snapshot (unchanged) | 24.6 ms | 24.6 ms | — |

The page statement went from 19.0 ms to 5.1 ms of execution (planning from 6.6 to 1.5 ms): it sorts on the stored `last_attempt_at` instead of looking up 5,100 attempts. An index on that column would save about 3 ms more but add a write to every result, so it was not added.

`make perf` (the Go checks only; the frontend is unchanged), run twice. The first run's recipient fixture had no `last_attempt_at` and the host was busier (P4, whose code is unchanged, measured 97.7 ms). The second run is representative:

| Target | Measured | Met |
|---|---|---|
| P1 full send | 4,191 messages: 95% at 24.2 s, all at 42.1 s | yes |
| P2 rate | peaks 100 per channel | yes |
| P3 10 marketers back to back | reads p95 51.2 ms, writes 24.7 ms, 31,278 requests (first run: 26,741), 0 server errors | yes |
| P4 list search | p95 72.4 ms | yes |
| P5 recipient details | p95 55.4 ms (T21: 78.9 ms) | no, back to back only, as before. The page statement now averages 8.9 ms under that load. The campaign list's three statements now use the most database time (about 200 s of the phase), so the next gain is there, not in recipient details |
| P6 live screens | 13 ms/s of database time, slowest update 1.86 s behind | yes |
