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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'

const queryClients: QueryClient[] = []

function makeLog(other: LogOtherData, type = 5): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type,
    content: 'request rejected',
    username: 'user',
    token_name: 'token',
    model_name: 'gpt-test',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 1,
    channel_name: 'channel',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function renderDetails(
  isAdmin: boolean,
  other: LogOtherData = {
    admin_info: { reject_reason: 'blocked by channel policy' },
  },
  type = 5,
  open = true
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClients.push(queryClient)

  return render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={makeLog(other, type)}
        isAdmin={isAdmin}
        isRoot={false}
        open={open}
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const queryClient of queryClients) {
    queryClient.clear()
  }
  queryClients.length = 0
})

test.each([2, 5])(
  'original request is loaded only by an open admin detail for log type %s',
  async (type) => {
    const content = '<script>window.untrusted = true</script>'
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ref: 'opaque-ref',
          request_id: 'req-1',
          status: 'truncated',
          captured_bytes: 120,
          truncated: true,
          submit_node_name: 'submit-hk',
          payload: {
            messages: [
              { role: 'system', content: 'policy' },
              { role: 'user', content },
            ],
          },
        },
      },
    })
    const metadata = {
      admin_info: {
        node_name: 'processor-us',
        submit_node_name: 'submit-hk',
        request_content: {
          ref: 'opaque-ref',
          status: 'truncated',
          truncated: true,
          captured_bytes: 120,
        },
      },
    }
    const closed = renderDetails(true, metadata, type, false)
    expect(get).not.toHaveBeenCalled()
    closed.unmount()
    const ordinary = renderDetails(false, metadata, type)
    expect(get).not.toHaveBeenCalled()
    expect(screen.queryByText('Original Request')).not.toBeInTheDocument()
    ordinary.unmount()
    const opened = renderDetails(true, metadata, type)
    expect(await screen.findByText(content)).toBeVisible()
    expect(screen.getByText('system')).toBeVisible()
    expect(screen.getByTitle('user')).toBeVisible()
    expect(screen.getByText('processor-us')).toBeVisible()
    expect(screen.getByText('submit-hk')).toBeVisible()
    expect(screen.getByText('Truncated to the configured limit')).toBeVisible()
    expect(document.querySelector('script')).toBeNull()
    expect(get).toHaveBeenCalledWith(
      '/api/log/request/opaque-ref',
      expect.objectContaining({
        signal: expect.any(AbortSignal),
        disableDuplicate: true,
      })
    )
    expect(
      screen.getByRole('button', { name: 'Copy original request' })
    ).toBeEnabled()
    const client = queryClients.at(-1)
    opened.unmount()
    await waitFor(() =>
      expect(
        client?.getQueryState(['log-request-content', 'opaque-ref'])
      ).toBeUndefined()
    )
  }
)

test('unavailable captured body offers retry and legacy metadata never invents a processing node', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockRejectedValueOnce({ response: { status: 404 } })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          ref: 'missing-ref',
          request_id: 'req-1',
          payload: { prompt: 'recovered prompt' },
        },
      },
    })
  renderDetails(true, {
    root_info: { node_name: 'historical-submit' },
    admin_info: { request_content: { ref: 'missing-ref', status: 'recorded' } },
  })
  expect(
    await screen.findByText('Original request content is no longer available')
  ).toBeVisible()
  expect(screen.queryByText('historical-submit')).not.toBeInTheDocument()
  await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('recovered prompt')).toBeVisible()
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
})

describe('usage log reject reason', () => {
  test('shows the nested admin reject reason to admins', () => {
    renderDetails(true)

    expect(screen.getByText('Reject Reason')).toBeInTheDocument()
    expect(screen.getByText('blocked by channel policy')).toBeInTheDocument()
  })

  test('hides the nested admin reject reason from non-admin users', () => {
    renderDetails(false)

    expect(screen.queryByText('Reject Reason')).toBeNull()
    expect(screen.queryByText('blocked by channel policy')).toBeNull()
  })
})

describe.each([2, 5])('request conversion for log type %s', (type) => {
  test.each([
    { name: 'absent metadata', chain: undefined, label: 'Unknown' },
    { name: 'empty metadata', chain: [], label: 'Unknown' },
    {
      name: 'native request',
      chain: ['Claude Messages'],
      label: 'Native format',
    },
    {
      name: 'converted request',
      chain: ['Claude Messages', 'OpenAI Compatible', 'OpenAI Responses'],
      label: 'Claude Messages -> OpenAI Compatible -> OpenAI Responses',
    },
  ])('shows $name accurately', ({ chain, label }) => {
    renderDetails(
      true,
      { request_path: '/v1/messages', request_conversion: chain },
      type
    )

    expect(screen.getByText('Request Conversion')).toBeVisible()
    expect(screen.getByText(label)).toBeVisible()
    if (chain?.length !== 1) {
      expect(screen.queryByText('Native format')).toBeNull()
    }
  })

  test('keeps conversion details admin only', () => {
    renderDetails(
      false,
      {
        request_path: '/v1/messages',
        request_conversion: ['Claude Messages', 'OpenAI Responses'],
      },
      type
    )
    expect(screen.queryByText('Request Conversion')).toBeNull()
  })
})
