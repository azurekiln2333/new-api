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
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, test } from 'vitest'

import zh from '@/i18n/locales/zh.json'

import {
  getServerErrorMessage,
  getServerErrorMessageKey,
  getServerMessage,
  translateChannelManagementError,
  translateServerText,
} from './server-error-message'

describe('server error message mapping', () => {
  test('maps the active-session limit to recovery instructions', () => {
    const message = getServerErrorMessageKey({ code: 'AUTH_SESSION_LIMIT' })

    expect(message ?? '').toMatch(/Sign out other sessions/)
    expect(message ?? '').toMatch(/reset your password/)
  })

  test('maps an Axios-shaped issuance limit to rolling-window guidance', () => {
    const message = getServerErrorMessageKey({
      response: { data: { code: 'AUTH_SESSION_ISSUANCE_LIMIT' } },
    })

    expect(message ?? '').toMatch(/rolling window/)
    expect(getServerErrorMessageKey({ code: 'UNKNOWN_CODE' })).toBe(null)
  })

  test('maps stable Telegram bind errors without exposing server text', () => {
    const expected = {
      TELEGRAM_BIND_DISABLED: 'Telegram binding is disabled.',
      TELEGRAM_BIND_INVALID_REQUEST:
        'The Telegram authorization request is invalid or expired.',
      TELEGRAM_BIND_FLOW_INVALID:
        'This Telegram binding request has expired or has already been used.',
      TELEGRAM_BIND_SESSION_INVALID:
        'The login session that started this Telegram binding is no longer valid.',
      TELEGRAM_BIND_ALREADY_BOUND: 'This Telegram account is already bound.',
      TELEGRAM_BIND_USER_DELETED: 'This user account no longer exists.',
      TELEGRAM_BIND_USER_DISABLED: 'This user account is disabled.',
      TELEGRAM_BIND_INTERNAL_ERROR:
        'Telegram binding failed. Please try again.',
    }

    for (const [code, message] of Object.entries(expected)) {
      expect(getServerErrorMessageKey({ code })).toBe(message)
    }

    expect(
      getServerErrorMessageKey({
        response: {
          data: { code: 'TELEGRAM_BIND_INTERNAL_ERROR', message: 'raw detail' },
        },
      })
    ).toBe(expected.TELEGRAM_BIND_INTERNAL_ERROR)
  })

  test('translates message_key with message_params in the active language', async () => {
    i18next.addResourceBundle('zhCN', 'translation', {
      'Quota value exceeds valid range, maximum is {{max}}':
        '额度超出有效范围，最大为 {{max}}',
    })
    await i18next.changeLanguage('zhCN')
    try {
      const data = {
        success: false,
        message: 'Quota value exceeds valid range, maximum is 100',
        message_key: 'Quota value exceeds valid range, maximum is {{max}}',
        message_params: { max: 100 },
      }

      expect(getServerErrorMessage(data)).toBe('额度超出有效范围，最大为 100')
      expect(
        getServerErrorMessage({ isAxiosError: true, response: { data } })
      ).toBe('额度超出有效范围，最大为 100')
    } finally {
      await i18next.changeLanguage('en')
    }
  })

  test('renders an untranslated message_key as English text', () => {
    expect(
      getServerErrorMessage({
        success: false,
        message: 'Channel 7 does not exist',
        message_key: 'Channel {{id}} does not exist',
        message_params: { id: 7 },
      })
    ).toBe('Channel 7 does not exist')
  })

  test('translates a backend source key and leaves upstream text as written', async () => {
    i18next.addResourceBundle('zhCN', 'translation', {
      'Task timed out': '任务超时',
    })
    await i18next.changeLanguage('zhCN')
    try {
      expect(translateServerText(i18next.t, 'Task timed out')).toBe('任务超时')
      const upstream = 'upstream said {{bad}} and $t(Task timed out)'
      expect(translateServerText(i18next.t, upstream)).toBe(upstream)
    } finally {
      await i18next.changeLanguage('en')
    }
  })
})

describe('channel management error translation', () => {
  let originalLanguage: string
  let originalResources: Record<string, string> | undefined

  beforeEach(async () => {
    originalLanguage = i18next.language
    originalResources = i18next.hasResourceBundle('zhCN', 'translation')
      ? structuredClone(i18next.getResourceBundle('zhCN', 'translation'))
      : undefined
    i18next.addResourceBundle('zhCN', 'translation', zh.translation)
    await i18next.changeLanguage('zhCN')
  })

  afterEach(async () => {
    i18next.removeResourceBundle('zhCN', 'translation')
    if (originalResources) {
      i18next.addResourceBundle('zhCN', 'translation', originalResources)
    }
    await i18next.changeLanguage(originalLanguage)
  })

  test('translates fixed raw gateway failures in the active language', () => {
    const messages = [
      ['invalid upstream address', '上游地址无效'],
      [
        'channel origin changed during price sync',
        '价格同步期间渠道源站已更改',
      ],
      ['failed to read the upstream response', '上游请求失败'],
      ['upstream response is too large', '响应过大'],
      ['upstream returned an unsuccessful pricing response', '上游请求失败'],
      [
        'channel concurrency settings changed during admission',
        '等待准入期间渠道并发设置已更改',
      ],
      ['channel is no longer enabled', '渠道已停用'],
      [
        'channel balance plugin changed during admission',
        '等待准入期间渠道余额插件已更改',
      ],
      [
        'channel balance key changed during admission',
        '等待准入期间渠道余额查询密钥已更改',
      ],
      ['channel balance key is no longer enabled', '渠道余额查询密钥已停用'],
      [
        'channel balance source changed during the query',
        '查询期间渠道余额来源已更改',
      ],
    ]

    for (const [message, expected] of messages) {
      expect(
        getServerErrorMessage({
          response: { data: { success: false, message } },
        })
      ).toBe(expected)
    }
  })

  test('preserves unknown provider text even when it has a locale entry', () => {
    for (const message of [
      'Task timed out',
      'invalid upstream address: provider detail',
      ' invalid upstream address ',
      'provider returned {{secret}} and $t(Task timed out)',
    ]) {
      expect(getServerMessage({ message })).toBe(message)
      expect(translateChannelManagementError(i18next.t, message)).toBe(message)
    }
  })

  test('keeps structured message keys ahead of raw gateway failures', () => {
    expect(
      getServerMessage({
        message: 'invalid upstream address',
        message_key: 'Task timed out',
      })
    ).toBe('任务超时')
  })
})
