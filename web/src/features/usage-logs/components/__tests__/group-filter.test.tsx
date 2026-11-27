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
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import type { ChannelOptionsParams } from '@/features/channels/types'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { CommonLogsFilterBar } from '../common-logs-filter-bar'
import { UsageLogsProvider } from '../usage-logs-provider'

const pointerCaptureDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'setPointerCapture'
)

function FilterFixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <UsageLogsProvider>
      <CommonLogsFilterBar table={table} />
    </UsageLogsProvider>
  )
}

async function renderFilter(
  initialEntry = '/usage-logs/common',
  groups: Record<string, { desc: string; ratio: number }> | null = {
    default: { desc: '', ratio: 1 },
    premium: { desc: '', ratio: 2 },
  },
  channelOptions: Array<{
    id: number
    name: string
    models: string[]
  }> | null = []
) {
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url === '/api/channel/options') {
      if (channelOptions === null) {
        return { data: { success: false, message: 'Unavailable' } }
      }
      const params = config?.params as ChannelOptionsParams
      const selected = channelOptions.filter(
        (channel) => String(channel.id) === params.ids
      )
      const matches = channelOptions.filter(
        (channel) =>
          !params.keyword ||
          String(channel.id) === params.keyword ||
          channel.name.toLowerCase().includes(params.keyword.toLowerCase()) ||
          channel.models.some((model) =>
            model.toLowerCase().includes(params.keyword?.toLowerCase() ?? '')
          )
      )
      const page = params.p ?? 1
      const pageSize = params.page_size ?? 30
      const byId = new Map(
        [
          ...selected,
          ...matches.slice((page - 1) * pageSize, page * pageSize),
        ].map((channel) => [channel.id, channel])
      )
      return {
        data: {
          success: true,
          data: {
            items: [...byId.values()],
            page,
            page_size: pageSize,
            has_more: page * pageSize < matches.length,
          },
        },
      }
    }
    if (url === '/api/user/self/groups' || url === '/api/group/') {
      if (groups === null) throw new Error('Group loading failed')
      return {
        data: {
          success: true,
          data: url === '/api/group/' ? Object.keys(groups) : groups,
        },
      }
    }
    return { data: { success: true, data: { quota: 0, rpm: 0, tpm: 0 } } }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: FilterFixture,
    validateSearch: (search: Record<string, unknown>) => ({
      ...search,
      ...(search.channel != null ? { channel: String(search.channel) } : {}),
    }),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  if (window.matchMedia('(max-width: 640px)').matches) {
    await userEvent.click(
      await screen.findByRole('button', { name: /^Filter/ })
    )
  }
  await screen.findByRole('combobox', { name: 'Group' })
  return router
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.setUser(null)
  if (pointerCaptureDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'setPointerCapture',
      pointerCaptureDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'setPointerCapture')
  }
})

it('loads personal groups and filters choices without submitting until Search', async () => {
  const router = await renderFilter()
  const input = screen.getByRole('combobox', { name: 'Group' })
  await userEvent.click(input)
  expect(await screen.findByRole('option', { name: 'default' })).toBeVisible()
  await userEvent.type(input, 'prem')
  expect(
    screen.queryByRole('option', { name: 'default' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('option', { name: 'premium' }))
  expect(input).toHaveValue('premium')
  expect(router.state.location.search).not.toHaveProperty('group')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      group: 'premium',
      page: 1,
    })
  )
  expect(api.get).toHaveBeenCalledWith('/api/user/self/groups')
  expect(api.get).not.toHaveBeenCalledWith('/api/group/')
})

it('loads all groups in the administrator view', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  await renderFilter()
  await userEvent.click(screen.getByRole('combobox', { name: 'Group' }))
  expect(await screen.findByRole('option', { name: 'premium' })).toBeVisible()
  expect(api.get).toHaveBeenCalledWith('/api/group/')
  expect(api.get).not.toHaveBeenCalledWith('/api/user/self/groups')
})

