.PHONY: test test-local

# Runs all tests in Docker; only Docker and make are needed (D27).
# The test database is removed afterwards; the app's pgdata volume is never touched.
test:
	docker compose --profile test run --rm go-test; status=$$?; \
	docker compose --profile test rm --stop --force test-db; \
	exit $$status

# Development: needs Go and a Postgres reachable via DATABASE_URL.
test-local:
	go test -count=1 ./...
