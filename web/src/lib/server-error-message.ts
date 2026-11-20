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
import { isCancelledError } from '@tanstack/react-query'
import { isCancel } from 'axios'
import i18next from 'i18next'

export const safeServerErrorMessage = Symbol('safeServerErrorMessage')

const serverErrorMessageKeys = {
  TELEGRAM_OAUTH_NOT_CONFIGURED:
    'Telegram OAuth is not configured or enabled. Please contact your administrator.',
  TELEGRAM_OAUTH_CONFLICT:
    'The telegram OAuth provider name is reserved. Ask your administrator to rename the conflicting custom provider.',
  TELEGRAM_OAUTH_FAILED: 'Telegram authorization failed. Please try again.',
  TELEGRAM_ACCOUNT_NOT_BOUND:
    'This Telegram account is not linked. Sign in using another method and link it first.',
  TELEGRAM_LEGACY_AUTH_REMOVED:
    'Telegram login has changed. Reload the page and start Telegram OAuth again.',
  AUTH_INTERNAL_ERROR: 'Please try again later.',
  SECURITY_VERIFICATION_FAILED: 'Verification failed. Please try again.',
  PASSKEY_RP_ID_UNAVAILABLE:
    'This Passkey domain is not available on this website. Use its original website or another verification method.',
  PASSKEY_RP_ID_INVALID:
    'Invalid Passkey domain. Enter a domain without a scheme, port, path or wildcard.',
  SECURITY_VERIFICATION_FLOW_REQUIRED:
    'This verification method requires its dedicated verification flow.',
  SECURITY_VERIFICATION_LOCKED:
    'Two-factor authentication is temporarily locked.',
  OAUTH_ACCOUNT_MISMATCH:
    'The OAuth account does not match the account linked to your profile.',
  TWOFA_CODE_INVALID: 'The authenticator code is incorrect.',
  TWOFA_ALREADY_ENABLED: 'Two-factor authentication is already enabled.',
  TWOFA_NOT_ENABLED: 'Two-factor authentication is not enabled.',
  PASSKEY_NOT_FOUND: 'No Passkey is registered.',
  AUTH_FLOW_INVALID: 'Verification flow expired',
  SECURITY_PROOF_REQUIRED: 'Additional verification required',
  SECURITY_PROOF_EXPIRED:
    'Security verification has expired. Please verify again.',
  SECURITY_PROOF_INVALID:
    'Security verification is no longer valid. Please verify again.',
  SECURITY_PROOF_SCOPE_MISMATCH: 'Verification does not match this action.',
  SECURITY_PROOF_CONSUMED:
    'This verification has already been used. Please verify again.',
  SECURITY_PROOF_CONTEXT_MISMATCH:
    "Verification does not match this action's details. Please verify again.",
  SECURITY_CONTEXT_INVALID: 'The action details are invalid.',
  SECURITY_ACTION_FORBIDDEN:
    'You do not have permission to perform this action.',
  SECURITY_PROOF_METHOD_MISMATCH:
    'This verification method is not allowed for this action.',
  SECURITY_METHOD_UNAVAILABLE:
    'This verification method is currently unavailable.',
  TWOFA_SETUP_INVALID:
    'The two-factor setup has expired or changed. Start setup again.',

  AUTH_SESSION_LIMIT:
    'Too many active login sessions. On a device where you are already signed in, open Login sessions and use “Sign out other sessions” to revoke them. If you cannot access a signed-in device, reset your password to sign out all sessions.',
  AUTH_SESSION_ISSUANCE_LIMIT:
    'Too many login sessions were created recently. Please wait for the rolling window to pass, then try again.',
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
  TELEGRAM_BIND_INTERNAL_ERROR: 'Telegram binding failed. Please try again.',
} as const

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

/** Walk only error origins, never request configs or arbitrary response data. */
export function getServerErrorSources(
  value: unknown
): Record<string | symbol, unknown>[] {
  const sources: Record<string | symbol, unknown>[] = []
  const pending = [value]
  const seen = new Set<object>()
  for (let index = 0; index < pending.length; index++) {
    const source = pending[index]
    if (!isRecord(source) || seen.has(source)) continue
    seen.add(source)
    sources.push(source)
    if (isRecord(source.response)) pending.push(source.response.data)
    pending.push(source.cause, source.error)
  }
  return sources
}

