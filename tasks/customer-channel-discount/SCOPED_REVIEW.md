# Customer Channel Discount Scoped Re-review

Date: 2026-10-05
Scope: read-only review of the repair round in `service/tiered_settle.go`,
`model/customer_channel_discount.go`, and the related controller/service tests.
The review also compared `.superpowers/sdd/README/task-2-fix.diff` and the
previous independent findings in `task-2-review.md`. No source was modified,
committed, pushed, or deployed by this review.

## Verdict

**DONE_WITH_CONCERNS. Do not yet enter the final three-database acceptance
matrix.** The expression-error repair appears to address the prior reservation
regression, and deleted-channel tombstones are now accepted. However, the
canonical billing-model fix accepts any string beginning with a configured
channel model followed by `@`; it does not prove that the suffix is a valid,
configured canonical billing identity. Also, there is no regression test that
configures and resolves a canonical exact discount rule. This leaves the prior
canonical-identity finding only partially verified and permits arbitrary exact
rule keys that will never match a billing model.

## Findings

### P2 - Canonical exact model validation accepts arbitrary suffixes

Location: `model/customer_channel_discount.go:176-184`.

The new check uses `strings.HasPrefix(rule.Model, base+"@")`. Thus a channel
listing `qwen3-max` will accept values such as `qwen3-max@made-up:anything` or
`qwen3-max@../../unknown`, without checking whether the suffix is one of the
canonical billing identities derived by the existing reasoning/billing
helpers, or whether that identity has a price configuration. These rules are
not routing permission grants, but they create accepted configuration that
cannot match `RelayInfo.GetBillingModelName()` and is silently inert. The
regression scenario from the original finding (base-only channel plus a real
canonical modifier exact rule, default fallback, and one-time exact application)
is absent from the current tests.

Required before release: validate against the existing canonical identity
generation/pricing relationship, not a raw prefix; reject arbitrary suffixes.
Add a controller/model persistence test that accepts an actual configured
canonical identity, rejects a fabricated suffix, and proves exact rule wins
over `*` in the runtime resolver. Then run the focused test.

## Reviewed Fixes

- `TryTieredSettle` now returns the held `Billing.GetPreConsumedQuota()` after
  expression evaluation failure, falling back to `FinalPreConsumedQuota` only
  when there is no positive active hold. This aligns the failure result with
  the retained reservation and avoids applying a lower retry's discount to a
  normal-price estimate. The added failure test covers a channel retry from a
  0.5 to 0.8 discount and retains the 500 hold; the targeted test passed.
- Deleted-channel disabled tombstones now tolerate only `gorm.ErrRecordNotFound`
  when the rule is disabled, while active rules and other database errors still
  fail. The controller history test exercises a disabled tombstone for missing
  channel 99 and passed. It does not yet exercise a mixed batch with both a
  deleted-channel tombstone and a valid active update.
- Existing test suite already covers exact-vs-default resolution for ordinary
  model names and disabled exact fallback, but not a canonical modifier identity.

## Verification

Executed from the current working tree:

```text
go test ./controller ./service -run 'TestCustomerChannelDiscount|TestTieredSettlementFailureKeepsFrozenDiscountedReservation|TestTryTieredSettleFallsBackToFrozenPreConsumeOnExprError' -count=1
PASS: controller and service packages
```

No three-database matrix was run as part of this scoped review. The currently
recorded matrix predates the latest Midjourney changes and is not final evidence.
No paid upstream requests or production operations were performed.

## Gate

Once canonical validation and its discriminating regression are added, this
scoped review needs a short follow-up review of that changed code. After that,
the final SQLite/MySQL/PostgreSQL accounting matrix may proceed; this review
does not approve the broader feature, unrelated working-tree changes, CI, or
release.
