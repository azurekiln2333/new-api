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
import i18next, { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'

import { OfficialServiceStatus } from '..'
import { OFFICIAL_STATUS_PROVIDERS } from '../api'

let client: QueryClient
const originalAdapter = api.defaults.adapter
const now = 1795000000000

beforeEach(() => {
  vi.spyOn(Date, 'now').mockReturnValue(now)
})

afterEach(() => {
  cleanup()
  client?.clear()
  api.defaults.adapter = originalAdapter
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function renderStatus(instance = i18next, reactStrictMode = false) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={instance}>
        <OfficialServiceStatus />
      </I18nextProvider>
    </QueryClientProvider>,
    { reactStrictMode }
  )
}

const fixture = {
  id: 'openai',
  name: 'OpenAI',
  source_url: 'https://status.openai.com/',
  status: 'operational',
  source_updated_at: '',
  fetched_at: now,
  checked_at: now,
  stale: false,
  unavailable: false,
  fetch_error: '',
  components: [{ id: 'api', name: 'API', status: 'operational' }],
  incidents: [],
  recent_incidents: [],
  history_fetched_at: now,
  history_checked_at: now,
  history_stale: false,
  history_unavailable: false,
  history_error: '',
}

it('loads official status on the first StrictMode mount after query cancellation', async () => {
  api.defaults.adapter = async (config) => ({
    data: { success: true, data: [fixture] },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  renderStatus(i18next, true)
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  expect(await card.findByText('Operational')).toBeVisible()
  expect(screen.queryByText('canceled')).not.toBeInTheDocument()
})

it('keeps all eight official cards while loading, missing sources and failed requests', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('Feed unavailable'))
  renderStatus()
  expect(screen.getAllByRole('article')).toHaveLength(8)
  await screen.findByText('Feed unavailable')
  for (const provider of OFFICIAL_STATUS_PROVIDERS) {
    const card = within(screen.getByRole('article', { name: provider.name }))
    expect(card.getByRole('link', { name: 'Official source' })).toHaveAttribute(
      'href',
      provider.source_url
    )
    expect(card.getByText('No successful fetch yet.')).toBeVisible()
    expect(card.queryByText('Operational')).not.toBeInTheDocument()
  }
  expect(screen.queryByText(/Vertex/)).not.toBeInTheDocument()
})

it('renders attribution, source times and keyboard component disclosure without changing card height', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [fixture] },
  })
  renderStatus()
  const element = screen.getByRole('article', { name: 'OpenAI' })
  const card = within(element)
  expect(await card.findByText('Operational')).toBeVisible()
  expect(
    screen.getByText(
      'Data comes from official vendor status pages and may be delayed. This page does not indicate the availability of this site.'
    )
  ).toBeVisible()
  expect(card.getByText('Last successful fetch')).toBeVisible()
  expect(card.getByText('Source updated')).toBeVisible()
  expect(card.queryByText('API')).not.toBeInTheDocument()
  expect(element).toHaveClass('h-[46rem]', 'sm:h-[42rem]', 'overflow-hidden')
  const disclosure = card.getByRole('button', { name: /Components/ })
  expect(disclosure).toHaveAttribute('aria-expanded', 'false')
  disclosure.focus()
  await userEvent.keyboard('{Enter}')
  expect(disclosure).toHaveAttribute('aria-expanded', 'true')
  const list = card.getByRole('region', { name: 'Components' })
  expect(within(list).getByText('API')).toBeVisible()
  expect(list).toHaveClass('h-40')
  expect(list.querySelector('[data-slot="scroll-area-viewport"]')).toHaveClass(
    'size-full'
  )
  expect(card.getAllByText('Operational')).toHaveLength(2)
  expect(element).toHaveClass('h-[46rem]', 'sm:h-[42rem]')
  await userEvent.keyboard(' ')
  expect(disclosure).toHaveAttribute('aria-expanded', 'false')
  await waitFor(() => expect(card.queryByText('API')).not.toBeInTheDocument())
})

