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
import {
  CHART_METRIC_STORAGE_KEY,
  DASHBOARD_CHART_PREFERENCES_STORAGE_KEY,
  DEFAULT_DASHBOARD_CHART_PREFERENCES,
  DEFAULT_TIME_GRANULARITY,
  EMPTY_DASHBOARD_FILTERS,
  GRANULARITY_RANGE_LIMITS,
  TIME_GRANULARITY_STORAGE_KEY,
  TIME_RANGE_PRESETS,
} from '@/features/dashboard/constants'
import type {
  ChartMetric,
  ConsumptionDistributionChartType,
  DashboardChartPreferences,
  DashboardFilters,
  ModelAnalyticsChartTab,
} from '@/features/dashboard/types'
import { getRollingDateRange, type TimeGranularity } from '@/lib/time'

const GRANULARITY_ORDER: TimeGranularity[] = ['hour', 'day', 'week']

function isTimeGranularity(value: unknown): value is TimeGranularity {
  return value === 'hour' || value === 'day' || value === 'week'
}

function getLegacySavedGranularity(): TimeGranularity {
  if (typeof window === 'undefined') return DEFAULT_TIME_GRANULARITY
  const saved = localStorage.getItem(TIME_GRANULARITY_STORAGE_KEY)
  return isTimeGranularity(saved) ? saved : DEFAULT_TIME_GRANULARITY
}

function isConsumptionDistributionChartType(
  value: unknown
): value is ConsumptionDistributionChartType {
  return value === 'bar' || value === 'area'
}

function isModelAnalyticsChartTab(
  value: unknown
): value is ModelAnalyticsChartTab {
  return value === 'trend' || value === 'proportion' || value === 'top'
}

function isTimeRangePresetDays(value: unknown): value is number {
  return TIME_RANGE_PRESETS.some((preset) => preset.days === value)
}

export function cleanFilters<T extends Record<string, unknown>>(
  filters: T
): Partial<T> {
  const cleaned: Partial<T> = {}
  for (const [key, value] of Object.entries(filters)) {
    if (value === undefined || value === null) continue
    if (typeof value === 'string') {
      const trimmed = value.trim()
      if (trimmed) cleaned[key as keyof T] = trimmed as T[keyof T]
      continue
    }
    cleaned[key as keyof T] = value as T[keyof T]
  }
  return cleaned
}

export function getSavedGranularity(
  override?: TimeGranularity
): TimeGranularity {
  if (override) return override
  return getSavedChartPreferences().defaultTimeGranularity
}

export function saveGranularity(granularity: TimeGranularity): void {
  if (typeof window === 'undefined') return
  saveChartPreferences({
    ...getSavedChartPreferences(),
    defaultTimeGranularity: granularity,
  })
  localStorage.setItem(TIME_GRANULARITY_STORAGE_KEY, granularity)
}

export function getSavedChartPreferences(): DashboardChartPreferences {
  if (typeof window === 'undefined') return DEFAULT_DASHBOARD_CHART_PREFERENCES

  const fallbackPreferences = {
    ...DEFAULT_DASHBOARD_CHART_PREFERENCES,
    defaultTimeGranularity: getLegacySavedGranularity(),
  }

  try {
    const raw = localStorage.getItem(DASHBOARD_CHART_PREFERENCES_STORAGE_KEY)
    if (!raw) return fallbackPreferences

    const parsed = JSON.parse(raw) as Partial<DashboardChartPreferences>
    return {
      consumptionDistributionChart: isConsumptionDistributionChartType(
        parsed.consumptionDistributionChart
      )
        ? parsed.consumptionDistributionChart
        : fallbackPreferences.consumptionDistributionChart,
      modelAnalyticsChart: isModelAnalyticsChartTab(parsed.modelAnalyticsChart)
        ? parsed.modelAnalyticsChart
        : fallbackPreferences.modelAnalyticsChart,
      defaultTimeRangeDays: isTimeRangePresetDays(parsed.defaultTimeRangeDays)
        ? parsed.defaultTimeRangeDays
        : fallbackPreferences.defaultTimeRangeDays,
      defaultTimeGranularity: isTimeGranularity(parsed.defaultTimeGranularity)
        ? parsed.defaultTimeGranularity
        : fallbackPreferences.defaultTimeGranularity,
    }
  } catch {
    return fallbackPreferences
  }
}

