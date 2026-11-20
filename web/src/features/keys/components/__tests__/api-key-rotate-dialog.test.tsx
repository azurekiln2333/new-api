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
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'

import { apiKeySchema } from '../../types'
import { ApiKeyRotateDialog } from '../api-key-rotate-dialog'
import { ApiKeysProvider, useApiKeys } from '../api-keys-provider'

const token = apiKeySchema.parse({
  id: 7,
  name: 'production',
  key: 'old********key',
  status: 1,
  remain_quota: 10,
  used_quota: 20,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 1,
  accessed_time: 1,
  model_limits_enabled: false,
})
const clients: QueryClient[] = []

function Harness() {
  const keys = useApiKeys()
  return (
    <>
      <Button
        onClick={() => {
          keys.setCurrentRow(token)
          keys.setOpen('rotate')
        }}
      >
        Open rotation
      </Button>
      <Button
        onClick={async () => {
          await keys.resolveRealKey(7)
        }}
      >
        Reveal
      </Button>
      <output aria-label='Current revealed key'>
        {keys.resolvedKeys[7] ?? ''}
      </output>
      <ApiKeyRotateDialog />
    </>
  )
}

function renderRotation() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <ApiKeysProvider>
        <Harness />
      </ApiKeysProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const client of clients.splice(0)) client.clear()
})

it('rotates the selected token from the console session without an extra proof', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/token/7/key') {
      return { data: { success: true, data: { key: 'old' } } }
    }
    if (url === '/api/token/7/key/rotate') {
      return { data: { success: true, data: { key: 'replacement', token } } }
    }
    throw new Error(`Unexpected POST ${url}`)
  })
  renderRotation()
  await user.click(screen.getByRole('button', { name: 'Reveal' }))
  expect(screen.getByLabelText('Current revealed key')).toHaveTextContent(
    'sk-old'
  )
  await user.click(screen.getByRole('button', { name: 'Open rotation' }))
  await user.click(await screen.findByRole('button', { name: 'Rotate' }))

  expect(await screen.findByText('sk-replacement')).toBeVisible()
  expect(screen.getByLabelText('Current revealed key')).toBeEmptyDOMElement()
  expect(post).toHaveBeenCalledWith(
    '/api/token/7/key/rotate',
    {},
    expect.objectContaining({
      singleUseAuthorization: true,
      signal: expect.any(AbortSignal),
    })
  )
  expect(post).toHaveBeenCalledTimes(2)
  await user.click(screen.getByRole('button', { name: 'Copy Key' }))
  expect(await navigator.clipboard.readText()).toBe('sk-replacement')
  await user.click(screen.getByRole('button', { name: 'Done' }))
  await waitFor(() =>
    expect(screen.queryByText('sk-replacement')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Open rotation' }))
  expect(await screen.findByRole('button', { name: 'Rotate' })).toBeEnabled()
  expect(screen.queryByText('sk-replacement')).not.toBeInTheDocument()
})

it('sends no mutation when the confirmation is cancelled', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  renderRotation()
  await user.click(screen.getByRole('button', { name: 'Open rotation' }))
  const dialog = (await screen.findByRole('button', { name: 'Rotate' })).closest(
    '[role="dialog"]'
  )
  expect(dialog).not.toBeNull()
  await user.click(
    within(dialog as HTMLElement).getByRole('button', { name: 'Cancel' })
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'Rotate' })
    ).not.toBeInTheDocument()
  )
  expect(post).not.toHaveBeenCalled()
})

it('keeps rotation open after a server rejection and allows a plain retry', async () => {
  const user = userEvent.setup()
  let attempts = 0
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url !== '/api/token/7/key/rotate') {
      throw new Error(`Unexpected POST ${url}`)
    }
    attempts++
    if (attempts === 1) {
      return { data: { success: false, message: 'Please try again later.' } }
    }
    return { data: { success: true, data: { key: 'replacement', token } } }
  })
  renderRotation()
  await user.click(screen.getByRole('button', { name: 'Open rotation' }))
  await user.click(await screen.findByRole('button', { name: 'Rotate' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Rotate' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Rotate' }))
  expect(await screen.findByText('sk-replacement')).toBeVisible()
  expect(post).toHaveBeenCalledTimes(2)
})

it('sends a custom rotation secret exactly as typed and clears it when the dialog closes', async () => {
  const user = userEvent.setup()
  const customKey = ' sk-M7qR2zT9bV4nX6kP8sD3hF5jL0wC1yA9 '
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url !== '/api/token/7/key/rotate') {
      throw new Error(`Unexpected POST ${url}`)
    }
    return {
      data: {
        success: false,
        message: 'The API key may only contain letters and digits',
      },
    }
  })
  renderRotation()
  await user.click(screen.getByRole('button', { name: 'Open rotation' }))
  await user.type(
    await screen.findByLabelText('Custom key (optional)'),
    customKey
  )
  await user.click(screen.getByRole('button', { name: 'Rotate' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Rotate' })).toBeEnabled()
  )
  expect(post).toHaveBeenCalledWith(
    '/api/token/7/key/rotate',
    { key: customKey },
    expect.anything()
  )
  expect(screen.getByLabelText('Custom key (optional)')).toHaveValue(customKey)
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(screen.getByRole('button', { name: 'Open rotation' }))
  expect(await screen.findByLabelText('Custom key (optional)')).toHaveValue('')
})