it('distinguishes failed checks from thirty-minute stale data and preserves last known components', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          ...fixture,
          unavailable: true,
          fetch_error: 'transport_error',
          fetched_at: now - 5 * 60_000,
          history_fetched_at: now - 31 * 60_000,
        },
      ],
    },
  })
  renderStatus()
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  expect(await card.findByText('Last known status')).toBeVisible()
  expect(
    card.getByText(
      'The connection to the official feed could not be completed.'
    )
  ).toBeVisible()
  const status = within(card.getByRole('region', { name: 'Status data' }))
  expect(status.getByText('Fresh')).toBeVisible()
  expect(status.queryByText('Stale')).not.toBeInTheDocument()
  expect(
    within(card.getByRole('region', { name: 'Incidents' })).getByText('Stale')
  ).toBeVisible()
  await userEvent.click(card.getByRole('button', { name: /Components/ }))
  expect(card.getByText('API')).toBeVisible()
})

it.each([
  { age: 0, label: 'Fresh' },
  { age: 30 * 60_000, label: 'Fresh' },
  { age: 30 * 60_000 + 1, label: 'Stale' },
])(
  'uses validated fetch age $age independently of source and server stale flags',
  async ({ age, label }) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: [
          {
            ...fixture,
            fetched_at: now - age,
            history_fetched_at: now - age,
            source_updated_at: '2020-01-01T00:00:00Z',
            stale: label === 'Fresh',
            history_stale: label === 'Fresh',
          },
        ],
      },
    })
    renderStatus()
    const card = within(screen.getByRole('article', { name: 'OpenAI' }))
    await card.findByText('Operational')
    const status = within(card.getByRole('region', { name: 'Status data' }))
    expect(status.getByText(label)).toBeVisible()
    expect(
      within(card.getByRole('region', { name: 'Incidents' })).getByText(label)
    ).toBeVisible()
  }
)

it('ages retained data after resuming a hidden tab without a successful refetch', async () => {
  let clock = now
  vi.mocked(Date.now).mockImplementation(() => clock)
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [fixture] },
  })
  renderStatus()
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  await card.findByText('Operational')
  clock += 31 * 60_000
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  expect(
    within(card.getByRole('region', { name: 'Status data' })).getByText('Stale')
  ).toBeVisible()
  expect(card.getByText('Last known status')).toBeVisible()
})

it('keeps never-successful DeepSeek unknown with safe failure information instead of an outage', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          ...fixture,
          id: 'deepseek',
          name: 'DeepSeek',
          source_url: 'https://status.deepseek.com/',
          status: 'unknown',
          fetched_at: 0,
          unavailable: true,
          fetch_error: 'tls_error',
          components: null,
          incidents: null,
          history_fetched_at: 0,
          history_unavailable: true,
          history_error: 'unverified_source',
        },
      ],
    },
  })
  renderStatus()
  const card = within(screen.getByRole('article', { name: 'DeepSeek' }))
  expect(
    await card.findByText('The secure connection to the official feed failed.')
  ).toBeVisible()
  expect(card.getByText('No successful fetch yet.')).toBeVisible()
  expect(card.queryByText('Last known status')).not.toBeInTheDocument()
  expect(card.queryByText('Operational')).not.toBeInTheDocument()
  expect(card.queryByText('Major outage')).not.toBeInTheDocument()
})

it('retains successful summary, active incidents and recent history after a history HTTP error', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          ...fixture,
          history_fetched_at: now - 5 * 60_000,
          history_unavailable: true,
          history_error: 'http_error',
          history_http_status: 403,
          incidents: [
            {
              id: 'active',
              name: 'API delays',
              status: 'monitoring',
              updated_at: '',
            },
          ],
          recent_incidents: [
            {
              id: 'recent',
              name: 'Earlier outage',
              status: 'resolved',
              updated_at: '',
            },
          ],
        },
      ],
    },
  })
  renderStatus()
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  expect(await card.findByText('Operational')).toBeVisible()
  const incidents = within(card.getByRole('region', { name: 'Incidents' }))
  expect(incidents.getByText('API delays')).toBeVisible()
  expect(incidents.getByText('Earlier outage')).toBeVisible()
  expect(incidents.getByText('Fresh')).toBeVisible()
  expect(incidents.getByText('Unavailable')).toBeVisible()
  expect(
    incidents.getByText('Recent incident data is unavailable.')
  ).toBeVisible()
  expect(
    incidents.queryByText('No additional recent incidents reported.')
  ).not.toBeInTheDocument()
  expect(incidents.getByText(/HTTP 403$/)).toBeVisible()
})

