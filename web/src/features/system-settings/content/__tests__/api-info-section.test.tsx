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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ApiInfoSection } from '../api-info-section'

let client: QueryClient
const rootUser = { id: 1, username: 'root', role: ROLE.SUPER_ADMIN }
const instances = [
  {
    node_name: 'node-a',
    status: 'online',
    stale_after_seconds: 90,
    started_at: 1,
    last_seen_at: 2,
  },
  {
    node_name: 'node-stale',
    status: 'stale',
    stale_after_seconds: 90,
    started_at: 1,
    last_seen_at: 1,
  },
]

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  useAuthStore.getState().auth.setUser(rootUser)
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, message: '', data: instances },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.setUser(null)
  vi.restoreAllMocks()
})

const configuredEntries = [
  {
    id: 7,
    url: 'https://primary.example.com',
    route: 'Primary',
    description: 'First',
    color: 'blue',
    node_name: 'node-a',
  },
  {
    id: 12,
    url: 'https://secondary.example.com',
    route: 'Secondary',
    description: 'Second',
    color: 'green',
  },
]

function renderSection(data = JSON.stringify(configuredEntries)) {
  return render(
    <QueryClientProvider client={client}>
      <ApiInfoSection enabled data={data} />
    </QueryClientProvider>
  )
}

function savedApiInfo(payload: unknown) {
  const spy = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, message: '', data: null },
  })
  return { spy, payload }
}

