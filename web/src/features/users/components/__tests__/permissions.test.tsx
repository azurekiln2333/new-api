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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { User } from '../../types'
import { UsersMutateDrawer } from '../users-mutate-drawer'
import { UsersProvider } from '../users-provider'

const target: User = {
  id: 2,
  username: 'managed-admin',
  display_name: 'Managed admin',
  role: 10,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}
const label = "View other accounts' audit logs"
const description =
  'View audit records from user and admin roles. Root records are always excluded.'

function renderPermissions(viewerRole: number, allowed?: boolean) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: viewerRole })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') {
      return {
        data: {
          success: true,
          data: {
            resources: [
              {
                resource: 'audit',
                label_key: 'Audit Logs',
                actions: [
                  {
                    action: 'read',
                    label_key: label,
                    description_key: description,
                  },
                ],
              },
            ],
            roles: [{ key: 'admin', grants: { audit: { read: false } } }],
          },
        },
      }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'admin.user.update',
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          ...target,
          admin_permissions:
            allowed === undefined ? {} : { audit: { read: allowed } },
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer
          open
          onOpenChange={() => undefined}
          currentRow={target}
        />
      </UsersProvider>
    </QueryClientProvider>
  )
  return get
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('does not submit until the matching user detail has loaded and ignores a late previous response', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role: 100 })
  let resolveFirst!: (value: { data: unknown }) => void
  let resolveSecond!: (value: { data: unknown }) => void
  const first = new Promise<{ data: unknown }>((resolve) => {
    resolveFirst = resolve
  })
  const second = new Promise<{ data: unknown }>((resolve) => {
    resolveSecond = resolve
  })
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url === '/api/authz/catalog') {
      return { data: { success: true, data: { resources: [], roles: [] } } }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/user/2') return first
    if (url === '/api/user/3') return second
    throw new Error(`Unexpected GET ${url} ${String(config)}`)
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const rowA = { ...target, id: 2, username: 'row-a', display_name: 'Row A' }
  const rowB = { ...target, id: 3, username: 'row-b', display_name: 'Row B' }
  const view = render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={rowA} />
      </UsersProvider>
    </QueryClientProvider>
  )
  const save = screen.getByRole('button', { name: 'Save changes' })
  expect(save).toBeDisabled()
  expect(screen.getByDisplayValue('row-a')).toBeInTheDocument()
  view.rerender(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={rowB} />
      </UsersProvider>
    </QueryClientProvider>
  )
  expect(screen.getByDisplayValue('row-b')).toBeInTheDocument()
  expect(screen.queryByDisplayValue('Row A')).not.toBeInTheDocument()
  resolveFirst({ data: { success: true, data: { ...rowA, username: 'late-a' } } })
  await waitFor(() => expect(screen.getByDisplayValue('row-b')).toBeInTheDocument())
  expect(save).toBeDisabled()
  resolveSecond({ data: { success: true, data: { ...rowB, username: 'fresh-b' } } })
  await waitFor(() => expect(screen.getByDisplayValue('fresh-b')).toBeInTheDocument())
  expect(save).toBeEnabled()
  await userEvent.click(save)
  await waitFor(() => expect(put).toHaveBeenCalledWith('/api/user/', expect.objectContaining({ id: 3 }), {}))
})

it('keeps Retry accessible after a failed detail load and enables saving only after retry succeeds', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role: 100 })
  let detailReads = 0
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') {
      return { data: { success: true, data: { resources: [], roles: [] } } }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/user/2/rate_limit') {
      return { data: { success: true, data: { enabled: false } } }
    }
    detailReads += 1
    if (detailReads === 1) {
      return { data: { success: false, message: 'Detail unavailable' } }
    }
    return { data: { success: true, data: { ...target, username: 'fresh-user' } } }
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={target} />
      </UsersProvider>
    </QueryClientProvider>
  )
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  expect(retry.closest('[inert]')).toBeNull()
  retry.focus()
  expect(retry).toHaveFocus()

  await userEvent.click(retry)

  await screen.findByDisplayValue('fresh-user')
  expect(detailReads).toBe(2)
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
})