it.each(['toString', 'constructor', '__proto__'])(
  'uses safe labels for inherited object property %s in feed data',
  async (value) => {
    const timestamp = new Date(now).toISOString()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: [
          {
            ...fixture,
            status: value,
            source_updated_at: timestamp,
            fetch_error: value,
            fetch_http_status: 403,
            history_error: value,
            history_http_status: 999,
            components: [{ id: 'api', name: 'API', status: value }],
            incidents: [
              {
                id: 'active',
                name: 'Active incident',
                status: value,
                updated_at: timestamp,
              },
            ],
            recent_incidents: [
              {
                id: 'recent',
                name: 'Recent incident',
                status: value,
                updated_at: timestamp,
              },
            ],
          },
          { ...fixture, id: 'anthropic', name: 'Anthropic' },
        ],
      },
    })
    renderStatus()
    const healthyCard = within(
      screen.getByRole('article', { name: 'Anthropic' })
    )
    expect(await healthyCard.findByText('Operational')).toBeVisible()
    expect(screen.getAllByRole('article')).toHaveLength(8)
    const card = within(screen.getByRole('article', { name: 'OpenAI' }))
    expect(card.getByTitle('Unknown')).toBeVisible()
    expect(card.getByTitle('Unknown')).toHaveClass('text-muted-foreground')
    const status = within(card.getByRole('region', { name: 'Status data' }))
    expect(
      status.getByText('Official feed data is unavailable. · HTTP 403')
    ).toBeVisible()
    const incidents = within(card.getByRole('region', { name: 'Incidents' }))
    expect(
      incidents.getByText('Official feed data is unavailable.')
    ).toBeVisible()
    expect(incidents.queryByText(/HTTP 999/)).not.toBeInTheDocument()
    expect(incidents.getAllByRole('listitem')).toHaveLength(2)
    for (const incident of incidents.getAllByRole('listitem')) {
      expect(within(incident).getByText(/^Unknown ·$/)).toBeVisible()
    }
    await userEvent.click(card.getByRole('button', { name: /Components/ }))
    const components = within(card.getByRole('region', { name: 'Components' }))
    expect(components.getByText('API')).toBeVisible()
    expect(components.getByText('Unknown')).toBeVisible()
    expect(card.queryByText(value, { exact: false })).not.toBeInTheDocument()
    expect(card.getByRole('link', { name: 'Official source' })).toHaveAttribute(
      'href',
      'https://status.openai.com/'
    )
  }
)

