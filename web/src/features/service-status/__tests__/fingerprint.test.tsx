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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next, { type i18n as I18nInstance } from 'i18next'
import { Profiler } from 'react'
import { I18nextProvider } from 'react-i18next'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { ChannelFingerprint } from '../fingerprint'
import type {
  FingerprintChannel,
  FingerprintContext,
  FingerprintJob,
  FingerprintPreview,
} from '../fingerprint-api'

const {
  getChannelOptions,
  getFingerprintChannel,
  getFingerprintKeys,
  previewChannelFingerprint,
  startChannelFingerprint,
  retryChannelFingerprintProbe,
  getChannelFingerprint,
  cancelChannelFingerprint,
} = vi.hoisted(() => ({
  getChannelOptions: vi.fn(),
  getFingerprintChannel: vi.fn(),
  getFingerprintKeys: vi.fn(),
  previewChannelFingerprint: vi.fn(),
  startChannelFingerprint: vi.fn(),
  retryChannelFingerprintProbe: vi.fn(),
  getChannelFingerprint: vi.fn(),
  cancelChannelFingerprint: vi.fn(),
}))
const originalAuth = useAuthStore.getState().auth
const adminUser: AuthUser = {
  id: 1,
  username: 'admin-one',
  role: ROLE.ADMIN,
  permissions: {
    admin_permissions: { channel: { read: true, operate: true } },
  },
}

vi.mock('@/features/channels/api', () => ({ getChannelOptions }))

vi.mock('../fingerprint-api', () => ({
  getFingerprintChannel,
  getFingerprintKeys,
  previewChannelFingerprint,
  startChannelFingerprint,
  retryChannelFingerprintProbe,
  getChannelFingerprint,
  cancelChannelFingerprint,
}))

let client: QueryClient

const fingerprintChannel: FingerprintChannel = {
  id: 7,
  type: 1,
  is_multi_key: false,
  models: ['gpt-4.1'],
}

const preview: FingerprintPreview = {
  preview_id: 'preview-1',
  expires_at: 0,
  channel_id: 7,
  model: 'gpt-4.1',
  endpoint_type: '',
  count: 3,
  probes: [
    { id: 'preview-probe-1', prompt: 'actual prompt one', expected_count: 293 },
    { id: 'preview-probe-2', prompt: 'actual prompt two', expected_count: 294 },
    { id: 'preview-probe-3', prompt: 'actual prompt three', expected_count: 295 },
  ],
}

beforeEach(() => {
  useAuthStore.setState({ auth: { ...originalAuth, user: adminUser } })
  vi.resetAllMocks()
  getFingerprintChannel.mockImplementation(async (channelId: number) => {
    if (channelId === 8) {
      return { ...fingerprintChannel, id: 8, models: ['claude-sonnet-4'] }
    }
    if (channelId === 9) {
      return { ...fingerprintChannel, id: 9, models: [] }
    }
    return fingerprintChannel
  })
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 1,
    keys: [],
  })
  previewChannelFingerprint.mockImplementation(
    async (channelId: number, context: FingerprintContext) => ({
      ...preview,
      ...context,
      channel_id: channelId,
      expires_at: Date.now() + 60_000,
      probes: preview.probes.slice(0, context.count),
    })
  )
})

afterEach(() => {
  cleanup()
  client?.clear()
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
  vi.useRealTimers()
})

function renderPage(onCommit?: () => void, translation: I18nInstance = i18next) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={translation}>
        <Profiler id='fingerprint' onRender={() => onCommit?.()}>
          <ChannelFingerprint />
        </Profiler>
      </I18nextProvider>
    </QueryClientProvider>
  )
}

const channels = {
  success: true,
  data: {
    items: [{ id: 7, name: 'channel-seven', models: ['gpt-4.1'] }],
    page: 1,
    page_size: 30,
    total: 1,
    has_more: false,
  },
}

function runningJob(overrides: Partial<FingerprintJob> = {}): FingerprintJob {
  return {
    id: 'job-1',
    revision: 1,
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

function retryableJob(overrides: Partial<FingerprintJob> = {}): FingerprintJob {
  return runningJob({
    state: 'inconclusive',
    token_budget: 24000,
    tokens_used: 600,
    request_budget: 3,
    requests_used: 3,
    finished_at: 1795000001000,
    probes: preview.probes.map((sample, index) => ({
      ...sample,
      state: index === 1 ? 'failed' : 'complete',
      parsed_count: index === 1 ? 0 : 3,
      prompt_tokens: 100,
      output_tokens: 100,
      max_tokens: 4096,
      attempt: 1,
      retry_allowed: index === 1,
      output: index === 1 ? undefined : `preserved answer ${index + 1}`,
      error: index === 1 ? 'fingerprint request failed (HTTP 503)' : undefined,
    })),
    ...overrides,
  })
}

it('shows elapsed request time and timeout while queued samples wait without claiming streamed output', async () => {
  getChannelOptions.mockResolvedValue(channels)
  const timestamp = Date.now()
  const initial = retryableJob()
  vi.mocked(startChannelFingerprint).mockResolvedValue({
    ...initial,
    state: 'running',
    finished_at: undefined,
    probes: [
      initial.probes[0],
      {
        ...initial.probes[1],
        state: 'running',
        started_at: timestamp - 5000,
        timeout_seconds: 120,
        error: undefined,
        retry_allowed: false,
      },
      { ...initial.probes[2], state: 'queued', output: undefined },
    ],
  })
  getChannelFingerprint.mockReturnValue(new Promise(() => {}))
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  vi.useFakeTimers()
  vi.setSystemTime(timestamp)
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /start check/i }))
    await vi.advanceTimersByTimeAsync(1)
  })
  const sample = screen.getByRole('region', { name: /^challenge 2$/i })
  expect(within(sample).getByRole('status')).toHaveTextContent(
    'Waiting for reply: 5s elapsed; request timeout 120s.'
  )
  expect(
    within(screen.getByRole('region', { name: /^challenge 3$/i })).getByText(
      'Queued'
    )
  ).toBeVisible()
  expect(screen.getByText(/Polling · Replies are not streamed\./)).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /retry sample/i })
  ).not.toBeInTheDocument()
  expect(getChannelFingerprint).toHaveBeenCalledTimes(1)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  expect(within(sample).getByRole('status')).toHaveTextContent(
    'Waiting for reply: 15s elapsed; request timeout 120s.'
  )
  expect(
    getChannelFingerprint,
    'elapsed updates never overlap the pending status read'
  ).toHaveBeenCalledTimes(1)
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
  expect(retryChannelFingerprintProbe).not.toHaveBeenCalled()
})

it('retries only the failed sample with its saved preview after response loss and preserves successful outputs', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    auto_models: fingerprintChannel.models,
  })
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 9,
    keys: [
      {
        id: '9007199254740993',
        rev: 4,
        index: 0,
        status: 1,
        key_preview: 'sk-…retry',
        scope_mode: 'all',
        models: [],
      },
    ],
  })
  const initial = retryableJob({ endpoint: 'openai-response' })
  vi.mocked(startChannelFingerprint).mockResolvedValue(initial)
  const retried = retryableJob({
    endpoint: 'openai-response',
    revision: 2,
    state: 'running',
    finished_at: undefined,
    probes: initial.probes.map((probe, index) =>
      index === 1
        ? {
            ...probe,
            state: 'running',
            attempt: 2,
            retry_allowed: false,
            error: undefined,
          }
        : probe
    ),
  })
  const finished = {
    ...retried,
    state: 'inconclusive' as const,
    finished_at: Date.now(),
    probes: retried.probes.map((probe, index) =>
      index === 1
        ? {
            ...probe,
            state: 'complete' as const,
            output: 'retried answer',
            parsed_count: 3,
          }
        : probe
    ),
  }
  retryChannelFingerprintProbe
    .mockRejectedValueOnce(new Error('retry response unavailable'))
    .mockResolvedValueOnce(retried)
  let finishPoll!: (job: FingerprintJob) => void
  getChannelFingerprint.mockReturnValueOnce(
    new Promise<FingerprintJob>((resolve) => {
      finishPoll = resolve
    })
  )
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const keyControl = await screen.findByRole('combobox', { name: /^tested key$/i })
  await waitFor(() => expect(keyControl).toBeEnabled())
  await user.click(keyControl)
  await user.click(await screen.findByRole('option', { name: /1 · sk-…retry/ }))
  expect(screen.queryByLabelText(/^endpoint$/i)).not.toBeInTheDocument()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('preserved answer 1')
  const failed = screen.getByRole('region', { name: /^challenge 2$/i })
  const retry = await within(failed).findByRole('button', { name: /retry sample/i })
  const previewCalls = previewChannelFingerprint.mock.calls.length
  expect(screen.getAllByRole('button', { name: /retry sample/i })).toHaveLength(1)
  expect(within(failed).getByText(/one additional paid request/)).toBeVisible()
  await user.click(retry)
  expect(await screen.findByText('retry response unavailable')).toBeVisible()
  expect(screen.getByText('preserved answer 1')).toBeVisible()
  expect(screen.getByText('preserved answer 3')).toBeVisible()
  await user.click(within(failed).getByRole('button', { name: /retry sample/i }))
  await waitFor(() =>
    expect(retryChannelFingerprintProbe).toHaveBeenCalledTimes(2)
  )
  expect(retryChannelFingerprintProbe.mock.calls[1].slice(0, 3)).toEqual(
    retryChannelFingerprintProbe.mock.calls[0].slice(0, 3)
  )
  expect(retryChannelFingerprintProbe).toHaveBeenLastCalledWith(
    7,
    initial.id,
    {
      preview_id: 'preview-1',
      model: 'gpt-4.1',
      count: 3,
      key_entry_id: '9007199254740993',
      expected_rev: 4,
      key_index: 0,
      probe_id: preview.probes[1].id,
      expected_attempt: 1,
    },
    expect.any(AbortSignal)
  )
  expect(screen.getByText('preserved answer 1')).toBeVisible()
  expect(screen.getByText('preserved answer 3')).toBeVisible()
  expect(within(failed).getByText('Running')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /retry sample/i })
  ).not.toBeInTheDocument()
  await waitFor(() => expect(finishPoll).toBeTypeOf('function'))
  await act(async () => {
    finishPoll(finished)
  })
  expect(await screen.findByText('retried answer')).toBeVisible()
  expect(screen.getByText('preserved answer 1')).toBeVisible()
  expect(screen.getByText('preserved answer 3')).toBeVisible()
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
  expect(previewChannelFingerprint).toHaveBeenCalledTimes(previewCalls)
  expect(
    screen.queryByRole('button', { name: /retry sample/i })
  ).not.toBeInTheDocument()
})

