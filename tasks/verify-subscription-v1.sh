#!/usr/bin/env bash
# Run only in a disposable checkout and test environment, never on production data.
set -euo pipefail
cd "$(dirname "$0")/.."

if [[ "${SUBSCRIPTION_V1_ISOLATED_TEST:-}" != "1" ]]; then
  echo 'Refusing to run: set SUBSCRIPTION_V1_ISOLATED_TEST=1 in a disposable test environment.' >&2
  exit 2
fi
command -v go >/dev/null || { echo 'Go is required; this script does not install it.' >&2; exit 2; }
command -v make >/dev/null || { echo 'make is required.' >&2; exit 2; }
if [[ -n "${SQL_DSN:-}" || -n "${LOG_SQL_DSN:-}" || -n "${REDIS_CONN_STRING:-}" ]]; then
  echo 'Refusing inherited application database/cache configuration. Use a clean test environment.' >&2
  exit 2
fi
export GOWORK=off
export GOTOOLCHAIN=local
echo 'Go version:'
go version
files=(model/redemption.go model/redemption_test.go model/subscription.go)
[[ ! -f model/subscription_v1_test.go ]] || files+=(model/subscription_v1_test.go)
[[ ! -f model/subscription_v1_usage.go ]] || files+=(model/subscription_v1_usage.go)
files+=(model/main.go)
unformatted=$(gofmt -l "${files[@]}")
if [[ -n "$unformatted" ]]; then
  echo 'Format these files before verification:' >&2
  printf '%s\n' "$unformatted" >&2
  exit 1
fi
git diff --check
go test ./model -run 'Test(Subscription|Redeem|Redemption)' -count=1
go test -race ./model -run 'Test(Subscription|Redeem|Redemption)' -count=1
go vet ./model
make test
echo 'Model regression and package suite completed. This is NOT three-database or end-to-end acceptance.'
