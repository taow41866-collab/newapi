import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { appendPurchasePriceRule, getPurchasePrices } from '../../../api'
import { ChannelCostConfiguration } from '../channel-cost-configuration'

vi.mock('../../../api', () => ({
  appendPurchasePriceRule: vi.fn(),
  getPurchasePrices: vi.fn(),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

function mount() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ChannelCostConfiguration channelId={7} />
    </QueryClientProvider>
  )
}

it('appends a channel default multiplier without sending a replacement rule list', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({
    success: true,
    data: [
      {
        channel_id: 7,
        model: 'gpt-a',
        unit: 'model_multiplier',
        unit_price: 0.2,
        effective_at: 10,
        source: 'old',
      },
      {
        channel_id: 9,
        model: 'gpt-b',
        unit: 'model_multiplier',
        unit_price: 0.4,
        effective_at: 11,
        source: 'other channel',
      },
      {
        channel_id: 7,
        model: 'gpt-image',
        unit: 'image',
        unit_price: 0.06,
        effective_at: 12,
        source: 'invoice',
      },
    ],
  })
  vi.mocked(appendPurchasePriceRule).mockResolvedValue({
    success: true,
    data: {
      channel_id: 7,
      model: '*',
      unit: 'model_multiplier',
      unit_price: 0.75,
      effective_at: 100,
      source: 'channel cost estimate',
    },
  })
  mount()

  fireEvent.change(
    await screen.findByLabelText('Model or * for channel default'),
    { target: { value: '*' } }
  )
  fireEvent.change(screen.getByLabelText('Upstream cost multiplier'), {
    target: { value: '0.75' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save upstream cost' }))

  await waitFor(() => expect(appendPurchasePriceRule).toHaveBeenCalledTimes(1))
  expect(vi.mocked(appendPurchasePriceRule).mock.calls[0][0]).toEqual(
    expect.objectContaining({
      channel_id: 7,
      model: '*',
      unit: 'model_multiplier',
      unit_price: 0.75,
      effective_at: 0,
      source: 'channel cost estimate',
    })
  )
})

it('supports a fixed image purchase price on the channel', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({ success: true, data: [] })
  vi.mocked(appendPurchasePriceRule).mockResolvedValue({
    success: true,
    data: {
      channel_id: 7,
      model: 'image-model',
      unit: 'image',
      unit_price: 0.06,
      effective_at: 100,
      source: 'channel purchase price',
    },
  })
  mount()
  const user = userEvent.setup()

  fireEvent.change(
    await screen.findByLabelText('Model or * for channel default'),
    { target: { value: 'image-model' } }
  )
  await user.click(
    screen.getByRole('combobox', { name: 'Cost pricing method' })
  )
  await user.click(await screen.findByRole('option', { name: 'Per image' }))
  fireEvent.change(screen.getByLabelText('Upstream price per image'), {
    target: { value: '0.06' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save upstream cost' }))

  await waitFor(() => expect(appendPurchasePriceRule).toHaveBeenCalledTimes(1))
  expect(vi.mocked(appendPurchasePriceRule).mock.calls[0][0]).toEqual(
    expect.objectContaining({
      channel_id: 7,
      model: 'image-model',
      unit: 'image',
      unit_price: 0.06,
    })
  )
})

it('supports optional cached token prices for exact channel costs', async () => {
  vi.mocked(getPurchasePrices).mockResolvedValue({ success: true, data: [] })
  vi.mocked(appendPurchasePriceRule).mockResolvedValue({
    success: true,
    data: {
      channel_id: 7,
      model: 'cached-model',
      unit: 'tokens',
      input_price: 1,
      output_price: 2,
      cache_price: 0.2,
      cache_write_price: 0.4,
      effective_at: 100,
      source: 'channel purchase price',
    },
  })
  mount()
  const user = userEvent.setup()

  fireEvent.change(
    await screen.findByLabelText('Model or * for channel default'),
    { target: { value: 'cached-model' } }
  )
  await user.click(
    screen.getByRole('combobox', { name: 'Cost pricing method' })
  )
  await user.click(await screen.findByRole('option', { name: 'Token prices' }))
  fireEvent.change(screen.getByLabelText('Input price per million tokens'), {
    target: { value: '1' },
  })
  fireEvent.change(screen.getByLabelText('Output price per million tokens'), {
    target: { value: '2' },
  })
  fireEvent.change(
    screen.getByLabelText('Cache read price per million tokens'),
    {
      target: { value: '0.2' },
    }
  )
  fireEvent.change(
    screen.getByLabelText('Cache write price per million tokens'),
    {
      target: { value: '0.4' },
    }
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save upstream cost' }))

  await waitFor(() => expect(appendPurchasePriceRule).toHaveBeenCalledTimes(1))
  expect(vi.mocked(appendPurchasePriceRule).mock.calls[0][0]).toEqual(
    expect.objectContaining({
      input_price: 1,
      output_price: 2,
      cache_price: 0.2,
      cache_write_price: 0.4,
    })
  )
})
