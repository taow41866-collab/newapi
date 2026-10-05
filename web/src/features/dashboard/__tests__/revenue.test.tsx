import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth-store'
import { RevenueDashboard } from '../revenue'
import { getPurchasePrices, getRevenueReport, updatePurchasePrices } from '../api'

vi.mock('../api', () => ({ getPurchasePrices: vi.fn(), getRevenueReport: vi.fn(), updatePurchasePrices: vi.fn() }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }) }))
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); useAuthStore.getState().auth.reset() })

function mount(role = 100) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'test', role })
  vi.mocked(getRevenueReport).mockResolvedValue({ success: true, data: { rows: [], net_sales: 10, known_cost: 2, gross_profit: 8, uncovered_entries: 0 } })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RevenueDashboard /></QueryClientProvider>)
}

it('does not offer price editing when the existing configuration cannot be read', async () => {
  vi.mocked(getPurchasePrices).mockRejectedValue(new Error('read failed'))
  mount()
  await screen.findByRole('columnheader', { name: 'Net sales' })
  await screen.findByRole('alert')
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Save purchase prices' })).toBeNull())
  expect(updatePurchasePrices).not.toHaveBeenCalled()
})

it('non-root administrators view revenue without requesting root price configuration', async () => {
  mount(10)
  await screen.findByRole('columnheader', { name: 'Net sales' })
  expect(getPurchasePrices).not.toHaveBeenCalled()
  expect(screen.queryByRole('textbox')).toBeNull()
})

it('reports malformed JSON instead of silently ignoring a save', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({ success: true, data: [] })
  mount()
  const input = await screen.findByRole('textbox', { name: 'Purchase price rules' })
  fireEvent.change(input, { target: { value: '{bad' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save purchase prices' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Invalid purchase price rules')
  expect(updatePurchasePrices).not.toHaveBeenCalled()
})

it('keeps the report time window stable while editing configuration', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({ success: true, data: [] })
  mount()
  const input = await screen.findByRole('textbox')
  const first = vi.mocked(getRevenueReport).mock.calls[0][0]
  vi.spyOn(Date, 'now').mockReturnValue((first.end_timestamp + 60) * 1000)
  fireEvent.change(input, { target: { value: '[ ]' } })
  expect(getRevenueReport).toHaveBeenCalledTimes(1)
})
