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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosAdapter } from 'axios'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { usageLogSchema } from '../../data/schema'
import { UsageLogsProvider, useLogsViewScope } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

const originalAdapter = api.defaults.adapter

function LogsFixture() {
  const { setViewScope } = useLogsViewScope()
  return (
    <>
      {(['all', 'self', 'others'] as const).map((scope) => (
        <button key={scope} type='button' onClick={() => setViewScope(scope)}>
          Scope {scope}
        </button>
      ))}
      <UsageLogsTable logCategory='common' />
    </>
  )
}

async function renderLogs(props: {
  role: number
  enabledPlans: boolean[]
  activeSubscription: boolean
  initialEntry?: string
  mockRequests?: boolean
}) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'tester', role: props.role })
  const records = ['wallet', 'subscription'].map((source, index) =>
    usageLogSchema.parse({
      id: index + 1,
      user_id: 1,
      created_at: 1788840000,
      type: 2,
      content: '',
      quota: 5000,
      other: JSON.stringify({ billing_source: source }),
    })
  )
  const respond: typeof api.get = vi.fn(async (url) => {
    let data: unknown = { quota: 0, rpm: 0, tpm: 0 }
    if (url === '/api/subscription/admin/plans') {
      data = props.enabledPlans.map((enabled) => ({ plan: { enabled } }))
    } else if (url === '/api/subscription/self') {
      data = {
        subscriptions: props.activeSubscription
          ? [{ subscription: { status: 'active' } }]
          : [],
        all_subscriptions: [{ subscription: { status: 'expired' } }],
      }
    } else if (
      url.startsWith('/api/log?') ||
      url.startsWith('/api/log/self?')
    ) {
      data = { items: records, total: records.length }
    } else if (url === '/api/group/') {
      data = []
    } else if (url === '/api/user/self/groups') {
      data = {}
    }
    return { data: { success: true, data } }
  })
  if (props.mockRequests !== false) {
    vi.spyOn(api, 'get').mockImplementation(respond)
  }
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: LogsFixture,
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({
      initialEntries: [props.initialEntry ?? '/usage-logs/common'],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <UsageLogsProvider>
        <RouterProvider router={router} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
  await screen.findAllByText('$0.01')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return client
}

afterEach(() => {
  cleanup()
  api.defaults.adapter = originalAdapter
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  localStorage.clear()
})

test.each([{ enabledPlans: [] }, { enabledPlans: [false] }])(
  'hides the wallet icon in admin view when no plan is enabled but keeps the subscription marker ($enabledPlans)',
  async ({ enabledPlans }) => {
    await renderLogs({
      role: ROLE.ADMIN,
      enabledPlans,
      activeSubscription: true,
    })

    expect(
      screen.queryByRole('img', { name: 'Wallet' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Subscription' })).toBeVisible()
  }
)

test('shows both source icons in admin view when any system plan is enabled', async () => {
  await renderLogs({
    role: ROLE.ADMIN,
    enabledPlans: [false, true],
    activeSubscription: false,
  })

  expect(screen.getByRole('img', { name: 'Wallet' })).toBeVisible()
  expect(screen.getByRole('img', { name: 'Subscription' })).toBeVisible()
})

test('keeps the subscription marker on a subscription-billed log for a user with only expired subscriptions', async () => {
  await renderLogs({
    role: ROLE.USER,
    enabledPlans: [true],
    activeSubscription: false,
  })

  expect(screen.queryByRole('img', { name: 'Wallet' })).not.toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'Subscription' })).toBeVisible()
})

test('shows both source icons for a user with an active subscription', async () => {
  await renderLogs({
    role: ROLE.USER,
    enabledPlans: [],
    activeSubscription: true,
  })

  expect(screen.getByRole('img', { name: 'Wallet' })).toBeVisible()
  expect(screen.getByRole('img', { name: 'Subscription' })).toBeVisible()
})

test.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
  'preserves administrator columns, source visibility and preferences in every row scope for role %s',
  async (role) => {
    const user = userEvent.setup()
    const tier = role === ROLE.SUPER_ADMIN ? 'root' : 'admin'
    const preferenceKey = `usage-logs:common:${tier}:column-visibility`
    localStorage.setItem(preferenceKey, JSON.stringify({ token_name: false }))
    const client = await renderLogs({
      role,
      enabledPlans: [true],
      activeSubscription: false,
    })
    const headers = () =>
      screen.getAllByRole('columnheader').map((header) => header.textContent)
    const originalHeaders = headers()
    expect(originalHeaders).toContain('Channel')
    expect(originalHeaders).toContain('User')
    expect(originalHeaders).not.toContain('Token')
    expect(screen.getByRole('img', { name: 'Wallet' })).toBeVisible()

    for (const scope of ['self', 'others', 'all']) {
      await user.click(screen.getByRole('button', { name: `Scope ${scope}` }))
      await waitFor(() => {
        const urls = vi.mocked(api.get).mock.calls.map(([url]) => String(url))
        for (const path of ['/api/log?', '/api/log/stat?']) {
          expect(
            urls.some(
              (url) =>
                url.startsWith(path) &&
                new URL(url, 'http://localhost').searchParams.get(
                  'user_scope'
                ) === scope
            )
          ).toBe(true)
        }
      })
      await waitFor(() => expect(client.isFetching()).toBe(0))
      expect(headers()).toEqual(originalHeaders)
      expect(screen.getByRole('img', { name: 'Wallet' })).toBeVisible()
      expect(screen.getByRole('img', { name: 'Subscription' })).toBeVisible()
      expect(JSON.parse(localStorage.getItem(preferenceKey) ?? '{}')).toEqual({
        token_name: false,
      })
      expect(
        localStorage.getItem('usage-logs:common:self:column-visibility')
      ).toBeNull()
    }
    const urls = vi.mocked(api.get).mock.calls.map(([url]) => String(url))
    expect(urls.some((url) => url.startsWith('/api/log/self'))).toBe(false)
    expect(urls).not.toContain('/api/subscription/self')
    expect(urls).toContain('/api/subscription/admin/plans')
  }
)

