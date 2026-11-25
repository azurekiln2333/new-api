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
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance, type i18n } from 'i18next'
import { useState } from 'react'
import { I18nextProvider } from 'react-i18next'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { useUpdateOption } from '../../hooks/use-update-option'
import type { SystemOptionsResponse, UpdateOptionRequest } from '../../types'
import { BotProtectionSection } from '../bot-protection-section'

const defaults = {
  TurnstileCheckEnabled: false,
  TurnstileSiteKey: '',
  TurnstileSecretKey: '',
}

let testI18n: i18n

function Fixture() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  )

  return (
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={client}>
        <div ref={setContainer} />
        <SettingsPageProvider actionsContainer={container}>
          <BotProtectionSection defaultValues={defaults} />
        </SettingsPageProvider>
      </QueryClientProvider>
    </I18nextProvider>
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

beforeEach(async () => {
  testI18n = createInstance()
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} }, zh },
    interpolation: { escapeValue: false },
  })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

describe('settings section batch mutation', () => {
  it('patches all committed keys without refetching the options payload', async () => {
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    client.setQueryData<SystemOptionsResponse>(['system-options'], {
      success: true,
      message: '',
      data: [
        { key: 'TurnstileSiteKey', value: 'old-site' },
        { key: 'TurnstileCheckEnabled', value: 'false' },
        { key: 'Unchanged', value: 'keep' },
      ],
    })
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useUpdateOption(), {
      wrapper: (props) => (
        <QueryClientProvider client={client}>
          {props.children}
        </QueryClientProvider>
      ),
    })
    await act(async () => {
      await result.current.mutateAsync([
        { key: 'TurnstileSiteKey', value: 'new-site' },
        { key: 'TurnstileCheckEnabled', value: true },
      ])
    })
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(
      client.getQueryData<SystemOptionsResponse>(['system-options'])?.data
    ).toEqual([
      { key: 'TurnstileSiteKey', value: 'new-site' },
      { key: 'TurnstileCheckEnabled', value: 'true' },
      { key: 'Unchanged', value: 'keep' },
    ])
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ['system-options'],
      refetchType: 'none',
    })
  })

  it('keeps cached values unchanged and names the failing key on rejected batch', async () => {
    vi.mocked(api.put).mockResolvedValue({
      data: {
        success: false,
        key: 'TurnstileSiteKey',
        message: 'Configuration rejected',
      },
    })
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const initial: SystemOptionsResponse = {
      success: true,
      message: '',
      data: [{ key: 'TurnstileSiteKey', value: 'old-site' }],
    }
    client.setQueryData(['system-options'], initial)
    const success = vi.spyOn(toast, 'success')
    const error = vi.spyOn(toast, 'error')
    const { result } = renderHook(() => useUpdateOption(), {
      wrapper: (props) => (
        <QueryClientProvider client={client}>
          {props.children}
        </QueryClientProvider>
      ),
    })
    await act(async () => {
      await expect(
        result.current.mutateAsync([
          { key: 'TurnstileSiteKey', value: 'draft' },
        ])
      ).rejects.toThrow()
    })
    expect(client.getQueryData(['system-options'])).toEqual(initial)
    expect(success).not.toHaveBeenCalled()
    expect(error).toHaveBeenCalledWith(
      expect.stringContaining('TurnstileSiteKey')
    )
  })

  it('keeps specialized keys on single PUT and batches only ordinary keys', async () => {
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    const { result } = renderHook(() => useUpdateOption(), {
      wrapper: (props) => (
        <QueryClientProvider client={client}>
          {props.children}
        </QueryClientProvider>
      ),
    })
    await act(async () => {
      await result.current.mutateAsync([
        { key: 'ServerAddress', value: 'https://gateway.example.com' },
        { key: 'RetryTimes', value: 2 },
        { key: 'ModelRatio', value: '{}' },
        { key: 'TestOrdinary', value: 0 },
      ])
    })
    expect(api.put).toHaveBeenCalledTimes(4)
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'ServerAddress',
      value: 'https://gateway.example.com',
    })
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'RetryTimes',
      value: 2,
    })
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'ModelRatio',
      value: '{}',
    })
    expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
      options: [{ key: 'TestOrdinary', value: 0 }],
    })
  })
})

describe('Bot Protection settings saves', () => {
  it('saves both keys before enabling Turnstile in one save', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true, message: '' },
    })
    const user = userEvent.setup()
    await renderSettings()
    await user.type(
      screen.getByRole('textbox', { name: 'Site Key' }),
      'site-key'
    )
    await user.type(screen.getByLabelText('Secret Key'), 'secret-key')
    await user.click(screen.getByRole('switch', { name: 'Enable Turnstile' }))
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith('/api/option/batch', {
      options: [
        { key: 'TurnstileSiteKey', value: 'site-key' },
        { key: 'TurnstileSecretKey', value: 'secret-key' },
        { key: 'TurnstileCheckEnabled', value: true },
      ] satisfies UpdateOptionRequest[],
    })
  })
})
