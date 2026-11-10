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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import type { ReactNode } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { CacheTokenDetails } from '../components/cache-token-details'
import { MarketShareSection } from '../components/market-share-section'
import { ModelLeaderboard } from '../components/model-leaderboard'
import { ModelsSection } from '../components/models-section'

// Canvas rendering is a browser boundary; totals and rows use real components.
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: vi.fn() },
}))

afterEach(cleanup)

async function renderRankings(children: ReactNode) {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
  })
  const route = createRootRoute({
    component: () => <I18nextProvider i18n={i18n}>{children}</I18nextProvider>,
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
  return i18n
}

test('model rows show separate cache reads and writes without adding them to total again', async () => {
  const row = {
    rank: 1,
    model_name: 'gpt-test',
    vendor: 'OpenAI',
    category: 'all' as const,
    total_tokens: 500,
    cache_read_tokens: 300,
    cache_creation_tokens: 50,
    share: 1,
    growth_pct: 0,
  }
  await renderRankings(<ModelLeaderboard rows={[row]} />)
  const model = await screen.findByRole('listitem')
  expect(within(model).getByText('500')).toBeInTheDocument()
  expect(within(model).getByText('Cache Read')).toBeInTheDocument()
  expect(within(model).getByText('300')).toBeInTheDocument()
  expect(within(model).getByText('Cache Write')).toBeInTheDocument()
  expect(within(model).getByText('50')).toBeInTheDocument()
})

test('ranking header uses all-model totals even when the returned leaderboard is empty', async () => {
  const totals = {
    total_tokens: 512,
    cache_read_tokens: 300,
    cache_creation_tokens: 50,
  }
  await renderRankings(
    <ModelsSection
      rows={[]}
      history={{ points: [], models: [], buckets: 0 }}
      period='today'
      totals={totals}
    />
  )
  expect(await screen.findByText('512')).toBeInTheDocument()
  expect(screen.getByText('300')).toBeInTheDocument()
  expect(screen.getByText('50')).toBeInTheDocument()
})

test('vendor rows show aggregated cache quantities', async () => {
  await renderRankings(
    <MarketShareSection
      rows={[
        {
          rank: 1,
          vendor: 'OpenAI',
          total_tokens: 500,
          cache_read_tokens: 300,
          cache_creation_tokens: 50,
          share: 1,
          growth_pct: 0,
          models_count: 2,
          top_model: 'gpt-test',
        },
      ]}
      history={{ points: [], vendors: [], buckets: 0 }}
      period='today'
    />
  )
  const vendor = await screen.findByRole('listitem')
  expect(within(vendor).getByText('Cache Read')).toBeInTheDocument()
  expect(within(vendor).getByText('300')).toBeInTheDocument()
  expect(within(vendor).getByText('50')).toBeInTheDocument()
})

test('empty cache quantities remain visible as zero', async () => {
  await renderRankings(
    <CacheTokenDetails cache_read_tokens={0} cache_creation_tokens={0} />
  )
  expect(await screen.findAllByText('0')).toHaveLength(2)
})

test('cache details update number formatting when interface language changes', async () => {
  const i18n = await renderRankings(
    <CacheTokenDetails
      cache_read_tokens={201344}
      cache_creation_tokens={1000}
    />
  )
  await act(() => i18n.changeLanguage('zhCN'))
  expect(screen.getByText('20.1万')).toBeInTheDocument()
  await act(() => i18n.changeLanguage('zhTW'))
  expect(screen.getByText('20.1萬')).toBeInTheDocument()
})