test('keeps every list request on the applied URL predicate while table filters catch up', async () => {
  const user = userEvent.setup()
  const client = await renderLogs({
    role: ROLE.ADMIN,
    enabledPlans: [],
    activeSubscription: false,
    initialEntry: '/usage-logs/common?model=previous-model',
  })
  const model = screen.getByPlaceholderText('Model Name')
  await user.clear(model)
  await user.type(model, 'current-model')
  await user.click(screen.getByRole('button', { name: /^Expand/ }))
  await user.type(screen.getByPlaceholderText('Request ID'), 'current-request')
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() => {
    const urls = vi.mocked(api.get).mock.calls.map(([url]) => String(url))
    for (const path of ['/api/log?', '/api/log/stat?']) {
      expect(
        urls.some(
          (url) =>
            url.startsWith(path) &&
            new URL(url, 'http://localhost').searchParams.get('request_id') ===
              'current-request'
        )
      ).toBe(true)
    }
  })
  await waitFor(() => expect(client.isFetching()).toBe(0))
  for (const [url] of vi.mocked(api.get).mock.calls) {
    if (!url.startsWith('/api/log?') && !url.startsWith('/api/log/stat?')) {
      continue
    }
    const params = new URL(url, 'http://localhost').searchParams
    if (params.get('request_id') !== 'current-request') continue
    expect(params.get('model_name')).toBe('current-model')
    expect(params.get('user_scope')).toBe('all')
  }
})

