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
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SidebarModulesSection } from '@/features/system-settings/maintenance/sidebar-modules-section'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { updateUserSettings } from '../api'
import { NotificationTab } from '../components/tabs/notification-tab'
import { Profile } from '../index'
import type { UserProfile } from '../types'

const profile: UserProfile = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 1,
  group: 'default',
  quota: 1000000,
  used_quota: 0,
  request_count: 0,
  status: 1,
  aff_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
  created_time: 0,
}
const settings = {
  notify_type: 'webhook',
  quota_warning_threshold: 1200,
  notification_email: '',
  webhook_url: 'https://example.com/notify',
  webhook_secret: 'webhook-secret',
  bark_url: '',
  gotify_url: '',
  gotify_token: '',
  gotify_priority: 5,
  accept_unset_model_ratio_model: true,
  record_ip_log: true,
  upstream_model_update_notify_enabled: true,
}

const clients: QueryClient[] = []

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

/** The body a PUT sends over the wire: undefined fields are left out. */
function sentBody(put: { mock: { calls: unknown[][] } }): unknown {
  return JSON.parse(JSON.stringify(put.mock.calls[0][1]))
}

describe('sidebar personalization card visibility', () => {
  const sidebarModules = JSON.stringify({
    personal: { enabled: true, topup: false, personal: true },
  })

  async function renderPersonalSettings(
    enabled: boolean | undefined,
    role: number = ROLE.USER,
    canConfigure = true
  ) {
    const user = {
      ...profile,
      role,
      permissions: { sidebar_settings: canConfigure },
      sidebar_modules: sidebarModules,
    }
    useAuthStore.getState().auth.setUser(user)
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: user },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    })
    clients.push(client)
    client.setQueryData(['status'], {
      sidebar_personal_settings_enabled: enabled,
      checkin_enabled: true,
      quota_remind_enabled: false,
      SidebarModulesAdmin: '',
    })
    render(
      <QueryClientProvider client={client}>
        <Profile />
      </QueryClientProvider>
    )
    expect(await screen.findByText('Settings & Preferences')).toBeVisible()
    return client
  }

  it.each([
    { name: 'legacy default', enabled: undefined, visible: true },
    { name: 'enabled', enabled: true, visible: true },
    { name: 'disabled', enabled: false, visible: false },
  ])(
    'shows the ordinary user card according to $name',
    async ({ enabled, visible }) => {
      const put = vi.spyOn(api, 'put')
      await renderPersonalSettings(enabled)
      const card = screen.queryByText('Sidebar Personal Settings')
      if (visible) {
        expect(card).toBeVisible()
      } else {
        expect(card).not.toBeInTheDocument()
        expect(document.querySelector('.xl\\:sticky')).toBeNull()
      }
      expect(screen.getByText('Language Preferences')).toBeVisible()
      expect(useAuthStore.getState().auth.user?.sidebar_modules).toBe(
        sidebarModules
      )
      expect(put).not.toHaveBeenCalled()
      expect(screen.queryByText('Daily Check-in')).not.toBeInTheDocument()
      expect(
        vi.mocked(api.get).mock.calls.some(([url]) =>
          String(url).includes('/checkin')
        )
      ).toBe(false)
    }
  )

  it.each([
    { role: ROLE.USER, canConfigure: false, visible: false },
    { role: ROLE.ADMIN, canConfigure: true, visible: true },
    { role: ROLE.SUPER_ADMIN, canConfigure: false, visible: false },
  ])(
    'keeps existing sidebar permission behavior for role $role',
    async ({ role, canConfigure, visible }) => {
      await renderPersonalSettings(false, role, canConfigure)
      const card = screen.queryByText('Sidebar Personal Settings')
      if (visible) expect(card).toBeVisible()
      else expect(card).not.toBeInTheDocument()
    }
  )

  it('hides and restores the whole card without clearing stored preferences', async () => {
    const put = vi.spyOn(api, 'put')
    const client = await renderPersonalSettings(true)
    expect(screen.getByText('Sidebar Personal Settings')).toBeVisible()
    act(() => {
      client.setQueryData(['status'], {
        sidebar_personal_settings_enabled: false,
      })
    })
    await waitFor(() =>
      expect(screen.queryByText('Sidebar Personal Settings')).toBeNull()
    )
    expect(useAuthStore.getState().auth.user?.sidebar_modules).toBe(
      sidebarModules
    )
    act(() => {
      client.setQueryData(['status'], {
        sidebar_personal_settings_enabled: true,
      })
    })
    await waitFor(() =>
      expect(screen.getByText('Sidebar Personal Settings')).toBeVisible()
    )
    expect(useAuthStore.getState().auth.user?.sidebar_modules).toBe(
      sidebarModules
    )
    expect(put).not.toHaveBeenCalled()
  })

  it('saves only the card flag without resetting an unsaved menu draft on a parent refresh', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true },
    })
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    clients.push(client)
    client.setQueryData(['status'], {
      sidebar_personal_settings_enabled: true,
    })
    const user = userEvent.setup()
    const savedModules = { personal: { enabled: true, topup: true } }
    const savedBaseline = JSON.stringify(savedModules)
    const view = render(
      <QueryClientProvider client={client}>
        <SidebarModulesSection
          config={savedModules}
          initialSerialized={savedBaseline}
        />
      </QueryClientProvider>
    )
    const wallet = screen.getByRole('switch', { name: 'Wallet' })
    await user.click(wallet)
    expect(wallet).not.toBeChecked()
    const toggle = screen.getByRole('switch', {
      name: 'Show sidebar personalization card to ordinary users',
    })
    expect(toggle).toBeChecked()
    await user.click(toggle)
    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(put).toHaveBeenCalledTimes(1)
    expect(put).toHaveBeenCalledWith('/api/option/', {
      key: 'console_setting.sidebar_personal_settings_enabled',
      value: false,
    })
    expect(client.getQueryState(['status'])?.isInvalidated).toBe(true)
    view.rerender(
      <QueryClientProvider client={client}>
        <SidebarModulesSection
          config={{ personal: { ...savedModules.personal } }}
          initialSerialized={savedBaseline}
          personalSettingsEnabled={false}
        />
      </QueryClientProvider>
    )
    expect(screen.getByRole('switch', { name: 'Wallet' })).not.toBeChecked()

    const changedModules = {
      personal: { enabled: true, topup: true, personal: false },
    }
    view.rerender(
      <QueryClientProvider client={client}>
        <SidebarModulesSection
          config={changedModules}
          initialSerialized={JSON.stringify(changedModules)}
          personalSettingsEnabled={false}
        />
      </QueryClientProvider>
    )
    expect(screen.getByRole('switch', { name: 'Wallet' })).toBeChecked()
    expect(screen.getByRole('switch', { name: 'Profile' })).not.toBeChecked()
    expect(put).toHaveBeenCalledTimes(1)
  })

  it('keeps the saved visibility on a rejected flag update', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'Save failed' },
    })
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })
    clients.push(client)
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <SidebarModulesSection config={{}} initialSerialized='{}' />
      </QueryClientProvider>
    )
    const toggle = screen.getByRole('switch', {
      name: 'Show sidebar personalization card to ordinary users',
    })
    await user.click(toggle)
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(toggle).toBeEnabled())
    expect(toggle).toBeChecked()
  })
})

