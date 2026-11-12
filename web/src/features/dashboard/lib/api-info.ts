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
import type { PingStatus } from '@/features/dashboard/types'

/**
 * Get color class for latency status
 */
export function getLatencyColorClass(latency: number): string {
  if (latency < 200) {
    return 'text-green-600 dark:text-green-400'
  }
  if (latency < 500) {
    return 'text-yellow-600 dark:text-yellow-400'
  }
  return 'text-red-600 dark:text-red-400'
}

/**
 * Per-attempt timeout. A request that has not answered by then is useless as a
 * latency sample: the panel would otherwise sit on "Testing..." forever when a
 * host silently drops the connection.
 */
const LATENCY_TIMEOUT_MS = 4000

/** Samples kept after discarding the first (connection setup) request. */
const LATENCY_SAMPLE_COUNT = 3

/**
 * An https console cannot reach an http API: the browser blocks it as mixed
 * content, so there is no honest number to show.
 */
function isBlockedInsecureTarget(url: string): boolean {
  if (typeof window === 'undefined') return false
  return window.location.protocol === 'https:' && /^http:\/\//i.test(url)
}

type LatencySample =
  | { latency: number }
  | { failure: 'timeout' | 'unreachable' }

/**
 * One browser request to the target, timed with `performance.now()`.
 *
 * `mode: 'no-cors'` makes the response opaque and unreadable, which is exactly
 * what an arbitrary third-party API host needs: the request still travels the
 * network, so we can time it without CORS headers on the target.
 */
async function measureLatencyOnce(
  url: string,
  timeoutMs: number
): Promise<LatencySample> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const startTime = performance.now()
    await fetch(url, {
      method: 'HEAD',
      mode: 'no-cors',
      cache: 'no-cache',
      signal: controller.signal,
    })
    return { latency: performance.now() - startTime }
  } catch (error) {
    return {
      failure:
        error instanceof DOMException && error.name === 'AbortError'
          ? 'timeout'
          : 'unreachable',
    }
  } finally {
    clearTimeout(timer)
  }
}

/**
 * Test URL latency from the user's browser.
 *
 * The first request is discarded because it pays for DNS, TCP and TLS setup;
 * the remaining samples are taken over the already-open connection and reduced
 * to their median, so a single slow packet does not define the result.
 */
export async function testUrlLatency(url: string): Promise<PingStatus> {
  if (isBlockedInsecureTarget(url)) {
    return {
      latency: null,
      testing: false,
      error: true,
      reason: 'mixed-content',
    }
  }

  const warmup = await measureLatencyOnce(url, LATENCY_TIMEOUT_MS)
  if ('failure' in warmup) {
    return {
      latency: null,
      testing: false,
      error: true,
      reason: warmup.failure,
    }
  }

  const samples: number[] = []
  let lastFailure: 'timeout' | 'unreachable' = 'unreachable'
  for (let i = 0; i < LATENCY_SAMPLE_COUNT; i++) {
    const sample = await measureLatencyOnce(url, LATENCY_TIMEOUT_MS)
    if ('failure' in sample) {
      lastFailure = sample.failure
      continue
    }
    samples.push(sample.latency)
  }

  if (samples.length === 0) {
    return { latency: null, testing: false, error: true, reason: lastFailure }
  }

  samples.sort((a, b) => a - b)
  return {
    latency: Math.round(samples[Math.floor(samples.length / 2)]),
    testing: false,
    error: false,
  }
}

/**
 * Open external speed test link
 */
export function openExternalSpeedTest(url: string): void {
  const encodedUrl = encodeURIComponent(url)
  const speedTestUrl = `https://www.tcptest.cn/http/${encodedUrl}`
  window.open(speedTestUrl, '_blank', 'noopener,noreferrer')
}

/**
 * Get default ping status
 */
export function getDefaultPingStatus(): PingStatus {
  return {
    latency: null,
    testing: false,
    error: false,
  }
}
