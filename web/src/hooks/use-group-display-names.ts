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
import { useQuery } from '@tanstack/react-query'
import { useCallback, useEffect } from 'react'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'
import { useGroupDisplayNamesStore } from '@/stores/group-display-names-store'

export function useGroupLabel() {
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  const ownerId = useGroupDisplayNamesStore((state) => state.userId)
  const names = useGroupDisplayNamesStore((state) => state.names)
  return useCallback(
    (id: string, fallback = id) => {
      if (!id || id === 'auto' || ownerId !== userId) return fallback
      const name = Object.hasOwn(names, id) ? names[id] : undefined
      return typeof name === 'string' && name.trim() ? name : fallback
    },
    [names, ownerId, userId]
  )
}

export function useLoadGroupDisplayNames() {
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  const setNames = useGroupDisplayNamesStore((state) => state.setNames)
  const query = useQuery({
    queryKey: ['group-display-names', userId],
    enabled: userId !== null,
    queryFn: async () => {
      const response = await api.get('/api/group/display-names')
      const result = requireServerSuccess(response.data)
      return result.data as Record<string, string>
    },
    staleTime: 5 * 60 * 1000,
  })
  useEffect(() => {
    if (query.data) setNames(userId, query.data)
  }, [query.data, userId, setNames])
}
