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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { useEffect, useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { channelsQueryKeys } from '../../lib'
import {
  channelSchema,
  type Channel,
  type ChannelBudgetView,
  type KeyStatus,
} from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { MultiKeyManageDialog } from '../dialogs/multi-key-manage-dialog'

afterEach(async () => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  await i18next.changeLanguage('en')
})

const multiKeyChannel = {
  id: 7,
  type: 61,
  key: '',
  name: 'Account Live',
  status: 1,
  created_time: 1,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  channel_info: {
    is_multi_key: true,
    multi_key_size: 2,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  },
}

function keyStatusResponse(keys: KeyStatus[]) {
  const enabled = keys.filter((key) => key.status === 1).length
  return {
    data: {
      success: true,
      data: {
        keys,
        revision: 10,
        quota_supported: keys.some((key) => key.quota?.ledger_enabled === true),
        total: keys.length,
        page: 1,
        page_size: 10,
        total_pages: 1,
        enabled_count: enabled,
        manual_disabled_count: 0,
        auto_disabled_count: keys.length - enabled,
      },
    },
  }
}

function OpenDialog(props: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return <MultiKeyManageDialog open onOpenChange={() => {}} />
}

test('translates the disabled reasons the server writes and shows upstream text as written', async () => {
  i18next.addResourceBundle('zhCN', 'translation', {
    'Plugin credential needs a new login': '插件凭证需要重新登录',
  })
  await i18next.changeLanguage('zhCN')
  const upstream = 'upstream said {{bad}}'
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        keys: [
          {
            index: 0,
            status: 3,
            disabled_time: 1,
            reason: 'Plugin credential needs a new login',
          },
          { index: 1, status: 3, disabled_time: 1, reason: upstream },
        ],
        total: 2,
        page: 1,
        page_size: 10,
        total_pages: 1,
        enabled_count: 0,
        manual_disabled_count: 0,
        auto_disabled_count: 2,
      },
    },
  })
  const channel = channelSchema.parse(multiKeyChannel)

  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  expect(await screen.findByText('插件凭证需要重新登录')).toBeInTheDocument()
  expect(screen.getByText(upstream)).toBeInTheDocument()
})

/** Opens Manage Keys from inside another dialog, as the channel drawer does. */
function ManageKeysInsideDialog(props: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  const [editOpen, setEditOpen] = useState(true)
  const [manageOpen, setManageOpen] = useState(false)
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return (
    <Dialog open={editOpen} onOpenChange={setEditOpen} title='Edit Channel'>
      <Button onClick={() => setManageOpen(true)}>Manage Keys</Button>
      <MultiKeyManageDialog open={manageOpen} onOpenChange={setManageOpen} />
    </Dialog>
  )
}

test('after a key is deleted, Escape closes Manage Keys and not the dialog that opened it', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const post = vi.spyOn(api, 'post')
  post
    .mockResolvedValueOnce(
      keyStatusResponse([
        { index: 0, status: 1 },
        { index: 1, status: 1 },
      ])
    )
    .mockResolvedValueOnce({ data: { success: true, message: '' } })
    .mockResolvedValueOnce(keyStatusResponse([{ index: 0, status: 1 }]))
  const queryClient = new QueryClient()
  const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
  const user = userEvent.setup()

  render(
    <QueryClientProvider client={queryClient}>
      <ChannelsProvider>
        <ManageKeysInsideDialog
          channel={channelSchema.parse(multiKeyChannel)}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Manage Keys' }))
  const manage = await screen.findByRole('dialog', {
    name: /Multi-Key Management/,
  })
  const row = await within(manage).findByRole('row', { name: /^2\./ })
  await user.click(within(row).getByRole('button', { name: 'Delete' }))
  await user.click(await screen.findByRole('button', { name: 'Continue' }))

  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(
    within(manage).queryByRole('row', { name: /^2\./ })
  ).not.toBeInTheDocument()
  expect(post).toHaveBeenNthCalledWith(
    2,
    '/api/channel/multi_key/manage',
    { channel_id: 7, action: 'delete_key', key_index: 1 },
    expect.anything()
  )
  // The credential panel in the edit drawer shows the lines that are left.
  expect(invalidate).toHaveBeenCalledWith({
    queryKey: channelsQueryKeys.pluginCredentials(7),
  })
  await waitFor(() =>
    expect(manage.contains(document.activeElement)).toBe(true)
  )

  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: /Multi-Key Management/ })
    ).not.toBeInTheDocument()
  )
  expect(screen.getByRole('dialog', { name: 'Edit Channel' })).toBeVisible()
})

