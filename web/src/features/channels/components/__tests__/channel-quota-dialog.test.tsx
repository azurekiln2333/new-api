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
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { channelSchema, type ChannelBudgetView } from '../../types'
import { ChannelBudgetCell, ChannelQuotaCell } from '../channel-budget-cell'
import { ChannelBudgetContext } from '../channel-budget-context'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { ChannelQuotaDialog } from '../dialogs/channel-quota-dialog'

const originalCurrency = useSystemConfigStore.getState().config.currency
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
const displayBudget: ChannelBudgetView = {
  ...budget,
  limit_quota: 250_000_000,
  period_used_quota: 12_500_000,
  lifetime_used_quota: 25_000_000,
  reserved_quota: 5_000_000,
  remaining_quota: 232_500_000,
}

function QuotaCellProbe(props: {
  view?: ChannelBudgetView
  loading?: boolean
  aggregate?: boolean
  hidden?: boolean
  table?: boolean
}) {
  const { setSensitiveVisible } = useChannels()
  useEffect(() => {
    setSensitiveVisible(!props.hidden)
  }, [props.hidden, setSensitiveVisible])
  return (
    <ChannelBudgetContext.Provider
      value={{
        byChannel: props.view ? { 7: props.view } : {},
        loading: props.loading ?? false,
      }}
    >
      {props.table ? (
        <ChannelBudgetCell channelId={7} />
      ) : (
        <ChannelQuotaCell
          channelId={7}
          usedQuota={62_500_000}
          aggregate={props.aggregate}
          fallback={<button type='button'>Upstream balance details</button>}
        />
      )}
    </ChannelBudgetContext.Provider>
  )
}

function renderQuotaCell(props: Parameters<typeof QuotaCellProbe>[0]) {
  return render(
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <QuotaCellProbe {...props} />
      </ChannelsProvider>
    </QueryClientProvider>
  )
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
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
  useSystemConfigStore.getState().setConfig({ currency: originalCurrency })
  vi.restoreAllMocks()
})

test.each([
  { state: 'enabled', view: displayBudget, expected: '$125 / $500' },
  {
    state: 'disabled with a retained limit',
    view: { ...displayBudget, enabled: false },
    expected: '$125 / $0',
  },
  {
    state: 'unconfigured',
    view: { ...budget, enabled: false, limit_quota: 0 },
    expected: '$125 / $0',
  },
  {
    state: 'ledger disabled',
    view: { ...displayBudget, ledger_enabled: false },
    expected: '$125 / $0',
  },
])(
  'a $state card budget keeps billed usage and shows its configured total',
  ({ view, expected }) => {
    renderQuotaCell({ view })
    expect(screen.getByText(expected)).toBeVisible()
  }
)

test.each([
  {
    state: 'loading',
    view: displayBudget,
    loading: true,
    expected: '$125 / Loading...',
    detailStatus: 'Loading...',
  },
  {
    state: 'unavailable',
    view: undefined,
    loading: false,
    expected: '$125 / Unavailable',
    detailStatus: 'Unavailable',
  },
  {
    state: 'ledger-disabled',
    view: { ...displayBudget, ledger_enabled: false },
    loading: false,
    expected: '$125 / $0',
    detailStatus: 'Not enabled',
  },
])(
  'a $state card budget keeps billed usage with honest details and upstream access',
  async ({ view, loading, expected, detailStatus }) => {
    const user = userEvent.setup()
    renderQuotaCell({ view, loading })
    expect(screen.getByText(expected)).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Upstream balance details' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Local Budget' }))
    const details = within(
      await screen.findByRole('dialog', { name: 'Local Budget' })
    )
    expect(details.getAllByText(detailStatus).length).toBeGreaterThan(0)
    expect(details.queryByText('$500')).not.toBeInTheDocument()
    expect(
      details.getByRole('button', { name: 'Upstream balance details' })
    ).toBeVisible()
  }
)

test.each([
  { state: 'privacy-masked', hidden: true, aggregate: false, expected: '••••' },
  { state: 'aggregate', hidden: false, aggregate: true, expected: '-' },
])(
  'a $state card hides individual quota and upstream balance details',
  ({ hidden, aggregate, expected }) => {
    renderQuotaCell({ view: displayBudget, hidden, aggregate })
    expect(screen.getByText(expected)).toBeVisible()
    expect(screen.queryByText('$125 / $500')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  }
)

test('the billed card quota retains local accounting details and separate upstream balance access', async () => {
  const user = userEvent.setup()
  renderQuotaCell({ view: displayBudget })
  expect(screen.getByText('$125 / $500')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Upstream balance details' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Local Budget' }))
  const details = within(
    await screen.findByRole('dialog', { name: 'Local Budget' })
  )
  expect(
    details.getByRole('button', { name: 'Upstream balance details' })
  ).toBeVisible()
  expect(details.getByText('Limit')).toBeVisible()
  expect(details.getByText('$500')).toBeVisible()
  expect(details.getByText('Used this period')).toBeVisible()
  expect(details.getByText('$25')).toBeVisible()
  expect(details.getByText('Reserved')).toBeVisible()
  expect(details.getByText('$10')).toBeVisible()
  expect(details.getByText('Remaining')).toBeVisible()
  expect(details.getByText('$465')).toBeVisible()
  expect(details.getByText('Lifetime used')).toBeVisible()
  expect(details.getByText('$50')).toBeVisible()
})

test('the table local budget still shows remaining capacity instead of billed usage', () => {
  renderQuotaCell({ view: displayBudget, table: true })
  expect(
    screen.getByRole('button', { name: 'Local Budget' })
  ).toHaveTextContent('$465')
  expect(screen.queryByText('$125 / $500')).not.toBeInTheDocument()
})

test('both billed card values use the selected quota display currency', () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7,
    },
  })
  renderQuotaCell({ view: displayBudget })
  expect(screen.getByText('¥875 / ¥3,500')).toBeVisible()
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
