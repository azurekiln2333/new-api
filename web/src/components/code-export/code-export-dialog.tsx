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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

import {
  downloadCodeExport,
  type CodeExportFormat,
  type CodeExportScope,
} from './code-export-api'

export type CodeExportSelection =
  | { kind: 'ids'; ids: number[] }
  | { kind: 'batch'; operationId: string }
  | { kind: 'filter'; keyword: string; status: string }

export type CodeExportDialogProps = {
  scope: CodeExportScope
  selection: CodeExportSelection
  onClose: () => void
}

function selectionCountLabel(
  selection: CodeExportSelection,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (selection.kind === 'ids') {
    return t('{{count}} selected codes', { count: selection.ids.length })
  }
  if (selection.kind === 'batch') {
    return t('Every code in this create batch')
  }
  return t('Every code matching the current filters, across all pages')
}

/**
 * Shared download dialog for invitation and redemption codes. It renders the
 * selection it will export before writing anything, so an operator never
 * downloads a different set than the one the dialog describes.
 */
export function CodeExportDialog(props: CodeExportDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const [format, setFormat] = useState<CodeExportFormat>('txt')
  const [includeName, setIncludeName] = useState(true)
  const [includeQuota, setIncludeQuota] = useState(props.scope === 'redemption')
  const [pending, setPending] = useState(false)

  const handleDownload = async () => {
    setPending(true)
    try {
      const common = {
        scope: props.scope,
        format,
        includeName,
        includeQuota: props.scope === 'redemption' ? includeQuota : false,
      }
      if (props.selection.kind === 'ids') {
        await downloadCodeExport({ ...common, ids: props.selection.ids })
      } else if (props.selection.kind === 'batch') {
        await downloadCodeExport({
          ...common,
          operationId: props.selection.operationId,
        })
      } else {
        await downloadCodeExport({
          ...common,
          keyword: props.selection.keyword,
          status: props.selection.status,
        })
      }
      props.onClose()
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t('Failed to export codes')
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) props.onClose()
      }}
      title={t('Export codes')}
      description={selectionCountLabel(props.selection, t)}
      contentClassName='sm:max-w-md'
      bodyClassName='space-y-5'
      footer={
        <>
          <Button variant='outline' disabled={pending} onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button disabled={pending} onClick={handleDownload}>
            {pending ? t('Exporting...') : t('Download')}
          </Button>
        </>
      }
    >
      <RadioGroup
        value={format}
        onValueChange={(value) => {
          if (value === 'txt' || value === 'md') setFormat(value)
        }}
        aria-label={t('File format')}
        className='flex flex-wrap gap-x-5 gap-y-3'
      >
        {[
          { value: 'txt', label: t('Save as TXT') },
          { value: 'md', label: t('Save as Markdown') },
        ].map((option) => (
          <div key={option.value} className='flex items-center gap-2'>
            <RadioGroupItem value={option.value} id={`${id}-${option.value}`} />
            <Label htmlFor={`${id}-${option.value}`}>{option.label}</Label>
          </div>
        ))}
      </RadioGroup>
      <div className='flex flex-wrap gap-6'>
        <div className='flex items-center gap-2'>
          <Checkbox
            id={`${id}-name`}
            checked={includeName}
            onCheckedChange={setIncludeName}
          />
          <Label htmlFor={`${id}-name`}>{t('Include name')}</Label>
        </div>
        {props.scope === 'redemption' && (
          <div className='flex items-center gap-2'>
            <Checkbox
              id={`${id}-quota`}
              checked={includeQuota}
              onCheckedChange={setIncludeQuota}
            />
            <Label htmlFor={`${id}-quota`}>{t('Include quota')}</Label>
          </div>
        )}
      </div>
    </Dialog>
  )
}
