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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ConfigDrawer } from '@/components/config-drawer'
import { SidebarProvider } from '@/components/ui/sidebar'
import { DirectionProvider } from '@/context/direction-provider'
import { LayoutProvider } from '@/context/layout-provider'
import { ThemeCustomizationProvider } from '@/context/theme-customization-provider'
import { ThemeProvider } from '@/context/theme-provider'
import { updateSystemOption } from '@/features/system-settings/api'
import { ROLE } from '@/lib/roles'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

vi.mock('@/features/system-settings/api', () => ({
  updateSystemOption: vi.fn(),
}))

const mockedUpdateSystemOption = vi.mocked(updateSystemOption)

/**
 * A root user's theme drawer writes the shared theme, so this module guards the
 * two rules that keep the control from leaking: the button is root-only, and an
 * edit is persisted as the `ThemeSettings` option.
 *
 * The drawer owns the visibility rule itself, so rendering it directly is what
 * the header does too — a non-root user gets nothing back.
 */
function renderDrawer(props: { role: number; themeSettings?: string }) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: props.role,
  })

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(STATUS_QUERY_KEY, {
    ThemeSettings: props.themeSettings ?? '',
  })

  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <ThemeProvider>
          <ThemeCustomizationProvider>
            <DirectionProvider>
              <LayoutProvider>
                <SidebarProvider>
                  <ConfigDrawer />
                </SidebarProvider>
              </LayoutProvider>
            </DirectionProvider>
          </ThemeCustomizationProvider>
        </ThemeProvider>
      </QueryClientProvider>
    ),
  }
}

beforeEach(() => {
  localStorage.clear()
  mockedUpdateSystemOption.mockResolvedValue({
    success: true,
    message: '',
  })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.clear()
  useAuthStore.getState().auth.setUser(null)
})

describe('global theme drawer access', () => {
  it('hides the theme button from a non-root user', () => {
    renderDrawer({ role: ROLE.USER })

    expect(
      screen.queryByRole('button', { name: 'Open theme settings' })
    ).not.toBeInTheDocument()
  })

  it('shows the theme button to a root user', () => {
    renderDrawer({ role: ROLE.SUPER_ADMIN })

    expect(
      screen.getByRole('button', { name: 'Open theme settings' })
    ).toBeInTheDocument()
  })
})

describe('global theme drawer persistence', () => {
  it('saves a root user choice as the ThemeSettings option', async () => {
    const user = userEvent.setup()
    renderDrawer({ role: ROLE.SUPER_ADMIN })

    await user.click(
      screen.getByRole('button', { name: 'Open theme settings' })
    )
    await user.click(screen.getByRole('radio', { name: 'Select dark' }))

    await waitFor(() => {
      expect(mockedUpdateSystemOption).toHaveBeenCalledWith({
        key: 'ThemeSettings',
        value: JSON.stringify({ mode: 'dark' }),
      })
    })
  })

  it('merges a new choice into the already stored theme', async () => {
    const user = userEvent.setup()
    renderDrawer({
      role: ROLE.SUPER_ADMIN,
      themeSettings: JSON.stringify({ preset: 'anthropic' }),
    })

    await user.click(
      screen.getByRole('button', { name: 'Open theme settings' })
    )
    await user.click(screen.getByRole('radio', { name: 'Select dark' }))

    await waitFor(() => {
      expect(mockedUpdateSystemOption).toHaveBeenCalledWith({
        key: 'ThemeSettings',
        value: JSON.stringify({ preset: 'anthropic', mode: 'dark' }),
      })
    })
  })

  it('clears the stored theme when a root user resets', async () => {
    const user = userEvent.setup()
    renderDrawer({
      role: ROLE.SUPER_ADMIN,
      themeSettings: JSON.stringify({ mode: 'dark', preset: 'anthropic' }),
    })

    await user.click(
      screen.getByRole('button', { name: 'Open theme settings' })
    )
    await user.click(
      screen.getByRole('button', {
        name: 'Reset all settings to default values',
      })
    )

    await waitFor(() => {
      expect(mockedUpdateSystemOption).toHaveBeenCalledWith({
        key: 'ThemeSettings',
        value: '',
      })
    })
  })
})
