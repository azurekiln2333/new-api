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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { testUrlLatency } from '../api-info'

// One entry per request in call order: a number is that request's duration in
// milliseconds, `null` fails as a network error, `'timeout'` hangs until the
// caller's abort signal fires.
type FetchStep = number | null | 'timeout'

function mockFetchSequence(steps: FetchStep[]) {
  const fetchMock = vi.fn()
  let clock = 0
  vi.spyOn(performance, 'now').mockImplementation(() => clock)

  for (const step of steps) {
    fetchMock.mockImplementationOnce(
      async (_url: string, init?: RequestInit) => {
        if (step === null) {
          clock += 50
          throw new TypeError('Failed to fetch')
        }
        if (step === 'timeout') {
          return new Promise((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => {
              reject(new DOMException('aborted', 'AbortError'))
            })
          })
        }
        clock += step
        return new Response(null, { status: 200 })
      }
    )
  }
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('testUrlLatency', () => {
  it('discards the connection-setup request and reports the median sample', async () => {
    // Warm-up pays 900ms of DNS/TCP/TLS, then samples arrive out of order.
    const fetchMock = mockFetchSequence([900, 300, 100, 200])

    const status = await testUrlLatency('https://api.example.com/v1/models')

    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(status).toEqual({ latency: 200, testing: false, error: false })
  })

  it('keeps measuring with the remaining samples when one sample fails', async () => {
    mockFetchSequence([500, 240, null, 260])

    await expect(testUrlLatency('https://api.example.com')).resolves.toEqual({
      latency: 260,
      testing: false,
      error: false,
    })
  })

  it('reports an unreachable host without claiming a latency', async () => {
    mockFetchSequence([null])

    await expect(testUrlLatency('https://api.example.com')).resolves.toEqual({
      latency: null,
      testing: false,
      error: true,
      reason: 'unreachable',
    })
  })

  it('aborts a hanging request and reports a timeout', async () => {
    vi.useFakeTimers()
    const fetchMock = mockFetchSequence(['timeout'])
    const pending = testUrlLatency('https://api.example.com')
    await vi.advanceTimersByTimeAsync(4000)

    await expect(pending).resolves.toEqual({
      latency: null,
      testing: false,
      error: true,
      reason: 'timeout',
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })

  it('refuses to measure an http target from an https console', async () => {
    const fetchMock = mockFetchSequence([100])
    vi.spyOn(window, 'location', 'get').mockReturnValue({
      ...window.location,
      protocol: 'https:',
    } as Location)

    await expect(
      testUrlLatency('http://insecure.example.com/status')
    ).resolves.toEqual({
      latency: null,
      testing: false,
      error: true,
      reason: 'mixed-content',
    })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('measures an http target normally from an http console', async () => {
    const fetchMock = mockFetchSequence([200, 120, 100, 110])
    vi.spyOn(window, 'location', 'get').mockReturnValue({
      ...window.location,
      protocol: 'http:',
    } as Location)

    await expect(
      testUrlLatency('http://local.example.com/status')
    ).resolves.toEqual({ latency: 110, testing: false, error: false })
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })
})
