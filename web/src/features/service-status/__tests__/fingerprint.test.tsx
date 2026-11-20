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
import i18next from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'

import { ChannelFingerprint } from '../fingerprint'
import type { FingerprintJob } from '../fingerprint-api'

const searchChannels = vi.fn()
const getChannelFingerprint = vi.fn()
const cancelChannelFingerprint = vi.fn()

vi.mock('@/features/channels/api', () => ({
  searchChannels: (...args: unknown[]) => searchChannels(...args),
}))

vi.mock('../fingerprint-api', () => ({
  startChannelFingerprint: vi.fn(),
  getChannelFingerprint: (...args: unknown[]) => getChannelFingerprint(...args),
  cancelChannelFingerprint: (...args: unknown[]) =>
    cancelChannelFingerprint(...args),
}))

let client: QueryClient

afterEach(() => {
  cleanup()
  client?.clear()
  vi.clearAllMocks()
  vi.useRealTimers()
})

function renderPage() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18next}>
        <ChannelFingerprint />
      </I18nextProvider>
    </QueryClientProvider>
  )
}

const channels = {
  success: true,
  data: { items: [{ id: 7, name: 'channel-seven', models: 'gpt-4.1' }], total: 1, page: 1, page_size: 200 },
}

function runningJob(overrides: Partial<FingerprintJob> = {}): FingerprintJob {
  return {
    id: 'job-1',
    channel_id: 7,
    channel_name: 'channel-seven',
    model: 'gpt-4.1',
    endpoint: 'openai',
    state: 'running',
    probes: [
      {
        id: 'p1',
        state: 'complete',
        expected_count: 100,
        parsed_count: 100,
        prompt_tokens: 40,
        output_tokens: 60,
        max_tokens: 4488,
      },
      {
        id: 'p2',
        state: 'queued',
        expected_count: 100,
        parsed_count: 0,
        prompt_tokens: 0,
        output_tokens: 0,
        max_tokens: 0,
      },
    ],
    request_budget: 2,
    requests_used: 1,
    token_budget: 5000,
    tokens_used: 100,
    detector_version: '2026-11-19',
    bank_sha256: 'bank-digest',
    detector_sha256: 'detector-digest',
    attribution:
      'Fingerprint scoring uses the MIT-licensed lm-detector reference bank and detector (Copyright (c) 2026 xqy2006, github.com/Ikaleio/lm-detector).',
    limitation:
      'A match is a ranking relative to the reference bank. It does not prove model identity, quality, or authenticity, and a model outside the bank still receives a closest candidate.',
    started_at: 1795000000000,
    ...overrides,
  }
}

it('states that a match is not proof of identity', async () => {
  searchChannels.mockResolvedValue(channels)
  renderPage()

  expect(
    await screen.findByText(
      /does not prove model identity, quality, or authenticity/
    )
  ).toBeTruthy()
  expect(screen.getByText(/paid requests to the selected channel/)).toBeTruthy()
})

