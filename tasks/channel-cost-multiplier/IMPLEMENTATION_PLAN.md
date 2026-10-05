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
4. [x] Complete code review and affected tests/typecheck/lint/build/diff checks. Fix and test the generic Option overwrite bypass and first-append transaction race. SQLite 3.50.4, MySQL 8.0.46 and PostgreSQL 16.15 independent-process tests passed; final Linux CI 37289529950 passed on fe6b323a4b32022b40fd4a41a1275275a11d9741. Earlier Windows SQLite cleanup and initial Linux concurrency failures are retained in README, not treated as passing evidence.
5. [x] Push the reviewed commit, require passing CI, build immutable digest 1377de554212e69be2611d64690893d980ceecf27515bb8102f84eb60cff2e7d, verify backup restoration and isolated permissions, switch master/slave at 2026-10-05 09:36:22 UTC, and verify production resources/dashboard. Keep the rollback script in the persistent release directory.
6. [x] Fresh inventory confirmed three unreferenced candidate images; delete them without force. Keep current, rollback and all container-referenced images. No merged, unprotected and demonstrably unused branch was identified; delete none. Preserve unrelated worktrees and local changes.
7. [ ] Complete first-hour observation. The 09:50:29 UTC read-only checkpoint passed; no production price rules or paid requests were created. Do not claim a full-hour latency/error-rate or paid video end-to-end result from this checkpoint.

## Release Gate

Do not publish unless the built image revision matches the reviewed commit, CI is green, production state and backup/rollback path are verified, and the new dashboard/cost configuration can be checked without exposing cost rules to non-Root users. Destructive image and remote-branch cleanup is the final step and must be based on a fresh inventory after publication.
