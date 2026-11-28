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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { useSystemOptions } from '../../hooks/use-system-options'
import type { SystemOptionsResponse } from '../../types'
import { AnnouncementsSection } from '../announcements-section'

const announcementKey = 'console_setting.announcements'
const enabledKey = 'console_setting.announcements_enabled'
const original = [
  {
    id: 1,
    content: 'Original announcement',
    publishDate: '2026-01-01T00:00:00Z',
    type: 'default',
    extra: '',
  },
]
let client: QueryClient

function SectionObserver() {
  const query = useSystemOptions()
  const options = query.data?.data ?? []
  return (
    <AnnouncementsSection
      enabled={
        options.find((option) => option.key === enabledKey)?.value !== 'false'
      }
      data={
        options.find((option) => option.key === announcementKey)?.value ?? '[]'
      }
    />
  )
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData<SystemOptionsResponse>(['system-options'], {
    success: true,
    message: '',
    data: [
      { key: announcementKey, value: JSON.stringify(original) },
      { key: enabledKey, value: 'true' },
    ],
  })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  render(
    <QueryClientProvider client={client}>
      <SectionObserver />
    </QueryClientProvider>
  )
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

async function editAnnouncement(content: string, rowContent: string) {
  const user = userEvent.setup()
  const row = screen.getByText(rowContent).closest('tr')
  expect(row).not.toBeNull()
  await user.click(
    within(row as HTMLElement).getByRole('button', { name: 'Edit' })
  )
  const dialog = await screen.findByRole('dialog', {
    name: 'Edit Announcement',
  })
  const input = within(dialog).getByRole('textbox', { name: 'Content' })
  await user.clear(input)
  await user.type(input, content)
  await user.click(within(dialog).getByRole('button', { name: 'Update' }))
  await waitFor(() => expect(dialog).not.toBeInTheDocument())
  expect(screen.getByText(content)).toBeVisible()
}

function updateSavedAnnouncements(content: string) {
  client.setQueryData<SystemOptionsResponse>(['system-options'], (previous) => {
    if (!previous) return previous
    return {
      ...previous,
      data: previous.data.map((option) =>
        option.key === announcementKey
          ? { ...option, value: JSON.stringify([{ ...original[0], content }]) }
          : option
      ),
    }
  })
}

test('incoming cache data updates a clean list but preserves unsaved announcement edits and the open form', async () => {
  act(() => updateSavedAnnouncements('External announcement'))
  expect(await screen.findByText('External announcement')).toBeVisible()
  await editAnnouncement('Unsaved announcement', 'External announcement')
  act(() => updateSavedAnnouncements('New server announcement'))
  await waitFor(() =>
    expect(screen.getByText('Unsaved announcement')).toBeVisible()
  )
  expect(screen.queryByText('New server announcement')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()

  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Edit Announcement',
  })
  const input = within(dialog).getByRole('textbox', { name: 'Content' })
  await user.type(input, ' still editing')
  act(() => updateSavedAnnouncements('Another server announcement'))
  await waitFor(() =>
    expect(input).toHaveValue('Unsaved announcement still editing')
  )
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(dialog).not.toBeInTheDocument())
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: announcementKey,
    value: JSON.stringify([
      { ...original[0], content: 'Unsaved announcement' },
    ]),
  })
})

test('an earlier save completion and its cache echo preserve edits made while the request is pending', async () => {
  let finishSave!: (response: { data: { success: boolean } }) => void
  vi.mocked(api.put).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finishSave = resolve
      })
  )
  await editAnnouncement('Submitted announcement', 'Original announcement')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  expect(
    await screen.findByRole('button', { name: 'Saving...' })
  ).toBeDisabled()
  const enabledSwitch = screen.getByRole('switch')
  expect(enabledSwitch).toHaveAttribute('aria-disabled', 'true')
  await user.click(enabledSwitch)
  expect(api.put).toHaveBeenCalledTimes(1)
  await editAnnouncement('Later draft', 'Submitted announcement')
  await act(async () => {
    finishSave({ data: { success: true } })
  })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()
  )
  expect(enabledSwitch).not.toHaveAttribute('aria-disabled', 'true')
  expect(screen.getByText('Later draft')).toBeVisible()
  expect(screen.queryByText('Submitted announcement')).not.toBeInTheDocument()
  expect(
    client.getQueryData<SystemOptionsResponse>(['system-options'])?.data[0]
      .value
  ).toBe(
    JSON.stringify([{ ...original[0], content: 'Submitted announcement' }])
  )
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(vi.mocked(api.put).mock.calls[1][1]).toEqual({
    key: announcementKey,
    value: JSON.stringify([{ ...original[0], content: 'Later draft' }]),
  })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
  )
  act(() => updateSavedAnnouncements('Latest server baseline'))
  expect(await screen.findByText('Latest server baseline')).toBeVisible()
})

test('a rejected save keeps the later draft and retry sends that draft', async () => {
  let rejectSave!: (error: Error) => void
  vi.mocked(api.put).mockImplementationOnce(
    () =>
      new Promise((_resolve, reject) => {
        rejectSave = reject
      })
  )
  await editAnnouncement('Submitted announcement', 'Original announcement')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await editAnnouncement('Retry this draft', 'Submitted announcement')
  await act(async () => {
    rejectSave(new Error('Save failed'))
  })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()
  )
  act(() => updateSavedAnnouncements('Unrelated cache refresh'))
  await waitFor(() =>
    expect(screen.getByText('Retry this draft')).toBeVisible()
  )
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(vi.mocked(api.put).mock.calls[1][1]).toEqual({
    key: announcementKey,
    value: JSON.stringify([{ ...original[0], content: 'Retry this draft' }]),
  })
})
