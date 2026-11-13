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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { MultiKeyConfirmAction } from '../../types'

type MultiKeyTableRowActionsProps = {
  keyIndex: number
  status: number
  boundUserId?: number
  canManage: boolean
  onAction: (action: MultiKeyConfirmAction) => void
  onEditModels: (keyIndex: number) => void
  onEditUser: (keyIndex: number) => void
  onUnbind: (keyIndex: number) => void
}

export function MultiKeyTableRowActions({
  keyIndex,
  status,
  boundUserId,
  canManage,
  onAction,
  onEditModels,
  onEditUser,
  onUnbind,
}: MultiKeyTableRowActionsProps) {
  const { t } = useTranslation()
  const isEnabled = status === 1
  const noPermissionTitle = canManage
    ? undefined
    : t('No permission to perform this action')

  return (
    <div className='flex flex-wrap justify-end gap-2'>
      {isEnabled ? (
        <Button
          variant='outline'
          size='sm'
          onClick={() => onAction({ type: 'disable', keyIndex })}
        >
          {t('Disable')}
        </Button>
      ) : (
        <Button
          variant='outline'
          size='sm'
          onClick={() => onAction({ type: 'enable', keyIndex })}
        >
          {t('Enable')}
        </Button>
      )}
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          if (!canManage) return
          onEditModels(keyIndex)
        }}
        disabled={!canManage}
        title={noPermissionTitle}
      >
        {t('Set Models')}
      </Button>
      {boundUserId ? (
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            if (!canManage) return
            onUnbind(keyIndex)
          }}
          disabled={!canManage}
          title={noPermissionTitle}
        >
          {t('Unbind')}
        </Button>
      ) : (
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            if (!canManage) return
            onEditUser(keyIndex)
          }}
          disabled={!canManage}
          title={noPermissionTitle}
        >
          {t('Bind User')}
        </Button>
      )}
      <Button
        variant='destructive'
        size='sm'
        onClick={() => {
          if (!canManage) return
          onAction({ type: 'delete', keyIndex })
        }}
        disabled={!canManage}
        title={noPermissionTitle}
      >
        {t('Delete')}
      </Button>
    </div>
  )
}
