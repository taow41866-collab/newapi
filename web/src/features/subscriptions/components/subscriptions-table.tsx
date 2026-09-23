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

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getAdminPlans } from '../api'
import { SubscriptionPlanActions } from './data-table-row-actions'
import { SubscriptionPlanCards } from './subscription-plan-cards'
import { useSubscriptions } from './subscriptions-provider'

export function SubscriptionsTable() {
  const { refreshTrigger, complianceConfirmed, setOpen, setCreatePeriod } =
    useSubscriptions()

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['admin-subscription-plans', refreshTrigger],
    queryFn: async () => {
      const result = requireServerSuccess(await getAdminPlans())
      return result.data || []
    },
    placeholderData: (prev) => prev,
  })

  if (isLoading) return <LoadingState />
  if (isError) return <ErrorState onRetry={() => void refetch()} />

  return (
    <SubscriptionPlanCards
      plans={data || []}
      canCreate={complianceConfirmed}
      onCreate={(period) => {
        setCreatePeriod(period)
        setOpen('create')
      }}
      renderActions={(record) => <SubscriptionPlanActions record={record} />}
    />
  )
}
