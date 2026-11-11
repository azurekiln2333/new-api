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
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { Invitation } from '../api'
import { InvitationEditor } from '../components/invitation-editor'
import { InvitationCodes } from '../index'

const clients: QueryClient[] = []
const item: Invitation = {
  id: 7,
  key: 'private-invitation-code',
  name: 'Launch invitations',
  status: 1,
  created_time: 1,
  expired_time: 0,
  used_user_id: 0,
  used_time: 0,
}
afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function queryClient() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return client
}

async function renderManager(items: Invitation[], failure = false) {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  const get = vi.spyOn(api, 'get').mockImplementation(async () => {
    if (failure) throw new Error('offline')
    return { data: { success: true, data: { items, total: items.length } } }
  })
  const root = createRootRoute()
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const invitations = createRoute({
    getParentRoute: () => authenticated,
    path: 'invitation-codes/',
    component: InvitationCodes,
  })
  const router = createRouter({
    routeTree: root.addChildren([authenticated.addChildren([invitations])]),
    history: createMemoryHistory({ initialEntries: ['/invitation-codes'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={queryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: 'Generate invitation codes' })
  return {
    get,
    recover: () => {
      failure = false
    },
    removeItem: () => {
      items = []
    },
  }
}

it('generates a batch and returns the codes for export', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: ['code-a', 'code-b'] } })
  const saved = vi.fn()
  render(
    <QueryClientProvider client={queryClient()}>
      <InvitationEditor onClose={vi.fn()} onSaved={saved} />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await user.clear(screen.getByRole('spinbutton', { name: 'Count' }))
  await user.type(screen.getByRole('spinbutton', { name: 'Count' }), '2')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(saved).toHaveBeenCalledWith(['code-a', 'code-b']))
  expect(post).toHaveBeenCalledWith('/api/invitation/', {
    name: 'Launch',
    count: 2,
    expired_time: 0,
  })
})

it('keeps editing available after a rejected save and persists a successful retry', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Invitation code cannot be changed' },
    })
    .mockResolvedValue({ data: { success: true } })
  const saved = vi.fn()
  render(
    <QueryClientProvider client={queryClient()}>
      <InvitationEditor invitation={item} onClose={vi.fn()} onSaved={saved} />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.clear(screen.getByRole('textbox', { name: 'Name' }))
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Renamed')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(saved).not.toHaveBeenCalled()
  expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Renamed')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(saved).toHaveBeenCalledWith([]))
  expect(put).toHaveBeenLastCalledWith('/api/invitation/', {
    id: 7,
    name: 'Renamed',
    expired_time: 0,
  })
})

it('shows a retry action on load failure and the empty state after recovery', async () => {
  const { recover } = await renderManager([], true)
  await screen.findByText('Failed to load invitation codes')
  recover()
  await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No invitation codes found')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Export current page' })
  ).toBeDisabled()
})

it('exports the current page and requires confirmation before deleting a code', async () => {
  const { removeItem } = await renderManager([item])
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    removeItem()
    return { data: { success: true } }
  })
  const user = userEvent.setup()
  await screen.findByText(item.name)
  await user.click(screen.getByRole('button', { name: 'Export current page' }))
  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).getByText(item.key)).toBeVisible()
  expect(within(dialog).getByRole('button', { name: /Copy/ })).toBeEnabled()
  expect(within(dialog).getByRole('button', { name: 'Download' })).toBeEnabled()
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Delete' }))
  const confirm = await screen.findByRole('alertdialog')
  expect(post).not.toHaveBeenCalled()
  await user.click(within(confirm).getByRole('button', { name: 'Continue' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/invitation/batch', { ids: [7] })
  )
  expect(await screen.findByText('No invitation codes found')).toBeVisible()
})

it('prevents editing or re-enabling an already used code', async () => {
  await renderManager([{ ...item, status: 3, used_user_id: 12, used_time: 2 }])
  await screen.findByText(item.name)
  expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeDisabled()
})
