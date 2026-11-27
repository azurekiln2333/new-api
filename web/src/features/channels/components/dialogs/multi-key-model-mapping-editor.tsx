import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { validateModelMappingJson } from '../../lib/model-mapping-validation'
import { ModelMappingEditor } from '../model-mapping-editor'

type MultiKeyModelMappingEditorProps = {
  value: string
  channelModels: string[]
  isSaving: boolean
  onSave: (value: string) => void
  onCancel: () => void
}

/** The shared editor edits only this Key's overrides, never the inherited map. */
export function MultiKeyModelMappingEditor(
  props: MultiKeyModelMappingEditorProps
) {
  const { t } = useTranslation()
  const [value, setValue] = useState(props.value)
  const validation = validateModelMappingJson(value)

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.'
        )}
      </p>
      <ModelMappingEditor
        value={value}
        onChange={setValue}
        sourceModelOptions={props.channelModels}
        disabled={props.isSaving}
      />
      {!validation.valid && validation.error && (
        <p role='alert' className='text-destructive text-sm'>
          {t(validation.error)}
        </p>
      )}
      <div className='flex flex-wrap justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={props.isSaving}
          onClick={() => setValue('')}
        >
          {t('Restore inheritance')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={props.isSaving}
          onClick={props.onCancel}
        >
          {t('Cancel')}
        </Button>
        <Button
          type='button'
          disabled={props.isSaving || !validation.valid}
          onClick={() => props.onSave(value)}
        >
          {t('Save')}
        </Button>
      </div>
    </div>
  )
}