export function getServerErrorMessageKey(value: unknown): string | null {
  for (const source of getServerErrorSources(value)) {
    if (typeof source.code === 'string') {
      const key =
        serverErrorMessageKeys[
          source.code as keyof typeof serverErrorMessageKeys
        ]
      if (key) return key
    }
    if (source[safeServerErrorMessage]) break
  }
  return null
}

export function getServerErrorStatus(value: unknown): number | undefined {
  for (const source of getServerErrorSources(value)) {
    const status = isRecord(source.response)
      ? source.response.status
      : source.status
    if (typeof status === 'number') return status
  }
  return undefined
}

export function isServerErrorCancelled(value: unknown): boolean {
  return getServerErrorSources(value).some(
    (source) =>
      isCancel(source) ||
      isCancelledError(source) ||
      source.name === 'AbortError'
  )
}

function messageText(value: unknown): string | undefined {
  if (
    typeof value !== 'string' ||
    !value.trim() ||
    value.trim() === 'success'
  ) {
    return undefined
  }
  // A proxy's HTML error document is not an actionable API error message.
  if (/^\s*(?:<!doctype|<html[\s>])/i.test(value)) return undefined
  return value
}

/** Only these exact gateway failures are safe to translate in raw error fields. */
function translateFixedChannelManagementError(
  t: (key: string, options?: Record<string, unknown>) => string,
  text: string
): string | undefined {
  switch (text) {
    case 'invalid upstream address':
      return t('invalid upstream address', { skipInterpolation: true })
    case 'channel origin changed during price sync':
      return t('channel origin changed during price sync', {
        skipInterpolation: true,
      })
    case 'failed to read the upstream response':
    case 'upstream returned an unsuccessful pricing response':
    case 'failed to pull model':
    case 'Upstream request failed':
    case 'failed to get Codex client version: Upstream request failed':
      return t('Upstream request failed', { skipInterpolation: true })
    case 'upstream response is too large':
    case 'Response too large':
      return t('Response too large', { skipInterpolation: true })
    case 'channel concurrency settings changed during admission':
      return t('channel concurrency settings changed during admission', {
        skipInterpolation: true,
      })
    case 'channel is no longer enabled':
      return t('channel is no longer enabled', { skipInterpolation: true })
    case 'channel balance plugin changed during admission':
      return t('channel balance plugin changed during admission', {
        skipInterpolation: true,
      })
    case 'channel balance key changed during admission':
      return t('channel balance key changed during admission', {
        skipInterpolation: true,
      })
    case 'channel balance key is no longer enabled':
      return t('channel balance key is no longer enabled', {
        skipInterpolation: true,
      })
    case 'channel balance source changed during the query':
      return t('channel balance source changed during the query', {
        skipInterpolation: true,
      })
    case 'channel configuration changed during model discovery':
    case 'channel credential changed during model discovery':
      return t(
        'Connection settings changed. Fetch models again to refresh the list.',
        { skipInterpolation: true }
      )
    case 'failed to reload channel for management request':
      return t('Failed to fetch channel. Please try again later', {
        skipInterpolation: true,
      })
    case 'channel has no enabled keys':
    case 'no enabled key serves this request':
      return t('No enabled channel key', { skipInterpolation: true })
    case 'the selected model is not available on this channel':
      return t('the selected model is not available on this channel', {
        skipInterpolation: true,
      })
    case 'unsupported endpoint type':
    case 'failed to create request':
    case 'Invalid parameters':
      return t('Invalid parameters', { skipInterpolation: true })
    case 'upstream stream did not complete successfully':
      return t('Stream did not complete successfully', {
        skipInterpolation: true,
      })
    case 'invalid Gemini models response':
    case 'invalid Gemini model name':
    case 'repeated Gemini model page token':
    case 'Gemini model pagination limit exceeded':
    case 'invalid Ollama model inventory':
    case 'invalid OpenAI Models response: data is required':
    case 'invalid OpenAI Models response: model ID is required':
    case 'OpenAI Models response contains no valid model IDs':
    case 'upstream returned an error response':
    case 'failed to parse response':
    case 'failed to parse pull response':
    case 'Invalid response':
    case 'failed to get Codex client version: Invalid response':
      return t('Invalid response', { skipInterpolation: true })
    case 'channel not found':
      return t('Channel does not exist', { skipInterpolation: true })
    case 'failed to refresh Codex channel credential':
      return t('Failed to refresh credentials. Please try again later', {
        skipInterpolation: true,
      })
    case 'Invalid channel header override':
      return t('Invalid channel header override', { skipInterpolation: true })
    case 'Invalid channel proxy':
    case 'failed to get Codex client version: Invalid channel proxy':
      return t('Invalid channel proxy', { skipInterpolation: true })
    default:
      return undefined
  }
}

