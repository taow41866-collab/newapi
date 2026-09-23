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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'

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

export function SubscriptionPlanCards(props: Props) {
  const { t, i18n } = useTranslation()
  const [period, setPeriod] = useState('all')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const labels = {
    day: t('Day pass'),
    week: t('Week pass'),
    month: t('Month pass'),
    year: t('Year pass'),
    other: t('Other periods'),
  }
  const groups: PlanPeriod[] = ['day', 'week', 'month']
  if (props.plans.some((record) => getPlanPeriod(record.plan) === 'year')) {
    groups.push('year')
  }
  if (props.plans.some((record) => getPlanPeriod(record.plan) === 'other')) {
    groups.push('other')
  }
  const selected = groups.includes(period as PlanPeriod) ? period : 'all'
  const visibleGroups = groups.filter(
    (group) => selected === 'all' || group === selected
  )

  return (
    <div className='flex h-full min-h-0 flex-col gap-3'>
      <div className='flex flex-wrap items-center justify-between gap-3 border-b pb-3'>
        <div>
          <p className='text-sm font-medium'>{t('Subscription plans')}</p>
          <p className='text-muted-foreground text-xs'>
            {t('Administrator preview')}
          </p>
        </div>
        <NativeSelect
          aria-label={t('Plan period')}
          value={selected}
          onChange={(event) => setPeriod(event.target.value)}
        >
          <NativeSelectOption value='all'>
            {t('All periods')}
          </NativeSelectOption>
          {groups.map((group) => (
            <NativeSelectOption key={group} value={group}>
              {labels[group]}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </div>
      <div className='min-h-0 flex-1 overflow-y-auto p-1'>
        <div role='region' aria-label={t('Subscription plans')} className='flex flex-col gap-5'>
          {visibleGroups.map((group) => {
            const records = props.plans
              .filter((record) => getPlanPeriod(record.plan) === group)
              .sort(
                (a, b) =>
                  b.plan.sort_order - a.plan.sort_order || a.plan.id - b.plan.id
              )
            let gridColumns = ''
            if (records.length > 2) {
              gridColumns = 'xl:grid-cols-3'
            } else if (records.length > 1) {
              gridColumns = 'xl:grid-cols-2'
            }
            return (
              <section
                key={group}
                role='region'
                aria-label={labels[group]}
                className='min-w-0 space-y-2.5'
              >
                <div className='flex items-center gap-3'>
                  <h2 className='shrink-0 text-base font-semibold'>
                    {labels[group]}
                  </h2>
                  <span
                    aria-label={`${records.length}`}
                    className='bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs tabular-nums'
                  >
                    {records.length}
                  </span>
                  <div className='bg-border h-px flex-1' />
                </div>
                <div
                  className={`grid grid-cols-1 gap-3 ${gridColumns}`}
                >
                  {records.length === 0 && (
                    <div className='bg-muted/30 flex flex-wrap items-center justify-between gap-3 rounded-md border border-dashed px-4 py-3'>
                      <p className='text-muted-foreground'>
                        {t('No subscription plans yet')}
                      </p>
                      <Button
                        variant='outline'
                        disabled={!props.canCreate}
                        onClick={() => props.onCreate(group)}
                      >
                        {t('Create Plan')}
                      </Button>
                    </div>
                  )}
                  {records.map((record) => {
                    const plan = record.plan
                    const details = [
                      [t('Validity'), formatDuration(plan, t)],
                      [t('Quota Reset'), formatResetPeriod(plan, t)],
                      [t('Priority'), formatNumber(plan.sort_order, locale)],
                      [
                        t('Upgrade Group'),
                        plan.upgrade_group || t('No Upgrade'),
                      ],
                    ]
                    if (plan.billing_policy === 'ds-flash-v1') {
                      details.push(
                        [
                          t('Daily input tokens'),
                          plan.daily_input_token_limit
                            ? formatNumber(plan.daily_input_token_limit, locale)
                            : t('Not configured'),
                        ],
                        [
                          t('Daily output tokens'),
                          plan.daily_output_token_limit
                            ? formatNumber(
                                plan.daily_output_token_limit,
                                locale
                              )
                            : t('Not configured'),
                        ],
                        [t('Model'), plan.service_model || t('Not configured')]
                      )
                    } else {
                      details.push([
                        t('Plan Quota'),
                        plan.total_amount > 0
                          ? formatQuota(plan.total_amount)
                          : t('Unlimited'),
                      ])
                    }
                    return (
                      <article
                        key={plan.id}
                        aria-label={plan.title}
                        className='bg-card min-w-0 rounded-md border p-4 sm:p-5'
                      >
                        <div className='flex flex-wrap items-start justify-between gap-x-4 gap-y-3'>
                          <div className='min-w-0 flex-1 basis-40 break-words'>
                            <h3 className='font-medium'>{plan.title}</h3>
                            {plan.subtitle && (
                              <p className='text-muted-foreground mt-1 text-sm'>
                                {plan.subtitle}
                              </p>
                            )}
                          </div>
                          <div className='flex min-w-0 flex-wrap items-center justify-end gap-2'>
                            <span className='text-primary text-lg font-semibold tabular-nums'>
                              {plan.currency}{' '}
                              {new Intl.NumberFormat(locale, {
                                minimumFractionDigits: 2,
                                maximumFractionDigits: 2,
                              }).format(plan.price_amount)}
                            </span>
                            <StatusBadge
                              label={plan.enabled ? t('Enable') : t('Disable')}
                              variant={plan.enabled ? 'success' : 'neutral'}
                              copyable={false}
                            />
                            {props.renderActions(record)}
                          </div>
                        </div>
                        <dl className='mt-4 grid grid-cols-2 gap-x-4 gap-y-3 border-t pt-3 md:grid-cols-4'>
                          {details.map(([label, value]) => (
                            <div key={label} className='min-w-0 break-words'>
                              <dt className='text-muted-foreground mb-1 text-xs'>
                                {label}
                              </dt>
                              <dd className='text-sm font-medium tabular-nums'>
                                {value}
                              </dd>
                            </div>
                          ))}
                        </dl>
                      </article>
                    )
                  })}
                </div>
              </section>
            )
          })}
        </div>
      </div>
    </div>
  )
}
