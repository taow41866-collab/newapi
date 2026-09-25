import { cleanup, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, describe, expect, it } from 'vitest'

import { subscriptionPlanSchema } from '../../types'
import { SubscriptionSummaryCards } from '../subscription-summary-cards'

afterEach(cleanup)

async function setup() {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    fallbackLng: 'en',
  })

  const plan = subscriptionPlanSchema.parse({
    id: 7,
    title: 'Daily Pro',
    subtitle: 'Fast model access',
    price_amount: 4.9,
    currency: 'CNY',
    billing_policy: 'ds-flash-v1',
    service_model: 'deepseek-v4.1-flash',
    daily_input_token_limit: 50000,
    daily_output_token_limit: 10000,
    duration_unit: 'day',
    duration_value: 1,
    quota_reset_period: 'daily',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 0,
  })
  const now = Math.floor(Date.now() / 1000)

  render(
    <I18nextProvider i18n={i18n}>
      <SubscriptionSummaryCards
        plans={[{ plan }]}
        subscriptions={[
          {
            subscription: {
              id: 12,
              user_id: 1,
              plan_id: 7,
              billing_policy: 'ds-flash-v1',
              service_model: 'deepseek-v4.1-flash',
              daily_input_token_limit: 50000,
              daily_output_token_limit: 10000,
              daily_input_tokens_used: 12000,
              daily_output_tokens_used: 2500,
              status: 'active',
              start_time: now - 3600,
              end_time: now + 86400,
              amount_total: 0,
              amount_used: 0,
              next_reset_time: now + 3600,
            },
          },
        ]}
      />
    </I18nextProvider>
  )
}

describe('subscription summary cards', () => {
  it('shows the subscribed plan and used and remaining V1 token quotas', async () => {
    await setup()

    const article = screen.getByRole('article', { name: 'Daily Pro' })
    expect(article).toBeVisible()
    expect(screen.getByText('Active')).toBeVisible()
    expect(screen.getByText('Day card')).toBeVisible()
    expect(screen.getByText('Price: ¥ 4.90')).toBeVisible()
    expect(article.textContent).toMatch(
      /Purchased at\s*\d{4}-\d{2}-\d{2} \d{2}:\d{2}/
    )
    expect(article.textContent).toMatch(
      /Expires at\s*\d{4}-\d{2}-\d{2} \d{2}:\d{2}/
    )
    expect(screen.getByText('Input tokens')).toBeVisible()
    expect(screen.getByText(/12,000/)).toBeVisible()
    expect(screen.getByText(/50,000/)).toBeVisible()
    expect(screen.getByText('Remaining: 38,000')).toBeVisible()
    expect(screen.getByText('Output tokens')).toBeVisible()
    expect(screen.getByText(/2,500/)).toBeVisible()
    expect(screen.getByText(/10,000/)).toBeVisible()
    expect(screen.getByText('Remaining: 7,500')).toBeVisible()
  })

  it('uses the purchase price snapshot when the plan is no longer public', async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: 'en',
      resources: { en: { translation: {} } },
      fallbackLng: 'en',
    })
    const now = Math.floor(Date.now() / 1000)
    render(
      <I18nextProvider i18n={i18n}>
        <SubscriptionSummaryCards
          plans={[]}
          subscriptions={[
            {
              plan_display: {
                title: 'DS day',
                duration_unit: 'day',
                duration_value: 1,
              },
              subscription: {
                id: 99,
                user_id: 1,
                plan_id: 999,
                price_amount: 2.9,
                currency: 'CNY',
                status: 'active',
                start_time: now - 60,
                end_time: now + 86400,
                amount_total: 0,
                amount_used: 0,
              },
            },
          ]}
        />
      </I18nextProvider>
    )

    expect(screen.getByText('Price: ¥ 2.90')).toBeVisible()
    expect(screen.getByRole('article', { name: 'DS day' })).toBeVisible()
    expect(screen.getByText('Day card')).toBeVisible()
  })
})
