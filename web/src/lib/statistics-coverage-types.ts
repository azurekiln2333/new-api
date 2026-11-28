export type StatisticsCoverageState =
  | 'unknown'
  | 'partial'
  | 'retained_verified'
  | 'complete'

export interface StatisticsCoverage {
  complete: boolean
  coverage: StatisticsCoverageState
  historical_coverage: StatisticsCoverageState
  live_coverage: StatisticsCoverageState
  legacy_baseline_included: boolean
  start_timestamp: number
  end_timestamp: number
}

export const UNKNOWN_STATISTICS_COVERAGE: StatisticsCoverage = {
  complete: false,
  coverage: 'unknown',
  historical_coverage: 'unknown',
  live_coverage: 'unknown',
  legacy_baseline_included: false,
  start_timestamp: 0,
  end_timestamp: 0,
}