it('ignores an older job revision polled after retry and cancels only the visible retry revision', async () => {
  getChannelOptions.mockResolvedValue(channels)
  const initial = retryableJob()
  const retried = retryableJob({
    revision: 2,
    state: 'running',
    finished_at: undefined,
    probes: initial.probes.map((probe, index) =>
      index === 1
        ? {
            ...probe,
            state: 'running',
            attempt: 2,
            retry_allowed: false,
            error: undefined,
          }
        : probe
    ),
  })
  vi.mocked(startChannelFingerprint).mockResolvedValue(initial)
  retryChannelFingerprintProbe.mockResolvedValue(retried)
  let resolveStale!: (job: FingerprintJob) => void
  getChannelFingerprint
    .mockReturnValueOnce(
      new Promise<FingerprintJob>((resolve) => {
        resolveStale = resolve
      })
    )
    .mockReturnValue(new Promise(() => {}))
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('preserved answer 1')
  await user.click(await screen.findByRole('button', { name: /retry sample/i }))
  await waitFor(() => expect(resolveStale).toBeTypeOf('function'))
  await act(async () => {
    resolveStale({ ...initial, detector_sha256: 'stale-initial-revision' })
  })
  const sample = screen.getByRole('region', { name: /^challenge 2$/i })
  expect(within(sample).getByText('Running')).toBeVisible()
  expect(screen.queryByText('stale-initial-revision')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^cancel$/i })).toBeEnabled()
  const cancelled = {
    ...retried,
    state: 'cancelled' as const,
    probes: retried.probes.map((probe, index) =>
      index === 1 ? { ...probe, state: 'cancelled' as const } : probe
    ),
  }
  cancelChannelFingerprint.mockResolvedValueOnce({
    ...initial,
    state: 'cancelled',
  })
  await user.click(screen.getByRole('button', { name: /^cancel$/i }))
  await waitFor(() => expect(cancelChannelFingerprint).toHaveBeenCalledTimes(1))
  expect(within(sample).getByText('Running')).toBeVisible()
  expect(cancelChannelFingerprint).toHaveBeenCalledWith(
    7,
    initial.id,
    expect.any(AbortSignal),
    2
  )
  cancelChannelFingerprint.mockResolvedValueOnce(cancelled)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /^cancel$/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /^cancel$/i }))
  expect(await within(sample).findByText('Cancelled')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /retry sample/i })
  ).not.toBeInTheDocument()
  expect(screen.getByText('preserved answer 1')).toBeVisible()
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
  expect(retryChannelFingerprintProbe).toHaveBeenCalledTimes(1)
})

it.each(['cancelled', 'exhausted', 'already retried'] as const)(
  'does not offer paid sample retry when the server reports %s',
  async (condition) => {
    getChannelOptions.mockResolvedValue(channels)
    const initial = retryableJob()
    vi.mocked(startChannelFingerprint).mockResolvedValue({
      ...initial,
      state: condition === 'cancelled' ? 'cancelled' : 'failed',
      tokens_used: condition === 'exhausted' ? 24000 : initial.tokens_used,
      probes: initial.probes.map((probe) => ({
        ...probe,
        retry_allowed: false,
        attempt: condition === 'already retried' ? 2 : 1,
      })),
    })
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    expect(await screen.findByText('preserved answer 1')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /retry sample/i })
    ).not.toBeInTheDocument()
    expect(retryChannelFingerprintProbe).not.toHaveBeenCalled()
  }
)

it('states that a match is not proof of identity', async () => {
  getChannelOptions.mockResolvedValue(channels)
  renderPage()

  expect(
    await screen.findByText(
      /does not prove model identity, quality, or authenticity/
    )
  ).toBeTruthy()
  expect(screen.getByText(/paid requests to the selected channel/)).toBeTruthy()
})

it('submits three server-derived challenges with only channel and model controls', async () => {
  getChannelOptions.mockResolvedValue(channels)
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob({ state: 'completed' }))
  renderPage()
  const user = userEvent.setup()
  expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
  expect(screen.queryByLabelText(/^endpoint$/i)).not.toBeInTheDocument()
  expect(screen.queryByLabelText(/budget/i)).not.toBeInTheDocument()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      { preview_id: 'preview-1', model: 'gpt-4.1', count: 3 },
      expect.any(AbortSignal)
    )
  )
  expect(previewChannelFingerprint).toHaveBeenLastCalledWith(
    7,
    { model: 'gpt-4.1', count: 3 },
    expect.any(AbortSignal)
  )
  expect(getFingerprintChannel).toHaveBeenCalledWith(7, expect.any(AbortSignal))
  expect(getFingerprintKeys).not.toHaveBeenCalled()
})

it('shows a failed poll and replays only the same accepted preview instead of starting another paid job', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getChannelFingerprint.mockRejectedValue(new Error('check expired'))
  renderPage()

  // Driving a run requires a started job; the page polls only while running.
  const user = userEvent.setup()
  await screen.findByRole('combobox', { name: /channel/i })
  await user.click(screen.getByRole('combobox', { name: /channel/i }))
  await user.click(
    await screen.findByRole('option', { name: /#7 channel-seven/ })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    'gpt-4.1'
  )
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob())
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(
    await screen.findByText(
      /a model outside the bank still receives a closest candidate/
    )
  ).toBeTruthy()
  await waitFor(() => expect(getChannelFingerprint).toHaveBeenCalled(), {
    timeout: 4000,
  })
  expect(await screen.findByText(/check expired/)).toBeTruthy()
  const unavailableSample = screen.getByRole('region', { name: /^challenge 2$/i })
  expect(within(unavailableSample).getByText('Status unavailable')).toBeVisible()
  expect(within(unavailableSample).queryByText('Queued')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
  // A stopped run must not keep the form disabled: otherwise the operator could
  // not pick another channel or model after the run expired elsewhere.
  expect(screen.getByRole('combobox', { name: /^model$/i })).toBeEnabled()
  expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  const previewCalls = previewChannelFingerprint.mock.calls.length
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() => expect(startChannelFingerprint).toHaveBeenCalledTimes(2))
  expect(vi.mocked(startChannelFingerprint).mock.calls[1].slice(0, 2)).toEqual(
    vi.mocked(startChannelFingerprint).mock.calls[0].slice(0, 2)
  )
  expect(previewChannelFingerprint).toHaveBeenCalledTimes(previewCalls)
}, 10000)

it('polls a new run after the previous one stopped', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getChannelFingerprint.mockReset()
  getChannelFingerprint.mockRejectedValueOnce(new Error('check expired'))
  renderPage()

  const user = userEvent.setup()
  await screen.findByRole('combobox', { name: /channel/i })
  await user.click(screen.getByRole('combobox', { name: /channel/i }))
  await user.click(
    await screen.findByRole('option', { name: /#7 channel-seven/ })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    'gpt-4.1'
  )
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(runningJob())
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(await screen.findByText(/check expired/)).toBeTruthy()
  previewChannelFingerprint.mockResolvedValueOnce({
    ...preview,
    preview_id: 'preview-2',
    expires_at: Date.now() + 60_000,
  })
  await user.click(screen.getByRole('button', { name: /change prompts/i }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )

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
  expect(vi.mocked(startChannelFingerprint).mock.calls[1][1]).toHaveProperty(
    'preview_id',
    'preview-2'
  )
  await waitFor(
    () =>
      expect(getChannelFingerprint).toHaveBeenCalledWith(
        7,
        'job-2',
        expect.anything()
      ),
    { timeout: 4000 }
  )
}, 15000)

it.each(['cancelled', 'running'] as const)(
  'does not revive a cancelled job when a pre-cancel GET resolves after a %s acknowledgement',
  async (acknowledgedState) => {
    getChannelOptions.mockResolvedValue(channels)
    const running = runningJob()
    const cancelled = runningJob({
      state: 'cancelled',
      probes: running.probes.map((probe) =>
        probe.state === 'complete' ? probe : { ...probe, state: 'cancelled' }
      ),
      finished_at: running.started_at + 1000,
    })
    let completeOldPoll!: (job: FingerprintJob) => void
    getChannelFingerprint
      .mockReturnValueOnce(
        new Promise<FingerprintJob>((resolve) => {
          completeOldPoll = resolve
        })
      )
      .mockResolvedValueOnce(cancelled)
    vi.mocked(startChannelFingerprint).mockResolvedValue(running)
    cancelChannelFingerprint.mockResolvedValue(
      acknowledgedState === 'cancelled' ? cancelled : running
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    await waitFor(() => expect(getChannelFingerprint).toHaveBeenCalledTimes(1))
    const oldSignal = getChannelFingerprint.mock.calls[0][2] as AbortSignal
    expect(oldSignal.aborted).toBe(false)

    await user.click(screen.getByRole('button', { name: /^cancel$/i }))
    const sample = screen.getByRole('region', { name: /^challenge 2$/i })
    expect(await within(sample).findByText('Cancelled')).toBeVisible()
    expect(oldSignal.aborted).toBe(true)
    const expectedReads = acknowledgedState === 'cancelled' ? 1 : 2
    expect(getChannelFingerprint).toHaveBeenCalledTimes(expectedReads)
    expect(
      screen.queryByRole('button', { name: /^cancel$/i })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /change prompts/i })).toBeEnabled()

    // The old transport deliberately ignores abort and returns the same job.
    vi.useFakeTimers()
    await act(async () => {
      completeOldPoll(runningJob({ detector_sha256: 'stale-running-read' }))
      await vi.advanceTimersByTimeAsync(4500)
    })
    expect(within(sample).getByText('Cancelled')).toBeVisible()
    expect(screen.queryByText('stale-running-read')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /^cancel$/i })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /change prompts/i })).toBeEnabled()
    expect(getChannelFingerprint).toHaveBeenCalledTimes(expectedReads)
    expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
  }
)

