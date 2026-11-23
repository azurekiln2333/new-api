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
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { translateServerText } from '@/lib/server-error-message'

import type { MultiKeyDedupResult } from '../../types'

type MultiKeyDedupDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  isFetching: boolean
  isApplying: boolean
  preview: MultiKeyDedupResult | null
  error: string | null
  onRefresh: () => void
  onApply: () => void
}

/**
 * Explicit dedup preview. Nothing is merged until the operator confirms; the
 * server revalidates identity and revisions on apply, and entries with live
 * work are reported as skipped rather than silently dropped.
 */
export function MultiKeyDedupDialog(props: MultiKeyDedupDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const groups = props.preview?.groups ?? []
  const canApply =
    !!props.preview?.preview_token &&
    props.preview.revision != null &&
    !props.preview.applied &&
    groups.some((group) => group.entry_ids.length > 1)
  const formatQuota = (value: number | null | undefined) =>
    value == null
      ? '-'
      : formatQuotaWithCurrency(value, {
          digitsLarge: 2,
          digitsSmall: 4,
          locale,
        })

  let body = (
    <p className='text-muted-foreground text-sm'>
      {t('No equivalent keys found')}
    </p>
  )
  if (props.isFetching) {
    body = <LoadingState size='sm' className='min-h-20' />
  } else if (props.error) {
    body = <ErrorState className='min-h-0 py-3' title={props.error} />
  } else if (groups.length > 0) {
    body = (
      <div className='space-y-2'>
        {groups.map((group) => {
          const survivor = group.survivor_id
          return (
            <div
              key={survivor || group.skipped[0]?.id}
              className='min-w-0 space-y-2 rounded-md border p-2.5'
            >
              {group.entry_ids.length > 1 && (
                <div className='flex flex-wrap items-center gap-2'>
                  <span className='text-xs font-medium'>
                    {t('Keep allocation')}
                  </span>
                  <StatusBadge
                    label={survivor}
                    variant='success'
                    size='sm'
                    copyable={false}
                    className='font-mono'
                  />
                  <span className='text-muted-foreground text-xs'>
                    {t('Merging allocations: {{count}}', {
                      count: group.entry_ids.length,
                    })}
                  </span>
                </div>
              )}
              {group.entry_ids.length > 1 && (
                <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
                  <span>
                    {t('Merged usage')}{' '}
                    <span className='text-foreground font-mono tabular-nums'>
                      {formatQuota(group.merged_period_used)}
                    </span>
                  </span>
                  <span>
                    {t('Merged lifetime')}{' '}
                    <span className='text-foreground font-mono tabular-nums'>
                      {formatQuota(group.merged_lifetime_used)}
                    </span>
                  </span>
                  <span>
                    {t('Limit')}{' '}
                    <span className='text-foreground font-mono tabular-nums'>
                      {group.limit_quota == null
                        ? t('Unlimited')
                        : formatQuota(group.limit_quota)}
                    </span>
                  </span>
                  <span>
                    {t('Remaining')}{' '}
                    <span className='text-foreground font-mono tabular-nums'>
                      {group.limit_quota == null
                        ? t('Unlimited')
                        : formatQuota(
                            Math.max(
                              0,
                              group.limit_quota - group.merged_period_used
                            )
                          )}
                    </span>
                  </span>
                </div>
              )}
              <div className='flex flex-wrap gap-1'>
                {group.entry_ids.map((id) => (
                  <StatusBadge
                    key={id}
                    label={id}
                    variant={id === survivor ? 'success' : 'neutral'}
                    size='sm'
                    copyable={false}
                    className='font-mono'
                  />
                ))}
              </div>
              {group.skipped.length > 0 && (
                <ul className='space-y-0.5 text-xs text-amber-600 dark:text-amber-400'>
                  {group.skipped.map((skip) => (
                    <li key={skip.id} className='wrap-break-word'>
                      {t('Skipped')}{' '}
                      <span className='font-mono'>{skip.id}</span>
                      {': '}
                      {translateServerText(t, skip.reason)}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )
        })}
      </div>
    )
  }

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={(next) => {
        if (props.isApplying) return
        props.onOpenChange(next)
      }}
      title={t('Deduplicate Keys')}
      desc={t(
        'Equivalent allocations are merged into one stable entry. Used and lifetime usage are conserved, and entries with live requests or tasks are skipped.'
      )}
      className='sm:max-w-2xl'
      confirmText={t('Merge equivalent allocations')}
      handleConfirm={props.onApply}
      isLoading={props.isApplying}
      disabled={!canApply || props.isFetching || !!props.error}
    >
      <Button
        variant='outline'
        size='sm'
        onClick={props.onRefresh}
        disabled={props.isFetching || props.isApplying}
      >
        <RefreshCw className='mr-2 size-4' />
        {t('Refresh preview')}
      </Button>
      <div className='max-h-[45dvh] min-w-0 overflow-y-auto'>{body}</div>
    </ConfirmDialog>
  )
}
