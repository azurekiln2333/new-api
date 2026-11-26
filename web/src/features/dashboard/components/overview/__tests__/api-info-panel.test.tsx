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
  render,
  screen,
  waitFor,
  act,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { ApiInfoPanel } from '../api-info-panel'

let client: QueryClient
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeEach(() => {
  localStorage.clear()
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  client.clear()
  vi.restoreAllMocks()
  if (getAnimationsDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      getAnimationsDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

// Both the public status payload and the viewer-safe instance summary are
// fetched through the same client, so the mocks are routed by URL.
const apiInfoEntries = [
  {
    id: 1,
    url: 'https://api.example.com',
    route: 'CN2 GIA',
    description: 'Primary route',
    color: 'blue',
  },
  {
    id: 2,
    url: 'https://api2.example.com',
    route: 'Backup',
    description: 'Standby route',
    color: 'green',
  },
]

function mockStatusAndInstances(instances: unknown, pending = false) {
  vi.spyOn(api, 'get').mockImplementation((url: string) => {
    if (url.includes('/api/status/instances')) {
      if (pending) return new Promise(() => {})
      return Promise.resolve({ data: { success: true, data: instances } })
    }
    return Promise.resolve({
      data: {
        success: true,
        data: { api_info_enabled: true, api_info: apiInfoEntries },
      },
    })
  })
}

function renderPanel() {
  return render(
    <QueryClientProvider client={client}>
      <ApiInfoPanel />
    </QueryClientProvider>
  )
}

describe('ApiInfoPanel', () => {
  it('renders distinct API cards in configured order while the instance summary is still loading', async () => {
    // Resource loading must never block the API cards: the routes are the
    // primary content and their controls stay usable.
    mockStatusAndInstances([], true)

    renderPanel()

    expect(await screen.findByText('CN2 GIA')).toBeVisible()
    expect(screen.getByText('Backup')).toBeVisible()
    expect(screen.getByText('https://api.example.com')).toBeVisible()
    const cards = screen.getAllByRole('article')
    expect(cards).toHaveLength(2)
    expect(cards.map((card) => card.getAttribute('aria-label'))).toEqual([
      'CN2 GIA',
      'Backup',
    ])
    expect(within(cards[0]).queryByText('Backup')).not.toBeInTheDocument()
    expect(within(cards[0]).queryByRole('link')).not.toBeInTheDocument()
    // No resource sample yet, so the explicit unknown state is shown instead
    // of a fabricated 0%.
    expect(within(cards[0]).getAllByText('Unknown')).toHaveLength(4)
    expect(within(cards[0]).queryByText('0%')).not.toBeInTheDocument()
  })

  it('keeps fractional resources readable under the transparent card hover and in its dialog', async () => {
    const user = userEvent.setup()
    mockStatusAndInstances({
      instances: [
        {
          key: 'api-1',
          name: 'CN2 GIA',
          status: 'healthy',
          domains: ['https://api.example.com'],
          resources: {
            cpu_percent: 12.34,
            memory_percent: 34,
            storage_percent: 56,
          },
          observed_at: 1000,
          stale: false,
          disabled: false,
        },
      ],
      generated_at: 1010,
      monitoring: 'enabled',
    })

    renderPanel()

    expect(await screen.findByText('12.3%')).toBeVisible()
    expect(screen.getByText('34%')).toBeVisible()
    expect(screen.getByText('56%')).toBeVisible()
    const card = screen.getByRole('article', { name: 'CN2 GIA' })
    const trigger = within(card).getByRole('button', { name: 'CN2 GIA' })
    // jsdom cannot paint hover layers; protect the transparent overlay contract
    // in both themes as well as the visible labels under real hover events.
    expect(card).toHaveClass('hover:bg-muted/40')
    expect(trigger).toHaveClass(
      'bg-transparent',
      'hover:bg-transparent',
      'dark:hover:bg-transparent'
    )
    expect(trigger).not.toHaveClass('hover:bg-muted')
    expect(trigger).not.toHaveClass('dark:hover:bg-muted/50')
    await user.hover(trigger)
    expect(within(card).getByText('https://api.example.com')).toBeVisible()
    expect(within(card).getByText('12.3%')).toBeVisible()
    expect(within(card).getByText('34%')).toBeVisible()
    expect(within(card).getByText('56%')).toBeVisible()
    expect(within(card).getByText('Online')).toBeVisible()
    for (const label of ['CPU', 'Memory', 'Storage']) {
      const metric = within(card).getByRole('group', { name: label })
      expect(metric.querySelector('svg circle')).not.toBeNull()
    }
    const knownValue = within(card).getByText('12.3%')
    const unknownValue = within(screen.getByRole('article', { name: 'Backup' }))
      .getAllByText('Unknown')
      .at(-1)
    expect(knownValue).toHaveClass('text-[11px]')
    expect(unknownValue).toHaveClass('text-[11px]')
    // The second, unbound card knows nothing and says so.
    expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0)

    await user.click(trigger)
    const detail = within(
      await screen.findByRole('dialog', { name: 'CN2 GIA' })
    )
    const percentage = detail.getByText('12.3%')
    expect(percentage).toBeVisible()
    expect(percentage).toHaveClass('text-[11px]')
    expect(percentage.previousElementSibling).toHaveAttribute(
      'aria-hidden',
      'true'
    )
    expect(detail.queryByText('12.34%')).not.toBeInTheDocument()
  })

  it('renders a legitimate zero percent reading as 0% rather than unknown', async () => {
    mockStatusAndInstances({
      instances: [
        {
          key: 'api-1',
          name: 'CN2 GIA',
          status: 'healthy',
          domains: ['https://api.example.com'],
          resources: {
            cpu_percent: 0,
            memory_percent: 34,
            storage_percent: 56,
          },
          observed_at: 1000,
          stale: false,
          disabled: false,
        },
      ],
      generated_at: 1010,
      monitoring: 'enabled',
    })

    renderPanel()

    expect(await screen.findByText('0%')).toBeVisible()
  })

  it('opens a centered dialog from the whole API card and returns keyboard focus on close', async () => {
    const user = userEvent.setup()
    mockStatusAndInstances({
      instances: [
        {
          key: 'api-1',
          name: 'CN2 GIA',
          status: 'healthy',
          domains: ['https://api.example.com'],
          resources: {
            cpu_percent: 12,
            memory_percent: 34,
            storage_percent: 56,
          },
          observed_at: 1000,
          stale: false,
          disabled: false,
        },
      ],
      generated_at: 1010,
      monitoring: 'enabled',
    })

    renderPanel()

    const trigger = await screen.findByRole('button', { name: 'CN2 GIA' })
    expect(trigger).toHaveAttribute('aria-haspopup', 'dialog')
    expect(trigger).toHaveClass('absolute', 'inset-0')
    trigger.focus()
    await user.keyboard('{Enter}')

    // The detail surface states only the approved fields for this entry.
    const dialog = screen.getByRole('dialog', { name: 'CN2 GIA' })
    expect(dialog).toHaveAttribute('data-slot', 'dialog-content')
    expect(dialog).toHaveClass('left-1/2')
    expect(dialog).not.toHaveAttribute('data-slot', 'sheet-content')
    const detail = within(dialog)
    await waitFor(() => {
      expect(detail.getByText('Domains')).toBeVisible()
    })
    expect(detail.getByText('Primary route')).toBeVisible()
    expect(detail.getByText('12%')).toBeVisible()
    expect(detail.queryByText('Standby route')).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(trigger).toHaveFocus())
    await user.keyboard(' ')
    expect(await screen.findByRole('dialog', { name: 'CN2 GIA' })).toBeVisible()
  })

  it('keeps copy and latency controls independent of the whole-card dialog trigger', async () => {
    mockStatusAndInstances([], true)
    const user = userEvent.setup()
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    const fetch = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue({} as Response)
    const external = vi.spyOn(window, 'open').mockReturnValue(null)
    renderPanel()

    const card = await screen.findByRole('article', { name: 'CN2 GIA' })
    const copyButton = within(card).getByRole('button', { name: 'Copy URL' })
    copyButton.focus()
    await user.keyboard('{Enter}')
    await waitFor(() =>
      expect(copy).toHaveBeenCalledWith('https://api.example.com')
    )
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    await user.click(within(card).getByRole('button', { name: 'Test Latency' }))
    await waitFor(() => expect(fetch).toHaveBeenCalled())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.click(
      within(card).getByRole('button', { name: 'External Speed Test' })
    )
    expect(external).toHaveBeenCalledWith(
      'https://www.tcptest.cn/http/https%3A%2F%2Fapi.example.com',
      '_blank',
      'noopener,noreferrer'
    )
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps a legacy route selected when entries sharing its URL are reordered', async () => {
    const user = userEvent.setup()
    const legacyEntries = [
      { ...apiInfoEntries[0], id: 0 },
      { ...apiInfoEntries[1], id: 0, url: apiInfoEntries[0].url },
    ]
    vi.spyOn(api, 'get').mockImplementation((url: string) => {
      if (url.includes('/api/status/instances')) {
        return Promise.resolve({
          data: { success: true, data: { instances: [] } },
        })
      }
      return Promise.resolve({
        data: {
          success: true,
          data: { api_info_enabled: true, api_info: legacyEntries },
        },
      })
    })

    renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Backup' }))
    const detail = within(screen.getByRole('dialog'))
    expect(detail.getByText('Standby route')).toBeVisible()

    await act(async () => {
      client.setQueryData(STATUS_QUERY_KEY, {
        api_info_enabled: true,
        api_info: [legacyEntries[1], legacyEntries[0]],
      })
    })

    await waitFor(() => {
      expect(detail.getByText('Standby route')).toBeVisible()
      expect(detail.queryByText('Primary route')).not.toBeInTheDocument()
    })
  })

  it('reports an operator-disabled instance instead of pretending it is unknown', async () => {
    mockStatusAndInstances({
      instances: [
        {
          key: 'api-1',
          name: 'CN2 GIA',
          status: 'disabled',
          domains: ['https://api.example.com'],
          resources: {
            cpu_percent: null,
            memory_percent: null,
            storage_percent: 40,
          },
          observed_at: 1000,
          stale: false,
          disabled: true,
        },
      ],
      generated_at: 1010,
      monitoring: 'disabled',
    })

    renderPanel()

    const card = await screen.findByRole('article', { name: 'CN2 GIA' })
    expect(await within(card).findByText('Disabled')).toBeVisible()
    expect(within(card).queryByText('Offline')).not.toBeInTheDocument()
    expect(within(card).getByRole('group', { name: 'CPU' })).toHaveTextContent(
      'Unknown'
    )
    expect(within(card).getByText('40%')).toBeVisible()
  })

  it('shows a stale heartbeat as offline without replacing its last sample with zero', async () => {
    mockStatusAndInstances({
      instances: [
        {
          key: 'api-1',
          name: 'CN2 GIA',
          status: 'stale',
          domains: ['https://api.example.com', 'https://api2.example.com'],
          resources: {
            cpu_percent: 12,
            memory_percent: null,
            storage_percent: 56,
          },
          observed_at: 1000,
          stale: true,
          disabled: false,
        },
      ],
      generated_at: 2000,
      monitoring: 'enabled',
    })
    renderPanel()

    for (const label of ['CN2 GIA', 'Backup']) {
      const card = await screen.findByRole('article', { name: label })
      expect(await within(card).findByText('Offline')).toBeVisible()
      expect(within(card).getByText('12%')).toBeVisible()
      expect(within(card).getByText('56%')).toBeVisible()
      expect(
        within(within(card).getByRole('group', { name: 'Memory' })).getByText(
          'Unknown'
        )
      ).toHaveClass('text-[11px]')
      expect(within(card).queryByText('0%')).not.toBeInTheDocument()
      expect(within(card).queryByText('Disabled')).not.toBeInTheDocument()
    }
  })

  it('keeps the open node detail on the newest sample after a refetch', async () => {
    const user = userEvent.setup()
    const instance = (cpu: number) => ({
      instances: [
        {
          key: 'api-1',
          name: 'CN2 GIA',
          status: 'healthy',
          domains: ['https://api.example.com'],
          resources: {
            cpu_percent: cpu,
            memory_percent: 34,
            storage_percent: 56,
          },
          observed_at: 1000,
          stale: false,
          disabled: false,
        },
      ],
      generated_at: 1010,
      monitoring: 'enabled',
    })

    let call = 0
    vi.spyOn(api, 'get').mockImplementation((url: string) => {
      if (url.includes('/api/status/instances')) {
        call += 1
        // The second response represents the next 30s sample.
        return Promise.resolve({
          data: { success: true, data: instance(call === 1 ? 12 : 77) },
        })
      }
      return Promise.resolve({
        data: {
          success: true,
          data: { api_info_enabled: true, api_info: apiInfoEntries },
        },
      })
    })

    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'CN2 GIA' }))
    const detail = within(screen.getByRole('dialog'))
    await waitFor(() => {
      expect(detail.getByText('12%')).toBeVisible()
    })

    // Force the next poll; the open dialog must not keep the old snapshot.
    await act(async () => {
      await client.refetchQueries({
        queryKey: ['dashboard', 'overview', 'instance-resources'],
      })
    })

    await waitFor(() => {
      expect(detail.getByText('77%')).toBeVisible()
    })
    expect(detail.queryByText('12%')).not.toBeInTheDocument()
  })
})