it.each(['cancelled', 'running'] as const)(
  'clears a stopped poll failure when cancellation returns a current %s acknowledgement',
  async (acknowledgedState) => {
    getChannelOptions.mockResolvedValue(channels)
    const running = runningJob()
    const cancelled = runningJob({
      state: 'cancelled',
      probes: running.probes.map((probe) =>
        probe.state === 'complete' ? probe : { ...probe, state: 'cancelled' }
      ),
      finished_at: Date.now(),
    })
    vi.mocked(startChannelFingerprint).mockResolvedValue(running)
    getChannelFingerprint
      .mockRejectedValueOnce(new Error('fingerprint status read failed'))
      .mockResolvedValueOnce(cancelled)
    cancelChannelFingerprint.mockResolvedValue(
      acknowledgedState === 'cancelled' ? cancelled : running
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    expect(await screen.findByText('fingerprint status read failed')).toBeVisible()
    const sample = screen.getByRole('region', { name: /^challenge 2$/i })
    expect(within(sample).getByText('Status unavailable')).toBeVisible()
    await user.click(screen.getByRole('button', { name: /^cancel$/i }))
    expect(await within(sample).findByText('Cancelled')).toBeVisible()
    expect(
      screen.queryByText('fingerprint status read failed')
    ).not.toBeInTheDocument()
    expect(screen.queryByText('Status unavailable')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /^retry$/i })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
    expect(getChannelFingerprint).toHaveBeenCalledTimes(
      acknowledgedState === 'cancelled' ? 1 : 2
    )
    expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
    expect(retryChannelFingerprintProbe).not.toHaveBeenCalled()
  }
)

it('keeps a new run when cancelling the previous run completes late', async () => {
  getChannelOptions.mockResolvedValue(channels)
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
  await user.click(
    await screen.findByRole('option', { name: /#7 channel-seven/ })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    'gpt-4.1'
  )
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(runningJob())
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByRole('button', { name: /retry/i })
  await user.click(screen.getByRole('button', { name: /^cancel$/i }))
  await waitFor(() =>
    expect(cancelChannelFingerprint).toHaveBeenCalledWith(
      7,
      'job-1',
      expect.any(AbortSignal),
      1
    )
  )

  const cancelSignal = cancelChannelFingerprint.mock.calls[0][2] as AbortSignal
  expect(cancelSignal.aborted).toBe(false)
  const second = runningJob({
    id: 'job-2',
    state: 'completed',
    detector_sha256: 'detector-digest-2',
  })
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(second)
  previewChannelFingerprint.mockResolvedValueOnce({
    ...preview,
    preview_id: 'preview-2',
    expires_at: Date.now() + 60_000,
  })
  await user.click(screen.getByRole('button', { name: /change prompts/i }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('detector-digest-2')
  expect(vi.mocked(startChannelFingerprint).mock.calls[1][1]).toHaveProperty(
    'preview_id',
    'preview-2'
  )
  expect(cancelSignal.aborted).toBe(true)

  await act(async () => {
    completeCancel(runningJob({ state: 'cancelled' }))
  })
  await waitFor(() =>
    expect(screen.queryByRole('button', { name: /^cancel$/i })).toBeNull()
  )
  expect(screen.getByText('detector-digest-2')).toBeTruthy()
  expect(screen.queryByText('detector-digest')).toBeNull()
}, 15000)

it('shows three initial sample cards and bottom actions without a token-budget control', () => {
  getChannelOptions.mockResolvedValue(channels)
  renderPage()

  const samples = screen.getAllByRole('region', { name: /^challenge [123]$/i })
  expect(samples).toHaveLength(3)
  for (const sample of samples) {
    expect(within(sample).getByText('Not run')).toBeVisible()
    expect(within(sample).getByText('Prompt')).toBeVisible()
    expect(within(sample).getByText('Output')).toBeVisible()
    expect(within(sample).getByText('Parsed numbers: 0')).toBeVisible()
  }
  expect(screen.queryByLabelText(/token budget/i)).not.toBeInTheDocument()
  expect(
    screen.queryByText(/tokens used|estimated token/i)
  ).not.toBeInTheDocument()
  const change = screen.getByRole('button', { name: /change prompts/i })
  const start = screen.getByRole('button', { name: /start check/i })
  expect(
    samples[2].compareDocumentPosition(change) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(
    samples[2].compareDocumentPosition(start) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  expect(change).toBeDisabled()
  expect(start).toBeDisabled()
  expect(previewChannelFingerprint).not.toHaveBeenCalled()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
})

it('reports both verified checksums next to the result', async () => {
  getChannelOptions.mockResolvedValue(channels)
  renderPage()

  const user = userEvent.setup()
  await screen.findByRole('combobox', { name: /channel/i })
  await user.click(screen.getByRole('combobox', { name: /channel/i }))
  await user.click(
    await screen.findByRole('option', { name: /#7 channel-seven/ })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    'gpt-4.1'
  )
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({ state: 'completed' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('bank-digest')).toBeTruthy()
  expect(screen.getByText('detector-digest')).toBeTruthy()
}, 10000)

it('offers unique channel model aliases and replaces stale selections on channel changes', async () => {
  getChannelOptions.mockResolvedValue({
    ...channels,
    data: {
      ...channels.data,
      items: [
        {
          id: 7,
          name: 'channel-seven',
          // The server already splits and trims the model list; a repeated
          // alias must still collapse to one option.
          models: [
            'qwen3-max@stable',
            'gpt-4.1',
            'qwen3-max@stable',
            'gpt-4.1',
          ],
        },
        { id: 8, name: 'channel-eight', models: ['claude-sonnet-4'] },
        { id: 9, name: 'channel-empty', models: [] },
      ],
    },
  })
  getFingerprintChannel.mockImplementation(async (channelId: number) => {
    const models = {
      7: ['qwen3-max@stable', 'gpt-4.1', 'qwen3-max@stable', 'gpt-4.1'],
      8: ['claude-sonnet-4'],
      9: [],
    }
    return {
      ...fingerprintChannel,
      id: channelId,
      models: models[channelId as keyof typeof models],
    }
  })
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({ state: 'completed', model: 'qwen3-max@stable' })
  )
  const user = userEvent.setup()
  renderPage()

  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(screen.getByRole('combobox', { name: /^model$/i })).toBeDisabled()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await user.click(screen.getByRole('combobox', { name: /^model$/i }))
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual(['gpt-4.1', 'qwen3-max@stable'])
  await user.click(screen.getByRole('option', { name: 'qwen3-max@stable' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ model: 'qwen3-max@stable' }),
      expect.any(AbortSignal)
    )
  )
  await screen.findByText('detector-digest')

  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#8 channel-eight' })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    'claude-sonnet-4'
  )
  await user.click(screen.getByRole('combobox', { name: /^model$/i }))
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual(['claude-sonnet-4'])
  await user.click(screen.getByRole('option', { name: 'claude-sonnet-4' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )

  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#9 channel-empty' })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    /^Model$/
  )
  expect(screen.getByText('This channel declares no models')).toBeVisible()
  const startButton = screen.getByRole('button', { name: /start check/i })
  expect(startButton).toBeDisabled()
  await user.click(startButton)
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
})

it('appends bounded channel pages inside the selector and retains the selected ID and models', async () => {
  getChannelOptions.mockResolvedValue({
    ...channels,
    data: { ...channels.data, has_more: true },
  })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  const modelSelector = screen.getByRole('combobox', { name: /^model$/i })
  const startButton = screen.getByRole('button', { name: /start check/i })
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  let completeOptions!: (result: typeof channels) => void
  getChannelOptions.mockReturnValueOnce(
    new Promise<typeof channels>((resolve) => {
      completeOptions = resolve
    })
  )
  await user.click(await screen.findByRole('button', { name: /^load more$/i }))
  await waitFor(() =>
    expect(getChannelOptions).toHaveBeenLastCalledWith(
      expect.objectContaining({ p: 2, ids: '7', page_size: 30 }),
      expect.any(AbortSignal)
    )
  )
  expect(modelSelector).toHaveTextContent('gpt-4.1')
  expect(startButton).toBeEnabled()
  await act(async () => {
    completeOptions({
      ...channels,
      data: {
        ...channels.data,
        items: [{ id: 8, name: 'channel-eight', models: ['claude-sonnet-4'] }],
        page: 2,
      },
    })
  })
  expect(await screen.findByRole('option', { name: '#8 channel-eight' })).toBeVisible()
  expect(screen.getByRole('option', { name: '#7 channel-seven' })).toBeVisible()
  expect(screen.queryByRole('button', { name: /^load more$/i })).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  expect(screen.getByRole('combobox', { name: /^channel$/i })).toHaveValue('#7 channel-seven')
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent('gpt-4.1')
  expect(screen.queryByRole('button', { name: /previous|next/i })).not.toBeInTheDocument()
})

it('keeps the selected models during refetch and fails closed when channel hydration removes its ID', async () => {
  getChannelOptions.mockResolvedValue(channels)
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  let completeOptions!: (result: typeof channels) => void
  getChannelOptions.mockReturnValueOnce(
    new Promise<typeof channels>((resolve) => {
      completeOptions = resolve
    })
  )
  act(() => {
    void client.invalidateQueries({ queryKey: ['fingerprint-channels'] })
  })
  await waitFor(() => expect(completeOptions).toBeTypeOf('function'))
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent('gpt-4.1')
  await act(async () => {
    completeOptions({ ...channels, data: { ...channels.data, items: [] } })
  })
  expect(await screen.findByText('Unavailable')).toBeVisible()
  expect(screen.getByRole('combobox', { name: /^channel$/i })).toHaveValue('#7 Unavailable')
  expect(screen.getByRole('combobox', { name: /^model$/i })).toBeDisabled()
  const start = screen.getByRole('button', { name: /start check/i })
  expect(start).toBeDisabled()
  await user.click(start)
  expect(startChannelFingerprint).not.toHaveBeenCalled()
})

it.each([
  { label: 'signed out', user: null },
  {
    label: 'an ordinary user with an operate grant',
    user: { ...adminUser, role: ROLE.USER },
  },
  {
    label: 'an administrator without channel operate permission',
    user: {
      ...adminUser,
      permissions: {
        admin_permissions: { channel: { read: true, operate: false } },
      },
    },
  },
])('does not load or show channel controls for $label', ({ user }) => {
  useAuthStore.setState({ auth: { ...originalAuth, user } })
  renderPage()

  expect(
    screen.queryByRole('combobox', { name: /^channel$/i })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('combobox', { name: /^model$/i })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /start check/i })
  ).not.toBeInTheDocument()
  expect(getChannelOptions).not.toHaveBeenCalled()
  expect(getChannelFingerprint).not.toHaveBeenCalled()
})

it.each([
  { label: 'role demotion', nextUser: { ...adminUser, role: ROLE.USER } },
  {
    label: 'operate permission removal',
    nextUser: {
      ...adminUser,
      permissions: {
        admin_permissions: { channel: { read: true, operate: false } },
      },
    },
  },
])(
  'hides cached channel data and cancels polling immediately on $label',
  async ({ nextUser }) => {
    getChannelOptions.mockResolvedValue(channels)
    let completePoll!: (job: FingerprintJob) => void
    getChannelFingerprint.mockReturnValue(
      new Promise<FingerprintJob>((resolve) => {
        completePoll = resolve
      })
    )
    const { startChannelFingerprint } = await import('../fingerprint-api')
    vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob())
    const user = userEvent.setup()
    const visibleCommits: string[] = []
    renderPage(() => visibleCommits.push(document.body.textContent ?? ''))
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    vi.useFakeTimers()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /start check/i }))
      await vi.advanceTimersByTimeAsync(1)
    })
    expect(screen.getByText('detector-digest')).toBeVisible()
    expect(getChannelFingerprint).toHaveBeenCalledTimes(1)
    const pollSignal = getChannelFingerprint.mock.calls[0][2] as AbortSignal
    expect(pollSignal.aborted).toBe(false)
    const channelLoadsBeforeDemotion = getChannelOptions.mock.calls.length

    visibleCommits.length = 0
    act(() => {
      useAuthStore.getState().auth.setUser(nextUser)
    })
    expect(visibleCommits.length).toBeGreaterThan(0)
    for (const text of visibleCommits) {
      expect(text).not.toMatch(/channel-seven|gpt-4\.1|detector-digest/)
    }
    expect(
      screen.queryByText(/channel-seven|gpt-4\.1|detector-digest/)
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /start check/i })
    ).not.toBeInTheDocument()
    expect(pollSignal.aborted).toBe(true)
    await act(async () => {
      completePoll(
        runningJob({ state: 'completed', detector_sha256: 'late-detector' })
      )
      await vi.advanceTimersByTimeAsync(6000)
    })
    expect(
      screen.queryByText(/channel-seven|gpt-4\.1|late-detector/)
    ).not.toBeInTheDocument()
    expect(getChannelFingerprint).toHaveBeenCalledTimes(1)
    expect(getChannelOptions).toHaveBeenCalledTimes(channelLoadsBeforeDemotion)
  }
)

