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
import { AlertTriangleIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { PlaygroundStorageFailure } from '../../types'

interface StorageWarningBannerProps {
  failure: PlaygroundStorageFailure
  onDismiss: () => void
}

export function StorageWarningBanner(props: StorageWarningBannerProps) {
  const { t } = useTranslation()
  if (!props.failure) return null

  const message =
    props.failure === 'quota'
      ? t(
          'Browser storage is full or this chat is too large, so it could not be saved. Delete older chats or remove attachments to continue.'
        )
      : t(
          'This chat could not be saved to browser storage. Your latest changes may be lost.'
        )

  return (
    <div
      role='alert'
      className='border-destructive/30 bg-destructive/10 text-destructive flex items-start gap-2 border-b px-4 py-2 text-xs'
    >
      <AlertTriangleIcon className='mt-px size-3.5 shrink-0' />
      <p className='min-w-0 flex-1'>{message}</p>
      <Button
        className='text-destructive h-auto shrink-0 p-0 text-xs underline underline-offset-2'
        type='button'
        variant='link'
        onClick={props.onDismiss}
      >
        {t('Dismiss')}
      </Button>
    </div>
  )
}