function ManageKeysInsideSheet(props: {
  channel: Channel
  onSubmit: () => void
}) {
  const { setCurrentRow } = useChannels()
  const [manageOpen, setManageOpen] = useState(false)
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return (
    <Sheet open>
      <SheetContent>
        <SheetTitle>Edit Channel</SheetTitle>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            props.onSubmit()
          }}
        >
          <Button type='button' onClick={() => setManageOpen(true)}>
            Manage Keys
          </Button>
          <MultiKeyManageDialog
            open={manageOpen}
            onOpenChange={setManageOpen}
          />
        </form>
      </SheetContent>
    </Sheet>
  )
}

const allocationQuota: ChannelBudgetView = {
  scope_type: 'key',
  scope_id: '9007199254740995',
  channel_id: 7,
  ledger_enabled: true,
  enabled: true,
  limit_quota: 1000,
  period_used_quota: 30,
  lifetime_used_quota: 50,
  reserved_quota: 10,
  remaining_quota: 960,
  config_version: 8,
  reset_epoch: 2,
  accounting_started_at: 1,
}

const editableAllocation: KeyStatus = {
  id: '9007199254740995',
  rev: 4,
  index: 1,
  status: 1,
  key_preview: 'same…key',
  quota: allocationQuota,
}

test.each([
  {
    action: 'Set Quota',
    inputRole: 'textbox',
    inputName: 'Local limit (site quota units)',
    value: '2000',
    payload: {
      action: 'set_key_quota',
      expected_config_version: 8,
      enabled: true,
      limit_quota: 2000,
    },
  },
  {
    action: 'Bind User',
    inputRole: 'spinbutton',
    inputName: 'User ID',
    value: '12',
    payload: { action: 'set_key_user', user_id: 12 },
  },
])(
  '$action from a row inside a Sheet opens a focused scrollable editor and saves that allocation',
  async ({ action, inputRole, inputName, value, payload }) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    const keys = [
      { ...editableAllocation, id: '9007199254740993', index: 0, rev: 2 },
      editableAllocation,
    ]
    const post = vi
      .spyOn(api, 'post')
      .mockImplementation(async (_url, body) => {
        if ((body as { action: string }).action === 'get_key_status') {
          return keyStatusResponse(keys)
        }
        return { data: { success: true } }
      })
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelsProvider>
          <ManageKeysInsideSheet
            channel={channelSchema.parse(multiKeyChannel)}
            onSubmit={onSubmit}
          />
        </ChannelsProvider>
      </QueryClientProvider>
    )

    await user.click(screen.getByRole('button', { name: 'Manage Keys' }))
    const manager = await screen.findByRole('dialog', {
      name: /Multi-Key Management/,
    })
    const keyList = within(manager).getByRole('region', { name: 'API Keys' })
    const row = await within(manager).findByRole('row', { name: /^2\./ })
    await user.click(within(row).getByRole('button', { name: action }))

    const editor = await screen.findByRole('dialog', { name: action })
    expect(editor).toHaveClass('overflow-y-auto')
    expect(manager).not.toContainElement(editor)
    expect(keyList).not.toContainElement(editor)
    await waitFor(() =>
      expect(editor.contains(document.activeElement)).toBe(true)
    )
    const input = within(editor).getByRole(inputRole, { name: inputName })
    await user.clear(input)
    await user.type(input, value)
    await user.click(within(editor).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/channel/multi_key/manage',
        {
          channel_id: 7,
          key_entry_id: '9007199254740995',
          expected_rev: 4,
          ...payload,
        },
        expect.objectContaining({ singleUseAuthorization: true })
      )
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: action })
      ).not.toBeInTheDocument()
    )
    await waitFor(() =>
      expect(manager.contains(document.activeElement)).toBe(true)
    )
    expect(onSubmit).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: /Multi-Key Management/ })
      ).not.toBeInTheDocument()
    )
    expect(screen.getByRole('dialog', { name: 'Edit Channel' })).toBeVisible()
  }
)

