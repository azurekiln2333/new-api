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
import { useTranslation } from 'react-i18next'

import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { CHART_METRIC_OPTIONS } from '@/features/dashboard/constants'
import type { ChartMetric } from '@/features/dashboard/types'
import { cn } from '@/lib/utils'

interface ChartMetricToggleProps {
  value: ChartMetric
  onChange: (metric: ChartMetric) => void
  className?: string
}

/**
 * Switches what the dashboard charts measure: consumed quota or tokens. The
 * choice is shared by the model and user sections.
 */
export function ChartMetricToggle(props: ChartMetricToggleProps) {
  const { t } = useTranslation()

  return (
    <ToggleGroup
      value={[props.value]}
      onValueChange={(values) => {
        const next = values[0]
        if (next === 'quota' || next === 'tokens') props.onChange(next)
      }}
      variant='outline'
      size='sm'
      aria-label={t('Usage metric')}
      className={cn(
        '[&_[data-slot=toggle-group-item]]:text-xs',
        props.className
      )}
    >
      {CHART_METRIC_OPTIONS.map((option) => (
        <ToggleGroupItem key={option.value} value={option.value}>
          {t(option.labelKey)}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