it('applies an edited quota as a delta and refreshes only the matching quota field', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role: 100 })
  const detail = { ...target, quota: 1_000_000 }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') return { data: { success: true, data: { resources: [], roles: [] } } }
    if (url === '/api/group/') return { data: { success: true, data: ['default'] } }
    return { data: { success: true, data: detail } }
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { success: true } })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={detail} />
      </UsersProvider>
    </QueryClientProvider>
  )
  await screen.findByDisplayValue('Managed admin')
  const quota = screen.getByLabelText(/Remaining Quota/)
  await userEvent.clear(quota)
  await userEvent.type(quota, '3')
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(post).toHaveBeenCalledWith('/api/user/manage', { id: 2, action: 'add_quota', mode: 'add', value: 500000 }))
})

it('sends one quota request when Confirm is pressed twice, then reconciles with the server balance', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role: 100 })
  let serverQuota = 1_000_000
  const detail = () => ({ ...target, quota: serverQuota })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') return { data: { success: true, data: { resources: [], roles: [] } } }
    if (url === '/api/group/') return { data: { success: true, data: ['default'] } }
    return { data: { success: true, data: detail() } }
  })
  let resolveAdjustment!: (value: { data: { success: boolean } }) => void
  const adjustment = new Promise<{ data: { success: boolean } }>((resolve) => {
    resolveAdjustment = resolve
  })
  const post = vi.spyOn(api, 'post').mockImplementation(() => {
    // Concurrent consumption lands before the requested delta is committed.
    serverQuota = serverQuota - 100_000 + 500_000
    return adjustment
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={detail()} />
      </UsersProvider>
    </QueryClientProvider>
  )
  await screen.findByDisplayValue('Managed admin')
  const quota = screen.getByLabelText(/Remaining Quota/)
  await userEvent.clear(quota)
  await userEvent.type(quota, '3')
  const confirm = screen.getByRole('button', { name: 'Confirm' })
  await userEvent.click(confirm)
  await userEvent.click(confirm)
  expect(post).toHaveBeenCalledTimes(1)
  expect(post).toHaveBeenCalledWith('/api/user/manage', { id: 2, action: 'add_quota', mode: 'add', value: 500000 })
  resolveAdjustment({ data: { success: true } })
  // The delta survives concurrent consumption: 1000000 - 100000 + 500000.
  // The displayed form follows that reconciled balance, not the requested 3.
  await waitFor(() => expect(quota).toHaveValue(1_400_000 / 500_000))
  // The reconciled baseline matches what the form shows, so nothing is pending.
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
})

it("keeps the delta baseline when the reconciliation read reports a server failure", async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role: 100 })
  let detailReads = 0
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') return { data: { success: true, data: { resources: [], roles: [] } } }
    if (url === '/api/group/') return { data: { success: true, data: ['default'] } }
    if (url === '/api/user/2/rate_limit') {
      return { data: { success: true, data: { enabled: false } } }
    }
    detailReads += 1
    // A failed body must never seed the baseline, even if it carries data.
    if (detailReads > 1) {
      return { data: { success: false, data: { ...target, quota: 0 } } }
    }
    return { data: { success: true, data: { ...target, quota: 1_000_000 } } }
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { success: true } })
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={target} />
      </UsersProvider>
    </QueryClientProvider>
  )
  await waitFor(() => expect(detailReads).toBe(1))
  const quota = screen.getByLabelText(/Remaining Quota/)
  await userEvent.clear(quota)
  await userEvent.type(quota, '3')
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(post).toHaveBeenCalledWith('/api/user/manage', { id: 2, action: 'add_quota', mode: 'add', value: 500000 }))
  await waitFor(() => expect(detailReads).toBe(2))
  // A failed reconciliation must not replace the committed baseline with zero
  // or leave the same delta available to submit again.
  expect(quota).toHaveValue(3)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  )
})

