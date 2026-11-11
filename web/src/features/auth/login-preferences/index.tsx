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
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { TitledCard } from '@/components/ui/titled-card'
import { useAccountSecurity } from '@/features/security/hooks/use-account-security'
import { api } from '@/lib/api'
import { authRequestOptions, authResult } from '@/lib/secure-verification'
import { useAuthStore } from '@/stores/auth-store'

import { SecureVerificationDialog } from '../secure-verification'

type LoginPreference = {
  required: boolean
  enrolled: boolean
  policy_required: boolean
}

export function LoginVerificationPreference() {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const accessToken = useAuthStore((state) => state.auth.accessToken)
  const queryClient = useQueryClient()
  const security = useAccountSecurity()
  const query = useQuery({
    queryKey: ['login-verification-preference', userID],
    queryFn: () =>
      authResult<LoginPreference>(
        api.get('/api/user/login-verification', authRequestOptions)
      ),
    staleTime: 0,
  })
  // Enrolling or removing a factor rotates authentication. Refresh availability
  // after that rotation without putting the credential into the query cache key.
  useEffect(() => {
    void queryClient.invalidateQueries({
      queryKey: ['login-verification-preference', userID],
    })
  }, [accessToken, queryClient, userID])
  const mutation = useMutation({
    mutationFn: async (required: boolean) => {
      await security.run(async (signal) => {
        let proof: string | undefined
        if (!required) {
          proof = await security.verify(
            { scope: 'auth.login_verification.disable' },
            signal
          )
        }
        const result = await authResult<{ notification_warning?: boolean }>(
          api.put(
            '/api/user/login-verification',
            { required },
            {
              ...authRequestOptions,
              signal,
              acceptAuthRotation: true,
              singleUseAuthorization: true,
              headers: proof ? { 'X-Security-Proof': proof } : undefined,
            }
          )
        )
        await queryClient.invalidateQueries({
          queryKey: ['login-verification-preference'],
        })
        return result
      })
    },
  })

  return (
    <>
      <TitledCard title={t('Login verification')}>
        <div className='space-y-3'>
          <div className='flex items-center justify-between gap-4'>
            <Label htmlFor='login-verification-required'>
              {t('Require verification at login')}
            </Label>
            <Switch
              id='login-verification-required'
              checked={query.data?.required ?? false}
              disabled={
                !query.data || !query.data.enrolled || mutation.isPending
              }
              onCheckedChange={(value) => mutation.mutate(value)}
            />
          </div>
          {query.data && !query.data.enrolled && (
            <p className='text-muted-foreground text-sm'>
              {t('Enroll two-factor authentication or a Passkey first')}
            </p>
          )}
          {query.data?.policy_required && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'The administrator requires login verification for all enrolled users'
              )}
            </p>
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Sensitive actions still require verification. Turning this off requires security verification.'
            )}
          </p>
          <Button
            type='button'
            variant='ghost'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
          {query.isError && <p role='alert'>{t('Failed to load settings')}</p>}
        </div>
      </TitledCard>
      <SecureVerificationDialog {...security.verificationDialogProps} />
    </>
  )
}