it('clears a completed run and model selection when another administrator signs in', async () => {
  getChannelOptions
    .mockResolvedValueOnce(channels)
    .mockResolvedValueOnce(channels)
  let completeSearch!: (result: typeof channels) => void
  getChannelOptions.mockReturnValueOnce(
    new Promise<typeof channels>((resolve) => {
      completeSearch = resolve
    })
  )
  getChannelOptions.mockResolvedValue(channels)
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({ state: 'completed' })
  )
  const user = userEvent.setup()
  const visibleCommits: string[] = []
  renderPage(() => visibleCommits.push(document.body.textContent ?? ''))
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('detector-digest')

  visibleCommits.length = 0
  act(() => {
    useAuthStore
      .getState()
      .auth.setUser({ ...adminUser, id: 2, username: 'admin-two' })
  })
  expect(visibleCommits.length).toBeGreaterThan(0)
  for (const text of visibleCommits) {
    expect(text).not.toMatch(/channel-seven|gpt-4\.1|detector-digest/)
  }
  expect(
    screen.queryByText(/channel-seven|gpt-4\.1|detector-digest/)
  ).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: /^model$/i })).toBeDisabled()
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(getChannelOptions).toHaveBeenCalledTimes(3)
  await act(async () => {
    completeSearch(channels)
  })
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
})

it.each(['account', 'session'] as const)(
  'does not resurrect an old pending Start after an administrator %s switch',
  async (change) => {
    getChannelOptions.mockResolvedValue(channels)
    let completeStart!: (job: FingerprintJob) => void
    const { startChannelFingerprint } = await import('../fingerprint-api')
    vi.mocked(startChannelFingerprint).mockReturnValueOnce(
      new Promise<FingerprintJob>((resolve) => {
        completeStart = resolve
      })
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    await waitFor(() => expect(startChannelFingerprint).toHaveBeenCalledTimes(1))

    const startSignal = vi.mocked(startChannelFingerprint).mock.calls[0][2] as AbortSignal
    expect(startSignal).toBeInstanceOf(AbortSignal)
    expect(startSignal.aborted).toBe(false)

    act(() => {
      if (change === 'account') {
        useAuthStore
          .getState()
          .auth.setUser({ ...adminUser, id: 2, username: 'admin-two' })
      } else {
        useAuthStore.setState((state) => ({
          auth: {
            ...state.auth,
            session: {
              sid: 'replacement-session',
              current: true,
              login_method: 'password',
              ip: '',
              user_agent: '',
              created_at: 1,
              last_active_at: 1,
              expires_at: 9_999_999_999,
            },
          },
        }))
      }
    })
    expect(startSignal.aborted).toBe(true)
    expect(screen.getByRole('combobox', { name: /^model$/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
    await act(async () => {
      completeStart(runningJob())
    })
    expect(
      screen.queryByText(/detector-digest|bank-digest/)
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
    expect(getChannelFingerprint).not.toHaveBeenCalled()

    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    vi.mocked(startChannelFingerprint).mockResolvedValueOnce(
      runningJob({
        id: 'job-2',
        state: 'completed',
        detector_sha256: 'new-admin-detector',
      })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    expect(await screen.findByText('new-admin-detector')).toBeVisible()
    expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
  }
)

it('cancels the previous account channel search and ignores its late results', async () => {
  let completeSearch!: (result: typeof channels) => void
  getChannelOptions.mockReturnValueOnce(
    new Promise<typeof channels>((resolve) => {
      completeSearch = resolve
    })
  )
  getChannelOptions.mockResolvedValueOnce({
    ...channels,
    data: {
      ...channels.data,
      items: [{ id: 8, name: 'channel-eight', models: ['claude-sonnet-4'] }],
    },
  })
  const user = userEvent.setup()
  renderPage()
  await waitFor(() => expect(getChannelOptions).toHaveBeenCalledTimes(1))
  const searchSignal = getChannelOptions.mock.calls[0][1] as AbortSignal
  expect(searchSignal.aborted).toBe(false)

  act(() => {
    useAuthStore
      .getState()
      .auth.setUser({ ...adminUser, id: 2, username: 'admin-two' })
  })
  expect(searchSignal.aborted).toBe(true)
  await act(async () => {
    completeSearch(channels)
  })
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  expect(
    await screen.findByRole('option', { name: '#8 channel-eight' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: '#7 channel-seven' })
  ).not.toBeInTheDocument()
})

it('debounces channel search and follows has_more instead of fetching everything', async () => {
  getChannelOptions.mockResolvedValue(channels)
  renderPage()
  await waitFor(() => expect(getChannelOptions).toHaveBeenCalledTimes(1))
  expect(getChannelOptions.mock.calls[0][0]).toMatchObject({
    p: 1,
    page_size: 30,
  })

  vi.useFakeTimers()
  const search = screen.getByPlaceholderText('Search channels')
  fireEvent.change(search, { target: { value: 'sev' } })
  fireEvent.change(search, { target: { value: 'seven' } })
  expect(getChannelOptions).toHaveBeenCalledTimes(1)

  await act(async () => {
    await vi.advanceTimersByTimeAsync(300)
  })
  expect(getChannelOptions).toHaveBeenCalledTimes(2)
  expect(getChannelOptions).toHaveBeenLastCalledWith(
    expect.objectContaining({ keyword: 'seven', p: 1 }),
    expect.any(AbortSignal)
  )

  // has_more=false means the only page is loaded; no separate paging UI.
  expect(
    screen.queryByRole('button', { name: /load more|next|previous/i })
  ).not.toBeInTheDocument()
  // Narrowing with a keyword restarts at the first page.
  expect(getChannelOptions).toHaveBeenLastCalledWith(
    expect.objectContaining({ keyword: 'seven', p: 1 }),
    expect.any(AbortSignal)
  )
})

it('shows a channel options business failure and retries the load', async () => {
  getChannelOptions
    .mockResolvedValueOnce({
      success: false,
      message: 'Channel options are temporarily unavailable',
    })
    .mockResolvedValue(channels)
  const user = userEvent.setup()
  renderPage()

  expect(
    await screen.findByText('Channel options are temporarily unavailable')
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: /retry/i }))
  await waitFor(() =>
    expect(
      screen.queryByText('Channel options are temporarily unavailable')
    ).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  expect(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  ).toBeVisible()
})

it('shows the actual prompt and reply of every probe in its own card', async () => {
  getChannelOptions.mockResolvedValue(channels)
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({
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
          prompt: 'list one hundred integers',
          output: '1 2 3 4 5',
        },
        {
          id: 'p2',
          state: 'queued',
          expected_count: 100,
          parsed_count: 0,
          prompt_tokens: 0,
          output_tokens: 0,
          max_tokens: 0,
          prompt: 'list one hundred integers again',
        },
      ],
    })
  )
  getChannelFingerprint.mockImplementation(() => new Promise(() => {}))
  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('list one hundred integers')).toBeVisible()
  expect(screen.getByText('1 2 3 4 5')).toBeVisible()
  expect(screen.getByText('list one hundred integers again')).toBeVisible()
  expect(
    within(screen.getByRole('region', { name: /^challenge 1$/i })).getByText(
      'Parsed numbers: 100'
    )
  ).toBeVisible()
  expect(
    within(screen.getByRole('region', { name: /^challenge 2$/i })).getByText(
      'Parsed numbers: 0'
    )
  ).toBeVisible()
  // Every slot is rendered: two real probes plus one explicit not-run slot.
  expect(document.querySelectorAll('[data-probe-slot="probe"]')).toHaveLength(2)
  expect(document.querySelectorAll('[data-probe-slot="unused"]')).toHaveLength(
    1
  )
  expect(screen.getByText('Not run')).toBeVisible()
})

it('updates live probe states and preserves terminal scoring, failure evidence and cancellation controls', async () => {
  getChannelOptions.mockResolvedValue(channels)
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob())
  let completePoll!: (value: FingerprintJob) => void
  getChannelFingerprint.mockReturnValueOnce(
    new Promise<FingerprintJob>((resolve) => {
      completePoll = resolve
    })
  )
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(await screen.findByRole('button', { name: /^cancel$/i })).toBeEnabled()
  expect(screen.getByRole('button', { name: /change prompts/i })).toBeDisabled()
  expect(
    within(screen.getByRole('region', { name: /^challenge 2$/i })).getByText(
      'Queued'
    )
  ).toBeVisible()
  await waitFor(() => expect(completePoll).toBeTypeOf('function'))
  const current = runningJob()
  await act(async () => {
    completePoll({
      ...current,
      state: 'inconclusive',
      probes: [
        current.probes[0],
        {
          ...current.probes[1],
          state: 'failed',
          prompt: 'actual failed prompt',
          error: 'No challenge produced a reply.',
        },
      ],
      analysis: {
        Decision: 'inconclusive',
        Prediction: '',
        PredictionName: '',
        Family: '',
        FamilyName: '',
        Probability: null,
        UsedOutputs: 1,
        Reason: 'Received 1/3 valid samples. Three valid samples are required.',
        BankVersion: 'reference-version',
        Results: [
          {
            Model: 'reference-model',
            DisplayName: 'Reference candidate',
            Family: '',
            FamilyName: '',
            Score: 0.75,
            Probability: null,
          },
        ],
      },
    })
  })
  const failed = screen.getByRole('region', { name: /^challenge 2$/i })
  expect(await within(failed).findByText('Failed')).toBeVisible()
  expect(within(failed).getByRole('alert')).toHaveTextContent(
    'No challenge produced a reply.'
  )
  expect(within(failed).getByText('Parsed numbers: 0')).toBeVisible()
  expect(screen.getByText('Reference candidate')).toBeVisible()
  expect(screen.getByText('score 0.750 (uncalibrated)')).toBeVisible()
  expect(
    screen.getByText('Received 1/3 valid samples. Three valid samples are required.')
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /^cancel$/i })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /change prompts/i })).toBeEnabled()
})

