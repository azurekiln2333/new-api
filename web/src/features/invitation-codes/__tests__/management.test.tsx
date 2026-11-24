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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  completeCodeCreateOperation,
  getCodeCreateOperation,
} from '@/lib/code-create-operation'
import { useAuthStore } from '@/stores/auth-store'

import type { Invitation } from '../api'
import { InvitationEditor } from '../components/invitation-editor'
import { InvitationCodes } from '../index'

const clients: QueryClient[] = []
const actor = { id: 1, username: 'admin', role: 100 }
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
beforeEach(() => {
  sessionStorage.clear()
  useAuthStore.getState().auth.setUser(actor)
})
afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  sessionStorage.clear()
  useAuthStore.getState().auth.setUser(null)
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

function renderCreateEditor() {
  const saved = vi.fn()
  const result = render(
    <QueryClientProvider client={queryClient()}>
      <InvitationEditor onClose={vi.fn()} onSaved={saved} />
    </QueryClientProvider>
  )
  return { saved, user: userEvent.setup(), result }
}

it('generates a batch and returns the codes for export', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({
      data: {
        success: true,
        data: ['code-a', 'code-b'],
        operation_id: 'replayed-operation',
        replayed: true,
        batch_id: '',
      },
    })
  const { saved, user } = renderCreateEditor()
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await user.clear(screen.getByRole('spinbutton', { name: 'Count' }))
  await user.type(screen.getByRole('spinbutton', { name: 'Count' }), '2')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(saved).toHaveBeenCalledWith(['code-a', 'code-b']))
  expect(post).toHaveBeenCalledWith(
    '/api/invitation/',
    { name: 'Launch', count: 2, expired_time: 0 },
    { params: { operation_id: expect.any(String) } }
  )
})

it.each(['lost response', 'server rejection'])(
  'reuses the invitation operation ID after %s for the same normalized payload',
  async (failure) => {
    const post = vi.spyOn(api, 'post')
    if (failure === 'lost response') {
      post.mockRejectedValueOnce(new Error('response lost'))
    } else {
      post.mockResolvedValueOnce({
        data: { success: false, message: 'Database temporarily unavailable' },
      })
    }
    post.mockResolvedValueOnce({
      data: {
        success: true,
        data: ['original-code'],
        replayed: true,
        batch_id: '',
      },
    })
    const { saved, user } = renderCreateEditor()
    const name = screen.getByRole('textbox', { name: 'Name' })
    await user.type(name, ' Launch ')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
    )
    expect(saved).not.toHaveBeenCalled()
    expect(post.mock.calls[0]?.[2]?.params?.operation_id).toBeTruthy()

    await user.clear(name)
    await user.type(name, 'Launch')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saved).toHaveBeenCalledWith(['original-code']))
    expect(post).toHaveBeenNthCalledWith(
      2,
      '/api/invitation/',
      { name: 'Launch', count: 1, expired_time: 0 },
      post.mock.calls[0]?.[2]
    )
  }
)

it('uses a new invitation operation ID when a failed create payload changes', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Database temporarily unavailable' },
  })
  const { saved, user } = renderCreateEditor()
  const name = screen.getByRole('textbox', { name: 'Name' })
  await user.type(name, 'Launch')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  const originalId = post.mock.calls[0]?.[2]?.params?.operation_id

  await user.clear(name)
  await user.type(name, 'Partners')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(originalId).toBeTruthy()
  expect(post.mock.calls[1]?.[1]).toEqual({
    name: 'Partners',
    count: 1,
    expired_time: 0,
  })
  expect(post.mock.calls[1]?.[2]?.params?.operation_id).toEqual(
    expect.any(String)
  )
  expect(post.mock.calls[1]?.[2]?.params?.operation_id).not.toBe(originalId)
  expect(saved).not.toHaveBeenCalled()
})

it('uses a new invitation operation ID for another create after success', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({ data: { success: true, data: ['first-code'] } })
    .mockResolvedValueOnce({ data: { success: true, data: ['second-code'] } })
  const { saved, user } = renderCreateEditor()
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(saved).toHaveBeenCalledWith(['first-code']))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  const originalId = post.mock.calls[0]?.[2]?.params?.operation_id

  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(saved).toHaveBeenLastCalledWith(['second-code']))
  expect(post).toHaveBeenCalledTimes(2)
  expect(post.mock.calls[1]?.[1]).toEqual(post.mock.calls[0]?.[1])
  expect(originalId).toBeTruthy()
  expect(post.mock.calls[1]?.[2]?.params?.operation_id).toEqual(
    expect.any(String)
  )
  expect(post.mock.calls[1]?.[2]?.params?.operation_id).not.toBe(originalId)
})

