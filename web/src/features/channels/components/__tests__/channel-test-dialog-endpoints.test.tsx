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
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { TaskPluginOption } from '../../api'
import { channelSchema, type Channel } from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { ChannelTestDialog } from '../dialogs/channel-test-dialog'

const originalAuth = useAuthStore.getState().auth
const pluginOptions: TaskPluginOption[] = [
  {
    key: 'relay-x',
    name: 'Relay X',
    models: ['model-a'],
    provider: true,
    relayPaths: ['/v1/responses', '/v1/messages'],
  },
]

function OpenDialog(props: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return <ChannelTestDialog open onOpenChange={() => {}} />
}

function channel(type: number, setting = '') {
  return channelSchema.parse({
    id: 9,
    name: 'Test channel',
    type,
    key: '',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'model-a',
    setting,
  })
}

beforeEach(() => {
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
    },
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/task_plugin_options') {
      return { data: { success: true, data: pluginOptions } }
    }
    if (url === '/api/channel/test/9') {
      return { data: { success: true, message: '', time: 0.012 } }
    }
    throw new Error(`Unexpected GET ${url}`)
  })
})

afterEach(() => {
  cleanup()
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function endpointOptions() {
  const user = userEvent.setup()
  if (!screen.queryByRole('combobox', { name: 'Endpoint Type' })) {
    await user.click(await screen.findByRole('button', { name: 'Settings' }))
  }
  await user.click(
    await screen.findByRole('combobox', { name: 'Endpoint Type' })
  )
  return within(await screen.findByRole('listbox'))
    .getAllByRole('option')
    .map((option) => option.textContent)
}

test('a plugin channel offers only the endpoints its plugin relays', async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel(61, '{"task_plugin_key":"relay-x"}')} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  await vi.waitFor(() =>
    expect(api.get).toHaveBeenCalledWith('/api/task_plugin_options')
  )
  expect(await endpointOptions()).toEqual([
    'Auto detect (default)',
    'OpenAI Responses (/v1/responses)',
    'Anthropic (/v1/messages)',
  ])
})

test.each(['', '{}', '{"supported_endpoints":[]}'])(
  'a built-in channel without declared upstreams offers every legacy endpoint (%s)',
  async (setting) => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelsProvider>
          <OpenDialog channel={channel(1, setting)} />
        </ChannelsProvider>
      </QueryClientProvider>
    )

    const options = await endpointOptions()
    expect(options).toEqual([
      'Auto detect (default)',
      'OpenAI (/v1/chat/completions)',
      'OpenAI Responses (/v1/responses)',
      'OpenAI Response Compaction (/v1/responses/compact)',
      'Anthropic (/v1/messages)',
      'Gemini (/v1beta/models/{model}:generateContent)',
      'Jina Rerank (/v1/rerank)',
      'Image Generation (/v1/images/generations)',
      'Embeddings (/v1/embeddings)',
    ])
    expect(api.get).not.toHaveBeenCalledWith('/api/task_plugin_options')
  }
)

test('Auto on a Responses-only channel leaves endpoint resolution to the server', async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog
          channel={channel(1, '{"supported_endpoints":["responses"]}')}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Settings' }))
  const endpoint = await screen.findByRole('combobox', {
    name: 'Endpoint Type',
  })
  expect(endpoint).toHaveValue('Auto detect (default)')
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Test Connection' }))
  await vi.waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      '/api/channel/test/9',
      expect.objectContaining({ params: { model: 'model-a', stream: true } })
    )
  )
  const options = await endpointOptions()
  expect(options).toContain('OpenAI Responses (/v1/responses)')
  expect(options).not.toContain(
    'Gemini (/v1beta/models/{model}:generateContent)'
  )
})

test('a Chat-only channel does not offer explicit Responses overrides', async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel(1, '{"supported_endpoints":["chat"]}')} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const options = await endpointOptions()
  expect(options).toContain('Auto detect (default)')
  expect(options).toContain('OpenAI (/v1/chat/completions)')
  expect(options).toContain('Embeddings (/v1/embeddings)')
  expect(options).not.toContain('OpenAI Responses (/v1/responses)')
  expect(options).not.toContain(
    'OpenAI Response Compaction (/v1/responses/compact)'
  )
})

test.each([
  { endpoints: ['messages'], responses: true, compact: false, gemini: false },
  {
    endpoints: ['chat', 'messages'],
    responses: true,
    compact: false,
    gemini: true,
  },
  {
    endpoints: ['responses', 'messages'],
    responses: true,
    compact: true,
    gemini: false,
  },
  {
    endpoints: ['chat', 'responses', 'messages'],
    responses: true,
    compact: true,
    gemini: true,
  },
])(
  'Messages capabilities $endpoints offer only supported diagnostic client protocols',
  async ({ endpoints, responses, compact, gemini }) => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelsProvider>
          <OpenDialog
            channel={channel(
              1,
              JSON.stringify({ supported_endpoints: endpoints })
            )}
          />
        </ChannelsProvider>
      </QueryClientProvider>
    )

    const options = await endpointOptions()
    expect(options).toContain('OpenAI (/v1/chat/completions)')
    expect(options).toContain('Anthropic (/v1/messages)')
    expect(options.includes('OpenAI Responses (/v1/responses)')).toBe(responses)
    expect(
      options.includes('OpenAI Response Compaction (/v1/responses/compact)')
    ).toBe(compact)
    expect(
      options.includes('Gemini (/v1beta/models/{model}:generateContent)')
    ).toBe(gemini)
  }
)

