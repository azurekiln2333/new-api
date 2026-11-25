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
import {
  focusManager,
  onlineManager,
  queryOptions,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useEffect, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'

import { useStatus } from '@/hooks/use-status'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { statusQueryOptions } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import {
  fetchLatestSystemRelease,
  UpdateCheckError,
  type UpdateCheckErrorCode,
} from './api'
import {
  compareCustomBuildRelease,
  compareSystemVersions,
  parseCustomBuildVersion,
  resolveUpdateSourceRepository,
  type SystemRelease,
} from './releases'
import {
  subscribeSystemUpdatePreferences,
  useSystemUpdatePreferencesStore,
  useSystemUpdateStore,
  type SystemUpdateSnapshot,
} from './store'

export const SYSTEM_UPDATE_INTERVAL = 60 * 60 * 1000
export const SYSTEM_UPDATE_QUERY_KEY = ['system-update'] as const

function systemUpdateQueryKey(repository: string) {
  return [...SYSTEM_UPDATE_QUERY_KEY, repository] as const
}

/**
 * Order a running build against the latest release.
 *
 * Fork builds (`custom-YYYYMMDD-<sha>`) are dated, not version ranked, so they
 * compare on the release timeline. Everything else keeps the semver ordering
 * that the upstream release channel relies on.
 */
export function compareRunningVersion(
  currentVersion: string | null | undefined,
  release: SystemRelease | null | undefined
): -1 | 0 | 1 | null {
  if (!release) return null
  if (parseCustomBuildVersion(currentVersion)) {
    return compareCustomBuildRelease(currentVersion, release)
  }
  return compareSystemVersions(currentVersion, release.tag_name)
}

export function getUpdateErrorMessage(
  code: UpdateCheckErrorCode,
  t: TFunction
): string {
  switch (code) {
    case 'rate-limit':
      return t('GitHub rate limit reached. Try again later.')
    case 'timeout':
      return t('Update check timed out. Try again.')
    case 'payload':
      return t('Unexpected release payload')
    default:
      return t('Failed to check for updates')
  }
}

export const systemUpdateQueryOptions = (repository: string) => {
  const stored = useSystemUpdateStore.getState().snapshot
  const initialSnapshot = stored?.repository === repository ? stored : undefined
  return queryOptions({
    queryKey: systemUpdateQueryKey(repository),
    queryFn: async ({ signal, client }): Promise<SystemUpdateSnapshot> => {
      // Refresh the running server version too, so an open tab notices upgrades.
      void client
        .fetchQuery({ ...statusQueryOptions, meta: { errorToast: false } })
        .catch(() => undefined)
      let snapshot: SystemUpdateSnapshot
      try {
        const release = await fetchLatestSystemRelease(signal, repository)
        signal.throwIfAborted()
        const now = Date.now()
        snapshot = {
          repository,
          release,
          lastCheckedAt: now,
          lastAttemptAt: now,
          error: null,
        }
      } catch (error) {
        if (signal.aborted) throw error
        const storedSnapshot = useSystemUpdateStore.getState().snapshot
        const previous =
          client.getQueryData<SystemUpdateSnapshot>(
            systemUpdateQueryKey(repository)
          ) ??
          (storedSnapshot?.repository === repository ? storedSnapshot : null)
        // A failed attempt is also cached, so focus changes and remounts cannot
        // hammer GitHub while it is unavailable or rate limiting this browser.
        snapshot = {
          repository,
          release: previous?.release ?? null,
          lastCheckedAt: previous?.lastCheckedAt ?? 0,
          lastAttemptAt: Date.now(),
          error: error instanceof UpdateCheckError ? error.code : 'network',
        }
      }
      useSystemUpdateStore.getState().setSnapshot(snapshot)
      return snapshot
    },
    initialData: initialSnapshot,
    initialDataUpdatedAt: initialSnapshot?.lastAttemptAt,
    staleTime: SYSTEM_UPDATE_INTERVAL,
    gcTime: 24 * SYSTEM_UPDATE_INTERVAL,
    retry: false,
    meta: { errorToast: false },
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
    refetchIntervalInBackground: false,
    refetchInterval: (query) => {
      if (query.state.fetchStatus === 'fetching') return false
      return Math.max(
        1_000,
        SYSTEM_UPDATE_INTERVAL -
          (Date.now() - (query.state.data?.lastAttemptAt ?? 0))
      )
    },
  })
}

function isPageVisible(): boolean {
  return focusManager.isFocused()
}

function isBrowserOnline(): boolean {
  return (
    onlineManager.isOnline() &&
    (typeof navigator === 'undefined' || navigator.onLine)
  )
}

export function useSystemUpdate() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN
  const visible = useSyncExternalStore(focusManager.subscribe, isPageVisible)
  const online = useSyncExternalStore(onlineManager.subscribe, isBrowserOnline)
  const { status } = useStatus()
  const queryClient = useQueryClient()
  const repository = resolveUpdateSourceRepository(
    status?.update_source_repository
  )
  const query = useQuery({
    ...systemUpdateQueryOptions(repository),
    enabled: isAdmin && Boolean(status) && visible && online,
  })

  useEffect(() => {
    if (!isAdmin) {
      void queryClient.cancelQueries({ queryKey: SYSTEM_UPDATE_QUERY_KEY })
    }
  }, [isAdmin, queryClient])

  const version = status?.version?.trim()
  const currentVersion =
    version === 'v0.0.0' || version === '0.0.0'
      ? undefined
      : version || undefined
  const release = query.data?.release ?? null
  const comparison = compareRunningVersion(currentVersion, release)
  const hasUpdate = comparison === -1
  const isIgnored = useSyncExternalStore(subscribeSystemUpdatePreferences, () =>
    Boolean(
      user &&
      release &&
      useSystemUpdatePreferencesStore
        .getState()
        .ignoredVersionsByUserId[user.id]?.includes(release.tag_name)
    )
  )

  const setIgnored = (ignored: boolean) => {
    if (!isAdmin || !user || !release) return
    useSystemUpdatePreferencesStore
      .getState()
      .setVersionIgnored(user.id, release.tag_name, ignored)
  }

  const checkNow = async () => {
    if (!isAdmin || !online) return
    const result = await query.refetch({ cancelRefetch: false })
    if (result.data?.error) {
      handleServerError(new Error(getUpdateErrorMessage(result.data.error, t)))
    }
  }

  return {
    currentVersion,
    release,
    repository,
    comparison,
    hasUpdate,
    isIgnored,
    shouldNotify: isAdmin && hasUpdate && !isIgnored,
    setIgnored,
    checking: query.isFetching,
    online,
    snapshot: query.data,
    checkNow,
  }
}
