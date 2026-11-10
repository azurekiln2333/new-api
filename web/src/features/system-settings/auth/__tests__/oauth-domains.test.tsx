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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance, type i18n } from 'i18next'
import { useState } from 'react'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { OAuthDomainsSection } from '../oauth-domains-section'

let testI18n: i18n
function Fixture(props: { origins: string }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  )
  return (
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={client}>
        <div ref={setContainer} />
        <SettingsPageProvider actionsContainer={container}>
          <OAuthDomainsSection
            allowedOrigins={props.origins}
            serverAddress='https://main.example.com'
          />
        </SettingsPageProvider>
      </QueryClientProvider>
    </I18nextProvider>
  )
}
async function renderSettings(origins = '') {
  const route = createRootRoute({
    component: () => <Fixture origins={origins} />,
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
}
beforeEach(async () => {
  testI18n = createInstance()
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
  })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})
describe('OAuth login domains', () => {
  it('saves multiple websites from the panel and clears the dirty state', async () => {
    const user = userEvent.setup()
    await renderSettings()
    await user.type(
      screen.getByRole('textbox', { name: 'Allowed login websites' }),
      'https://second.example.com{Enter}https://third.example.net:8443'
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/', {
        key: 'oauth.allowed_origins',
        value: 'https://second.example.com\nhttps://third.example.net:8443',
      })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled()
    )
  })
  it('rejects wildcard and callback path entries before saving', async () => {
    const user = userEvent.setup()
    await renderSettings()
    const field = screen.getByRole('textbox', {
      name: 'Allowed login websites',
    })
    for (const value of [
      'https://*.example.com',
      'https://second.example.com/oauth/test',
    ]) {
      await user.clear(field)
      await user.type(field, value)
      await user.click(screen.getByRole('button', { name: 'Save Changes' }))
      await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'))
    }
    expect(api.put).not.toHaveBeenCalled()
  })
  it('keeps edited domains and enables retry when saving fails', async () => {
    vi.mocked(api.put).mockResolvedValue({
      data: { success: false, message: 'Save failed' },
    })
    const user = userEvent.setup()
    await renderSettings()
    const field = screen.getByRole('textbox', {
      name: 'Allowed login websites',
    })
    await user.type(field, 'https://second.example.com')
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await screen.findByText('Save failed')
    expect(field).toHaveValue('https://second.example.com')
    expect(screen.getByRole('button', { name: 'Reset' })).toBeEnabled()
  })
  it('clears additional domains and keeps the primary website visible', async () => {
    const user = userEvent.setup()
    await renderSettings('https://second.example.com')
    await user.clear(
      screen.getByRole('textbox', { name: 'Allowed login websites' })
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/', {
        key: 'oauth.allowed_origins',
        value: '',
      })
    )
    expect(screen.getByText('https://main.example.com')).toBeVisible()
  })
  it('disables editing during save and resets later edits to the saved websites', async () => {
    let completeSave!: () => void
    vi.mocked(api.put).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          completeSave = () => resolve({ data: { success: true } })
        })
    )
    const user = userEvent.setup()
    await renderSettings()
    const field = screen.getByRole('textbox', {
      name: 'Allowed login websites',
    })
    await user.type(field, 'https://second.example.com')
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(field).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled()
    await act(async () => completeSave())
    await waitFor(() => expect(field).toBeEnabled())
    await user.type(field, '{Enter}https://third.example.net')
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(field).toHaveValue('https://second.example.com')
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  })
})