test.each([
  [
    'OpenAI Response Compaction (/v1/responses/compact)',
    'openai-response-compact',
  ],
  ['Embeddings (/v1/embeddings)', 'embeddings'],
  ['Image Generation (/v1/images/generations)', 'image-generation'],
  ['Jina Rerank (/v1/rerank)', 'jina-rerank'],
])(
  'the %s endpoint forbids streaming even when it defaults on',
  async (label, endpoint) => {
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelsProvider>
          <OpenDialog channel={channel(1)} />
        </ChannelsProvider>
      </QueryClientProvider>
    )
    await user.click(await screen.findByRole('button', { name: 'Settings' }))
    const stream = await screen.findByRole('switch', { name: 'Stream Mode' })
    expect(stream).toBeChecked()

    await user.click(screen.getByRole('combobox', { name: 'Endpoint Type' }))
    await user.click(await screen.findByRole('option', { name: label }))

    expect(stream).not.toBeChecked()
    expect(stream).toHaveAttribute('aria-disabled', 'true')
    await user.click(stream)
    expect(stream).not.toBeChecked()
    fireEvent.keyDown(stream, { key: ' ' })
    fireEvent.keyUp(stream, { key: ' ' })
    expect(stream).not.toBeChecked()
    fireEvent.keyDown(stream, { key: 'Enter' })
    fireEvent.keyUp(stream, { key: 'Enter' })
    expect(stream).not.toBeChecked()
    expect(api.get).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Test Connection' }))
    await vi.waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        '/api/channel/test/9',
        expect.objectContaining({
          params: { model: 'model-a', endpoint_type: endpoint },
        })
      )
    )
  }
)

test('only the model list scrolls while test controls and the footer stay outside it', async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel(1)} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const dialog = await screen.findByRole('dialog')
  const region = within(dialog).getByRole('region', { name: 'Channel models' })
  const scroller = region.querySelector('.overflow-auto') as HTMLElement
  const footer = dialog.querySelector(
    '[data-slot="dialog-footer"]'
  ) as HTMLElement

  expect(dialog).toHaveClass(
    'overflow-hidden',
    '[&>div.overscroll-contain]:overflow-hidden'
  )
  expect(scroller).toHaveClass('min-h-0', 'flex-1')
  expect(dialog.querySelectorAll('.overflow-auto')).toHaveLength(1)
  expect(scroller).toContainElement(within(region).getByRole('table'))
  expect(region.querySelector('[data-slot="table-container"]')).toBeNull()
  expect(dialog).toHaveClass('[&>div.overscroll-contain]:flex-1')
  expect(scroller).not.toContainElement(
    within(dialog).getByRole('button', { name: 'Settings' })
  )
  expect(
    screen.queryByRole('combobox', { name: 'Endpoint Type' })
  ).not.toBeInTheDocument()
  expect(footer).toHaveClass('flex-shrink-0')
  expect(within(footer).getByRole('button', { name: 'Close' })).toBeVisible()
})

test('a short viewport keeps secondary settings out of the model list and footer', async () => {
  vi.stubGlobal('innerWidth', 360)
  vi.stubGlobal('innerHeight', 400)
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel(1)} />
      </ChannelsProvider>
    </QueryClientProvider>
  )
  const dialog = await screen.findByRole('dialog')
  const region = within(dialog).getByRole('region', { name: 'Channel models' })
  const footer = dialog.querySelector(
    '[data-slot="dialog-footer"]'
  ) as HTMLElement
  // jsdom covers the compact interaction/layout contract, not pixel geometry.
  expect(dialog).toHaveClass('h-[min(90dvh,var(--dialog-available-height))]')
  expect(within(dialog).getByPlaceholderText('Filter models...')).toBeVisible()
  expect(region.querySelector('table')).not.toBeNull()
  await user.click(within(dialog).getByRole('button', { name: 'Settings' }))
  const endpoint = await screen.findByRole('combobox', {
    name: 'Endpoint Type',
  })
  expect(dialog).not.toContainElement(endpoint)
  expect(screen.getByRole('switch', { name: 'Stream Mode' })).toBeChecked()
  await user.keyboard('{Escape}')
  await user.click(within(footer).getByRole('button', { name: 'Close' }))
  expect(within(footer).getByRole('button', { name: 'Close' })).toBeVisible()
  expect(api.get).not.toHaveBeenCalled()
})

test.each([false, true])(
  'a Messages-only channel submits HTTP Responses with streaming=%s',
  async (stream) => {
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelsProvider>
          <OpenDialog
            channel={channel(1, '{"supported_endpoints":["messages"]}')}
          />
        </ChannelsProvider>
      </QueryClientProvider>
    )

    await user.click(await screen.findByRole('button', { name: 'Settings' }))
    await user.click(
      await screen.findByRole('combobox', { name: 'Endpoint Type' })
    )
    await user.click(
      await screen.findByRole('option', {
        name: 'OpenAI Responses (/v1/responses)',
      })
    )
    const streamSwitch = screen.getByRole('switch', { name: 'Stream Mode' })
    expect(streamSwitch).toBeEnabled()
    expect(streamSwitch).toBeChecked()
    if (!stream) {
      await user.click(streamSwitch)
      expect(streamSwitch).not.toBeChecked()
    }
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Test Connection' }))
    await vi.waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        '/api/channel/test/9',
        expect.objectContaining({
          params: {
            model: 'model-a',
            endpoint_type: 'openai-response',
            ...(stream ? { stream: true } : {}),
          },
        })
      )
    )
  }
)
