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
import { BadgePercent, CircleDollarSign, Coins, TrendingUp } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getRevenueReport, getUserQuotaDates } from '@/features/dashboard/api'
import { useModelStatCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import {
  buildQueryParams,
  calculateDashboardStats,
  getDefaultDays,
} from '@/features/dashboard/lib'
import type {
  QuotaDataItem,
  DashboardFilters,
} from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatCompactNumber, formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

interface LogStatCardsProps {
  filters?: DashboardFilters
  onDataUpdate?: (data: QuotaDataItem[], loading: boolean) => void
}

const MAX_INLINE_STAT_CHARS = 9

function formatStatNumber(value: number, locale: Intl.LocalesArgument) {
  const fullValue = formatNumber(value, locale)
  const displayValue =
    fullValue.length > MAX_INLINE_STAT_CHARS
      ? formatCompactNumber(value, locale)
      : fullValue

  return {
    displayValue,
    fullValue,
  }
}

export function LogStatCards(props: LogStatCardsProps) {
  const { t, i18n } = useTranslation()
  const statCardsConfig = useModelStatCardsConfig()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = !!(user?.role && user.role >= 10)
  const [stats, setStats] = useState<{
    totalQuota: number
    totalCount: number
    totalTokens: number
  } | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [revenueState, setRevenueState] = useState<
    | { status: 'hidden' | 'loading' | 'error' | 'range' }
    | {
        status: 'ready'
        report: Awaited<ReturnType<typeof getRevenueReport>>['data']
      }
  >({ status: isAdmin ? 'loading' : 'hidden' })

  const [timeRangeMinutes, setTimeRangeMinutes] = useState(0)

  const { filters, onDataUpdate } = props

  useEffect(() => {
    const abortController = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)

    setError(false)
    onDataUpdate?.([], true)

    const timeRange = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )
    const timeDiff = (timeRange.end_timestamp - timeRange.start_timestamp) / 60
    setTimeRangeMinutes(timeDiff)

    void getUserQuotaDates(buildQueryParams(timeRange, filters), isAdmin)
      .then((res) => {
        if (abortController.signal.aborted) return
        const data = res?.data || []
        setStats(calculateDashboardStats(data))
        onDataUpdate?.(data, false)
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setStats(null)
        setError(true)
        onDataUpdate?.([], false)
      })
      .finally(() => {
        if (!abortController.signal.aborted) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
    }
  }, [filters, isAdmin, onDataUpdate])

  useEffect(() => {
    if (!isAdmin) {
      setRevenueState({ status: 'hidden' })
      return
    }
    const range = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )
    if (range.end_timestamp - range.start_timestamp > 31 * 86400) {
      setRevenueState({ status: 'range' })
      return
    }

    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRevenueState({ status: 'loading' })
    void getRevenueReport({ ...range, username: filters?.username })
      .then((result) => {
        if (!cancelled) {
          setRevenueState({ status: 'ready', report: result.data })
        }
      })
      .catch(() => {
        if (!cancelled) setRevenueState({ status: 'error' })
      })
    return () => {
      cancelled = true
    }
  }, [filters, isAdmin])

  const adaptedStats = {
    rpm: stats?.totalCount ?? 0,
    quota: stats?.totalQuota ?? 0,
    tpm: stats?.totalTokens ?? 0,
  }

  const items = statCardsConfig.map((config) => {
    const rawValue = config.getValue(adaptedStats, timeRangeMinutes)
    const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
    const formatted =
      config.key === 'quota'
        ? {
            displayValue: formatQuota(rawValue),
            fullValue: formatQuota(rawValue),
          }
        : formatStatNumber(rawValue, locale)

    return {
      key: config.key,
      title: config.title,
      value: formatted.displayValue,
      fullValue: formatted.fullValue,
      desc: config.description,
      icon: config.icon,
      iconTone: config.iconTone,
      loading,
      error,
    }
  })

  const revenueReport =
    revenueState.status === 'ready' ? revenueState.report : undefined
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  let revenueStatusText = t('Estimated from configured channel costs')
  if (revenueState.status === 'range') {
    revenueStatusText = t('Select a period of 31 days or less')
  } else if (revenueState.status === 'error') {
    revenueStatusText = t('Unable to load revenue')
  }
  const revenueUnavailable =
    revenueState.status === 'range' || revenueState.status === 'error'
  const netSalesValue = revenueReport
    ? formatBillingCurrencyFromUSD(revenueReport.net_sales)
    : '--'
  const knownCostValue = revenueReport
    ? formatBillingCurrencyFromUSD(revenueReport.known_cost)
    : '--'
  let grossProfitValue = t('Unknown')
  if (revenueReport?.gross_profit != null) {
    grossProfitValue = formatBillingCurrencyFromUSD(revenueReport.gross_profit)
  }
  let grossMarginValue = t('Unknown')
  if (revenueReport?.gross_margin_rate != null) {
    grossMarginValue = new Intl.NumberFormat(locale, {
      style: 'percent',
      maximumFractionDigits: 2,
    }).format(revenueReport.gross_margin_rate)
  }
  let knownCostDescription = revenueStatusText
  if (!revenueUnavailable && revenueReport) {
    knownCostDescription = t('{{count}} entries uncovered', {
      count: revenueReport.uncovered_entries,
    })
  }
  let grossProfitDescription = revenueStatusText
  if (!revenueUnavailable && revenueReport?.gross_profit != null) {
    grossProfitDescription = t('Shown only when all costs are covered')
  } else if (!revenueUnavailable && revenueReport) {
    grossProfitDescription = t('Incomplete cost data')
  }
  let grossMarginDescription = revenueStatusText
  if (!revenueUnavailable && revenueReport?.gross_margin_rate != null) {
    grossMarginDescription = t('Estimated from configured channel costs')
  } else if (!revenueUnavailable && revenueReport) {
    grossMarginDescription = t('Incomplete cost data')
  }
  const revenueItems =
    revenueState.status === 'hidden'
      ? []
      : [
          {
            key: 'revenue',
            title: t('Net sales'),
            value: netSalesValue,
            fullValue: netSalesValue,
            desc: revenueUnavailable
              ? revenueStatusText
              : t('Net sales for selected period'),
            icon: CircleDollarSign,
            iconTone: 'success' as const,
            loading: revenueState.status === 'loading',
            error:
              revenueState.status === 'error' ||
              revenueState.status === 'range',
          },
          {
            key: 'knownCost',
            title: t('Known cost'),
            value: knownCostValue,
            fullValue: knownCostValue,
            desc: knownCostDescription,
            icon: Coins,
            iconTone: 'warning' as const,
            loading: revenueState.status === 'loading',
            error:
              revenueState.status === 'error' ||
              revenueState.status === 'range',
          },
          {
            key: 'grossProfit',
            title: t('Estimated gross profit'),
            value: grossProfitValue,
            fullValue: grossProfitValue,
            desc: grossProfitDescription,
            icon: TrendingUp,
            iconTone: 'chart-2' as const,
            loading: revenueState.status === 'loading',
            error:
              revenueState.status === 'error' ||
              revenueState.status === 'range',
          },
          {
            key: 'grossMargin',
            title: t('Gross margin'),
            value: grossMarginValue,
            fullValue: grossMarginValue,
            desc: grossMarginDescription,
            icon: BadgePercent,
            iconTone: 'chart-4' as const,
            loading: revenueState.status === 'loading',
            error:
              revenueState.status === 'error' ||
              revenueState.status === 'range',
          },
        ]
  const allItems = [...items, ...revenueItems]

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='divide-border/60 grid min-w-0 grid-cols-2 divide-x sm:grid-cols-3 lg:grid-cols-5 2xl:grid-cols-9'>
        {allItems.map((it, idx) => {
          const Icon = it.icon
          let valueContent
          if (it.loading) {
            valueContent = (
              <div className='mt-1 flex flex-col gap-1 sm:mt-2 sm:gap-1.5'>
                <Skeleton className='h-5 w-16 sm:h-7 sm:w-20' />
                <Skeleton className='hidden h-3.5 w-28 md:block' />
              </div>
            )
          } else if (it.error) {
            valueContent = (
              <>
                <div className='text-muted-foreground mt-1 font-mono text-base leading-tight font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl sm:leading-normal'>
                  --
                </div>
                <div className='text-muted-foreground/40 mt-1 hidden text-xs md:block'>
                  {it.desc}
                </div>
              </>
            )
          } else {
            valueContent = (
              <>
                <div
                  className='text-foreground mt-1 max-w-full truncate font-mono text-base leading-tight font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl sm:leading-normal'
                  title={it.fullValue}
                >
                  {it.value}
                </div>
                <div className='text-muted-foreground/60 mt-1 hidden text-xs md:block'>
                  {it.desc}
                </div>
              </>
            )
          }

          return (
            <div
              key={it.title}
              className={cn(
                'min-w-0 px-2.5 py-1.5 sm:px-5 sm:py-4',
                idx === items.length - 1 &&
                  items.length % 2 !== 0 &&
                  'col-span-2 sm:col-span-1'
              )}
            >
              <div className='flex min-w-0 items-center gap-1.5 sm:gap-2'>
                <IconBadge
                  tone={it.iconTone}
                  size='stat'
                  className='size-4 rounded-sm sm:size-7 sm:rounded-md [&>svg]:size-2.5 sm:[&>svg]:size-3.5'
                >
                  <Icon />
                </IconBadge>
                <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium tracking-wide uppercase sm:text-xs sm:tracking-wider'>
                  {it.title}
                </div>
              </div>

              {valueContent}
            </div>
          )
        })}
      </div>
    </div>
  )
}
