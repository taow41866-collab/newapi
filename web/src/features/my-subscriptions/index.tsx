import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { SubscriptionRedemptionCard } from '@/features/subscriptions/components/subscription-redemption-card'
import { SubscriptionPlansCard } from '@/features/wallet/components/subscription-plans-card'
import { useTopupInfo } from '@/features/wallet/hooks'

export function MySubscriptions() {
  const { t } = useTranslation()
  const { topupInfo, loading } = useTopupInfo()
  const [refreshKey, setRefreshKey] = useState(0)
  const refresh = useCallback(() => setRefreshKey((key) => key + 1), [])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('My Subscriptions')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4'>
          <SubscriptionPlansCard
            topupInfo={topupInfo}
            userQuota={0}
            refreshKey={refreshKey}
            onPurchaseSuccess={refresh}
            showAvailablePlans={false}
          />
          {!loading && <SubscriptionRedemptionCard onRedeemed={refresh} />}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
