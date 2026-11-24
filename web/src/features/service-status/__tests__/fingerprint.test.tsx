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
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { Profiler } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { ChannelFingerprint } from '../fingerprint'
import type { FingerprintJob } from '../fingerprint-api'

const getChannelOptions = vi.fn()
const getChannelFingerprint = vi.fn()
const cancelChannelFingerprint = vi.fn()
const originalAuth = useAuthStore.getState().auth
const adminUser: AuthUser = {
  id: 1,
  username: 'admin-one',
  role: ROLE.ADMIN,
  permissions: {
    admin_permissions: { channel: { read: true, operate: true } },
  },
}

vi.mock('@/features/channels/api', () => ({
  getChannelOptions: (...args: unknown[]) => getChannelOptions(...args),
}))

vi.mock('../fingerprint-api', () => ({
  startChannelFingerprint: vi.fn(),
  getChannelFingerprint: (...args: unknown[]) => getChannelFingerprint(...args),
  cancelChannelFingerprint: (...args: unknown[]) =>
    cancelChannelFingerprint(...args),
}))

let client: QueryClient

beforeEach(() => {
  useAuthStore.setState({ auth: { ...originalAuth, user: adminUser } })
  vi.resetAllMocks()
})

afterEach(() => {
  cleanup()
  client?.clear()
  useAuthStore.setState({ auth: originalAuth })
  vi.clearAllMocks()
  vi.useRealTimers()
})

function renderPage(onCommit?: () => void) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18next}>
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
  getChannelOptions.mockResolvedValue(channels)
  renderPage()

  expect(
    await screen.findByText(
      /does not prove model identity, quality, or authenticity/
    )
  ).toBeTruthy()
  expect(screen.getByText(/paid requests to the selected channel/)).toBeTruthy()
})

it.each([
  { count: 3, changeCount: false },
  { count: 1, changeCount: true },
  { count: 2, changeCount: true },
])('submits $count challenges while defaulting to three', async (testCase) => {
  getChannelOptions.mockResolvedValue(channels)
  const { startChannelFingerprint } = await import('../fingerprint-api')
  vi.mocked(startChannelFingerprint).mockResolvedValue(
    runningJob({ state: 'completed' })
  )
  renderPage()

  const user = userEvent.setup()
  const countInput = screen.getByRole('spinbutton', { name: /^challenges$/i })
  expect(countInput).toHaveValue(3)
  expect(countInput).toHaveAttribute('min', '1')
  expect(countInput).toHaveAttribute('max', '3')
  if (testCase.changeCount) {
    fireEvent.change(countInput, { target: { value: String(testCase.count) } })
  }
  await user.click(await screen.findByRole('combobox', { name: /channel/i }))
  await user.click(
    await screen.findByRole('option', { name: /#7 channel-seven/ })
  )
  expect(screen.getByRole('combobox', { name: /^model$/i })).toHaveTextContent(
    'gpt-4.1'
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ model: 'gpt-4.1', count: testCase.count })
    )
  )
})

it('shows a failed poll with a retry instead of freezing progress', async () => {
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
  expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
  // A stopped run must not keep the form disabled: otherwise the operator could
  // not pick another channel or model after the run expired elsewhere.
  expect(screen.getByRole('combobox', { name: /^model$/i })).toBeEnabled()
  expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
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
  getChannelOptions.mockResolvedValue(channels)
  renderPage()

  expect(await screen.findByLabelText(/estimated token budget/i)).toBeTruthy()
  expect(screen.queryByLabelText(/output token budget/i)).toBeNull()
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
          models: ['qwen3-max@stable', 'gpt-4.1', 'qwen3-max@stable', 'gpt-4.1'],
        },
        { id: 8, name: 'channel-eight', models: ['claude-sonnet-4'] },
        { id: 9, name: 'channel-empty', models: [] },
      ],
    },
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
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() =>
    expect(startChannelFingerprint).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ model: 'qwen3-max@stable' })
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
  expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()

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

