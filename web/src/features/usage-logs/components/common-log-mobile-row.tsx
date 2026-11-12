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
import type { Cell } from '@tanstack/react-table'
import { ChevronDown } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import dayjs from '@/lib/dayjs'
import { formatLogQuota, formatTokens } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { UsageLog } from '../data/schema'
import {
  formatModelName,
  logTokenName,
  parseLogOther,
  renderLogContent,
} from '../lib/format'
import {
  getLogTypeConfig,
  isDisplayableLogType,
  isTimingLogType,
} from '../lib/utils'
import { CommonLogMobileCard } from './common-log-mobile-card'
import { useUsageLogsContext } from './usage-logs-provider'

/**
 * Dense two-line summary of one common log. Tapping the row expands the full
 * card below it, so every field, dialog and action stays available without
 * paying the card's height for every log in a long list.
 */
export function CommonLogMobileRow<TData>(props: {
  log: UsageLog
  cells: Map<string, Cell<TData, unknown>>
}) {
  const { t } = useTranslation()
  const { sensitiveVisible } = useUsageLogsContext()
  const [expanded, setExpanded] = useState(false)

  const log = props.log
  const other = parseLogOther(log.other)
  const displayable = isDisplayableLogType(log.type)
  const timing = isTimingLogType(log.type)
  const model = formatModelName(log)
  const config = getLogTypeConfig(log.type)

  const showTime = props.cells.has('created_at')
  const showModel = displayable && props.cells.has('model_name') && !!model.name
  const showCost = displayable && props.cells.has('quota')
  const showTokens = displayable && props.cells.has('prompt_tokens')
  const showUseTime = timing && props.cells.has('use_time')
  const showTokenName =
    displayable && props.cells.has('token_name') && !!log.token_name
  const showUser = props.cells.has('user') && !!log.username

  const useTimeLabel =
    log.use_time < 60
      ? `${log.use_time.toFixed(1)}s`
      : `${Math.floor(log.use_time / 60)}m${Math.round(log.use_time % 60)}s`

  // Operation-style logs (top-up, manage, system, login) carry a rendered
  // content string instead of model/token metrics; show it as the summary.
  const contentPreview = displayable
    ? ''
    : (renderLogContent(other, t) ?? log.content ?? '')

  const summaryParts: string[] = []
  if (contentPreview) {
    summaryParts.push(contentPreview)
  }
  if (showTokens && (log.prompt_tokens > 0 || log.completion_tokens > 0)) {
    summaryParts.push(
      `↑${formatTokens(log.prompt_tokens)} ↓${formatTokens(log.completion_tokens)}`
    )
  }
  if (showUseTime && log.use_time > 0) {
    summaryParts.push(useTimeLabel)
  }
  if (showTokenName) {
    summaryParts.push(sensitiveVisible ? logTokenName(log, t) : '••••')
  }
  if (showUser) {
    summaryParts.push(sensitiveVisible ? log.username : '••••')
  }

  return (
    <div className='min-w-0'>
      <button
        type='button'
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
        className='hover:bg-muted/40 focus-visible:ring-ring/50 flex w-full min-w-0 flex-col gap-1 px-3 py-2 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none'
      >
        <div className='flex min-w-0 items-center gap-2'>
          {showTime && (
            <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>
              {dayjs.unix(log.created_at).format('MM-DD HH:mm:ss')}
            </span>
          )}
          <StatusBadge
            label={t(config.label)}
            variant={config.color as StatusVariant}
            copyable={false}
            showDot
            className='h-4 shrink-0 px-0 text-[11px]'
          />
          {showModel && (
            <span className='text-foreground min-w-0 flex-1 truncate text-[13px] font-medium'>
              {model.name}
            </span>
          )}
          {!showModel && <span className='flex-1' />}
          {showCost && (
            <span className='text-foreground shrink-0 text-xs font-medium tabular-nums'>
              {formatLogQuota(log.quota)}
            </span>
          )}
          <ChevronDown
            aria-hidden='true'
            className={cn(
              'text-muted-foreground size-3.5 shrink-0 transition-transform',
              expanded && 'rotate-180'
            )}
          />
        </div>
        {summaryParts.length > 0 && (
          <div className='text-muted-foreground min-w-0 truncate text-[11px] leading-4'>
            {summaryParts.join(' · ')}
          </div>
        )}
      </button>
      {expanded && (
        <div className='border-border/40 border-t px-3 pt-2.5 pb-3'>
          <CommonLogMobileCard log={log} cells={props.cells} />
        </div>
      )}
    </div>
  )
}
