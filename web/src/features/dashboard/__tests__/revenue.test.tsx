import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth-store'
import { RevenueDashboard } from '../revenue'
import { getPurchasePrices, getRevenueReport, updatePurchasePrices } from '../api'

vi.mock('../api', () => ({ getPurchasePrices: vi.fn(), getRevenueReport: vi.fn(), updatePurchasePrices: vi.fn() }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }) }))
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); useAuthStore.getState().auth.reset() })

function mount(role = 100) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'test', role })
	vi.mocked(getRevenueReport).mockResolvedValue({ success: true, data: { rows: [{ channel_id: 7, model: 'gpt-test', net_sales: 10, cost: 2, gross_profit: 8, gross_margin_rate: 0.8, pending_entries: 0, estimated_cost_entries: 1 }], net_sales: 10, known_cost: 2, estimated_cost_entries: 1, gross_profit: 8, gross_margin_rate: 0.8, uncovered_entries: 0 } })
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

it('explains the estimated multiplier rule and shows its usage count to root', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({ success: true, data: [] })
  mount()
  expect(await screen.findAllByText('Estimated cost entries')).toHaveLength(2)
  expect(await screen.findByText(/model_multiplier rule estimates standard token cost/)).toBeTruthy()
  expect(screen.getAllByRole('columnheader', { name: 'Estimated cost entries' })).toHaveLength(1)
  expect(within(screen.getByRole('row', { name: /7 gpt-test/ })).getByText('1')).toBeTruthy()
  fireEvent.change(screen.getByLabelText('Channel ID'), { target: { value: '7' } })
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'gpt-test' } })
  fireEvent.change(screen.getByLabelText('Cost multiplier'), { target: { value: '0.75' } })
  fireEvent.click(screen.getByRole('button', { name: 'Add multiplier rule' }))
  expect((screen.getByLabelText('Purchase price rules') as HTMLTextAreaElement).value).toContain('"unit_price": 0.75')
})

it('rejects an empty multiplier instead of treating it as zero', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({ success: true, data: [] })
  mount()
  fireEvent.change(await screen.findByLabelText('Channel ID'), { target: { value: '7' } })
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'gpt-test' } })
  fireEvent.click(screen.getByRole('button', { name: 'Add multiplier rule' }))
  expect(await screen.findByRole('alert')).toBeTruthy()
  expect((screen.getByLabelText('Purchase price rules') as HTMLTextAreaElement).value).not.toContain('model_multiplier')
})

it('shows overall and per-channel gross margin rates', async () => {
	mount()
	expect(await screen.findAllByText('Gross margin')).toHaveLength(2)
	expect(await screen.findAllByText('80%')).toHaveLength(2)
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
  const input = await screen.findByLabelText('Purchase price rules')
  const first = vi.mocked(getRevenueReport).mock.calls[0][0]
  vi.spyOn(Date, 'now').mockReturnValue((first.end_timestamp + 60) * 1000)
  fireEvent.change(input, { target: { value: '[ ]' } })
  expect(getRevenueReport).toHaveBeenCalledTimes(1)
})
