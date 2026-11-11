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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { SignUpForm } from '../components/sign-up-form'

vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof import('@tanstack/react-router')>()),
  useNavigate: () => vi.fn(),
}))

const clients: QueryClient[] = []
afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
  window.sessionStorage.clear()
})

function renderRegistration(open: boolean, invite: boolean) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  client.setQueryData(STATUS_QUERY_KEY, {
    register_enabled: open,
    invite_register_enabled: invite,
    password_register_enabled: true,
    github_oauth: true,
    github_client_id: 'client-id',
  })
  render(
    <QueryClientProvider client={client}>
      <SignUpForm />
    </QueryClientProvider>
  )
}

it.each([
  [false, false],
  [false, true],
  [true, false],
  [true, true],
])(
  'renders registration fields for open=%s and invitation=%s',
  async (open, invite) => {
    renderRegistration(open, invite)
    expect(screen.queryByLabelText('Username') !== null).toBe(open)
    expect(screen.queryByLabelText('Invitation code') !== null).toBe(invite)
    expect(
      screen.queryByRole('button', { name: 'Create account' }) !== null
    ).toBe(open)
    if (!open && !invite) {
      expect(screen.getByRole('status')).toHaveTextContent(
        'New user registration has been disabled'
      )
      expect(
        screen.queryByRole('button', { name: /Continue with GitHub/ })
      ).not.toBeInTheDocument()
      return
    }
    const oauth = screen.getByRole('button', { name: /Continue with GitHub/ })
    if (invite) {
      expect(oauth).toBeDisabled()
      await userEvent
        .setup()
        .type(screen.getByLabelText('Invitation code'), 'single-use-code')
    }
    await waitFor(() => expect(oauth).toBeEnabled())
  }
)

it('sends the invitation to the server OAuth flow and never stores it in browser storage', async () => {
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/oauth/state') {
      return {
        data: {
          success: true,
          data: {
            flow_token: 'opaque-flow',
            authorization_url:
              'https://github.com/login/oauth/authorize?state=opaque-flow',
          },
        },
      }
    }
    if (url === '/api/user/auth/logout') return { data: { success: true } }
    throw new Error(`Unexpected request: ${url}`)
  })
  vi.spyOn(window, 'open').mockReturnValue(null)
  const setItem = vi.spyOn(Storage.prototype, 'setItem')
  renderRegistration(false, true)
  const user = userEvent.setup()
  await user.type(
    screen.getByLabelText('Invitation code'),
    'private-invitation'
  )
  await user.click(screen.getByRole('button', { name: /Continue with GitHub/ }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/oauth/state',
      expect.objectContaining({
        intent: 'login',
        invitation_code: 'private-invitation',
      }),
      expect.anything()
    )
  )
  expect(
    setItem.mock.calls.every(
      ([, value]) => !value.includes('private-invitation')
    )
  ).toBe(true)
})

it('submits password registration with the invitation and preserves the form after rejection', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: false,
      message: 'Invitation code is invalid or unavailable',
    },
  })
  renderRegistration(true, true)
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Username'), 'invited-user')
  await user.type(
    screen.getByLabelText('Password', { exact: true }),
    'safe-password-42'
  )
  await user.type(screen.getByLabelText('Confirm password'), 'safe-password-42')
  await user.type(
    screen.getByLabelText('Invitation code'),
    'private-invitation'
  )
  await user.click(screen.getByRole('button', { name: 'Create account' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.stringContaining('/api/user/register'),
      expect.objectContaining({
        username: 'invited-user',
        invitation_code: 'private-invitation',
      }),
      expect.anything()
    )
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Create account' })).toBeEnabled()
  )
  expect(screen.getByLabelText('Invitation code')).toHaveValue(
    'private-invitation'
  )
})