describe('user settings saves across profile and security', () => {
  it('disabling IP recording sends the latest complete notification settings', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { ...profile, setting: JSON.stringify(settings) },
      },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    await updateUserSettings({ record_ip_log: false })
    expect(get).toHaveBeenCalledWith('/api/user/self')
    expect(put).toHaveBeenCalledWith('/api/user/setting', {
      ...settings,
      record_ip_log: false,
    })
  })

  it('saving IP recording for a user with no settings supplies the defaults and leaves unchosen switches unset', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { ...profile, role: ROLE.SUPER_ADMIN } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    await updateUserSettings({ record_ip_log: true })
    expect(put.mock.calls[0][0]).toBe('/api/user/setting')
    expect(sentBody(put)).toEqual({
      notify_type: 'email',
      quota_warning_threshold: 0,
      notification_email: '',
      webhook_url: '',
      webhook_secret: '',
      bark_url: '',
      gotify_url: '',
      gotify_token: '',
      gotify_priority: 5,
      accept_unset_model_ratio_model: false,
      record_ip_log: true,
      upstream_model_update_notify_enabled: false,
    })
  })

  it('alternating notification and IP saves retains the latest values from each page', async () => {
    let saved = { ...settings }
    vi.spyOn(api, 'get').mockImplementation(async () => ({
      data: {
        success: true,
        data: { ...profile, setting: JSON.stringify(saved) },
      },
    }))
    vi.spyOn(api, 'put').mockImplementation(async (_url, body) => {
      saved = body as typeof settings
      return { data: { success: true } }
    })
    await updateUserSettings({ record_ip_log: false })
    await updateUserSettings({ quota_warning_threshold: 2500 })
    await updateUserSettings({ record_ip_log: true })
    expect(saved).toEqual({
      ...settings,
      quota_warning_threshold: 2500,
      record_ip_log: true,
    })
  })

  it('a rejected profile read prevents the settings write', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('offline'))
    const put = vi.spyOn(api, 'put')
    await expect(updateUserSettings({ record_ip_log: false })).rejects.toThrow(
      'offline'
    )
    expect(put).not.toHaveBeenCalled()
  })

  it('an unsuccessful profile response prevents the settings write', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: false, message: 'Unavailable' },
    })
    const put = vi.spyOn(api, 'put')
    expect(await updateUserSettings({ record_ip_log: false })).toEqual({
      success: false,
      message: 'Unavailable',
    })
    expect(put).not.toHaveBeenCalled()
  })

  it('an unsuccessful settings write is returned to the caller', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: profile },
    })
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'Save failed' },
    })
    expect(await updateUserSettings({ record_ip_log: true })).toEqual({
      success: false,
      message: 'Save failed',
    })
  })

  it('saving a stale notification form keeps the current IP setting and the notification edits', async () => {
    const onUpdate = vi.fn()
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...profile,
          setting: JSON.stringify({ ...settings, record_ip_log: false }),
        },
      },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(
      <NotificationTab
        profile={{ ...profile, setting: JSON.stringify(settings) }}
        onUpdate={onUpdate}
        quotaRemindEnabled
        quotaRemindThreshold={1000}
      />
    )
    expect(
      screen.queryByRole('switch', { name: 'Record IP Address' })
    ).not.toBeInTheDocument()
    fireEvent.change(
      screen.getByRole('spinbutton', { name: 'Reminder threshold (USD)' }),
      { target: { value: '0.0054' } }
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalled())
    expect(put).toHaveBeenCalledWith('/api/user/setting', {
      ...settings,
      quota_warning_threshold: 2700,
      record_ip_log: false,
    })
  })
})