it.each([
  { remainingState: 'queued', processed: 2, progress: 67 },
  { remainingState: 'cancelled', processed: 3, progress: 100 },
] as const)(
  'counts terminal samples as processed progress when the remaining sample is $remainingState',
  async ({ remainingState, processed, progress }) => {
    getChannelOptions.mockResolvedValue(channels)
    const base = runningJob()
    vi.mocked(startChannelFingerprint).mockResolvedValue({
      ...base,
      state: remainingState === 'queued' ? 'running' : 'inconclusive',
      request_budget: 3,
      requests_used: 2,
      probes: [
        { ...base.probes[0], state: 'complete', output: 'preserved reply' },
        {
          ...base.probes[1],
          state: 'failed',
          error: 'fingerprint request failed (HTTP 503)',
        },
        {
          ...base.probes[1],
          id: 'p3',
          state: remainingState,
          error:
            remainingState === 'cancelled'
              ? 'Cancelled before dispatch.'
              : undefined,
        },
      ],
    })
    getChannelFingerprint.mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    expect(await screen.findByText('preserved reply')).toBeVisible()
    const failed = screen.getByRole('region', { name: /^challenge 2$/i })
    expect(within(failed).getByRole('alert')).toHaveTextContent(
      'fingerprint request failed (HTTP 503)'
    )
    expect(within(failed).getByText('Failed')).toBeVisible()
    const remaining = screen.getByRole('region', { name: /^challenge 3$/i })
    expect(
      within(remaining).getByText(
        remainingState === 'cancelled' ? 'Cancelled' : 'Queued'
      )
    ).toBeVisible()
    expect(
      screen.getByRole('progressbar', {
        name: `${processed} of 3 challenges processed`,
      })
    ).toHaveAttribute('aria-valuenow', String(progress))
  }
)

it('copies the actual preview prompt and keeps disclosure collapsed to four lines until expanded', async () => {
  getChannelOptions.mockResolvedValue(channels)
  const user = userEvent.setup()
  const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const prompt = await screen.findByText('actual prompt one')
  const sample = screen.getByRole('region', { name: /^challenge 1$/i })
  expect(within(sample).getByText('Ready')).toBeVisible()
  expect(prompt).toHaveClass('line-clamp-4')
  const expand = within(sample).getByRole('button', { name: /^expand$/i })
  expect(expand).toHaveAttribute('aria-expanded', 'false')
  expect(expand).toHaveAttribute('aria-controls', prompt.id)
  await user.click(expand)
  expect(prompt).not.toHaveClass('line-clamp-4')
  const collapse = within(sample).getByRole('button', { name: /^collapse$/i })
  expect(collapse).toHaveAttribute('aria-expanded', 'true')
  collapse.focus()
  await user.keyboard('{Enter}')
  expect(prompt).toHaveClass('line-clamp-4')
  await user.click(
    within(sample).getByRole('button', { name: /copy to clipboard/i })
  )
  expect(copy).toHaveBeenCalledWith('actual prompt one')
  expect(
    within(sample).getByText(
      'Each prompt independently uses Chinese, English, Japanese, Korean, or French.'
    )
  ).toBeVisible()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
  copy.mockRestore()
})

it('reuses the immutable submitted preview and full Key context after an ambiguous Start and a cache replacement', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    auto_models: fingerprintChannel.models,
  })
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 9,
    keys: [
      {
        id: '9007199254740993',
        rev: 4,
        index: 0,
        status: 1,
        key_preview: 'sk-…masked',
        scope_mode: 'all',
        models: [],
      },
    ],
  })
  const selection: FingerprintContext = {
    model: 'gpt-4.1',
    count: 3,
    key_entry_id: '9007199254740993',
    expected_rev: 4,
    key_index: 0,
  }
  startChannelFingerprint
    .mockRejectedValueOnce(new Error('response unavailable'))
    .mockResolvedValueOnce(
      runningJob({ state: 'completed' })
    )
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const keyControl = await screen.findByRole('combobox', { name: /^tested key$/i })
  await waitFor(() => expect(keyControl).toBeEnabled())
  await user.click(keyControl)
  await user.click(await screen.findByRole('option', { name: /1 · sk-…masked/ }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  const previewCalls = previewChannelFingerprint.mock.calls.length
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(await screen.findByText('response unavailable')).toBeVisible()
  expect(screen.getByText('actual prompt one')).toBeVisible()
  // A separate cache writer/refetch can replace even a disabled query. The
  // already-submitted opaque ID, prompts and bound selection must not change.
  const submittedPreviewQuery = client.getQueryCache().find({
    queryKey: ['fingerprint-preview'],
    exact: false,
    predicate: (query) => {
      const data = query.state.data as FingerprintPreview | undefined
      return (
        data !== undefined &&
        data.key_entry_id === selection.key_entry_id &&
        !data.endpoint_type &&
        data.count === 3
      )
    },
  })
  expect(submittedPreviewQuery).toBeDefined()
  if (!submittedPreviewQuery) throw new Error('Expected the submitted preview')
  await act(async () => {
    client.setQueryData(submittedPreviewQuery.queryKey, {
      ...preview,
      ...selection,
      preview_id: 'replacement-after-start',
      expires_at: Date.now() + 60_000,
      probes: preview.probes.map((probe) => ({
        ...probe,
        prompt: `replacement ${probe.prompt}`,
      })),
    })
    await client.invalidateQueries({ queryKey: ['fingerprint-preview'] })
  })
  await waitFor(() => {
    expect(
      client.getQueryData<FingerprintPreview>(submittedPreviewQuery.queryKey)
        ?.preview_id
    ).toBe('replacement-after-start')
    expect(
      screen.queryByText('replacement actual prompt one')
    ).not.toBeInTheDocument()
  })
  expect(screen.getByText('actual prompt one')).toBeVisible()
  expect(previewChannelFingerprint).toHaveBeenCalledTimes(previewCalls)
  const now = vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 120_000)
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(await screen.findByText('detector-digest')).toBeVisible()
  expect(vi.mocked(startChannelFingerprint).mock.calls[1].slice(0, 2)).toEqual(
    vi.mocked(startChannelFingerprint).mock.calls[0].slice(0, 2)
  )
  expect(startChannelFingerprint).toHaveBeenLastCalledWith(
    7,
    { ...selection, preview_id: 'preview-1' },
    expect.any(AbortSignal)
  )
  expect(previewChannelFingerprint).toHaveBeenCalledTimes(previewCalls)
  now.mockRestore()

  previewChannelFingerprint.mockResolvedValueOnce({
    ...preview,
    ...selection,
    preview_id: 'preview-2',
    expires_at: Date.now() + 60_000,
    probes: preview.probes.map((probe) => ({
      ...probe,
      prompt: `new ${probe.prompt}`,
    })),
  })
  await user.click(screen.getByRole('button', { name: /change prompts/i }))
  expect(await screen.findByText('new actual prompt one')).toBeVisible()
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
  expect(startChannelFingerprint).toHaveBeenCalledTimes(2)
  vi.mocked(startChannelFingerprint).mockResolvedValueOnce(
    runningJob({ id: 'job-2', state: 'completed' })
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenLastCalledWith(
      7,
      { ...selection, preview_id: 'preview-2' },
      expect.any(AbortSignal)
    )
  )
})

