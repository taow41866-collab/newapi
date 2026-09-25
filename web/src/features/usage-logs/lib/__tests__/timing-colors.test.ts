/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, test } from 'vitest'

import {
  getFirstResponseTimeColor,
  getResponseTimeColor,
  getThroughputColor,
  getTimeColor,
} from '../format'

describe('usage timing colors', () => {
  test.each([
    [0, 'success'],
    [4.999, 'success'],
    [5, 'warning'],
    [9.999, 'warning'],
    [10, 'danger'],
  ])(
    'matches the online first-token thresholds at %s seconds',
    (seconds, color) => {
      expect(getFirstResponseTimeColor(seconds)).toBe(color)
    }
  )

  test('keeps duration and throughput green, as in the online baseline', () => {
    expect(getTimeColor(0)).toBe('success')
    expect(getTimeColor(60)).toBe('success')
    expect(getThroughputColor(0)).toBe('success')
    expect(getThroughputColor(1)).toBe('success')
    expect(getThroughputColor(1000)).toBe('success')
    expect(getResponseTimeColor(60, 0)).toBe('success')
    expect(getResponseTimeColor(60, 1000)).toBe('success')
  })
})
