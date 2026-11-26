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
import {
  createDecipheriv,
  generateKeyPairSync,
  privateDecrypt,
  webcrypto,
} from 'node:crypto'

import { waitFor } from '@testing-library/react'
import { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  AUTH_REQUEST_TIMEOUT_MS,
  refreshAuthentication,
} from '@/lib/auth-session'
import { AuthOperationError, authResult } from '@/lib/secure-verification'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { createOAuthFlow } from '../../api'
import { OAUTH_POPUP_CALLBACK_MESSAGE } from '../../constants'
import {
  clearPasswordEncryptionCache,
  encryptPassword,
} from '../../lib/password-encryption'
import { checkVerificationMethods, verify } from '../api'
import type { SecurityProof, VerificationInput } from '../types'

const originalAdapter = api.defaults.adapter
const originalLocation = window.location.href

it('allows a security key when the browser has WebAuthn but no platform authenticator', async () => {
  vi.stubGlobal(
    'PublicKeyCredential',
    class {
      static isUserVerifyingPlatformAuthenticatorAvailable() {
        return Promise.resolve(false)
      }
    }
  )
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        scope: 'passkey.delete',
        methods: [{ method: 'passkey', available: true }],
        oauth_providers: [],
        password_encryption_enabled: false,
      },
    },
  })
  const requirements = await checkVerificationMethods('passkey.delete')
  expect(requirements.methods).toEqual([{ method: 'passkey', available: true }])
})

it.each([false, true])(
  'retains the Telegram verification request after popup close and honors caller cancellation: %s',
  async (cancel) => {
    const popup = {
      closed: false,
      location: { replace: vi.fn() },
      sessionStorage: window.sessionStorage,
      close: vi.fn(),
      postMessage: vi.fn(),
    }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          flow_token: 'verification-state',
          authorization_url: 'https://oauth.telegram.org/auth?server=pkce',
        },
      },
    })
    const proof: SecurityProof = {
      proof_token: 'telegram-proof',
      method: 'oauth',
      scope: '2fa.setup',
      expires_at: Math.floor(Date.now() / 1000) + 300,
    }
    let resolve!: (response: {
      data: { success: boolean; data: SecurityProof }
    }) => void
    const response = new Promise<{
      data: { success: boolean; data: SecurityProof }
    }>((done) => {
      resolve = done
    })
    const get = vi
      .spyOn(api, 'get')
      .mockImplementation((url) =>
        url === '/api/status'
          ? Promise.resolve({ data: { success: true, data: {} } })
          : response
      )
    const controller = new AbortController()
    const result = verify(
      { method: 'oauth', provider: 'telegram' },
      { scope: '2fa.setup' },
      false,
      controller.signal
    )
    const outcome = cancel
      ? expect(result).rejects.toMatchObject({ code: 'AUTH_CANCELLED' })
      : expect(result).resolves.toEqual(proof)
    await waitFor(() =>
      expect(popup.location.replace).toHaveBeenCalledWith(
        'https://oauth.telegram.org/auth?server=pkce'
      )
    )
    const event = new MessageEvent('message', {
      origin: window.location.origin,
      data: {
        type: OAUTH_POPUP_CALLBACK_MESSAGE,
        intent: 'verify',
        provider: 'telegram',
        state: 'verification-state',
        code: 'code',
      },
    })
    Object.defineProperty(event, 'source', { value: popup })
    window.dispatchEvent(event)
    popup.closed = true
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        '/api/oauth/telegram',
        expect.objectContaining({ singleUseAuthorization: true })
      )
    )
    const signal = get.mock.calls.find(
      ([url]) => url === '/api/oauth/telegram'
    )?.[1]?.signal
    expect(signal?.aborted).toBe(false)
    if (cancel) {
      controller.abort(new AuthOperationError('Cancelled', 'AUTH_CANCELLED'))
    }
    expect(signal?.aborted).toBe(cancel)
    resolve({ data: { success: true, data: proof } })
    await outcome
  }
)
const sessionBundle = {
  access_token: 'access-token',
  token_type: 'Bearer' as const,
  access_expires_at: Math.floor(Date.now() / 1000) + 600,
  user: { id: 42, username: 'user', role: 1 },
  session: {
    sid: 'session',
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'test',
    created_at: 1,
    last_active_at: 1,
    expires_at: Math.floor(Date.now() / 1000) + 3600,
  },
}