it('confirms a keyboard choice before Enter submits the selected group', async () => {
  const router = await renderFilter()
  const input = screen.getByRole('combobox', { name: 'Group' })
  await userEvent.click(input)
  await screen.findByRole('option', { name: 'default' })
  await userEvent.keyboard('{ArrowDown}{Enter}')
  expect(input).toHaveValue('default')
  expect(input).toHaveAttribute('aria-expanded', 'false')
  expect(router.state.location.search).not.toHaveProperty('group')
  await userEvent.keyboard('{Enter}')
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ group: 'default' })
  )
})

it.each([{}, null])(
  'preserves historical input and supports clearing when groups are unavailable (%s)',
  async (groups) => {
    const router = await renderFilter(
      '/usage-logs/common?group=retired',
      groups
    )
    const input = screen.getByRole('combobox', { name: 'Group' })
    expect(input).toHaveValue('retired')
    await userEvent.clear(input)
    await userEvent.type(input, 'historical')
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        group: 'historical',
      })
    )
    await userEvent.clear(input)
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(router.state.location.search).not.toHaveProperty('group')
    )
  }
)

it('resets the selected group and restores a group from URL navigation', async () => {
  const router = await renderFilter('/usage-logs/common?group=premium')
  const input = screen.getByRole('combobox', { name: 'Group' })
  expect(input).toHaveValue('premium')
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() => expect(input).toHaveValue(''))
  expect(router.state.location.search).not.toHaveProperty('group')
  await router.history.push('/usage-logs/common?group=retired')
  await waitFor(() => expect(input).toHaveValue('retired'))
})

it('keeps a selected group visible on focus and can clear it without choosing another option', async () => {
  const router = await renderFilter('/usage-logs/common?group=premium')
  const input = screen.getByRole('combobox', { name: 'Group' })
  await userEvent.click(input)
  expect(input).toHaveValue('premium')
  expect(await screen.findByRole('option', { name: 'default' })).toBeVisible()
  await userEvent.clear(input)
  await userEvent.keyboard('{Enter}')
  await waitFor(() =>
    expect(router.state.location.search).not.toHaveProperty('group')
  )
})

it('keeps the compact input and masks the dropdown together with other sensitive filters', async () => {
  await renderFilter()
  const input = screen.getByRole('combobox', { name: 'Group' })
  expect(input).toHaveClass('h-8', 'text-sm', 'leading-5')
  await userEvent.click(screen.getByRole('button', { name: /^Hide$/ }))
  await userEvent.click(input)
  const option = await screen.findByRole('option', { name: 'premium' })
  const maskedField = input.closest('.\\[-webkit-text-security\\:disc\\]')
  expect(maskedField).not.toBeNull()
  expect(option.closest('.\\[-webkit-text-security\\:disc\\]')).not.toBeNull()
  await userEvent.keyboard('{Escape}')
  expect(input).toHaveAttribute('aria-expanded', 'false')
  await userEvent.tab()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('lets mobile users select a long group name inside the filter drawer and submit it', async () => {
  Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', {
    configurable: true,
    value: vi.fn(),
  })
  const originalMatchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...originalMatchMedia(query),
    matches: query === '(max-width: 640px)',
  }))
  const longGroup = 'enterprise-team-with-a-long-group-name'
  const router = await renderFilter('/usage-logs/common', {
    [longGroup]: { desc: '', ratio: 1 },
  })
  const dialog = screen.getByRole('dialog')
  const input = within(dialog).getByRole('combobox', { name: 'Group' })
  await userEvent.click(input)
  const option = await within(dialog).findByRole('option', { name: longGroup })
  expect(option).toBeVisible()
  await userEvent.click(option)
  expect(input).toHaveValue(longGroup)
  expect(dialog).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ group: longGroup })
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

