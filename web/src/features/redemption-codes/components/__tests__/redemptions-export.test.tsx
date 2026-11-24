/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { CodeExportProvider } from '@/components/code-export/code-export-provider'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { RedemptionsMutateDrawer } from '../redemptions-mutate-drawer'
import { RedemptionsProvider } from '../redemptions-provider'

type Download = { filename: string; blob: Blob }

function captureDownloads() {
  const downloads: Download[] = []
  let currentBlob: Blob
  vi.stubGlobal(
    'URL',
    Object.assign(class extends URL {}, {
      createObjectURL: vi.fn((blob: Blob) => {
        currentBlob = blob
        return 'blob:redemption-export'
      }),
      revokeObjectURL: vi.fn(),
    })
  )
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
    this: HTMLAnchorElement
  ) {
    downloads.push({ filename: this.download, blob: currentBlob })
  })
  return downloads
}

function readDownload(download: Download): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.addEventListener('load', () => resolve(String(reader.result)), {
      once: true,
    })
    reader.addEventListener('error', () => reject(reader.error), { once: true })
    reader.readAsText(download.blob)
  })
}

function CreateDrawer() {
  const [open, setOpen] = useState(true)
  return (
    <CodeExportProvider scope='redemption'>
      <RedemptionsProvider>
        <RedemptionsMutateDrawer open={open} onOpenChange={setOpen} />
      </RedemptionsProvider>
    </CodeExportProvider>
  )
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 100 })
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  useAuthStore.getState().auth.setUser(null)
  localStorage.clear()
  sessionStorage.clear()
})

test('successful creation exports the committed batch through the shared server flow', async () => {
  const downloads = captureDownloads()
  const user = userEvent.setup()
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7.2,
    },
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: ['createdA', 'createdB'],
      operation_id: 'operation-123',
      replayed: false,
    },
  })
  const exportedFile = new Blob(
    ['batch\tcreatedA\t¥2,000\nbatch\tcreatedB\t¥2,000\n'],
    { type: 'text/plain;charset=utf-8' }
  )
  const exportRequest = vi.spyOn(api, 'get').mockResolvedValue({
    data: exportedFile,
    headers: { 'content-type': 'text/plain;charset=utf-8' },
  })

  render(<CreateDrawer />)
  const createDialog = screen.getByRole('dialog', {
    name: 'Create Redemption Code',
  })
  fireEvent.change(within(createDialog).getByLabelText('Name'), {
    target: { value: 'batch' },
  })
  fireEvent.change(within(createDialog).getByLabelText('Quantity'), {
    target: { value: '2' },
  })
  fireEvent.change(within(createDialog).getByLabelText('Quota (CNY)'), {
    target: { value: '2000' },
  })
  await user.click(
    within(createDialog).getByRole('button', { name: 'Save changes' })
  )

  const exportDialog = await screen.findByRole('dialog', {
    name: 'Export codes',
  })
  expect(api.post).toHaveBeenCalledWith(
    '/api/redemption/',
    expect.objectContaining({ count: 2, limit_per_user: true }),
    { params: { operation_id: expect.any(String) } }
  )
  expect(exportDialog).toHaveTextContent('Every code in this create batch')
  expect(exportDialog).not.toHaveTextContent('createdA')
  expect(exportDialog).not.toHaveTextContent('createdB')
  expect(
    within(exportDialog).getByRole('checkbox', { name: 'Include quota' })
  ).toBeChecked()

  await user.click(
    within(exportDialog).getByRole('button', { name: 'Download' })
  )
  await waitFor(() => expect(exportRequest).toHaveBeenCalled())
  expect(exportRequest).toHaveBeenCalledWith(
    '/api/redemption/export?operation_id=operation-123&format=txt',
    { responseType: 'blob' }
  )
  expect(downloads).toHaveLength(1)
  expect(downloads[0]?.filename).toMatch(/^redemption-codes-\d+\.txt$/)
  expect(await readDownload(downloads[0])).toBe(
    'batch\tcreatedA\t¥2,000\nbatch\tcreatedB\t¥2,000\n'
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog', { name: 'Export codes' })).toBeNull()
  )
})