function mockRefreshResponse(bundle: AuthBundle, onRequest?: () => void) {
  const requests = vi.fn()
  vi.stubGlobal(
    'XMLHttpRequest',
    class {
      status = 200
      statusText = 'OK'
      readyState = 4
      responseText = JSON.stringify({ success: true, data: bundle })
      onloadend: (() => void) | null = null
      url = ''
      open(_method: string, url: string) {
        this.url = url
      }
      setRequestHeader() {}
      getAllResponseHeaders() {
        return 'content-type: application/json'
      }
      abort() {}
      send() {
        requests(this.url)
        onRequest?.()
        queueMicrotask(() => this.onloadend?.())
      }
    }
  )
  return requests
}

afterEach(() => {
  clearPasswordEncryptionCache()
  vi.restoreAllMocks()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset('idle')
  window.history.replaceState(null, '', originalLocation)
})

it.each([true, false])(
  'encrypts short and 128-character Unicode passwords with Web Crypto enabled: %s',
  async (useWebCrypto) => {
    vi.stubGlobal(
      'crypto',
      useWebCrypto
        ? webcrypto
        : { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) }
    )
    const { publicKey, privateKey } = generateKeyPairSync('rsa', {
      modulusLength: 2048,
    })
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          kid: 'test-encryption-key',
          public_key: publicKey
            .export({ type: 'spki', format: 'pem' })
            .toString(),
        },
      },
    })
    for (const password of ['legacy-password', '🔒'.repeat(128)]) {
      const encrypted = await encryptPassword(password)
      const parts = encrypted.password_encrypted.split('.')
      let decrypted: Buffer
      if (parts[0] === 'v2') {
        const key = privateDecrypt(
          {
            key: privateKey,
            oaepHash: 'sha256',
            oaepLabel: Buffer.from('password-v2'),
          },
          Buffer.from(parts[1], 'base64')
        )
        const ciphertext = Buffer.from(parts[3], 'base64')
        const decipher = createDecipheriv(
          'aes-256-gcm',
          key,
          Buffer.from(parts[2], 'base64')
        )
        decipher.setAAD(Buffer.from('password-v2:test-encryption-key'))
        decipher.setAuthTag(ciphertext.subarray(-16))
        decrypted = Buffer.concat([
          decipher.update(ciphertext.subarray(0, -16)),
          decipher.final(),
        ])
      } else {
        decrypted = privateDecrypt(
          { key: privateKey, oaepHash: 'sha256' },
          Buffer.from(encrypted.password_encrypted, 'base64')
        )
      }
      expect(decrypted.toString('utf8')).toBe(password)
    }
  }
)

it.each(['proof', 'flow'] as const)(
  'never replays a %s request after a 401 response',
  async (kind) => {
    window.history.replaceState(null, '', '/sign-in')
    useAuthStore.getState().auth.setBundle(sessionBundle)
    const refresh = mockRefreshResponse(sessionBundle)
    const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
      throw new AxiosError(
        'Unauthorized',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        {
          status: 401,
          statusText: 'Unauthorized',
          config,
          headers: {},
          data: { code: 'AUTH_SESSION_REVOKED' },
        }
      )
    })
    api.defaults.adapter = adapter
    await expect(
      api.post(
        '/protected',
        {},
        {
          skipErrorHandler: true,
          ...(kind === 'proof'
            ? { headers: { 'X-Security-Proof': 'one-use-proof' } }
            : { singleUseAuthorization: true }),
        }
      )
    ).rejects.toThrow('Unauthorized')
    expect(adapter).toHaveBeenCalledTimes(1)
    expect(refresh).not.toHaveBeenCalled()
  }
)

it('refreshes an expiring login token before submitting a one-time proof', async () => {
  useAuthStore.getState().auth.setBundle({
    ...sessionBundle,
    access_expires_at: Math.floor(Date.now() / 1000) + 10,
  })
  const order: string[] = []
  mockRefreshResponse({ ...sessionBundle, access_token: 'fresh-access' }, () =>
    order.push('refresh')
  )
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    order.push('action')
    return {
      status: 200,
      statusText: 'OK',
      config,
      headers: {},
      data: { success: true },
    }
  })
  api.defaults.adapter = adapter
  await api.post(
    '/protected',
    {},
    { headers: { 'X-Security-Proof': 'one-use-proof' } }
  )
  expect(order).toEqual(['refresh', 'action'])
  expect(adapter).toHaveBeenCalledTimes(1)
  expect(adapter.mock.calls[0]?.[0].headers.Authorization).toBe(
    'Bearer fresh-access'
  )
})

