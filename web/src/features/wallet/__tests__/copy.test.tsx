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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { BillingHistoryDialog } from '@/features/wallet/components/dialogs/billing-history-dialog'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { formatQuota } from '@/lib/format'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { RechargeFormCard } from '../components/recharge-form-card'
import { Wallet } from '../index'

const i18n = createInstance()
const clients: QueryClient[] = []
const walletUser = {
  id: 1,
  username: 'alice',
  role: 1,
  quota: 1000000,
  used_quota: 0,
  request_count: 0,
  aff_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
}

afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients.length = 0
  useAuthStore.getState().auth.setUser(null)
  vi.restoreAllMocks()
  localStorage.clear()
})

function renderCheckinWallet(enabled: boolean, rejected = false) {
  useAuthStore.getState().auth.setUser(walletUser)
  const status = { checkin_enabled: enabled, turnstile_check: false }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  client.setQueryData(STATUS_QUERY_KEY, status)
  let checkedToday = false
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/status') {
      return { data: { success: true, data: status } }
    }
    if (url === '/api/user/self') {
      return {
        data: {
          success: true,
          data: { ...walletUser, quota: checkedToday ? 1250000 : 1000000 },
        },
      }
    }
    if (url.startsWith('/api/user/checkin?month=')) {
      return {
        data: {
          success: true,
          data: {
            enabled: true,
            stats: {
              checked_in_today: checkedToday,
              total_checkins: checkedToday ? 1 : 0,
              total_quota: checkedToday ? 250000 : 0,
              checkin_count: checkedToday ? 1 : 0,
              records: [],
            },
          },
        },
      }
    }
    if (url === '/api/user/topup/info') {
      return {
        data: {
          success: true,
          data: {
            enable_online_topup: false,
            enable_stripe_topup: false,
            pay_methods: [],
            min_topup: 1,
            stripe_min_topup: 1,
            amount_options: [1],
            discount: {},
          },
        },
      }
    }
    if (url === '/api/user/aff') {
      return { data: { success: true, data: 'wallet-referral' } }
    }
    if (url === '/api/subscription/plans') {
      return { data: { success: true, data: [] } }
    }
    if (url === '/api/subscription/self') {
      return {
        data: {
          success: true,
          data: { subscriptions: [], all_subscriptions: [] },
        },
      }
    }
    if (url.startsWith('/api/user/topup/self?')) {
      return { data: { success: true, data: { items: [], total: 0 } } }
    }
    throw new Error(`Unexpected Wallet GET ${url}`)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/user/amount') {
      return { data: { success: true, data: '1' } }
    }
    if (url === '/api/user/checkin') {
      if (rejected) {
        return { data: { success: false, message: 'Check-in failed' } }
      }
      checkedToday = true
      return { data: { success: true, data: { quota_awarded: 250000 } } }
    }
    throw new Error(`Unexpected Wallet POST ${url}`)
  })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <Wallet />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return { get, post }
}

beforeEach(async () => {
  localStorage.clear()
  useAuthStore.getState().auth.setUser(null)
  await i18n.use(initReactI18next).init({
    lng: 'zh',
    fallbackLng: false,
    nsSeparator: false,
    resources: { en, zh },
    interpolation: { escapeValue: false },
  })
})

it('translates billing history statuses and updates them when the language changes', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 3,
        items: ['success', 'pending', 'expired'].map((status, id) => ({
          id,
          status,
          amount: 10,
          money: 10,
          trade_no: `order-${id}`,
          payment_method: 'stripe',
          create_time: 1,
        })),
      },
    },
  })
  render(
    <I18nextProvider i18n={i18n}>
      <BillingHistoryDialog open onOpenChange={vi.fn()} />
    </I18nextProvider>
  )
  await screen.findByText('order-0')
  for (const label of ['成功', '待确认', '已过期']) {
    expect(screen.getByText(label)).toBeVisible()
  }
  await act(() => i18n.changeLanguage('en'))
  for (const label of ['Success', 'Pending', 'Expired']) {
    expect(screen.getByText(label)).toBeVisible()
  }
})

