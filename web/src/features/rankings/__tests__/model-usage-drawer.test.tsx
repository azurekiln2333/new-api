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
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ModelUsageDrawer } from '../components/model-usage-drawer'

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
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.setUser(null)
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

const usagePayload = {
  success: true,
  data: {
    model_name: 'gpt-fixture',
    period: 'week',
    start_time: 0,
    end_time: 0,
    generated_at: 1010,
    totals: { requests: 80, tokens: 8000, quota: 800 },
    users: [
      {
        rank: 1,
        masked: false,
        display_name: 'alice',
        user_id: 11,
        requests: 50,
        tokens: 5000,
        quota: 500,
      },
      {
        rank: 2,
        masked: true,
        display_name: 's***n',
        requests: 30,
        tokens: 3000,
        quota: 300,
      },
    ],
  },
}

function renderDrawer(onOpenChange = vi.fn()) {
  const utils = render(
    <QueryClientProvider client={client}>
      <ModelUsageDrawer
        modelName='gpt-fixture'
        period='week'
        open
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>
  )
  return { ...utils, onOpenChange }
}

describe('ModelUsageDrawer', () => {
  it('shows the model totals and the user ranking instead of navigating to pricing', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: usagePayload })

    renderDrawer()

    expect(await screen.findByText('5.0K')).toBeVisible()
    expect(screen.getByText('alice')).toBeVisible()
    // Masked identities stay partial exactly as the server sent them; the UI
    // must not attempt to unmask or re-derive a name.
    expect(screen.getByText('s***n')).toBeVisible()
    // The drawer is not a link to the pricing page.
    expect(
      screen.getByRole('dialog').querySelector('a[href*="/pricing"]')
    ).toBeNull()
  })

  it('partitions by viewer identity and role and renders the server mask unchanged', async () => {
    const response = (users: typeof usagePayload.data.users) => ({
      data: {
        ...usagePayload,
        data: { ...usagePayload.data, users },
      },
    })
    const request = vi
      .spyOn(api, 'get')
      .mockResolvedValueOnce(
        response([
          {
            rank: 1,
            masked: false,
            display_name: 'surface-admin',
            user_id: 11,
            requests: 50,
            tokens: 5000,
            quota: 500,
          },
          {
            rank: 2,
            masked: false,
            display_name: 'surface-ordinary',
            user_id: 22,
            requests: 30,
            tokens: 3000,
            quota: 300,
          },
        ])
      )
      .mockResolvedValueOnce(
        response([
          {
            rank: 1,
            masked: false,
            display_name: 'surface-admin',
            user_id: 11,
            requests: 50,
            tokens: 5000,
            quota: 500,
          },
          {
            rank: 2,
            masked: true,
            display_name: 's***y',
            requests: 30,
            tokens: 3000,
            quota: 300,
          },
        ])
      )
      .mockResolvedValueOnce(
        response([
          {
            rank: 1,
            masked: true,
            display_name: 's***n',
            requests: 50,
            tokens: 5000,
            quota: 500,
          },
          {
            rank: 2,
            masked: false,
            display_name: 'surface-ordinary',
            user_id: 22,
            requests: 30,
            tokens: 3000,
            quota: 300,
          },
        ])
      )

    useAuthStore.getState().auth.setUser({
      id: 11,
      username: 'surface-admin',
      role: ROLE.ADMIN,
    })
    renderDrawer()

    expect(await screen.findByText('surface-ordinary')).toBeVisible()
    expect(request.mock.calls[0]?.[1]?.signal).toBeInstanceOf(AbortSignal)
    expect(
      client
        .getQueryCache()
        .find({ queryKey: ['rankings', 'model-usage'], exact: false })
        ?.queryKey.slice(-2)
    ).toEqual([11, ROLE.ADMIN])

    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 11,
        username: 'surface-admin',
        role: ROLE.USER,
      })
    })

    expect(await screen.findByText('s***y')).toBeVisible()
    expect(screen.getByText('surface-admin')).toBeVisible()
    expect(screen.queryByText('surface-ordinary')).not.toBeInTheDocument()
    expect(request).toHaveBeenCalledTimes(2)
    expect(
      client
        .getQueryCache()
        .find({
          queryKey: ['rankings', 'model-usage', 'gpt-fixture', 'week', 11, ROLE.USER],
          exact: true,
        })
        ?.queryKey.slice(-2)
    ).toEqual([11, ROLE.USER])

    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 22,
        username: 'surface-ordinary',
        role: ROLE.USER,
      })
    })

    expect(await screen.findByText('s***n')).toBeVisible()
    expect(screen.getByText('surface-ordinary')).toBeVisible()
    expect(screen.queryByText('surface-admin')).not.toBeInTheDocument()
    expect(request).toHaveBeenCalledTimes(3)
    expect(
      client
        .getQueryCache()
        .find({
          queryKey: ['rankings', 'model-usage', 'gpt-fixture', 'week', 22, ROLE.USER],
          exact: true,
        })
        ?.queryKey.slice(-2)
    ).toEqual([22, ROLE.USER])
  })

  it('renders a failure as an error with a retry action rather than zero usage', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get')
      .mockRejectedValueOnce(new Error('Usage service unavailable'))
      .mockResolvedValueOnce({ data: usagePayload })

    renderDrawer()

    expect(
      await screen.findByText('Failed to load usage for this model')
    ).toBeVisible()
    // A failed load must not be presented as a real zero total.
    expect(screen.queryByText('0')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /retry/i }))
    await waitFor(() => {
      expect(screen.getByText('alice')).toBeVisible()
    })
    expect(
      screen.queryByText('Failed to load usage for this model')
    ).not.toBeInTheDocument()
  })

  it('renders the quota total with the currency formatter, not as a token count', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: usagePayload })

    renderDrawer()

    const quotaLabel = await screen.findByText('Quota')
    // The quota value is money-formatted; the raw token count string must not
    // appear in the quota cell.
    const quotaCell = quotaLabel.parentElement
    expect(quotaCell).not.toBeNull()
    expect(quotaCell?.textContent ?? '').not.toBe('800')
  })

  it('shows each ranked user quota alongside tokens and requests', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: usagePayload })

    renderDrawer()

    const row = (await screen.findByText('alice')).closest('li')
    expect(row).not.toBeNull()
    // Tokens and requests are still shown; the row now also carries its own
    // quota, labelled so the amount is not a bare number.
    const cells = within(row as HTMLElement)
    expect(cells.getByText('5.0K')).toBeVisible()
    expect(cells.getByText('50 requests')).toBeVisible()
    const rowText = row?.textContent ?? ''
    expect(rowText).toContain('Quota:')

    // The masked row keeps its quota: masking hides identity, not usage.
    const maskedRow = screen.getByText('s***n').closest('li')
    expect(maskedRow?.textContent ?? '').toContain('Quota:')
  })
})
