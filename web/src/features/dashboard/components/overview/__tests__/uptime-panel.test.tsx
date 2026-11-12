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

import { api } from '@/lib/api'

import { UptimePanel } from '../uptime-panel'

let client: QueryClient
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeEach(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
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

function renderPanel() {
  return render(
    <QueryClientProvider client={client}>
      <UptimePanel />
    </QueryClientProvider>
  )
}

const monitoredService = {
  success: true,
  data: [
    {
      categoryName: 'Services',
      monitors: [{ name: 'Gateway', uptime: 0.995, status: 1 }],
    },
  ],
}

describe('UptimePanel', () => {
  it('shows an empty state only when the server successfully returns no groups', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: [] },
    })

    renderPanel()

    expect(
      await screen.findByText('No uptime monitoring configured')
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Retry' })
    ).not.toBeInTheDocument()
  })

  it('shows a network failure instead of unconfigured monitoring and retries successfully', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get')
      .mockRejectedValueOnce(new Error('Monitoring service unavailable'))
      .mockResolvedValueOnce({ data: monitoredService })

    renderPanel()

    expect(
      await screen.findByText('Monitoring service unavailable')
    ).toBeVisible()
    expect(
      screen.queryByText('No uptime monitoring configured')
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Gateway')).toBeVisible()
    expect(screen.getByText('99.50%')).toBeVisible()
    expect(
      screen.queryByText('Monitoring service unavailable')
    ).not.toBeInTheDocument()
  })

  it('treats a failed business response as an error rather than an empty configuration', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: false,
        message: 'Monitoring request rejected',
        data: [],
      },
    })

    renderPanel()

    expect(await screen.findByText('Monitoring request rejected')).toBeVisible()
    expect(
      screen.queryByText('No uptime monitoring configured')
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  })

  it('keeps the previous monitors visible when a refresh fails and clears the error on retry', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get')
      .mockResolvedValueOnce({ data: monitoredService })
      .mockRejectedValueOnce(new Error('Refresh unavailable'))
      .mockResolvedValueOnce({ data: monitoredService })

    renderPanel()

    expect(await screen.findByText('Gateway')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('Refresh unavailable')).toBeVisible()
    expect(screen.getByText('Gateway')).toBeVisible()
    expect(
      screen.queryByText('No uptime monitoring configured')
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => {
      expect(screen.queryByText('Refresh unavailable')).not.toBeInTheDocument()
    })
    expect(screen.getByText('Gateway')).toBeVisible()
  })
})
