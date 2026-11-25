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
    // /api/status returns the status map itself, not an envelope.
    return Promise.resolve({
      data: { api_info_enabled: true, api_info: apiInfoEntries },
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
  it('renders every configured API card while the instance summary is still loading', async () => {
    // Resource loading must never block the API cards: the routes are the
    // primary content and their controls stay usable.
    mockStatusAndInstances([], true)

    renderPanel()

    expect(await screen.findByText('CN2 GIA')).toBeVisible()
    expect(screen.getByText('Backup')).toBeVisible()
    expect(screen.getByText('https://api.example.com')).toBeVisible()
    // No resource sample yet, so the explicit unknown state is shown instead
    // of a fabricated 0%.
    expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0)
  })

  it('shows the bound instance resources on the matching API card', async () => {
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

    expect(await screen.findByText('12%')).toBeVisible()
    expect(screen.getByText('34%')).toBeVisible()
    expect(screen.getByText('56%')).toBeVisible()
    // The second, unbound card knows nothing and says so.
    expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0)
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

  it('opens the filtered node detail when a configured route is clicked', async () => {
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

    await user.click(await screen.findByRole('button', { name: 'CN2 GIA' }))

    // The detail surface states the approved fields for this entry.
    await waitFor(() => {
      expect(screen.getByText('Domains')).toBeVisible()
    })
    expect(screen.getByText('Primary route')).toBeVisible()
    // Percentages go through the shared formatter, not a hand-rolled toFixed.
    expect(screen.getByText('12%')).toBeVisible()
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
        data: { api_info_enabled: true, api_info: legacyEntries },
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

    expect(await screen.findByText('Disabled')).toBeVisible()
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
        data: { api_info_enabled: true, api_info: apiInfoEntries },
      })
    })

    renderPanel()

    await user.click(await screen.findByRole('button', { name: 'CN2 GIA' }))
    await waitFor(() => {
      expect(screen.getByText('12%')).toBeVisible()
    })

    // Force the next poll; the open drawer must not keep the old snapshot.
    await act(async () => {
      await client.refetchQueries({
        queryKey: ['dashboard', 'overview', 'instance-resources'],
      })
    })

    await waitFor(() => {
      expect(screen.getByText('77%')).toBeVisible()
    })
    expect(screen.queryByText('12%')).not.toBeInTheDocument()
  })
})
