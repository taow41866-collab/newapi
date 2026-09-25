import type { TFunction } from 'i18next'
import { CalendarClock, Gauge } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Progress } from '@/components/ui/progress'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import type {
  PlanRecord,
  SubscriptionPlan,
  UserSubscriptionRecord,
} from '../types'

interface SubscriptionSummaryCardsProps {
  plans: PlanRecord[]
  subscriptions: UserSubscriptionRecord[]
}

function formatTokens(value: number, locale: string) {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(
    Math.max(0, value)
  )
}

function getRemainingDays(endTime: number) {
  return Math.max(0, Math.ceil((endTime - Date.now() / 1000) / 86400))
}

function formatSubscriptionTime(timestamp: number) {
  return timestamp ? dayjs(timestamp * 1000).format('YYYY-MM-DD HH:mm') : '-'
}

function getPeriodLabel(
  plan: Pick<SubscriptionPlan, 'duration_unit' | 'duration_value'> | undefined,
  t: TFunction
) {
  if (!plan) return t('Subscription')
  if (plan.duration_unit === 'day' && plan.duration_value === 1) {
    return t('Day card')
  }
  if (plan.duration_unit === 'day' && plan.duration_value === 7) {
    return t('Week card')
  }
  if (
    (plan.duration_unit === 'day' && plan.duration_value === 30) ||
    (plan.duration_unit === 'month' && plan.duration_value === 1)
  ) {
    return t('Month card')
  }
  const unitLabels = {
    year: t('years'),
    month: t('months'),
    day: t('days'),
    hour: t('hours'),
    custom: t('seconds'),
  }
  return `${plan.duration_value} ${unitLabels[plan.duration_unit] || plan.duration_unit}`
}

function formatPlanPrice(
  plan: Pick<SubscriptionPlan, 'price_amount' | 'currency'> | undefined,
  locale: string
) {
  if (!plan) return '-'
  const currency = plan.currency === 'CNY' ? '¥' : plan.currency
  const amount = new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(Number(plan.price_amount || 0))
  return `${currency} ${amount}`
}

interface QuotaMetricProps {
  label: string
  used: number
  total: number
  locale: string
  format: (value: number, locale: string) => string
  t: TFunction
}

function QuotaMetric({
  label,
  used,
  total,
  locale,
  format,
  t,
}: QuotaMetricProps) {
  const remaining = Math.max(0, total - used)
  const usagePercent = total > 0 ? Math.min(100, (used / total) * 100) : 0

  return (
    <div className='bg-muted/20 rounded-md border px-3 py-2.5'>
      <div className='flex items-center justify-between gap-3 text-xs'>
        <span className='flex items-center gap-1.5 font-medium'>
          <Gauge className='text-primary size-3.5' aria-hidden='true' />
          {label}
        </span>
        <span className='text-muted-foreground tabular-nums'>
          {t('Used')} {format(used, locale)} / {format(total, locale)}
        </span>
      </div>
      <div className='mt-2 flex items-baseline justify-between gap-3'>
        <span className='text-primary text-sm font-semibold tabular-nums'>
          {t('Remaining')}: {format(remaining, locale)}
        </span>
        <span className='text-muted-foreground text-[11px] tabular-nums'>
          {Math.round(usagePercent)}% {t('Used')}
        </span>
      </div>
      <Progress value={usagePercent} className='mt-2 h-1.5' />
    </div>
  )
}

