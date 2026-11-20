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
import { cleanup, render, renderHook, screen, waitFor } from '@testing-library/react'
import { createInstance } from 'i18next'
import type { ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  ChannelConcurrencyContext,
  ChannelConcurrencyCell,
} from '../../components/channel-concurrency-context'
import type { ChannelConcurrencyEntry } from '../../types'
import {
  CHANNEL_CONCURRENCY_BATCH_SIZE,
  CHANNEL_CONCURRENCY_REFRESH_MS,
  useChannelConcurrency,
} from '../use-channel-concurrency'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

let client: QueryClient

function entry(
  overrides: Partial<ChannelConcurrencyEntry>
): ChannelConcurrencyEntry {
  return {
    id: 1,
    current: 0,
    limit: null,
    scope: 'instance',
    sample_time: 1_700_000_000_000,
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

test('samples the whole visible set with one batched request', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          entry({ id: 3, current: 2, limit: 5 }),
          entry({ id: 5, current: 0, limit: 1 }),
        ],
      },
    },
  })

  const { result } = renderHook(() => useChannelConcurrency([5, 3, 5]), {
    wrapper: QueryWrapper,
  })

  await waitFor(() => expect(result.current.entriesById[3]).toBeDefined())
  expect(result.current.entriesById[3].current).toBe(2)
  expect(result.current.entriesById[5].limit).toBe(1)
  expect(get).toHaveBeenCalledOnce()
  expect(get).toHaveBeenCalledWith(
    '/api/channel/concurrency',
    expect.objectContaining({ params: { ids: '3,5' } })
  )
})

test('splits a set larger than the endpoint cap instead of truncating it', async () => {
  const ids = Array.from(
    { length: CHANNEL_CONCURRENCY_BATCH_SIZE + 2 },
    (_, index) => index + 1
  )
  const requestedIds = (config: { params?: unknown } | undefined) =>
    String((config?.params as { ids?: string } | undefined)?.ids ?? '').split(
      ','
    )
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    return {
      data: {
        success: true,
        data: {
          items: requestedIds(config).map((id) => entry({ id: Number(id) })),
        },
      },
    }
  })

  const { result } = renderHook(() => useChannelConcurrency(ids), {
    wrapper: QueryWrapper,
  })

  await waitFor(() =>
    expect(Object.keys(result.current.entriesById)).toHaveLength(ids.length)
  )
  expect(get).toHaveBeenCalledTimes(2)
  expect(get.mock.calls.map(([, config]) => requestedIds(config).length)).toEqual(
    [CHANNEL_CONCURRENCY_BATCH_SIZE, 2]
  )
})

test('does not sample anything when no channel is visible', () => {
  const get = vi.spyOn(api, 'get')

  const { result } = renderHook(() => useChannelConcurrency([]), {
    wrapper: QueryWrapper,
  })

  expect(get).not.toHaveBeenCalled()
  expect(result.current.entriesById).toEqual({})
})

test('resamples when the visible set changes to another page', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [entry({ id: 3, current: 1 })] } },
  })

  const { rerender } = renderHook(
    (props: { ids: number[] }) => useChannelConcurrency(props.ids),
    { wrapper: QueryWrapper, initialProps: { ids: [3] } }
  )
  await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
  rerender({ ids: [7] })

  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(get).toHaveBeenLastCalledWith(
    '/api/channel/concurrency',
    expect.objectContaining({ params: { ids: '7' } })
  )
})

test('keeps the last sample and marks it stale when a refresh fails', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { items: [entry({ id: 3, current: 4, limit: 8 })] },
      },
    })
    .mockRejectedValue(new Error('coordinator down'))

  const { result } = renderHook(() => useChannelConcurrency([3]), {
    wrapper: QueryWrapper,
  })

  await waitFor(() => expect(result.current.entriesById[3]?.current).toBe(4))
  expect(result.current.stale).toBe(false)

  await client.refetchQueries({ queryKey: ['channels', 'concurrency'] })

  await waitFor(() => expect(result.current.stale).toBe(true))
  expect(result.current.entriesById[3].current).toBe(4)
  expect(get).toHaveBeenCalledTimes(2)
})

