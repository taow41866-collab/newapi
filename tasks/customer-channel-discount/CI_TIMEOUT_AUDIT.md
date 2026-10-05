# CI Timeout Audit: Run 37334014676

## Scope

- Target commit: `58a666da01cb0d190cff84c73a5264f9419bd914`
- Workflow run: [CI #37334014676](https://github.com/taow41866-collab/newapi/actions/runs/37334014676)
- Read-only investigation. No source edits, CI rerun, or push.

## Confirmed CI Evidence

GitHub Actions run and job metadata were queried from the public API on 2026-10-06.

- Run conclusion: `failure`; head SHA matches the target commit.
- Backend job `Backend vet, build, and test`: failed.
- Frontend job `Frontend subscription lint, typecheck, test, and build`: succeeded.
- Backend steps through `Test transaction DB-time lookup on supported SQL services` succeeded.
- `Test root and relaykit modules` (`make test`) ran from `2026-10-05T15:37:17Z` to `15:47:26Z`, about 10m09s, then failed.
- The subsequent shared-email, authorization-seeding, and real-Redis routing steps were skipped.
- Check-run annotations contain only `Process completed with exit code 2`; they do not name a package or test.
- Downloading the run log endpoint returned HTTP 403. Thus the exact CI package/test and terminal output could not be observed from the available unauthenticated API evidence.

The timing is consistent with a Go test binary reaching Go's default 10-minute timeout because the CI `make test` invocation supplies no `-timeout`. This is an inference, not a confirmed diagnosis: the run metadata does not expose the test output or timed-out package.

## Local Package Evidence

Exact local command run on the current checkout:

```powershell
$env:GOWORK='off'; go test ./controller -count=1 -timeout=10m
```

Result: exit 0; `ok github.com/QuantumNous/new-api/controller 146.474s`.

Environment was Windows/amd64 with Go 1.27.1. This confirms the current controller package passes locally, but does not reproduce CI's Ubuntu/amd64 Go 1.25.1 environment or establish that the CI controller test completed.

Other available artifacts are not equivalent to this run:

- `build/customer-discount/linux-regression-final.log` records a previous Linux root-module regression in which `controller` passed in 194.181s and the listed root packages passed. Its provenance is earlier than CI #37334014676 and it is not a log from the target CI run.
- `build/customer-discount/relaykit-final.log` records a previous relaykit test run passing. It is not from target CI.
- `build/customer-discount/make-test-58a666-wsl.log` says `timeout: failed to run command ‘make’: No such file or directory`; this was an environment/tooling failure, not a test result.
- WSL's locally installed Go is 1.22.2, below the module's Go 1.25.1 requirement. Auto toolchain download did not complete during the check, so no exact-source Linux package rerun was obtained here.

## Conclusion

Status: `DONE_WITH_CONCERNS`.

Confirmed: the CI failure is confined to the `make test` workflow step; controller passes in the available Windows environment. Unknown: the exact package/test that caused CI exit 2. The evidence is insufficient to name a stuck test or call this a source-level timeout. Obtain the authenticated GitHub Actions job log for job `111844053343` (or another run's full log) before changing tests or attributing the failure. Do not treat the previous Linux regression artifacts as proof for this CI run.

## Authenticated Log Follow-up: 2026-10-06

The job log was subsequently read through the configured Git credential helper without printing credentials. This supersedes the earlier conclusion that only run metadata was available:

- `make test` hit `panic: test timed out after 10m0s`; the backend job itself has a 15-minute timeout. The stack identifies `TestResponsesWebSocketDialsNativeResponsesChannelTypes/new_api` as the active test case, but the captured summary did not preserve a specific blocked source line.
- Additional reported failures included the customer-discounted expression fallback (expected 2,500, actual 1,250), estimated tiered fallback (expected 999, actual 0), Kling fake-billing reserve event count, and Responses stream interruption/reuse tests.
- WSL has Go 1.22.2 while this module requires Go 1.25.1; toolchain download timed out. Therefore WSL could not run `make test` equivalently.

After these findings, the working tree was repaired and locally verified:

- `go test ./service -count=1` and `go vet ./service` pass on Windows/amd64.
- `go test ./controller` for the Kling route and three Responses WebSocket cases passes; native channel dialing also passed five consecutive runs. These are Windows results, not Linux CI evidence.
- A Linux/amd64 service test binary built with the available Windows Go toolchain ran the fixed-price billing matrix against real WSL SQLite 3.50.4, MySQL 8.0.46, and PostgreSQL 16.15; the related task-settlement DB matrix passed and temporary test accounts were removed and verified.
- The expression-error regression now includes a tool surcharge: a net held quota of 2,500 plus a 2,000 normal-price tool charge at multiplier 0.5 settles to 3,500. The exact SQLite integration case passes.

Three-second bounded waits were added to the Responses test so a missing upstream observation fails promptly instead of consuming Go's default timeout. Local Windows runs do not prove the original Linux hang is resolved. A fresh GitHub CI run on the resulting commit remains authoritative; do not release until it is green.