describe('ApiInfoSection', () => {
  it('selects one authorized instance for multiple API domains without changing ids', async () => {
    const put = savedApiInfo(null)
    const user = userEvent.setup()
    renderSection()

    expect(await screen.findByText('https://primary.example.com')).toBeVisible()
    expect(screen.getByText('https://secondary.example.com')).toBeVisible()
    expect(api.get).not.toHaveBeenCalled()

    const secondaryRow = screen
      .getByText('https://secondary.example.com')
      .closest('tr')
    expect(secondaryRow).not.toBeNull()
    await user.click(
      within(secondaryRow as HTMLElement).getByRole('button', { name: 'Edit' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'Edit API Shortcut',
    })
    const binding = within(dialog).getByRole('combobox', { name: 'Instance' })
    expect(binding).toHaveTextContent('Unbound')
    expect(
      within(dialog).queryByRole('textbox', { name: 'Instance' })
    ).toBeNull()
    await user.click(binding)
    await user.click(
      await screen.findByRole('option', { name: 'node-a (Online)' })
    )
    expect(api.get).toHaveBeenCalledWith('/api/system-info/instances')
    expect(binding).toHaveTextContent('node-a (Online)')
    await user.click(within(dialog).getByRole('button', { name: 'Update' }))
    await user.click(screen.getByRole('button', { name: 'Save Settings' }))

    await waitFor(() => {
      expect(put.spy).toHaveBeenCalled()
    })
    const body = put.spy.mock.calls[0][1] as { key: string; value: string }
    expect(body.key).toBe('console_setting.api_info')
    const stored = JSON.parse(body.value) as Array<Record<string, unknown>>
    expect(stored.map((entry) => entry.node_name)).toEqual(['node-a', 'node-a'])
    expect(stored.map((entry) => entry.id)).toEqual([7, 12])
  })

  it('reorders entries without changing their persistent ids', async () => {
    const put = savedApiInfo(null)
    renderSection()

    expect(await screen.findByText('https://primary.example.com')).toBeVisible()

    // Move the second entry up.
    const rows = screen.getAllByRole('row')
    const secondaryRow = rows.find((row) =>
      within(row).queryByText('https://secondary.example.com')
    )
    expect(secondaryRow).toBeDefined()
    await userEvent.click(
      within(secondaryRow as HTMLElement).getByRole('button', {
        name: 'Move up',
      })
    )

    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))

    await waitFor(() => {
      expect(put.spy).toHaveBeenCalled()
    })
    const body = put.spy.mock.calls[0][1] as { value: string }
    const stored = JSON.parse(body.value) as Array<Record<string, unknown>>
    // New order, same identities: reordering must never re-key an entry.
    expect(stored.map((entry) => entry.url)).toEqual([
      'https://secondary.example.com',
      'https://primary.example.com',
    ])
    expect(stored.map((entry) => entry.id)).toEqual([12, 7])
  })

  it('assigns a persistent id to a legacy entry that has none', async () => {
    const put = savedApiInfo(null)
    const legacy = [
      {
        url: 'https://legacy.example.com',
        route: 'Legacy',
        description: 'No id yet',
        color: 'blue',
      },
    ]
    renderSection(JSON.stringify(legacy))

    expect(await screen.findByText('https://legacy.example.com')).toBeVisible()

    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    await userEvent.click(screen.getByRole('button', { name: 'Update' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))

    await waitFor(() => {
      expect(put.spy).toHaveBeenCalled()
    })
    const body = put.spy.mock.calls[0][1] as { value: string }
    const stored = JSON.parse(body.value) as Array<Record<string, unknown>>
    // A stable positive id is persisted so future consumers stop depending on
    // array position.
    expect(stored[0].id).toBe(1)
  })

  it('gives two legacy rows distinct ids so one edit cannot touch both', async () => {
    // Two rows saved before ids existed. Sharing one id would make an edit or a
    // delete hit every legacy row at once.
    const legacy = [
      {
        id: 4,
        url: 'https://kept.example.com',
        route: 'Kept',
        description: 'Has an id',
        color: 'blue',
      },
      {
        url: 'https://legacy-one.example.com',
        route: 'Legacy One',
        description: 'No id',
        color: 'green',
      },
      {
        url: 'https://legacy-two.example.com',
        route: 'Legacy Two',
        description: 'No id either',
        color: 'red',
      },
    ]
    const put = savedApiInfo(null)
    renderSection(JSON.stringify(legacy))

    expect(
      await screen.findByText('https://legacy-one.example.com')
    ).toBeVisible()
    expect(screen.getByText('https://legacy-two.example.com')).toBeVisible()

    // Edit exactly one legacy row. Only that row may change.
    const oneRow = screen
      .getByText('https://legacy-one.example.com')
      .closest('tr')
    expect(oneRow).not.toBeNull()
    await userEvent.click(
      within(oneRow as HTMLElement).getByRole('button', { name: 'Edit' })
    )
    const description = await screen.findByLabelText('Description')
    await userEvent.clear(description)
    await userEvent.type(description, 'Edited legacy row')
    // The edit dialog submits with "Update".
    await userEvent.click(screen.getByRole('button', { name: 'Update' }))

    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => {
      expect(put.spy).toHaveBeenCalled()
    })
    const body = put.spy.mock.calls[0][1] as { value: string }
    const stored = JSON.parse(body.value) as Array<Record<string, unknown>>

    // Distinct ids above the existing maximum, existing id preserved.
    expect(stored.map((entry) => entry.id)).toEqual([4, 5, 6])
    expect(stored[0].description).toBe('Has an id')
    expect(stored[1].description).toBe('Edited legacy row')
    expect(stored[2].description).toBe('No id either')
  })

  it('deletes only the selected legacy row', async () => {
    const legacy = [
      {
        url: 'https://legacy-one.example.com',
        route: 'Legacy One',
        description: 'First legacy',
        color: 'green',
      },
      {
        url: 'https://legacy-two.example.com',
        route: 'Legacy Two',
        description: 'Second legacy',
        color: 'red',
      },
    ]
    const put = savedApiInfo(null)
    renderSection(JSON.stringify(legacy))

    expect(
      await screen.findByText('https://legacy-one.example.com')
    ).toBeVisible()

    const twoRow = screen
      .getByText('https://legacy-two.example.com')
      .closest('tr')
    expect(twoRow).not.toBeNull()
    await userEvent.click(
      within(twoRow as HTMLElement).getByRole('button', { name: 'Open menu' })
    )
    await userEvent.click(
      await screen.findByRole('menuitem', { name: 'Delete' })
    )
    // Confirm in the alert dialog, which also uses "Delete" as its action.
    await userEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )

    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => {
      expect(put.spy).toHaveBeenCalled()
    })
    const body = put.spy.mock.calls[0][1] as { value: string }
    const stored = JSON.parse(body.value) as Array<Record<string, unknown>>
    // Only the chosen row is gone; the sibling legacy row survives with its own
    // distinct id.
    expect(stored.map((entry) => entry.url)).toEqual([
      'https://legacy-one.example.com',
    ])
    expect(stored[0].id).toBe(1)
  })

  it.each([
    { nodeName: 'node-stale', label: 'node-stale (Stale)' },
    { nodeName: 'node-missing', label: 'node-missing (Unavailable)' },
  ])(
    'preserves the configured $nodeName binding while editing',
    async ({ nodeName, label }) => {
      const put = savedApiInfo(null)
      const user = userEvent.setup()
      renderSection(
        JSON.stringify([{ ...configuredEntries[0], node_name: nodeName }])
      )

      await user.click(screen.getByRole('button', { name: 'Edit' }))
      const binding = screen.getByRole('combobox', { name: 'Instance' })
      await waitFor(() => expect(binding).toHaveTextContent(label))
      await user.click(binding)
      expect(await screen.findByRole('option', { name: label })).toBeVisible()
      await user.keyboard('{Escape}')
      await user.click(screen.getByRole('button', { name: 'Update' }))
      await user.click(screen.getByRole('button', { name: 'Save Settings' }))
      await waitFor(() => expect(put.spy).toHaveBeenCalled())
      const body = put.spy.mock.calls[0][1] as { value: string }
      expect(JSON.parse(body.value)[0]).toMatchObject({
        id: 7,
        node_name: nodeName,
      })
    }
  )

  it('clears a binding only when Unbound is explicitly chosen', async () => {
    const put = savedApiInfo(null)
    const user = userEvent.setup()
    renderSection(JSON.stringify([configuredEntries[0]]))

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const binding = screen.getByRole('combobox', { name: 'Instance' })
    await waitFor(() => expect(binding).toHaveTextContent('node-a (Online)'))
    await user.click(binding)
    await user.click(await screen.findByRole('option', { name: 'Unbound' }))
    expect(binding).toHaveTextContent('Unbound')
    await user.click(screen.getByRole('button', { name: 'Update' }))
    await user.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => expect(put.spy).toHaveBeenCalled())
    const body = put.spy.mock.calls[0][1] as { value: string }
    expect(JSON.parse(body.value)[0]).not.toHaveProperty('node_name')
  })

  it('keeps the configured binding during loading and after a network failure', async () => {
    let rejectList!: (reason: Error) => void
    const pending = new Promise<never>((_resolve, reject) => {
      rejectList = reject
    })
    vi.mocked(api.get).mockReturnValueOnce(pending)
    const put = savedApiInfo(null)
    const user = userEvent.setup()
    renderSection(JSON.stringify([configuredEntries[0]]))

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const binding = screen.getByRole('combobox', { name: 'Instance' })
    expect(await screen.findByText('Loading...')).toBeVisible()
    expect(binding).toHaveTextContent('node-a (Unknown)')
    await act(async () => {
      rejectList(new Error('offline'))
    })
    expect(
      await screen.findByText('We could not load instances.')
    ).toBeVisible()
    expect(binding).toHaveTextContent('node-a (Unknown)')
    await user.click(screen.getByRole('button', { name: 'Update' }))
    await user.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => expect(put.spy).toHaveBeenCalled())
    const body = put.spy.mock.calls[0][1] as { value: string }
    expect(JSON.parse(body.value)[0]).toMatchObject({ node_name: 'node-a' })
  })

  it('treats a rejected instance response as an error and retries without changing the binding', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: { success: false, message: 'Unavailable', data: instances },
    })
    const user = userEvent.setup()
    renderSection(JSON.stringify([configuredEntries[0]]))

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const binding = screen.getByRole('combobox', { name: 'Instance' })
    expect(
      await screen.findByText('We could not load instances.')
    ).toBeVisible()
    expect(binding).toHaveTextContent('node-a (Unknown)')
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(binding).toHaveTextContent('node-a (Online)'))
    expect(screen.queryByText('We could not load instances.')).toBeNull()
  })

  it.each([ROLE.USER, ROLE.ADMIN])(
    'does not request or expose cached instance choices for role %s',
    async (role) => {
      useAuthStore.getState().auth.setUser({ id: 2, username: 'viewer', role })
      client.setQueryData(
        ['system-info', 'instances', 2, role, false],
        [{ node_name: 'private-node', status: 'online' }]
      )
      const user = userEvent.setup()
      renderSection(JSON.stringify([configuredEntries[0]]))

      await user.click(screen.getByRole('button', { name: 'Edit' }))
      const binding = screen.getByRole('combobox', { name: 'Instance' })
      expect(binding).toBeDisabled()
      expect(binding).toHaveTextContent('node-a (Unknown)')
      expect(screen.queryByText(/private-node/)).toBeNull()
      expect(api.get).not.toHaveBeenCalled()
    }
  )

  it('discards another root user instance choices and hides them after demotion', async () => {
    let finishNext!: (response: {
      data: { success: boolean; data: typeof instances }
    }) => void
    const nextList = new Promise<{
      data: { success: boolean; data: typeof instances }
    }>((resolve) => {
      finishNext = resolve
    })
    const user = userEvent.setup()
    renderSection(JSON.stringify([configuredEntries[0]]))
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    let binding = screen.getByRole('combobox', { name: 'Instance' })
    await user.click(binding)
    expect(
      await screen.findByRole('option', { name: 'node-stale (Stale)' })
    ).toBeVisible()
    vi.mocked(api.get).mockReturnValueOnce(nextList)

    act(() => {
      useAuthStore.getState().auth.setUser({ ...rootUser, id: 2 })
    })
    binding = screen.getByRole('combobox', { name: 'Instance' })
    expect(binding).toHaveTextContent('node-a (Unknown)')
    await user.click(binding)
    expect(
      screen.queryByRole('option', { name: 'node-stale (Stale)' })
    ).toBeNull()
    await user.keyboard('{Escape}')
    act(() => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'viewer',
        role: ROLE.USER,
      })
    })
    binding = screen.getByRole('combobox', { name: 'Instance' })
    expect(binding).toBeDisabled()
    await act(async () => {
      finishNext({
        data: {
          success: true,
          data: [{ ...instances[1], node_name: 'late-private-node' }],
        },
      })
    })
    expect(binding).toHaveTextContent('node-a (Unknown)')
    expect(screen.queryByText(/late-private-node|node-stale/)).toBeNull()
    expect(api.get).toHaveBeenCalledTimes(2)
  })

  it('shows the instance binding column and an unbound entry as unbound', async () => {
    renderSection()

    expect(await screen.findByText('https://primary.example.com')).toBeVisible()
    expect(screen.getByText('node-a')).toBeVisible()
    expect(screen.getByText('Unbound')).toBeVisible()
  })
})
