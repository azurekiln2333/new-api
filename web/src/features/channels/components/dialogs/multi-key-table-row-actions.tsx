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
import { RotateCcw, Wallet } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { KeyStatus, MultiKeyConfirmAction } from '../../types'

type MultiKeyTableRowActionsProps = {
  keyStatus: KeyStatus
  canOperate: boolean
  canManage: boolean
  /** The budget ledger is unavailable; never offer a limit that cannot apply. */
  quotaAvailable: boolean
  onAction: (action: MultiKeyConfirmAction) => void
  onEditModels: (keyStatus: KeyStatus) => void
  onEditUser: (keyStatus: KeyStatus) => void
  onUnbind: (keyStatus: KeyStatus) => void
  onEditQuota: (keyStatus: KeyStatus) => void
  onResetQuota: (keyStatus: KeyStatus) => void
}

export function MultiKeyTableRowActions(props: MultiKeyTableRowActionsProps) {
  const { t } = useTranslation()
  const keyStatus = props.keyStatus
  const isEnabled = keyStatus.status === 1
  const noPermissionTitle = props.canManage
    ? undefined
    : t('No permission to perform this action')
  const usedQuota = keyStatus.quota?.period_used_quota ?? 0
  let resetTitle = t('Reset used amount')
  if (!props.canManage) resetTitle = t('No permission to perform this action')
  else if (!props.quotaAvailable) {
    resetTitle = t('Per-key budget is not available yet')
  } else if (usedQuota <= 0) resetTitle = t('No used amount to reset')
  let unbindLabel = t('Bind User')
  if (keyStatus.user_id) {
    unbindLabel = t('Unbind')
  }

  return (
    <div className='flex flex-col flex-nowrap items-end justify-end gap-1.5 sm:flex-row sm:items-center'>
      {isEnabled ? (
        <Button
          variant='outline'
          size='sm'
          disabled={!props.canOperate}
          onClick={() =>
            props.onAction({
              type: 'disable',
              keyIndex: keyStatus.index,
              keyEntryId: keyStatus.id,
              expectedRev: keyStatus.rev,
            })
          }
        >
          {t('Disable')}
        </Button>
      ) : (
        <Button
          variant='outline'
          size='sm'
          disabled={!props.canOperate}
          onClick={() =>
            props.onAction({
              type: 'enable',
              keyIndex: keyStatus.index,
              keyEntryId: keyStatus.id,
              expectedRev: keyStatus.rev,
            })
          }
        >
          {t('Enable')}
        </Button>
      )}
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          if (!props.canManage) return
          props.onEditModels(keyStatus)
        }}
        disabled={!props.canManage}
        title={noPermissionTitle}
      >
        {t('Set Models')}
      </Button>
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          if (!props.canManage) return
          if (keyStatus.user_id) props.onUnbind(keyStatus)
          else props.onEditUser(keyStatus)
        }}
        disabled={!props.canManage}
        title={noPermissionTitle}
      >
        {unbindLabel}
      </Button>
      <Button
        variant='ghost'
        size='icon-sm'
        onClick={() => {
          if (!props.canManage) return
          props.onEditQuota(keyStatus)
        }}
        disabled={!props.canManage || !props.quotaAvailable}
        title={
          noPermissionTitle ??
          (props.quotaAvailable
            ? t('Set Quota')
            : t('Per-key budget is not available yet'))
        }
        aria-label={t('Set Quota')}
      >
        <Wallet className='size-4' aria-hidden />
      </Button>
      <Button
        variant='ghost'
        size='icon-sm'
        onClick={() => {
          if (!props.canManage) return
          props.onResetQuota(keyStatus)
        }}
        disabled={!props.canManage || !props.quotaAvailable || usedQuota <= 0}
        title={resetTitle}
        aria-label={t('Reset used amount')}
      >
        <RotateCcw className='size-4' aria-hidden />
      </Button>
      <Button
        variant='destructive'
        size='sm'
        onClick={() => {
          if (!props.canManage) return
          props.onAction({
            type: 'delete',
            keyIndex: keyStatus.index,
            keyEntryId: keyStatus.id,
            expectedRev: keyStatus.rev,
          })
        }}
        disabled={!props.canManage}
        title={noPermissionTitle}
      >
        {t('Delete')}
      </Button>
    </div>
  )
}
