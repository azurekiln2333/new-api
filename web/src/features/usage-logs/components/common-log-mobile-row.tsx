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
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { formatLogQuota, formatNumber } from '@/lib/format'
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
import { StreamTpsCell, TimingMetricsCell } from './timing-metrics-cell'
import { useUsageLogsContext } from './usage-logs-provider'

/** Model and throughput lead the summary; expanding retains the full card. */
export function CommonLogMobileRow<TData>(props: {
  log: UsageLog
  cells: Map<string, Cell<TData, unknown>>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { sensitiveVisible } = useUsageLogsContext()
  const [expanded, setExpanded] = useState(false)

  const log = props.log
  const other = parseLogOther(log.other)
  const displayable = isDisplayableLogType(log.type)
  const timing = isTimingLogType(log.type)
  const model = formatModelName(log)
  const config = getLogTypeConfig(log.type)

  const showTime = props.cells.has('created_at')
  const showModel = displayable && props.cells.has('model_name')
  const showCost = displayable && props.cells.has('quota')
  const showTokens =
    displayable &&
    props.cells.has('prompt_tokens') &&
    (log.prompt_tokens > 0 || log.completion_tokens > 0)
  const showUseTime = timing && props.cells.has('use_time')
  const showTokenName =
    displayable && props.cells.has('token_name') && !!log.token_name
  const showUser = props.cells.has('user') && !!log.username
  const nodeName = props.cells.has('node_name')
    ? other?.admin_info?.node_name || t('Not recorded')
    : ''
  const visibleNodeName = sensitiveVisible ? nodeName : '••••'

  const tokensPerSecond =
    log.use_time > 0 && log.completion_tokens > 0
      ? log.completion_tokens / log.use_time
      : null

  // Operation-style logs carry content instead of model/token metrics.
  const contentPreview = displayable
    ? ''
    : (renderLogContent(other, t) ?? log.content ?? '')

  return (
    <div className='min-w-0'>
      <div className='flex min-w-0 flex-col gap-2 px-3 py-2.5'>
        <div
          className='flex min-w-0 flex-col gap-1.5'
          data-slot='mobile-log-primary'
        >
          <Button
            type='button'
            variant='ghost'
            aria-expanded={expanded}
            onClick={() => setExpanded((value) => !value)}
            className='h-auto min-h-8 w-full min-w-0 items-start justify-between gap-2 px-0 py-0 text-left font-normal whitespace-normal'
          >
            {showModel ? (
              <span className='text-foreground min-w-0 flex-1 text-sm leading-5 font-semibold [overflow-wrap:anywhere]'>
                {model.name || '-'}
              </span>
            ) : (
              <StatusBadge
                label={t(config.label)}
                variant={config.color as StatusVariant}
                copyable={false}
                showDot
              />
            )}
            <ChevronDown
              aria-hidden='true'
              className={cn(
                'text-muted-foreground mt-0.5 size-4 shrink-0 transition-transform',
                expanded && 'rotate-180'
              )}
            />
          </Button>
          {timing && props.cells.has('is_stream') && (
            <StreamTpsCell
              compact={tokensPerSecond == null}
              className='max-w-full min-w-0 flex-row flex-wrap items-center gap-x-3 gap-y-1 text-sm [&>span+span]:text-foreground [&>span+span]:font-semibold'
              isStream={log.is_stream}
              isTask={other?.is_task === true}
              isSyncTask={other?.task_sync === true}
              tokensPerSecond={tokensPerSecond}
              streamStatus={other?.stream_status}
            />
          )}
        </div>
        <div className='text-muted-foreground flex min-w-0 flex-col gap-2 text-xs'>
          <div className='flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1'>
            {showModel && (
              <StatusBadge
                label={t(config.label)}
                variant={config.color as StatusVariant}
                copyable={false}
                showDot
                className='h-5 px-0 text-xs'
              />
            )}
            {showCost && (
              <span className='min-w-0 [overflow-wrap:anywhere]'>
                {t('Cost')}{' '}
                <span className='text-foreground tabular-nums'>
                  {formatLogQuota(log.quota)}
                </span>
              </span>
            )}
            {showTime && (
              <span className='tabular-nums'>
                {dayjs.unix(log.created_at).format('MM-DD HH:mm:ss')}
              </span>
            )}
          </div>
          {contentPreview && (
            <p className='text-foreground [overflow-wrap:anywhere]'>
              {contentPreview}
            </p>
          )}
          {showTokens && (
            <dl className='grid min-w-0 grid-cols-2 gap-x-4 gap-y-1'>
              <div className='min-w-0'>
                <dt>{t('Input')}</dt>
                <dd className='text-foreground font-medium tabular-nums [overflow-wrap:anywhere]'>
                  {formatNumber(log.prompt_tokens, locale)}
                </dd>
              </div>
              <div className='min-w-0'>
                <dt>{t('Output')}</dt>
                <dd className='text-foreground font-medium tabular-nums [overflow-wrap:anywhere]'>
                  {formatNumber(log.completion_tokens, locale)}
                </dd>
              </div>
            </dl>
          )}
          {showUseTime && log.use_time > 0 && (
            <TimingMetricsCell
              useTimeSec={log.use_time}
              completionTokens={log.completion_tokens}
              frtMs={other?.frt}
              isStream={log.is_stream}
              indicator='dot'
              compact
              className='min-w-0'
            />
          )}
          {(showTokenName || showUser || nodeName) && (
            <div className='flex min-w-0 flex-wrap gap-x-3 gap-y-1 [overflow-wrap:anywhere]'>
              {showTokenName && (
                <span className='min-w-0'>
                  {sensitiveVisible ? logTokenName(log, t) : '••••'}
                </span>
              )}
              {showUser && (
                <span className='min-w-0'>
                  {sensitiveVisible ? log.username : '••••'}
                </span>
              )}
              {nodeName && (
                <span
                  className='min-w-0 font-mono'
                  title={`${t('Processing Node')}: ${visibleNodeName}`}
                >
                  {visibleNodeName}
                </span>
              )}
            </div>
          )}
        </div>
      </div>
      {expanded && (
        <div className='border-border/40 border-t px-3 pt-2.5 pb-3'>
          <CommonLogMobileCard log={log} cells={props.cells} />
        </div>
      )}
    </div>
  )
}
