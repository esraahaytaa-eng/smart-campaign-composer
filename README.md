# Smart Campaign Composer

AI-assisted SMS and email campaign tool: Claude suggests the audience and content, the backend validates, the marketer approves, the system sends to mock providers. Requirements: [`CASE.md`](CASE.md). Decisions: [`DECISIONS.md`](DECISIONS.md). Plan: [`docs/plans/2026-10-21-implementation-plan.md`](docs/plans/2026-10-21-implementation-plan.md).

> **Status: work in progress.** Only the project skeleton exists (plan task T1): Postgres, a `migrate` step that checks the database connection, and an `api` with health and metrics endpoints. No campaign features, seed data, worker, mock providers or frontend yet.

## Requirements

Docker with Compose, and `make`. No local Go or Node toolchain is needed.

## Run

```sh
cp .env.example .env   # optional; defaults work without it
docker compose up
```

- `http://localhost:8080/healthz`: liveness
- `http://localhost:8080/readyz`: database reachable (503 otherwise)
- `http://localhost:8080/metrics`: Prometheus metrics

`LLM_MODE=mock` is the default. With `LLM_MODE=real`, `ANTHROPIC_API_KEY` must be set in `.env`, otherwise the api exits on startup.

## Test

```sh
make test         # in Docker: throwaway Postgres + Go tests
make test-local   # needs local Go and DATABASE_URL pointing to a Postgres
```

Integration tests fail, never skip, when no database is available.

## Architecture (current)

One Go module (`cmd/scc`) built into one image and run as separate Compose services (D20): `postgres`, `migrate` (one-shot), `api`. `internal/config` reads environment variables; `internal/obs` serves health and metrics.
