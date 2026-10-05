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
import { z } from 'zod'

import type {
  CustomerChannelDiscount,
  CustomerChannelDiscountInput,
} from '../types'

const discountRuleSchema = z.object({
  channel_id: z
    .number()
    .int('Channel ID must be a positive integer')
    .positive('Channel ID must be a positive integer'),
  model: z
    .string()
    .trim()
    .min(1, 'Use an exact model name or *')
    .max(255, 'Model name must not exceed 255 characters')
    .refine((value) => value === '*' || !value.includes('*'), {
      message: 'Use an exact model name or *',
    })
    .refine((value) => value === '*' || !value.includes('?'), {
      message: 'Use an exact model name or *',
    }),
  multiplier: z
    .number()
    .finite()
    .gt(0, 'Discount multiplier must be greater than 0 and at most 1')
    .lte(1, 'Discount multiplier must be greater than 0 and at most 1'),
})

export const customerDiscountFormSchema = z.object({
  rules: z
    .array(discountRuleSchema)
    .max(1000)
    .superRefine((rules, context) => {
      const keys = new Set<string>()
      rules.forEach((rule, index) => {
        const key = `${rule.channel_id}/${rule.model}`
        if (keys.has(key)) {
          context.addIssue({
            code: 'custom',
            path: [index, 'model'],
            message: 'Duplicate channel/model rule',
          })
        }
        keys.add(key)
      })
    }),
})

export type CustomerDiscountFormValues = z.infer<
  typeof customerDiscountFormSchema
>

export function customerDiscountEditorSchema(
  current: CustomerChannelDiscount[],
  historyCount: number
) {
  return customerDiscountFormSchema.superRefine((values, context) => {
    const changes = changedCustomerDiscounts(current, values.rules)
    if (changes.length > 100) {
      context.addIssue({
        code: 'custom',
        path: ['rules', 'root'],
        message: 'Save no more than 100 rule changes at a time',
      })
    }
    if (historyCount + changes.length > 1000) {
      context.addIssue({
        code: 'custom',
        path: ['rules', 'root'],
        message: 'Customer discount history limit reached',
      })
    }
  })
}

export function currentCustomerDiscounts(
  history: CustomerChannelDiscount[]
): CustomerChannelDiscount[] {
  const latest = new Map<string, CustomerChannelDiscount>()
  for (const rule of history) {
    const key = `${rule.channel_id}/${rule.model}`
    const previous = latest.get(key)
    if (!previous || rule.version > previous.version) latest.set(key, rule)
  }
  return [...latest.values()]
    .filter((rule) => !rule.disabled)
    .sort(
      (a, b) => a.channel_id - b.channel_id || a.model.localeCompare(b.model)
    )
}

export function changedCustomerDiscounts(
  current: CustomerChannelDiscount[],
  desired: CustomerChannelDiscountInput[]
): CustomerChannelDiscountInput[] {
  const currentMap = new Map(
    current.map((rule) => [`${rule.channel_id}/${rule.model}`, rule])
  )
  const desiredMap = new Map(
    desired.map((rule) => [`${rule.channel_id}/${rule.model}`, rule])
  )
  const changes: CustomerChannelDiscountInput[] = []
  for (const currentRule of current) {
    const key = `${currentRule.channel_id}/${currentRule.model}`
    const next = desiredMap.get(key)
    if (!next) {
      changes.push({
        channel_id: currentRule.channel_id,
        model: currentRule.model,
        multiplier: currentRule.multiplier,
        disabled: true,
      })
    }
  }
  for (const next of desired) {
    const key = `${next.channel_id}/${next.model}`
    const previous = currentMap.get(key)
    if (!previous || next.multiplier !== previous.multiplier) {
      changes.push({ ...next, disabled: false })
    }
  }
  return changes
}