test('resetting usage from a key row inside a Sheet requires confirmation and preserves allocation identity', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const keys = [
    { ...editableAllocation, id: '9007199254740993', index: 0, rev: 2 },
    editableAllocation,
  ]
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
    if ((body as { action: string }).action === 'get_key_status') {
      return keyStatusResponse(keys)
    }
    return { data: { success: true } }
  })
  const onSubmit = vi.fn()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <ManageKeysInsideSheet
          channel={channelSchema.parse(multiKeyChannel)}
          onSubmit={onSubmit}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Manage Keys' }))
  const manager = await screen.findByRole('dialog', {
    name: /Multi-Key Management/,
  })
  const row = await within(manager).findByRole('row', { name: /^2\./ })
  const reset = within(row).getByRole('button', { name: 'Reset used amount' })
  expect(reset).toBeEnabled()
  await user.click(reset)
  let confirmation = await screen.findByRole('alertdialog', {
    name: 'Reset used amount',
  })
  expect(
    within(confirmation).getByText(
      'Reset current-period usage without changing live holds, lifetime usage or user charges.'
    )
  ).toBeVisible()
  await user.click(within(confirmation).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(post).toHaveBeenCalledTimes(1)

  await user.click(reset)
  confirmation = await screen.findByRole('alertdialog', {
    name: 'Reset used amount',
  })
  await user.click(within(confirmation).getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(post).toHaveBeenNthCalledWith(
    2,
    '/api/channel/multi_key/manage',
    {
      action: 'reset_key_quota',
      channel_id: 7,
      key_entry_id: '9007199254740995',
      expected_rev: 4,
      expected_config_version: 8,
      operation_id: expect.stringMatching(/\S/),
    },
    expect.objectContaining({ singleUseAuthorization: true })
  )
  expect(post).toHaveBeenCalledTimes(3)
  expect(manager).toBeVisible()
  expect(onSubmit).not.toHaveBeenCalled()
})