it.each([now + 60_000, 0, -1])(
  'keeps invalid successful fetch time %s unknown and hides its status data',
  async (fetchedAt) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: [
          {
            ...fixture,
            fetched_at: fetchedAt,
            history_fetched_at: fetchedAt,
            incidents: [
              {
                id: 'active',
                name: 'Unverified active incident',
                status: 'monitoring',
                updated_at: '',
              },
            ],
            recent_incidents: [
              {
                id: 'recent',
                name: 'Unverified recent incident',
                status: 'resolved',
                updated_at: '',
              },
            ],
          },
        ],
      },
    })
    renderStatus()
    const card = within(screen.getByRole('article', { name: 'OpenAI' }))
    const timestamps = within(card.getByRole('region', { name: 'Status data' }))
    await waitFor(() => {
      expect(
        timestamps
          .getByText('Last checked')
          .nextElementSibling?.querySelector('time')
      ).toHaveAttribute('dateTime', new Date(now).toISOString())
    })
    expect(card.getByText('No successful fetch yet.')).toBeVisible()
    expect(
      timestamps.getByText('Last successful fetch').nextElementSibling
    ).toHaveTextContent(/^Unknown$/)
    const history = within(card.getByRole('region', { name: 'Incidents' }))
    expect(
      history.getByText('History last successful fetch').nextElementSibling
    ).toHaveTextContent(/^Unknown$/)
    expect(
      history.getByText('Active incident data is unavailable.')
    ).toBeVisible()
    expect(
      history.getByText('Recent incident data is unavailable.')
    ).toBeVisible()
    const disclosure = card.getByRole('button', { name: /Components/ })
    expect(disclosure).toHaveAttribute('aria-disabled', 'true')
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    const user = userEvent.setup()
    await user.click(disclosure)
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    disclosure.focus()
    expect(disclosure).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    await user.keyboard(' ')
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    expect(
      card.queryByRole('region', { name: 'Components' })
    ).not.toBeInTheDocument()
    expect(card.queryByText('Last known status')).not.toBeInTheDocument()
    expect(card.queryByText('Operational')).not.toBeInTheDocument()
    expect(card.queryByText('API')).not.toBeInTheDocument()
    expect(
      card.queryByText('Unverified active incident')
    ).not.toBeInTheDocument()
    expect(
      card.queryByText('Unverified recent incident')
    ).not.toBeInTheDocument()
    expect(card.queryByText('Fresh')).not.toBeInTheDocument()
    expect(card.queryByText('Stale')).not.toBeInTheDocument()
  }
)

it('accepts a successful fetch completed after the page mounted', async () => {
  let clock = now
  vi.mocked(Date.now).mockImplementation(() => clock)
  let finish: ((value: unknown) => void) | undefined
  vi.spyOn(api, 'get').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  renderStatus()
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  expect(card.getByText('No successful fetch yet.')).toBeVisible()
  clock += 1_000
  finish?.({
    data: {
      success: true,
      data: [{ ...fixture, fetched_at: clock, history_fetched_at: clock }],
    },
  })
  expect(await card.findByText('Operational')).toBeVisible()
  expect(
    within(card.getByRole('region', { name: 'Status data' })).getByText('Fresh')
  ).toBeVisible()
})

it('shows an active incident once when its ID also appears in recent history', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          ...fixture,
          incidents: [
            {
              id: 'active',
              name: 'Active API delays',
              status: 'monitoring',
              updated_at: '',
            },
          ],
          recent_incidents: [
            {
              id: 'active',
              name: 'Earlier active snapshot',
              status: 'investigating',
              updated_at: '',
            },
            {
              id: 'resolved',
              name: 'Earlier resolved incident',
              status: 'resolved',
              updated_at: '',
            },
          ],
        },
      ],
    },
  })
  renderStatus()
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  await card.findByText('Operational')
  const incidents = within(card.getByRole('region', { name: 'Incidents' }))
  expect(incidents.getAllByText('Active API delays')).toHaveLength(1)
  expect(
    incidents.queryByText('Earlier active snapshot')
  ).not.toBeInTheDocument()
  expect(incidents.getByText('Earlier resolved incident')).toBeVisible()
  expect(incidents.getAllByRole('listitem')).toHaveLength(2)
})

it('keeps refresh disabled while loading and recovers from a failed request', async () => {
  let finish: ((value: unknown) => void) | undefined
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('Feed unavailable'))
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
  renderStatus()
  expect(await screen.findByText('Feed unavailable')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
  finish?.({ data: { success: true, data: [fixture] } })
  expect(
    await within(screen.getByRole('article', { name: 'OpenAI' })).findByText(
      'Operational'
    )
  ).toBeVisible()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
  )
  expect(screen.queryByText('Feed unavailable')).not.toBeInTheDocument()
})

