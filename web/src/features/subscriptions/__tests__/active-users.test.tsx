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
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { ActiveUsersTable } from '../components/active-users-table'
import { Subscriptions } from '../index'
import { formatTimestamp } from '../lib'
import type {
  ApiResponse,
  EffectiveSubscriptionPage,
  EffectiveSubscriptionUser,
} from '../types'

const clients: QueryClient[] = []

const adminBundle: AuthBundle = {
  access_token: 'subscriptions-admin-token',
  token_type: 'Bearer',
  access_expires_at: 9_999_999_999,
  user: { id: 91, username: 'subscriptions-admin', role: ROLE.ADMIN },
  session: {
    sid: 'subscriptions-admin-session',
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 9_999_999_999,
  },
}

const rows: EffectiveSubscriptionUser[] = [
  {
    subscription_id: 101,
    user_id: 7,
    username: 'alice',
    display_name: 'Alice Example',
    plan_id: 3,
    plan_title: 'Pro',
    start_time: 1_700_000_000,
    end_time: 1_700_100_000,
  },
  {
    subscription_id: 102,
    user_id: 7,
    username: 'alice',
    display_name: 'Alice Example',
    plan_id: 4,
    plan_title: 'Team',
    start_time: 1_700_000_100,
    end_time: 1_700_200_000,
  },
]

const nextPageRow: EffectiveSubscriptionUser = {
  ...rows[0],
  subscription_id: 103,
  user_id: 8,
  username: 'bob',
  display_name: 'Bob Example',
}

type SubscriptionResponse = { data: ApiResponse<EffectiveSubscriptionPage> }

