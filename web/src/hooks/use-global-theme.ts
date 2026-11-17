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
import { useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { useCallback, useEffect, useMemo } from 'react'

import { updateSystemOption } from '@/features/system-settings/api'
import { useStatus } from '@/hooks/use-status'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import {
  parseThemeSettings,
  resolveGlobalThemeSettings,
  serializeThemeSettings,
  THEME_SETTINGS_OPTION_KEY,
  type ThemeSettings,
} from '@/lib/theme-customization'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Read the administrator-wide theme from the shared `/api/status` cache.
 *
 * `/api/status` is public, so this applies on sign-in and public pages alike.
 * Every provider below is mounted above the router, so they all subscribe to
 * the same cache entry and React Query dedupes the request.
 */
export function useGlobalTheme(): ThemeSettings {
  const { status } = useStatus()
  const raw = status?.ThemeSettings
  return resolveGlobalThemeSettings(
    parseThemeSettings(typeof raw === 'string' ? raw : '')
  )
}

/**
 * Whether the current user may edit the global theme.
 *
 * Writing an option requires root (`PUT /api/option/` is behind `RootAuth`),
 * so only a super admin is offered the theme drawer. The server remains the
 * authority: this gate only decides whether the control is rendered.
 */
export function useCanEditGlobalTheme(): boolean {
  return useAuthStore((state) => state.auth.user?.role === ROLE.SUPER_ADMIN)
}

/**
 * Serialised global-theme writes.
 *
 * Several controls can fire in one event — the drawer's "Reset all" clears the
 * mode, direction, layout and customization providers in a single click. Each
 * patch merges against the stored value, and the stored value only refreshes
 * after the status query round-trip, so concurrent writes would all merge from
 * the same pre-click snapshot and the last response would win with a stale
 * field still in place. Running the writes in order against an accumulated
 * value makes the result deterministic.
 */
let accumulatedTheme: ThemeSettings | null = null
let pendingWrite: Promise<unknown> = Promise.resolve()

/** Drop the accumulated value, e.g. after the stored theme changed elsewhere. */
function resetAccumulatedTheme() {
  accumulatedTheme = null
}

/**
 * Merge a patch into the stored global theme and save it.
 *
 * Uses the same `PUT /api/option/` contract as the option hooks but stays
 * silent on success: theme controls fire on every radio click, so a toast per
 * change would be noise. Failures still surface through `handleServerError`.
 */
export function useUpdateGlobalTheme() {
  const queryClient = useQueryClient()
  const { status } = useStatus()
  const raw = status?.ThemeSettings

  const stored = useMemo(
    () => parseThemeSettings(typeof raw === 'string' ? raw : ''),
    [raw]
  )

  // A new server value means another administrator (or this user in another
  // tab) wrote the option; stop merging into a value we no longer own.
  useEffect(() => {
    resetAccumulatedTheme()
  }, [raw])

  return useCallback(
    (patch: ThemeSettings) => {
      const next = { ...(accumulatedTheme ?? stored), ...patch }
      accumulatedTheme = next
      const previous = pendingWrite

      pendingWrite = (async () => {
        await previous
        try {
          requireServerSuccess(
            await updateSystemOption({
              key: THEME_SETTINGS_OPTION_KEY,
              value: serializeThemeSettings(next),
            })
          )
        } catch (error) {
          // Drop the accumulated value so the next write starts from what the
          // server actually holds rather than a value it rejected.
          resetAccumulatedTheme()
          handleServerError(error, i18next.t('Failed to save theme settings'))
          return
        }
        await queryClient.invalidateQueries({ queryKey: ['status'] })
      })()
    },
    [queryClient, stored]
  )
}
