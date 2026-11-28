import { Alert, AlertDescription } from '@/components/ui/alert'
import { useTranslation } from 'react-i18next'

import type { StatisticsCoverage } from './statistics-coverage-types'
import { UNKNOWN_STATISTICS_COVERAGE } from './statistics-coverage-types'

export function StatisticsCoverageNotice(props: {
  coverage?: StatisticsCoverage
  previousCoverage?: StatisticsCoverage
  previousCoverageRequired?: boolean
}) {
  const { t } = useTranslation()
  const coverage = props.coverage ?? UNKNOWN_STATISTICS_COVERAGE
  const hasIncompleteCurrent = coverage.coverage !== 'complete'
  const previousCoverage = props.previousCoverage
  const hasIncompletePrevious =
    props.previousCoverageRequired &&
    (!previousCoverage || previousCoverage.coverage !== 'complete')
  if (!hasIncompleteCurrent && !hasIncompletePrevious) return null

  let message: string | null = null
  if (hasIncompleteCurrent) {
    if (coverage.coverage === 'unknown') {
      message = t('Coverage is unknown; totals may be incomplete.')
    } else if (coverage.coverage === 'retained_verified') {
      message = t(
        'Totals include verified retained history; some older usage may be missing.'
      )
    } else {
      message = t('Totals are partial and may not include all usage.')
    }
  }
  if (hasIncompletePrevious) {
    message = [
      message,
      t('Previous-period coverage is incomplete; comparisons may be affected.'),
    ]
      .filter(Boolean)
      .join(' ')
  }

  if (!message) return null

  return (
    <Alert className='text-muted-foreground' role='status'>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  )
}
