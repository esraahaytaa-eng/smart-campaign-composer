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
