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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'

import type { MultiKeyModelSelection, MultiKeyModelSyncResult } from '../../types'
import { UpstreamModelSelection } from '../upstream-model-selection'

type MultiKeySyncDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  isFetching: boolean
  isApplying: boolean
  preview: MultiKeyModelSyncResult | null
  error: string | null
  onApply: (selections: MultiKeyModelSelection[]) => void
}

/** Reviews the models an upstream sync would add before writing them. */
export function MultiKeySyncDialog(props: MultiKeySyncDialogProps) {
  const { t } = useTranslation()

  const results = props.preview?.results ?? []
  const failed = results.filter((result) => Boolean(result.error))
  const skipped = results.filter((result) => result.skipped)
  const fetched = results.filter((result) => !result.error && !result.skipped)
  const [selectionState, setSelectionState] = useState<{
    preview: MultiKeyModelSyncResult | null
    models: Record<string, string[]>
  }>({ preview: props.preview, models: {} })
  if (selectionState.preview !== props.preview) {
    setSelectionState({ preview: props.preview, models: {} })
  }
  const selections = fetched.flatMap((result): MultiKeyModelSelection[] => {
    const models = selectionState.preview === props.preview
      ? selectionState.models[result.key_entry_id ?? ''] ?? []
      : []
    if (!result.key_entry_id || !result.rev || !models.length) return []
    return [{ key_entry_id: result.key_entry_id, rev: result.rev, models }]
  })
  const addedModels = props.preview?.applied
    ? props.preview.added_models
    : [...new Set(selections.flatMap((selection) => selection.models))].filter(
        (model) => !props.preview?.channel_models?.includes(model)
      )
  const canApply = selections.length > 0 && !!props.preview?.preview_token

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
        onClick={() => props.onApply(selections)}
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
        'Select models separately for each Key. Fetching does not save. Apply adds only your selections to that Key and the channel catalog, preserving existing models.'
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
                  {t('No new models selected for the channel.')}
                </p>
              )}
            </div>
            {results.map((result) => {
              let content: ReactNode = (
                <p className='text-muted-foreground text-xs'>
                  {t('No models returned by the upstream')}
                </p>
              )
              if (result.error || result.skipped) {
                content = (
                  <p className='text-muted-foreground text-xs'>
                    {result.error ? t(result.error) : t('Disabled keys are skipped')}
                  </p>
                )
              } else if (result.models?.length) {
                content = (
                  <UpstreamModelSelection
                    models={result.models}
                    selected={selectionState.models[result.key_entry_id ?? ''] ?? []}
                    onChange={(models) => {
                      if (props.isApplying || props.isFetching || props.preview?.applied || !result.key_entry_id || !result.rev) return
                      setSelectionState((previous) => ({
                        preview: props.preview,
                        models: { ...previous.models, [result.key_entry_id ?? '']: models },
                      }))
                    }}
                    existingModels={[]}
                    showChanges={false}
                  />
                )
              }
              return (
              <fieldset
                key={result.key_entry_id ?? result.key_index}
                aria-label={t('Models for Key {{key}}', { key: result.key_entry_id ?? result.key_index + 1 })}
                className='min-w-0 rounded-md border p-3'
                disabled={props.isApplying || props.isFetching || props.preview?.applied || !!result.error || result.skipped || !result.key_entry_id || !result.rev}
              >
                <legend className='px-1 text-sm font-medium'>
                  {t('Models for Key {{key}}', { key: result.key_entry_id ?? result.key_index + 1 })}
                  {result.key_preview && <span className='ml-2 font-mono text-xs'>{result.key_preview}</span>}
                </legend>
                {content}
              </fieldset>
              )
            })}
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
