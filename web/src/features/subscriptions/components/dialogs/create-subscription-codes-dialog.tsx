import { useQuery } from '@tanstack/react-query'
import { Copy, KeyRound } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { createSubscriptionRedemptions, getAdminPlans } from '../../api'
import {
  getSubscriptionCardKind,
  type SubscriptionCardKind,
} from '../../lib/redemption'
import type { PlanRecord } from '../../types'
import { useSubscriptions } from '../subscriptions-provider'

const kindLabels: Record<SubscriptionCardKind, string> = {
  day: 'Day pass',
  week: 'Week pass',
  month: 'Month pass',
}

export function CreateSubscriptionCodesDialog() {
  const { t } = useTranslation()
  const { open, setOpen } = useSubscriptions()
  const isOpen = open === 'create-codes'
  const [name, setName] = useState('')
  const [count, setCount] = useState('1')
  const [planId, setPlanId] = useState('')
  const [expiredDate, setExpiredDate] = useState('')
  const [codes, setCodes] = useState<string[]>([])
  const [isCreating, setIsCreating] = useState(false)

  const { data: plans = [], isLoading, isError } = useQuery({
    queryKey: ['admin-subscription-card-plans'],
    queryFn: async () => requireServerSuccess(await getAdminPlans()).data || [],
    enabled: isOpen,
  })
  const eligiblePlans = useMemo(
    () =>
      plans.flatMap((record: PlanRecord) => {
        const kind = getSubscriptionCardKind(record.plan)
        return kind ? [{ record, kind }] : []
      }),
    [plans]
  )
  const selectedPlan = eligiblePlans.find(
    ({ record }) => String(record.plan.id) === planId
  ) || (!planId ? eligiblePlans[0] : undefined)
  const parsedCount = Number(count)
  const isCountValid =
    Number.isInteger(parsedCount) && parsedCount >= 1 && parsedCount <= 100

  const closeDialog = () => {
    setName('')
    setCount('1')
    setPlanId('')
    setExpiredDate('')
    setCodes([])
    setOpen(null)
  }

  const handleCreate = async () => {
    const amount = parsedCount
    if (!selectedPlan || name.trim().length < 1 || name.trim().length > 20) {
      return
    }
    if (!Number.isInteger(amount) || amount < 1 || amount > 100) {
      return
    }

    let expiredTime = 0
    if (expiredDate) {
      const expiry = new Date(`${expiredDate}T23:59:59`)
      if (Number.isNaN(expiry.getTime())) return
      expiredTime = Math.floor(expiry.getTime() / 1000)
    }

    setIsCreating(true)
    try {
      const response = await createSubscriptionRedemptions({
        name: name.trim(),
        count: amount,
        plan_id: selectedPlan.record.plan.id,
        kind: selectedPlan.kind,
        expired_time: expiredTime,
      })
      if (!response.success || !response.data?.length) {
        handleServerError(response, t('Failed to create subscription codes'))
        return
      }
      setCodes(response.data)
      toast.success(
        t('Created {{count}} subscription codes', {
          count: response.data.length,
        })
      )
    } catch (error) {
      handleServerError(error, t('Failed to create subscription codes'))
    } finally {
      setIsCreating(false)
    }
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(codes.join('\n'))
      toast.success(t('Copied to clipboard'))
    } catch (error) {
      handleServerError(error, t('Failed to copy'))
    }
  }

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => !nextOpen && closeDialog()}
    >
      <DialogContent className='max-h-[calc(100dvh-2rem)] max-w-xl'>
        <DialogHeader>
          <div className='bg-primary/10 text-primary flex size-9 items-center justify-center rounded-lg'>
            <KeyRound className='size-4' />
          </div>
          <DialogTitle>{t('Generate subscription codes')}</DialogTitle>
          <DialogDescription>
            {t('Subscription codes are separate from wallet redemption codes.')}
          </DialogDescription>
        </DialogHeader>

        {codes.length > 0 ? (
          <div className='space-y-3'>
            <div className='flex items-center justify-between gap-3'>
              <p className='text-sm font-medium'>
                {t('Generated codes')} ({codes.length})
              </p>
              <Button size='sm' variant='outline' onClick={handleCopy}>
                <Copy className='size-4' />
                {t('Copy all codes')}
              </Button>
            </div>
            <div
              aria-label={t('Generated codes')}
              className='bg-muted/40 max-h-64 space-y-2 overflow-y-auto rounded-lg border p-3'
            >
              {codes.map((code) => (
                <code key={code} className='block break-all text-sm'>
                  {code}
                </code>
              ))}
            </div>
          </div>
        ) : (
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='subscription-code-plan'>{t('Plan')}</Label>
              <NativeSelect
                id='subscription-code-plan'
                value={planId}
                onChange={(event) => setPlanId(event.target.value)}
                disabled={isLoading || eligiblePlans.length === 0}
              >
                <NativeSelectOption value='' disabled>
                  {isLoading
                    ? t('Loading')
                    : t('Select subscription plan')}
                </NativeSelectOption>
                {eligiblePlans.map(({ record, kind }) => (
                  <NativeSelectOption
                    key={record.plan.id}
                    value={String(record.plan.id)}
                  >
                    {record.plan.title} · {t(kindLabels[kind])}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
              {isError && (
                <p role='alert' className='text-destructive text-sm'>
                  {t('Failed to load subscription plans')}
                </p>
              )}
              {!isLoading && !isError && eligiblePlans.length === 0 && (
                <p className='text-muted-foreground text-sm'>
                  {t('No eligible V1 plans')}
                </p>
              )}
            </div>
            <div className='space-y-2'>
              <Label htmlFor='subscription-code-name'>{t('Batch name')}</Label>
              <Input
                id='subscription-code-name'
                value={name}
                onChange={(event) => setName(event.target.value)}
                maxLength={20}
                required
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='subscription-code-count'>{t('Quantity')}</Label>
              <Input
                id='subscription-code-count'
                type='number'
                min={1}
                max={100}
                step={1}
                value={count}
                onChange={(event) => setCount(event.target.value)}
                required
              />
            </div>
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='subscription-code-expiry'>
                {t('Expiration date')}{' '}
                <span className='text-muted-foreground'>
                  ({t('Optional')})
                </span>
              </Label>
              <Input
                id='subscription-code-expiry'
                type='date'
                value={expiredDate}
                onChange={(event) => setExpiredDate(event.target.value)}
              />
            </div>
          </div>
        )}

        <DialogFooter>
          {codes.length > 0 ? (
            <Button onClick={closeDialog}>{t('Done')}</Button>
          ) : (
            <Button
              onClick={() => void handleCreate()}
              disabled={
                isCreating ||
                isLoading ||
                isError ||
                !selectedPlan ||
                !name.trim() ||
                !isCountValid
              }
            >
              <KeyRound className='size-4' />
              {isCreating ? t('Generating') : t('Generate codes')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
