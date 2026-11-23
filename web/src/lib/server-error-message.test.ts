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
  test('translates empty HTTP 429 responses and preserves specific session limits', async () => {
    i18next.addResourceBundle('zhCN', 'translation', {
      'Too many requests': zh.translation['Too many requests'],
    })
    await i18next.changeLanguage('zhCN')
    try {
      const error = {
        isAxiosError: true,
        message: 'Request failed with status code 429',
        response: { status: 429, data: '' },
      }
      expect(
        getServerErrorMessage(
          new Error('Registration failed', { cause: error })
        )
      ).toBe(zh.translation['Too many requests'])
      expect(
        getServerErrorMessage({
          ...error,
          response: {
            status: 429,
            data: { code: 'AUTH_SESSION_ISSUANCE_LIMIT' },
          },
        })
      ).toBe(
        i18next.t(
          getServerErrorMessageKey({ code: 'AUTH_SESSION_ISSUANCE_LIMIT' }) ??
            ''
        )
      )
    } finally {
      await i18next.changeLanguage('en')
    }
  })

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
      expect(getServerErrorMessage({ error: message })).toBe(message)
      expect(translateChannelManagementError(i18next.t, message)).toBe(message)
    }
  })

  test('translates fixed discovery and diagnostic failures through raw error details', () => {
    const messages = [
      [
        'channel configuration changed during model discovery',
        '连接配置已更改，请重新获取模型列表。',
      ],
      [
        'channel credential changed during model discovery',
        '连接配置已更改，请重新获取模型列表。',
      ],
      [
        'failed to reload channel for management request',
        '获取渠道信息失败，请稍后重试',
      ],
      ['channel has no enabled keys', '没有可用的渠道密钥'],
      ['no enabled key serves this request', '没有可用的渠道密钥'],
      [
        'the selected model is not available on this channel',
        '所选模型在该渠道不可用',
      ],
      ['unsupported endpoint type', '无效的参数'],
      ['upstream stream did not complete successfully', '流式输出未成功完成'],
      ['invalid Gemini models response', '响应无效'],
      ['invalid Gemini model name', '响应无效'],
      ['repeated Gemini model page token', '响应无效'],
      ['Gemini model pagination limit exceeded', '响应无效'],
      ['invalid Ollama model inventory', '响应无效'],
      ['invalid OpenAI Models response: data is required', '响应无效'],
      ['invalid OpenAI Models response: model ID is required', '响应无效'],
      ['OpenAI Models response contains no valid model IDs', '响应无效'],
      ['upstream returned an error response', '响应无效'],
      ['failed to parse response', '响应无效'],
      ['failed to parse pull response', '响应无效'],
      ['failed to pull model', '上游请求失败'],
      ['failed to create request', '无效的参数'],
      ['channel not found', '渠道不存在'],
      [
        'failed to refresh Codex channel credential',
        '刷新凭证失败，请稍后重试',
      ],
      ['Invalid response', '响应无效'],
      ['Response too large', '响应过大'],
      ['Invalid parameters', '无效的参数'],
      ['Invalid channel header override', '渠道请求头覆盖配置无效'],
      ['Invalid channel proxy', '渠道代理配置无效'],
      ['Upstream request failed', '上游请求失败'],
      [
        'failed to get Codex client version: Invalid channel proxy',
        '渠道代理配置无效',
      ],
      [
        'failed to get Codex client version: Upstream request failed',
        '上游请求失败',
      ],
      ['failed to get Codex client version: Invalid response', '响应无效'],
    ]

    for (const [message, expected] of messages) {
      expect(getServerMessage({ message })).toBe(expected)
      expect(getServerErrorMessage({ error: message })).toBe(expected)
    }
  })

  test('translates known nested reasons only inside discovery and version message keys', () => {
    const response = {
      message: 'invalid upstream address',
      message_key: 'Failed to fetch models: {{error}}',
      message_params: { error: 'invalid Gemini models response' },
    }

    expect(getServerMessage(response)).toBe('获取模型列表失败：响应无效')
    expect(
      getServerMessage({
        ...response,
        message_key: 'Failed to fetch Ollama version: {{error}}',
        message_params: { error: 'upstream response is too large' },
      })
    ).toBe('获取 Ollama 版本失败：响应过大')
    expect(
      getServerMessage({
        ...response,
        message_key: 'Invalid request parameters: {{error}}',
      })
    ).toBe(
      i18next.t('Invalid request parameters: {{error}}', {
        error: 'invalid Gemini models response',
      })
    )
    expect(response.message_params.error).toBe('invalid Gemini models response')
  })

  test('translates an enabled-key wrapper only when its suffix is an exact gateway failure', () => {
    const message =
      'enabled key 2 model discovery failed: channel credential changed during model discovery'
    const expected =
      '已启用密钥 2 的模型发现失败：连接配置已更改，请重新获取模型列表。'

    expect(getServerMessage({ message })).toBe(expected)
    expect(
      getServerMessage({
        message_key: 'Failed to fetch models: {{error}}',
        message_params: { error: message },
      })
    ).toBe(`获取模型列表失败：${expected}`)

    for (const providerText of [
      'enabled key 2 model discovery failed: Task timed out',
      'enabled key 2 model discovery failed: invalid upstream address: detail',
      'invalid OpenAI Models response: provider detail',
      'upstream returned an error response: provider detail',
      'enabled key 2 model discovery failed: invalid upstream address ',
      'enabled key 2 model discovery failed: invalid upstream address\n',
      'enabled key -1 model discovery failed: invalid upstream address',
      'enabled key 1.5 model discovery failed: invalid upstream address',
      ' enabled key 2 model discovery failed: invalid upstream address',
    ]) {
      expect(translateChannelManagementError(i18next.t, providerText)).toBe(
        providerText
      )
    }
  })

  test('recognizes an exact gateway suffix even when English translation matches its source', async () => {
    await i18next.changeLanguage('en')

    expect(
      translateChannelManagementError(
        i18next.t,
        'enabled key 0 model discovery failed: Invalid response'
      )
    ).toBe('Model discovery failed for enabled key 0: Invalid response')
  })

  test('preserves arbitrary provider details in raw errors and recognized structured wrappers', () => {
    const message =
      ' Task timed out: provider <detail> {{secret}} and $t(Task timed out) '

    expect(getServerErrorMessage({ error: message })).toBe(message)
    expect(getServerErrorMessage({ error: { message } })).toBe(message)
    expect(
      getServerMessage({
        message_key: 'Failed to fetch models: {{error}}',
        message_params: { error: message },
      })
    ).toBe(`获取模型列表失败：${message}`)
    expect(
      getServerMessage({
        message_key: 'Failed to fetch Ollama version: {{error}}',
        message_params: { error: message },
      })
    ).toBe(`获取 Ollama 版本失败：${message}`)
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
