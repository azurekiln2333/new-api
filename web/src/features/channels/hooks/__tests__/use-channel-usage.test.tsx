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
  act,
  cleanup,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import { createInstance } from 'i18next'
import type { ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  ChannelUsageContext,
  ChannelUsedTokens,
} from '../../components/channel-usage-context'
import { channelsQueryKeys } from '../../lib/channel-actions'
import type { ChannelUsageSummary } from '../../types'
import { useChannelUsageSummaries } from '../use-channel-usage'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

let client: QueryClient

function usage(overrides: Partial<ChannelUsageSummary>): ChannelUsageSummary {
  return {
    channel_id: 1,
    request_count: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    quota: 0,
    ...overrides,
  }
}

function QueryWrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

test('summaries hook keys usage by channel id and requests duplicate ids once', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          usage({ channel_id: 3, total_tokens: 30 }),
          usage({ channel_id: 5, total_tokens: 50 }),
        ],
      },
    },
  })

  const { result } = renderHook(() => useChannelUsageSummaries([5, 3, 5]), {
    wrapper: QueryWrapper,
  })

  await waitFor(() => expect(result.current.usageByChannel[3]).toBeDefined())
  expect(result.current.usageByChannel[3].total_tokens).toBe(30)
  expect(result.current.usageByChannel[5].total_tokens).toBe(50)
  expect(get).toHaveBeenCalledOnce()
  expect(get).toHaveBeenCalledWith(
    '/api/channel/usage',
    expect.objectContaining({ params: { ids: '3,5' } })
  )
})

test('summaries hook skips the request when there are no channels', () => {
  const get = vi.spyOn(api, 'get')

  const { result } = renderHook(() => useChannelUsageSummaries([]), {
    wrapper: QueryWrapper,
  })

  expect(get).not.toHaveBeenCalled()
  expect(result.current.usageByChannel).toEqual({})
})

test('usage is refetched when the channel list is invalidated', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [usage({ channel_id: 3, total_tokens: 30 })] },
    },
  })

  const { result } = renderHook(() => useChannelUsageSummaries([3]), {
    wrapper: QueryWrapper,
  })

  await waitFor(() => expect(result.current.usageByChannel[3]).toBeDefined())
  await act(async () => {
    await client.invalidateQueries({ queryKey: channelsQueryKeys.all })
  })

  expect(get).toHaveBeenCalledTimes(2)
  expect(result.current.usageByChannel[3].total_tokens).toBe(30)
})

test('summaries hook requests the new id set when the page changes', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [usage({ channel_id: 3, total_tokens: 30 })] },
    },
  })

  const { rerender } = renderHook(
    (props: { ids: number[] }) => useChannelUsageSummaries(props.ids),
    { wrapper: QueryWrapper, initialProps: { ids: [3] } }
  )
  await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
  rerender({ ids: [3, 7] })

  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(get).toHaveBeenLastCalledWith(
    '/api/channel/usage',
    expect.objectContaining({ params: { ids: '3,7' } })
  )
})

function renderUsedTokens(
  map: Record<number, ChannelUsageSummary>,
  tooltipOpen = false
) {
  render(
    <I18nextProvider i18n={i18n}>
      <ChannelUsageContext.Provider value={map}>
        <ChannelUsedTokens channelId={1} tooltipOpen={tooltipOpen} />
      </ChannelUsageContext.Provider>
    </I18nextProvider>
  )
}

test('used tokens shows the exact and compact total without clipping wrapped lines', () => {
  renderUsedTokens({
    1: usage({ total_tokens: 1_500_000, prompt_tokens: 1_000_000 }),
  })

  expect(screen.getByText('1,500,000 (1.5M)')).toBeVisible()
  expect(screen.getByText('1,500,000 (1.5M)')).toHaveClass(
    'h-auto',
    'whitespace-normal'
  )
})

test('used tokens keeps the input/output split in a tooltip instead of the row text', () => {
  renderUsedTokens(
    {
      1: usage({
        total_tokens: 1_500_000,
        prompt_tokens: 1_000_000,
        completion_tokens: 500_000,
        request_count: 42,
      }),
    },
    true
  )

  const split = document.querySelector('[data-slot=tooltip-content]')
  expect(split?.textContent).toBe(
    'Total tokens: 1,500,000\nInput tokens: 1,000,000\nOutput tokens: 500,000\nRequests: 42'
  )
})

test('used tokens shows a dash when the channel has no usage entry', () => {
  renderUsedTokens({})

  expect(screen.getByText('-')).toBeVisible()
})
