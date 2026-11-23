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
import { Loader2 } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'

import type { MultiKeyModelSyncResult } from '../../types'

type MultiKeySyncDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  isFetching: boolean
  isApplying: boolean
  preview: MultiKeyModelSyncResult | null
  error: string | null
  onApply: () => void
}

/** Reviews the models an upstream sync would add before writing them. */
export function MultiKeySyncDialog(props: MultiKeySyncDialogProps) {
  const { t } = useTranslation()

  const results = props.preview?.results ?? []
  const failed = results.filter((result) => Boolean(result.error))
  const skipped = results.filter((result) => result.skipped)
  const fetched = results.filter((result) => !result.error && !result.skipped)
  const addedModels = props.preview?.added_models ?? []
  const canApply =
    addedModels.length > 0 || fetched.some((result) => result.models?.length)

  let footer: ReactNode = (
    <>
      <Button
        type='button'
        variant='outline'
        onClick={() => props.onOpenChange(false)}
        disabled={props.isApplying}
      >
        {t('Cancel')}
      </Button>
      <Button
        type='button'
        onClick={props.onApply}
        disabled={
          !canApply ||
          props.preview == null ||
          !!props.error ||
          props.isApplying ||
          props.isFetching
        }
      >
        {props.isApplying && (
          <Loader2 className='mr-2 h-4 w-4 animate-spin' aria-hidden />
        )}
        {t('Apply')}
      </Button>
    </>
  )
  if (props.preview?.applied) {
    footer = (
      <Button type='button' onClick={() => props.onOpenChange(false)}>
        {t('Done')}
      </Button>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Sync models from upstream')}
      description={t(
        'Each enabled key reports the models it serves. Applying adds the missing models to the channel and limits every successful key to the models it serves.'
      )}
      contentHeight='min(70dvh, 640px)'
      footer={footer}
      footerClassName='flex justify-end gap-2'
    >
      <div className='flex min-h-0 flex-col gap-3'>
        {props.isFetching && <LoadingState message={t('Fetching models...')} />}
        {props.error && (
          <p className='text-destructive text-sm'>{props.error}</p>
        )}
        {!props.isFetching && !props.error && props.preview && (
          <>
            {props.preview.applied && (
              <p className='text-sm font-medium'>{t('Models synced')}</p>
            )}
            <div className='grid grid-cols-3 gap-2 text-sm'>
              <p>
                {t('Keys fetched')}: {fetched.length}
              </p>
              <p>
                {t('Keys failed')}: {failed.length}
              </p>
              <p>
                {t('Keys skipped')}: {skipped.length}
              </p>
            </div>
            {failed.length > 0 && (
              <div className='space-y-1 text-xs'>
                <p className='text-muted-foreground'>{t('Failed keys')}</p>
                <ul className='text-destructive list-disc ps-5'>
                  {failed.map((result) => (
                    <li key={result.key_index}>
                      #{result.key_index + 1}: {result.error}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {skipped.length > 0 && (
              <p className='text-muted-foreground text-xs'>
                {t('Disabled keys are skipped')}:{' '}
                {skipped.map((result) => `#${result.key_index + 1}`).join(', ')}
              </p>
            )}
            <div className='space-y-1 text-xs'>
              <p className='text-muted-foreground'>
                {t('Models added to the channel')}
              </p>
              {addedModels.length > 0 ? (
                <p className='font-mono break-words'>
                  {addedModels.join(', ')}
                </p>
              ) : (
                <p className='text-muted-foreground'>
                  {t('The channel already provides every discovered model.')}
                </p>
              )}
            </div>
            {fetched.length > 0 && (
              <div className='space-y-1 text-xs'>
                <p className='text-muted-foreground'>
                  {t('Models each key serves')}
                </p>
                <ul className='space-y-1'>
                  {fetched.map((result) => (
                    <li key={result.key_index} className='break-words'>
                      <span className='font-medium'>
                        #{result.key_index + 1}:
                      </span>{' '}
                      {result.models?.length
                        ? result.models.join(', ')
                        : t('No models returned by the upstream')}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {results.length === 0 && (
              <EmptyState
                className='min-h-0 p-3'
                title={t('No enabled keys to sync')}
              />
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}
