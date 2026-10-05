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
import { describe, expect, it } from 'vitest'

import {
  currentCustomerDiscounts,
  customerDiscountFormSchema,
  changedCustomerDiscounts,
  customerDiscountEditorSchema,
} from '../customer-discounts'

const history = [
  {
    channel_id: 1,
    model: '*',
    multiplier: 0.7,
    disabled: false,
    version: 1,
    effective_at: 100,
    actor_id: 1,
  },
  {
    channel_id: 1,
    model: '*',
    multiplier: 0.6,
    disabled: false,
    version: 2,
    effective_at: 101,
    actor_id: 1,
  },
  {
    channel_id: 2,
    model: 'model-a',
    multiplier: 0.8,
    disabled: false,
    version: 1,
    effective_at: 100,
    actor_id: 1,
  },
]

describe('customer channel discount editor', () => {
  it('shows only the latest active rule for each channel and model', () => {
    expect(currentCustomerDiscounts(history)).toEqual([history[1], history[2]])
    expect(
      currentCustomerDiscounts([
        ...history,
        { ...history[2], disabled: true, version: 3 },
      ])
    ).toEqual([history[1]])
  })

  it('appends only changed rules and tombstones removed rules', () => {
    expect(
      changedCustomerDiscounts(currentCustomerDiscounts(history), [
        { channel_id: 1, model: '*', multiplier: 0.5 },
        { channel_id: 3, model: '*', multiplier: 0.9 },
      ])
    ).toEqual([
      { channel_id: 2, model: 'model-a', multiplier: 0.8, disabled: true },
      { channel_id: 1, model: '*', multiplier: 0.5, disabled: false },
      { channel_id: 3, model: '*', multiplier: 0.9, disabled: false },
    ])
    expect(
      changedCustomerDiscounts(
        currentCustomerDiscounts(history),
        currentCustomerDiscounts(history)
      )
    ).toEqual([])
  })

  it.each([0, -0.1, 1.1, Infinity, Number.NaN])(
    'rejects invalid multiplier %s',
    (multiplier) => {
      expect(
        customerDiscountFormSchema.safeParse({
          rules: [{ channel_id: 1, model: '*', multiplier }],
        }).success
      ).toBe(false)
    }
  )

  it('accepts more than five channels without combining exact and default rules', () => {
    const rules = Array.from({ length: 6 }, (_, index) => ({
      channel_id: index + 1,
      model: '*',
      multiplier: 0.7,
    }))
    rules.push({ channel_id: 1, model: 'model-a', multiplier: 0.5 })
    expect(customerDiscountFormSchema.parse({ rules }).rules).toHaveLength(7)
  })

  it('rejects duplicate keys, wildcard patterns, oversized models and excessive batches', () => {
    const rule = { channel_id: 1, model: '*', multiplier: 0.7 }
    expect(
      customerDiscountFormSchema.safeParse({ rules: [rule, rule] }).success
    ).toBe(false)
    for (const model of ['', 'model-*', 'model?', 'a'.repeat(256)]) {
      expect(
        customerDiscountFormSchema.safeParse({ rules: [{ ...rule, model }] })
          .success
      ).toBe(false)
    }
    expect(
      customerDiscountFormSchema.safeParse({
        rules: Array.from({ length: 1001 }, (_, index) => ({
          ...rule,
          channel_id: index + 1,
        })),
      }).success
    ).toBe(false)
  })

  it('allows more than 100 unchanged current rules but limits each append batch to 100', () => {
    const rules = Array.from({ length: 101 }, (_, index) => ({
      ...history[0],
      channel_id: index + 1,
    }))
    const schema = customerDiscountEditorSchema(rules, rules.length)
    expect(schema.safeParse({ rules }).success).toBe(true)
    expect(
      schema.safeParse({
        rules: rules.map((rule) => ({ ...rule, multiplier: 0.5 })),
      }).success
    ).toBe(false)
    expect(
      customerDiscountEditorSchema([], 0).safeParse({ rules }).success
    ).toBe(false)
  })

  it('rejects appends when the historical 1000-rule limit would be exceeded', () => {
    expect(
      customerDiscountEditorSchema(history, 1000).safeParse({
        rules: [{ ...history[0], multiplier: 0.4 }],
      }).success
    ).toBe(false)
  })
})
