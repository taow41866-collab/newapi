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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CCSwitchDialog } from '../cc-switch-dialog'

const { getUserModels, getTokenAutoGroups, getSelf } = vi.hoisted(() => ({
  getUserModels: vi.fn(),
  getTokenAutoGroups: vi.fn(),
  getSelf: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ getSelf, getUserModels }))
vi.mock('@/features/keys/api', () => ({ getTokenAutoGroups }))

let queryClient: QueryClient

beforeEach(() => {
  getUserModels.mockReset()
  getTokenAutoGroups.mockReset()
  getSelf.mockReset()
  getUserModels.mockImplementation(async (group: string) => ({
    success: true,
    data:
      group === 'vip'
        ? ['vip-model', 'vip-sonnet-model']
        : ['default-model'],
  }))
  getSelf.mockResolvedValue({ success: true, data: { group: 'vip' } })
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
})

afterEach(() => {
  queryClient.clear()
})

function renderDialog(group = 'vip', autoGroups: string[] = []) {
  render(
    <QueryClientProvider client={queryClient}>
      <CCSwitchDialog
        open
        onOpenChange={vi.fn()}
        tokenKey='test-only'
        group={group}
        autoGroups={autoGroups}
      />
    </QueryClientProvider>
  )
}

describe('CC Switch model selection', () => {
  it.each(['Claude', 'Codex', 'Gemini'])(
    'opens %s models outside the clipping dialog and keeps the dialog open after selection',
    async (app) => {
      renderDialog()
      const user = userEvent.setup()
      await user.click(screen.getByRole('radio', { name: app }))
      const input = screen.getByRole('combobox', { name: 'Primary Model' })

      await user.click(input)

      expect(input).toHaveAttribute('aria-expanded', 'true')
      const list = await screen.findByRole('listbox')
      const dialog = screen.getByRole('dialog', { name: 'Import to CC Switch' })
      // The dialog is translated and clips overflow. Its popup must escape
      // that containing block to remain aligned and fully visible.
      expect(dialog).not.toContainElement(list)
      await user.click(screen.getByRole('option', { name: 'vip-model' }))
      await waitFor(() => expect(input).toHaveValue('vip-model'))
      expect(input).toHaveAttribute('aria-expanded', 'false')
      expect(dialog).toBeVisible()
      expect(getUserModels).toHaveBeenCalledWith('vip')
      expect(screen.queryByRole('option', { name: 'default-model' })).toBeNull()
    }
  )

  it('filters model names and supports keyboard selection and Escape without closing the dialog', async () => {
    renderDialog()
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    const input = screen.getByRole('combobox', { name: 'Primary Model' })
    await user.click(input)
    await user.type(input, 'sonnet')

    expect(
      screen.getByRole('option', { name: 'vip-sonnet-model' })
    ).toBeVisible()
    await user.keyboard('{ArrowDown}{Enter}')
    await waitFor(() => expect(input).toHaveValue('vip-sonnet-model'))
    await user.click(input)
    await user.keyboard('{Escape}')

    expect(input).toHaveValue('vip-sonnet-model')
    expect(input).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('dialog')).toBeVisible()
  })

  it('shows an empty result when no models are available and updates an open dropdown when models arrive', async () => {
    getUserModels.mockResolvedValueOnce({ success: true, data: [] })
    renderDialog()
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    const input = screen.getByRole('combobox', { name: 'Primary Model' })
    await user.click(screen.getByRole('button', { name: 'Primary Model' }))

    expect(await screen.findByText('No models found')).toBeVisible()
    await act(async () => {
      queryClient.setQueryData(['user-models-ccswitch', 'vip', []], {
        success: true,
        data: ['vip-model'],
      })
    })
    await user.click(await screen.findByRole('option', { name: 'vip-model' }))
    await waitFor(() => expect(input).toHaveValue('vip-model'))
  })

  it('loads only the auto groups assigned to this key', async () => {
    renderDialog('auto', ['vip'])
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    await user.click(screen.getByRole('button', { name: 'Primary Model' }))

    expect(await screen.findByRole('option', { name: 'vip-model' })).toBeVisible()
    expect(screen.queryByRole('option', { name: 'default-model' })).toBeNull()
    expect(getUserModels).toHaveBeenCalledTimes(1)
    expect(getUserModels).toHaveBeenCalledWith('vip')
    expect(getTokenAutoGroups).not.toHaveBeenCalled()
  })

  it('uses the account Auto groups when this key inherits the global order', async () => {
    getTokenAutoGroups.mockResolvedValueOnce({
      success: true,
      data: { groups: ['vip', 'default'], max_count: 2 },
    })
    renderDialog('auto')
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    await user.click(screen.getByRole('button', { name: 'Primary Model' }))

    expect(await screen.findByRole('option', { name: 'vip-model' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'default-model' })).toBeVisible()
    expect(getUserModels).toHaveBeenCalledWith('vip')
    expect(getUserModels).toHaveBeenCalledWith('default')
  })

  it('falls back to the account group for legacy keys without a stored group', async () => {
    renderDialog('')
    const user = userEvent.setup()
    await user.click(screen.getByRole('radio', { name: 'Codex' }))
    await user.click(screen.getByRole('button', { name: 'Primary Model' }))

    expect(await screen.findByRole('option', { name: 'vip-model' })).toBeVisible()
    expect(screen.queryByRole('option', { name: 'default-model' })).toBeNull()
    expect(getSelf).toHaveBeenCalledOnce()
    expect(getUserModels).toHaveBeenCalledWith('vip')
  })

  it('lets users edit the provider name without opening a model dropdown', async () => {
    renderDialog()
    const user = userEvent.setup()
    const input = screen.getByRole('textbox', { name: 'Name' })

    await user.clear(input)
    await user.type(input, 'Development')

    expect(input).toHaveValue('Development')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })
})
