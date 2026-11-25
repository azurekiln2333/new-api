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
import { zodResolver } from '@hookform/resolvers/zod'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { z } from 'zod'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { positiveIntegerSchema } from '../../utils/numeric-field'
import { GroupRatioForm } from '../group-ratio-form'
import { GroupSpecialUsableRulesEditor } from '../group-special-usable-editor'
import { RatioSettingsCard } from '../ratio-settings-card'

const originalAuth = useAuthStore.getState().auth

beforeEach(() => {
  useAuthStore.setState({ auth: { ...originalAuth, user: null } })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [{ key: 'GroupLevels', value: '{"default":0,"vip":1}' }],
    },
  })
})
afterEach(() => {
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
})

const defaults = {
  GroupRatio: '{"default":1,"vip":0.8}',
  TopupGroupRatio: '{"vip":1.2}',
  UserUsableGroups: '{"default":"Standard access","vip":"Premium access"}',
  GroupGroupRatio: '{}',
  AutoGroups: '["default","vip"]',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
}

const modelDefaults = {
  ModelPrice: '{}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  ExposeRatioEnabled: false,
  BillingMode: '{}',
  BillingExpr: '{}',
  PluginBillingExpr: '{}',
}

const schema = z.object({
  GroupRatio: z.string(),
  TopupGroupRatio: z.string(),
  UserUsableGroups: z.string(),
  GroupGroupRatio: z.string(),
  AutoGroups: z.string(),
  MaxTokenAutoGroups: positiveIntegerSchema('Enter a positive integer'),
  DefaultUseAutoGroup: z.boolean(),
  GroupSpecialUsableGroup: z.string(),
})

function Fixture(props: {
  onSave?: (values: typeof defaults) => Promise<void>
  initial?: Partial<typeof defaults>
  isSaving?: boolean
}) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: false },
          mutations: { retry: false },
        },
      })
  )
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const form = useForm({
    defaultValues: { ...defaults, ...props.initial },
    resolver: zodResolver(schema),
  })
  return (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <div ref={setActions} />
        <GroupRatioForm
          form={form}
          onSave={props.onSave ?? (async () => {})}
          isSaving={props.isSaving ?? false}
        />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

function RatioCardFixture({
  groupDefaults,
}: {
  groupDefaults: typeof defaults
}) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: false },
          mutations: { retry: false },
        },
      })
  )
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <QueryClientProvider client={client}>
      <SettingsPageProvider actionsContainer={actions}>
        <div ref={setActions} />
        <RatioSettingsCard
          modelDefaults={modelDefaults}
          groupDefaults={groupDefaults}
          toolPricesDefault='{}'
          visibleTabs={['groups']}
        />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
}

