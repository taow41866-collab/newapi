import { createFileRoute } from '@tanstack/react-router'

import { MySubscriptions } from '@/features/my-subscriptions'

export const Route = createFileRoute('/_authenticated/my-subscriptions/')({
  component: MySubscriptions,
})
