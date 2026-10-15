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
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'
import { useGroupDisplayNamesStore } from '@/stores/group-display-names-store'

import { useUpdateGroupSettings, useUpdateOption } from '../use-update-option'

const { put } = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { put } }))

function setUser(id: number, role: number) {
  useAuthStore.setState({
    auth: {
      ...useAuthStore.getState().auth,
      user: { id, username: `user${id}`, role },
    },
  })
}

function renderMutations() {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  return renderHook(
    () => ({ option: useUpdateOption(), bulk: useUpdateGroupSettings() }),
    {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    }
  )
}

beforeEach(() => {
  put.mockReset()
  setUser(1, 100)
  useGroupDisplayNamesStore.getState().setNames(1, { vip: 'Original' })
})
afterEach(() => {
  useAuthStore.setState({
    auth: { ...useAuthStore.getState().auth, user: null },
  })
  useGroupDisplayNamesStore.getState().setNames(null, {})
})

it.each(['option', 'bulk'] as const)(
  'does not publish a late %s response to a different account',
  async (mode) => {
    let resolveSave!: (value: { data: { success: boolean } }) => void
    put.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSave = resolve
        })
    )
    const { result } = renderMutations()
    let pending!: Promise<unknown>
    act(() => {
      pending =
        mode === 'option'
          ? result.current.option.mutateAsync({
              key: 'GroupDisplayNames',
              value: '{"vip":"Root private name"}',
            })
          : result.current.bulk.mutateAsync({
              GroupDisplayNames: '{"vip":"Root private name"}',
            })
    })
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    act(() => {
      setUser(2, 1)
      useGroupDisplayNamesStore.getState().setNames(2, { default: 'Public' })
    })
    await act(async () => {
      resolveSave({ data: { success: true } })
      await pending
    })
    expect(useGroupDisplayNamesStore.getState()).toMatchObject({
      userId: 2,
      names: { default: 'Public' },
    })
  }
)

it('keeps names unchanged when a sparse transaction only changes a ratio', async () => {
  put.mockResolvedValue({ data: { success: true } })
  const { result } = renderMutations()
  await act(async () => {
    await result.current.bulk.mutateAsync({ GroupRatio: '{"vip":0.4}' })
  })
  expect(useGroupDisplayNamesStore.getState().names).toEqual({
    vip: 'Original',
  })
})
