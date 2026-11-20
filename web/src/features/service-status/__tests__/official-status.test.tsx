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
import i18next, { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'

import { OfficialServiceStatus } from '..'

let client: QueryClient
const originalAdapter = api.defaults.adapter

afterEach(() => {
  cleanup()
  client?.clear()
  api.defaults.adapter = originalAdapter
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
  id: 'vendor',
  name: 'Example vendor',
  source_url: 'https://vendor.example/status',
  status: 'operational',
  source_updated_at: '',
  fetched_at: 1795000000000,
  checked_at: 1795000000000,
  stale: false,
  unavailable: false,
  components: [{ id: 'api', name: 'API', status: 'operational' }],
  incidents: [],
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

  expect(await screen.findByText('Example vendor')).toBeVisible()
  expect(screen.queryByText('canceled')).not.toBeInTheDocument()
})

it('renders attribution, fetched time, official link and component state', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [fixture] },
  })
  renderStatus()
  expect(await screen.findByText('Example vendor')).toBeVisible()
  expect(
    screen.getByText(
      'Data comes from official vendor status pages and may be delayed. This page does not indicate the availability of this site.'
    )
  ).toBeVisible()
  expect(screen.getByRole('link', { name: 'Official source' })).toHaveAttribute(
    'href',
    fixture.source_url
  )
  expect(screen.getByText('Last successful fetch')).toBeVisible()
  expect(screen.getByText('API')).toBeVisible()
  expect(screen.getAllByText('Operational')).toHaveLength(2)
  expect(screen.getByText('Unknown')).toBeVisible()
})

it('marks cached stale data as unavailable while retaining last known components', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [{ ...fixture, stale: true, unavailable: true }],
    },
  })
  renderStatus()
  expect(await screen.findByText('Unavailable')).toBeVisible()
  expect(screen.getByRole('status')).toHaveTextContent(
    'Showing the last successful update'
  )
  expect(screen.getByText('API')).toBeVisible()
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
  expect(await screen.findByText('Example vendor')).toBeVisible()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
  )
  expect(screen.queryByText('Feed unavailable')).not.toBeInTheDocument()
})

it('shows empty data separately from a server failure and handles missing timestamps', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  renderStatus()
  expect(await screen.findByText('No Data')).toBeVisible()
  expect(screen.queryByText('Unavailable')).not.toBeInTheDocument()
})

it.each([true, false])(
  'keeps same-name source entries distinct across refreshes (source IDs: %s)',
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
    expect(await screen.findAllByText('API')).toHaveLength(2)
    expect(screen.getAllByText('API delays')).toHaveLength(2)
    expect(log).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.getAllByText('API')).toHaveLength(1))
    expect(screen.getAllByText('API delays')).toHaveLength(1)
    expect(screen.getByText('Partial outage')).toBeVisible()
    expect(screen.getByText(/Monitoring/)).toBeVisible()
  }
)

it('updates timestamps for each interface language and safely handles an invalid language', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [fixture] },
  })
  const instance = createInstance()
  await instance.init({ lng: 'en', fallbackLng: false, resources: {} })
  renderStatus(instance)
  await screen.findByText('Example vendor')
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
      screen.getAllByText(formatted, { normalizer: (text) => text })
    ).toHaveLength(2)
  }
})