it.each(['password', '2fa', 'session'] as const)(
  'does not refresh or replay a %s verification submission after a 401',
  async (method) => {
    useAuthStore.getState().auth.setBundle(sessionBundle)
    const refresh = mockRefreshResponse(sessionBundle)
    const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
      throw new AxiosError('Unauthorized', 'ERR_BAD_REQUEST', config, undefined, {
        status: 401,
        statusText: 'Unauthorized',
        config,
        headers: {},
        data: { code: 'AUTH_SESSION_REVOKED' },
      })
    })
    api.defaults.adapter = adapter
    let input: VerificationInput = { method: 'session' }
    if (method === 'password') {
      input = { method, password: 'one-time-password' }
    } else if (method === '2fa') {
      input = { method, code: '123456' }
    }
    await expect(
      verify(
        input,
        {
          scope: 'admin.user.manage',
          context: { user_id: 2, action: 'disable' },
        },
        false,
        new AbortController().signal
      )
    ).rejects.toBeInstanceOf(Error)
    expect(adapter).toHaveBeenCalledTimes(1)
    expect(adapter.mock.calls[0][0].singleUseAuthorization).toBe(true)
    expect(refresh).not.toHaveBeenCalled()
  }
)

it('cancels one pre-submit refresh waiter without cancelling another or sending the cancelled proof', async () => {
  useAuthStore.getState().auth.setBundle({
    ...sessionBundle,
    access_expires_at: Math.floor(Date.now() / 1000) + 10,
  })
  let finishRefresh: (() => void) | undefined
  const send = vi
    .spyOn(XMLHttpRequest.prototype, 'send')
    .mockImplementation(function (this: XMLHttpRequest) {
      finishRefresh = () => {
        Object.defineProperties(this, {
          status: { value: 200, configurable: true },
          statusText: { value: 'OK', configurable: true },
          responseText: {
            value: JSON.stringify({
              success: true,
              data: { ...sessionBundle, access_token: 'fresh-access' },
            }),
            configurable: true,
          },
          readyState: { value: 4, configurable: true },
        })
        this.onloadend?.(new ProgressEvent('loadend'))
      }
    })
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => ({
    data: { success: true },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  }))
  api.defaults.adapter = adapter
  const controller = new AbortController()
  const cancelled = api.post(
    '/cancelled-proof',
    {},
    {
      headers: { 'X-Security-Proof': 'cancelled-proof' },
      signal: controller.signal,
    }
  )
  const cancelledResult = expect(cancelled).rejects.toBeInstanceOf(Error)
  const other = api.post(
    '/remaining-proof',
    {},
    { headers: { 'X-Security-Proof': 'remaining-proof' } }
  )
  await waitFor(() => expect(send).toHaveBeenCalledTimes(1))
  expect(adapter).not.toHaveBeenCalled()
  controller.abort()
  await cancelledResult
  expect(adapter).not.toHaveBeenCalled()
  expect(finishRefresh).toBeDefined()
  finishRefresh?.()
  await other
  expect(adapter).toHaveBeenCalledTimes(1)
  expect(adapter.mock.calls[0][0].url).toBe('/remaining-proof')
  expect(adapter.mock.calls[0][0].headers.Authorization).toBe(
    'Bearer fresh-access'
  )
})

