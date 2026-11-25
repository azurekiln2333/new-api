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
  flexRender,
  getCoreRowModel,
  useReactTable,
  type VisibilityState,
} from '@tanstack/react-table'
import { act, render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, it, vi } from 'vitest'

import { formatNumber } from '@/lib/format'

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
  columnId?: 'prompt_tokens' | 'channel' | 'model_name'
}) {
  const columns = useCommonLogsColumns(props.admin ?? true, false)
  const context = useUsageLogsContext()
  const table = useReactTable({
    data: props.logs ?? [log],
    columns,
    getCoreRowModel: getCoreRowModel(),
    state: { columnVisibility: props.visibility ?? {} },
  })
  if (props.columnId) {
    const cell = table
      .getRowModel()
      .rows[0].getAllCells()
      .find((item) => item.column.id === props.columnId)
    if (!cell) throw new Error('The requested log column must exist')
    return flexRender(cell.column.columnDef.cell, cell.getContext())
  }
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

it('shows the processing node in compact and expanded cards without showing the submit node', async () => {
  const user = userEvent.setup()
  renderCompactLogs({
    logs: [
      {
        ...log,
        other: JSON.stringify({
          admin_info: {
            node_name: 'processor-us',
            submit_node_name: 'submit-hk',
          },
          root_info: { node_name: 'historical-node' },
        }),
      },
    ],
  })
  const compact = screen.getByRole('button', { expanded: false })
  expect(screen.getByText('processor-us')).toBeVisible()
  await user.click(compact)
  expect(
    screen.getByRole('button', { name: 'Processing Node: processor-us' })
  ).toBeVisible()
  expect(
    screen.queryByText(/submit-hk|historical-node/)
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Hide sensitive data' }))
  expect(screen.queryByText('processor-us')).not.toBeInTheDocument()
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

it('hovering a desktop channel keeps the channel name and retry chain readable', async () => {
  const user = userEvent.setup()
  renderLogs({
    columnId: 'channel',
    logs: [
      {
        ...log,
        channel: 38,
        channel_name: 'CNBNew',
        other: JSON.stringify({
          admin_info: { use_channel: [118, 82, 38] },
        }),
      },
    ],
  })

  await user.hover(screen.getByText('CNBNew'))

  const channel = await screen.findByText('CNBNew #38')
  const tooltip = channel.closest('[data-slot="tooltip-content"]')
  expect(tooltip).toHaveClass('bg-popover', 'text-popover-foreground')
  expect(channel).toBeVisible()
  expect(
    within(tooltip as HTMLElement).getByText('Chain: 118 → 82 → 38')
  ).toBeVisible()
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

it.each([MOBILE_LOG_VIEW_MODES.CARD, MOBILE_LOG_VIEW_MODES.COMPACT])(
  'hides sensitive names and full-text inspection in the %s view when privacy is enabled',
  async (viewMode) => {
    const user = userEvent.setup()
    renderWithViewMode({}, viewMode)
    await user.click(screen.getByRole('button', { name: 'Hide sensitive data' }))
    expect(screen.queryByText('production-admin')).not.toBeInTheDocument()
    expect(
      screen.queryByText('backend-production-token')
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /Channel:/ })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /Token:/ })
    ).not.toBeInTheDocument()
  }
)

it.each([MOBILE_LOG_VIEW_MODES.CARD, MOBILE_LOG_VIEW_MODES.COMPACT])(
  'respects hidden model and metric columns and omits admin fields in the %s self view',
  (viewMode) => {
    renderWithViewMode(
      {
        admin: false,
        visibility: {
          token_name: false,
          model_name: false,
          is_stream: false,
          prompt_tokens: false,
          use_time: false,
        },
      },
      viewMode
    )
    expect(
      screen.queryByRole('button', {
        name: /Channel:|User:|Token:|Group:|Model:/,
      })
    ).not.toBeInTheDocument()
    expect(screen.queryByText(longName)).not.toBeInTheDocument()
    expect(screen.queryByText('enterprise-production')).not.toBeInTheDocument()
    expect(screen.queryByText('production-admin')).not.toBeInTheDocument()
    expect(screen.queryByText(/t\/s/)).not.toBeInTheDocument()
    expect(screen.queryByText('Non-stream')).not.toBeInTheDocument()
    expect(screen.queryByText('Input')).not.toBeInTheDocument()
  }
)

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

it.each([false, true])(
  'prioritises the model badge, mode and measured throughput in cards for streaming=%s',
  (streaming) => {
    renderLogs({ logs: [{ ...log, is_stream: streaming }] })
    expect(screen.getByText('P')).toBeVisible()
    const modelButton = screen.getByRole('button', {
      name: `Model: ${longName}`,
    })
    expect(
      modelButton.querySelector('[data-slot="status-badge"]')
    ).not.toBeNull()
    const primary = modelButton.closest('[data-slot="mobile-log-primary"]')
    expect(primary).not.toBeNull()
    expect(
      within(primary as HTMLElement).getByText(
        streaming ? 'Stream' : 'Non-stream'
      )
    ).toBeVisible()
    expect(within(primary as HTMLElement).getByText('533 t/s')).toBeVisible()
    expect(within(primary as HTMLElement).queryByText('Cost')).toBeNull()
    expect(within(primary as HTMLElement).queryByText('Input')).toBeNull()
    expect(
      within(primary as HTMLElement).queryByRole('button', { name: /^Time:/ })
    ).toBeNull()
  }
)

it.each([
  { columnId: 'channel' as const, value: '-' },
  { columnId: 'model_name' as const, value: '-' },
])(
  'shows an absent $columnId in a rejected desktop log',
  ({ columnId, value }) => {
    renderLogs({
      columnId,
      logs: [{ ...log, type: 5, channel: 0, channel_name: '', model_name: '' }],
    })
    expect(screen.getByText(value)).toBeVisible()
    expect(screen.queryByText('#0')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  }
)

it.each([
  { columnId: 'channel' as const, value: '#372' },
  { columnId: 'model_name' as const, value: longName },
])(
  'preserves a recorded $columnId in a rejected desktop log',
  ({ columnId, value }) => {
    renderLogs({ columnId, logs: [{ ...log, type: 5 }] })
    expect(screen.getByText(value)).toBeVisible()
  }
)

it('shows absent routing fields in rejected mobile cards without a channel action', () => {
  renderLogs({
    logs: [{ ...log, type: 5, channel: 0, channel_name: '', model_name: '' }],
  })
  expect(screen.getByLabelText('Model: -')).toBeVisible()
  const channel = screen.getByText('Channel').parentElement
  expect(within(channel as HTMLElement).getByText('-')).toBeVisible()
  expect(screen.queryByText('#0')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /^Channel:/ })
  ).not.toBeInTheDocument()
})

it('keeps an absent model readable when a rejected compact row is expanded', async () => {
  renderCompactLogs({
    logs: [{ ...log, type: 5, channel: 0, channel_name: '', model_name: '' }],
  })
  const row = screen.getByRole('button', { expanded: false })
  expect(within(row).getByText('-')).toBeVisible()
  await userEvent.setup().click(row)
  expect(screen.getByLabelText('Model: -')).toBeVisible()
  expect(screen.queryByText('#0')).not.toBeInTheDocument()
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

it.each([false, true])(
  'leads a collapsed row with a wrapping model, mode and measured throughput for streaming=%s',
  (streaming) => {
    renderCompactLogs({ logs: [{ ...log, is_stream: streaming }] })
    const row = screen.getByRole('button', { expanded: false })
    const model = within(row).getByText(longName)
    expect(model).toHaveClass('[overflow-wrap:anywhere]')
    expect(model).not.toHaveClass('truncate')
    const primary = row.closest('[data-slot="mobile-log-primary"]')
    expect(primary).not.toBeNull()
    const stream = within(primary as HTMLElement).getByText(
      streaming ? 'Stream' : 'Non-stream'
    )
    expect(stream).toBeVisible()
    expect(row).not.toContainElement(stream)
    expect(within(primary as HTMLElement).getByText('533 t/s')).toBeVisible()
    expect(within(primary as HTMLElement).queryByText('Input')).toBeNull()
    expect(within(primary as HTMLElement).queryByText('Cost')).toBeNull()
    expect(screen.getByText('Input')).toBeVisible()
    expect(screen.getByText('1,200')).toBeVisible()
    expect(screen.getByText('Output')).toBeVisible()
    expect(screen.getByText('800')).toBeVisible()
    expect(screen.getByText('backend-production-token')).toBeVisible()
    expect(screen.getByText('production-admin')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /Channel:/ })
    ).not.toBeInTheDocument()
  }
)

it.each([
  { use_time: 0, completion_tokens: 800 },
  { use_time: 1.5, completion_tokens: 0 },
])(
  'does not invent compact throughput from missing measurements: %j',
  (metrics) => {
    renderCompactLogs({ logs: [{ ...log, ...metrics, is_stream: true }] })
    const primary = screen
      .getByRole('button', { expanded: false })
      .closest('[data-slot="mobile-log-primary"]')
    expect(primary).not.toBeNull()
    expect(within(primary as HTMLElement).getByText('Stream')).toBeVisible()
    expect(within(primary as HTMLElement).queryByText(/t\/s|—/)).toBeNull()
  }
)

it.each([false, true])(
  'keeps task mode clear without fake TPS in compact rows for synchronous=%s',
  (synchronous) => {
    renderCompactLogs({
      logs: [
        {
          ...log,
          prompt_tokens: 0,
          completion_tokens: 0,
          other: JSON.stringify({ is_task: true, task_sync: synchronous }),
        },
      ],
    })
    const primary = screen
      .getByRole('button', { expanded: false })
      .closest('[data-slot="mobile-log-primary"]')
    expect(primary).not.toBeNull()
    expect(
      within(primary as HTMLElement).getByText(synchronous ? 'Sync' : 'Async')
    ).toBeVisible()
    expect(within(primary as HTMLElement).queryByText(/t\/s|—/)).toBeNull()
    expect(screen.queryByText('Input')).not.toBeInTheDocument()
  }
)

it('retains stream failure evidence outside the collapsed expand button', async () => {
  const user = userEvent.setup()
  renderCompactLogs({
    logs: [
      {
        ...log,
        is_stream: true,
        other: JSON.stringify({
          stream_status: {
            status: 'error',
            end_reason: 'upstream_closed',
            error_count: 2,
          },
        }),
      },
    ],
  })
  const expand = screen.getByRole('button', { expanded: false })
  const primary = expand.closest('[data-slot="mobile-log-primary"]')
  expect(primary).not.toBeNull()
  const indicator = primary?.querySelector('[data-slot="tooltip-trigger"]')
  expect(indicator).not.toBeNull()
  expect(expand).not.toContainElement(indicator as HTMLElement)
  await user.hover(indicator as HTMLElement)
  expect(await screen.findByText('Stream Status: Error')).toBeVisible()
  expect(screen.getByText('upstream_closed')).toBeVisible()
  expect(screen.getByText('Soft Errors: 2')).toBeVisible()
  expect(expand).toHaveAttribute('aria-expanded', 'false')
})

it('expands a compact row into the full card using the keyboard', async () => {
  const user = userEvent.setup()
  renderCompactLogs()

  screen.getByRole('button', { expanded: false }).focus()
  await user.keyboard('{Enter}')

  expect(screen.getByRole('button', { expanded: true })).toBeVisible()
  const channel = screen.getByRole('button', {
    name: `Channel: ${longName} #372`,
  })
  expect(channel).toBeVisible()
  const card = channel.closest('[data-slot="mobile-log-card"]')
  expect(card).not.toBeNull()
  expect(within(card as HTMLElement).getByText('Input')).toBeVisible()
})

it('keeps large compact token counts exact and separated by readable labels', () => {
  renderCompactLogs({
    logs: [{ ...log, prompt_tokens: 318574217, completion_tokens: 0 }],
  })
  const input = screen.getByText('318,574,217')
  expect(input).toBeVisible()
  expect(input).toHaveClass('[overflow-wrap:anywhere]')
  expect(input).not.toHaveClass('truncate')
  expect(input).not.toHaveClass('whitespace-nowrap')
  expect(input.closest('dl')).toHaveClass('grid-cols-2')
  expect(screen.getByText('Output')).toBeVisible()
  expect(screen.getByText('0')).toBeVisible()
  expect(screen.queryByText(/318\.57M|↑318/)).not.toBeInTheDocument()
})

it('updates exact compact token counts with each interface language and invalid-locale fallback', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    fallbackLng: false,
    resources: {},
    initAsync: false,
  })
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <UsageLogsProvider>
          <Fixture logs={[{ ...log, prompt_tokens: 41258 }]} />
        </UsageLogsProvider>
      </QueryClientProvider>
    </I18nextProvider>
  )
  for (const [language, value] of [
    ['en', '41,258'],
    ['zhCN', '41,258'],
    ['zhTW', '41,258'],
    ['fr', '41\u202f258'],
    ['ru', '41\u00a0258'],
    ['ja', '41,258'],
    ['vi', '41.258'],
    ['invalid_locale', formatNumber(41258)],
  ]) {
    await act(() => i18n.changeLanguage(language))
    const input = screen.getByText('Input').parentElement
    expect(input?.querySelector('dd')?.textContent).toBe(value)
  }
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

  expect(screen.getByText('Top-up granted by an administrator')).toBeVisible()
  expect(screen.queryByText('Input')).not.toBeInTheDocument()
  expect(screen.queryByText(/t\/s/)).not.toBeInTheDocument()
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

it('keeps desktop input, output and cache counts plain without widening the token cell', () => {
  renderLogs({
    columnId: 'prompt_tokens',
    logs: [
      {
        ...log,
        prompt_tokens: 41258,
        completion_tokens: 115,
        other: JSON.stringify({ cache_tokens: 41088 }),
      },
    ],
  })
  const tokens = screen.getByText('41,258 / 115')
  expect(tokens).toBeVisible()
  expect(tokens.parentElement).toHaveClass('whitespace-nowrap')
  expect(screen.getByText('Cache↓ 41,088')).toBeVisible()
  expect(screen.queryByText(/41\.3K|41\.1K|万/)).not.toBeInTheDocument()
})