describe('group settings workspace', () => {
  it('filters by description and clears search without discarding edits', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    const search = screen.getByRole('textbox', {
      name: 'Search groups by name or description',
    })
    await user.type(search, 'PREMIUM')
    expect(screen.getAllByRole('textbox', { name: 'Group name' })).toHaveLength(
      1
    )
    expect(screen.getByRole('textbox', { name: 'Group name' })).toHaveValue(
      'vip'
    )
    await user.clear(screen.getByRole('spinbutton', { name: 'Ratio' }))
    await user.type(screen.getByRole('spinbutton', { name: 'Ratio' }), '0.6')
    await user.click(screen.getByRole('button', { name: 'Clear search' }))
    expect(screen.getAllByRole('textbox', { name: 'Group name' })).toHaveLength(
      2
    )
    expect(screen.getAllByRole('spinbutton', { name: 'Ratio' })[1]).toHaveValue(
      0.6
    )
    await user.type(search, 'missing')
    expect(screen.getByText('No results found')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Add group' }))
    expect(search).toHaveValue('')
    expect(
      screen
        .getAllByRole<HTMLInputElement>('textbox', { name: 'Group name' })
        .map((input) => input.value)
    ).toEqual(['group_1', 'default', 'vip'])
    expect(screen.getAllByRole('spinbutton', { name: 'Ratio' })[2]).toHaveValue(
      0.6
    )
  })

  it('prepends consecutive new groups and saves them without changing existing settings', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)

    await user.click(screen.getByRole('button', { name: 'Add group' }))
    await user.click(screen.getByRole('button', { name: 'Add group' }))
    expect(
      screen
        .getAllByRole<HTMLInputElement>('textbox', { name: 'Group name' })
        .map((input) => input.value)
    ).toEqual(['group_2', 'group_1', 'default', 'vip'])

    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    const saved = onSave.mock.calls[0][0]
    expect(JSON.parse(saved.GroupRatio)).toEqual({
      group_2: 1,
      group_1: 1,
      default: 1,
      vip: 0.8,
    })
    expect(JSON.parse(saved.UserUsableGroups)).toEqual({
      group_2: '',
      group_1: '',
      default: 'Standard access',
      vip: 'Premium access',
    })
    expect(JSON.parse(saved.TopupGroupRatio)).toEqual({ vip: 1.2 })
    expect(saved.AutoGroups).toBe(defaults.AutoGroups)
  })

  it('keeps unfinished visibility rules while switching sections and supports keyboard tabs', async () => {
    const user = userEvent.setup()
    render(
      <Fixture
        initial={{
          GroupSpecialUsableGroup: '{"vip":{"+:default":"Standard"}}',
        }}
      />
    )
    const pricing = screen.getByRole('tab', { name: 'Pricing groups' })
    pricing.focus()
    await user.keyboard('{ArrowRight}{Enter}')
    expect(
      screen.getByRole('tab', { name: 'Special ratio rules' })
    ).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.queryByRole('table', { name: 'Pricing groups' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    expect(
      screen.getAllByRole('combobox', { name: 'Group name' })
    ).toHaveLength(2)
    await user.click(pricing)
    await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
    expect(
      screen.getAllByRole('combobox', { name: 'Group name' })
    ).toHaveLength(2)
    const toggle = screen.getByRole('button', { name: 'Rules for vip' })
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })

  it('reorders and removes auto groups with named controls and saves across sections', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    const list = screen.getByRole('list', { name: 'Auto group order' })
    expect(
      screen.getByRole('button', { name: 'Move default up' })
    ).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Move vip down' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Move vip up' }))
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent('vip')
    await user.click(
      screen.getByRole('switch', { name: 'Default to auto groups' })
    )
    await user.click(screen.getByRole('button', { name: 'Remove default' }))
    expect(screen.getByRole('button', { name: 'Move vip up' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Move vip down' })).toBeDisabled()
    await user.click(screen.getByRole('tab', { name: 'Pricing groups' }))
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls[0]?.[0]).toEqual(
      expect.objectContaining({
        AutoGroups: '[\n  "vip"\n]',
        DefaultUseAutoGroup: true,
      })
    )
  })

  it('reorders from the drag handle with arrow keys, preserves unknown groups and saves the new order', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(
      <Fixture
        onSave={onSave}
        initial={{
          AutoGroups: '["default","vip","retired"]',
          MaxTokenAutoGroups: 1,
        }}
      />
    )
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    const list = screen.getByRole('list', { name: 'Auto group order' })
    const handle = screen.getByRole('button', {
      name: 'Drag default to reorder',
    })
    handle.focus()
    await user.keyboard('{ArrowUp}')
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent(
      'default'
    )
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}')
    expect(within(list).getAllByRole('listitem')[2]).toHaveTextContent(
      'default'
    )
    expect(handle).toHaveFocus()
    await user.keyboard('{ArrowUp}')
    expect(within(list).getAllByRole('listitem')[1]).toHaveTextContent(
      'default'
    )
    expect(screen.getByText('Not in pricing table')).toBeVisible()
    await user.click(screen.getByRole('tab', { name: 'Pricing groups' }))
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(JSON.parse(onSave.mock.calls[0][0].AutoGroups)).toEqual([
      'vip',
      'default',
      'retired',
    ])
  })

  it('keeps a single auto group unchanged when using its drag handle keyboard controls', async () => {
    const user = userEvent.setup()
    render(<Fixture initial={{ AutoGroups: '["default"]' }} />)
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    const handle = screen.getByRole('button', {
      name: 'Drag default to reorder',
    })
    handle.focus()
    await user.keyboard('{ArrowUp}{ArrowDown}')
    const list = screen.getByRole('list', { name: 'Auto group order' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(1)
    expect(within(list).getByRole('listitem')).toHaveTextContent('default')
    expect(handle).toHaveFocus()
    expect(
      screen.getByRole('button', { name: 'Move default up' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Move default down' })
    ).toBeDisabled()
  })

  it('opens the auto section and focuses its invalid limit when saving from another section', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (_values: typeof defaults) => {})
    render(<Fixture onSave={onSave} />)
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Maximum custom groups per token',
      }),
      { target: { value: '0' } }
    )
    await user.click(screen.getByRole('tab', { name: 'Pricing groups' }))
    await user.click(
      screen.getByRole('button', { name: 'Save group settings' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'Auto group order' })
      ).toHaveAttribute('aria-selected', 'true')
    )
    const input = screen.getByRole('spinbutton', {
      name: 'Maximum custom groups per token',
    })
    expect(input).toHaveAttribute('aria-invalid', 'true')
    await waitFor(() => expect(input).toHaveFocus())
    expect(screen.getByText('Enter a positive integer')).toBeVisible()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows empty state guidance and disables save during a pending request', async () => {
    const user = userEvent.setup()
    render(
      <Fixture
        isSaving
        initial={{
          GroupRatio: '{}',
          TopupGroupRatio: '{}',
          UserUsableGroups: '{}',
          AutoGroups: '[]',
        }}
      />
    )
    expect(
      screen.getByText('No groups yet. Add a group to get started.')
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
    await user.click(screen.getByRole('tab', { name: 'Special ratio rules' }))
    expect(
      screen.getByText('Base group ratios apply until you add an override.')
    ).toBeVisible()
    await user.click(screen.getByRole('tab', { name: 'Auto group order' }))
    expect(screen.getByText('No auto groups configured')).toBeVisible()
  })

  it('preserves pricing edits when switching between visual and JSON editors', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    await user.clear(
      screen.getAllByRole('textbox', { name: 'Group description' })[1]
    )
    await user.type(
      screen.getAllByRole('textbox', { name: 'Group description' })[1],
      'Updated description'
    )
    await user.click(screen.getByRole('button', { name: 'Switch to JSON' }))
    await user.click(screen.getByRole('button', { name: 'Switch to Visual' }))
    expect(
      screen.getAllByRole('textbox', { name: 'Group description' })[1]
    ).toHaveValue('Updated description')
  })
})

