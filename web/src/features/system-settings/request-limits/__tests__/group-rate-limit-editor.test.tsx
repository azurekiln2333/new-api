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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { RateLimitSection } from '../rate-limit-section'
import { RateLimitVisualEditor } from '../rate-limit-visual-editor'

const apiMocks = vi.hoisted(() => ({
  getGroups: vi.fn(),
  clearGroupUserRateLimits: vi.fn(),
  updateOption: vi.fn(),
}))

vi.mock('../../hooks/use-update-option', () => ({
  useUpdateOption: () => ({
    mutateAsync: apiMocks.updateOption,
    isPending: false,
  }),
}))

vi.mock('@/features/users/api', () => ({
  getGroups: apiMocks.getGroups,
  clearGroupUserRateLimits: apiMocks.clearGroupUserRateLimits,
}))

function renderEditor(props: {
  value: string
  onChange: (value: string) => void
  onClearUserOverrides?: (group: string, clear: boolean) => void
}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <RateLimitVisualEditor {...props} />
    </QueryClientProvider>
  )
}

describe('group rate limit editor', () => {
  beforeEach(() => {
    apiMocks.updateOption.mockReset().mockResolvedValue({ success: true })
    apiMocks.getGroups.mockResolvedValue({
      success: true,
      data: ['default', 'vip'],
    })
    apiMocks.clearGroupUserRateLimits.mockResolvedValue({
      data: { success: true, data: { group: 'vip', cleared: 2 } },
    })
  })

  it.each([true, false])(
    'clears staged overrides only after a successful settings save (success=%s)',
    async (success) => {
      const writes: string[] = []
      apiMocks.updateOption.mockImplementation(async () => {
        writes.push('save')
        if (!success) throw new Error('save failed')
        return { success: true }
      })
      apiMocks.clearGroupUserRateLimits.mockImplementation(async () => {
        writes.push('clear')
        return { data: { success: true, data: { cleared: 1 } } }
      })
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      const result = render(
        <QueryClientProvider client={client}>
          <RateLimitSection
            defaultValues={{
              ModelRequestRateLimitEnabled: true,
              ModelRequestRateLimitDurationMinutes: 1,
              ModelRequestRateLimitCount: 100,
              ModelRequestRateLimitSuccessCount: 50,
              ModelRequestRateLimitGroup: '{"vip":[0,10]}',
            }}
          />
        </QueryClientProvider>
      )
      const user = userEvent.setup()
      await user.click(await screen.findByRole('button', { name: 'Edit' }))
      await user.click(
        screen.getByRole('checkbox', {
          name: 'Also clear the manual limits of users in this group',
        })
      )
      fireEvent.change(
        within(screen.getByRole('dialog')).getAllByRole('spinbutton')[0],
        { target: { value: '5' } }
      )
      await user.click(screen.getByRole('button', { name: 'Update' }))
      await waitFor(() =>
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      )
      expect(writes).toEqual([])
      const form = result.container.querySelector('form')
      if (!form) throw new Error('Settings form missing')
      fireEvent.submit(form)
      await waitFor(() =>
        expect(writes).toEqual(success ? ['save', 'clear'] : ['save'])
      )
    }
  )

  it('offers existing groups to choose from when adding an entry', async () => {
    const onChange = vi.fn()
    renderEditor({ value: '{}', onChange })
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: 'Add group' }))
    const combobox = await screen.findByRole('combobox')
    await user.click(combobox)
    await user.click(await screen.findByRole('option', { name: 'vip' }))

    const [maxRequests, maxSuccess] = screen.getAllByRole(
      'spinbutton'
    ) as HTMLInputElement[]
    fireEvent.change(maxRequests, { target: { value: '5' } })
    fireEvent.change(maxSuccess, { target: { value: '2' } })
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(onChange).toHaveBeenCalledTimes(1))
    expect(JSON.parse(onChange.mock.calls[0][0])).toEqual({ vip: [5, 2] })
  })

  it('keeps an entry whose group no longer exists and says so', async () => {
    apiMocks.getGroups.mockResolvedValue({
      success: true,
      data: ['default'],
    })
    renderEditor({ value: '{"ghost":[1,2]}', onChange: vi.fn() })
    const user = userEvent.setup()

    expect(screen.getByText('ghost')).toBeInTheDocument()
    expect(await screen.findByText('Group missing')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Edit' }))
    expect(
      await screen.findByText(
        'This group no longer exists in the group settings. The entry is kept until you delete it.'
      )
    ).toBeInTheDocument()
  })

  it('stages clearing manual limits until the page settings are saved', async () => {
    const onChange = vi.fn()
    const onClearUserOverrides = vi.fn()
    renderEditor({ value: '{"vip":[0,10]}', onChange, onClearUserOverrides })
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.click(
      await screen.findByRole('checkbox', {
        name: 'Also clear the manual limits of users in this group',
      })
    )
    await user.click(screen.getByRole('button', { name: 'Update' }))

    await waitFor(() =>
      expect(onClearUserOverrides).toHaveBeenCalledWith('vip', true)
    )
    expect(apiMocks.clearGroupUserRateLimits).not.toHaveBeenCalled()
    expect(JSON.parse(onChange.mock.calls[0][0])).toEqual({ vip: [0, 10] })
  })
})
