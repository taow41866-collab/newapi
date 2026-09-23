/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { subscriptionPlanSchema } from '../../types'
import { SubscriptionPlanCards } from '../subscription-plan-cards'

afterEach(cleanup)

function plan(id: number, unit: 'day' | 'month' | 'year', value: number) {
  return {
    plan: subscriptionPlanSchema.parse({
      id,
      title: `Plan ${id}`,
      price_amount: 4.9,
      currency: 'CNY',
      duration_unit: unit,
      duration_value: value,
      quota_reset_period: 'never',
      enabled: true,
      sort_order: 0,
      max_purchase_per_user: 0,
      total_amount: 0,
    }),
  }
}

async function setup(
  plans = [plan(3, 'month', 1), plan(2, 'day', 7), plan(1, 'day', 1)],
  locked = false
) {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    fallbackLng: 'en',
  })
  const onCreate = vi.fn()
  render(
    <I18nextProvider i18n={i18n}>
      <SubscriptionPlanCards
        plans={plans}
        canCreate={!locked}
        onCreate={onCreate}
        renderActions={() => null}
      />
    </I18nextProvider>
  )
  return { onCreate, user: userEvent.setup() }
}

describe('administrator plan cards', () => {
  it('orders daily, weekly and monthly sections vertically regardless of API order', async () => {
    await setup()
    expect(
      screen.getAllByRole('heading', { level: 2 }).map((el) => el.textContent)
    ).toEqual(['Day pass', 'Week pass', 'Month pass'])
    expect(
      screen.getByRole('region', { name: 'Subscription plans' })
    ).toHaveClass('flex', 'flex-col', 'gap-5')
    expect(
      screen.getByRole('region', { name: 'Subscription plans' })
    ).not.toHaveClass('auto-rows-fr', 'min-h-full')
    expect(screen.getByRole('article', { name: 'Plan 1' })).toHaveClass(
      'rounded-md',
      'border'
    )
    expect(
      screen.getByRole('article', { name: 'Plan 1' }).parentElement
    ).not.toHaveClass('xl:grid-cols-2')
    expect(
      screen.getByRole('region', { name: 'Subscription plans' }).parentElement
    ).toHaveClass('overflow-y-auto')
  })
  it('keeps missing periods visible with a working create action', async () => {
    const { user, onCreate } = await setup([])
    await user.click(
      within(screen.getByRole('region', { name: 'Week pass' })).getByRole(
        'button',
        { name: 'Create Plan' }
      )
    )
    expect(onCreate).toHaveBeenCalledWith('week')
  })
  it('disables creation when existing compliance restrictions apply', async () => {
    await setup([], true)
    for (const button of screen.getAllByRole('button', { name: 'Create Plan' }))
      expect(button).toBeDisabled()
  })
  it('offers a year filter when an annual plan exists and shows only that period', async () => {
    const { user } = await setup([plan(1, 'day', 1), plan(4, 'year', 1)])
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Plan period' }),
      'year'
    )
    expect(
      screen.getAllByRole('heading', { level: 2 }).map((el) => el.textContent)
    ).toEqual(['Year pass'])
    expect(screen.getByText('Plan 4')).toBeVisible()
    expect(screen.queryByText('Plan 1')).not.toBeInTheDocument()
  })
  it('does not invent daily token limits or convert the stored currency', async () => {
    await setup([plan(1, 'day', 1)])
    expect(screen.getByText('CNY 4.90')).toBeVisible()
    expect(screen.getByText('No Reset')).toBeVisible()
    expect(screen.getByText('Unlimited')).toBeVisible()
    expect(screen.queryByText('Daily input tokens')).not.toBeInTheDocument()
  })
  it('retains multiple plans and does not classify by a misleading title', async () => {
    const misleading = plan(5, 'day', 14)
    misleading.plan.title = 'Day pass special'
    await setup([plan(1, 'day', 1), plan(2, 'day', 1), misleading])
    expect(
      within(screen.getByRole('region', { name: 'Day pass' })).getByText(
        'Plan 2'
      )
    ).toBeVisible()
    expect(
      within(screen.getByRole('region', { name: 'Day pass' }))
        .getByRole('article', { name: 'Plan 1' }).parentElement
    ).toHaveClass('xl:grid-cols-2')
    expect(
      within(screen.getByRole('region', { name: 'Other periods' })).getByText(
        'Day pass special'
      )
    ).toBeVisible()
  })
  it('uses three columns when a period has three price tiers', async () => {
    await setup([plan(1, 'day', 1), plan(2, 'day', 1), plan(3, 'day', 1)])
    expect(
      screen.getByRole('article', { name: 'Plan 1' }).parentElement
    ).toHaveClass('xl:grid-cols-3')
  })
  it('shows independent input and output limits for a V1 plan instead of unlimited quota', async () => {
    const record = plan(1, 'day', 1)
    Object.assign(record.plan, {
      billing_policy: 'ds-flash-v1',
      daily_input_token_limit: 50000,
      daily_output_token_limit: 10000,
      service_channel_id: 24,
      service_model: 'deepseek-v4.1-flash',
    })
    await setup([record])
    expect(screen.getByText('50,000')).toBeVisible()
    expect(screen.getByText('10,000')).toBeVisible()
    expect(screen.getByText('deepseek-v4.1-flash')).toBeVisible()
    expect(screen.queryByText('Unlimited')).not.toBeInTheDocument()
    expect(screen.queryByText('Channel')).not.toBeInTheDocument()
    expect(screen.queryByText('#24')).not.toBeInTheDocument()
  })
})