test.each([false, true])(
  'mapping Save targets only the selected stable Key; restore inheritance is explicit (clear=%s)',
  async (clear) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    const keys: KeyStatus[] = [
      {
        ...editableAllocation,
        id: '9007199254740993',
        index: 0,
        rev: 2,
        model_mapping: '{"alias":"First-Upstream"}',
      },
      { ...editableAllocation, model_mapping: '{"alias":"Second-Upstream"}' },
    ]
    const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
      const request = body as { action: string; model_mapping?: string }
      if (request.action === 'get_key_status') return keyStatusResponse(keys)
      return {
        data: {
          success: true,
          data: {
            revision: 11,
            key_entry_id: editableAllocation.id,
            rev: 5,
            model_mapping: request.model_mapping,
          },
        },
      }
    })
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ChannelsProvider>
          <ManageKeysInsideSheet
            channel={channelSchema.parse({
              ...multiKeyChannel,
              type: 1,
              models: 'alias,shared',
              model_mapping: '{"alias":"Channel-Upstream"}',
            })}
            onSubmit={onSubmit}
          />
        </ChannelsProvider>
      </QueryClientProvider>
    )

    await user.click(screen.getByRole('button', { name: 'Manage Keys' }))
    const manager = await screen.findByRole('dialog', {
      name: /Multi-Key Management/,
    })
    const row = await within(manager).findByRole('row', { name: /^2\./ })
    await user.click(within(row).getByRole('button', { name: 'Model Mapping' }))
    const editor = await screen.findByRole('dialog', { name: 'Model Mapping' })
    expect(
      within(editor).getByRole('combobox', { name: 'Upstream Model Name' })
    ).toHaveValue('Second-Upstream')
    expect(
      within(editor).queryByDisplayValue('Channel-Upstream')
    ).not.toBeInTheDocument()
    expect(
      within(editor).queryByDisplayValue('First-Upstream')
    ).not.toBeInTheDocument()
    if (clear) {
      await user.click(
        within(editor).getByRole('button', { name: 'Restore inheritance' })
      )
      expect(
        within(editor).queryByDisplayValue('Second-Upstream')
      ).not.toBeInTheDocument()
    } else {
      const target = within(editor).getByRole('combobox', {
        name: 'Upstream Model Name',
      })
      await user.clear(target)
      await user.type(target, 'Updated-Upstream')
      await user.tab()
    }
    expect(post).toHaveBeenCalledTimes(1)
    await user.click(within(editor).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/channel/multi_key/manage',
        {
          channel_id: 7,
          action: 'set_key_model_mapping',
          key_entry_id: '9007199254740995',
          expected_rev: 4,
          model_mapping: clear ? '' : '{\n  "alias": "Updated-Upstream"\n}',
        },
        expect.objectContaining({ singleUseAuthorization: true })
      )
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: 'Model Mapping' })
      ).not.toBeInTheDocument()
    )
    expect(onSubmit).not.toHaveBeenCalled()
  }
)

test('switching channels discards a mapping draft and ignores its late conflict', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  let resolveSave!: (value: {
    data: { success: boolean; error_code: string }
  }) => void
  const save = new Promise<{
    data: { success: boolean; error_code: string }
  }>((resolve) => {
    resolveSave = resolve
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
    const request = body as { action: string; channel_id: number }
    if (request.action === 'get_key_status') {
      return keyStatusResponse([
        {
          ...editableAllocation,
          model_mapping:
            request.channel_id === 7 ? '{"alias":"Old-Upstream"}' : '',
        },
      ])
    }
    return save
  })
  const client = new QueryClient()
  const view = (id: number, name: string) => (
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <OpenDialog
          channel={channelSchema.parse({
            ...multiKeyChannel,
            id,
            name,
            type: 1,
          })}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )
  const { rerender } = render(view(7, 'Old Channel'))
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Model Mapping' }))
  const editor = await screen.findByRole('dialog', { name: 'Model Mapping' })
  await user.click(within(editor).getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      expect.objectContaining({ action: 'set_key_model_mapping', channel_id: 7 }),
      expect.anything()
    )
  )
  rerender(view(8, 'Next Channel'))
  await screen.findByRole('dialog', { name: /Next Channel/ })
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: 'Model Mapping' })
    ).not.toBeInTheDocument()
  )
  await act(async () => {
    resolveSave({
      data: { success: false, error_code: 'channel_key_revision_conflict' },
    })
    await save
  })
  expect(screen.getByRole('dialog', { name: /Next Channel/ })).toBeVisible()
  expect(post).toHaveBeenCalledTimes(3)
  await user.click(await screen.findByRole('button', { name: 'Model Mapping' }))
  const nextEditor = await screen.findByRole('dialog', { name: 'Model Mapping' })
  expect(
    within(nextEditor).queryByDisplayValue('Old-Upstream')
  ).not.toBeInTheDocument()
  expect(
    within(nextEditor).getByRole('button', { name: 'Save' })
  ).toBeEnabled()
})

