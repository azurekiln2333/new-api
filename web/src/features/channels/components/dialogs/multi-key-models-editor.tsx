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
import { handleServerError } from '@/lib/handle-server-error'

import { fetchMultiKeyModels } from '../../api'

type MultiKeyModelsEditorProps = {
  channelId: number
  keyIndex: number
  channelModels: string[]
  /** Current scope of the key; an empty list means every channel model. */
  initialModels: string[]
  isSaving: boolean
  onCancel: () => void
  onSave: (models: string[]) => void
}

/** Per-key model scope editor, with discovery of the models one key serves. */
export function MultiKeyModelsEditor(props: MultiKeyModelsEditorProps) {
  const { t } = useTranslation()
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
        props.keyIndex
      )
      if (!response.success) {
        handleServerError(response, t('Failed to fetch models'))
        return
      }
      setUpstreamModels(response.data?.models ?? [])
    } catch (error: unknown) {
      handleServerError(error, t('Failed to fetch models'))
    } finally {
      setIsFetching(false)
    }
  }

  const options = [...props.channelModels, ...servedByChannel].map((model) => ({
    value: model,
    label: model,
  }))

  return (
    <div className='flex min-h-0 flex-col gap-2 rounded-md border p-3'>
      <div className='flex items-center justify-between gap-2'>
        <p className='text-sm font-medium'>
          {t('Allowed Models')} #{props.keyIndex + 1}
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
            disabled={props.isSaving || props.channelModels.length === 0}
            onClick={() => props.onSave(models)}
          >
            {props.isSaving && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' aria-hidden />
            )}
            {t('Save')}
          </Button>
        </div>
      </div>

      <MultiSelect
        options={options}
        selected={models}
        onChange={setModels}
        placeholder={t('All models')}
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
                    onClick={() =>
                      setModels((current) =>
                        current.includes(model) ? current : [...current, model]
                      )
                    }
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
        {t('Leave empty to let this key serve every channel model.')}
      </p>
    </div>
  )
}
