import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { appendPurchasePriceRule, getPurchasePrices } from '../../api'
import type { PurchasePriceRule } from '../../types'

type PurchasePriceUnit = PurchasePriceRule['unit']

export function ChannelCostConfiguration(props: { channelId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [model, setModel] = useState('')
  const [unit, setUnit] = useState<PurchasePriceUnit>('model_multiplier')
  const [unitPrice, setUnitPrice] = useState('')
  const [inputPrice, setInputPrice] = useState('')
  const [outputPrice, setOutputPrice] = useState('')
  const [cachePrice, setCachePrice] = useState('')
  const [cacheWritePrice, setCacheWritePrice] = useState('')
  const [error, setError] = useState('')
  const prices = useQuery({
    queryKey: ['channel-cost-rules'],
    queryFn: getPurchasePrices,
    staleTime: 60_000,
  })
  const savePrices = useMutation({
    mutationFn: appendPurchasePriceRule,
    onSuccess: async () => {
      setModel('')
      setUnitPrice('')
      setInputPrice('')
      setOutputPrice('')
      setCachePrice('')
      setCacheWritePrice('')
      setError('')
      await queryClient.invalidateQueries({
        queryKey: ['channel-cost-rules'],
      })
    },
  })

  const currentRules = useMemo(() => {
    const latest = new Map<string, PurchasePriceRule>()
    for (const rule of prices.data?.data ?? []) {
      if (rule.channel_id !== props.channelId) continue
      const ruleKey = `${rule.model}/${rule.unit}`
      const existing = latest.get(ruleKey)
      if (!existing || rule.effective_at > existing.effective_at) {
        latest.set(ruleKey, rule)
      }
    }
    return [...latest.values()].sort((a, b) => a.model.localeCompare(b.model))
  }, [prices.data?.data, props.channelId])

  async function saveCostRule() {
    const normalizedModel = model.trim()
    if (
      !normalizedModel ||
      (normalizedModel === '*' && unit !== 'model_multiplier')
    ) {
      setError(t('Channel defaults only support model cost multipliers'))
      return
    }

    let newRule: Omit<PurchasePriceRule, 'effective_at' | 'source'>
    if (unit === 'tokens') {
      const input = Number(inputPrice)
      const output = Number(outputPrice)
      const cache = cachePrice.trim() ? Number(cachePrice) : undefined
      const cacheWrite = cacheWritePrice.trim()
        ? Number(cacheWritePrice)
        : undefined
      if (
        !inputPrice.trim() ||
        !outputPrice.trim() ||
        !Number.isFinite(input) ||
        !Number.isFinite(output) ||
        input < 0 ||
        output < 0 ||
        (cache !== undefined && (!Number.isFinite(cache) || cache < 0)) ||
        (cacheWrite !== undefined &&
          (!Number.isFinite(cacheWrite) || cacheWrite < 0))
      ) {
        setError(t('Enter valid non-negative input and output prices'))
        return
      }
      newRule = {
        channel_id: props.channelId,
        model: normalizedModel,
        unit,
        input_price: input,
        output_price: output,
        ...(cache !== undefined ? { cache_price: cache } : {}),
        ...(cacheWrite !== undefined ? { cache_write_price: cacheWrite } : {}),
      }
    } else {
      const value = Number(unitPrice)
      if (!unitPrice.trim() || !Number.isFinite(value) || value < 0) {
        setError(t('Enter a non-negative upstream price'))
        return
      }
      newRule = {
        channel_id: props.channelId,
        model: normalizedModel,
        unit,
        unit_price: value,
      }
    }

    const nextRule: PurchasePriceRule = {
      ...newRule,
      effective_at: 0,
      source:
        unit === 'model_multiplier'
          ? 'channel cost estimate'
          : 'channel purchase price',
    }

    setError('')
    try {
      await savePrices.mutateAsync(nextRule)
    } catch {
      setError(t('Unable to save channel cost rule'))
    }
  }

  let unitPriceLabel = t('Upstream price per second')
  if (unit === 'model_multiplier') {
    unitPriceLabel = t('Upstream cost multiplier')
  } else if (unit === 'request') {
    unitPriceLabel = t('Upstream price per request')
  } else if (unit === 'image') {
    unitPriceLabel = t('Upstream price per image')
  }

  return (
    <section aria-labelledby='channel-cost-heading' className='space-y-4'>
      <div>
        <h3 id='channel-cost-heading' className='text-sm font-semibold'>
          {t('Upstream cost')}
        </h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Cost settings do not affect user charges. Multipliers estimate cost from the saved New API model price; 0.75 means 75% of that price, not an upstream invoice. Exact purchase prices use USD and take precedence. Use * for a channel default multiplier; exact model settings override it. Changes apply to future requests only.'
          )}
        </p>
      </div>

      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4 xl:items-end'>
        <label className='grid gap-1.5 text-sm'>
          <span>{t('Model or * for channel default')}</span>
          <Input
            value={model}
            onChange={(event) => setModel(event.target.value)}
            placeholder='*'
            aria-label={t('Model or * for channel default')}
          />
        </label>
        <div className='grid gap-1.5 text-sm'>
          <span>{t('Cost pricing method')}</span>
          <Select
            value={unit}
            onValueChange={(value) => setUnit(value as PurchasePriceUnit)}
          >
            <SelectTrigger aria-label={t('Cost pricing method')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='model_multiplier'>
                {t('Model multiplier estimate')}
              </SelectItem>
              <SelectItem value='tokens'>{t('Token prices')}</SelectItem>
              <SelectItem value='request'>{t('Per request')}</SelectItem>
              <SelectItem value='image'>{t('Per image')}</SelectItem>
              <SelectItem value='second'>{t('Per second')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {unit === 'tokens' ? (
          <>
            <label className='grid gap-1.5 text-sm'>
              <span>{t('Input price per million tokens')}</span>
              <Input
                type='number'
                min='0'
                step='any'
                value={inputPrice}
                onChange={(event) => setInputPrice(event.target.value)}
                aria-label={t('Input price per million tokens')}
              />
            </label>
            <label className='grid gap-1.5 text-sm'>
              <span>{t('Output price per million tokens')}</span>
              <Input
                type='number'
                min='0'
                step='any'
                value={outputPrice}
                onChange={(event) => setOutputPrice(event.target.value)}
                aria-label={t('Output price per million tokens')}
              />
            </label>
            <label className='grid gap-1.5 text-sm'>
              <span>{t('Cache read price per million tokens')}</span>
              <Input
                type='number'
                min='0'
                step='any'
                value={cachePrice}
                onChange={(event) => setCachePrice(event.target.value)}
                aria-label={t('Cache read price per million tokens')}
              />
            </label>
            <label className='grid gap-1.5 text-sm'>
              <span>{t('Cache write price per million tokens')}</span>
              <Input
                type='number'
                min='0'
                step='any'
                value={cacheWritePrice}
                onChange={(event) => setCacheWritePrice(event.target.value)}
                aria-label={t('Cache write price per million tokens')}
              />
            </label>
          </>
        ) : (
          <label className='grid gap-1.5 text-sm'>
            <span>{unitPriceLabel}</span>
            <Input
              type='number'
              min='0'
              step='any'
              value={unitPrice}
              onChange={(event) => setUnitPrice(event.target.value)}
              aria-label={unitPriceLabel}
            />
          </label>
        )}
        <Button
          type='button'
          disabled={prices.isLoading || prices.isError || savePrices.isPending}
          onClick={() => void saveCostRule()}
        >
          {savePrices.isPending ? t('Saving...') : t('Save upstream cost')}
        </Button>
      </div>

      {error || prices.isError ? (
        <p role='alert' className='text-destructive text-sm'>
          {error || t('Unable to load channel cost rules')}
        </p>
      ) : null}

      {prices.isLoading ? (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      ) : null}
      {!prices.isLoading && currentRules.length > 0 ? (
        <ul className='divide-y rounded-md border text-sm'>
          {currentRules.map((rule) => (
            <li
              key={`${rule.channel_id}-${rule.model}-${rule.unit}`}
              className='flex min-w-0 items-center justify-between gap-3 px-3 py-2'
            >
              <span className='truncate font-mono'>{rule.model}</span>
              <span className='shrink-0 tabular-nums'>
                {formatPurchasePrice(rule)}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      {!prices.isLoading && currentRules.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No upstream cost rules configured')}
        </p>
      ) : null}
    </section>
  )
}

function formatPurchasePrice(rule: PurchasePriceRule) {
  if (rule.unit === 'tokens') {
    const prices = [`${rule.input_price} / ${rule.output_price}`]
    if (rule.cache_price != null) prices.push(`cache ${rule.cache_price}`)
    if (rule.cache_write_price != null) {
      prices.push(`cache write ${rule.cache_write_price}`)
    }
    return `${prices.join(' / ')} USD / 1M`
  }
  if (rule.unit === 'model_multiplier') {
    return `${rule.unit_price}x`
  }
  return `${rule.unit_price} USD / ${rule.unit}`
}
