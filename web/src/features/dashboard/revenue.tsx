import { useQuery } from '@tanstack/react-query'
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { getRevenueReport } from './api'
import { PanelWrapper } from './components/ui/panel-wrapper'

export function RevenueDashboard() {
  const { t, i18n } = useTranslation()
  const quotaPerUnit = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const [end] = React.useState(() => Math.floor(Date.now() / 1000))
  const start = end - 30 * 86400
  const query = useQuery({
    queryKey: ['dashboard', 'revenue', start, end],
    queryFn: () =>
      getRevenueReport({ start_timestamp: start, end_timestamp: end }),
    staleTime: 60_000,
  })
  const report = query.data?.data
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <PanelWrapper
      title={t('Revenue and margin')}
      description={t(
        'Last 30 days. Sales are net quota charges; profit is estimated only where purchase prices and usage are complete.'
      )}
      loading={query.isLoading}
      empty={!report}
      emptyMessage={
        query.error ? t('Unable to load revenue') : t('No data available')
      }
      height='h-auto'
    >
      {report ? (
        <div className='space-y-4 p-4 sm:p-5'>
          <div className='grid gap-3 sm:grid-cols-3 lg:grid-cols-6'>
            <Metric
              label={t('Net sales')}
              value={formatBillingCurrencyFromUSD(report.net_sales)}
            />
            <Metric
              label={t('Known cost')}
              value={formatBillingCurrencyFromUSD(report.known_cost)}
            />
            <Metric
              label={t('Estimated gross profit')}
              value={
                report.gross_profit == null
                  ? t('Incomplete cost data')
                  : formatBillingCurrencyFromUSD(report.gross_profit)
              }
            />
            <Metric
              label={t('Gross margin')}
              value={
                report.gross_margin_rate == null
                  ? t('Unknown')
                  : formatPercent(report.gross_margin_rate, locale)
              }
            />
            <Metric
              label={t('Uncovered entries')}
              value={formatNumber(report.uncovered_entries, locale)}
            />
            <Metric
              label={t('Estimated cost entries')}
              value={formatNumber(report.estimated_cost_entries ?? 0, locale)}
            />
          </div>
          <div className='overflow-x-auto'>
            <table className='w-full text-sm'>
              <thead>
                <tr className='border-b text-left'>
                  <th className='p-2'>{t('Channel')}</th>
                  <th className='p-2'>{t('Model')}</th>
                  <th className='p-2'>{t('Net sales')}</th>
                  <th className='p-2'>{t('Cost')}</th>
                  <th className='p-2'>{t('Profit')}</th>
                  <th className='p-2'>{t('Gross margin')}</th>
                  <th className='p-2'>{t('Estimated cost entries')}</th>
                  <th className='p-2'>{t('Pending')}</th>
                </tr>
              </thead>
              <tbody>
                {report.rows.map((row) => (
                  <tr
                    key={`${row.channel_id}-${row.model}`}
                    className='border-b'
                  >
                    <td className='p-2'>{row.channel_id}</td>
                    <td className='p-2'>{row.model}</td>
                    <td className='p-2'>
                      {formatBillingCurrencyFromUSD(row.net_sales)}
                    </td>
                    <td className='p-2'>
                      {row.cost == null
                        ? t('Unknown')
                        : formatBillingCurrencyFromUSD(row.cost)}
                    </td>
                    <td className='p-2'>
                      {row.gross_profit == null
                        ? t('Unknown')
                        : formatBillingCurrencyFromUSD(row.gross_profit)}
                    </td>
                    <td className='p-2'>
                      {row.gross_margin_rate == null
                        ? t('Unknown')
                        : formatPercent(row.gross_margin_rate, locale)}
                    </td>
                    <td className='p-2'>
                      {formatNumber(row.estimated_cost_entries ?? 0, locale)}
                    </td>
                    <td className='p-2'>
                      {formatNumber(row.pending_entries, locale)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Quota unit: {{value}} per USD. Pending and incomplete records are excluded from profit.',
              { value: quotaPerUnit }
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Configure estimated upstream costs in each channel. Exact purchase prices take precedence.'
            )}
          </p>
        </div>
      ) : null}
    </PanelWrapper>
  )
}

function Metric(props: { label: string; value: string }) {
  return (
    <div className='rounded-md border p-3'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='mt-1 text-lg font-semibold'>{props.value}</div>
    </div>
  )
}

function formatPercent(value: number, locale: string | undefined) {
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 2,
  }).format(value)
}
