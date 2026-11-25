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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { formatTimestampToDate } from '@/lib/format'

import { listCodeBatches, type CodeExportScope } from './code-export-api'

export type CodeBatchHistoryDialogProps = {
  scope: CodeExportScope
  onExportBatch: (operationId: string) => void
  onClose: () => void
}

/**
 * Historical create-batch list. This is the recovery surface for a lost response:
 * the batch is already committed, so the operator finds it here and re-downloads
 * the original keys instead of submitting a duplicate create. The server lists and
 * exports only the caller's own batches.
 */
export function CodeBatchHistoryDialog(props: CodeBatchHistoryDialogProps) {
  const { t } = useTranslation()
  const batches = useQuery({
    queryKey: ['code-batches', props.scope],
    queryFn: () => listCodeBatches(props.scope, 1, 50),
  })

  const items = batches.data?.items ?? []

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Create history')}
      description={t(
        'Recover the original codes of a batch when the creation response was lost.'
      )}
      contentClassName='sm:max-w-2xl'
      footer={
        <Button variant='outline' onClick={props.onClose}>
          {t('Close')}
        </Button>
      }
    >
      {batches.isLoading ? (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      ) : null}
      {!batches.isLoading && items.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No create batches yet.')}
        </p>
      ) : null}
      {!batches.isLoading && items.length > 0 ? (
        <ul className='divide-y'>
          {items.map((batch) => (
            <li
              key={batch.operation_id}
              className='flex items-center justify-between gap-4 py-3'
            >
              <div className='min-w-0'>
                <p className='truncate text-sm font-medium'>
                  {formatTimestampToDate(batch.created_at)}
                </p>
                <p className='text-muted-foreground truncate text-xs'>
                  {t('{{count}} codes', { count: batch.count })}
                  {batch.creation_batch_id
                    ? ` · ${t('Shared claim limit')}`
                    : ''}
                </p>
              </div>
              <Button
                variant='outline'
                size='sm'
                onClick={() => props.onExportBatch(batch.operation_id)}
              >
                {t('Export')}
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
    </Dialog>
  )
}