test('cancels a pending root read on same-SID demotion and keeps the fresh role projection', async () => {
  const session = {
    sid: 'r33-same-session',
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  }
  useAuthStore.setState((state) => ({ auth: { ...state.auth, session } }))
  let finishRoot: (() => void) | undefined
  let finishAdmin: (() => void) | undefined
  const listRequests: Array<{
    role: number | undefined
    url: string
    signal: AbortSignal | undefined
  }> = []
  const adapter: AxiosAdapter = async (config) => {
    const url = config.url ?? ''
    let data: unknown = { quota: 0, rpm: 0, tpm: 0 }
    if (url.startsWith('/api/log?') || url.startsWith('/api/log/self?')) {
      const role = useAuthStore.getState().auth.user?.role
      listRequests.push({
        role,
        url,
        signal: config.signal as AbortSignal | undefined,
      })
      if (listRequests.length === 2) {
        await new Promise<void>((resolve) => {
          finishRoot = resolve
        })
      } else if (role === ROLE.ADMIN) {
        await new Promise<void>((resolve) => {
          finishAdmin = resolve
        })
      }
      let model = 'self-current-row'
      if (role === ROLE.SUPER_ADMIN) model = 'root-only-row'
      if (role === ROLE.ADMIN) model = 'admin-current-row'
      const record = usageLogSchema.parse({
        id: 1,
        user_id: 1,
        created_at: 1_788_840_000,
        type: 2,
        content: '',
        model_name: model,
        quota: 5000,
        other:
          role === ROLE.SUPER_ADMIN
            ? JSON.stringify({
                root_info: { upstream_task_id: 'root-retained-evidence' },
              })
            : '',
      })
      data = { items: [record], total: 1 }
    } else if (
      url === '/api/subscription/admin/plans' ||
      url === '/api/group/'
    ) {
      data = []
    } else if (url === '/api/subscription/self') {
      data = { subscriptions: [] }
    } else if (url === '/api/user/self/groups') {
      data = {}
    }
    return {
      data: { success: true, data },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  api.defaults.adapter = adapter
  const client = await renderLogs({
    role: ROLE.SUPER_ADMIN,
    enabledPlans: [],
    activeSubscription: false,
    initialEntry:
      '/usage-logs/common?startTime=1788825600000&endTime=1788912000000',
    mockRequests: false,
  })
  expect(screen.getByText('root-only-row')).toBeVisible()
  let pending: Promise<void> | undefined
  await act(async () => {
    pending = client.invalidateQueries({ queryKey: ['logs'] })
  })
  try {
    await waitFor(() => expect(listRequests).toHaveLength(2))
    const rootRequest = listRequests[1]
    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 1,
        username: 'tester',
        role: ROLE.ADMIN,
      })
    })
    await waitFor(() => expect(listRequests).toHaveLength(3))
    expect(listRequests[2].url).toBe(rootRequest.url)
    expect(listRequests[2].role).toBe(ROLE.ADMIN)
    expect(rootRequest.signal).toBeInstanceOf(AbortSignal)
    expect(rootRequest.signal?.aborted).toBe(true)
    expect(listRequests[2].signal).toBeInstanceOf(AbortSignal)
    expect(listRequests[2].signal).not.toBe(rootRequest.signal)
    expect(screen.queryByText('root-only-row')).not.toBeInTheDocument()

    await act(async () => {
      finishAdmin?.()
    })
    expect(await screen.findByText('admin-current-row')).toBeVisible()
    await waitFor(() => expect(client.isFetching()).toBe(0))
    await act(async () => {
      finishRoot?.()
      await pending
    })
    expect(screen.getByText('admin-current-row')).toBeVisible()
    expect(screen.queryByText('root-only-row')).not.toBeInTheDocument()
    expect(
      JSON.stringify(
        client.getQueriesData({ queryKey: ['logs', 'common', 'admin'] })
      )
    ).not.toContain('root-retained-evidence')
    expect(useAuthStore.getState().auth.session?.sid).toBe(session.sid)

    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 1,
        username: 'tester',
        role: ROLE.USER,
      })
    })
    expect(await screen.findByText('self-current-row')).toBeVisible()
    expect(
      screen.queryByRole('columnheader', { name: 'User' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('columnheader', { name: 'Channel' })
    ).not.toBeInTheDocument()
    expect(listRequests.at(-1)?.url).toMatch(/^\/api\/log\/self\?/)
    expect(listRequests.at(-1)?.signal).toBeInstanceOf(AbortSignal)
    expect(useAuthStore.getState().auth.session?.sid).toBe(session.sid)
  } finally {
    finishAdmin?.()
    finishRoot?.()
    await pending
    client.clear()
  }
})

test('keeps an ordinary user self-only despite a forged URL scope and scope controls', async () => {
  const user = userEvent.setup()
  const client = await renderLogs({
    role: ROLE.USER,
    enabledPlans: [],
    activeSubscription: false,
    initialEntry: '/usage-logs/common?user_scope=others&username=bob&channel=9',
  })
  const headers = screen
    .getAllByRole('columnheader')
    .map((header) => header.textContent)
  expect(headers).not.toContain('Channel')
  expect(headers).not.toContain('User')
  await user.click(screen.getByRole('button', { name: 'Scope all' }))
  await user.click(screen.getByRole('button', { name: 'Scope others' }))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const urls = vi.mocked(api.get).mock.calls.map(([url]) => String(url))
  const logUrls = urls.filter((url) => url.startsWith('/api/log'))
  expect(logUrls.length).toBeGreaterThan(0)
  for (const url of logUrls) {
    expect(url).toMatch(/^\/api\/log\/self(?:\/stat)?\?/)
    const params = new URL(url, 'http://localhost').searchParams
    expect(params.has('user_scope')).toBe(false)
    expect(params.has('username')).toBe(false)
    expect(params.has('channel')).toBe(false)
  }
  expect(urls).not.toContain('/api/subscription/admin/plans')
})