const replacementResponse: SubscriptionResponse = {
  data: {
    success: true,
    data: {
      items: [
        {
          ...nextPageRow,
          plan_title: 'Replacement plan',
          start_time: 1_800_000_000,
          end_time: 1_800_100_000,
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
    },
  },
}

function pendingSubscriptionResponse() {
  let resolve!: (response: SubscriptionResponse) => void
  const promise = new Promise<SubscriptionResponse>((finish) => {
    resolve = finish
  })
  return { promise, resolve }
}

function renderWithClient(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const view = render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
  return { ...view, client }
}

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

describe('effective subscription users table', () => {
  beforeEach(() => {
    useAuthStore.getState().auth.reset()
    useAuthStore.getState().auth.setBundle(adminBundle)
    vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
      if (url === '/api/subscription/admin/plans' || url === '/api/option/') {
        return { data: { success: true, data: [] } }
      }
      if (url !== '/api/subscription/admin/active-users') {
        throw new Error(`Unexpected request: ${url}`)
      }
      const params = config?.params as { p?: number; page_size?: number }
      const page = params?.p ?? 1
      return {
        data: {
          success: true,
          data: {
            items: page === 1 ? rows : [nextPageRow],
            total: 21,
            page,
            page_size: params?.page_size ?? 20,
          },
        },
      }
    })
  })

  it('shows one row for every returned subscription and its user/plan dates', async () => {
    renderWithClient(<ActiveUsersTable />)

    expect(await screen.findAllByText('alice')).toHaveLength(2)
    expect(screen.getAllByText('Alice Example')).toHaveLength(2)
    expect(screen.getByText('Pro')).toBeVisible()
    expect(screen.getByText('Team')).toBeVisible()
    expect(
      screen.getAllByText(formatTimestamp(rows[0].start_time))
    ).toHaveLength(1)
    expect(
      screen.getAllByText(formatTimestamp(rows[0].end_time))
    ).toHaveLength(1)
    expect(screen.getAllByText('alice')).toHaveLength(2)
  })

  it('keeps active-user pagination in the page footer across tab switches and remote pages', async () => {
    const user = userEvent.setup()
    renderWithClient(<Subscriptions />)
    const main = screen.getByRole('main')
    const plansTab = screen.getByRole('tab', { name: 'Subscription Plans' })
    const activeUsersTab = screen.getByRole('tab', { name: 'active users' })
    expect(plansTab).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText('No subscription plans yet')).toBeVisible()

    await user.click(activeUsersTab)
    const panel = screen.getByRole('tabpanel', { name: 'active users' })
    expect(await within(panel).findByText('Pro')).toBeVisible()
    expect(activeUsersTab).toHaveAttribute('aria-selected', 'true')
    expect(plansTab).toHaveAttribute('aria-selected', 'false')

    const next = screen.getByRole('button', { name: 'Go to next page' })
    const previous = screen.getByRole('button', {
      name: 'Go to previous page',
    })
    // The non-scrolling page footer must own pagination, not the clipped tab.
    expect(main.lastElementChild).toHaveClass('shrink-0')
    expect(main.lastElementChild).toContainElement(next)
    expect(main.lastElementChild).toContainElement(previous)
    expect(panel).not.toContainElement(next)
    expect(next).toBeVisible()
    expect(next).toBeEnabled()
    expect(previous).toBeDisabled()

    await user.click(next)
    expect(await within(panel).findByText('bob')).toBeVisible()
    expect(api.get).toHaveBeenCalledWith(
      '/api/subscription/admin/active-users',
      {
        params: { p: 2, page_size: 20 },
        signal: expect.any(AbortSignal),
      }
    )
    expect(within(panel).queryByText('alice')).not.toBeInTheDocument()
    expect(next).toBeDisabled()
    expect(previous).toBeEnabled()

    await user.click(previous)
    expect(await within(panel).findAllByText('alice')).toHaveLength(2)
    expect(previous).toBeDisabled()
    expect(next).toBeEnabled()

    await user.click(plansTab)
    expect(plansTab).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText('No subscription plans yet')).toBeVisible()
    // Switching back must remove the active-user portal, not leave two pagers.
    await waitFor(() => {
      expect(
        screen.getAllByRole('button', { name: 'Go to next page' })
      ).toHaveLength(1)
    })
    const plansNext = screen.getByRole('button', { name: 'Go to next page' })
    expect(main.lastElementChild).toContainElement(plansNext)
    expect(plansNext).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toBeDisabled()
  })

  it('hides populated admin cache and stops requests after a same-SID demotion', async () => {
    const { client } = renderWithClient(<ActiveUsersTable />)
    expect(await screen.findAllByText('alice')).toHaveLength(2)
    const populated = client.getQueryCache().find({
      queryKey: ['admin-effective-subscriptions'],
      exact: false,
    })
    expect(populated).toBeDefined()
    expect(populated?.state.data).toEqual(
      expect.objectContaining({ items: rows })
    )

    await act(async () => {
      useAuthStore.getState().auth.setBundle({
        ...adminBundle,
        user: { ...adminBundle.user, role: ROLE.USER },
      })
    })

    expect(useAuthStore.getState().auth.session?.sid).toBe(
      adminBundle.session.sid
    )
    // No global cache clear: the live render guard must hide the old payload.
    expect(populated?.state.data).toEqual(
      expect.objectContaining({ items: rows })
    )
    expect(screen.queryByText('alice')).not.toBeInTheDocument()
    expect(screen.queryByText('Alice Example')).not.toBeInTheDocument()
    expect(screen.queryByText('Pro')).not.toBeInTheDocument()
    expect(screen.queryByText('Team')).not.toBeInTheDocument()
    expect(
      screen.queryByText(formatTimestamp(rows[0].start_time))
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(formatTimestamp(rows[0].end_time))
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Retry' })
    ).not.toBeInTheDocument()

    await act(async () => {
      await client.invalidateQueries({
        queryKey: ['admin-effective-subscriptions'],
      })
    })
    expect(api.get).toHaveBeenCalledTimes(1)
  })

  it.each(['account', 'session', 'role'] as const)(
    'does not reuse populated rows or placeholders after a live %s change',
    async (change) => {
      renderWithClient(<ActiveUsersTable />)
      expect(await screen.findAllByText('alice')).toHaveLength(2)
      const pending = pendingSubscriptionResponse()
      const get = vi.mocked(api.get).mockReturnValueOnce(pending.promise)

      await act(async () => {
        const next = { ...adminBundle }
        if (change === 'account') {
          next.user = { ...adminBundle.user, id: 92 }
        } else if (change === 'session') {
          next.session = { ...adminBundle.session, sid: 'replacement-session' }
        } else {
          next.user = { ...adminBundle.user, role: ROLE.SUPER_ADMIN }
        }
        useAuthStore.getState().auth.setBundle(next)
      })

      expect(screen.queryByText('alice')).not.toBeInTheDocument()
      expect(screen.queryByText('Pro')).not.toBeInTheDocument()
      expect(screen.queryByText('Team')).not.toBeInTheDocument()
      await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
      expect(get.mock.calls[1]?.[1]?.signal).toBeInstanceOf(AbortSignal)

      await act(async () => {
        pending.resolve(replacementResponse)
        await pending.promise
      })
      expect(await screen.findByText('bob')).toBeVisible()
      expect(screen.getByText('Replacement plan')).toBeVisible()
      expect(screen.queryByText('alice')).not.toBeInTheDocument()
    }
  )

  it.each(['demotion', 'identity switch'] as const)(
    'aborts an in-flight admin read on %s and fences its late result',
    async (change) => {
      const pending = pendingSubscriptionResponse()
      const get = vi
        .mocked(api.get)
        .mockReturnValueOnce(pending.promise)
        .mockResolvedValueOnce(replacementResponse)
      const { client } = renderWithClient(<ActiveUsersTable />)
      await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
      const signal = get.mock.calls[0]?.[1]?.signal
      expect(signal).toBeInstanceOf(AbortSignal)
      const oldQuery = client.getQueryCache().find({
        queryKey: ['admin-effective-subscriptions'],
        exact: false,
      })
      expect(oldQuery).toBeDefined()

      await act(async () => {
        useAuthStore.getState().auth.setBundle({
          ...adminBundle,
          user: {
            ...adminBundle.user,
            id: change === 'demotion' ? adminBundle.user.id : 92,
            role: change === 'demotion' ? ROLE.USER : ROLE.ADMIN,
          },
          session: {
            ...adminBundle.session,
            sid:
              change === 'demotion'
                ? adminBundle.session.sid
                : 'replacement-session',
          },
        })
      })
      expect(signal?.aborted).toBe(true)
      if (change === 'identity switch') {
        expect(await screen.findByText('bob')).toBeVisible()
      }

      // This transport mock deliberately resolves even after cancellation.
      await act(async () => {
        pending.resolve({
          data: {
            success: true,
            data: { items: rows, total: 2, page: 1, page_size: 20 },
          },
        })
        await pending.promise
      })
      expect(oldQuery?.state.data).toBeUndefined()
      expect(screen.queryByText('alice')).not.toBeInTheDocument()
      expect(screen.queryByText('Pro')).not.toBeInTheDocument()
      expect(
        screen.queryByText(formatTimestamp(rows[0].end_time))
      ).not.toBeInTheDocument()
      if (change === 'demotion') {
        expect(screen.queryByRole('table')).not.toBeInTheDocument()
        expect(get).toHaveBeenCalledTimes(1)
      } else {
        expect(screen.getByText('bob')).toBeVisible()
        expect(screen.getByText('Replacement plan')).toBeVisible()
        expect(get).toHaveBeenCalledTimes(2)
      }
    }
  )

  it('exposes a retryable error state when the read fails', async () => {
    const get = vi.mocked(api.get)
    get.mockRejectedValueOnce(new Error('read failed'))
    renderWithClient(<ActiveUsersTable />)

    expect(await screen.findByText('read failed')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  })
})