it('treats a saved matrix as changed when the loaded baseline came from a user that was replaced', async () => {
  // The baseline must come from the fetched detail, never from a previous row:
  // comparing against the wrong matrix drops a real permission change silently.
  const first = vi.spyOn(api, 'get')
  renderPermissions(100, true)
  await screen.findByDisplayValue('Managed admin')
  const detail = first.mock.calls
    .map(([url]) => url)
    .filter((url) => url === '/api/user/2')
  expect(detail).toEqual(['/api/user/2'])
})

it.each([undefined, true])(
  'root can save an audit grant or revocation after step-up verification (previous=%s)',
  async (allowed) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          proof_token: 'update-proof',
          method: '2fa',
          scope: 'admin.user.update',
          expires_at: Math.floor(Date.now() / 1000) + 60,
        },
      },
    })
    renderPermissions(100, allowed)
    await screen.findByDisplayValue('Managed admin')
    const toggle = await screen.findByRole('button', { name: label })
    await waitFor(() =>
      expect(toggle).toHaveAttribute('aria-pressed', String(!!allowed))
    )
    expect(toggle).toHaveAccessibleDescription(description)
    await userEvent.click(toggle)
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    // The permission matrix changed, so the save waits for verification.
    expect(put).not.toHaveBeenCalled()
    await userEvent.type(
      await screen.findByLabelText('Authenticator code or backup code'),
      '123456'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/user/',
        expect.objectContaining({
          id: 2,
          admin_permissions: { audit: { read: !allowed } },
        }),
        expect.objectContaining({
          headers: { 'X-Security-Proof': 'update-proof' },
          singleUseAuthorization: true,
        })
      )
    )
  }
)

it('re-reads the fresh detail for a reopened user so the quota baseline is not the stale list value', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role: 100 })
  let serverQuota = 1_000_000
  const detailCalls: string[] = []
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') {
      return { data: { success: true, data: { resources: [], roles: [] } } }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/user/2/rate_limit') {
      return { data: { success: true, data: { enabled: false } } }
    }
    detailCalls.push(url)
    return { data: { success: true, data: { ...target, quota: serverQuota } } }
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const row = { ...target, quota: 1_000_000 }
  const view = render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={row} />
      </UsersProvider>
    </QueryClientProvider>
  )
  await screen.findByDisplayValue('Managed admin')
  view.rerender(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open={false} onOpenChange={() => undefined} currentRow={row} />
      </UsersProvider>
    </QueryClientProvider>
  )
  // The user spent quota while the drawer was closed.
  serverQuota = 400_000
  view.rerender(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer open onOpenChange={() => undefined} currentRow={row} />
      </UsersProvider>
    </QueryClientProvider>
  )
  expect(detailCalls).toEqual(['/api/user/2', '/api/user/2'])
  await waitFor(() =>
    expect(screen.getByLabelText(/Remaining Quota/)).toHaveValue(400_000 / 500_000)
  )
})

it('root saving an administrator without changing permissions or password does not verify', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const get = renderPermissions(100, true)
  const displayName = await screen.findByDisplayValue('Managed admin')
  await userEvent.clear(displayName)
  await userEvent.type(displayName, 'Renamed admin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/',
      expect.objectContaining({ id: 2, display_name: 'Renamed admin' }),
      {}
    )
  )
  expect(put.mock.calls[0][1]).not.toHaveProperty('admin_permissions')
  expect(get).not.toHaveBeenCalledWith('/api/verify/methods', expect.anything())
})

it('admin cannot edit the audit permission even when the catalog is available', async () => {
  renderPermissions(10)
  await screen.findByDisplayValue('Managed admin')
  expect(screen.queryByRole('button', { name: label })).not.toBeInTheDocument()
})
