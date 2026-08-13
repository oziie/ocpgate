#!/usr/bin/env bash
#
# Writes a durable test report to reports/, so a run against fakes and a
# run against a real cluster can be compared side by side rather than
# living only in terminal scrollback.
#
# Usage:
#   scripts/test-report.sh
#   OCPGATE_TEST_TARGET=prod-cluster-1 scripts/test-report.sh
#   OCPGATE_RACE=0 scripts/test-report.sh          # skip the race detector
#
# The target is a label only — it records what the run exercised. Set it
# when tests are pointed at a real cluster so the report says so.

set -euo pipefail

cd "$(dirname "$0")/.."

REPORT_DIR="${OCPGATE_REPORT_DIR:-reports}"
TARGET="${OCPGATE_TEST_TARGET:-fakes}"
RACE="${OCPGATE_RACE:-1}"

# A target label may become part of a filename, so keep it tame.
SAFE_TARGET="$(printf '%s' "$TARGET" | tr -c 'A-Za-z0-9._-' '-')"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

mkdir -p "$REPORT_DIR"

LOG="$REPORT_DIR/test-log.txt"
COVER="$REPORT_DIR/coverage.out"
REPORT="$REPORT_DIR/test-report.md"
ARCHIVE="$REPORT_DIR/history/${STAMP}-${SAFE_TARGET}.md"

TEST_FLAGS=(-v -covermode=atomic -coverprofile="$COVER")
RACE_LABEL="disabled"
if [ "$RACE" = "1" ]; then
	TEST_FLAGS=(-race "${TEST_FLAGS[@]}")
	RACE_LABEL="enabled"
fi

echo "running tests (target: $TARGET, race: $RACE_LABEL)…"

# The exit status is the report's headline result, so capture it rather
# than letting set -e abort here.
set +e
go test "${TEST_FLAGS[@]}" ./... >"$LOG" 2>&1
TEST_STATUS=$?
set -e

if [ "$TEST_STATUS" -eq 0 ]; then
	RESULT="PASS"
else
	RESULT="FAIL"
fi

# Top-level tests sit at column 0; subtests are indented beneath them.
count_top() { grep -cE "^--- $1" "$LOG" || true; }
count_all() { grep -cE "^[[:space:]]*--- $1" "$LOG" || true; }

PASSED="$(count_top PASS)"
FAILED="$(count_top FAIL)"
SKIPPED="$(count_top SKIP)"
PASSED_ALL="$(count_all PASS)"
FAILED_ALL="$(count_all FAIL)"

TOTAL_COVERAGE="n/a"
if [ -s "$COVER" ]; then
	TOTAL_COVERAGE="$(go tool cover -func="$COVER" | awk '/^total:/ {print $3}')"
fi

RACE_HITS="$(grep -c 'WARNING: DATA RACE' "$LOG" || true)"

{
	echo "# ocpgate test report"
	echo
	echo "| | |"
	echo "|---|---|"
	echo "| Result | **$RESULT** |"
	echo "| Target | \`$TARGET\` |"
	echo "| Generated | $(date -u +%Y-%m-%dT%H:%M:%SZ) |"
	echo "| Commit | $(git rev-parse --short HEAD 2>/dev/null || echo unknown)$(git diff --quiet 2>/dev/null || echo ' (dirty)') |"
	echo "| Go | $(go version | awk '{print $3, $4}') |"
	echo "| Race detector | $RACE_LABEL |"
	echo "| Total coverage | $TOTAL_COVERAGE |"
	echo
	echo "Tests: **$PASSED passed**, **$FAILED failed**, $SKIPPED skipped"
	echo "(including subtests: $PASSED_ALL passed, $FAILED_ALL failed)"
	if [ "$RACE_HITS" -gt 0 ]; then
		echo
		echo "> **$RACE_HITS data race(s) detected** — see \`$LOG\`."
	fi
	echo

	echo "## Packages"
	echo
	echo "| Package | Result | Time | Coverage |"
	echo "|---|---|---|---|"
	# go test reports a package three different ways, and all three have to
	# be recognised or packages vanish from the report:
	#   ok    <pkg>  1.2s  coverage: 61.3% of statements
	#   ?     <pkg>  [no test files]
	#         <pkg>        coverage: 0.0% of statements   (no tests, but instrumented)
	awk '
		function coverage_of(  i) {
			for (i = 1; i <= NF; i++)
				if ($i == "coverage:") return $(i + 1)
			return "—"
		}

		/^(ok|FAIL)[ \t]+github/      { result = $1;         pkg = $2; time = $3;   cov = coverage_of() }
		/^\?[ \t]+github/             { result = "no tests"; pkg = $2; time = "—";  cov = "—" }
		/^[ \t]+github.*coverage:/    { result = "no tests"; pkg = $1; time = "—";  cov = coverage_of() }

		/github\.com\/oziie\/ocpgate/ {
			if (result == "") next
			gsub(/^github\.com\/oziie\/ocpgate\/?/, "", pkg)
			if (pkg == "") pkg = "."
			printf "| `%s` | %s | %s | %s |\n", pkg, result, time, cov
			result = ""
		}
	' "$LOG" | sort

	echo
	echo "## Least-covered functions"
	echo
	if [ -s "$COVER" ]; then
		echo '```'
		go tool cover -func="$COVER" |
			grep -v '^total:' |
			awk '{print $NF, $0}' |
			sort -n |
			head -15 |
			cut -d' ' -f2-
		echo '```'
	else
		echo "_No coverage profile produced._"
	fi

	if [ "$FAILED" -gt 0 ]; then
		echo
		echo "## Failures"
		echo
		echo '```'
		grep -E "^[[:space:]]*--- FAIL" "$LOG" | head -40
		echo '```'
	fi

	echo
	echo "## What this run proves"
	echo
	if [ "$TARGET" = "fakes" ]; then
		cat <<-'NOTE'
			This run exercised **fakes only**. The OCP OAuth server, the cluster
			API, and GitLab were all stubbed in-process, so it confirms ocpgate's
			own logic and nothing about a real cluster's behavior.

			Assumptions still unverified against real infrastructure:

			- `/.well-known/oauth-authorization-server` returns `authorization_endpoint`
			- `X-CSRF-Token: 1` is enough to get a Basic challenge, not the HTML login page
			- the token arrives in the redirect's URL **fragment** as `access_token` + `expires_in`
			- 401/403 from the authorize endpoint means bad credentials, not a disabled
			  account or an LDAP timeout
			- namespace listing is normally forbidden for ordinary users (the text-field
			  fallback), and `oc get projects` is not needed instead

			Re-run with `OCPGATE_TEST_TARGET=<cluster>` once pointed at real infrastructure.
		NOTE
	else
		echo "This run was labelled \`$TARGET\` — real infrastructure was in play."
		echo "Compare the package results against the most recent \`fakes\` run in"
		echo "\`$REPORT_DIR/history/\` to see what real behavior changed."
	fi

	echo
	echo "---"
	echo "_Full output: \`$LOG\`. Coverage profile: \`$COVER\`_"
	echo "_Regenerate with \`make test-report\`._"
} >"$REPORT"

mkdir -p "$REPORT_DIR/history"
cp "$REPORT" "$ARCHIVE"

echo
echo "$RESULT — $PASSED passed, $FAILED failed, coverage $TOTAL_COVERAGE"
echo "report:  $REPORT"
echo "archive: $ARCHIVE"

exit "$TEST_STATUS"
