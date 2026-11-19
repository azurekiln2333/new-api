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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { NavGroup } from '@/components/layout/components/nav-group'
import { checkIsActive } from '@/components/layout/lib/url-utils'
import type { NavGroup as SidebarNavGroup } from '@/components/layout/types'
import { SidebarProvider } from '@/components/ui/sidebar'
import { invalidateModelPricing } from '@/features/model-pricing/api'
import {
  parseSidebarModulesAdmin,
  serializeSidebarModulesAdmin,
} from '@/features/system-settings/maintenance/config'
import {
  MODEL_PRICING_ISSUES_QUERY_KEY,
  type ModelPricingIssue,
} from '@/features/system-settings/models/pricing-issues'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarConfig } from '../use-sidebar-config'
import { useSidebarData } from '../use-sidebar-data'
import { useSidebarView } from '../use-sidebar-view'

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  useAuthStore.getState().auth.reset()
})

function sidebarFor(admin?: object, user?: object, canConfigure = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: admin ? JSON.stringify(admin) : '',
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: 1,
    permissions: { sidebar_settings: canConfigure },
    sidebar_modules: user ? JSON.stringify(user) : '',
  })
  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
  const result = renderHook(
    () => useSidebarConfig(useSidebarData().navGroups),
    { wrapper: Wrapper }
  )
  return result
}

function logItems(groups: SidebarNavGroup[]) {
  return (
    groups
      .find((group) => group.id === 'general')
      ?.items.find((item) => item.title === 'Logs')?.items ?? []
  )
}

describe('security sidebar visibility', () => {
  it('old configurations show Security & Access immediately after Profile and keep API Keys', () => {
    const { result } = sidebarFor(
      { personal: { enabled: true, personal: true, topup: true } },
      { personal: { enabled: true, personal: true } }
    )
    expect(
      result.current
        .find((group) => group.id === 'personal')
        ?.items.map((item) => item.title)
    ).toEqual(['Wallet', 'Profile', 'Security & Access'])
    expect(
      result.current
        .flatMap((group) => group.items)
        .some((item) => item.title === 'API Keys')
    ).toBe(true)
  })
  it.each([
    [{ personal: { enabled: true, security: false } }, undefined],
    [{ personal: { enabled: false } }, { personal: { security: true } }],
    [undefined, { personal: { enabled: true, security: false } }],
    [undefined, { personal: { enabled: false } }],
  ])(
    'admin or user disablement hides Security & Access (%j, %j)',
    (admin, user) => {
      const { result } = sidebarFor(admin, user)
      expect(
        result.current
          .flatMap((group) => group.items)
          .some((item) => item.title === 'Security & Access')
      ).toBe(false)
    }
  )
  it('users without sidebar configuration permission retain the admin view', () => {
    const { result } = sidebarFor(
      undefined,
      { personal: { security: false } },
      false
    )
    expect(
      result.current
        .flatMap((group) => group.items)
        .some((item) => item.title === 'Security & Access')
    ).toBe(true)
  })
})

