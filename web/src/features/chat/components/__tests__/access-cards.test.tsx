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
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { AccessCards } from '../access-cards'

const clients: Record<string, string>[] = [
  { 'Web Client': 'https://chat.example.test/?server={address}' },
  { 'Key Web Client': 'https://key.example.test/?key={key}' },
  {
    'Desktop Client': 'cherrystudio://providers/api-keys?config={cherryConfig}',
  },
  { FluentRead: 'fluent://config' },
]
const queryClients: QueryClient[] = []

beforeEach(() => {
  vi.stubGlobal('scrollTo', vi.fn())
  localStorage.clear()
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
})

afterEach(() => {
  cleanup()
  for (const client of queryClients.splice(0)) client.clear()
  useAuthStore.getState().auth.reset()
  vi.unstubAllGlobals()
})

async function renderAccessCards(
  chats = clients,
  statusState: 'ready' | 'loading' | 'error' = 'ready'
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(client)
  if (statusState === 'ready') {
    client.setQueryData(['status'], {
      chats,
      server_address: 'https://api.example.test',
    })
  }
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [{ id: 42, status: 1 }] } },
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { key: 'test-client-key' } },
  })
  if (statusState === 'loading') get.mockReturnValue(new Promise(() => {}))
  if (statusState === 'error') {
    get.mockRejectedValue(new Error('Client configuration unavailable'))
  }
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  const root = createRootRoute({ component: Outlet })
  const access = createRoute({
    getParentRoute: () => root,
    path: '/access',
    component: AccessCards,
  })
  const chat = createRoute({
    getParentRoute: () => root,
    path: '/chat/$chatId',
    component: () => <p>Connected web client</p>,
  })
  const router = createRouter({
    routeTree: root.addChildren([access, chat]),
    history: createMemoryHistory({ initialEntries: ['/access'] }),
  })
  await router.load()
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { client, router, get, post, open, view }
}

it('shows configured client cards without reading or transmitting a key on render', async () => {
  const { get, post, open } = await renderAccessCards()
  expect(
    screen.getByRole('heading', { name: 'Access configuration' })
  ).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /^Connect to / })).toHaveLength(
    3
  )
  expect(screen.queryByText('FluentRead')).not.toBeInTheDocument()
  expect(screen.getByText('https://key.example.test')).toBeInTheDocument()
  expect(screen.queryByText(/\{key\}|\{cherryConfig\}/)).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
  expect(open).not.toHaveBeenCalled()
})

it('opens a web client through its existing route with keyboard activation', async () => {
  const { router, get, open } = await renderAccessCards()
  const user = userEvent.setup()
  screen.getByRole('button', { name: 'Connect to Web Client' }).focus()
  await user.keyboard('{Enter}')
  await waitFor(() => expect(router.state.location.pathname).toBe('/chat/0'))
  expect(screen.getByText('Connected web client')).toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
  expect(open).not.toHaveBeenCalled()
})

it('opens a custom protocol client without a key directly from the connect action', async () => {
  const { get, post, open } = await renderAccessCards([
    { Desktop: 'client://open?server={address}' },
  ])
  await userEvent.click(
    screen.getByRole('button', { name: 'Connect to Desktop' })
  )
  expect(open).toHaveBeenCalledWith(
    'client://open?server=https%3A%2F%2Fapi.example.test',
    '_blank',
    'noopener,noreferrer'
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
})

it('requires confirmation before navigating to a web client that receives a key', async () => {
  const { router, get, post } = await renderAccessCards()
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Connect to Key Web Client' })
  )
  const dialog = screen.getByRole('alertdialog', {
    name: 'Connect to Key Web Client?',
  })
  expect(
    within(dialog).getByText('https://key.example.test')
  ).toBeInTheDocument()
  expect(router.state.location.pathname).toBe('/access')
  expect(get).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Connect' }))
  await waitFor(() => expect(router.state.location.pathname).toBe('/chat/1'))
})

it('canceling a client confirmation never requests a key or launches a client', async () => {
  const { get, post, open } = await renderAccessCards()
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Connect to Desktop Client' })
  )
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
  expect(open).not.toHaveBeenCalled()
})

it('uses the existing key and preset resolver only after confirmation', async () => {
  const { get, post, open } = await renderAccessCards()
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Connect to Desktop Client' })
  )
  expect(get).not.toHaveBeenCalled()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Connect',
    })
  )
  await waitFor(() => expect(open).toHaveBeenCalledOnce())
  expect(get).toHaveBeenCalledWith('/api/token/?p=1&size=50')
  expect(post).toHaveBeenCalledWith('/api/token/42/key')
  const [destination, target, features] = open.mock.calls[0]
  expect(target).toBe('_blank')
  expect(features).toBe('noopener,noreferrer')
  const encoded = new URL(String(destination)).searchParams.get('config')
  if (!encoded) throw new Error('Expected encoded connection configuration')
  expect(JSON.parse(atob(encoded))).toEqual({
    id: 'new-api',
    baseUrl: 'https://api.example.test',
    apiKey: 'sk-test-client-key',
  })
  expect(document.body).not.toHaveTextContent('test-client-key')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

