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
  getCoreRowModel,
  useReactTable,
  type VisibilityState,
} from '@tanstack/react-table'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'

import { usageLogSchema, type UsageLog } from '../../data/schema'
import {
  MOBILE_LOG_VIEW_MODES,
  MOBILE_LOG_VIEW_STORAGE_KEY,
  readMobileLogViewMode,
  writeMobileLogViewMode,
} from '../../lib/mobile-log-view'
import { useCommonLogsColumns } from '../columns/common-logs-columns'
import { UsageLogsMobileList } from '../usage-logs-mobile-card'
import { UsageLogsProvider, useUsageLogsContext } from '../usage-logs-provider'

const longName = 'enterprise-production-failover-2026-without-any-short-alias'
const log = usageLogSchema.parse({
  id: 1,
  user_id: 2,
  created_at: 1788840000,
  type: 2,
  content: '',
  model_name: longName,
  username: 'production-admin',
  channel: 372,
  channel_name: longName,
  token_name: 'backend-production-token',
  group: 'enterprise-production',
  quota: 123456789,
  use_time: 1.5,
  prompt_tokens: 1200,
  completion_tokens: 800,
  other: JSON.stringify({
    cache_tokens: 300,
    cache_creation_tokens_5m: 200,
    model_ratio: 1,
  }),
})

function Fixture(props: {
  admin?: boolean
  visibility?: VisibilityState
  logs?: UsageLog[]
  loading?: boolean
}) {
  const columns = useCommonLogsColumns(props.admin ?? true, false)
  const context = useUsageLogsContext()
  const table = useReactTable({
    data: props.logs ?? [log],
    columns,
    getCoreRowModel: getCoreRowModel(),
    state: { columnVisibility: props.visibility ?? {} },
  })
  return (
    <>
      <button type='button' onClick={() => context.setSensitiveVisible(false)}>
        Hide sensitive data
      </button>
      <UsageLogsMobileList
        table={table}
        logCategory='common'
        isLoading={props.loading}
      />
    </>
  )
}

function renderWithViewMode(
  props: Parameters<typeof Fixture>[0],
  viewMode?: (typeof MOBILE_LOG_VIEW_MODES)[keyof typeof MOBILE_LOG_VIEW_MODES]
) {
  if (viewMode) {
    window.localStorage.setItem(MOBILE_LOG_VIEW_STORAGE_KEY, viewMode)
  }
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <UsageLogsProvider>
        <Fixture {...props} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
}

// The cases below describe the expanded card, which the list now renders only
// in card view. Compact-view behaviour has its own cases further down.
function renderLogs(props: Parameters<typeof Fixture>[0] = {}) {
  return renderWithViewMode(props, MOBILE_LOG_VIEW_MODES.CARD)
}

// No stored preference: the list must fall back to its compact default.
function renderCompactLogs(props: Parameters<typeof Fixture>[0] = {}) {
  return renderWithViewMode(props)
}

beforeEach(() => {
  window.localStorage.clear()
})

it('shows model mismatch evidence when tapping the mobile model badge', async () => {
  const user = userEvent.setup()
  renderLogs({
    logs: [
      {
        ...log,
        other: JSON.stringify({
          response_model: {
            requested_model: longName,
            upstream_model: 'mapped-model',
            returned_model: 'unexpected-model',
          },
        }),
      },
    ],
  })
  await user.click(
    screen.getByRole('button', {
      name: `Model: ${longName}, Response model: unexpected-model`,
    })
  )
  const dialog = await screen.findByRole('dialog', { name: 'Model' })
  expect(
    within(dialog).getByText('Response model: unexpected-model')
  ).toBeVisible()
  expect(within(dialog).getByText('mapped-model')).toBeVisible()
  expect(within(dialog).getByText('unexpected-model')).toBeVisible()
})

it('opens long channel text on tap and copies the complete value', async () => {
  const user = userEvent.setup()
  const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  renderLogs()
  await user.click(
    screen.getByRole('button', { name: `Channel: ${longName} #372` })
  )
  const dialog = await screen.findByRole('dialog', { name: 'Channel' })
  expect(within(dialog).getByText(`${longName} #372`)).toHaveClass(
    '[overflow-wrap:anywhere]'
  )
  await user.click(
    within(dialog).getByRole('button', { name: 'Copy to clipboard' })
  )
  expect(copy).toHaveBeenCalledWith(`${longName} #372`)
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('button', { name: `Channel: ${longName} #372` })
  ).toHaveFocus()
})

