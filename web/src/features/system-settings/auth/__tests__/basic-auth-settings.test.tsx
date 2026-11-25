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
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { BasicAuthSection } from '../basic-auth-section'

const defaults = {
  PasswordLoginEnabled: true,
  PasswordRegisterEnabled: true,
  EmailVerificationEnabled: false,
  RegisterEnabled: true,
  InviteRegisterEnabled: true,
  RegisterAffiliateLinkEnabled: true,
  LoginVerificationRequiredEnabled: false,
  EmailDomainRestrictionEnabled: false,
  EmailAliasRestrictionEnabled: false,
  EmailDomainWhitelist: '',
}

const clients: QueryClient[] = []

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
  window.localStorage.clear()
  vi.restoreAllMocks()
})

function Fixture() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  const [client] = useState(() => {
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    clients.push(queryClient)
    return queryClient
  })

  return (
    <QueryClientProvider client={client}>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <BasicAuthSection defaultValues={defaults} />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

async function renderSettings() {
  const route = createRootRoute({ component: Fixture })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
}

it.each([
  ['Allow referral links', 'RegisterAffiliateLinkEnabled'],
  ['Require an invitation code', 'InviteRegisterEnabled'],
])('saves %s independently', async (label, key) => {
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, message: '' },
  })
  await renderSettings()
  const invitation = screen.getByRole('switch', {
    name: 'Require an invitation code',
  })
  const referral = screen.getByRole('switch', { name: 'Allow referral links' })
  expect(invitation).toHaveAttribute('aria-checked', 'true')
  expect(referral).toHaveAttribute('aria-checked', 'true')
  expect(referral).toHaveAccessibleDescription(
    'Referral links attribute the inviter and rewards. They never replace a required invitation code.'
  )

  const user = userEvent.setup()
  const selected = screen.getByRole('switch', { name: label })
  selected.focus()
  await user.keyboard(' ')
  expect(selected).toHaveAttribute('aria-checked', 'false')
  const unchanged = label === 'Allow referral links' ? invitation : referral
  expect(unchanged).toHaveAttribute('aria-checked', 'true')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(put).toHaveBeenCalledWith('/api/option/batch', {
    options: [{ key, value: false }],
  })
})