it('bounds a blocked browser refresh lock and permits a later fresh refresh', async () => {
  const nativeLocks = Object.getOwnPropertyDescriptor(navigator, 'locks')
  let lockSignal: AbortSignal | undefined
  let grantLock: (() => Promise<unknown>) | undefined
  const request = vi.fn(
    (
      _name: string,
      options: LockOptions,
      callback: () => Promise<unknown>
    ) => {
      lockSignal = options.signal
      grantLock = callback
      return new Promise(() => {})
    }
  )
  Object.defineProperty(navigator, 'locks', {
    value: { request },
    configurable: true,
  })
  try {
    vi.useFakeTimers()
    useAuthStore.getState().auth.setBundle(sessionBundle)
    const refresh = refreshAuthentication()
    await vi.advanceTimersByTimeAsync(0)
    expect(request).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(AUTH_REQUEST_TIMEOUT_MS)
    expect(await refresh).toMatchObject({ kind: 'transient_error' })
    expect(lockSignal?.aborted).toBe(true)
    expect(useAuthStore.getState().auth.accessToken).toBe(
      sessionBundle.access_token
    )
    // Even a late grant must not start a request on the expired owner.
    await expect(grantLock?.()).resolves.toMatchObject({
      kind: 'transient_error',
    })
    Object.defineProperty(navigator, 'locks', {
      value: undefined,
      configurable: true,
    })
    mockRefreshResponse({ ...sessionBundle, access_token: 'after-timeout' })
    const recovered = refreshAuthentication()
    await vi.advanceTimersByTimeAsync(0)
    expect(await recovered).toMatchObject({ kind: 'authenticated' })
    expect(useAuthStore.getState().auth.accessToken).toBe('after-timeout')
  } finally {
    if (nativeLocks) Object.defineProperty(navigator, 'locks', nativeLocks)
    else Reflect.deleteProperty(navigator, 'locks')
  }
})

it('bounds a stalled refresh HTTP request without clearing the session or sending a one-shot action', async () => {
  useAuthStore.getState().auth.setBundle({
    ...sessionBundle,
    access_expires_at: Math.floor(Date.now() / 1000) + 10,
  })
  const send = vi
    .spyOn(XMLHttpRequest.prototype, 'send')
    .mockImplementation(() => {})
  const abort = vi
    .spyOn(XMLHttpRequest.prototype, 'abort')
    .mockImplementation(() => {})
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => ({
    data: { success: true },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  }))
  api.defaults.adapter = adapter
  vi.useFakeTimers()
  const result = api.post(
    '/proof-after-refresh',
    {},
    { singleUseAuthorization: true }
  )
  const rejected = expect(result).rejects.toBeInstanceOf(Error)
  await vi.advanceTimersByTimeAsync(0)
  expect(send).toHaveBeenCalledTimes(1)
  await vi.advanceTimersByTimeAsync(AUTH_REQUEST_TIMEOUT_MS)
  await rejected
  expect(abort).toHaveBeenCalledTimes(1)
  expect(adapter).not.toHaveBeenCalled()
  expect(useAuthStore.getState().auth.accessToken).toBe(
    sessionBundle.access_token
  )
  expect(useAuthStore.getState().auth.bootstrapState).toBe('idle')
})

it('cancels a method-query 401 refresh wait without dispatching its replay', async () => {
  useAuthStore.getState().auth.setBundle(sessionBundle)
  const send = vi
    .spyOn(XMLHttpRequest.prototype, 'send')
    .mockImplementation(() => {})
  vi.spyOn(XMLHttpRequest.prototype, 'abort').mockImplementation(() => {})
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    throw new AxiosError('Unauthorized', 'ERR_BAD_REQUEST', config, undefined, {
      status: 401,
      statusText: 'Unauthorized',
      config,
      headers: {},
      data: {},
    })
  })
  api.defaults.adapter = adapter
  vi.useFakeTimers()
  const controller = new AbortController()
  const methods = checkVerificationMethods('admin.user.manage', controller.signal)
  const rejected = expect(methods).rejects.toBeInstanceOf(Error)
  await vi.advanceTimersByTimeAsync(0)
  expect(send).toHaveBeenCalledTimes(1)
  controller.abort()
  await rejected
  await vi.advanceTimersByTimeAsync(AUTH_REQUEST_TIMEOUT_MS)
  expect(adapter).toHaveBeenCalledTimes(1)
})

it('cancels encryption-key retrieval before a verification POST can be sent', async () => {
  let keySignal: AbortSignal | undefined
  vi.spyOn(api, 'get').mockImplementation((_url, options) => {
    keySignal = options?.signal as AbortSignal
    return new Promise(() => {})
  })
  const post = vi.spyOn(api, 'post')
  const controller = new AbortController()
  const result = verify(
    { method: 'password', password: 'not-transmitted' },
    { scope: 'admin.user.manage', context: { user_id: 2, action: 'disable' } },
    true,
    controller.signal
  )
  const rejected = expect(result).rejects.toBeInstanceOf(Error)
  await waitFor(() => expect(keySignal).toBeDefined())
  controller.abort()
  await rejected
  expect(keySignal?.aborted).toBe(true)
  expect(post).not.toHaveBeenCalled()
})