it.each([false, true])(
  'copies the full mobile model name with the keyboard without opening details when there is no mapping or difference (response observed: %s)',
  async (observed) => {
    const user = userEvent.setup()
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    renderLogs({
      logs: [
        {
          ...log,
          other: observed
            ? JSON.stringify({
                response_model: {
                  requested_model: longName,
                  upstream_model: longName,
                  returned_model: longName,
                },
              })
            : log.other,
        },
      ],
    })
    const button = screen.getByRole('button', { name: `Model: ${longName}` })
    const modelText = within(button).getByText(longName)
    expect(modelText).toHaveClass(
      'line-clamp-2',
      'leading-5',
      '[overflow-wrap:anywhere]'
    )
    expect(modelText.closest('[data-slot="status-badge"]')).toHaveClass(
      'max-w-full',
      'min-h-6',
      'py-px'
    )
    button.focus()
    await user.keyboard('{Enter}')
    expect(copy).toHaveBeenCalledWith(longName)
    expect(
      screen.queryByRole('dialog', { name: 'Model' })
    ).not.toBeInTheDocument()
  }
)

it('hides sensitive names and disables full-text inspection when privacy is enabled', async () => {
  const user = userEvent.setup()
  renderLogs()
  await user.click(screen.getByRole('button', { name: 'Hide sensitive data' }))
  expect(screen.queryByText('production-admin')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /Channel:/ })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /Token:/ })
  ).not.toBeInTheDocument()
})

it('respects hidden columns and omits admin fields in the self view', () => {
  renderLogs({
    admin: false,
    visibility: { token_name: false, model_name: false },
  })
  expect(
    screen.queryByRole('button', {
      name: /Channel:|User:|Token:|Group:|Model:/,
    })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('enterprise-production')).not.toBeInTheDocument()
})

it('keeps input, output and cache quantities readable without empty metric cells', () => {
  renderLogs()
  expect(screen.getByText('Input')).toBeVisible()
  expect(screen.getByText('Output')).toBeVisible()
  expect(screen.getByText(/300/)).toBeVisible()
  expect(screen.getByText('Cache ↑ 200')).toBeVisible()
})

it('shows the established empty state when no logs exist', () => {
  renderLogs({ logs: [] })
  expect(screen.getByText('No Logs Found')).toBeVisible()
})

it.each([false, true])(
  'keeps timing in the right column for streaming=%s',
  (streaming) => {
    renderLogs({
      logs: [
        {
          ...log,
          is_stream: streaming,
          use_time: 58,
          other: JSON.stringify({ frt: 58000 }),
        },
      ],
    })
    const row = screen
      .getByRole('button', { name: /^Time:/ })
      .closest('[data-slot="log-time-and-timing"]')
    expect(row).toHaveClass('grid', 'grid-cols-2')
    expect(
      screen.getByRole('button', { name: /^Time:/ }).parentElement
    ).toHaveClass('flex-col', 'justify-between')
    expect(
      within(row as HTMLElement)
        .getByText('Duration')
        .closest('.col-start-2')
    ).not.toBeNull()
    if (streaming) {
      expect(within(row as HTMLElement).getByText('First token')).toBeVisible()
    }
  }
)

it('retains the user avatar and model badge in the mobile summary', () => {
  renderLogs()
  expect(screen.getByText('P')).toBeVisible()
  const modelButton = screen.getByRole('button', { name: `Model: ${longName}` })
  expect(modelButton.querySelector('[data-slot="status-badge"]')).not.toBeNull()
})

it('omits unused token and throughput placeholders for async jobs', () => {
  renderLogs({
    logs: [
      {
        ...log,
        prompt_tokens: 0,
        completion_tokens: 0,
        other: JSON.stringify({ is_task: true }),
      },
    ],
  })
  expect(screen.getByText('Async')).toBeVisible()
  expect(screen.queryByText('Input')).not.toBeInTheDocument()
  const timing = screen
    .getByRole('button', { name: /^Time:/ })
    .closest('[data-slot="log-time-and-timing"]')
  expect(within(timing as HTMLElement).queryByText('—')).not.toBeInTheDocument()
})

it('labels a task whose result was returned in the response as synchronous', () => {
  renderLogs({
    logs: [
      {
        ...log,
        prompt_tokens: 0,
        completion_tokens: 0,
        other: JSON.stringify({ is_task: true, task_sync: true }),
      },
    ],
  })
  expect(screen.getByText('Sync')).toBeVisible()
  expect(screen.queryByText('Async')).not.toBeInTheDocument()
})

