import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { getRevenueReport, getUserQuotaDates } from '../../../api'
import { LogStatCards } from '../log-stat-cards'

vi.mock('../../../api', () => ({
  getRevenueReport: vi.fn(),
  getUserQuotaDates: vi.fn(),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.clearAllMocks()
  useAuthStore.getState().auth.reset()
})

it('shows profitability metrics to admins and marks incomplete cost as unknown', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  vi.mocked(getUserQuotaDates).mockResolvedValue({ success: true, data: [] })
  vi.mocked(getRevenueReport).mockResolvedValue({
    success: true,
    data: {
      rows: [],
      net_sales: 20,
      known_cost: 8,
      gross_profit: null,
      gross_margin_rate: null,
      uncovered_entries: 2,
    },
  })

  render(<LogStatCards />)

  expect(await screen.findByText('Net sales')).toBeTruthy()
  expect(screen.getByText('Known cost')).toBeTruthy()
  expect(screen.getByText('Estimated gross profit')).toBeTruthy()
  expect(screen.getByText('Gross margin')).toBeTruthy()
  expect(screen.getAllByText('Incomplete cost data').length).toBeGreaterThan(0)
  await waitFor(() => expect(getRevenueReport).toHaveBeenCalledTimes(1))
})

it('does not request or expose revenue metrics for regular users', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'user', role: 1 })
  vi.mocked(getUserQuotaDates).mockResolvedValue({ success: true, data: [] })

  render(<LogStatCards />)

  await waitFor(() => expect(getUserQuotaDates).toHaveBeenCalledTimes(1))
  expect(getRevenueReport).not.toHaveBeenCalled()
  expect(screen.queryByText('Gross margin')).toBeNull()
})

it('applies the selected username to administrator profitability metrics', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  vi.mocked(getUserQuotaDates).mockResolvedValue({ success: true, data: [] })
  vi.mocked(getRevenueReport).mockResolvedValue({
    success: true,
    data: {
      rows: [],
      net_sales: 0,
      known_cost: 0,
      gross_profit: 0,
      gross_margin_rate: null,
      uncovered_entries: 0,
    },
  })

  render(<LogStatCards filters={{ username: 'alice' }} />)

  await waitFor(() =>
    expect(getRevenueReport).toHaveBeenCalledWith(
      expect.objectContaining({ username: 'alice' })
    )
  )
})

it('does not request revenue for a range beyond the report limit', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  vi.mocked(getUserQuotaDates).mockResolvedValue({ success: true, data: [] })

  render(
    <LogStatCards
      filters={{
        start_timestamp: new Date(1 * 1000),
        end_timestamp: new Date(33 * 86400 * 1000),
      }}
    />
  )

  expect(
    await screen.findAllByText('Select a period of 31 days or less')
  ).toHaveLength(4)
  expect(getRevenueReport).not.toHaveBeenCalled()
})