test('refreshes the shared sample every five seconds', async () => {
  vi.useFakeTimers()
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [entry({ id: 3, current: 1 })] } },
  })

  try {
    renderHook(() => useChannelConcurrency([3]), { wrapper: QueryWrapper })
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1))

    await vi.advanceTimersByTimeAsync(CHANNEL_CONCURRENCY_REFRESH_MS / 2)
    expect(get).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(CHANNEL_CONCURRENCY_REFRESH_MS / 2)
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  } finally {
    vi.useRealTimers()
  }
})

test('pauses the refresh timer while the tab is hidden', async () => {
  vi.useFakeTimers()
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [entry({ id: 3, current: 1 })] } },
  })
  const originalVisibility = Object.getOwnPropertyDescriptor(
    document,
    'visibilityState'
  )

  try {
    const { result } = renderHook(() => useChannelConcurrency([3]), {
      wrapper: QueryWrapper,
    })
    await vi.waitFor(() => expect(result.current.entriesById[3]).toBeDefined())
    expect(get).toHaveBeenCalledTimes(1)

    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => 'hidden',
    })
    document.dispatchEvent(new Event('visibilitychange'))

    await vi.advanceTimersByTimeAsync(CHANNEL_CONCURRENCY_REFRESH_MS * 3)
    expect(get).toHaveBeenCalledTimes(1)
  } finally {
    if (originalVisibility) {
      Object.defineProperty(document, 'visibilityState', originalVisibility)
    }
    document.dispatchEvent(new Event('visibilitychange'))
    vi.useRealTimers()
  }
})

function renderCell(
  value: {
    entriesById: Record<number, ChannelConcurrencyEntry>
    isPending: boolean
    stale: boolean
  },
  props: { channelId: number | null; aggregate?: boolean }
) {
  render(
    <I18nextProvider i18n={i18n}>
      <ChannelConcurrencyContext.Provider value={value}>
        <ChannelConcurrencyCell {...props} />
      </ChannelConcurrencyContext.Provider>
    </I18nextProvider>
  )
}

const settled = { isPending: false, stale: false }

test('shows the live count against a configured limit', () => {
  renderCell(
    { ...settled, entriesById: { 1: entry({ current: 3, limit: 10 }) } },
    { channelId: 1 }
  )

  expect(screen.getByText('3/10')).toBeVisible()
})

test('shows the live count of an unlimited channel without inventing a cap', () => {
  renderCell(
    { ...settled, entriesById: { 1: entry({ current: 7, limit: null }) } },
    { channelId: 1 }
  )

  expect(screen.getByText('7 / ∞')).toBeVisible()
})

test('shows unavailable, never zero, when the sample could not be read', () => {
  renderCell(
    {
      ...settled,
      entriesById: { 1: entry({ current: null, limit: 10 }) },
    },
    { channelId: 1 }
  )

  expect(screen.getByText('Unavailable')).toBeVisible()
  expect(screen.queryByText('0/10')).not.toBeInTheDocument()
})

test('renders an idle channel as a real zero sample', () => {
  renderCell(
    { ...settled, entriesById: { 1: entry({ current: 0, limit: 10 }) } },
    { channelId: 1 }
  )

  expect(screen.getByText('0/10')).toBeVisible()
})

test('a tag aggregate row has no channel of its own to sample', () => {
  renderCell(
    { ...settled, entriesById: { 1: entry({ current: 3, limit: 10 }) } },
    { channelId: 1, aggregate: true }
  )

  expect(screen.getByText('-')).toBeVisible()
  expect(screen.queryByText('3/10')).not.toBeInTheDocument()
})

test('shows a pending placeholder before the first sample arrives', () => {
  renderCell({ entriesById: {}, isPending: true, stale: false }, { channelId: 1 })

  expect(screen.getByText('…')).toBeVisible()
})

test('keeps a stale sample readable instead of hiding it in a tooltip', () => {
  renderCell(
    {
      entriesById: { 1: entry({ current: 3, limit: 10 }) },
      isPending: false,
      stale: true,
    },
    { channelId: 1 }
  )

  expect(screen.getByText('3/10')).toBeVisible()
  expect(screen.getByText('Refresh failed')).toBeVisible()
  expect(
    document.querySelector('[data-concurrency-stale]')
  ).toBeInTheDocument()
})