it.each([
  { priceRatio: 7.3, paid: '584', saved: '146', full: '146' },
  { priceRatio: 0.000123, paid: '0.0098', saved: '0.0025', full: '0.0025' },
])(
  'translates recharge presets and minimum amount without changing precision at price ratio $priceRatio',
  async ({ priceRatio, paid, saved, full }) => {
    render(
      <I18nextProvider i18n={i18n}>
        <RechargeFormCard
          topupInfo={{
            enable_online_topup: true,
            enable_stripe_topup: false,
            pay_methods: [],
            min_topup: 1,
            stripe_min_topup: 1,
            amount_options: [100, 20],
            discount: {},
          }}
          presetAmounts={[{ value: 100, discount: 0.8 }, { value: 20 }]}
          selectedPreset={null}
          topupAmount={1}
          paymentAmount={priceRatio}
          priceRatio={priceRatio}
          calculating={false}
          paymentLoading={null}
          redemptionCode=''
          redeeming={false}
          onSelectPreset={vi.fn()}
          onTopupAmountChange={vi.fn()}
          onPaymentMethodSelect={vi.fn()}
          onRedemptionCodeChange={vi.fn()}
          onRedeem={vi.fn()}
        />
      </I18nextProvider>
    )
    expect(screen.getByRole('button', { name: /^100 / })).toHaveTextContent(
      `优惠 20%支付 ${paid} • 节省 ${saved}`
    )
    expect(screen.getByRole('button', { name: /^20 / })).toHaveTextContent(
      `支付 ${full}`
    )
    expect(screen.getByPlaceholderText('最低 1')).toBeVisible()
    await act(() => i18n.changeLanguage('en'))
    expect(screen.getByRole('button', { name: /^100 / })).toHaveTextContent(
      `20% OFFPay ${paid} • Save ${saved}`
    )
    expect(screen.getByRole('button', { name: /^20 / })).toHaveTextContent(
      `Pay ${full}`
    )
    expect(screen.getByPlaceholderText('Minimum 1')).toBeVisible()
  }
)

it('shows the existing check-in card in Wallet and refreshes authoritative balance after a successful reward', async () => {
  await i18n.changeLanguage('en')
  const { get, post } = renderCheckinWallet(true)
  const user = userEvent.setup()
  expect(await screen.findByText(formatQuota(1000000))).toBeVisible()
  expect(await screen.findByText('Daily Check-in')).toBeVisible()
  const checkin = await screen.findByRole('button', { name: 'Check in now' })
  await waitFor(() => expect(checkin).toBeEnabled())

  await user.click(checkin)

  expect(await screen.findByText(formatQuota(1250000))).toBeVisible()
  expect(
    await screen.findByRole('button', { name: 'Checked in' })
  ).toBeDisabled()
  expect(post).toHaveBeenCalledWith('/api/user/checkin')
  expect(
    get.mock.calls.filter(([url]) => url === '/api/user/self')
  ).toHaveLength(2)
  expect(
    post.mock.calls.filter(([url]) => url === '/api/user/checkin')
  ).toHaveLength(1)
})

it('keeps Wallet check-in hidden without requesting check-in data when the feature is disabled', async () => {
  await i18n.changeLanguage('en')
  const { get, post } = renderCheckinWallet(false)
  expect(await screen.findByText(formatQuota(1000000))).toBeVisible()

  expect(screen.queryByText('Daily Check-in')).not.toBeInTheDocument()
  expect(
    get.mock.calls.some(([url]) => url.startsWith('/api/user/checkin'))
  ).toBe(false)
  expect(post.mock.calls.some(([url]) => url === '/api/user/checkin')).toBe(
    false
  )
})

it('keeps the Wallet balance unchanged and permits retry when check-in is rejected', async () => {
  await i18n.changeLanguage('en')
  const { get, post } = renderCheckinWallet(true, true)
  const user = userEvent.setup()
  expect(await screen.findByText(formatQuota(1000000))).toBeVisible()
  const checkin = await screen.findByRole('button', { name: 'Check in now' })
  await waitFor(() => expect(checkin).toBeEnabled())

  await user.click(checkin)

  await waitFor(() => expect(post).toHaveBeenCalledWith('/api/user/checkin'))
  await waitFor(() => expect(checkin).toBeEnabled())
  expect(screen.getByText(formatQuota(1000000))).toBeVisible()
  expect(screen.queryByText(formatQuota(1250000))).not.toBeInTheDocument()
  expect(
    get.mock.calls.filter(([url]) => url === '/api/user/self')
  ).toHaveLength(1)
})
