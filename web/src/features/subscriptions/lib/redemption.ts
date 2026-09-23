import type { SubscriptionPlan } from '../types'

export type SubscriptionCardKind = 'day' | 'week' | 'month'

export function getSubscriptionCardKind(
  plan: SubscriptionPlan
): SubscriptionCardKind | null {
  if (
    !plan.enabled ||
    plan.billing_policy !== 'ds-flash-v1' ||
    plan.service_channel_id !== 24 ||
    plan.service_model !== 'deepseek-v4.1-flash' ||
    plan.duration_unit !== 'day' ||
    plan.quota_reset_period !== 'daily' ||
    plan.allow_wallet_overflow !== false ||
    plan.daily_input_token_limit <= 0 ||
    plan.daily_output_token_limit <= 0 ||
    plan.upgrade_group ||
    plan.downgrade_group
  ) {
    return null
  }

  switch (plan.duration_value) {
    case 1:
      return 'day'
    case 7:
      return 'week'
    case 30:
      return 'month'
    default:
      return null
  }
}
