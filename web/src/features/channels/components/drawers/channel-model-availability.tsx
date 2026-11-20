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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getChannel, restoreChannelModelAvailability } from '../../api'
import { channelsQueryKeys } from '../../lib/channel-actions'
import type { Channel } from '../../types'

export function ChannelModelAvailability(props: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const canOperate = hasPermission(
    user,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )
  const [confirmOpen, setConfirmOpen] = useState(false)
  const queryKey = [
    ...channelsQueryKeys.detail(props.channel.id),
    'model_availability',
  ]
  const observations = props.channel.channel_info.model_unavailable
  const availability = useQuery({
    queryKey,
    enabled: (observations?.length ?? 0) > 0,
    initialData: observations,
    queryFn: async () => {
      const result = requireServerSuccess(await getChannel(props.channel.id))
      if (!result.data) throw new Error('Missing channel data')
      return result.data.channel_info.model_unavailable ?? []
    },
  })
  const visible = availability.data ?? observations ?? []
  const restore = useMutation({
    mutationFn: () => restoreChannelModelAvailability(props.channel.id),
    onMutate: () => queryClient.cancelQueries({ queryKey }),
    onSuccess: () => {
      queryClient.setQueryData(queryKey, [])
      setConfirmOpen(false)
      toast.success(t('Model availability restored'))
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
    },
    onError: (error) =>
      handleServerError(error, t('Failed to restore model availability')),
  })

  if (visible.length === 0) return null

  return (
    <section aria-label={t('Unavailable models')} className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='text-sm font-medium'>{t('Unavailable models')}</h3>
        {canOperate ? (
          <Button
            variant='outline'
            size='sm'
            onClick={() => setConfirmOpen(true)}
          >
            {t('Restore model availability')}
          </Button>
        ) : null}
      </div>
      <ul className='space-y-2'>
        {visible.map((entry) => (
          <li
            key={`${entry.upstream_model}:${entry.key_fingerprint}`}
            className='rounded-md border p-3 text-sm'
          >
            <div className='font-medium break-all'>{entry.upstream_model}</div>
            <div className='text-muted-foreground text-xs'>
              {entry.evidence === 'error_code'
                ? t('Structured upstream model refusal')
                : t('Explicit upstream model refusal')}
              {' · '}
              {entry.status_code}
              {' · '}
              {formatTimestampToDate(entry.observed_at)}
            </div>
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={confirmOpen && canOperate}
        onOpenChange={setConfirmOpen}
        title={t('Restore model availability')}
        desc={t(
          'Clear these observations so future requests can try the affected credentials again.'
        )}
        confirmText={t('Restore')}
        isLoading={restore.isPending}
        handleConfirm={() => {
          if (canOperate) restore.mutate()
        }}
      />
    </section>
  )
}
