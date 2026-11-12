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
import { afterEach, describe, expect, test } from 'vitest'

import { CHART_METRIC_STORAGE_KEY } from '@/features/dashboard/constants'
import type { DashboardChartPreferences } from '@/features/dashboard/types'

import {
  buildDefaultDashboardFilters,
  getFilterRangeDays,
  getGranularityRangeReason,
  getSavedChartMetric,
  isGranularityAllowed,
  nearestAllowedGranularity,
  saveChartMetric,
} from '../filters'

const DAY_MS = 86_400_000

afterEach(() => {
  localStorage.clear()
})

describe('granularity and range compatibility', () => {
  test.each([
    ['hour', 1, true],
    ['hour', 7, true],
    ['hour', 14, false],
    ['hour', 29, false],
    ['day', 1, true],
    ['day', 29, true],
    ['day', 91, false],
    ['week', 7, false],
    ['week', 8, true],
    ['week', 29, true],
  ] as const)(
    'reports %s granularity over %i days as allowed=%s',
    (granularity, days, allowed) => {
      expect(isGranularityAllowed(granularity, days)).toBe(allowed)
    }
  )

  test('never blocks a granularity while the range is unknown', () => {
    expect(isGranularityAllowed('hour', null)).toBe(true)
    expect(isGranularityAllowed('week', null)).toBe(true)
  })

  test.each([
    ['hour', 29, 'day'],
    ['hour', 14, 'day'],
    ['week', 1, 'day'],
    ['week', 7, 'day'],
    ['day', 120, 'week'],
    ['hour', 1, 'hour'],
    ['week', 29, 'week'],
  ] as const)(
    'switches %s granularity over %i days to %s',
    (granularity, days, expected) => {
      expect(nearestAllowedGranularity(granularity, days)).toBe(expected)
    }
  )

  test('explains which bound an unavailable granularity violates', () => {
    expect(getGranularityRangeReason('hour', 29)).toEqual({
      key: 'Up to {{days}} days',
      days: 7,
    })
    expect(getGranularityRangeReason('week', 7)).toEqual({
      key: 'At least {{days}} days',
      days: 8,
    })
    expect(getGranularityRangeReason('day', 29)).toBeNull()
  })

  test('measures the selected range in days', () => {
    const end = new Date('2026-02-01T00:00:00')
    const start = new Date(end.getTime() - 14 * DAY_MS)
    expect(
      getFilterRangeDays({ start_timestamp: start, end_timestamp: end })
    ).toBe(14)
    // Sub-day offsets from the date inputs must not cross a bound.
    expect(
      getFilterRangeDays({
        start_timestamp: new Date(start.getTime() + 2 * 60 * 60 * 1000),
        end_timestamp: end,
      })
    ).toBe(14)
    expect(getFilterRangeDays({ start_timestamp: start })).toBeNull()
    expect(
      getFilterRangeDays({ start_timestamp: end, end_timestamp: start })
    ).toBeNull()
  })
})

describe('dashboard default filters', () => {
  const preferences = (
    defaultTimeRangeDays: number,
    defaultTimeGranularity: DashboardChartPreferences['defaultTimeGranularity']
  ): DashboardChartPreferences => ({
    consumptionDistributionChart: 'bar',
    modelAnalyticsChart: 'trend',
    defaultTimeRangeDays,
    defaultTimeGranularity,
  })

  test('keeps a compatible saved range and granularity pair', () => {
    const filters = buildDefaultDashboardFilters(preferences(29, 'week'))
    expect(filters.time_granularity).toBe('week')
    expect(getFilterRangeDays(filters)).toBe(29)
  })

  test('keeps the saved range and fixes a granularity that cannot bucket it', () => {
    const filters = buildDefaultDashboardFilters(preferences(29, 'hour'))
    expect(filters.time_granularity).toBe('day')
    expect(getFilterRangeDays(filters)).toBe(29)
  })
})

describe('chart metric preference', () => {
  test('defaults to quota and remembers the saved metric', () => {
    expect(getSavedChartMetric()).toBe('quota')
    saveChartMetric('tokens')
    expect(localStorage.getItem(CHART_METRIC_STORAGE_KEY)).toBe('tokens')
    expect(getSavedChartMetric()).toBe('tokens')
  })

  test('ignores an unknown stored value', () => {
    localStorage.setItem(CHART_METRIC_STORAGE_KEY, 'requests')
    expect(getSavedChartMetric()).toBe('quota')
  })
})
