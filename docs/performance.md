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