it('keeps unknown provider cards for empty data and exposes API business failures', async () => {
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: [] } })
    .mockResolvedValueOnce({
      data: { success: false, message: 'Status request denied' },
    })
  renderStatus()
  expect(await screen.findByText('No Data')).toBeVisible()
  expect(screen.getAllByRole('article')).toHaveLength(8)
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Status request denied')).toBeVisible()
})

it.each([true, false])(
  'keeps same-name source entries and disclosure distinct across refreshes (source IDs: %s)',
  async (hasIds) => {
    const components = [
      { id: hasIds ? 'primary' : '', name: 'API', status: 'operational' },
      { id: hasIds ? 'secondary' : '', name: 'API', status: 'partial_outage' },
    ]
    const incidents = [
      {
        id: hasIds ? 'first' : '',
        name: 'API delays',
        status: 'investigating',
        updated_at: '',
      },
      {
        id: hasIds ? 'second' : '',
        name: 'API delays',
        status: 'monitoring',
        updated_at: '',
      },
    ]
    vi.spyOn(api, 'get')
      .mockResolvedValueOnce({
        data: { success: true, data: [{ ...fixture, components, incidents }] },
      })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: [
            {
              ...fixture,
              components: components.slice(1),
              incidents: incidents.slice(1),
            },
          ],
        },
      })
    const log = vi.spyOn(console, 'error')
    renderStatus()
    const card = within(screen.getByRole('article', { name: 'OpenAI' }))
    await card.findByText('Operational')
    await userEvent.click(card.getByRole('button', { name: /Components/ }))
    expect(card.getAllByText('API')).toHaveLength(2)
    expect(card.getAllByText('API delays')).toHaveLength(2)
    expect(log).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(card.getAllByText('API')).toHaveLength(1))
    expect(card.getAllByText('API delays')).toHaveLength(1)
    expect(card.getByRole('button', { name: /Components/ })).toHaveAttribute(
      'aria-expanded',
      'true'
    )
    expect(card.getByText('Partial outage')).toBeVisible()
    expect(card.getByText(/Monitoring/)).toBeVisible()
  }
)

it('bounds long components and recent incidents inside the same fixed frame', async () => {
  const longName = 'Long official service name '.repeat(8)
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          ...fixture,
          components: [{ id: 'long', name: longName, status: 'operational' }],
          recent_incidents: [
            {
              id: 'resolved',
              name: 'Earlier resolved incident',
              status: 'resolved',
              updated_at: '',
            },
          ],
        },
      ],
    },
  })
  renderStatus()
  const element = screen.getByRole('article', { name: 'OpenAI' })
  const card = within(element)
  await card.findByText('Operational')
  await userEvent.click(card.getByRole('button', { name: /Components/ }))
  expect(card.getByText(longName.trim())).toHaveClass('break-words')
  expect(card.getByText('Earlier resolved incident')).toBeVisible()
  expect(card.getByRole('region', { name: 'Incidents' })).toHaveClass(
    'min-h-0',
    'overflow-hidden'
  )
  expect(element).toHaveClass('h-[46rem]', 'sm:h-[42rem]', 'min-w-0')
})

it('updates timestamps for every interface language and safely handles an invalid language', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [fixture] },
  })
  const instance = createInstance()
  await instance.init({ lng: 'en', fallbackLng: false, resources: {} })
  renderStatus(instance)
  const card = within(screen.getByRole('article', { name: 'OpenAI' }))
  await card.findByText('Operational')
  for (const [language, locale] of [
    ['zhCN', 'zh-CN'],
    ['zhTW', 'zh-TW'],
    ['en', 'en'],
    ['fr', 'fr'],
    ['ja', 'ja'],
    ['ru', 'ru'],
    ['vi', 'vi'],
    ['invalid_language', undefined],
  ]) {
    await act(async () => {
      await instance.changeLanguage(language)
    })
    const formatted = new Date(fixture.fetched_at).toLocaleString(
      toIntlLocale(locale)
    )
    expect(
      card.getAllByText(formatted, { normalizer: (text) => text })
    ).toHaveLength(4)
  }
})