it('preserves manual drafts after saving levels and receiving unchanged group defaults', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  const user = userEvent.setup()
  const initial = {
    ...defaults,
    GroupSpecialUsableGroup: '{"vip":{"+:default":"Standard"}}',
  }
  const { rerender } = render(<RatioCardFixture groupDefaults={initial} />)
  await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
  const description = screen.getByRole('textbox', { name: 'Description' })
  await user.clear(description)
  await user.type(description, 'Draft access')
  await user.click(screen.getByRole('button', { name: 'Add rule' }))
  const level = await screen.findByRole('spinbutton', { name: 'Level for vip' })
  fireEvent.change(level, { target: { value: '2' } })
  await user.click(screen.getByRole('button', { name: 'Save group levels' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'GroupLevels',
      value: '{"default":0,"vip":2}',
    })
  )

  rerender(<RatioCardFixture groupDefaults={{ ...initial }} />)
  expect(
    screen
      .getAllByRole<HTMLInputElement>('textbox', { name: 'Description' })
      .map((input) => input.value)
  ).toEqual(['Draft access', ''])
  expect(
    screen.getAllByRole('combobox', { name: 'Group name' })
  ).toHaveLength(2)
  await user.click(screen.getByRole('button', { name: 'Save group settings' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/batch', {
      options: [{
        key: 'group_ratio_setting.group_special_usable_group',
        value: '{"vip":{"+:default":"Draft access"}}',
      }],
    })
  )

  rerender(
    <RatioCardFixture
      groupDefaults={{
        ...initial,
        GroupSpecialUsableGroup:
          '{"vip":{"+:default":"Updated on server"}}',
      }}
    />
  )
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Description' })).toHaveValue(
      'Updated on server'
    )
  )
})