it('keeps the confirmation usable and reports key-fetch failures without launching', async () => {
  const { post, open } = await renderAccessCards()
  post.mockResolvedValueOnce({
    data: { success: false, message: 'Key lookup failed' },
  })
  const error = vi.spyOn(toast, 'error')
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Connect to Desktop Client' })
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Connect',
    })
  )
  await waitFor(() => expect(error).toHaveBeenCalledWith('Key lookup failed'))
  expect(open).not.toHaveBeenCalled()
  expect(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Connect',
    })
  ).toBeEnabled()
})

it('blocks duplicate confirmation and discards a completed key request after leaving the page', async () => {
  const { get, open, view } = await renderAccessCards()
  let completeRequest!: (response: {
    data: {
      success: boolean
      data: { items: { id: number; status: number }[] }
    }
  }) => void
  const request = new Promise<Parameters<typeof completeRequest>[0]>(
    (resolve) => {
      completeRequest = resolve
    }
  )
  get.mockReturnValueOnce(request)
  const user = userEvent.setup()
  const webButton = screen.getByRole('button', {
    name: 'Connect to Web Client',
  })
  await user.click(
    screen.getByRole('button', { name: 'Connect to Desktop Client' })
  )
  await user.dblClick(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Connect',
    })
  )
  expect(get).toHaveBeenCalledOnce()
  expect(
    screen.getByRole('button', { name: 'Preparing your chat link…' })
  ).toBeDisabled()
  expect(webButton).toBeDisabled()
  view.unmount()
  await act(async () => {
    completeRequest({
      data: { success: true, data: { items: [{ id: 42, status: 1 }] } },
    })
    await request
  })
  expect(open).not.toHaveBeenCalled()
})

it('rejects executable URL schemes before fetching a key', async () => {
  const { get, post, open } = await renderAccessCards([
    { Unsafe: 'javascript:alert("{key}")' },
  ])
  const error = vi.spyOn(toast, 'error')
  await userEvent.click(
    screen.getByRole('button', { name: 'Connect to Unsafe' })
  )
  expect(error).toHaveBeenCalledWith(
    'Invalid chat link. Please contact the administrator.'
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
  expect(open).not.toHaveBeenCalled()
})

it('does not launch a client when the signed-in account changes during key preparation', async () => {
  const { post, open } = await renderAccessCards()
  let completeRequest!: (response: {
    data: { success: boolean; data: { key: string } }
  }) => void
  const request = new Promise<Parameters<typeof completeRequest>[0]>(
    (resolve) => {
      completeRequest = resolve
    }
  )
  post.mockReturnValueOnce(request)
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Connect to Desktop Client' })
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Connect',
    })
  )
  expect(post).toHaveBeenCalledOnce()
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 8, username: 'other-user', role: 1 })
    completeRequest({
      data: { success: true, data: { key: 'previous-user-key' } },
    })
    await request
  })
  expect(open).not.toHaveBeenCalled()
  expect(document.body).not.toHaveTextContent('previous-user-key')
  expect(screen.getByRole('button', { name: 'Connect' })).toBeEnabled()
})

it('shows a loading state without treating pending configuration as an empty client list', async () => {
  const { get, post, open } = await renderAccessCards([], 'loading')
  expect(screen.getByText('Loading...')).toBeInTheDocument()
  expect(screen.queryByText('No clients configured')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /^Connect/ })
  ).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledOnce()
  expect(get).toHaveBeenCalledWith('/api/status')
  expect(post).not.toHaveBeenCalled()
  expect(open).not.toHaveBeenCalled()
})

it('shows a configuration error without presenting an empty client list', async () => {
  const { post, open } = await renderAccessCards([], 'error')
  expect(
    await screen.findByText('Failed to load client configuration')
  ).toBeInTheDocument()
  expect(screen.queryByText('No clients configured')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /^Connect/ })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
  expect(open).not.toHaveBeenCalled()
})

it('shows an actionable empty-state explanation when no clients are configured', async () => {
  await renderAccessCards([])
  expect(screen.getByText('No clients configured')).toBeInTheDocument()
  expect(
    screen.getByText('Ask your administrator to configure a client.')
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /^Connect/ })
  ).not.toBeInTheDocument()
})