it('shows mapped model names in full when inspecting a mobile model badge', async () => {
  const user = userEvent.setup()
  renderLogs({
    logs: [
      {
        ...log,
        other: JSON.stringify({
          is_model_mapped: true,
          upstream_model_name:
            'provider-production-mapped-model-with-a-long-name',
        }),
      },
    ],
  })
  await user.click(screen.getByRole('button', { name: `Model: ${longName}` }))
  const dialog = await screen.findByRole('dialog', { name: 'Model' })
  expect(within(dialog).getByText('Actual Model')).toBeVisible()
  expect(
    within(dialog).getByText(
      'provider-production-mapped-model-with-a-long-name'
    )
  ).toBeVisible()
})

it('shows loading placeholders without displaying stale log fields', () => {
  renderLogs({ loading: true })
  expect(screen.getByRole('status', { name: 'Loading' })).toHaveAttribute(
    'aria-busy',
    'true'
  )
  expect(
    screen.queryByRole('button', { name: /^Model:/ })
  ).not.toBeInTheDocument()
})

it('summarises each log in a collapsed compact row by default', () => {
  renderCompactLogs()

  const row = screen.getByRole('button', { expanded: false })
  expect(row).toHaveTextContent(longName)
  expect(row).toHaveTextContent('↑1.2K ↓800 · 1.5s')
  expect(row).toHaveTextContent('backend-production-token')
  expect(row).toHaveTextContent('production-admin')

  // Card-only detail stays collapsed until the row is expanded.
  expect(
    screen.queryByRole('button', { name: /Channel:/ })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Input')).not.toBeInTheDocument()
})

it('expands a compact row into the full card', async () => {
  const user = userEvent.setup()
  renderCompactLogs()

  await user.click(screen.getByRole('button', { expanded: false }))

  expect(screen.getByRole('button', { expanded: true })).toBeVisible()
  expect(
    screen.getByRole('button', { name: `Channel: ${longName} #372` })
  ).toBeVisible()
  expect(screen.getByText('Input')).toBeVisible()
})

it('switches to card view from the toggle and remembers the choice', async () => {
  const user = userEvent.setup()
  renderCompactLogs()

  await user.click(screen.getByRole('button', { name: 'Card view' }))

  expect(
    screen.getByRole('button', { name: `Model: ${longName}` })
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { expanded: false })
  ).not.toBeInTheDocument()
  expect(window.localStorage.getItem(MOBILE_LOG_VIEW_STORAGE_KEY)).toBe(
    MOBILE_LOG_VIEW_MODES.CARD
  )

  await user.click(screen.getByRole('button', { name: 'Compact view' }))
  expect(screen.getByRole('button', { expanded: false })).toBeVisible()
  expect(window.localStorage.getItem(MOBILE_LOG_VIEW_STORAGE_KEY)).toBe(
    MOBILE_LOG_VIEW_MODES.COMPACT
  )
})

it('summarises an operation log with its content instead of token metrics', () => {
  renderCompactLogs({
    logs: [
      {
        ...log,
        type: 1,
        content: 'Top-up granted by an administrator',
        prompt_tokens: 0,
        completion_tokens: 0,
      },
    ],
  })

  const row = screen.getByRole('button', { expanded: false })
  expect(row).toHaveTextContent('Top-up granted by an administrator')
  expect(row).not.toHaveTextContent('↑')
})

it('reads the stored mobile list preference and ignores unknown values', () => {
  expect(readMobileLogViewMode()).toBe(MOBILE_LOG_VIEW_MODES.COMPACT)

  writeMobileLogViewMode(MOBILE_LOG_VIEW_MODES.CARD)
  expect(readMobileLogViewMode()).toBe(MOBILE_LOG_VIEW_MODES.CARD)

  window.localStorage.setItem(MOBILE_LOG_VIEW_STORAGE_KEY, 'sideways')
  expect(readMobileLogViewMode()).toBe(MOBILE_LOG_VIEW_MODES.COMPACT)

  const throwingStorage = {
    getItem: () => {
      throw new Error('blocked')
    },
    setItem: () => {
      throw new Error('blocked')
    },
  }
  expect(readMobileLogViewMode(throwingStorage)).toBe(
    MOBILE_LOG_VIEW_MODES.COMPACT
  )
  expect(() =>
    writeMobileLogViewMode(MOBILE_LOG_VIEW_MODES.CARD, throwingStorage)
  ).not.toThrow()
})