export function SubscriptionSummaryCards({
  plans,
  subscriptions,
}: SubscriptionSummaryCardsProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) || 'en-US'
  const planMap = new Map(plans.map((record) => [record.plan.id, record.plan]))

  if (subscriptions.length === 0) {
    return (
      <p className='text-muted-foreground py-2 text-xs'>
        {t('Subscribe to a plan for model access')}
      </p>
    )
  }

  return (
    <div
      aria-label={t('My Subscriptions')}
      className='grid min-w-0 grid-cols-1 gap-3'
    >
      {subscriptions.map(({ subscription, plan_display }) => {
        const plan = planMap.get(subscription.plan_id)
        const displayPlan = plan_display || plan
        const planTitle =
          displayPlan?.title || `${t('Subscription')} #${subscription.id}`
        const pricePlan =
          subscription.price_amount != null
            ? {
                price_amount: subscription.price_amount,
                currency: subscription.currency || 'USD',
              }
            : plan
        const now = Date.now() / 1000
        const isExpired = subscription.end_time < now
        const isCancelled = subscription.status === 'cancelled'
        const isActive = subscription.status === 'active' && !isExpired
        const isV1 =
          subscription.billing_policy === 'ds-flash-v1' ||
          plan?.billing_policy === 'ds-flash-v1'
        const inputTotal = Number(
          subscription.daily_input_token_limit ??
            plan?.daily_input_token_limit ??
            0
        )
        const outputTotal = Number(
          subscription.daily_output_token_limit ??
            plan?.daily_output_token_limit ??
            0
        )
        const inputUsed = Number(subscription.daily_input_tokens_used || 0)
        const outputUsed = Number(subscription.daily_output_tokens_used || 0)
        const totalAmount = Number(subscription.amount_total || 0)
        const usedAmount = Number(subscription.amount_used || 0)
        let statusLabel = t('Expired')
        if (isActive) {
          statusLabel = t('Active')
        } else if (isCancelled) {
          statusLabel = t('Cancelled')
        }

        let quotaContent: ReactNode
        if (isV1 && (inputTotal > 0 || outputTotal > 0)) {
          quotaContent = (
            <div className='mt-4 space-y-3 border-t pt-3'>
              <p className='text-muted-foreground text-xs font-medium'>
                {t('Daily quota')}
              </p>
              {inputTotal > 0 && (
                <QuotaMetric
                  label={t('Input tokens')}
                  used={inputUsed}
                  total={inputTotal}
                  locale={locale}
                  format={formatTokens}
                  t={t}
                />
              )}
              {outputTotal > 0 && (
                <QuotaMetric
                  label={t('Output tokens')}
                  used={outputUsed}
                  total={outputTotal}
                  locale={locale}
                  format={formatTokens}
                  t={t}
                />
              )}
              {(subscription.service_model || plan?.service_model) && (
                <p className='text-muted-foreground truncate text-[11px]'>
                  {subscription.service_model || plan?.service_model}
                </p>
              )}
            </div>
          )
        } else if (totalAmount > 0) {
          quotaContent = (
            <div className='mt-4 border-t pt-3'>
              <QuotaMetric
                label={t('Total Quota')}
                used={usedAmount}
                total={totalAmount}
                locale={locale}
                format={(value) => formatQuota(value)}
                t={t}
              />
            </div>
          )
        } else {
          quotaContent = (
            <p className='text-muted-foreground mt-4 border-t pt-3 text-xs'>
              {t('Quota details unavailable')}
            </p>
          )
        }

        return (
          <article
            key={subscription.id}
            aria-label={planTitle}
            className={cn(
              'bg-background relative min-w-0 rounded-md border',
              isActive && 'border-primary/60 shadow-xs',
              !isActive && 'opacity-80'
            )}
          >
            <div className='p-4'>
              <div className='flex items-start justify-between gap-3'>
                <div className='min-w-0'>
                  <h3 className='truncate font-semibold'>{planTitle}</h3>
                  {displayPlan?.subtitle && (
                    <p className='text-muted-foreground mt-1 truncate text-xs'>
                      {displayPlan.subtitle}
                    </p>
                  )}
                  <div className='text-muted-foreground mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
                    <span className='text-primary font-medium'>
                      {getPeriodLabel(displayPlan, t)}
                    </span>
                    <span>
                      {t('Price')}: {formatPlanPrice(pricePlan, locale)}
                    </span>
                  </div>
                </div>
                <StatusBadge
                  label={statusLabel}
                  variant={isActive ? 'success' : 'neutral'}
                  copyable={false}
                />
              </div>

              <div className='bg-muted/20 mt-4 grid gap-2 rounded-md border px-3 py-2.5 text-xs'>
                <div className='min-w-0'>
                  <span className='text-muted-foreground flex items-center gap-1.5'>
                    <CalendarClock className='size-3.5' aria-hidden='true' />
                    {t('Purchased at')}
                  </span>
                  <span className='mt-1 block truncate font-medium tabular-nums'>
                    {formatSubscriptionTime(subscription.start_time)}
                  </span>
                </div>
                <div className='min-w-0'>
                  <span className='text-muted-foreground block'>
                    {t('Expires at')}
                  </span>
                  <span className='mt-1 block truncate font-medium tabular-nums'>
                    {formatSubscriptionTime(subscription.end_time)}
                  </span>
                </div>
                <div className='text-muted-foreground flex items-center justify-between gap-2 border-t pt-2'>
                  <span>{isActive ? t('Remaining') : t('Status')}</span>
                  <span className='text-primary font-medium'>
                    {isActive
                      ? t('{{count}} days remaining', {
                          count: getRemainingDays(subscription.end_time),
                        })
                      : statusLabel}
                  </span>
                </div>
              </div>

              {quotaContent}

              {isActive && subscription.next_reset_time ? (
                <p className='text-muted-foreground mt-3 text-[11px]'>
                  {t('Next reset')}:{' '}
                  {new Date(
                    subscription.next_reset_time * 1000
                  ).toLocaleString()}
                </p>
              ) : null}
            </div>
          </article>
        )
      })}
    </div>
  )
}