it('binds a channel verification to the requested channel context', async () => {
  const proof = {
    proof_token: 'channel-proof',
    method: '2fa',
    scope: 'channel.key.read',
    expires_at: Math.floor(Date.now() / 1000) + 60,
  }
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: proof },
  })
  await expect(
    verify(
      { method: '2fa', code: '123456' },
      { scope: 'channel.key.read', context: { channel_id: 123 } },
      false,
      new AbortController().signal
    )
  ).resolves.toEqual(proof)
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    {
      method: '2fa',
      code: '123456',
      scope: 'channel.key.read',
      context: { channel_id: 123 },
    },
    expect.anything()
  )
})

it('passes the operation context to Passkey begin and completes with only its flow and assertion', async () => {
  vi.stubGlobal('navigator', {
    credentials: {
      get: vi.fn().mockResolvedValue({
        id: 'credential',
        rawId: new Uint8Array([1, 2, 3]).buffer,
        type: 'public-key',
        response: {
          clientDataJSON: new Uint8Array([1]).buffer,
          authenticatorData: new Uint8Array([2]).buffer,
          signature: new Uint8Array([3]).buffer,
          userHandle: null,
        },
        getClientExtensionResults: () => ({}),
      }),
    },
  })
  const proof = {
    proof_token: 'passkey-proof',
    scope: 'channel.key.read',
    method: 'passkey',
    expires_at: Math.floor(Date.now() / 1000) + 60,
  }
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          flow_token: 'flow',
          options: { publicKey: { challenge: 'AQID', allowCredentials: [] } },
        },
      },
    })
    .mockResolvedValueOnce({ data: { success: true, data: proof } })
  await expect(
    verify(
      { method: 'passkey' },
      { scope: 'channel.key.read', context: { channel_id: 123 } },
      false,
      new AbortController().signal
    )
  ).resolves.toEqual(proof)
  expect(post).toHaveBeenNthCalledWith(
    1,
    '/api/user/passkey/verify/begin',
    {
      scope: 'channel.key.read',
      context: { channel_id: 123 },
    },
    expect.anything()
  )
  expect(post.mock.calls[1]?.[1]).toEqual({
    flow_token: 'flow',
    credential: expect.anything(),
  })
})

it('passes an enrollment operation through OAuth state creation', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { flow_token: 'oauth-flow' } },
  })
  await expect(
    createOAuthFlow('github', 'verify', {
      scope: 'passkey.register',
      context: {},
    })
  ).resolves.toBe('oauth-flow')
  expect(post).toHaveBeenCalledWith(
    '/api/oauth/state',
    expect.objectContaining({
      provider: 'github',
      intent: 'verify',
      scope: 'passkey.register',
      context: {},
    }),
    expect.anything()
  )
})

it.each([
  [
    'SECURITY_PROOF_CONSUMED',
    'This verification has already been used. Please verify again.',
  ],
  [
    'SECURITY_PROOF_CONTEXT_MISMATCH',
    "Verification does not match this action's details. Please verify again.",
  ],
])('provides a re-verification message for %s', async (code, message) => {
  await expect(
    authResult(
      Promise.resolve({
        data: { success: false, code, message: 'untranslated backend text' },
      })
    )
  ).rejects.toMatchObject({ code, message })
})

it('reports a failed method query instead of treating the account as unenrolled', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(
    new Error('Unable to load verification methods')
  )
  await expect(checkVerificationMethods('passkey.register')).rejects.toThrow(
    'Unable to load verification methods'
  )
})

it('displays a generic internal error even if the server includes database details', async () => {
  await expect(
    authResult(
      Promise.resolve({
        data: {
          success: false,
          code: 'AUTH_INTERNAL_ERROR',
          message: 'SELECT private_table at private-db-host',
        },
      })
    )
  ).rejects.toThrow('Please try again later.')
  const failure = AuthOperationError.from({
    isAxiosError: true,
    response: {
      status: 500,
      data: { message: 'SELECT private_table at private-db-host' },
    },
  })
  expect(failure.message).toBe('Please try again later.')
})
