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
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { resolveAuthentication, type RefreshOutcome } from '@/lib/auth-session'
import { Route as SignInRoute } from '@/routes/(auth)/sign-in'
import { Route as AuthenticatedRoute } from '@/routes/_authenticated/route'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

vi.mock('@/lib/auth-session', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/auth-session')>()),
  resolveAuthentication: vi.fn(),
}))

const bundle: AuthBundle = {
  access_token: 'test-access-token',
  token_type: 'Bearer',
  access_expires_at: Math.floor(Date.now() / 1000) + 600,
  user: { id: 42, username: 'test-user', role: 1 },
  session: {
    sid: 'test-session',
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: Math.floor(Date.now() / 1000) + 3600,
  },
}

type ProtectedBeforeLoad = (context: {
  location: { href: string }
}) => Promise<void>
type SignInBeforeLoad = (context: {
  search: { redirect?: string }
}) => Promise<void>

function createAuthRouter(initialPath: string) {
  const root = createRootRoute({ component: Outlet })
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const protectedRoute = createRoute({
    getParentRoute: () => authenticated,
    path: '/protected',
    beforeLoad: AuthenticatedRoute.options.beforeLoad as ProtectedBeforeLoad,
    errorComponent: AuthenticatedRoute.options.errorComponent,
    component: () => <p>Protected content</p>,
  })
  const signInRoute = createRoute({
    getParentRoute: () => root,
    path: '/sign-in',
    validateSearch: (search: Record<string, unknown>) => ({
      redirect:
        typeof search.redirect === 'string' ? search.redirect : undefined,
    }),
    beforeLoad: SignInRoute.options.beforeLoad as SignInBeforeLoad,
    errorComponent: SignInRoute.options.errorComponent,
    component: () => <p>Sign-in form</p>,
  })
  const destination = createRoute({
    getParentRoute: () => root,
    path: '/dashboard',
    component: () => <p>Dashboard destination</p>,
  })
  return createRouter({
    routeTree: root.addChildren([
      authenticated.addChildren([protectedRoute]),
      signInRoute,
      destination,
    ]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  })
}

const resolveAuth = vi.mocked(resolveAuthentication)

beforeEach(() => {
  useAuthStore.getState().auth.reset('idle')
  resolveAuth.mockReset()
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset('idle')
})

test('a transient protected-route refresh keeps the target and retry restores it', async () => {
  resolveAuth.mockResolvedValue({
    kind: 'transient_error',
    error: new Error('offline'),
  })

  const router = createAuthRouter('/protected?tab=keys')
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(router.state.location.pathname).toBe('/protected')
  expect(router.state.location.search).toEqual({ tab: 'keys' })
  expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
  expect(screen.queryByText('Sign-in form')).not.toBeInTheDocument()

  resolveAuth.mockImplementation(async (): Promise<RefreshOutcome> => {
    useAuthStore.getState().auth.setBundle(bundle)
    return { kind: 'authenticated', bundle }
  })
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

  expect(await screen.findByText('Protected content')).toBeVisible()
  expect(router.state.location.pathname).toBe('/protected')
  expect(router.state.location.search).toEqual({ tab: 'keys' })
  expect(resolveAuth).toHaveBeenCalledTimes(2)
})

test('a transient sign-in refresh hides the form until retry restores its destination', async () => {
  resolveAuth.mockResolvedValue({
    kind: 'transient_error',
    error: new Error('offline'),
  })

  const router = createAuthRouter(
    '/sign-in?redirect=%2Fdashboard%3Ftab%3Dusage'
  )
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(screen.queryByText('Sign-in form')).not.toBeInTheDocument()

  resolveAuth.mockImplementation(async (): Promise<RefreshOutcome> => {
    useAuthStore.getState().auth.setBundle(bundle)
    return { kind: 'authenticated', bundle }
  })
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

  expect(await screen.findByText('Dashboard destination')).toBeVisible()
  expect(router.state.location.pathname).toBe('/dashboard')
  expect(router.state.location.search).toEqual({ tab: 'usage' })
  expect(resolveAuth).toHaveBeenCalledTimes(2)
})

test('a confirmed anonymous protected check redirects to sign-in with its target', async () => {
  resolveAuth.mockResolvedValue({ kind: 'anonymous' })

  const router = createAuthRouter('/protected?tab=keys')
  render(<RouterProvider router={router} />)
  await act(() => router.load())

  expect(await screen.findByText('Sign-in form')).toBeVisible()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(router.state.location.search).toEqual({
    redirect: '/protected?tab=keys',
  })
  expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
})

test('a confirmed anonymous sign-in check still renders the sign-in form', async () => {
  resolveAuth.mockResolvedValue({ kind: 'anonymous' })

  const router = createAuthRouter('/sign-in')
  render(<RouterProvider router={router} />)
  await act(() => router.load())

  expect(await screen.findByText('Sign-in form')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Retry' })
  ).not.toBeInTheDocument()
})