it.each([1, 10])(
  'excludes only auto from group choices for role %s',
  async (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role })
    const router = await renderFilter('/usage-logs/common', {
      auto: { desc: '', ratio: 1 },
      'auto-team': { desc: '', ratio: 1 },
    })
    const input = screen.getByRole('combobox', { name: 'Group' })
    await userEvent.click(input)
    const option = await screen.findByRole('option', { name: 'auto-team' })
    expect(
      screen.queryByRole('option', { name: 'auto' })
    ).not.toBeInTheDocument()
    await userEvent.click(option)
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ group: 'auto-team' })
    )
  }
)

async function openChannelPicker() {
  const expand = screen.queryByRole('button', { name: /^Expand/ })
  if (expand) await userEvent.click(expand)
  return screen.findByRole('combobox', { name: 'Search channels' })
}

it('searches bounded channel names, disambiguates duplicates by id and submits only the selected id', async () => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: 10,
    permissions: { admin_permissions: { channel: { read: true } } },
  })
  const router = await renderFilter(
    '/usage-logs/common?user_scope=others',
    undefined,
    [
      { id: 9, name: 'Primary', models: [] },
      { id: 42, name: 'Primary', models: [] },
      { id: 77, name: 'Backup', models: [] },
    ]
  )
  const input = await openChannelPicker()
  await userEvent.click(input)
  await userEvent.type(input, 'Primary')
  await waitFor(() =>
    expect(
      vi
        .mocked(api.get)
        .mock.calls.some(
          ([url, config]) =>
            url === '/api/channel/options' &&
            (config?.params as ChannelOptionsParams | undefined)?.keyword ===
              'Primary'
        )
    ).toBe(true)
  )
  expect(
    await screen.findByRole('option', { name: 'Primary (#9)' })
  ).toBeVisible()
  await userEvent.click(
    await screen.findByRole('option', { name: 'Primary (#42)' })
  )
  expect(router.state.location.search).not.toHaveProperty('channel')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      channel: '42',
      user_scope: 'others',
      page: 1,
    })
  )
  await waitFor(() => expect(input).toHaveValue('Primary (#42)'))
  const optionCalls = vi
    .mocked(api.get)
    .mock.calls.filter(([url]) => url === '/api/channel/options')
  expect(
    optionCalls.some(
      ([, config]) =>
        (config?.params as ChannelOptionsParams | undefined)?.keyword ===
        'Primary'
    )
  ).toBe(true)
  for (const [, config] of optionCalls) {
    const params = config?.params as ChannelOptionsParams | undefined
    if (params?.ids) {
      expect(params).toEqual({
        p: 1,
        page_size: 1,
        keyword: '42',
        ids: '42',
      })
    } else {
      expect(params?.page_size).toBe(30)
      expect(params).not.toHaveProperty('ids')
    }
    expect(config?.signal).toBeInstanceOf(AbortSignal)
  }
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(
        ([url]) =>
          String(url).startsWith('/api/channel/?') || url === '/api/channel/'
      )
  ).toBe(false)
})

it('hydrates a selected channel outside the first page, pages options and preserves scope on reset', async () => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: 10,
    permissions: { admin_permissions: { channel: { read: true } } },
  })
  const channels = Array.from({ length: 35 }, (_, index) => ({
    id: index + 1,
    name: `Channel ${index + 1}`,
    models: [],
  }))
  const router = await renderFilter(
    '/usage-logs/common?channel=35&user_scope=self',
    undefined,
    channels
  )
  const input = await openChannelPicker()
  await waitFor(() => expect(input).toHaveValue('Channel 35 (#35)'))
  expect(api.get).toHaveBeenCalledWith(
    '/api/channel/options',
    expect.objectContaining({
      params: expect.objectContaining({
        p: 1,
        page_size: 1,
        ids: '35',
        keyword: '35',
      }),
      signal: expect.any(AbortSignal),
    })
  )
  await userEvent.click(input)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Load more' })
  )
  expect(
    await screen.findByRole('option', { name: 'Channel 34 (#34)' })
  ).toBeVisible()
  expect(api.get).toHaveBeenCalledWith(
    '/api/channel/options',
    expect.objectContaining({
      params: { p: 2, page_size: 30 },
    })
  )
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(router.state.location.search).not.toHaveProperty('channel')
  )
  expect(router.state.location.search).toHaveProperty('user_scope', 'self')
  await router.history.push('/usage-logs/common?channel=34&user_scope=others')
  await waitFor(() => expect(input).toHaveValue('Channel 34 (#34)'))
})

