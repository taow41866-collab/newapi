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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { CustomerChannelDiscountHistory } from '../../types'
import { CustomerChannelDiscountDialog } from '../customer-channel-discount-dialog'

const history: CustomerChannelDiscountHistory = {
  version: 1,
  rules: [
    {
      channel_id: 32,
      model: '*',
      multiplier: 0.7,
      disabled: false,
      version: 1,
      effective_at: 1791187200000,
      actor_id: 1,
    },
  ],
}

function renderEditor(role = 100) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <CustomerChannelDiscountDialog
        open
        onOpenChange={() => undefined}
        userId={2}
        username='customer'
      />
    </QueryClientProvider>
  )
  return client
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('administrator cannot see the editor or request Root-only rules', () => {
  const get = vi.spyOn(api, 'get')
  renderEditor(10)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

it('closing and reopening reads current authoritative rules instead of an indefinitely cached version', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: history } })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: 100 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = (open: boolean) => (
    <QueryClientProvider client={client}>
      <CustomerChannelDiscountDialog
        open={open}
        onOpenChange={() => undefined}
        userId={2}
        username='customer'
      />
    </QueryClientProvider>
  )
  const rendered = render(view(true))
  expect(
    await screen.findByRole('spinbutton', { name: 'Discount multiplier 1' })
  ).toHaveValue(0.7)
  rendered.rerender(view(false))
  get.mockResolvedValue({
    data: {
      success: true,
      data: {
        version: 2,
        rules: [{ ...history.rules[0], multiplier: 0.4, version: 2 }],
      },
    },
  })
  rendered.rerender(view(true))
  await waitFor(() =>
    expect(
      screen.getByRole('spinbutton', { name: 'Discount multiplier 1' })
    ).toHaveValue(0.4)
  )
  expect(get).toHaveBeenCalledTimes(2)
})

it('shows loading and preserves failure as an inline error with refresh', async () => {
  let reject!: (reason: unknown) => void
  vi.spyOn(api, 'get').mockImplementation(
    () =>
      new Promise((_resolve, rejectPromise) => {
        reject = rejectPromise
      })
  )
  renderEditor()
  expect(await screen.findByText('Loading...')).toBeVisible()
  reject(new Error('Rules unavailable'))
  expect(await screen.findByText('Rules unavailable')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(
    screen.queryByRole('button', { name: 'Save customer discounts' })
  ).not.toBeInTheDocument()
})

it('shows current rules and history timestamps and appends only changed rules', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: history },
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...history,
        version: 2,
        rules: [
          ...history.rules,
          { ...history.rules[0], multiplier: 0.5, version: 2 },
        ],
      },
    },
  })
  renderEditor()
  const multiplier = await screen.findByRole('spinbutton', {
    name: 'Discount multiplier 1',
  })
  await userEvent.clear(multiplier)
  await userEvent.type(multiplier, '0.5')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save customer discounts' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/2/customer-channel-discounts', {
      expected_version: 1,
      rules: [{ channel_id: 32, model: '*', multiplier: 0.5, disabled: false }],
    })
  )
  expect(await screen.findByText('Customer discounts saved')).toBeVisible()
  await userEvent.click(screen.getByText('Rule history'))
  expect(screen.getAllByText('1').length).toBeGreaterThan(0)
})

it('supports six channel rules and disables a removed saved rule without overwriting history', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: history },
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: { version: 2, rules: [] } },
  })
  renderEditor()
  await screen.findByRole('spinbutton', { name: 'Channel ID 1' })
  await userEvent.click(screen.getByRole('button', { name: 'Remove rule 1' }))
  for (let index = 0; index < 6; index++) {
    await userEvent.click(
      screen.getByRole('button', { name: 'Add discount rule' })
    )
    await userEvent.type(
      screen.getByRole('spinbutton', { name: `Channel ID ${index + 1}` }),
      String(index + 1)
    )
  }
  expect(
    screen.getAllByRole('spinbutton', { name: /Channel ID/ })
  ).toHaveLength(6)
  await userEvent.click(
    screen.getByRole('button', { name: 'Save customer discounts' })
  )
  await waitFor(() => expect(put).toHaveBeenCalled())
  expect(put.mock.calls[0][1]).toEqual({
    expected_version: 1,
    rules: [
      { channel_id: 32, model: '*', multiplier: 0.7, disabled: true },
      ...Array.from({ length: 6 }, (_, index) => ({
        channel_id: index + 1,
        model: '*',
        multiplier: 1,
        disabled: false,
      })),
    ],
  })
})

it('rejects invalid multiplier visibly and does not save', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: history },
  })
  const put = vi.spyOn(api, 'put')
  renderEditor()
  const multiplier = await screen.findByRole('spinbutton', {
    name: 'Discount multiplier 1',
  })
  await userEvent.clear(multiplier)
  await userEvent.type(multiplier, '0')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save customer discounts' })
  )
  await waitFor(() =>
    expect(multiplier).toHaveAttribute('aria-invalid', 'true')
  )
  expect(put).not.toHaveBeenCalled()
})

it('conflict retains draft and requires an explicit refresh before another save', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: history } })
  vi.spyOn(api, 'put').mockRejectedValue({
    response: {
      status: 409,
      data: { success: false, message: 'Rules changed' },
    },
  })
  renderEditor()
  const multiplier = await screen.findByRole('spinbutton', {
    name: 'Discount multiplier 1',
  })
  await userEvent.clear(multiplier)
  await userEvent.type(multiplier, '0.5')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save customer discounts' })
  )
  expect(
    await screen.findByText(
      'Customer discounts changed. Refresh before saving again.'
    )
  ).toBeVisible()
  expect(multiplier).toHaveValue(0.5)
  expect(
    screen.getByRole('button', { name: 'Save customer discounts' })
  ).toBeDisabled()
  get.mockResolvedValue({
    data: { success: true, data: { ...history, version: 2 } },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Refresh rules' }))
  await waitFor(() =>
    expect(
      screen.getByRole('spinbutton', { name: 'Discount multiplier 1' })
    ).toHaveValue(0.7)
  )
  expect(
    screen.getByRole('button', { name: 'Save customer discounts' })
  ).toBeEnabled()
})
