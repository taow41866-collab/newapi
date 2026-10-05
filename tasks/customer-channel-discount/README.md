# Customer Channel Discount

## Scope

Implement effective-dated wallet discounts keyed by user, actual selected channel, and exact model or channel-default `*`. Support a bounded one-to-many rule list, including five or more channels, without changing user Group, requiring extra accounts/tokens, changing purchase costs, or granting routing/model access.

Exact model rules override the channel default; only one discount applies after the existing pricing engine. `0.70` means 70% of the normal wallet price. No matching rule means `1`. User IDs and ratios mentioned in discussion are examples, not production configuration.

Subscription billing stays unchanged. Preserve pre-consume/settlement/refund consistency, retry channel changes without duplicate reservations, and immutable async billing snapshots. Revenue uses the actual sales/refund ledger; costs remain independent and missing costs remain unknown.

## Delivery Boundary

This is a separate change from the channel-cost/dashboard release at `fe6b323a4b32022b40fd4a41a1275275a11d9741`. Do not include unverified billing changes in that immutable release. Preserve other working-tree changes. Production changes require reviewed tests, real SQLite/MySQL/PostgreSQL validation, green CI, immutable image, verified backup and rollback.

## Acceptance

- [x] Trace actual pricing, reservation, retry, settlement/refund, subscription and async task snapshot contracts.
- [x] Add effective-dated, auditable rules with bounded validation and privileged management APIs.
- [x] Add repeatable rule editing within existing user management UI.
- [x] Verify five channel discounts for one customer, model override without stacking, no-rule equivalence and customer/access isolation.
- [x] Verify nonzero token/cache/expression/image/second-video billing, refund/failure, retry channel switch, async snapshot and effective-time boundaries.
- [ ] Verify independent replicas, three real databases, review and CI before separate publication.

## Current State

Implementation is in the uncommitted working tree based on ce5a85f2aeaeffd4294a83a2428eb029fe28c389. This separate discount task has not shipped and is not included in production fe6b323a4b32022b40fd4a41a1275275a11d9741. No production discount configuration has been written.

Local results observed on 2026-10-05:

- Root-only GET/PUT uses expected_version compare-and-swap and append-only bounded history. Generic Options cannot expose or overwrite the rules.
- Wallet charge uses the actual chosen channel and exact billing model. Request history is frozen at its first read; retries use that same history on the actual next channel. Subscription funding, routing permissions and purchase cost rules remain separate.
- Async task billing freezes the discount. Legacy Midjourney persists the final discounted quota, token and billing channel; refund uses that amount, never rereads rules. Detailed rule snapshot is in admin-only consume logs, not a new Midjourney schema.
- Linux root full regression: exit 0, build/customer-discount/linux-regression-final.log. Frontend full run: 182 files / 2215 tests, typecheck and build exit 0.
- Real SQLite 3.50.4, MySQL 8.0.46 and PostgreSQL 16.15 CAS/process-read/snapshot migration matrix passed; fresh and upgrade migration twice retained legacy data. MySQL/PG immediate task settlement passed. This matrix predates the final Midjourney changes; final affected accounting matrix remains a gate.
- Desktop browser local isolated API: one user, five disabled test channels, six rules; save/reopen/history and stale draft 409 explicit refresh passed. Browser console had no errors/warnings. No real upstream called.
- Task submission regression now preserves nonzero upward reservation intent; sufficient existing reservations settle without redundant Reserve. Windows SQLite test connections are closed. Isolated PAT audit fixture failure remains under investigation.

The final affected SQLite/MySQL/PostgreSQL accounting matrix and targeted canonical-model regressions passed after the latest repair. The scoped reviewer report is being refreshed against the current diff before release; do not infer that the earlier report approved the fix. Latest targeted canonical test command: `go test ./controller -run '^TestCustomerChannelDiscountCanonical' -count=1` (exit 0). Still pending: independent scoped re-review, final whole-branch review, selective Git commit/CI/image/restore-verified canary/publication, and exact old-image cleanup after successful release. Review and handoff ledger: .superpowers/sdd/README/progress.md. Preserve unrelated dirty edits; review separately before inclusion.

## Initial Source Findings

Read-only investigation on local HEAD ce5a85f2aeaeffd4294a83a2428eb029fe28c389 (documentation-only descendant of the published source). These are entry points and design constraints, not proof of implemented discount behavior or a complete billing audit.

- `controller/relay.go:141` calls `relay.PrepareRequestBilling` before the normal retry loop. `relay/request_billing.go:26` handles subscription V1 before wallet reservation; retries retain the billing session. Actual-channel discounts require a selected-attempt refresh without creating a second reservation.
- `service/billing_session.go:379` chooses wallet/subscription funding and implements configured fallback. Resolve wallet discounts within the chosen funding path, not before this decision; subscription amounts must remain unchanged.
- `service/billing.go:51` settles the actual amount against the held reservation. Discounting only here would leave consume logs and user/channel totals inconsistent; all must use the same final discounted charge.
- `service/text_quota.go:355` applies existing request multipliers before adding tool surcharges. A customer discount inserted into `OtherRatios` would not cover the complete normal price. Expression, audio, image and task paths have separate calculations and also need explicit final-price coverage.
- `relay/relay_task.go:199` rebuilds task pricing on each attempt; `recalcQuotaFromRatios` can replace request multipliers after submission. Do not rely on an arbitrary multiplier map entry surviving plugin adjustments.
- `controller/relay.go:629` persists `TaskBillingContext` before task settlement. `model/task.go:164` currently has price/group/model/request/expression fields but no customer-rule snapshot. Freeze actual channel, resolved discount, version and time alongside existing immutable task context.
- `service/task_billing.go:261` and `:309` handle refund and asynchronous adjustment; `:381` recalculates legacy token tasks using current ratio configuration. The discount must use the submission snapshot, including after rules are replaced, and cannot grant channel/model access.

The initial findings above are historical design inputs, not current implementation status. Current status and remaining gates are recorded above; no production discount rules have been created.
