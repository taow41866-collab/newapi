import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { getRevenueReport } from '../api'
import { RevenueDashboard } from '../revenue'

vi.mock('../api', () => ({ getRevenueReport: vi.fn() }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

function mount() {
  vi.mocked(getRevenueReport).mockResolvedValue({
    success: true,
    data: {
      rows: [
        {
          channel_id: 7,
          model: 'gpt-test',
          net_sales: 10,
          cost: 2,
          gross_profit: 8,
          gross_margin_rate: 0.8,
          pending_entries: 0,
          estimated_cost_entries: 1,
        },
      ],
      net_sales: 10,
      known_cost: 2,
      estimated_cost_entries: 1,
      gross_profit: 8,
      gross_margin_rate: 0.8,
      uncovered_entries: 0,
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <RevenueDashboard />
    </QueryClientProvider>
  )
}

it('keeps the detailed report read-only and directs cost configuration to channels', async () => {
  mount()
  expect(
    await screen.findByRole('columnheader', { name: 'Net sales' })
  ).toBeTruthy()
  expect(
    screen.getByText(/Configure estimated upstream costs in each channel/)
  ).toBeTruthy()
  expect(screen.queryByRole('textbox')).toBeNull()
  expect(
    screen.queryByRole('button', { name: /Save purchase prices/ })
  ).toBeNull()
})

it('shows estimated gross margin and row-level costs', async () => {
  mount()
  expect(await screen.findAllByText('Gross margin')).toHaveLength(2)
  expect(await screen.findAllByText('80%')).toHaveLength(2)
  expect(screen.getByRole('row', { name: /7 gpt-test/ })).toBeTruthy()
})
