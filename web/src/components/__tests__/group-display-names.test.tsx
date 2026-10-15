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
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiKeyGroupCombobox } from '@/features/keys/components/api-key-group-combobox'
import { useAuthStore } from '@/stores/auth-store'
import { useGroupDisplayNamesStore } from '@/stores/group-display-names-store'

import { GroupBadge } from '../group-badge'

afterEach(() => {
  useGroupDisplayNamesStore.getState().setNames(null, {})
  useAuthStore.setState({
    auth: { ...useAuthStore.getState().auth, user: null },
  })
})

describe('group display names', () => {
  it('shows a display name while retaining the original identifier when selected', async () => {
    useAuthStore.setState({
      auth: {
        ...useAuthStore.getState().auth,
        user: { id: 1, username: 'admin', role: 100 },
      },
    })
    useGroupDisplayNamesStore.getState().setNames(1, { vip: 'Premium' })
    const onValueChange = vi.fn()
    const user = userEvent.setup()
    render(
      <>
        <GroupBadge group='vip' />
        <ApiKeyGroupCombobox
          options={[{ value: 'vip', label: 'vip' }]}
          onValueChange={onValueChange}
        />
      </>
    )
    expect(screen.getByText('Premium')).toBeVisible()
    await user.click(screen.getByRole('combobox'))
    fireEvent.click(screen.getByRole('option', { name: /Premium/ }))
    expect(onValueChange).toHaveBeenCalledWith('vip')
  })

  it('falls back to the identifier for unknown names and after switching accounts', () => {
    useGroupDisplayNamesStore.getState().setNames(1, { vip: 'Premium' })
    useAuthStore.setState({
      auth: {
        ...useAuthStore.getState().auth,
        user: { id: 2, username: 'other', role: 1 },
      },
    })
    render(
      <>
        <GroupBadge group='vip' />
        <GroupBadge group='legacy' />
      </>
    )
    expect(screen.getByText('vip')).toBeVisible()
    expect(screen.getByText('legacy')).toBeVisible()
  })
})
