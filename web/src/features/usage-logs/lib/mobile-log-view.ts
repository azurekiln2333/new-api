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
/**
 * Mobile presentation of the common usage-log list.
 *
 * `compact` renders one dense two-line row per log (default), `card` keeps the
 * previous stacked card. The choice is persisted per browser so the list does
 * not flip back after navigation or reload.
 */
export const MOBILE_LOG_VIEW_MODES = {
  COMPACT: 'compact',
  CARD: 'card',
} as const

export type MobileLogViewMode =
  (typeof MOBILE_LOG_VIEW_MODES)[keyof typeof MOBILE_LOG_VIEW_MODES]

export const MOBILE_LOG_VIEW_STORAGE_KEY = 'usage-logs:mobile-view-mode'

export const DEFAULT_MOBILE_LOG_VIEW_MODE: MobileLogViewMode =
  MOBILE_LOG_VIEW_MODES.COMPACT

export function isMobileLogViewMode(
  value: unknown
): value is MobileLogViewMode {
  return (
    value === MOBILE_LOG_VIEW_MODES.COMPACT ||
    value === MOBILE_LOG_VIEW_MODES.CARD
  )
}

function resolveStorage(): Storage | null {
  if (typeof window === 'undefined') {
    return null
  }
  try {
    return window.localStorage
  } catch {
    // Access itself throws in some privacy configurations.
    return null
  }
}

export function readMobileLogViewMode(
  storage: Pick<Storage, 'getItem'> | null = resolveStorage()
): MobileLogViewMode {
  if (!storage) return DEFAULT_MOBILE_LOG_VIEW_MODE
  try {
    const stored = storage.getItem(MOBILE_LOG_VIEW_STORAGE_KEY)
    return isMobileLogViewMode(stored) ? stored : DEFAULT_MOBILE_LOG_VIEW_MODE
  } catch {
    return DEFAULT_MOBILE_LOG_VIEW_MODE
  }
}

export function writeMobileLogViewMode(
  mode: MobileLogViewMode,
  storage: Pick<Storage, 'setItem'> | null = resolveStorage()
): void {
  if (!storage) return
  try {
    storage.setItem(MOBILE_LOG_VIEW_STORAGE_KEY, mode)
  } catch {
    // Storage can be full or unavailable; the in-memory choice still applies.
  }
}