test('changing channels discards an open binding draft and ignores its late save result', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  let resolveSave!: (value: {
    data: { success: boolean; data: { channel_models: string[] } }
  }) => void
  const save = new Promise<{
    data: { success: boolean; data: { channel_models: string[] } }
  }>((resolve) => {
    resolveSave = resolve
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
    if ((body as { action: string }).action === 'get_key_status') {
      return keyStatusResponse([editableAllocation])
    }
    return save
  })
  const client = new QueryClient()
  const firstChannel = channelSchema.parse(multiKeyChannel)
  const nextChannel = channelSchema.parse({
    ...multiKeyChannel,
    id: 8,
    name: 'Next Channel',
  })
  const view = (channel: Channel) => (
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <OpenDialog channel={channel} />
      </ChannelsProvider>
    </QueryClientProvider>
  )
  const { rerender } = render(view(firstChannel))
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Bind User' }))
  const editor = await screen.findByRole('dialog', { name: 'Bind User' })
  await user.type(
    within(editor).getByRole('spinbutton', { name: 'User ID' }),
    '12'
  )
  await user.click(within(editor).getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      expect.objectContaining({ action: 'set_key_user', channel_id: 7 }),
      expect.anything()
    )
  )

  rerender(view(nextChannel))
  await screen.findByRole('dialog', { name: /Next Channel/ })
  expect(
    screen.queryByRole('dialog', { name: 'Bind User' })
  ).not.toBeInTheDocument()
  await act(async () => {
    resolveSave({
      data: { success: true, data: { channel_models: ['old-channel-model'] } },
    })
    await save
  })
  expect(screen.getByRole('dialog', { name: /Next Channel/ })).toBeVisible()
  await user.click(await screen.findByRole('button', { name: 'Bind User' }))
  const nextEditor = await screen.findByRole('dialog', { name: 'Bind User' })
  expect(
    within(nextEditor).getByRole('spinbutton', { name: 'User ID' })
  ).toHaveValue(null)
  expect(
    within(nextEditor).getByRole('button', { name: 'Save' })
  ).toBeDisabled()
  await user.click(within(nextEditor).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: 'Bind User' })
    ).not.toBeInTheDocument()
  )
})

test.each([
  {
    gate: 'sensitive-write permission',
    key: editableAllocation,
    permitted: false,
  },
  {
    gate: 'missing allocation ID',
    key: { ...editableAllocation, id: undefined },
    permitted: true,
  },
  {
    gate: 'missing allocation revision',
    key: { ...editableAllocation, rev: undefined },
    permitted: true,
  },
])('the $gate still disables the quota action', async ({ key, permitted }) => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'operator',
    role: ROLE.ADMIN,
    permissions: {
      admin_permissions: {
        channel: { operate: true, sensitive_write: permitted },
      },
    },
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue(keyStatusResponse([key]))
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channelSchema.parse(multiKeyChannel)} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const quotaAction = await screen.findByRole('button', { name: 'Set Quota' })
  const bindAction = screen.getByRole('button', { name: 'Bind User' })
  expect(quotaAction).toBeDisabled()
  if (permitted) expect(bindAction).toBeEnabled()
  else expect(bindAction).toBeDisabled()
  const user = userEvent.setup()
  await user.click(quotaAction)
  expect(
    screen.queryByRole('dialog', { name: 'Set Quota' })
  ).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('an inactive ledger explains both quota icons without allowing a budget mutation', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const post = vi.spyOn(api, 'post').mockResolvedValue(
    keyStatusResponse([
      {
        ...editableAllocation,
        quota: { ...allocationQuota, ledger_enabled: false },
      },
    ])
  )
  const onSubmit = vi.fn()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <ManageKeysInsideSheet
          channel={channelSchema.parse(multiKeyChannel)}
          onSubmit={onSubmit}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Manage Keys' }))
  const manager = await screen.findByRole('dialog', {
    name: /Multi-Key Management/,
  })
  expect(
    await within(manager).findByText('Channel budget accounting is not enabled')
  ).toBeVisible()
  const quota = within(manager).getByRole('button', { name: 'Set Quota' })
  expect(quota).toBeEnabled()
  await user.click(quota)
  const editor = await screen.findByRole('dialog', { name: 'Set Quota' })
  expect(
    within(editor).getByText('Per-key budget is not available yet')
  ).toBeVisible()
  expect(
    within(editor).getByText('Channel budget accounting is not enabled')
  ).toBeVisible()
  expect(within(editor).queryByRole('button', { name: 'Save' })).toBeNull()
  expect(within(editor).queryByRole('textbox')).toBeNull()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: 'Set Quota' })
    ).not.toBeInTheDocument()
  )

  await waitFor(() =>
    expect(manager.contains(document.activeElement)).toBe(true)
  )
  const reset = within(manager).getByRole('button', {
    name: 'Reset used amount',
  })
  expect(reset).toHaveAttribute('aria-disabled', 'true')
  expect(reset).toHaveAttribute(
    'title',
    'Channel budget accounting is not enabled'
  )
  act(() => quota.focus())
  await user.tab()
  expect(reset).toHaveFocus()
  const tooltip = await screen.findByRole('tooltip')
  expect(tooltip).toHaveTextContent('Channel budget accounting is not enabled')
  await user.keyboard('{Enter}')
  await user.click(reset)
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
  expect(onSubmit).not.toHaveBeenCalled()
})

