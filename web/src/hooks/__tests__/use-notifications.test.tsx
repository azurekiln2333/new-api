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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useNotificationStore } from '@/stores/notification-store'

import { useNotifications } from '../use-notifications'

let client: QueryClient

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
  useNotificationStore.setState({
    lastReadNotice: '',
    readAnnouncementKeys: [],
    closedUntilDate: null,
  })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['notice'], {
    success: true,
    data: 'Service notice',
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  useNotificationStore.setState({
    lastReadNotice: '',
    readAnnouncementKeys: [],
    closedUntilDate: null,
  })
  vi.unstubAllGlobals()
})

function renderNotifications(announcementsEnabled?: boolean) {
  client.setQueryData(['status'], {
    announcements_enabled: announcementsEnabled,
    announcements: [{ id: 'release', content: 'Release timeline' }],
  })
  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
  return renderHook(() => useNotifications(), { wrapper: Wrapper })
}

describe('notification center default tab', () => {
  it('opens the enabled timeline by default and marks its content read', () => {
    const { result } = renderNotifications(true)
    expect(result.current.unreadCount).toBe(2)

    act(() => result.current.setPopoverOpen(true))

    expect(result.current.popoverOpen).toBe(true)
    expect(result.current.activeTab).toBe('announcements')
    expect(result.current.announcements).toHaveLength(1)
    expect(result.current.unreadAnnouncementsCount).toBe(0)
    expect(result.current.unreadNoticeCount).toBe(0)
  })

  it.each([false, undefined])(
    'opens the notice when the timeline flag is %s',
    (enabled) => {
      const { result } = renderNotifications(enabled)

      act(() => result.current.setPopoverOpen(true))

      expect(result.current.popoverOpen).toBe(true)
      expect(result.current.activeTab).toBe('notice')
      expect(result.current.notice).toBe('Service notice')
      expect(result.current.announcements).toEqual([])
      expect(result.current.unreadCount).toBe(0)
    }
  )

  it('keeps an explicit notice selection without marking the enabled timeline read', () => {
    const { result } = renderNotifications(true)

    act(() => result.current.openPopover('notice'))

    expect(result.current.activeTab).toBe('notice')
    expect(result.current.unreadNoticeCount).toBe(0)
    expect(result.current.unreadAnnouncementsCount).toBe(1)
  })
})
