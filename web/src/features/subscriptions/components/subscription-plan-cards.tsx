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
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuota } from '@/lib/format'

import { formatDuration, formatResetPeriod } from '../lib/format'
import type { PlanRecord, SubscriptionPlan } from '../types'

export type PlanPeriod = 'day' | 'week' | 'month' | 'year' | 'other'

function getPlanPeriod(plan: SubscriptionPlan): PlanPeriod {
  if (plan.duration_unit === 'day' && plan.duration_value === 1) {
    return 'day'
  }
  if (plan.duration_unit === 'day' && plan.duration_value === 7) {
    return 'week'
  }
  if (
    (plan.duration_unit === 'month' && plan.duration_value === 1) ||
    (plan.duration_unit === 'day' && plan.duration_value === 30)
  ) {
    return 'month'
  }
  if (
    (plan.duration_unit === 'year' && plan.duration_value === 1) ||
    (plan.duration_unit === 'month' && plan.duration_value === 12)
  ) {
    return 'year'
  }
  return 'other'
}

interface Props {
  plans: PlanRecord[]
  canCreate: boolean
  onCreate: (period: PlanPeriod) => void
  renderActions: (record: PlanRecord) => ReactNode
}

function periodDays(plan: SubscriptionPlan) {
  if (plan.duration_unit === 'day') return plan.duration_value
  if (plan.duration_unit === 'month') return plan.duration_value * 30
  if (plan.duration_unit === 'year') return plan.duration_value * 365
  return 1
}

function formatTokens(value: number, locale: string) {
  if (locale.startsWith('zh')) {
    if (value >= 100_000_000) {
      return `${Number((value / 100_000_000).toFixed(2))} 亿`
    }
    if (value >= 10_000) {
      return `${Number((value / 10_000).toFixed(2))} 万`
    }
  }
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value)
}

function periodLabel(
  period: PlanPeriod,
  t: ReturnType<typeof useTranslation>['t']
) {
  return {
    day: t('Day pass'),
    week: t('Week pass'),
    month: t('Month pass'),
    year: t('Year pass'),
    other: t('Other periods'),
  }[period]
}

