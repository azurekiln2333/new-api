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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ComponentProps } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { LogSettingsSection } from '../log-settings-section'

type LogSettingsDefaults = ComponentProps<
  typeof LogSettingsSection
>['defaultValues']

function Fixture(props: { defaultValues?: Partial<LogSettingsDefaults> }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <LogSettingsSection
          defaultValues={{
            LogConsumeEnabled: true,
            'log_setting.user_export_enabled': true,
            'log_setting.max_range_days': 30,
            'log_setting.retention_days': 0,
            'log_setting.request_content_retention_days': 0,
            'log_setting.request_content_enabled': true,
            'log_setting.request_content_max_bytes': 262144,
            'system_task_setting.retention_days': 30,
            ...props.defaultValues,
          }}
        />
      </SettingsPageProvider>
    </>
  )
}

async function renderSettings(defaultValues?: Partial<LogSettingsDefaults>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture defaultValues={defaultValues} />
    </QueryClientProvider>
  )
  return screen.findByRole('spinbutton', {
    name: 'Longest log time range (days)',
  })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/performance/logs'
          ? { enabled: true, log_dir: '/var/log/new-api', file_count: 3 }
          : null,
    },
  }))
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('the page shows the log modules in order, each with its own controls', async () => {
  await renderSettings()

  expect(
    screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)
  ).toEqual([
    'Log recording',
    'Log search and export',
    'Storage retention',
    'System task records',
    'Clean history logs',
    'Server Log Management',
  ])
  const recording = screen.getByRole('group', { name: 'Log recording' })
  expect(within(recording).getAllByRole('switch')).toHaveLength(1)
  expect(
    within(recording).getByRole('switch', { name: 'Record quota usage' })
  ).toBeVisible()
  const search = screen.getByRole('group', { name: 'Log search and export' })
  expect(
    within(search).getByRole('switch', { name: 'Allow users to export logs' })
  ).toBeVisible()
  expect(
    within(search).getByRole('spinbutton', {
      name: 'Longest log time range (days)',
    })
  ).toBeVisible()
  const tasks = screen.getByRole('group', { name: 'System task records' })
  expect(
    within(tasks).getByRole('spinbutton', {
      name: 'Keep finished tasks for (days)',
    })
  ).toHaveValue(30)
  const cleanup = screen.getByRole('group', { name: 'Clean history logs' })
  expect(
    within(cleanup).getByRole('button', { name: 'Clean logs' })
  ).toBeVisible()
  const server = screen.getByRole('group', { name: 'Server Log Management' })
  expect(
    await within(server).findByRole('button', { name: 'Clean Up Log Files' })
  ).toBeVisible()
})

test('confirming a history cleanup starts the task and shows its progress in that module', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        task_id: 'systask_1',
        status: 'running',
        state: { total: 40, processed: 10, progress: 25, remaining: 30 },
      },
    },
  })
  await renderSettings()
  const cleanup = screen.getByRole('group', { name: 'Clean history logs' })

  await user.click(within(cleanup).getByRole('button', { name: 'Clean logs' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete logs',
    })
  )

  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/system-task/log-cleanup',
      null,
      { params: { target_timestamp: expect.any(Number) } }
    )
  )
  expect(
    await within(cleanup).findByText('10 of 40 log entries processed.')
  ).toBeVisible()
  expect(
    within(cleanup).getByRole('button', { name: 'Cleaning...' })
  ).toBeDisabled()
})

