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
/* eslint-disable react-refresh/only-export-components */
import { getRouteApi } from '@tanstack/react-router'
import { createContext, useContext, useState, type ReactNode } from 'react'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  readMobileLogViewMode,
  writeMobileLogViewMode,
  type MobileLogViewMode,
} from '../lib/mobile-log-view'
import type { AdminLogUserScope, ChannelAffinityInfo } from '../types'

const route = getRouteApi('/_authenticated/usage-logs/$section')

export type LogsViewScope = AdminLogUserScope
export type LogsViewAccess = 'self' | 'admin' | 'root'

export function resolveLogsViewAccess(role: number): LogsViewAccess {
  if (role < ROLE.ADMIN) return 'self'
  return role === ROLE.SUPER_ADMIN ? 'root' : 'admin'
}

interface UsageLogsContextValue {
  selectedUserId: number | null
  setSelectedUserId: (userId: number | null) => void
  userInfoDialogOpen: boolean
  setUserInfoDialogOpen: (open: boolean) => void
  affinityTarget: ChannelAffinityInfo | null
  setAffinityTarget: (target: ChannelAffinityInfo | null) => void
  affinityDialogOpen: boolean
  setAffinityDialogOpen: (open: boolean) => void
  sensitiveVisible: boolean
  setSensitiveVisible: (visible: boolean) => void
  viewScope: LogsViewScope
  setViewScope: (scope: LogsViewScope) => void
  mobileLogViewMode: MobileLogViewMode
  setMobileLogViewMode: (mode: MobileLogViewMode) => void
}

const UsageLogsContext = createContext<UsageLogsContextValue | undefined>(
  undefined
)

export function UsageLogsProvider({ children }: { children: ReactNode }) {
  const [selectedUserId, setSelectedUserId] = useState<number | null>(null)
  const [userInfoDialogOpen, setUserInfoDialogOpen] = useState(false)
  const [affinityTarget, setAffinityTarget] =
    useState<ChannelAffinityInfo | null>(null)
  const [affinityDialogOpen, setAffinityDialogOpen] = useState(false)
  const [sensitiveVisible, setSensitiveVisible] = useState(true)
  const [viewScope, setViewScope] = useState<LogsViewScope>('all')
  const [mobileLogViewMode, setMobileLogViewModeState] =
    useState<MobileLogViewMode>(readMobileLogViewMode)

  const setMobileLogViewMode = (mode: MobileLogViewMode) => {
    setMobileLogViewModeState(mode)
    writeMobileLogViewMode(mode)
  }

  return (
    <UsageLogsContext.Provider
      value={{
        selectedUserId,
        setSelectedUserId,
        userInfoDialogOpen,
        setUserInfoDialogOpen,
        affinityTarget,
        setAffinityTarget,
        affinityDialogOpen,
        setAffinityDialogOpen,
        sensitiveVisible,
        setSensitiveVisible,
        viewScope,
        setViewScope,
        mobileLogViewMode,
        setMobileLogViewMode,
      }}
    >
      {children}
    </UsageLogsContext.Provider>
  )
}

export function useUsageLogsContext() {
  const context = useContext(UsageLogsContext)
  if (!context) {
    throw new Error('useUsageLogsContext must be used within UsageLogsProvider')
  }
  return context
}

/** Common-log row scope never changes the authenticated viewer's capabilities.
 * Drawing/task logs retain their existing all/self endpoint behavior.
 */
export function useLogsViewScope() {
  const role = useAuthStore((state) => state.auth.user?.role ?? ROLE.GUEST)
  const context = useUsageLogsContext()
  const search = route.useSearch()
  const { section } = route.useParams()
  const navigate = route.useNavigate()
  const isCommon = section === 'common'
  const canManageScope = role >= ROLE.ADMIN
  let requestedScope: LogsViewScope = context.viewScope === 'self' ? 'self' : 'all'
  if (isCommon) requestedScope = search.user_scope ?? 'all'
  const viewScope = canManageScope ? requestedScope : 'self'
  const viewAccess =
    !isCommon && viewScope === 'self' ? 'self' : resolveLogsViewAccess(role)
  const isAdminView = viewAccess !== 'self'
  const isRootView = viewAccess === 'root'
  const userScope = viewScope
  const setViewScope = (scope: LogsViewScope) => {
    if (!canManageScope || (!isCommon && scope === 'others')) return
    context.setViewScope(scope)
    void navigate({
      search: (previous) => ({
        ...previous,
        user_scope: isCommon ? scope : undefined,
        page: 1,
      }),
    })
  }

  return {
    canManageScope,
    viewScope,
    userScope,
    setViewScope,
    isAdminView,
    isRootView,
    viewAccess,
  }
}