export function saveChartPreferences(
  preferences: DashboardChartPreferences
): void {
  if (typeof window === 'undefined') return
  localStorage.setItem(
    DASHBOARD_CHART_PREFERENCES_STORAGE_KEY,
    JSON.stringify(preferences)
  )
}

export function getDefaultDays(): number {
  return getSavedChartPreferences().defaultTimeRangeDays
}

/**
 * Days covered by a filter range, or null while the range is incomplete.
 * Rounded rather than ceiled so a range picked through the date inputs is not
 * pushed over a compatibility bound by sub-day offsets.
 */
export function getFilterRangeDays(filters?: {
  start_timestamp?: Date
  end_timestamp?: Date
}): number | null {
  const start = filters?.start_timestamp?.getTime()
  const end = filters?.end_timestamp?.getTime()
  if (start == null || end == null) return null
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
    return null
  }
  return Math.round((end - start) / 86_400_000)
}

/**
 * Whether a granularity can bucket a range of the given length. An unknown
 * range never blocks a granularity.
 */
export function isGranularityAllowed(
  granularity: TimeGranularity,
  rangeDays: number | null
): boolean {
  if (rangeDays == null) return true
  const limits = GRANULARITY_RANGE_LIMITS[granularity]
  return rangeDays >= limits.minDays && rangeDays <= limits.maxDays
}

/**
 * The granularity to use instead of one that cannot bucket the range: the
 * closest still-compatible option, preferring the finer one on a tie.
 */
export function nearestAllowedGranularity(
  granularity: TimeGranularity,
  rangeDays: number | null
): TimeGranularity {
  if (isGranularityAllowed(granularity, rangeDays)) return granularity
  const current = GRANULARITY_ORDER.indexOf(granularity)
  const distance = (item: TimeGranularity) =>
    Math.abs(GRANULARITY_ORDER.indexOf(item) - current)
  const candidates = GRANULARITY_ORDER.filter((item) =>
    isGranularityAllowed(item, rangeDays)
  ).sort((a, b) => distance(a) - distance(b))
  return candidates[0] ?? granularity
}

/**
 * Why a granularity cannot be used for the range, as an i18n key plus the
 * bound it violates. Null when the combination is allowed.
 */
export function getGranularityRangeReason(
  granularity: TimeGranularity,
  rangeDays: number | null
): { key: string; days: number } | null {
  if (isGranularityAllowed(granularity, rangeDays) || rangeDays == null) {
    return null
  }
  const limits = GRANULARITY_RANGE_LIMITS[granularity]
  if (rangeDays > limits.maxDays) {
    return { key: 'Up to {{days}} days', days: limits.maxDays }
  }
  return { key: 'At least {{days}} days', days: limits.minDays }
}

export function isChartMetric(value: unknown): value is ChartMetric {
  return value === 'quota' || value === 'tokens'
}

export function getSavedChartMetric(): ChartMetric {
  if (typeof window === 'undefined') return 'quota'
  const saved = localStorage.getItem(CHART_METRIC_STORAGE_KEY)
  return isChartMetric(saved) ? saved : 'quota'
}

export function saveChartMetric(metric: ChartMetric): void {
  if (typeof window === 'undefined') return
  localStorage.setItem(CHART_METRIC_STORAGE_KEY, metric)
}

export function buildDefaultDashboardFilters(
  preferences: DashboardChartPreferences = getSavedChartPreferences()
): DashboardFilters {
  const { start, end } = getRollingDateRange(preferences.defaultTimeRangeDays)
  return {
    ...EMPTY_DASHBOARD_FILTERS,
    start_timestamp: start,
    end_timestamp: end,
    // A saved pair may predate the independent range and granularity; keep the
    // range and fall back to the closest granularity that can bucket it.
    time_granularity: nearestAllowedGranularity(
      preferences.defaultTimeGranularity,
      preferences.defaultTimeRangeDays
    ),
  }
}

export function buildQueryParams(
  timeRange: { start_timestamp: number; end_timestamp: number },
  filters?: { time_granularity?: TimeGranularity; username?: string }
): {
  start_timestamp: number
  end_timestamp: number
  default_time: string
  username?: string
} {
  return {
    ...timeRange,
    default_time: getSavedGranularity(filters?.time_granularity),
    ...(filters?.username && { username: filters.username }),
  }
}