it('recovers a lost invitation response after remount and clears only confirmed success', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('response lost'))
    .mockResolvedValueOnce({
      data: { success: true, data: ['original-code'], replayed: true },
    })
    .mockResolvedValueOnce({ data: { success: true, data: ['fresh-code'] } })
  const first = renderCreateEditor()
  await first.user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await first.user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  const originalId = post.mock.calls[0]?.[2]?.params?.operation_id
  expect(originalId).toBeTruthy()
  expect(sessionStorage.length).toBe(1)
  first.result.unmount()

  const retry = renderCreateEditor()
  await retry.user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await retry.user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(retry.saved).toHaveBeenCalledWith(['original-code']))
  expect(post.mock.calls[1]?.[2]?.params?.operation_id).toBe(originalId)
  expect(sessionStorage.length).toBe(0)
  retry.result.unmount()

  const fresh = renderCreateEditor()
  await fresh.user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await fresh.user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(fresh.saved).toHaveBeenCalledWith(['fresh-code']))
  expect(post.mock.calls[2]?.[2]?.params?.operation_id).not.toBe(originalId)
  expect(sessionStorage.length).toBe(0)
})

it.each([
  { label: 'another user', nextActor: { id: 2, username: 'other', role: 100 } },
  { label: 'another role', nextActor: { ...actor, role: 10 } },
])('isolates pending invitation creates for $label', async ({ nextActor }) => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Database temporarily unavailable' },
  })
  const first = renderCreateEditor()
  await first.user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await first.user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  const originalId = post.mock.calls[0]?.[2]?.params?.operation_id
  first.result.unmount()
  useAuthStore.getState().auth.setUser(nextActor)

  const next = renderCreateEditor()
  await next.user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await next.user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(originalId).toBeTruthy()
  expect(post.mock.calls[1]?.[2]?.params?.operation_id).not.toBe(originalId)
  next.result.unmount()
  useAuthStore.getState().auth.setUser(actor)

  const retry = renderCreateEditor()
  await retry.user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await retry.user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(3))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(post.mock.calls[2]?.[2]?.params?.operation_id).toBe(originalId)
})

it('does not create invitations when browser storage cannot preserve the retry ID', async () => {
  const post = vi.spyOn(api, 'post')
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('storage blocked')
  })
  const client = queryClient()
  const reportedError = vi.fn()
  client.setDefaultOptions({ mutations: { retry: false, onError: reportedError } })
  render(
    <QueryClientProvider client={client}>
      <InvitationEditor onClose={vi.fn()} onSaved={vi.fn()} />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(reportedError).toHaveBeenCalled())
  expect(post).not.toHaveBeenCalled()
  expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Launch')
})

it('keeps code types independent and never persists keys or auth data', () => {
  const input = {
    name: 'Launch',
    count: 1,
    expired_time: 0,
    quota: 500000,
    limit_per_user: true,
    key: 'generated-secret-code',
    access_token: 'private-auth-token',
  }
  const invitation = getCodeCreateOperation(actor, 'invitation', input)
  const redemption = getCodeCreateOperation(actor, 'redemption', input)
  expect(redemption.id).not.toBe(invitation.id)
  expect(getCodeCreateOperation(actor, 'invitation', input).id).toBe(
    invitation.id
  )
  expect(getCodeCreateOperation(actor, 'redemption', input).id).toBe(
    redemption.id
  )
  for (let index = 0; index < sessionStorage.length; index += 1) {
    const stored = sessionStorage.getItem(sessionStorage.key(index) ?? '')
    expect(stored).not.toContain(input.key)
    expect(stored).not.toContain(input.access_token)
  }
})

it('keeps a newer pending payload when an older create confirms success', () => {
  const input = { name: 'Launch', count: 1, expired_time: 0 }
  const first = getCodeCreateOperation(actor, 'invitation', input)
  const changedInput = { ...input, name: 'Partners' }
  const second = getCodeCreateOperation(actor, 'invitation', changedInput)
  completeCodeCreateOperation(first)
  expect(getCodeCreateOperation(actor, 'invitation', changedInput).id).toBe(
    second.id
  )
  completeCodeCreateOperation(second)
  expect(getCodeCreateOperation(actor, 'invitation', changedInput).id).not.toBe(
    second.id
  )
})

it('does not replay a confirmed invitation when storage cleanup fails', () => {
  const input = { name: 'Launch', count: 1, expired_time: 0 }
  const original = getCodeCreateOperation(actor, 'invitation', input)
  vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => undefined)
  completeCodeCreateOperation(original)
  const next = getCodeCreateOperation(actor, 'invitation', input)
  expect(next.id).not.toBe(original.id)
  expect(getCodeCreateOperation(actor, 'invitation', input).id).toBe(next.id)
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