test('an active budget with no current-period usage explains why reset is unavailable', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const post = vi.spyOn(api, 'post').mockResolvedValue(
    keyStatusResponse([
      {
        ...editableAllocation,
        quota: { ...allocationQuota, period_used_quota: 0 },
      },
    ])
  )
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channelSchema.parse(multiKeyChannel)} />
      </ChannelsProvider>
    </QueryClientProvider>
  )
  const quota = await screen.findByRole('button', { name: 'Set Quota' })
  const reset = screen.getByRole('button', { name: 'Reset used amount' })
  act(() => quota.focus())
  await user.tab()
  expect(reset).toHaveFocus()
  expect(reset).toHaveAttribute('aria-disabled', 'true')
  expect(await screen.findByRole('tooltip')).toHaveTextContent(
    'No used amount to reset'
  )
  await user.keyboard('{Enter}')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('a plugin channel shows each key its own balance', async () => {
  vi.spyOn(api, 'post').mockResolvedValue(
    keyStatusResponse([
      { index: 0, status: 1 },
      { index: 1, status: 1 },
      { index: 2, status: 1 },
    ])
  )
  const channel = channelSchema.parse({
    ...multiKeyChannel,
    channel_info: { ...multiKeyChannel.channel_info, multi_key_size: 3 },
    balance_detail: {
      amount: null,
      currency: 'USD',
      unlimited: false,
      windows: [],
      keys: {
        '0': { amount: 42, currency: 'CNY', unlimited: false, windows: [] },
        '1': {
          amount: null,
          currency: 'USD',
          unlimited: false,
          windows: [{ name: 'Daily', used: 30, limit: 40 }],
          error: 'Key rejected by upstream',
        },
      },
    },
  })

  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const first = await screen.findByRole('row', { name: /^1\./ })
  expect(within(first).getByText('CN¥42')).toBeInTheDocument()
  const second = screen.getByRole('row', { name: /^2\./ })
  expect(within(second).getByText('75% used')).toBeInTheDocument()
  expect(within(second).getByText('Daily: 30 / 40')).toBeInTheDocument()
  expect(
    within(second).getByText('Last error: Key rejected by upstream')
  ).toBeInTheDocument()
  const balanceColumn = screen
    .getAllByRole('columnheader')
    .findIndex((header) => header.textContent === 'Balance')
  const third = screen.getByRole('row', { name: /^3\./ })
  expect(within(third).getAllByRole('cell')[balanceColumn]).toHaveTextContent(
    /^-$/
  )
})