/** Preserve provider text, including unknown reasons in a gateway key wrapper. */
export function translateChannelManagementError(
  t: (key: string, options?: Record<string, unknown>) => string,
  text: string
): string {
  const translated = translateFixedChannelManagementError(t, text)
  if (translated !== undefined) return translated

  const keyFailure = /^enabled key ([0-9]+) model discovery failed: (.+)$/.exec(
    text
  )
  if (!keyFailure || keyFailure[0] !== text) return text
  const error = translateFixedChannelManagementError(t, keyFailure[2])
  if (error === undefined) return text
  return t('Model discovery failed for enabled key {{index}}: {{error}}', {
    index: keyFailure[1],
    error,
    interpolation: { escapeValue: false },
    nest: false,
  })
}

/**
 * The backend sends web console messages as English source text in
 * `message_key` (a key of the locale files) plus `message_params`, and keeps
 * the rendered English text in `message` for other API callers.
 */
export function getServerMessage(value: unknown): string | undefined {
  if (!isRecord(value)) return undefined
  if (typeof value.message_key === 'string' && value.message_key) {
    const params = isRecord(value.message_params)
      ? value.message_params
      : undefined
    if (
      (value.message_key === 'Failed to fetch models: {{error}}' ||
        value.message_key === 'Failed to fetch Ollama version: {{error}}') &&
      typeof params?.error === 'string'
    ) {
      return i18next.t(value.message_key, {
        ...params,
        error: translateChannelManagementError(i18next.t, params.error),
        interpolation: { escapeValue: false },
        nest: false,
      })
    }
    return i18next.t(value.message_key, params)
  }
  const message = messageText(value.message)
  return message === undefined
    ? undefined
    : translateChannelManagementError(i18next.t, message)
}

/**
 * Translate text the backend stores or sends as an English source key without
 * params (task fail reasons, built-in group descriptions). The same fields also
 * carry upstream errors and administrator text; those have no locale entry and
 * are shown exactly as written, so interpolation and nesting stay off.
 */
export function translateServerText(
  t: (key: string, options?: Record<string, unknown>) => string,
  text: string
): string {
  return t(text, { skipInterpolation: true })
}

export function getServerErrorMessage(
  value: unknown,
  fallback?: string
): string {
  const sources = getServerErrorSources(value)
  // AuthOperationError deliberately hides internal server details. Never unwrap
  // its cause for presentation, even when a caller wraps it in another Error.
  const safe = sources.find((source) => source[safeServerErrorMessage])
  if (safe) {
    return i18next.t(
      getServerErrorMessageKey(safe) ||
        messageText(safe.message) ||
        fallback ||
        'Something went wrong!'
    )
  }
  const key = getServerErrorMessageKey(value)
  if (key) return i18next.t(key)
  for (const source of sources) {
    if (
      source instanceof Error ||
      source.isAxiosError ||
      isRecord(source.response)
    ) {
      continue
    }
    const detail = isRecord(source.error)
      ? messageText(source.error.message)
      : messageText(source.error)
    const message =
      getServerMessage(source) ||
      (detail === undefined
        ? undefined
        : translateChannelManagementError(i18next.t, detail)) ||
      messageText(source.title)
    if (message) return message
  }
  const status = getServerErrorStatus(value)
  if (status === 304) return i18next.t('Content not modified!')
  if (status === 204) return i18next.t('Content not found.')
  for (const source of sources) {
    const message = messageText(source.message)
    if (message) return message
  }
  return messageText(value) || (fallback ?? i18next.t('Something went wrong!'))
}

/** Preserve the payload/cause so every layer can recognize the same failure. */
export function createServerError(value: unknown, fallback?: string): Error {
  return new Error(getServerErrorMessage(value, fallback ?? ''), {
    cause: value,
  })
}

/** Queries reject failed business responses without changing the raw API contract. */
export function requireServerSuccess<T>(response: T): T {
  if (isRecord(response) && response.success === false) {
    throw createServerError(response)
  }
  return response
}