it('rejects an expired unstarted preview without regenerating it or dispatching paid work', async () => {
  getChannelOptions.mockResolvedValue(channels)
  previewChannelFingerprint.mockResolvedValue({
    ...preview,
    expires_at: Date.now() - 1,
  })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await screen.findByText('actual prompt one')
  const previewCalls = previewChannelFingerprint.mock.calls.length
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(
    await screen.findByText(
      'Fingerprint preview is unavailable. Change prompts and try again.'
    )
  ).toBeVisible()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
  expect(previewChannelFingerprint).toHaveBeenCalledTimes(previewCalls)
})

it.each([
  { label: 'missing opaque ID', change: { preview_id: '' } },
  { label: 'different channel', change: { channel_id: 8 } },
  { label: 'different model', change: { model: 'other-model' } },
  { label: 'different endpoint', change: { endpoint_type: 'anthropic' } },
  { label: 'different count', change: { count: 2 } },
  {
    label: 'unexpected key binding',
    change: { key_entry_id: '41', expected_rev: 1 },
  },
  {
    label: 'missing actual prompt',
    change: { probes: [{ id: 'p1', prompt: '', expected_count: 293 }] },
  },
  { label: 'incomplete probe set', change: { probes: [] } },
])('does not dispatch a preview with $label', async ({ change }) => {
  getChannelOptions.mockResolvedValue(channels)
  previewChannelFingerprint.mockResolvedValue({
    ...preview,
    expires_at: Date.now() + 60_000,
    ...change,
  })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  expect(
    await screen.findByText(
      'Fingerprint preview is unavailable. Change prompts and try again.'
    )
  ).toBeVisible()
  const start = screen.getByRole('button', { name: /start check/i })
  expect(start).toBeDisabled()
  await user.click(start)
  expect(startChannelFingerprint).not.toHaveBeenCalled()
  expect(screen.queryByText('actual prompt one')).not.toBeInTheDocument()
})

it('discards a late preview when the model changes and starts only the current challenge set', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    models: ['gpt-4.1', 'gpt-4.1-mini'],
  })
  let completePreview!: (value: FingerprintPreview) => void
  previewChannelFingerprint.mockReturnValueOnce(
    new Promise<FingerprintPreview>((resolve) => {
      completePreview = resolve
    })
  )
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob({ state: 'completed' }))
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() => expect(completePreview).toBeTypeOf('function'))
  const signal = previewChannelFingerprint.mock.calls[0][2] as AbortSignal
  await user.click(screen.getByRole('combobox', { name: /^model$/i }))
  await user.click(await screen.findByRole('option', { name: 'gpt-4.1-mini' }))
  await screen.findByText('actual prompt one')
  expect(screen.getByText('actual prompt two')).toBeVisible()
  expect(signal.aborted).toBe(true)
  await act(async () => {
    completePreview({
      ...preview,
      expires_at: Date.now() + 60_000,
      probes: preview.probes.map((probe) => ({
        ...probe,
        prompt: `stale ${probe.prompt}`,
      })),
    })
  })
  expect(screen.queryByText('stale actual prompt one')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      {
        preview_id: 'preview-1',
        model: 'gpt-4.1-mini',
        count: 3,
      },
      expect.any(AbortSignal)
    )
  )
})

it('shows a preview failure without paid work and retries only the preview', async () => {
  getChannelOptions.mockResolvedValue(channels)
  previewChannelFingerprint.mockRejectedValueOnce(
    new Error('preview temporarily unavailable')
  )
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  expect(await screen.findByText('preview temporarily unavailable')).toBeVisible()
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: /^retry$/i }))
  expect(await screen.findByText('actual prompt one')).toBeVisible()
  expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
})

it('pins an explicit masked Key by string ID, row revision and index while filtering its model allowlist', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    models: ['gpt-4.1', 'gpt-4.1-mini'],
    auto_models: ['gpt-4.1-mini'],
  })
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 9,
    keys: [
      {
        id: '9007199254740993',
        rev: 4,
        index: 0,
        status: 1,
        key_preview: 'sk-…masked',
        scope_mode: 'allowlist',
        models: ['gpt-4.1-mini'],
      },
      {
        id: '42',
        rev: 2,
        index: 1,
        status: 1,
        key_preview: 'sk-…empty',
        scope_mode: 'allowlist',
        models: [],
      },
      {
        id: '43',
        rev: 1,
        index: 2,
        status: 1,
        key_preview: 'sk-…all',
        scope_mode: 'all',
        models: [],
      },
    ],
  })
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob({ state: 'completed' }))
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const keyControl = await screen.findByRole('combobox', { name: /^tested key$/i })
  await waitFor(() => expect(keyControl).toBeEnabled())
  expect(keyControl).toHaveValue('Auto (by tested model)')
  expect(getFingerprintKeys).toHaveBeenCalledWith(7, 1, expect.any(AbortSignal))
  await user.click(screen.getByRole('combobox', { name: /^model$/i }))
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual(['gpt-4.1-mini'])
  await user.click(screen.getByRole('option', { name: 'gpt-4.1-mini' }))
  await user.click(keyControl)
  await user.click(await screen.findByRole('option', { name: /1 · sk-…masked/ }))
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
      'gpt-4.1-mini'
    )
  )
  await user.click(screen.getByRole('combobox', { name: /^model$/i }))
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual(['gpt-4.1-mini'])
  await user.click(screen.getByRole('option', { name: 'gpt-4.1-mini' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  expect(previewChannelFingerprint).toHaveBeenLastCalledWith(
    7,
    {
      model: 'gpt-4.1-mini',
      count: 3,
      key_entry_id: '9007199254740993',
      expected_rev: 4,
      key_index: 0,
    },
    expect.any(AbortSignal)
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      {
        preview_id: 'preview-1',
        model: 'gpt-4.1-mini',
        count: 3,
        key_entry_id: '9007199254740993',
        expected_rev: 4,
        key_index: 0,
      },
      expect.any(AbortSignal)
    )
  )
  await screen.findByText('detector-digest')
  await user.click(keyControl)
  await user.click(await screen.findByRole('option', { name: /2 · sk-…empty/ }))
  expect(await screen.findByText('This channel declares no models')).toBeVisible()
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
  await user.click(keyControl)
  await user.click(await screen.findByRole('option', { name: /3 · sk-…all/ }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('combobox', { name: /^model$/i }))
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual(['gpt-4.1', 'gpt-4.1-mini'])
  await user.click(screen.getByRole('option', { name: 'gpt-4.1' }))
  await user.click(keyControl)
  await user.click(
    await screen.findByRole('option', { name: /^auto \(by tested model\)$/i })
  )
  await waitFor(() =>
    expect(previewChannelFingerprint).toHaveBeenLastCalledWith(
      7,
      { model: 'gpt-4.1-mini', count: 3 },
      expect.any(AbortSignal)
    )
  )
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
})

it.each([
  { label: 'revision', replacement: { id: '41', rev: 5, index: 0 } },
  { label: 'position', replacement: { id: '41', rev: 4, index: 1 } },
  { label: 'allocation', replacement: { id: '42', rev: 4, index: 0 } },
])('does not silently fall back when the selected Key changes $label', async ({ replacement }) => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    auto_models: fingerprintChannel.models,
  })
  const key = {
    id: '41',
    rev: 4,
    index: 0,
    status: 1,
    key_preview: 'sk-…masked',
    scope_mode: 'all' as const,
  }
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 9,
    keys: [key],
  })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const keyControl = await screen.findByRole('combobox', { name: /^tested key$/i })
  await waitFor(() => expect(keyControl).toBeEnabled())
  await user.click(keyControl)
  await user.click(await screen.findByRole('option', { name: /1 · sk-…masked/ }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  const previewCalls = previewChannelFingerprint.mock.calls.length
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob())
  let completePoll!: (job: FingerprintJob) => void
  getChannelFingerprint.mockReturnValueOnce(
    new Promise<FingerprintJob>((resolve) => {
      completePoll = resolve
    })
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('detector-digest')
  await waitFor(() => expect(completePoll).toBeTypeOf('function'))
  const signal = getChannelFingerprint.mock.calls[0][2] as AbortSignal
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 10,
    keys: [{ ...key, ...replacement }],
  })
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['fingerprint-keys'] })
  })
  expect(await screen.findByText('Channel key configuration changed, reload and retry')).toBeVisible()
  expect(keyControl).toHaveValue('Unavailable')
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(previewChannelFingerprint).toHaveBeenCalledTimes(previewCalls)
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
  expect(signal.aborted).toBe(true)
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
  await act(async () => {
    completePoll(
      runningJob({ state: 'completed', detector_sha256: 'old-key-result' })
    )
  })
  expect(screen.queryByText('old-key-result')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
})

