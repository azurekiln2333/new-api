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
import { cn } from '@/lib/utils'

import type { LogTextMatchMode } from '../types'

interface LogsMatchModeToggleProps {
  value: LogTextMatchMode
  onChange: (mode: LogTextMatchMode) => void
  className?: string
}

/**
 * Chooses how the usage-log text filters compare stored values: contains
 * matching (default) or exact value matching.
 */
export function LogsMatchModeToggle(props: LogsMatchModeToggleProps) {
  const { t } = useTranslation()

  return (
    <ToggleGroup
      value={[props.value]}
      onValueChange={(values) => {
        const next = values[0]
        if (next === 'fuzzy' || next === 'exact') props.onChange(next)
      }}
      variant='outline'
      size='sm'
      aria-label={t('Text match mode')}
      className={cn(
        '[&_[data-slot=toggle-group-item]]:text-xs',
        props.className
      )}
    >
      <ToggleGroupItem value='fuzzy'>{t('Contains')}</ToggleGroupItem>
      <ToggleGroupItem value='exact'>{t('Exact')}</ToggleGroupItem>
    </ToggleGroup>
  )
}