test('saving sends only the log export settings that changed', async () => {
  const user = userEvent.setup()
  const days = await renderSettings()
  expect(days).toHaveValue(30)

  await user.click(
    screen.getByRole('switch', { name: 'Allow users to export logs' })
  )
  fireEvent.change(days, { target: { value: '90' } })
  await user.click(screen.getByRole('button', { name: 'Save log settings' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
    options: [
      { key: 'log_setting.user_export_enabled', value: false },
      { key: 'log_setting.max_range_days', value: 90 },
    ],
  })
})

test('a time range under one day shows a field error and is not saved', async () => {
  const user = userEvent.setup()
  const days = await renderSettings()

  fireEvent.change(days, { target: { value: '0' } })
  await user.click(screen.getByRole('button', { name: 'Save log settings' }))

  await waitFor(() => expect(days).toHaveAttribute('aria-invalid', 'true'))
  expect(api.put).not.toHaveBeenCalled()
})

test('retention defaults keep forever and changed content settings save in one batch', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const usage = screen.getByRole('spinbutton', {
    name: 'Usage log retention (days)',
  })
  const content = screen.getByRole('spinbutton', {
    name: 'Request content retention (days)',
  })
  const bytes = screen.getByRole('spinbutton', {
    name: 'Request content limit (bytes)',
  })
  expect(usage).toHaveValue(0)
  expect(content).toHaveValue(0)
  expect(bytes).toHaveValue(262144)
  expect(
    screen.getByRole('switch', { name: 'Capture request content' })
  ).toBeChecked()
  fireEvent.change(usage, { target: { value: '30' } })
  fireEvent.change(content, { target: { value: '7' } })
  fireEvent.change(bytes, { target: { value: '4096' } })
  await user.click(
    screen.getByRole('switch', { name: 'Capture request content' })
  )
  await user.click(screen.getByRole('button', { name: 'Save log settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
    options: [
      { key: 'log_setting.retention_days', value: 30 },
      { key: 'log_setting.request_content_retention_days', value: 7 },
      { key: 'log_setting.request_content_enabled', value: false },
      { key: 'log_setting.request_content_max_bytes', value: 4096 },
    ],
  })
})

test.each([256, 134217728])(
  'saving zero retention and a capture limit of %i preserves explicit boundary values',
  async (limit) => {
    const user = userEvent.setup()
    await renderSettings({
      'log_setting.retention_days': 30,
      'log_setting.request_content_retention_days': 7,
      'log_setting.request_content_enabled': false,
    })
    fireEvent.change(
      screen.getByRole('spinbutton', { name: 'Usage log retention (days)' }),
      { target: { value: '0' } }
    )
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Request content retention (days)',
      }),
      { target: { value: '0' } }
    )
    fireEvent.change(
      screen.getByRole('spinbutton', { name: 'Request content limit (bytes)' }),
      { target: { value: String(limit) } }
    )
    await user.click(
      screen.getByRole('switch', { name: 'Capture request content' })
    )
    await user.click(screen.getByRole('button', { name: 'Save log settings' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
      options: [
        { key: 'log_setting.retention_days', value: 0 },
        { key: 'log_setting.request_content_retention_days', value: 0 },
        { key: 'log_setting.request_content_enabled', value: true },
        { key: 'log_setting.request_content_max_bytes', value: limit },
      ],
    })
  }
)

test.each([
  ['negative usage retention', 'Usage log retention (days)', '-1'],
  ['fractional usage retention', 'Usage log retention (days)', '1.5'],
  ['excessive usage retention', 'Usage log retention (days)', '36501'],
  ['negative content retention', 'Request content retention (days)', '-1'],
  ['fractional content retention', 'Request content retention (days)', '1.5'],
  ['excessive content retention', 'Request content retention (days)', '36501'],
  ['undersized capture limit', 'Request content limit (bytes)', '255'],
  ['fractional capture limit', 'Request content limit (bytes)', '4096.5'],
  ['excessive capture limit', 'Request content limit (bytes)', '134217729'],
])('%s shows a field error without saving', async (_name, label, value) => {
  const user = userEvent.setup()
  await renderSettings()
  const input = screen.getByRole('spinbutton', { name: label })
  fireEvent.change(input, { target: { value } })
  await user.click(screen.getByRole('button', { name: 'Save log settings' }))
  await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
  expect(api.put).not.toHaveBeenCalled()
})

