.PHONY: test test-local lint

# Runs all tests in Docker; only Docker and make are needed (D27).
# The test database is removed afterwards; the app's pgdata volume is never touched.
test:
	docker compose --profile test run --rm go-test; status=$$?; \
	docker compose --profile test rm --stop --force test-db; \
	exit $$status

# Development: needs Go and a Postgres reachable via DATABASE_URL.
test-local:
	go test -count=1 ./...

# Format and vet checks in Docker; fails if any file needs gofmt.
lint:
	docker compose --profile test run --rm --no-deps go-test \
		sh -c 'files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi; go vet ./...'