it.each([
  { label: 'missing identity', identity: {} },
  { label: 'noncanonical identity', identity: { id: '041', rev: 4 } },
  { label: 'missing revision', identity: { id: '41' } },
])('does not offer an explicit Key with $label', async ({ identity }) => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    auto_models: fingerprintChannel.models,
  })
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 9,
    keys: [{ ...identity, index: 0, status: 1, key_preview: 'sk-…invalid' }],
  })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const key = await screen.findByRole('combobox', { name: /^tested key$/i })
  await waitFor(() => expect(key).toBeEnabled())
  await user.click(key)
  const invalid = await screen.findByRole('option', { name: /1 · sk-…invalid/ })
  expect(invalid).toHaveAttribute('aria-disabled', 'true')
  await user.click(invalid)
  await user.keyboard('{Escape}')
  expect(key).toHaveValue('Auto (by tested model)')
  expect(startChannelFingerprint).not.toHaveBeenCalled()
})

it('rejects mixed-revision Key pages and reloads a coherent masked snapshot', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    auto_models: fingerprintChannel.models,
  })
  getFingerprintKeys
    .mockResolvedValueOnce({ page: 1, total_pages: 2, revision: 9, keys: [] })
    .mockResolvedValueOnce({ page: 2, total_pages: 2, revision: 10, keys: [] })
    .mockResolvedValue({ page: 1, total_pages: 1, revision: 10, keys: [] })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  const loadMore = await screen.findByRole('button', { name: /^load more$/i })
  await waitFor(() => expect(loadMore).toBeEnabled())
  await user.click(loadMore)
  expect(await screen.findByText('Channel key configuration changed, reload and retry')).toBeVisible()
  expect(getFingerprintKeys).toHaveBeenLastCalledWith(7, 2, expect.any(AbortSignal))
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: /^retry$/i }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  expect(screen.queryByText('Channel key configuration changed, reload and retry')).not.toBeInTheDocument()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
})

it('uses server-selected protocols without an endpoint chooser or endpoint override payload', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({ ...fingerprintChannel, type: 14 })
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob({ state: 'completed' }))
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  expect(screen.queryByLabelText(/^endpoint$/i)).not.toBeInTheDocument()
  expect(previewChannelFingerprint).toHaveBeenLastCalledWith(
    7,
    { model: 'gpt-4.1', count: 3 },
    expect.any(AbortSignal)
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      { preview_id: 'preview-1', model: 'gpt-4.1', count: 3 },
      expect.any(AbortSignal)
    )
  )
})

it('fails closed when a multi-Key channel omits its authoritative automatic model projection', async () => {
  getChannelOptions.mockResolvedValue(channels)
  getFingerprintChannel.mockResolvedValue({
    ...fingerprintChannel,
    is_multi_key: true,
    auto_models: undefined,
  })
  getFingerprintKeys.mockResolvedValue({
    page: 1,
    total_pages: 1,
    revision: 3,
    keys: [],
  })
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  expect(await screen.findByText('This channel declares no models')).toBeVisible()
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(previewChannelFingerprint).not.toHaveBeenCalled()
})

it('keeps long channel names readable and prevents obsolete search pages appending to a new search', async () => {
  const longName =
    'channel-seven-with-a-complete-long-operator-visible-name-and-suffix'
  getChannelOptions.mockResolvedValue({
    ...channels,
    data: {
      ...channels.data,
      items: [{ ...channels.data.items[0], name: longName }],
      has_more: true,
    },
  })
  const user = userEvent.setup()
  renderPage()
  const channel = screen.getByRole('combobox', { name: /^channel$/i })
  await user.click(channel)
  const option = await screen.findByRole('option', { name: `#7 ${longName}` })
  expect(within(option).getByText(`#7 ${longName}`)).toHaveClass(
    'whitespace-normal'
  )
  await user.click(option)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  expect(channel).toHaveValue(`#7 ${longName}`)
  expect(channel).toHaveAttribute('title', `#7 ${longName}`)
  await user.click(channel)
  let completeOldPage!: (value: typeof channels) => void
  getChannelOptions.mockReturnValueOnce(
    new Promise<typeof channels>((resolve) => {
      completeOldPage = resolve
    })
  )
  await user.click(await screen.findByRole('button', { name: /^load more$/i }))
  await waitFor(() => expect(completeOldPage).toBeTypeOf('function'))
  const oldSignal = getChannelOptions.mock.calls.at(-1)?.[1] as AbortSignal
  getChannelOptions.mockResolvedValue({
    ...channels,
    data: {
      ...channels.data,
      items: [
        { ...channels.data.items[0], name: longName },
        { id: 9, name: 'fresh-search', models: [] },
      ],
    },
  })
  await user.type(channel, 'fresh')
  await waitFor(() =>
    expect(getChannelOptions).toHaveBeenLastCalledWith(
      expect.objectContaining({ p: 1, keyword: 'fresh', ids: '7' }),
      expect.any(AbortSignal)
    )
  )
  expect(oldSignal.aborted).toBe(true)
  await act(async () => {
    completeOldPage({
      ...channels,
      data: {
        ...channels.data,
        page: 2,
        items: [{ id: 8, name: 'obsolete-page', models: [] }],
      },
    })
  })
  expect(
    await screen.findByRole('option', { name: '#9 fresh-search' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: '#8 obsolete-page' })
  ).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  expect(channel).toHaveValue(`#7 ${longName}`)
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent('gpt-4.1')
})

it.each(['metadata', 'keys'] as const)(
  'does not preview or Start while %s are unavailable and recovers only after the safe read succeeds',
  async (failure) => {
    getChannelOptions.mockResolvedValue(channels)
    if (failure === 'metadata') {
      getFingerprintChannel.mockRejectedValueOnce(
        new Error('safe metadata unavailable')
      )
    } else {
      getFingerprintChannel.mockResolvedValue({
        ...fingerprintChannel,
        is_multi_key: true,
        auto_models: fingerprintChannel.models,
      })
      getFingerprintKeys.mockRejectedValueOnce(
        new Error('masked keys unavailable')
      )
    }
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
    expect(
      await screen.findByText(
        failure === 'metadata'
          ? 'safe metadata unavailable'
          : 'masked keys unavailable'
      )
    ).toBeVisible()
    expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
    expect(previewChannelFingerprint).not.toHaveBeenCalled()
    expect(startChannelFingerprint).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: /^retry$/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    expect(startChannelFingerprint).not.toHaveBeenCalled()
  }
)

it('aborts an old administrator preview and never shows it under the next identity', async () => {
  getChannelOptions.mockResolvedValue(channels)
  let completePreview!: (value: FingerprintPreview) => void
  previewChannelFingerprint.mockReturnValueOnce(
    new Promise<FingerprintPreview>((resolve) => {
      completePreview = resolve
    })
  )
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() => expect(completePreview).toBeTypeOf('function'))
  const signal = previewChannelFingerprint.mock.calls[0][2] as AbortSignal
  act(() => {
    useAuthStore
      .getState()
      .auth.setUser({ ...adminUser, id: 2, username: 'admin-two' })
  })
  expect(signal.aborted).toBe(true)
  await act(async () => {
    completePreview({ ...preview, expires_at: Date.now() + 60_000 })
  })
  expect(screen.queryByText('actual prompt one')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  expect(startChannelFingerprint).not.toHaveBeenCalled()
})

it('projects sanitized metadata and masked Key pages without requesting credentials', async () => {
  const actual = await vi.importActual<typeof import('../fingerprint-api')>(
    '../fingerprint-api'
  )
  const { api } = await import('@/lib/api')
  const controller = new AbortController()
  const get = vi.spyOn(api, 'get').mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        id: 7,
        type: 1,
        models: ['gpt-4.1', 'gpt-4.1-mini'],
        auto_models: ['gpt-4.1-mini'],
        is_multi_key: true,
        key: 'must-not-retain',
        base_url: 'https://private-upstream.invalid',
      },
    },
  })
  get.mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        id: 7,
        type: 1,
        models: ['gpt-4.1', 'gpt-4.1-mini'],
        auto_models: ['gpt-4.1-mini'],
        is_multi_key: true,
        page: 1,
        total_pages: 2,
        revision: 9,
        keys: [
          {
            id: '41',
            rev: 4,
            index: 0,
            status: 1,
            key_preview: 'sk-…masked',
            scope_mode: 'all',
            models: [],
            reason: 'must-not-retain',
            key: 'must-not-retain',
          },
        ],
      },
    },
  })
  try {
    expect(await actual.getFingerprintChannel(7, controller.signal)).toEqual({
      id: 7,
      type: 1,
      models: ['gpt-4.1', 'gpt-4.1-mini'],
      auto_models: ['gpt-4.1-mini'],
      is_multi_key: true,
    })
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith('/api/channel/7/fingerprint/options', {
      signal: controller.signal,
    })
    expect(await actual.getFingerprintKeys(7, 1, controller.signal)).toEqual({
      page: 1,
      total_pages: 2,
      revision: 9,
      keys: [
        {
          id: '41',
          rev: 4,
          index: 0,
          status: 1,
          key_preview: 'sk-…masked',
          scope_mode: 'all',
          models: [],
        },
      ],
    })
    expect(get).toHaveBeenCalledTimes(2)
    expect(get).toHaveBeenLastCalledWith(
      '/api/channel/7/fingerprint/options',
      { params: { p: 1, page_size: 50 }, signal: controller.signal }
    )
    get.mockResolvedValueOnce({
      data: {
        success: true,
        data: { id: 7, page: 1, total_pages: 1, revision: 10, keys: null },
      },
    })
    expect(await actual.getFingerprintKeys(7, 1, controller.signal)).toEqual({
      page: 1,
      total_pages: 1,
      revision: 10,
      keys: [],
    })
  } finally {
    get.mockRestore()
  }
})

