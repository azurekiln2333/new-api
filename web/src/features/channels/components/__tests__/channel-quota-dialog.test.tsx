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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { channelSchema, type ChannelBudgetView } from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { ChannelQuotaDialog } from '../dialogs/channel-quota-dialog'

let client: QueryClient
const channel = channelSchema.parse({
  id: 7,
  type: 8,
  key: '',
  status: 1,
  name: 'No upstream balance',
  created_time: 1,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
})
const budget: ChannelBudgetView = {
  scope_type: 'channel',
  scope_id: '7',
  channel_id: 7,
  ledger_enabled: true,
  enabled: true,
  limit_quota: 100,
  period_used_quota: 25,
  lifetime_used_quota: 50,
  reserved_quota: 10,
  remaining_quota: 65,
  config_version: 3,
  reset_epoch: 2,
  accounting_started_at: 1,
}
function OpenBudget() {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(channel)
  }, [setCurrentRow])
  return <ChannelQuotaDialog open onOpenChange={() => {}} />
}
function renderBudget(view = budget) {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: view } })
  render(
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <OpenBudget />
      </ChannelsProvider>
    </QueryClientProvider>
  )
  return get
}
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

test('a channel without upstream balance can edit its local limit and reload a CAS conflict without retrying the mutation', async () => {
  const get = renderBudget()
  const put = vi.spyOn(api, 'put').mockRejectedValue({
    response: {
      status: 409,
      data: { success: false, code: 'channel_budget_version_conflict' },
    },
  })
  const user = userEvent.setup()
  const input = await screen.findByRole('textbox', {
    name: 'Local limit (site quota units)',
  })
  await user.clear(input)
  await user.type(input, '200')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(input).toHaveValue('100')
  expect(put).toHaveBeenCalledTimes(1)
  expect(put).toHaveBeenCalledWith(
    '/api/channel/7/quota',
    { enabled: true, limit_quota: 200, expected_config_version: 3 },
    expect.objectContaining({ singleUseAuthorization: true })
  )
  expect(screen.queryByText('Query Balance')).not.toBeInTheDocument()
})

test('reset requires confirmation and keeps its operation ID for an uncertain failed request', async () => {
  renderBudget()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'channel budget is not available' },
  })
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Reset used amount' })
  )
  expect(post).not.toHaveBeenCalled()
  const confirm = within(screen.getByRole('alertdialog'))
  await user.click(confirm.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(confirm.getByRole('button', { name: 'Reset' })).toBeEnabled()
  )
  await user.click(confirm.getByRole('button', { name: 'Reset' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  const first = post.mock.calls[0][1] as {
    expected_config_version: number
    operation_id: string
  }
  expect(first.expected_config_version).toBe(3)
  expect(first.operation_id).toBeTruthy()
  expect(post.mock.calls[1][1]).toEqual(first)
})

test('inactive ledger shows an explicit state instead of an editable or unlimited budget', async () => {
  renderBudget({ ...budget, ledger_enabled: false })
  expect(
    await screen.findByText('Channel budget accounting is not enabled')
  ).toBeVisible()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Reset used amount' })
  ).not.toBeInTheDocument()
})

test('read-only channel permission cannot save or reset a local budget', async () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'reader',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { channel: { read: true } } },
  })
  renderBudget()
  expect(
    await screen.findByRole('textbox', {
      name: 'Local limit (site quota units)',
    })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Reset used amount' })
  ).toBeDisabled()
})