it.each(['page', 'refetch'] as const)(
  'keeps models during a pending %s and disables a missing hydrated channel when it settles',
  async (reload) => {
    const available = {
      ...channels,
      data: { ...channels.data, has_more: true },
    }
    getChannelOptions.mockResolvedValue(available)
    const { startChannelFingerprint } = await import('../fingerprint-api')
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
    await user.click(
      await screen.findByRole('option', { name: '#7 channel-seven' })
    )
    await waitFor(() => expect(getChannelOptions).toHaveBeenCalledTimes(2))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^next$/i })).toBeEnabled()
    )

    let completeOptions!: (result: typeof channels) => void
    getChannelOptions.mockReturnValueOnce(
      new Promise<typeof channels>((resolve) => {
        completeOptions = resolve
      })
    )
    if (reload === 'page') {
      await user.click(screen.getByRole('button', { name: /^next$/i }))
    } else {
      act(() => {
        void client.invalidateQueries({ queryKey: ['fingerprint-channels'] })
      })
    }
    await waitFor(() => expect(getChannelOptions).toHaveBeenCalledTimes(3))
    expect(getChannelOptions).toHaveBeenLastCalledWith(
      expect.objectContaining({ p: reload === 'page' ? 2 : 1, ids: '7' }),
      expect.any(AbortSignal)
    )
    expect(
      screen.getByRole('combobox', { name: /^model$/i })
    ).toHaveTextContent('gpt-4.1')
    expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
    expect(screen.queryByText('Unavailable')).not.toBeInTheDocument()

    await act(async () => {
      completeOptions({
        ...channels,
        data: {
          ...channels.data,
          items: [
            { id: 8, name: 'channel-eight', models: ['claude-sonnet-4'] },
          ],
          page: reload === 'page' ? 2 : 1,
        },
      })
    })
    expect(await screen.findByText('Unavailable')).toBeVisible()
    expect(screen.getByRole('combobox', { name: /^model$/i })).toBeDisabled()
    expect(
      screen.getByRole('combobox', { name: /^model$/i })
    ).not.toHaveTextContent(/gpt-4\.1|claude-sonnet-4/)
    expect(
      screen.getByRole('combobox', { name: /^channel$/i })
    ).not.toHaveTextContent('channel-eight')
    const startButton = screen.getByRole('button', { name: /start check/i })
    expect(startButton).toBeDisabled()
    await user.click(startButton)
    expect(startChannelFingerprint).not.toHaveBeenCalled()
  }
)

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
  expect(screen.getByRole('button', { name: /start check/i })).toBeEnabled()
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
})

it('does not resurrect an old pending start after an administrator account switch', async () => {
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
  await user.click(screen.getByRole('button', { name: /start check/i }))
  await waitFor(() => expect(startChannelFingerprint).toHaveBeenCalledTimes(1))

  act(() => {
    useAuthStore
      .getState()
      .auth.setUser({ ...adminUser, id: 2, username: 'admin-two' })
  })
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
  await user.click(screen.getByRole('button', { name: /start check/i }))
  expect(await screen.findByText('new-admin-detector')).toBeVisible()
  expect(screen.queryByText('detector-digest')).not.toBeInTheDocument()
})

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

  // has_more=false means the only page is loaded, so paging stays closed.
  expect(screen.getByRole('button', { name: /next/i })).toBeDisabled()
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
      ] as never,
    })
  )
  getChannelFingerprint.mockImplementation(() => new Promise(() => {}))
  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('list one hundred integers')).toBeVisible()
  expect(screen.getByText('1 2 3 4 5')).toBeVisible()
  expect(screen.getByText('list one hundred integers again')).toBeVisible()
  // Every slot is rendered: two real probes plus one explicit not-run slot.
  expect(document.querySelectorAll('[data-probe-slot="probe"]')).toHaveLength(
    2
  )
  expect(
    document.querySelectorAll('[data-probe-slot="unused"]')
  ).toHaveLength(1)
  expect(screen.getByText('Not run')).toBeVisible()
})

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
      ] as never,
    })
  )
  getChannelFingerprint.mockImplementation(() => new Promise(() => {}))
  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole('combobox', { name: /^channel$/i }))
  await user.click(
    await screen.findByRole('option', { name: '#7 channel-seven' })
  )
  await user.click(screen.getByRole('button', { name: /start check/i }))

  expect(await screen.findByText('Truncated')).toBeVisible()
})
