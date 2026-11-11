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
import { expect, it } from 'vitest'

import { formatCacheHitRate } from '../format'

it.each([
  { cacheRead: 600, input: 1000, expected: '60.0%' },
  { cacheRead: 0, input: 1000, expected: '0.0%' },
  { cacheRead: 1, input: 3, expected: '33.3%' },
])(
  'formats $cacheRead of $input input tokens as $expected',
  ({ cacheRead, input, expected }) => {
    expect(formatCacheHitRate(cacheRead, input)).toBe(expected)
  }
)

it('shows a dash when no consume log reported input tokens', () => {
  expect(formatCacheHitRate(0, 0)).toBe('-')
  expect(formatCacheHitRate(undefined, undefined)).toBe('-')
})

it('caps the rate at 100% for inconsistent cache and input totals', () => {
  expect(formatCacheHitRate(1500, 1000)).toBe('100.0%')
})

it('treats a missing cache-read total as zero', () => {
  expect(formatCacheHitRate(undefined, 1000)).toBe('0.0%')
})
