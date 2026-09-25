import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { redeemSubscriptionCode } from '../../api'
import { SubscriptionRedemptionCard } from '../subscription-redemption-card'

vi.mock('../../api', () => ({
  redeemSubscriptionCode: vi.fn(),
}))

const redeemSubscriptionCodeMock = vi.mocked(redeemSubscriptionCode)

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function setup(onRedeemed = vi.fn()) {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    fallbackLng: 'en',
  })
  render(
    <I18nextProvider i18n={i18n}>
      <SubscriptionRedemptionCard onRedeemed={onRedeemed} />
    </I18nextProvider>
  )
  return { onRedeemed, user: userEvent.setup() }
}

describe('subscription redemption card', () => {
  it('redeems a subscription card through the subscription endpoint', async () => {
    redeemSubscriptionCodeMock.mockResolvedValue({
      success: true,
      data: {
        id: 12,
        user_id: 1,
        plan_id: 2,
        status: 'active',
        start_time: 1,
        end_time: 2,
        amount_total: 0,
        amount_used: 0,
      },
    })
    const { onRedeemed, user } = await setup()

    await user.type(
      screen.getByRole('textbox', { name: 'Subscription card code' }),
      'SUB-d-EXAMPLE'
    )
    await user.click(screen.getByRole('button', { name: 'Redeem subscription card' }))

    expect(redeemSubscriptionCodeMock).toHaveBeenCalledWith('SUB-d-EXAMPLE')
    expect(onRedeemed).toHaveBeenCalledOnce()
    expect(screen.getByRole('textbox', { name: 'Subscription card code' })).toHaveValue('')
  })
})
