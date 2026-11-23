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
import { Loader2, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'

import { fetchMultiKeyModels } from '../../api'
import { isQuotaConflict } from '../../lib/quota-operation'

type MultiKeyModelsEditorProps = {
  channelId: number
  keyIndex: number
  keyEntryId?: string
  expectedRev?: number
  initialScopeMode?: 'all' | 'allowlist'
  channelModels: string[]
  /** The initial allowlist, if any; scope mode is tracked independently. */
  initialModels: string[]
  isSaving: boolean
  onCancel: () => void
  onConflict: () => void
  onSave: (models: string[], scopeMode: 'all' | 'allowlist') => void
}

/** Per-key model scope editor, with discovery of the models one key serves. */
export function MultiKeyModelsEditor(props: MultiKeyModelsEditorProps) {
  const { t } = useTranslation()
  const [allModels, setAllModels] = useState(
    props.initialScopeMode
      ? props.initialScopeMode === 'all'
      : props.initialModels.length === 0
  )
  const [models, setModels] = useState<string[]>(props.initialModels)
  const [upstreamModels, setUpstreamModels] = useState<string[] | null>(null)
  const [isFetching, setIsFetching] = useState(false)

  const channelModelSet = new Set(props.channelModels)
  const servedByChannel = upstreamModels
    ? upstreamModels.filter((model) => channelModelSet.has(model))
    : []
  const notServedByChannel = upstreamModels
    ? upstreamModels.filter((model) => !channelModelSet.has(model))
    : []

  const fetchUpstreamModels = async () => {
    setIsFetching(true)
    try {
      const response = await fetchMultiKeyModels(
        props.channelId,
        props.keyIndex,
        props.keyEntryId,
        props.expectedRev
      )
      if (!response.success) {
        if (isQuotaConflict(response)) props.onConflict()
        else handleServerError(response, t('Failed to fetch models'))
        return
      }
      setUpstreamModels(response.data?.models ?? [])
    } catch (error: unknown) {
      if (isQuotaConflict(error)) props.onConflict()
      else handleServerError(error, t('Failed to fetch models'))
    } finally {
      setIsFetching(false)
    }
  }

  const options = props.channelModels.map((model) => ({
    value: model,
    label: model,
  }))

  return (
    <div className='flex min-h-0 flex-col gap-2 rounded-md border p-3'>
      <div className='flex items-center justify-between gap-2'>
        <p className='text-sm font-medium'>
          {t('Allowed Models')} #{props.keyEntryId ?? props.keyIndex + 1}
        </p>
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void fetchUpstreamModels()}
            disabled={isFetching || props.isSaving}
          >
            {isFetching ? (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' aria-hidden />
            ) : (
              <RefreshCw className='mr-2 h-4 w-4' aria-hidden />
            )}
            {t('Fetch from upstream')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={props.isSaving}
            onClick={props.onCancel}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            size='sm'
            disabled={props.isSaving || isFetching}
            onClick={() =>
              props.onSave(
                allModels ? [] : models,
                allModels ? 'all' : 'allowlist'
              )
            }
          >
            {props.isSaving && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' aria-hidden />
            )}
            {t('Save')}
          </Button>
        </div>
      </div>

      <div className='flex items-center gap-2'>
        <Switch
          id='allocation-all-models'
          checked={allModels}
          onCheckedChange={setAllModels}
          disabled={props.isSaving}
        />
        <Label htmlFor='allocation-all-models'>{t('All models')}</Label>
      </div>
      <MultiSelect
        options={options}
        selected={models}
        onChange={(next) => {
          setAllModels(false)
          setModels(next)
        }}
        placeholder={allModels ? t('All models') : t('Select models')}
        emptyText={t('No channel models yet')}
        disabled={props.isSaving}
      />

      {upstreamModels && (
        <div className='space-y-1 text-xs'>
          {servedByChannel.length > 0 && (
            <div>
              <p className='text-muted-foreground'>
                {t('Models this key serves upstream:')}
              </p>
              <div className='flex flex-wrap gap-1'>
                {servedByChannel.map((model) => (
                  <Button
                    key={model}
                    type='button'
                    variant='outline'
                    size='sm'
                    className='h-6'
                    disabled={props.isSaving}
                    onClick={() => {
                      setAllModels(false)
                      setModels((current) =>
                        current.includes(model) ? current : [...current, model]
                      )
                    }}
                  >
                    {model}
                  </Button>
                ))}
              </div>
            </div>
          )}
          {notServedByChannel.length > 0 && (
            <div>
              <p className='text-muted-foreground'>
                {t(
                  'Models this key serves upstream but the channel does not provide yet:'
                )}
              </p>
              <p className='text-warning'>
                {notServedByChannel.join(', ')}{' '}
                {t(
                  'Add them to the channel models first, or sync models from upstream.'
                )}
              </p>
            </div>
          )}
          {upstreamModels.length === 0 && (
            <p className='text-muted-foreground'>
              {t('No models returned by the upstream')}
            </p>
          )}
        </div>
      )}

      <p className='text-muted-foreground text-xs'>
        {allModels
          ? t('This allocation can use every channel model.')
          : t(
              'Only selected models are allowed. An empty selection blocks all models.'
            )}
      </p>
    </div>
  )
}