export function SubscriptionPlanCards(props: Props) {
  const { t, i18n } = useTranslation()
  const [period, setPeriod] = useState<PlanPeriod>('day')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) || 'en-US'
  const groups = useMemo(() => {
    const available = new Set(props.plans.map(({ plan }) => getPlanPeriod(plan)))
    return [
      'day',
      'week',
      'month',
      ...(available.has('year') ? ['year' as const] : []),
      ...(available.has('other') ? ['other' as const] : []),
    ]
  }, [props.plans])
  const activePeriod = groups.includes(period) ? period : 'day'
  const records = props.plans
    .filter(({ plan }) => getPlanPeriod(plan) === activePeriod)
    .sort(
      (a, b) => a.plan.sort_order - b.plan.sort_order || a.plan.id - b.plan.id
    )
  const model = props.plans.find(
    ({ plan }) => plan.billing_policy === 'ds-flash-v1' && plan.service_model
  )?.plan.service_model

  return (
    <div className='flex h-full min-h-0 flex-col gap-4'>
      <div className='flex flex-wrap items-end justify-between gap-4 border-b pb-4'>
        <div>
          <p className='text-sm font-medium'>{t('Subscription plans')}</p>
          <p className='text-muted-foreground mt-1 text-xs'>
            {model || t('Administrator preview')}
          </p>
        </div>
        <div className='flex items-center gap-3'>
          <div
            className='bg-muted/60 flex items-center gap-1 rounded-lg p-1'
            role='tablist'
            aria-label={t('Plan period')}
          >
            {(['day', 'week', 'month'] as PlanPeriod[]).map((item) => (
              <Button
                key={item}
                role='tab'
                aria-selected={activePeriod === item}
                variant={activePeriod === item ? 'secondary' : 'ghost'}
                size='sm'
                className='rounded-md px-3'
                onClick={() => setPeriod(item)}
              >
                {periodLabel(item, t)}
              </Button>
            ))}
          </div>
          {groups.some((item) => item === 'year' || item === 'other') && (
            <NativeSelect
              aria-label={t('Additional periods')}
              value={activePeriod === 'year' || activePeriod === 'other' ? activePeriod : ''}
              onChange={(event) => setPeriod(event.target.value as PlanPeriod)}
            >
              <NativeSelectOption value=''>{t('More')}</NativeSelectOption>
              {groups
                .filter((item) => item === 'year' || item === 'other')
                .map((item) => (
                  <NativeSelectOption key={item} value={item}>
                    {periodLabel(item, t)}
                  </NativeSelectOption>
                ))}
            </NativeSelect>
          )}
        </div>
      </div>

      <div className='min-h-0 flex-1 overflow-y-auto p-1'>
        <section
          role='region'
          aria-label={t('Subscription plans')}
          className='min-w-0'
        >
          <div className='mb-4 flex items-center gap-3'>
            <h2 className='shrink-0 text-base font-semibold'>
              {periodLabel(activePeriod, t)}
            </h2>
            <span className='bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs tabular-nums'>
              {records.length}
            </span>
            <div className='bg-border h-px flex-1' />
          </div>

          {records.length === 0 ? (
            <div className='bg-muted/20 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-dashed px-5 py-5'>
              <div>
                <p className='font-medium'>{t('No subscription plans yet')}</p>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('Click "Create Plan" to create your first subscription plan')}
                </p>
              </div>
              <Button
                variant='outline'
                disabled={!props.canCreate}
                onClick={() => props.onCreate(activePeriod)}
              >
                {t('Create Plan')}
              </Button>
            </div>
          ) : (
            <div className='grid grid-cols-1 gap-4 lg:grid-cols-3'>
              {records.map((record) => {
                const plan = record.plan
                const isV1 = plan.billing_policy === 'ds-flash-v1'
                const dailyTotal =
                  (plan.daily_input_token_limit || 0) +
                  (plan.daily_output_token_limit || 0)
                const cycleTotal = dailyTotal * periodDays(plan)
                const details = isV1
                  ? [
                      [
                        t('Daily input tokens'),
                        formatTokens(plan.daily_input_token_limit || 0, locale),
                      ],
                      [
                        t('Daily output tokens'),
                        formatTokens(plan.daily_output_token_limit || 0, locale),
                      ],
                    ]
                  : [
                      [t('Validity'), formatDuration(plan, t)],
                      [t('Quota Reset'), formatResetPeriod(plan, t)],
                      [
                        t('Plan Quota'),
                        plan.total_amount > 0
                          ? formatQuota(plan.total_amount)
                          : t('Unlimited'),
                      ],
                    ]
                return (
                  <article
                    key={plan.id}
                    aria-label={plan.title}
                    className='bg-card min-w-0 rounded-lg border p-5 shadow-xs'
                  >
                    <div className='flex items-start justify-between gap-3'>
                      <div className='min-w-0'>
                        <h3 className='truncate font-semibold'>{plan.title}</h3>
                        {plan.subtitle && (
                          <p className='text-muted-foreground mt-1 text-xs'>
                            {plan.subtitle}
                          </p>
                        )}
                      </div>
                      <div className='flex items-center gap-2'>
                        <StatusBadge
                          label={plan.enabled ? t('Enable') : t('Disable')}
                          variant={plan.enabled ? 'success' : 'neutral'}
                          copyable={false}
                        />
                      </div>
                    </div>
                    <div className='mt-5 flex items-baseline gap-1'>
                      <span className='text-primary text-3xl font-bold tabular-nums'>
                        {plan.currency === 'CNY' ? '¥' : plan.currency}{' '}
                        {new Intl.NumberFormat(locale, {
                          minimumFractionDigits: 2,
                          maximumFractionDigits: 2,
                        }).format(plan.price_amount)}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        / {formatDuration(plan, t)}
                      </span>
                    </div>
                    {isV1 ? (
                      <>
                        <div className='bg-muted/50 mt-4 rounded-lg px-3 py-3 text-sm'>
                          <span className='text-muted-foreground'>{t('Cycle quota')}</span>{' '}
                          <strong className='ml-1'>
                            {formatTokens(cycleTotal, locale)} Token
                          </strong>
                        </div>
                        <dl className='mt-4 space-y-2 border-t pt-3'>
                          {details.map(([label, value]) => (
                            <div
                              key={label}
                              className='flex items-center justify-between gap-3 text-sm'
                            >
                              <dt className='text-muted-foreground'>{label}</dt>
                              <dd className='font-medium tabular-nums'>{value}</dd>
                            </div>
                          ))}
                        </dl>
                        <p className='text-muted-foreground mt-4 text-xs'>
                          {plan.service_model || t('Not configured')}
                        </p>
                      </>
                    ) : (
                      <dl className='mt-4 space-y-2 border-t pt-3'>
                        {details.map(([label, value]) => (
                          <div
                            key={label}
                            className='flex items-center justify-between gap-3 text-sm'
                          >
                            <dt className='text-muted-foreground'>{label}</dt>
                            <dd className='font-medium tabular-nums'>{value}</dd>
                          </div>
                        ))}
                      </dl>
                    )}
                    <div className='mt-5 flex items-center justify-end gap-2 border-t pt-3'>
                      {props.renderActions(record)}
                    </div>
                  </article>
                )
              })}
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