it('loads manual visibility rules received after mount and preserves every rule on edit', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const { rerender } = render(
    <GroupSpecialUsableRulesEditor
      value='{}'
      groupOptions={['default', 'vip', 'svip']}
      onChange={onChange}
    />
  )
  expect(
    screen.getByText('No rules yet. Add a group below to get started.')
  ).toBeVisible()

  rerender(
    <GroupSpecialUsableRulesEditor
      value='{"vip":{"-:default":"remove","+:svip":"Special access"}}'
      groupOptions={['default', 'vip', 'svip']}
      onChange={onChange}
    />
  )
  const description = await screen.findByRole('textbox', {
    name: 'Description',
  })
  expect(description).toHaveValue('Special access')
  expect(screen.getByText('Hidden')).toBeVisible()
  await user.clear(description)
  await user.type(description, 'Updated access')
  expect(JSON.parse(onChange.mock.lastCall?.[0])).toEqual({
    vip: { '-:default': 'remove', '+:svip': 'Updated access' },
  })
})

it('keeps unfinished manual rules when the parent echoes the current serialized value', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const { rerender } = render(
    <GroupSpecialUsableRulesEditor
      value='{"vip":{"default":"Standard"}}'
      groupOptions={['default', 'vip']}
      onChange={onChange}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Add rule' }))
  const echoedValue = onChange.mock.lastCall?.[0]
  expect(JSON.parse(echoedValue)).toEqual({
    vip: { '+:default': 'Standard' },
  })
  rerender(
    <GroupSpecialUsableRulesEditor
      value={echoedValue}
      groupOptions={['default', 'vip']}
      onChange={onChange}
    />
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Group name' })
  ).toHaveLength(2)
})

it('edits group levels, rejects fractions and previews the server permission result', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: ['vip'] },
  })
  render(
    <Fixture
      initial={{
        GroupSpecialUsableGroup: '{"vip":{"-:default":"remove"}}',
      }}
    />
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Group visibility' }))
  const input = await screen.findByRole('spinbutton', { name: 'Level for vip' })
  expect(input).toHaveValue(1)
  fireEvent.change(input, { target: { value: '1.5' } })
  expect(
    screen.getByRole('button', { name: 'Save group levels' })
  ).toBeDisabled()
  fireEvent.change(input, { target: { value: '2' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save group levels' })
  )
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'GroupLevels',
      value: '{"default":0,"vip":2}',
    })
  )
  expect(screen.getByText('Hidden')).toBeVisible()
  expect(api.put).not.toHaveBeenCalledWith(
    '/api/option/',
    expect.objectContaining({
      key: 'group_ratio_setting.group_special_usable_group',
    })
  )
  const select = screen.getByRole('combobox', {
    name: 'Select user group to preview',
  })
  await userEvent.click(select)
  await userEvent.click(screen.getByRole('option', { name: 'vip' }))
  await userEvent.click(
    screen.getByRole('button', { name: 'Preview visible groups' })
  )
  expect(await screen.findByRole('status')).toHaveTextContent('vip')
  expect(api.post).toHaveBeenCalledWith(
    '/api/group/preview',
    expect.objectContaining({
      user_group: 'vip',
      levels: { default: 0, vip: 2 },
      usable_groups: { default: 'Standard access', vip: 'Premium access' },
      special_groups: { vip: { '-:default': 'remove' } },
    })
  )
  fireEvent.change(input, { target: { value: '' } })
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', { name: 'Save group levels' })
  )
  await waitFor(() =>
    expect(api.put).toHaveBeenLastCalledWith('/api/option/', {
      key: 'GroupLevels',
      value: '{"default":0}',
    })
  )
})

it('explains global manual grants and opens their existing pricing controls', async () => {
  const user = userEvent.setup()
  render(<Fixture />)
  await user.click(screen.getByRole('tab', { name: 'Group visibility' }))
  expect(
    await screen.findByText(
      'Global manual grants: default, vip. These groups are available regardless of level unless explicitly hidden. Edit User selectable in Pricing groups to change this list.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Pricing groups' }))
  expect(screen.getByRole('tab', { name: 'Pricing groups' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(screen.getByRole('table', { name: 'Pricing groups' })).toBeVisible()
})
