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
import { useAuthStore } from '@/stores/auth-store'

import { LoginVerificationPreference } from '../index'

const clients: QueryClient[] = []
afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset('idle')
})

function renderPreference(
  required: boolean,
  enrolled: boolean,
  policy = false
) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'user', role: 1 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/login-verification') {
      return {
        data: {
          success: true,
          data: { required, enrolled, policy_required: policy },
        },
      }
    }
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'auth.login_verification.disable',
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
  render(
    <QueryClientProvider client={client}>
      <LoginVerificationPreference />
    </QueryClientProvider>
  )
  return {
    get,
    setRequired: (value: boolean) => {
      required = value
    },
  }
}

it('disables the preference until a factor is enrolled and explains the site policy', async () => {
  renderPreference(false, false, true)
  expect(
    await screen.findByText(
      'The administrator requires login verification for all enrolled users'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('switch', { name: 'Require verification at login' })
  ).toHaveAttribute('aria-disabled', 'true')
  expect(
    screen.getByText('Enroll two-factor authentication or a Passkey first')
  ).toBeVisible()
})

it('enables login verification without requesting a proof', async () => {
  const { get, setRequired } = renderPreference(false, true)
  const put = vi.spyOn(api, 'put').mockImplementation(async () => {
    setRequired(true)
    return { data: { success: true, data: {} } }
  })
  const toggle = screen.getByRole('switch', {
    name: 'Require verification at login',
  })
  await waitFor(() =>
    expect(toggle).not.toHaveAttribute('aria-disabled', 'true')
  )
  await userEvent.setup().click(toggle)
  await waitFor(() => expect(toggle).toBeChecked())
  expect(put).toHaveBeenCalledWith(
    '/api/user/login-verification',
    { required: true },
    expect.objectContaining({
      acceptAuthRotation: true,
      singleUseAuthorization: true,
    })
  )
  expect(get.mock.calls.some(([url]) => url === '/api/verify/methods')).toBe(
    false
  )
})

it('requires the scoped proof before disabling and refreshes the saved preference', async () => {
  const { setRequired } = renderPreference(true, true)
  const put = vi.spyOn(api, 'put').mockImplementation(async () => {
    setRequired(false)
    return { data: { success: true, data: {} } }
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        proof_token: 'one-use-proof',
        scope: 'auth.login_verification.disable',
        method: '2fa',
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  })
  const toggle = screen.getByRole('switch', {
    name: 'Require verification at login',
  })
  await waitFor(() => expect(toggle).toBeChecked())
  const user = userEvent.setup()
  await user.click(toggle)
  const input = await screen.findByRole('textbox', {
    name: 'Authenticator code or backup code',
  })
  expect(put).not.toHaveBeenCalled()
  await user.type(input, '123456')
  await user.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/login-verification',
      { required: false },
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'one-use-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({ scope: 'auth.login_verification.disable' }),
    expect.anything()
  )
  await waitFor(() => expect(toggle).not.toBeChecked())
})

it('keeps verification enabled when the proof dialog is cancelled', async () => {
  renderPreference(true, true)
  const put = vi.spyOn(api, 'put')
  const toggle = screen.getByRole('switch', {
    name: 'Require verification at login',
  })
  await waitFor(() => expect(toggle).toBeChecked())
  const user = userEvent.setup()
  await user.click(toggle)
  await screen.findByRole('textbox', {
    name: 'Authenticator code or backup code',
  })
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(toggle).not.toHaveAttribute('aria-disabled', 'true')
  )
  expect(toggle).toBeChecked()
  expect(put).not.toHaveBeenCalled()
})
