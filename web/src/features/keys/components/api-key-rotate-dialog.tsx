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
import { KeyRound } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { handleServerError } from '@/lib/handle-server-error'
import { AuthOperationError } from '@/lib/secure-verification'

import { rotateApiKey } from '../api'
import { SUCCESS_MESSAGES } from '../constants'
import { useApiKeys } from './api-keys-provider'

// The replacement secret is shown once, after a verified rotation, and is never
// stored in the API-key cache.
export function ApiKeyRotateDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh, forgetResolvedKey } =
    useApiKeys()
  const isOpen = open === 'rotate'
  const verification = useSecureVerification()
  const { requestVerification, cancel: cancelVerification } = verification
  const [pending, setPending] = useState(false)
  const [rotatedKey, setRotatedKey] = useState('')
  const operation = useRef<AbortController | null>(null)

  useEffect(() => {
    if (!isOpen) return
    return () => {
      const current = operation.current
      operation.current = null
      current?.abort()
      cancelVerification()
      setPending(false)
      setRotatedKey('')
    }
  }, [isOpen, cancelVerification])

  const handleRotate = useCallback(async () => {
    if (!currentRow || operation.current) return
    const controller = new AbortController()
    operation.current = controller
    setPending(true)
    try {
      const proof = await requestVerification({
        scope: 'token.key.rotate',
        context: { token_id: currentRow.id },
        title: t('Rotate API key'),
        description: t(
          'Verify your identity to replace this API key. The old key stops working immediately.'
        ),
      })
      if (operation.current !== controller || !proof) return
      // Revoke local reveals before sending: closing/unmounting after submission
      // cannot prove the server cancelled the mutation.
      forgetResolvedKey(currentRow.id)
      const rotated = await rotateApiKey(
        currentRow.id,
        proof.proof_token,
        controller.signal
      )
      if (operation.current !== controller) return
      setRotatedKey(`sk-${rotated.key}`)
      // The previous credential is no longer valid: drop any cached or in-flight
      // reveal so a later copy cannot hand out the retired secret.
      forgetResolvedKey(currentRow.id)
      toast.success(t(SUCCESS_MESSAGES.API_KEY_ROTATED))
      triggerRefresh()
    } catch (error) {
      if (operation.current !== controller) return
      const failure = AuthOperationError.from(error)
      if (failure.code !== 'AUTH_CANCELLED') handleServerError(failure)
    } finally {
      if (operation.current === controller) {
        operation.current = null
        setPending(false)
      }
    }
  }, [currentRow, forgetResolvedKey, requestVerification, t, triggerRefresh])

  const close = () => {
    operation.current?.abort()
    operation.current = null
    cancelVerification()
    setPending(false)
    setRotatedKey('')
    setOpen(null)
  }

  return (
    <>
      <ConfirmDialog
        open={isOpen && rotatedKey === ''}
        onOpenChange={(next) => {
          if (!next && !pending) close()
        }}
        title={t('Rotate API key')}
        desc={t(
          'A new credential replaces the current one for {{name}}. Permissions, quota and usage history stay on this key. The old credential stops working immediately.',
          { name: currentRow?.name ?? '' }
        )}
        confirmText={t('Rotate')}
        handleConfirm={handleRotate}
        isLoading={pending}
      />

      <ConfirmDialog
        open={isOpen && rotatedKey !== ''}
        onOpenChange={(next) => {
          if (!next) close()
        }}
        title={t('New API key')}
        desc={t(
          'Copy the new key now. It is shown once and the previous key no longer works.'
        )}
        confirmText={t('Done')}
        handleConfirm={close}
      >
        <div className='flex items-center gap-2 rounded-md border p-2'>
          <KeyRound className='text-muted-foreground size-4 shrink-0' />
          <code className='min-w-0 flex-1 truncate font-mono text-sm'>
            {rotatedKey}
          </code>
          <CopyButton value={rotatedKey} aria-label={t('Copy Key')} />
        </div>
      </ConfirmDialog>

      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}
