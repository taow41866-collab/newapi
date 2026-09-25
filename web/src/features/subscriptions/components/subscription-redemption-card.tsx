import { KeyRound, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TitledCard } from '@/components/ui/titled-card'
import { handleServerError } from '@/lib/handle-server-error'

import { redeemSubscriptionCode } from '../api'

interface SubscriptionRedemptionCardProps {
  onRedeemed?: () => void | Promise<void>
}

export function SubscriptionRedemptionCard({
  onRedeemed,
}: SubscriptionRedemptionCardProps) {
  const { t } = useTranslation()
  const [code, setCode] = useState('')
  const [isRedeeming, setIsRedeeming] = useState(false)

  const handleRedeem = async () => {
    const normalizedCode = code.trim()
    if (!normalizedCode || isRedeeming) return

    setIsRedeeming(true)
    try {
      const response = await redeemSubscriptionCode(normalizedCode)
      if (!response.success || !response.data) {
        handleServerError(response, t('Subscription redemption failed'))
        return
      }
      setCode('')
      toast.success(t('Subscription redeemed successfully'))
      await onRedeemed?.()
    } catch (error) {
      handleServerError(error, t('Subscription redemption failed'))
    } finally {
      setIsRedeeming(false)
    }
  }

  return (
    <TitledCard
      title={t('Redeem subscription card')}
      description={t('Enter a subscription card code to activate your plan')}
      icon={<KeyRound className='h-4 w-4' />}
      iconTone='info'
      disableHoverEffect
    >
      <div className='flex flex-col gap-2 sm:flex-row'>
        <Input
          aria-label={t('Subscription card code')}
          value={code}
          onChange={(event) => setCode(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void handleRedeem()
          }}
          placeholder={t('Enter subscription card code')}
          autoComplete='off'
          className='min-w-0 flex-1'
        />
        <Button
          onClick={() => void handleRedeem()}
          disabled={isRedeeming || !code.trim()}
          className='sm:shrink-0'
        >
          {isRedeeming && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
          {t('Redeem subscription card')}
        </Button>
      </div>
    </TitledCard>
  )
}
