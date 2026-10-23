.PHONY: test test-local lint e2e

# Runs all tests in Docker (Go, then Vitest); only Docker and make are needed (D27).
# The test database is removed afterwards; the app's pgdata volume is never touched.
test:
	docker compose --profile test run --rm go-test; status=$$?; \
	docker compose --profile test rm --stop --force test-db; \
	docker compose --profile test run --rm web-test || status=1; \
	exit $$status

# Development: needs Go and a Postgres reachable via DATABASE_URL.
test-local:
	go test -count=1 ./...

# Format and vet checks in Docker; fails if any file needs gofmt. The
# frontend's TypeScript type check runs as part of web-test (make test).
lint:
	docker compose --profile test run --rm --no-deps go-test \
		sh -c 'files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi; go vet ./...'

# Opt-in browser tests (Playwright) against the app from `docker compose up`.
# They create campaigns and clean up after themselves (drafts deleted,
# approved ones cancelled); one small campaign is sent to the mock SMS provider.
e2e:
	docker compose --profile e2e run --rm e2e