it('masks channel names in the selected input, hover text and options without changing the selected id', async () => {
  const user = userEvent.setup()
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const router = await renderFilter(
    '/usage-logs/common?channel=9&user_scope=others',
    undefined,
    [
      { id: 9, name: 'Private primary', models: [] },
      { id: 77, name: 'Private backup', models: [] },
    ]
  )
  const input = await openChannelPicker()
  await waitFor(() => expect(input).toHaveValue('Private primary (#9)'))
  expect(input).toHaveAttribute('title', 'Private primary (#9)')

  await user.click(screen.getByRole('button', { name: 'Hide' }))
  expect(input).toHaveValue('•••• (#9)')
  expect(input).not.toHaveAttribute('title')
  await user.hover(input)
  expect(screen.queryByTitle('Private primary (#9)')).not.toBeInTheDocument()
  await user.click(input)
  expect(await screen.findByRole('option', { name: '•••• (#9)' })).toBeVisible()
  const backup = await screen.findByRole('option', { name: '•••• (#77)' })
  expect(backup).toBeVisible()
  expect(
    screen.queryByRole('option', { name: /Private/ })
  ).not.toBeInTheDocument()
  expect(input.closest('.\\[-webkit-text-security\\:disc\\]')).not.toBeNull()
  await user.click(backup)
  await waitFor(() => expect(input).toHaveValue('•••• (#77)'))
  expect(input).not.toHaveAttribute('title')
  expect(router.state.location.search).toHaveProperty('channel', '9')

  await user.click(screen.getByRole('button', { name: 'Show' }))
  await waitFor(() => expect(input).toHaveValue('Private backup (#77)'))
  expect(input).toHaveAttribute('title', 'Private backup (#77)')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      channel: '77',
      user_scope: 'others',
    })
  )
})

it('masks channel-name search text and results while sensitive values are hidden', async () => {
  const user = userEvent.setup()
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  await renderFilter('/usage-logs/common?channel=9', undefined, [
    { id: 9, name: 'Private primary', models: [] },
    { id: 77, name: 'Private backup', models: [] },
  ])
  const input = await openChannelPicker()
  await waitFor(() => expect(input).toHaveValue('Private primary (#9)'))
  await user.click(screen.getByRole('button', { name: 'Hide' }))
  await user.click(input)
  await user.type(input, 'Private backup')
  expect(input.closest('.\\[-webkit-text-security\\:disc\\]')).not.toBeNull()
  expect(input).not.toHaveAttribute('title')
  expect(input).toHaveValue('Private backup')
  expect(
    await screen.findByRole('option', { name: '•••• (#77)' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: /Private/ })
  ).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await waitFor(() => expect(input).toHaveValue('•••• (#9)'))

  await user.click(screen.getByRole('button', { name: 'Show' }))
  await waitFor(() => expect(input).toHaveValue('Private primary (#9)'))
})