describe('notifications a user receives', () => {
  function renderNotificationTab(
    role: number,
    setting: Record<string, unknown>,
    quotaRemindEnabled = true
  ) {
    const onUpdate = vi.fn()
    render(
      <NotificationTab
        profile={{ ...profile, role, setting: JSON.stringify(setting) }}
        onUpdate={onUpdate}
        quotaRemindEnabled={quotaRemindEnabled}
        quotaRemindThreshold={1000}
      />
    )
    return onUpdate
  }

  function mockSave(role: number, setting: Record<string, unknown>) {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { ...profile, role, setting: JSON.stringify(setting) },
      },
    })
    return vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  }

  async function save(onUpdate: () => void) {
    fireEvent.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalled())
  }

  it.each([
    { name: 'user', role: ROLE.USER, channel: undefined },
    { name: 'admin', role: ROLE.ADMIN, channel: false },
    { name: 'root', role: ROLE.SUPER_ADMIN, channel: true },
  ])(
    'a $name who never chose sees the reminder on and the channel switch as $channel',
    (row) => {
      renderNotificationTab(row.role, { notify_type: 'email' })
      expect(
        screen.getByRole('switch', { name: 'Low balance reminder' })
      ).toBeChecked()
      const channel = screen.queryByRole('switch', {
        name: 'Channel notifications',
      })
      const upstream = screen.queryByRole('switch', {
        name: 'Upstream Model Updates',
      })
      if (row.channel === undefined) {
        expect(channel).not.toBeInTheDocument()
        expect(upstream).not.toBeInTheDocument()
        return
      }
      expect(channel?.getAttribute('aria-checked')).toBe(String(row.channel))
      expect(upstream).not.toBeChecked()
    }
  )

  it('a user on a site with reminders off sees one line instead of notification settings', () => {
    renderNotificationTab(ROLE.USER, { notify_type: 'email' }, false)
    expect(
      screen.getByText('No notifications are available to you right now')
    ).toBeInTheDocument()
    expect(screen.queryByText('Notification Method')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('switch', { name: 'Low balance reminder' })
    ).not.toBeInTheDocument()
  })

  it('an admin on a site with reminders off keeps only the administrator notifications', () => {
    renderNotificationTab(ROLE.ADMIN, { notify_type: 'email' }, false)
    expect(
      screen.queryByRole('switch', { name: 'Low balance reminder' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('switch', { name: 'Channel notifications' })
    ).toBeInTheDocument()
    expect(screen.getByText('Notification Method')).toBeInTheDocument()
  })

  it('an empty threshold shows the site default and saves as the site default', async () => {
    const put = mockSave(ROLE.USER, { notify_type: 'email' })
    const onUpdate = renderNotificationTab(ROLE.USER, { notify_type: 'email' })
    const threshold = screen.getByRole('spinbutton', {
      name: 'Reminder threshold (USD)',
    })
    expect(threshold).toHaveValue(null)
    expect(threshold).toHaveAttribute('placeholder', '0.002')
    expect(
      screen.getByText('Leave empty to use the site default ($0.002)')
    ).toBeInTheDocument()
    await save(onUpdate)
    expect(sentBody(put)).toMatchObject({ quota_warning_threshold: 0 })
  })

  it('an untouched threshold is saved as stored, in quota units', async () => {
    const setting = { notify_type: 'email', quota_warning_threshold: 1234 }
    const put = mockSave(ROLE.USER, setting)
    const onUpdate = renderNotificationTab(ROLE.USER, setting)
    expect(
      screen.getByRole('spinbutton', { name: 'Reminder threshold (USD)' })
    ).toHaveValue(0.002468)
    await save(onUpdate)
    expect(sentBody(put)).toMatchObject({ quota_warning_threshold: 1234 })
  })

  it('turning the reminder off hides its threshold and saves the switch', async () => {
    const put = mockSave(ROLE.USER, { notify_type: 'email' })
    const onUpdate = renderNotificationTab(ROLE.USER, { notify_type: 'email' })
    fireEvent.click(
      screen.getByRole('switch', { name: 'Low balance reminder' })
    )
    expect(
      screen.queryByRole('spinbutton', { name: 'Reminder threshold (USD)' })
    ).not.toBeInTheDocument()
    await save(onUpdate)
    expect(sentBody(put)).toMatchObject({ quota_warning_enabled: false })
  })

  it('switches root never touched stay unset when it saves', async () => {
    const put = mockSave(ROLE.SUPER_ADMIN, { notify_type: 'email' })
    const onUpdate = renderNotificationTab(ROLE.SUPER_ADMIN, {
      notify_type: 'email',
    })
    await save(onUpdate)
    expect(sentBody(put)).not.toHaveProperty('channel_notify_enabled')
    expect(sentBody(put)).not.toHaveProperty('quota_warning_enabled')
  })

  it('an admin turning channel notifications on saves the switch', async () => {
    const put = mockSave(ROLE.ADMIN, { notify_type: 'email' })
    const onUpdate = renderNotificationTab(ROLE.ADMIN, { notify_type: 'email' })
    fireEvent.click(
      screen.getByRole('switch', { name: 'Channel notifications' })
    )
    await save(onUpdate)
    expect(sentBody(put)).toMatchObject({ channel_notify_enabled: true })
  })
})