it('uses the opaque preview envelope for Start without client prompts or a scheduling-budget field', async () => {
  const actual = await vi.importActual<typeof import('../fingerprint-api')>(
    '../fingerprint-api'
  )
  const { api } = await import('@/lib/api')
  const controller = new AbortController()
  const selection: FingerprintContext = {
    model: 'gpt-4.1',
    count: 3,
    key_entry_id: '9007199254740993',
    expected_rev: 4,
    key_index: 0,
  }
  const savedPreview = {
    ...preview,
    ...selection,
    expires_at: Date.now() + 60_000,
  }
  const finished = runningJob({ state: 'completed' })
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({ data: { success: true, data: savedPreview } })
    .mockResolvedValueOnce({ data: { success: true, data: finished } })
  try {
    expect(
      await actual.previewChannelFingerprint(7, selection, controller.signal)
    ).toEqual(savedPreview)
    expect(post).toHaveBeenNthCalledWith(
      1,
      '/api/channel/7/fingerprint/preview',
      selection,
      { signal: controller.signal }
    )
    expect(
      await actual.startChannelFingerprint(
        7,
        { ...selection, preview_id: savedPreview.preview_id },
        controller.signal
      )
    ).toEqual(finished)
    expect(post).toHaveBeenNthCalledWith(
      2,
      '/api/channel/7/fingerprint',
      { ...selection, preview_id: savedPreview.preview_id },
      { signal: controller.signal }
    )
    expect(post.mock.calls[1][1]).not.toHaveProperty('token_budget')
    expect(post.mock.calls[1][1]).not.toHaveProperty('probes')
    expect(post.mock.calls[1][1]).not.toHaveProperty('prompt')
    const retryPayload = {
      ...selection,
      preview_id: savedPreview.preview_id,
      probe_id: savedPreview.probes[1].id,
      expected_attempt: 1,
    }
    post.mockResolvedValueOnce({ data: { success: true, data: finished } })
    expect(
      await actual.retryChannelFingerprintProbe(
        7,
        finished.id,
        retryPayload,
        controller.signal
      )
    ).toEqual(finished)
    expect(post).toHaveBeenLastCalledWith(
      `/api/channel/7/fingerprint/${finished.id}/retry`,
      retryPayload,
      { signal: controller.signal }
    )
    post.mockResolvedValueOnce({ data: { success: true, data: finished } })
    await actual.cancelChannelFingerprint(7, finished.id, controller.signal, 2)
    expect(post).toHaveBeenLastCalledWith(
      `/api/channel/7/fingerprint/${finished.id}/cancel`,
      { expected_revision: 2 },
      { signal: controller.signal }
    )
  } finally {
    post.mockRestore()
  }
})

it('hides a running result and aborts its read when channel metadata removes the selected model', async () => {
  getChannelOptions.mockResolvedValue(channels)
  vi.mocked(startChannelFingerprint).mockResolvedValue(runningJob())
  let completePoll!: (job: FingerprintJob) => void
  getChannelFingerprint.mockReturnValue(
    new Promise<FingerprintJob>((resolve) => {
      completePoll = resolve
    })
  )
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(await screen.findByRole('option', { name: '#7 channel-seven' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await screen.findByText('detector-digest')
  await waitFor(() => expect(completePoll).toBeTypeOf('function'))
  const signal = getChannelFingerprint.mock.calls[0][2] as AbortSignal
  getFingerprintChannel.mockResolvedValue({ ...fingerprintChannel, models: [] })
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['fingerprint-channel'] })
  })
  expect(await screen.findByText('This channel declares no models')).toBeVisible()
  expect(signal.aborted).toBe(true)
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
  await act(async () => {
    completePoll(runningJob({ state: 'completed', detector_sha256: 'stale-poll' }))
  })
  expect(screen.queryByText('stale-poll')).not.toBeInTheDocument()
  expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
})

it.each([
  {
    message: 'fingerprint request no longer matches the previewed prompt',
    key: 'fingerprint request no longer matches the previewed prompt',
    translation: 'Preview text was changed before dispatch',
    expected: 'Preview text was changed before dispatch',
  },
  {
    message: 'fingerprint request could not be completed',
    key: 'fingerprint request could not be completed',
    translation: 'Localized request failure',
    expected: 'Localized request failure',
  },
  {
    message: 'fingerprint request failed (HTTP 503)',
    key: 'fingerprint request failed (HTTP {{status}})',
    translation: 'Localized request failure: HTTP {{status}}',
    expected: 'Localized request failure: HTTP 503',
  },
  {
    message: 'the reply contained no text to score',
    key: 'the reply contained no text to score',
    translation: 'Localized empty reply',
    expected: 'Localized empty reply',
  },
  {
    message: 'fingerprint reference data is not available',
    key: 'fingerprint reference data is not available',
    translation: 'Localized reference data failure',
    expected: 'Localized reference data failure',
  },
  {
    message: 'context deadline exceeded',
    key: 'Timed out',
    translation: 'Localized timeout',
    expected: 'Localized timeout',
  },
  {
    message: 'fingerprint request failed (HTTP 503) provider {{detail}} $t(secret)',
    key: 'fingerprint request failed (HTTP 503) provider {{detail}} $t(secret)',
    translation: 'Unknown provider text must not be translated',
    expected:
      'fingerprint request failed (HTTP 503) provider {{detail}} $t(secret)',
  },
])(
  'translates only the fixed safe run message: $message',
  async ({ message, key, translation, expected }) => {
    getChannelOptions.mockResolvedValue(channels)
    const instance = i18next.createInstance()
    await instance.init({
      lng: 'en',
      fallbackLng: 'en',
      resources: { en: { translation: { [key]: translation } } },
    })
    vi.mocked(startChannelFingerprint).mockResolvedValue(
      runningJob({
        state: 'failed',
        error: message,
        probes: [
          {
            ...runningJob().probes[0],
            state: 'failed',
            prompt: preview.probes[0].prompt,
            output: '',
            error: message,
          },
        ],
      })
    )
    renderPage(undefined, instance)
    const user = userEvent.setup()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: /start check/i }))
    await waitFor(() => expect(screen.getAllByText(expected)).toHaveLength(2))
    expect(startChannelFingerprint).toHaveBeenCalledTimes(1)
    expect(getChannelFingerprint).not.toHaveBeenCalled()
    expect(retryChannelFingerprintProbe).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: /start check/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /change prompts/i })).toBeEnabled()
  }
)

it.each([
  { phase: 'preview', message: 'fingerprint preview is unavailable or expired' },
  { phase: 'start', message: 'fingerprint reference data is not available' },
  { phase: 'retry', message: 'fingerprint sample cannot be retried' },
  { phase: 'poll', message: 'fingerprint check was not found or has expired' },
  { phase: 'cancel', message: 'fingerprint check already finished' },
] as const)(
  'translates a safe $phase failure at its presentation boundary',
  async ({ phase, message }) => {
    getChannelOptions.mockResolvedValue(channels)
    vi.mocked(startChannelFingerprint).mockResolvedValue(
      phase === 'retry' ? retryableJob() : runningJob()
    )
    getChannelFingerprint.mockReturnValue(new Promise(() => {}))
    const error = new Error(message)
    if (phase === 'preview') previewChannelFingerprint.mockRejectedValue(error)
    if (phase === 'start') vi.mocked(startChannelFingerprint).mockRejectedValue(error)
    if (phase === 'retry') retryChannelFingerprintProbe.mockRejectedValue(error)
    if (phase === 'poll') getChannelFingerprint.mockRejectedValue(error)
    if (phase === 'cancel') cancelChannelFingerprint.mockRejectedValue(error)
    const toastError = vi.spyOn(toast, 'error').mockReturnValue('fingerprint-error')
    const instance = i18next.createInstance()
    await instance.init({
      lng: 'en',
      fallbackLng: 'en',
      resources: {
        en: { translation: { [message]: 'Localized safe failure' } },
        fr: { translation: { [message]: 'Erreur traduite' } },
      },
    })
    const user = userEvent.setup()
    renderPage(undefined, instance)
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    if (phase !== 'preview') {
      await waitFor(() =>
        expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
      )
      await user.click(screen.getByRole('button', { name: /start check/i }))
    }
    if (phase === 'retry') {
      await user.click(
        await screen.findByRole('button', { name: /retry sample/i })
      )
    }
    if (phase === 'cancel') {
      await user.click(await screen.findByRole('button', { name: /^cancel$/i }))
      await waitFor(() =>
        expect(toastError).toHaveBeenCalledWith('Localized safe failure')
      )
    } else {
      expect(await screen.findByText('Localized safe failure')).toBeVisible()
      await act(async () => {
        await instance.changeLanguage('fr')
      })
      expect(await screen.findByText('Erreur traduite')).toBeVisible()
    }
    expect(startChannelFingerprint).toHaveBeenCalledTimes(
      phase === 'preview' ? 0 : 1
    )
    expect(retryChannelFingerprintProbe).toHaveBeenCalledTimes(
      phase === 'retry' ? 1 : 0
    )
  }
)

it('marks retained probe text that was cut at the memory cap', async () => {
  getChannelOptions.mockResolvedValue(channels)
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({
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
          prompt: 'list integers',
          output: '1 2 3',
          output_truncated: true,
        },
      ],
    })
  )
  getChannelFingerprint.mockImplementation(() => new Promise(() => {}))
  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('Truncated')).toBeVisible()
})
