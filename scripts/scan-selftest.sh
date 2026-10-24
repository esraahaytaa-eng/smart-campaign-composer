#!/bin/sh
# Self-test for the scanner configuration (D51): in a throwaway repository,
# every planted secret or piece of personal data must be found, and the
# project's allowed fictional data must not be. Run: make scan-selftest
set -eu

IMAGE=zricethezav/gitleaks:v8.24.3
ROOT=$(git rev-parse --show-toplevel)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Random values made at run time, with the entropy of real keys (gitleaks
# ignores low-entropy strings); this file itself holds no secret.
rand() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c "$1"; }
key="sk-ant-api03-$(rand 93)AA"
github="ghp_$(rand 36)"

expect() { # case rule file content: the case must be reported by that rule
	mkdir -p "$WORK/$1/repo"
	printf '%s\n' "$4" >"$WORK/$1/repo/$3"
	echo "$2" >"$WORK/$1/rule"
}
expect key1 anthropic-api-key anthropic.txt "ANTHROPIC_API_KEY=$key"
expect key2 github-pat token.txt "export GITHUB_TOKEN=$github"
# Real-looking contacts are put together here, so this file holds none.
at=@
expect mail1 email-address notes.md "Contact the customer at ayse.yilmaz${at}gmail.com tomorrow."
expect mail2 email-address users.csv "id,email
7,mehmet.kaya${at}hotmail.com"
expect phone1 phone-number lead.txt "Call +90 532 $(echo 123) 45 67 before noon."
expect phone2 phone-number lead2.txt "telefon: 0532 $(echo 987) 65 43"
expect phone3 phone-number lead3.json "{\"phone\":\"+49151$(echo 12345678)\"}"
expect env dotenv-file .env "LOG_LEVEL=debug"
expect env2 dotenv-file .env.production.local "LOG_LEVEL=debug"
expect env3 dotenv-file prod.env "LOG_LEVEL=debug"
expect env4 dotenv-file .envrc "export LOG_LEVEL=debug"
# Only the UI's own placeholders at these real domains are allowed.
expect mail3 email-address crm.txt "Owner: mehmet.kaya${at}company.com"
# A Go checksum on the same line must not hide a key next to it.
expect key3 anthropic-api-key notes.txt "example.com/mod v1.0.0 h1:uiha35bPI8zGxUpS+qJ7Ux/2hD/2k5Qo3G5L2hUQFgQ= ANTHROPIC_API_KEY=$key"

# Allowed: the project's fictional seed and test data, documented demo login,
# placeholders and masked contacts. None of these may be reported.
mkdir -p "$WORK/allowed/repo/internal/db" "$WORK/allowed/repo/docs"
cat >"$WORK/allowed/repo/docs/allowed.md" <<'EOF'
Sign in as marketer@marketer.com (local demo only). Fixtures use a@example.com,
ayse.demir.17@example.com and the UI's placeholders you@company.com, name@company.com, siz@sirket.com, ad@sirket.com.
Masked contacts look like +90 555 ••• •• 17 and ays•••@example.com; sign-in is case-insensitive (Marketer@Example.COM).
A non-Turkish test number from the range reserved for fiction: +447700900078.
Commits end with Co-Authored-By: Claude <noreply@anthropic.com>.
go.sum: cloud.google.com/go/auth v0.7.2 h1:uiha35bPI8zGxUpS+qJ7Ux/2hD/2k5Qo3G5L2hUQFgQ=
EOF
printf 'phone := fmt.Sprintf("+90555%%07d", id)\n' >"$WORK/allowed/repo/internal/db/generate.go"
cp "$ROOT/.env.example" "$WORK/allowed/repo/.env.example"

fail=0
for case in "$WORK"/*/; do
	name=$(basename "$case")
	(cd "$case/repo" && git init -q && git add -A && git -c user.email=t@example.com -c user.name=t commit -qm test)
	rules=$(docker run --rm -v "$case/repo":/repo:ro -v "$ROOT/.gitleaks.toml":/config.toml:ro --entrypoint sh "$IMAGE" -c \
		'git config --global --add safe.directory "*" && gitleaks git /repo --config /config.toml --no-banner --redact --exit-code 0 \
			--report-format json --report-path /tmp/r.json >/dev/null 2>&1; cat /tmp/r.json' |
		grep -o '"RuleID": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/' | sort -u | tr '\n' ' ')
	if [ "$name" = allowed ]; then
		if [ -z "$rules" ]; then echo "PASS allowed: nothing reported"; else echo "FAIL allowed: reported by $rules"; fail=1; fi
		continue
	fi
	want=$(cat "$case/rule")
	case " $rules" in
	*" $want "*) echo "PASS $name: caught by $want" ;;
	*) echo "FAIL $name: expected $want, got ${rules:-nothing}"; fail=1 ;;
	esac
done
exit $fail