it('shows a failed poll with a retry instead of freezing progress', async () => {
  searchChannels.mockResolvedValue(channels)
  getChannelFingerprint.mockRejectedValue(new Error('check expired'))
  renderPage()

  // Driving a run requires a started job; the page polls only while running.
  const user = userEvent.setup()
  await screen.findByRole('combobox', { name: /channel/i })
  await user.click(screen.getByRole('combobox', { name: /channel/i }))
  await user.click(await screen.findByRole('option', { name: /#7 channel-seven/ }))
  await user.type(screen.getByLabelText(/model/i), 'gpt-4.1')
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob())
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(
    await screen.findByText(/a model outside the bank still receives a closest candidate/)
  ).toBeTruthy()
  await waitFor(() => expect(getChannelFingerprint).toHaveBeenCalled(), {
    timeout: 4000,
  })
  expect(await screen.findByText(/check expired/)).toBeTruthy()
  expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
  // A stopped run must not keep the form disabled: otherwise the operator could
  // not pick another channel or model after the run expired elsewhere.
  expect((screen.getByLabelText(/model/i) as HTMLInputElement).disabled).toBe(
    false
  )
  expect((screen.getByRole('button', { name: /start check/i }) as HTMLButtonElement).disabled).toBe(
    false
  )
}, 10000)

it('polls a new run after the previous one stopped', async () => {
  searchChannels.mockResolvedValue(channels)
  getChannelFingerprint.mockReset()
  getChannelFingerprint.mockRejectedValueOnce(new Error('check expired'))
  renderPage()

  const user = userEvent.setup()
  await screen.findByRole('combobox', { name: /channel/i })
  await user.click(screen.getByRole('combobox', { name: /channel/i }))
  await user.click(await screen.findByRole('option', { name: /#7 channel-seven/ }))
  await user.type(screen.getByLabelText(/model/i), 'gpt-4.1')
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(runningJob())
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(await screen.findByText(/check expired/)).toBeTruthy()

  const second = runningJob({
    id: 'job-2',
    detector_sha256: 'detector-digest-2',
  })
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(second)
  getChannelFingerprint.mockResolvedValue({
    ...second,
    state: 'completed',
  })
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('detector-digest-2')).toBeTruthy()
  await waitFor(() =>
    expect(getChannelFingerprint).toHaveBeenCalledWith(
      7,
      'job-2',
      expect.anything()
    ), { timeout: 4000 }
  )
}, 15000)

it('keeps a new run when cancelling the previous run completes late', async () => {
  searchChannels.mockResolvedValue(channels)
  getChannelFingerprint.mockReset()
  getChannelFingerprint.mockRejectedValueOnce(new Error('status unavailable'))
  let completeCancel!: (job: FingerprintJob) => void
  cancelChannelFingerprint.mockReturnValueOnce(
    new Promise<FingerprintJob>((resolve) => {
      completeCancel = resolve
    })
  )
  renderPage()

  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: /channel/i }))
  await user.click(await screen.findByRole('option', { name: /#7 channel-seven/ }))
  await user.type(screen.getByLabelText(/model/i), 'gpt-4.1')
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(runningJob())
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByRole('button', { name: /retry/i })
  await user.click(screen.getByRole('button', { name: /^cancel$/i }))
  await waitFor(() =>
    expect(cancelChannelFingerprint).toHaveBeenCalledWith(7, 'job-1')
  )

  const second = runningJob({
    id: 'job-2',
    state: 'completed',
    detector_sha256: 'detector-digest-2',
  })
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(second)
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('detector-digest-2')

  await act(async () => {
    completeCancel(runningJob({ state: 'cancelled' }))
  })
  await waitFor(() =>
    expect(screen.queryByRole('button', { name: /^cancel$/i })).toBeNull()
  )
  expect(screen.getByText('detector-digest-2')).toBeTruthy()
  expect(screen.queryByText('detector-digest')).toBeNull()
}, 15000)

it('shows the budget that is spent on input and output together', async () => {
  searchChannels.mockResolvedValue(channels)
  renderPage()

  expect(await screen.findByLabelText(/estimated token budget/i)).toBeTruthy()
  expect(screen.queryByLabelText(/output token budget/i)).toBeNull()
})

it('reports both verified checksums next to the result', async () => {
  searchChannels.mockResolvedValue(channels)
  renderPage()

  const user = userEvent.setup()
  await screen.findByRole('combobox', { name: /channel/i })
  await user.click(screen.getByRole('combobox', { name: /channel/i }))
  await user.click(await screen.findByRole('option', { name: /#7 channel-seven/ }))
  await user.type(screen.getByLabelText(/model/i), 'gpt-4.1')
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({ state: 'completed' })
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('bank-digest')).toBeTruthy()
  expect(screen.getByText('detector-digest')).toBeTruthy()
}, 10000)
