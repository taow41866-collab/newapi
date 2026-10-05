# Channel Cost Configuration and Dashboard Summary

## Objective

Move upstream cost multiplier entry from the revenue report into each channel's configuration, preserve effective-time history and exact-price precedence, and show administrator revenue/cost/profit/margin in the model data statistics bar.

## Decisions

- Reuse the existing `RevenuePurchasePrices` rules and endpoints as the persisted historical ledger; do not add a schema migration or affect user billing.
- Support a channel default rule using model `*`, with exact model rules taking precedence. Exact token purchase prices support input/output plus optional cache read/write rates; exact prices continue to take precedence over multiplier estimates.
- Channel edits append a new effective-time rule instead of rewriting historical rules. Do not expose deletion that could retroactively change reports.
- Keep the detailed revenue page for reporting, but remove its purchase-rule editor. Keep sensitive cost configuration Root-only; revenue statistics remain visible to administrators.
- Show the four monetary metrics only when the selected period is within the revenue API's 31-day limit. Incomplete costs must remain visibly incomplete.

## Tasks and Verification

1. [x] Add regression tests for wildcard fallback, exact-model override, and exact purchase-price precedence. The atomic append test was first run as RED (missing method), then passed after implementation.
2. [x] Add a Root-only per-channel cost editor for multiplier and exact token/request/image/second prices. New UI uses a server-side append endpoint; legacy full-list PUT is restricted to append-only and cannot rewrite/delete history.
3. [x] Make the revenue detail page read-only and show admin-only net sales, known cost, estimated gross profit, and gross margin directly in the model data bar. Incomplete cost, API failure, and >31-day ranges remain explicit; username filtering follows the dashboard selection.
4. [ ] Complete current code review and the final affected test/typecheck/lint/build/diff checks. Local full controller tests hit a Windows SQLite temp-file cleanup failure in the unrelated access-token audit matrix; the Linux CI result is still required.
5. [ ] Push the reviewed feature commit, require passing CI, build an immutable image, deploy with verified backup/rollback, and verify the production dashboard.
6. [ ] Only after successful publication, retain one image version per production service and remove confirmed obsolete images. Remove only remote Git branches proven merged, inactive, and unprotected; preserve current/deployment branches and unrelated local work.

## Release Gate

Do not publish unless the built image revision matches the reviewed commit, CI is green, production state and backup/rollback path are verified, and the new dashboard/cost configuration can be checked without exposing cost rules to non-Root users. Destructive image and remote-branch cleanup is the final step and must be based on a fresh inventory after publication.