describe('log sidebar menu', () => {
  it('admin settings default Audit Logs to visible and preserve its independent toggle when saved', () => {
    const config = parseSidebarModulesAdmin(
      '{"console":{"enabled":true,"log":true}}'
    )
    expect(config.console.audit).toBe(true)
    config.console.audit = false
    const saved = parseSidebarModulesAdmin(serializeSidebarModulesAdmin(config))
    const { result } = sidebarFor(saved)
    const titles = logItems(result.current).map((item) => item.title)
    expect(titles).not.toContain('Audit Logs')
    expect(titles).toContain('Usage Logs')
  })

  it('legacy configurations show four independent log links in one collapsible menu', () => {
    const { result } = sidebarFor(
      { console: { enabled: true, log: true } },
      { console: { enabled: true, log: true } }
    )
    expect(logItems(result.current)).toMatchObject([
      { title: 'Usage Logs', url: '/usage-logs/common' },
      { title: 'Audit Logs', url: '/usage-logs/audit' },
      { title: 'Drawing Logs', url: '/usage-logs/drawing' },
      { title: 'Task Logs', url: '/usage-logs/task' },
    ])
    expect(
      result.current
        .flatMap((group) => group.items)
        .filter((item) => item.url?.startsWith('/usage-logs'))
    ).toHaveLength(0)
    const selected = logItems(result.current).filter((item) =>
      checkIsActive('/usage-logs/drawing?model=test', item)
    )
    expect(selected.map((item) => item.title)).toEqual(['Drawing Logs'])
  })

  it.each([
    [{ console: { enabled: true, audit: false } }, undefined],
    [{ console: { enabled: false } }, { console: { audit: true } }],
    [undefined, { console: { enabled: true, audit: false } }],
    [undefined, { console: { enabled: false } }],
  ])(
    'admin and personal visibility rules can hide Audit Logs (%j, %j)',
    (admin, user) => {
      const { result } = sidebarFor(admin, user)
      expect(
        logItems(result.current).some((item) => item.title === 'Audit Logs')
      ).toBe(false)
    }
  )

  it('hiding Usage Logs does not hide the independently configured Audit Logs entry', () => {
    const { result } = sidebarFor({ console: { enabled: true, log: false } })
    const titles = logItems(result.current).map((item) => item.title)
    expect(titles).not.toContain('Usage Logs')
    expect(titles).toContain('Audit Logs')
  })

  it.each([
    [{ console: { midjourney: false } }, undefined],
    [undefined, { console: { midjourney: false } }],
  ])(
    'hides only Drawing Logs when its module is disabled (%j, %j)',
    (admin, user) => {
      const { result } = sidebarFor(admin, user)
      expect(logItems(result.current).map((item) => item.title)).toEqual([
        'Usage Logs',
        'Audit Logs',
        'Task Logs',
      ])
    }
  )

  it('retains Drawing Logs when the other three modules are disabled', () => {
    const { result } = sidebarFor({
      console: { log: false, audit: false, task: false },
    })
    expect(logItems(result.current)).toMatchObject([
      { title: 'Drawing Logs', url: '/usage-logs/drawing' },
    ])
  })

  it.each([
    [
      { console: { log: false, audit: false, midjourney: false, task: false } },
      undefined,
    ],
    [
      undefined,
      { console: { log: false, audit: false, midjourney: false, task: false } },
    ],
    [{ console: { enabled: false } }, undefined],
  ])('hides the empty Logs menu (%j, %j)', (admin, user) => {
    const { result } = sidebarFor(admin, user)
    expect(
      result.current
        .flatMap((group) => group.items)
        .some((item) => item.title === 'Logs')
    ).toBe(false)
  })
})

describe('log menu interaction', () => {
  function GeneralNavigation() {
    const { navGroups } = useSidebarView()
    const general = navGroups.find((group) => group.id === 'general')
    return general ? <NavGroup {...general} /> : null
  }

  async function renderLogMenu(role: number, collapsed = false) {
    vi.stubGlobal('scrollTo', vi.fn())
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(['status'], { SidebarModulesAdmin: '' })
    useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role })
    const root = createRootRoute({ component: GeneralNavigation })
    const route = createRoute({ getParentRoute: () => root, path: '$' })
    const router = createRouter({
      routeTree: root.addChildren([route]),
      history: createMemoryHistory({ initialEntries: ['/usage-logs/drawing'] }),
    })
    await router.load()
    const view = render(
      <QueryClientProvider client={client}>
        <SidebarProvider defaultOpen={!collapsed}>
          <RouterProvider router={router} />
        </SidebarProvider>
      </QueryClientProvider>
    )
    return {
      router,
      cleanup: () => {
        view.unmount()
        client.clear()
      },
    }
  }

  it.each([
    { role: ROLE.USER, width: 1024 },
    { role: ROLE.ADMIN, width: 1024 },
    { role: ROLE.USER, width: 390 },
  ])(
    'expands the active log menu and supports keyboard collapse/navigation (%j)',
    async ({ role, width }) => {
      vi.stubGlobal('innerWidth', width)
      const user = userEvent.setup()
      const view = await renderLogMenu(role)
      try {
        const trigger = screen.getByRole('button', { name: 'Logs' })
        expect(trigger).toHaveAttribute('aria-expanded', 'true')
        expect(
          screen.getByRole('link', { name: 'Drawing Logs' })
        ).toHaveAttribute('data-active')
        expect(
          screen.getByRole('link', { name: 'Task Logs' })
        ).not.toHaveAttribute('data-active')
        trigger.focus()
        await user.keyboard('{Enter}')
        expect(trigger).toHaveAttribute('aria-expanded', 'false')
        await user.keyboard('{Enter}')
        expect(trigger).toHaveAttribute('aria-expanded', 'true')
        await user.click(screen.getByRole('link', { name: 'Task Logs' }))
        await waitFor(() =>
          expect(view.router.state.location.pathname).toBe('/usage-logs/task')
        )
        expect(screen.getByRole('link', { name: 'Task Logs' })).toHaveAttribute(
          'data-active'
        )
        expect(
          screen.getByRole('link', { name: 'Drawing Logs' })
        ).not.toHaveAttribute('data-active')
      } finally {
        view.cleanup()
      }
    }
  )

  it('offers all four log destinations when the desktop sidebar is collapsed', async () => {
    vi.stubGlobal('innerWidth', 1024)
    const user = userEvent.setup()
    const view = await renderLogMenu(ROLE.USER, true)
    try {
      await user.click(screen.getByRole('button', { name: 'Logs' }))
      expect(
        screen.getAllByRole('menuitem').map((item) => item.textContent)
      ).toEqual(['Usage Logs', 'Audit Logs', 'Drawing Logs', 'Task Logs'])
      await user.click(screen.getByRole('menuitem', { name: 'Audit Logs' }))
      await waitFor(() =>
        expect(view.router.state.location.pathname).toBe('/usage-logs/audit')
      )
    } finally {
      view.cleanup()
    }
  })
})

