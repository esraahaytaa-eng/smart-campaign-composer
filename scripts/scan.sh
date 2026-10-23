#!/bin/sh
# Scans for API keys, credentials, secrets and personal data (D51) with
# gitleaks in Docker, using the repository's rules in .gitleaks.toml.
#
#   scripts/scan.sh tree      files Git tracks or would add (respects .gitignore)
#   scripts/scan.sh staged    staged changes (the pre-commit hook)
#   scripts/scan.sh unpushed  commits on no remote branch yet (before a push)
#   scripts/scan.sh history   every commit (report only; see D51)
#   scripts/scan.sh selftest  planted secrets must be caught, allowed data must not
set -eu

IMAGE=zricethezav/gitleaks:v8.24.3
ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"

# gitleaks runs as root in the container; git refuses a repository owned by
# someone else unless it is marked safe.
gitleaks() {
	docker run --rm -i -v "$ROOT":/repo:ro -w /repo --entrypoint sh "$IMAGE" -c \
		'git config --global --add safe.directory "*" && gitleaks "$@"' gitleaks "$@"
}
OPTS="--config /repo/.gitleaks.toml --no-banner --redact --verbose --exit-code 1" # findings shown, values redacted

# scan_stream scans the files of a tar stream from stdin.
scan_stream() {
	docker run --rm -i -v "$ROOT/.gitleaks.toml":/config.toml:ro --entrypoint sh "$IMAGE" -c \
		'mkdir /scan && tar -xf - -C /scan && gitleaks dir /scan --config /config.toml --no-banner --redact --verbose --exit-code 1'
}

case "${1:-}" in
tree)
	# Only what Git would see: a local .env or node_modules is never scanned.
	git ls-files -z --cached --others --exclude-standard |
		xargs -0 -I{} sh -c 'test -f "$1" && printf "%s\0" "$1"' _ {} |
		tar --null -T - -cf - | scan_stream
	;;
staged)
	# The staged contents are copied on this machine and streamed in, so the
	# container never reads .git through Docker's file sharing, which can lag
	# behind a "git add" made a moment before.
	TMP=$(mktemp -d)
	trap 'rm -rf "$TMP"' EXIT
	git diff --cached --name-only --diff-filter=ACMR -z | xargs -0 -r git checkout-index --prefix="$TMP/" --
	(cd "$TMP" && find . -type f -print0 | tar --null -T - -cf -) | scan_stream
	;;
unpushed)
	if [ -z "$(git rev-list HEAD --not --remotes)" ]; then
		echo "no unpushed commits"
		exit 0
	fi
	# shellcheck disable=SC2086
	gitleaks git /repo --log-opts="HEAD --not --remotes" $OPTS
	;;
history)
	# shellcheck disable=SC2086
	gitleaks git /repo $OPTS
	;;
selftest)
	exec "$ROOT/scripts/scan-selftest.sh"
	;;
*)
	echo "usage: scripts/scan.sh tree|staged|unpushed|history|selftest" >&2
	exit 2
	;;
esac
