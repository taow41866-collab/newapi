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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { RatioSettingsCard } from '../ratio-settings-card'

const { mutateAsync, put } = vi.hoisted(() => ({
  mutateAsync: vi.fn(),
  put: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { put },
}))
vi.mock('../../hooks/use-update-option', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../hooks/use-update-option')>()),
  useUpdateOption: () => ({ mutateAsync, isPending: false }),
}))
vi.mock('@/features/model-pricing/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/model-pricing/api')>()),
  useModelPricing: () => ({ data: null, isError: false }),
  useSaveModelPricing: () => ({ isPending: false }),
}))

const groups = {
  GroupDisplayNames: '{}',
  GroupRatio: '{"default":1,"vip":0.8}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{}',
  GroupGroupRatio: '{}',
  AutoGroups: '["default","vip"]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
}
const models = {
  ModelPrice: '{}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  ExposeRatioEnabled: false,
  BillingMode: '{}',
  BillingExpr: '{}',
  PluginBillingExpr: '{}',
}
const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
function Fixture({ defaults = groups }: { defaults?: typeof groups }) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <div ref={setActions} />
        <RatioSettingsCard
          modelDefaults={models}
          groupDefaults={defaults}
          toolPricesDefault='{}'
          visibleTabs={['groups']}
        />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}
beforeEach(() => {
  put.mockReset()
  put.mockResolvedValue({ data: { success: true } })
  mutateAsync.mockReset()
  mutateAsync.mockResolvedValue({ success: true })
})

it('keeps rejected edits across a settings refetch, retries, and only advances the baseline after success', async () => {
  const user = userEvent.setup()
  put.mockRejectedValueOnce(new Error('Referenced group cannot be removed'))
  const view = render(<Fixture />)
  const name = screen.getAllByRole('textbox', { name: 'Group name' })[1]
  await user.clear(name)
  await user.type(name, 'Premium')
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Referenced group cannot be removed'
  )
  view.rerender(<Fixture defaults={{ ...groups }} />)
  expect(screen.getAllByRole('textbox', { name: 'Group name' })[1]).toHaveValue(
    'Premium'
  )
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls[1]).toEqual([
    '/api/group/settings',
    {
      values: {
        GroupDisplayNames: '{"vip":"Premium"}',
      },
    },
  ])
  await waitFor(() =>
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  expect(put).toHaveBeenCalledTimes(2)
  expect(mutateAsync).not.toHaveBeenCalled()
})

it('locks a new identifier only after its settings were saved', async () => {
  const user = userEvent.setup()
  render(<Fixture />)
  await user.click(screen.getByRole('button', { name: 'Add group' }))
  const id = screen.getByRole('textbox', { name: 'Group identifier' })
  await user.clear(id)
  await user.type(id, 'enterprise')
  await user.type(
    screen.getAllByRole('textbox', { name: 'Group name' })[2],
    'Enterprise'
  )
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() =>
    expect(
      screen.queryByRole('textbox', { name: 'Group identifier' })
    ).not.toBeInTheDocument()
  )
  expect(screen.getByText('enterprise', { selector: 'code' })).toBeVisible()
  expect(put).toHaveBeenCalledExactlyOnceWith('/api/group/settings', {
    values: {
      GroupRatio: '{"default":1,"vip":0.8,"enterprise":1}',
      UserUsableGroups: '{"enterprise":""}',
      GroupDisplayNames: '{"enterprise":"Enterprise"}',
    },
  })
  expect(mutateAsync).not.toHaveBeenCalled()
})

it('treats a server rejection as a failed transaction and retries the changed settings', async () => {
  const user = userEvent.setup()
  put.mockResolvedValueOnce({
    data: { success: false, message: 'Duplicate group display name' },
  })
  render(<Fixture />)
  const name = screen.getAllByRole('textbox', { name: 'Group name' })[1]
  await user.clear(name)
  await user.type(name, 'Premium')
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Duplicate group display name'
  )
  expect(name).toHaveValue('Premium')
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls[1]).toEqual(put.mock.calls[0])
  expect(mutateAsync).not.toHaveBeenCalled()
})

it('does not overwrite a newer ratio when saving a display-name draft', async () => {
  const user = userEvent.setup()
  const view = render(
    <Fixture defaults={{ ...groups, GroupRatio: '{"default":1,"vip":0.3}' }} />
  )
  const name = screen.getAllByRole('textbox', { name: 'Group name' })[1]
  await user.clear(name)
  await user.type(name, 'Premium')
  view.rerender(
    <Fixture defaults={{ ...groups, GroupRatio: '{"default":1,"vip":0.4}' }} />
  )
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(put).toHaveBeenCalledWith('/api/group/settings', {
    values: { GroupDisplayNames: '{"vip":"Premium"}' },
  })
})

it('keeps edits made during an in-flight save dirty for the next transaction', async () => {
  const user = userEvent.setup()
  let resolveSave!: (value: { data: { success: boolean } }) => void
  put.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveSave = resolve
      })
  )
  const view = render(<Fixture />)
  const name = screen.getAllByRole('textbox', { name: 'Group name' })[1]
  await user.clear(name)
  await user.type(name, 'Premium')
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  await user.clear(name)
  await user.type(name, 'Premium Plus')
  await act(async () => {
    resolveSave({ data: { success: true } })
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save group settings' })
    ).toBeEnabled()
  )
  view.rerender(
    <Fixture defaults={{ ...groups, GroupDisplayNames: '{"vip":"Premium"}' }} />
  )
  expect(screen.getAllByRole('textbox', { name: 'Group name' })[1]).toHaveValue(
    'Premium Plus'
  )
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls[1][1].values.GroupDisplayNames).toBe(
    '{"vip":"Premium Plus"}'
  )
})