describe('model pricing badge', () => {
  function issue(modelName: string, pluginKey: string): ModelPricingIssue {
    return {
      model_name: modelName,
      plugin_key: pluginKey,
      plugin_name: pluginKey,
      reason: 'unconfigured',
    }
  }

  /** Lists the sidebar entries of the current view that carry a badge. */
  function BadgedEntries() {
    const { navGroups } = useSidebarView()
    const entries = navGroups
      .flatMap((group) => group.items)
      .flatMap((item) => [item, ...(item.items ?? [])])
      .filter((item) => item.badge)
    return (
      <ul aria-label='Badged entries'>
        {entries.map((item) => (
          <li key={item.title}>{`${item.title}: ${item.badge}`}</li>
        ))}
      </ul>
    )
  }

  async function renderSidebar(
    path: string,
    role: number,
    issues: () => ModelPricingIssue[]
  ) {
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: {
        success: true,
        data: url === '/api/option/model_pricing/issues' ? issues() : {},
      },
    }))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(['status'], { SidebarModulesAdmin: '' })
    useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role })
    const router = createRouter({
      routeTree: createRootRoute({ component: BadgedEntries }),
      history: createMemoryHistory({ initialEntries: [path] }),
    })
    await router.load()
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    )
    return { client, get }
  }

  function badgedEntries() {
    return screen
      .getByRole('list', { name: 'Badged entries' })
      .querySelectorAll('li')
  }

  it('counts each task model once on the System Settings entry of the main sidebar', async () => {
    await renderSidebar('/dashboard/overview', ROLE.SUPER_ADMIN, () => [
      issue('kling-v3', 'kling'),
      issue('shared-video', 'alpha'),
      issue('shared-video', 'beta'),
    ])

    expect(await screen.findByText('System Settings: 2')).toBeInTheDocument()
    expect(badgedEntries()).toHaveLength(1)
  })

  it('inside System Settings shows the count on Billing & Payment and Model Pricing', async () => {
    await renderSidebar('/system-settings/site', ROLE.SUPER_ADMIN, () => [
      issue('kling-v3', 'kling'),
    ])

    expect(await screen.findByText('Model Pricing: 1')).toBeInTheDocument()
    expect(screen.getByText('Billing & Payment: 1')).toBeInTheDocument()
    expect(badgedEntries()).toHaveLength(2)
  })

  it('follows plugin changes and saved prices without a page reload', async () => {
    let issues = [issue('kling-v3', 'kling')]
    const { client } = await renderSidebar(
      '/dashboard/overview',
      ROLE.SUPER_ADMIN,
      () => issues
    )
    await screen.findByText('System Settings: 1')

    // A plugin action refreshes the count it may have changed.
    issues = [issue('kling-v3', 'kling'), issue('vidu-q3', 'vidu')]
    await act(() =>
      client.invalidateQueries({ queryKey: MODEL_PRICING_ISSUES_QUERY_KEY })
    )
    expect(await screen.findByText('System Settings: 2')).toBeInTheDocument()

    // Every pricing save runs this invalidation.
    issues = []
    await act(() => invalidateModelPricing(client))
    await waitFor(() => expect(badgedEntries()).toHaveLength(0))
  })

  it('never asks for the count below the super administrator role', async () => {
    const { get } = await renderSidebar(
      '/dashboard/overview',
      ROLE.ADMIN,
      () => [issue('kling-v3', 'kling')]
    )

    expect(
      await screen.findByRole('list', { name: 'Badged entries' })
    ).toBeInTheDocument()
    expect(get).not.toHaveBeenCalledWith('/api/option/model_pricing/issues')
    expect(badgedEntries()).toHaveLength(0)
  })
})
