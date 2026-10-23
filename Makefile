.PHONY: test test-local lint e2e scan scan-staged scan-selftest hooks

# Runs all tests in Docker, Go and the frontend at the same time (the frontend's
# output is shown after Go's); only Docker and make are needed (D27).
# The test database is removed afterwards; the app's pgdata volume is never touched.
test:
	@web=$$(mktemp); \
	docker compose --profile test run --rm web-test > $$web 2>&1 & pid=$$!; \
	docker compose --profile test run --rm go-test; status=$$?; \
	docker compose --profile test rm --stop --force test-db; \
	wait $$pid || status=1; \
	echo "--- frontend (ran alongside the Go tests)"; cat $$web; rm -f $$web; \
	exit $$status

# Development: needs Go and a Postgres reachable via DATABASE_URL.
test-local:
	go test -count=1 ./...

# Format and vet checks in Docker on the module's own packages (not caches such
# as CI's .ci-cache); fails if any file needs gofmt. The
# frontend's TypeScript type check runs as part of web-test (make test).
lint:
	docker compose --profile test run --rm --no-deps go-test \
		sh -c 'files=$$(gofmt -l $$(go list -f "{{.Dir}}" ./...)); if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi; go vet ./...'

# Opt-in browser tests (Playwright) on their own stack (D53): a separate
# Compose project with a fresh database, started here and removed afterwards,
# so the local app and its data are never touched. Some checks really send
# campaigns, to the mock providers only.
E2E = docker compose -p scc-e2e -f docker-compose.yml -f docker-compose.e2e.yml
e2e:
	$(E2E) up -d --build --wait api worker mock-provider
	$(E2E) --profile e2e run --rm e2e; status=$$?; \
	$(E2E) down --remove-orphans; \
	exit $$status

# Secret and personal-data scanning (D51), gitleaks in Docker with the rules
# in .gitleaks.toml: the files Git sees plus the commits not pushed yet.
# Run before every commit and push; CI runs it too.
scan:
	scripts/scan.sh tree
	scripts/scan.sh unpushed

# What the pre-commit hook runs: the staged changes only.
scan-staged:
	scripts/scan.sh staged

# Proves the rules: planted keys, emails, phone numbers and a .env file are
# caught, and the project's fictional data is not.
scan-selftest:
	scripts/scan.sh selftest

# Installs the repository's git hooks (pre-commit scan) for this clone.
hooks:
	git config core.hooksPath .githooks
	@echo "pre-commit scan enabled"
