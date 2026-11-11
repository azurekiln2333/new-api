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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  deleteUserRateLimit,
  getUserRateLimit,
  updateUserRateLimit,
} from '../api'
import type { UserRateLimitState } from '../types'

interface UserRateLimitSectionProps {
  userId: number
}

/**
 * A user follows the rate limit of their group unless an administrator gives
 * the user a separate limit. Turning the switch back off removes the override.
 */
export function UserRateLimitSection(props: UserRateLimitSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [state, setState] = useState<UserRateLimitState | null>(null)
  const [customEnabled, setCustomEnabled] = useState(false)
  const [maxRequests, setMaxRequests] = useState(0)
  const [maxSuccess, setMaxSuccess] = useState(1)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    getUserRateLimit(props.userId)
      .then((result) => {
        if (cancelled || !result.success || !result.data) return
        setState(result.data)
        setCustomEnabled(result.data.enabled)
        setMaxRequests(result.data.total_count)
        setMaxSuccess(result.data.success_count)
      })
      .catch((e: unknown) => {
        if (!cancelled) handleServerError(e, t('Failed to load'))
      })
    return () => {
      cancelled = true
    }
  }, [props.userId, t])

  const applyState = (next: UserRateLimitState) => {
    setState(next)
    setCustomEnabled(next.enabled)
    setMaxRequests(next.total_count)
    setMaxSuccess(next.success_count)
  }

  const handleSave = async () => {
    if (customEnabled && (maxRequests < 0 || maxSuccess < 1)) {
      setError(
        t(
          'Total requests must be 0 or more, and successful requests must be 1 or more'
        )
      )
      return
    }
    setError('')
    setSaving(true)
    try {
      const result = customEnabled
        ? await updateUserRateLimit(props.userId, {
            total_count: maxRequests,
            success_count: maxSuccess,
          })
        : await deleteUserRateLimit(props.userId)
      const data = requireServerSuccess(result).data
      if (data) applyState(data)
      toast.success(
        customEnabled
          ? t('Custom rate limit saved')
          : t('Rate limit follows the group again')
      )
    } catch (e: unknown) {
      handleServerError(e, t('Failed to save the rate limit'))
    } finally {
      setSaving(false)
    }
  }

  const groupLimitText = state?.group_configured
    ? t('Group limit: {{total}} requests ({{success}} successful) per period', {
        total:
          state.group_total_count === 0
            ? t('unlimited')
            : formatNumber(state.group_total_count, locale),
        success: formatNumber(state.group_success_count, locale),
      })
    : t('This group has no dedicated limit, so the global rate limit applies.')

  return (
    <div className='flex flex-col gap-3'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'A separate limit overrides the group limit for this user only. The time window is the global limit period.'
        )}
      </p>

      <div className='flex items-center justify-between gap-3'>
        <div className='space-y-0.5'>
          <Label htmlFor='user-rate-limit-custom'>
            {t('Set a rate limit for this user')}
          </Label>
          <p className='text-muted-foreground text-xs'>{groupLimitText}</p>
        </div>
        <Switch
          id='user-rate-limit-custom'
          checked={customEnabled}
          onCheckedChange={setCustomEnabled}
        />
      </div>

      {customEnabled && (
        <div className='grid gap-3 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='user-rate-limit-total'>
              {t('Max requests per period')}
            </Label>
            <Input
              id='user-rate-limit-total'
              type='number'
              min={0}
              step={1}
              value={maxRequests}
              onChange={(e) => setMaxRequests(Number(e.target.value) || 0)}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Including failed requests, 0 = unlimited')}
            </p>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='user-rate-limit-success'>
              {t('Max successful requests')}
            </Label>
            <Input
              id='user-rate-limit-success'
              type='number'
              min={1}
              step={1}
              value={maxSuccess}
              onChange={(e) => setMaxSuccess(Number(e.target.value) || 1)}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Only successful requests')}
            </p>
          </div>
        </div>
      )}

      {error && <p className='text-destructive text-xs'>{error}</p>}

      <div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={handleSave}
          disabled={saving || !state}
        >
          {saving ? t('Saving...') : t('Save rate limit')}
        </Button>
      </div>
    </div>
  )
}
