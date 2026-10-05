# Customer Discount Independent Local Checkpoint

Date: 2026-10-05. Working tree based on ce5a85f2a, with concurrent local implementation. This is a bounded checkpoint, not a frozen release approval.

## Added Accounting Coverage

`controller/customer_channel_discount_test.go`: `TestCustomerChannelDiscountRetryWalletAccounting` uses real SQLite user and token balances, the retained billing session, selected-channel refresh, `PostTextConsumeQuota`, and consume logs.

For a normal charge of 1000 quota:

- Channel multiplier 0.7, retry to 0.5: final wallet/token debit and log quota are 500.
- Channel multiplier 0.5, retry to 0.8: final debit and log quota are 800.
- Channel multiplier 0.5, retry to an unconfigured channel: final debit and log quota are 1000.
- Each case retains one billing session; repeated settlement does not add another charge.

## Observed Commands

- `go test ./controller -run '^TestCustomerChannelDiscount' -count=1`: passed locally, including the SQLite separate-process rule test. MySQL/PostgreSQL cases skipped without their DSNs in this shell.
- `go test ./service -run 'CustomerDiscount|Billing|Quota|Tiered' -count=1`: passed locally.
- `go test ./relay/... -run 'Billing|Task|Channel' -count=1`: passed locally.
- `go vet ./controller ./service ./relay`: passed at its checkpoint.
- `bun run test src/features/users/components/__tests__/customer-channel-discounts.test.tsx src/features/users/lib/__tests__/customer-discounts.test.ts`: latest run at 19:15 local time passed all 18 tests, including the main thread's additional editor cases.
- `bun run typecheck`: initially failed on an unused import and unsupported `toReversed`; passed after the shared implementation corrected them.
- Whole-frontend lint: failed, including unrelated existing errors. Scoped lint initially found an array-index key and later missing braces in the new history list/editor. After correction, `bunx --no-install oxlint -c .oxlintrc.json` restricted to the seven affected user discount API/types/editor/test files passed (exit 0).

## Handoff Boundaries

The main thread owns the management UI/API, billing implementation, complete database matrix, browser verification, and final review. Findings about SQLite retry closure state, immutable request rule history, stale editor cache, and TypeScript compatibility were sent there for resolution.

Do not infer release readiness from these focused tests. No production rules, user groups, channel costs, routing weights, Git push, or deployment were changed by this checkpoint.

## Frontend Handoff (2026-10-05)

The Root-only user-management editor is implemented in the shared working tree:

- `web/src/features/users/api.ts` and `types.ts` define the GET/PUT contract.
- `web/src/features/users/components/customer-channel-discount-dialog.tsx` adds the Root-only editor, current rules, historical append-only rows, disabled tombstones, explicit 409 refresh, loading/error states, and 100-change/1000-history guards.
- `web/src/features/users/lib/customer-discounts.ts` contains schema and pure change/deduplication logic.
- The existing user edit drawer opens the editor; locale keys were appended to all seven locale files.

Fresh verification in this checkout:

- `bun run test src/features/users`: exit 0, 6 files and 42 tests passed.
- `bun run typecheck`: exit 0.
- `bunx oxfmt --write` on the seven affected source/test files: exit 0.
- `bunx oxlint -c .oxlintrc.json` restricted to the affected user discount/API/type/editor/test files: exit 0.
- `bun run build`: exit 0 (`rsbuild` production build; generated `web/dist` is not a release artifact here).

Not verified here: backend/API integration against a running server, Root browser session against production, real channel/model validation, billing/settlement, database matrix, Git push/CI, image build, deployment, or production smoke/rollback. The parent task must perform independent review before release.

## Independent Review Follow-up (2026-10-05)

- Fixed tiered-expression evaluation failure handling in `service/tiered_settle.go`: when a request already has a wallet reservation, the failed evaluation now settles against that retained reservation rather than the normal-price estimate. This prevents a cheaper retry from releasing frozen quota or applying a customer discount twice.
- Fixed `model.AppendCustomerChannelDiscounts`: disabled tombstones may target a removed channel so historical rules can be disabled; active rules still require an existing channel and configured exact model.
- Added deterministic regressions in the existing `service/task_billing_test.go` and `controller/customer_channel_discount_test.go`.
- Verified: `go test ./service -run 'TestTieredSettlementFailureKeepsFrozenDiscountedReservation|TestCustomerDiscountMidjourney|TestCustomerDiscountRetryReservesDifference' -count=1` passed; `go test ./controller -run '^TestCustomerChannelDiscountHistoryAndIsolation$' -count=1` passed; `go vet ./service ./model ./controller` passed; `git diff --check` passed.
- This is local source/test evidence only. No commit, push, CI, image build, deployment, production rules, or paid upstream request was performed.
