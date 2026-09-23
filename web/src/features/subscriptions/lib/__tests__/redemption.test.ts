import { describe, expect, it } from 'vitest'

import { subscriptionPlanSchema } from '../../types'
import { getSubscriptionCardKind } from '../redemption'

function v1Plan(overrides: Record<string, unknown> = {}) {
  return subscriptionPlanSchema.parse({
    id: 1,
    title: 'DS day',
    price_amount: 4.9,
    currency: 'CNY',
    billing_policy: 'ds-flash-v1',
    service_channel_id: 24,
    service_model: 'deepseek-v4.1-flash',
    daily_input_token_limit: 50_000_000,
    daily_output_token_limit: 10_000_000,
    duration_unit: 'day',
    duration_value: 1,
    quota_reset_period: 'daily',
    enabled: true,
    sort_order: 0,
    allow_wallet_overflow: false,
    max_purchase_per_user: 0,
    total_amount: 1000,
    ...overrides,
  })
}

describe('subscription card plan binding', () => {
  it.each([
    [1, 'day'],
    [7, 'week'],
    [30, 'month'],
  ] as const)('maps %i days to the %s card kind', (days, kind) => {
    expect(getSubscriptionCardKind(v1Plan({ duration_value: days }))).toBe(
      kind
    )
  })

  it.each([
    { billing_policy: '' },
    { service_channel_id: 25 },
    { service_model: 'other-model' },
    { duration_value: 14 },
    { quota_reset_period: 'never' },
    { allow_wallet_overflow: true },
    { enabled: false },
  ])('rejects incompatible plans: %o', (overrides) => {
    expect(getSubscriptionCardKind(v1Plan(overrides))).toBeNull()
  })
})