it.each([
  { keyword: 'Backup', matches: true },
  { keyword: 'backup-model', matches: true },
  { keyword: 'missing-channel', matches: false },
])(
  'keeps selected-id hydration out of keyword results for $keyword',
  async ({ keyword, matches }) => {
    const user = userEvent.setup()
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
    const router = await renderFilter(
      '/usage-logs/common?channel=9',
      undefined,
      [
        { id: 9, name: 'Primary', models: [] },
        { id: 77, name: 'Backup', models: ['backup-model'] },
      ]
    )
    const input = await openChannelPicker()
    await waitFor(() => expect(input).toHaveValue('Primary (#9)'))
    await user.click(input)
    await user.type(input, keyword)
    await waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        '/api/channel/options',
        expect.objectContaining({
          params: { p: 1, page_size: 30, keyword },
          signal: expect.any(AbortSignal),
        })
      )
    )
    if (matches) {
      expect(
        await screen.findByRole('option', { name: 'Backup (#77)' })
      ).toBeVisible()
    } else {
      await waitFor(() =>
        expect(screen.getByText('No matching channels')).toBeVisible()
      )
      expect(screen.queryByRole('option')).not.toBeInTheDocument()
    }
    expect(
      screen.queryByRole('option', { name: 'Primary (#9)' })
    ).not.toBeInTheDocument()
    expect(router.state.location.search).toHaveProperty('channel', '9')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(input).toHaveValue('Primary (#9)'))
  }
)

it.each([
  { role: 1, read: true },
  { role: 10, read: false },
])(
  'does not fetch channel options without both administrator role and channel.read ($role/$read)',
  async ({ role, read }) => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'viewer',
      role,
      permissions: { admin_permissions: { channel: { read } } },
    })
    await renderFilter('/usage-logs/common?channel=9')
    const expand = screen.queryByRole('button', { name: /^Expand/ })
    if (expand) await userEvent.click(expand)
    expect(
      screen.queryByRole('combobox', { name: 'Search channels' })
    ).not.toBeInTheDocument()
    expect(
      vi
        .mocked(api.get)
        .mock.calls.some(([url]) => url === '/api/channel/options')
    ).toBe(false)
    if (role === 10) {
      expect(screen.getByPlaceholderText('Channel ID')).toHaveValue('9')
    }
  }
)

it('hides cached channel names immediately after channel.read is revoked', async () => {
  const admin = {
    id: 1,
    username: 'admin',
    role: 10,
    permissions: { admin_permissions: { channel: { read: true } } },
  }
  useAuthStore.getState().auth.setUser(admin)
  await renderFilter('/usage-logs/common?channel=9', undefined, [
    { id: 9, name: 'Protected name', models: [] },
  ])
  const input = await openChannelPicker()
  await waitFor(() => expect(input).toHaveValue('Protected name (#9)'))
  useAuthStore.getState().auth.setUser({
    ...admin,
    permissions: { admin_permissions: { channel: { read: false } } },
  })
  await waitFor(() =>
    expect(
      screen.queryByRole('combobox', { name: 'Search channels' })
    ).not.toBeInTheDocument()
  )
  expect(
    screen.queryByDisplayValue('Protected name (#9)')
  ).not.toBeInTheDocument()
  expect(screen.getByPlaceholderText('Channel ID')).toHaveValue('9')
})

it('keeps an unavailable selected channel id and offers retry instead of presenting a business error as no matches', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const router = await renderFilter(
    '/usage-logs/common?channel=99',
    undefined,
    null
  )
  const input = await openChannelPicker()
  await userEvent.click(input)
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.queryByText('No matching channels')).not.toBeInTheDocument()
  expect(router.state.location.search).toHaveProperty('channel', '99')
  const before = vi
    .mocked(api.get)
    .mock.calls.filter(([url]) => url === '/api/channel/options').length
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(
      vi
        .mocked(api.get)
        .mock.calls.filter(([url]) => url === '/api/channel/options').length
    ).toBeGreaterThan(before)
  )
})

it('keeps historical auto values editable when auto is the only available group', async () => {
  const router = await renderFilter('/usage-logs/common?group=auto', {
    auto: { desc: '', ratio: 1 },
  })
  const input = screen.getByRole('combobox', { name: 'Group' })
  expect(input).toHaveValue('auto')
  await userEvent.click(input)
  expect(screen.queryByRole('option', { name: 'auto' })).not.toBeInTheDocument()
  await userEvent.clear(input)
  await userEvent.type(input, 'retired')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ group: 'retired' })
  )
})