test('pending storage metadata does not block saving and later shows known, zero and estimated sizes', async () => {
  const user = userEvent.setup()
  let releaseMetadata: () => void = () => undefined
  const metadataReady = new Promise<void>((resolve) => {
    releaseMetadata = resolve
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/option/log_storage') {
      await metadataReady
      return {
        data: {
          success: true,
          data: {
            tables: [
              {
                name: 'logs',
                database: 'postgres',
                bytes: 2600000000,
                estimated: false,
              },
              {
                name: 'log_request_details',
                database: 'postgres',
                bytes: 0,
                estimated: false,
              },
              {
                name: 'log_request_content_protections',
                database: 'mysql',
                bytes: 1500,
                estimated: true,
              },
            ],
            capture_enabled: true,
            capture_max_bytes: 4096,
            capture_enabled_override: false,
            capture_max_bytes_override: false,
          },
        },
      }
    }
    return { data: { success: true, data: null } }
  })
  await renderSettings()
  const storage = screen.getByRole('group', { name: 'Storage retention' })
  expect(within(storage).getByText('Loading...')).toBeVisible()
  expect(
    within(storage).queryByText('Storage metadata unavailable')
  ).not.toBeInTheDocument()
  expect(within(storage).queryByText('0 B')).not.toBeInTheDocument()

  fireEvent.change(
    within(storage).getByRole('spinbutton', {
      name: 'Usage log retention (days)',
    }),
    { target: { value: '14' } }
  )
  await user.click(screen.getByRole('button', { name: 'Save log settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
    options: [{ key: 'log_setting.retention_days', value: 14 }],
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save log settings' })
    ).toBeEnabled()
  )
  expect(within(storage).getByText('Loading...')).toBeVisible()

  await act(async () => releaseMetadata())
  expect(await within(storage).findByText('2.6 GB')).toBeVisible()
  expect(within(storage).getByText('0 B')).toBeVisible()
  expect(within(storage).getByText('1.5 KB (Estimated)')).toBeVisible()
  expect(within(storage).queryByText('Loading...')).not.toBeInTheDocument()
  expect(
    within(storage).queryByText(
      'Environment overrides are active on this node. Saved capture settings apply only where no override is configured.'
    )
  ).not.toBeInTheDocument()
  expect(
    within(storage).getByText(
      'Storage sizes may be unavailable or estimated. PostgreSQL deletion makes space reusable; returning it to the OS requires a separate VACUUM FULL or pg_repack operation, never run automatically.'
    )
  ).toBeVisible()
})

test('storage metadata shows unknown bytes and environment override without inventing zero', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/option/log_storage'
          ? {
              tables: [
                {
                  name: 'logs',
                  database: 'sqlite',
                  bytes: null,
                  estimated: false,
                },
              ],
              capture_enabled: false,
              capture_max_bytes: 4096,
              capture_enabled_override: true,
              capture_max_bytes_override: false,
            }
          : null,
    },
  }))
  await renderSettings()
  expect(await screen.findByText('Unknown')).toBeVisible()
  expect(
    screen.getByText(
      'Environment overrides are active on this node. Saved capture settings apply only where no override is configured.'
    )
  ).toBeVisible()
  expect(
    screen.getByText('Effective capture on this node: Disabled, limit 4.1 KB')
  ).toBeVisible()
})

test('failed storage metadata remains unknown and does not block settings', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/option/log_storage') throw new Error('unavailable')
    return { data: { success: true, data: null } }
  })
  await renderSettings()
  expect(await screen.findByText('Storage metadata unavailable')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Save log settings' })
  ).toBeEnabled()
})

test('the task retention saves whole days and rejects less than one day', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const retention = screen.getByRole('spinbutton', {
    name: 'Keep finished tasks for (days)',
  })
  const save = screen.getByRole('button', { name: 'Save log settings' })

  fireEvent.change(retention, { target: { value: '0' } })
  await user.click(save)
  await waitFor(() => expect(retention).toHaveAttribute('aria-invalid', 'true'))
  expect(api.put).not.toHaveBeenCalled()

  fireEvent.change(retention, { target: { value: '7' } })
  await user.click(save)
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
    options: [{ key: 'system_task_setting.retention_days', value: 7 }],
  })
})
