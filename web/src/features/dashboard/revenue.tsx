import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import * as React from 'react'
import { PanelWrapper } from './components/ui/panel-wrapper'
import { getPurchasePrices, getRevenueReport, updatePurchasePrices, type PurchasePriceRule } from './api'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

export function RevenueDashboard() {
  const { t, i18n } = useTranslation()
  const quotaPerUnit = DEFAULT_CURRENCY_CONFIG.quotaPerUnit
  const end = Math.floor(Date.now() / 1000)
  const start = end - 30 * 86400
  const query = useQuery({ queryKey: ['dashboard', 'revenue', start, end], queryFn: () => getRevenueReport({ start_timestamp: start, end_timestamp: end }), staleTime: 60_000 })
  const prices = useQuery({ queryKey: ['dashboard', 'revenue', 'prices'], queryFn: getPurchasePrices })
  const queryClient = useQueryClient()
  const savePrices = useMutation({ mutationFn: updatePurchasePrices, onSuccess: () => queryClient.invalidateQueries({ queryKey: ['dashboard', 'revenue'] }) })
  const report = query.data?.data
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return <PanelWrapper title={t('Revenue and margin')} description={t('Last 30 days. Sales are net quota charges; profit is estimated only where purchase prices and usage are complete.')} loading={query.isLoading} empty={!report} emptyMessage={query.error ? t('Unable to load revenue') : t('No data available')} height='h-auto'>
    {report ? <div className='space-y-4 p-4 sm:p-5'>
      <div className='grid gap-3 sm:grid-cols-4'>
        <Metric label={t('Net sales')} value={formatBillingCurrencyFromUSD(report.net_sales)} />
        <Metric label={t('Known cost')} value={formatBillingCurrencyFromUSD(report.known_cost)} />
        <Metric label={t('Estimated gross profit')} value={report.gross_profit == null ? t('Incomplete cost data') : formatBillingCurrencyFromUSD(report.gross_profit)} />
        <Metric label={t('Uncovered entries')} value={formatNumber(report.uncovered_entries, locale)} />
      </div>
      <div className='overflow-x-auto'>
        <table className='w-full text-sm'><thead><tr className='border-b text-left'><th className='p-2'>{t('Channel')}</th><th className='p-2'>{t('Model')}</th><th className='p-2'>{t('Net sales')}</th><th className='p-2'>{t('Cost')}</th><th className='p-2'>{t('Profit')}</th><th className='p-2'>{t('Pending')}</th></tr></thead><tbody>{report.rows.map((row) => <tr key={`${row.channel_id}-${row.model}`} className='border-b'><td className='p-2'>{row.channel_id}</td><td className='p-2'>{row.model}</td><td className='p-2'>{formatBillingCurrencyFromUSD(row.net_sales)}</td><td className='p-2'>{row.cost == null ? t('Unknown') : formatBillingCurrencyFromUSD(row.cost)}</td><td className='p-2'>{row.gross_profit == null ? t('Unknown') : formatBillingCurrencyFromUSD(row.gross_profit)}</td><td className='p-2'>{formatNumber(row.pending_entries, locale)}</td></tr>)}</tbody></table>
      </div>
      <p className='text-muted-foreground text-xs'>{t('Quota unit: {{value}} per USD. Pending and incomplete records are excluded from profit.', { value: quotaPerUnit })}</p>
      <PurchasePriceEditor rules={prices.data?.data ?? []} saving={savePrices.isPending} onSave={(rules) => savePrices.mutate(rules)} />
    </div> : null}
  </PanelWrapper>
}

function Metric(props: { label: string; value: string }) { return <div className='rounded-md border p-3'><div className='text-muted-foreground text-xs'>{props.label}</div><div className='mt-1 text-lg font-semibold'>{props.value}</div></div> }

function PurchasePriceEditor({ rules, saving, onSave }: { rules: PurchasePriceRule[]; saving: boolean; onSave: (rules: PurchasePriceRule[]) => void }) {
  const { t } = useTranslation()
  const [draft, setDraft] = React.useState(JSON.stringify(rules, null, 2))
  React.useEffect(() => setDraft(JSON.stringify(rules, null, 2)), [rules])
  return <div className='space-y-2 border-t pt-4'>
    <div className='text-sm font-semibold'>{t('Purchase price rules')}</div>
    <p className='text-muted-foreground text-xs'>{t('Root users can edit JSON rules. Prices use USD per unit; token prices are per million tokens.')}</p>
    <Textarea value={draft} onChange={(e) => setDraft(e.target.value)} className='min-h-40 font-mono text-xs' aria-label={t('Purchase price rules')} />
    <Button disabled={saving} onClick={() => { try { const parsed = JSON.parse(draft); if (!Array.isArray(parsed)) throw new Error(); onSave(parsed) } catch { /* server remains unchanged on malformed input */ } }}>{saving ? t('Saving...') : t('Save purchase prices')}</Button>
  </div>
}
