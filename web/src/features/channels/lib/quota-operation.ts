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
/**
 * Shared helpers for budget mutations: a retry-safe operation ID and the one
 * server answer that means "the console state is stale", not "bad input".
 */

import { getServerErrorSources } from '@/lib/server-error-message'

/** Accept only a whole, non-negative amount in raw site quota units. */
export function parseLocalQuotaLimit(input: string): number | null {
  const amount = Number(input)
  return /^\d+$/.test(input.trim()) && Number.isSafeInteger(amount)
    ? amount
    : null
}

const staleQuotaMessages = new Set([
  'channel budget configuration version changed, reload and retry',
  'channel key configuration changed, reload and retry',
  'channel key allocation revision is stale',
  'deduplication preview changed, reload and retry',
])

/** A reset retry must reuse the same ID so the server can replay it safely. */
export function newQuotaOperationId(): string {
  const uuid = globalThis.crypto?.randomUUID?.()
  if (uuid) return uuid
  return `qreset-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

/**
 * The server version checks reject a stale configuration instead of applying
 * over another administrator's change. Those answers ask for a reload; other
 * failures are validation or availability errors and must stay inline.
 */
export function isQuotaConflict(error: unknown): boolean {
  return getServerErrorSources(error).some((source) => {
    const code = source.error_code ?? source.code
    if (
      code === 'channel_budget_version_conflict' ||
      code === 'channel_key_revision_conflict'
    ) {
      return true
    }
    const message = source.message_key ?? source.message
    return (
      typeof message === 'string' &&
      staleQuotaMessages.has(message.toLowerCase())
    )
  })
}
