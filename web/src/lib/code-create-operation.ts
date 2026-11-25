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
import { t } from 'i18next'

import type { AuthUser } from '@/stores/auth-store'

type CodeCreateInput = {
  name: string
  count?: number
  expired_time: number
  quota?: number
  limit_per_user?: boolean
}

export type CodeCreateOperation = {
  id: string
  payload: string
  storageKey: string
}

// A failed browser cleanup must never turn a confirmed success into a retry
// within the same page. Pending IDs themselves are read from sessionStorage.
const completedOperations = new Set<string>()

export function getCodeCreateOperation(
  actor: Pick<AuthUser, 'id' | 'role'> | null,
  kind: 'invitation' | 'redemption',
  input: CodeCreateInput
): CodeCreateOperation {
  if (
    !actor ||
    !Number.isSafeInteger(actor.id) ||
    actor.id <= 0 ||
    !Number.isSafeInteger(actor.role)
  ) {
    throw new Error(t('Session expired!'))
  }

  // Explicit fields only: never persist created codes, auth tokens, or whole
  // request/response objects. Keep one pending create for each actor/role/kind.
  const payload = JSON.stringify({
    name: kind === 'invitation' ? input.name.trim() : input.name,
    count: input.count ?? 1,
    expired_time: input.expired_time,
    ...(kind === 'redemption'
      ? { quota: input.quota, limit_per_user: input.limit_per_user ?? false }
      : {}),
  })
  const storageKey = `new-api:code-create:v1:${actor.id}:${actor.role}:${kind}`

  try {
    const storage = window.sessionStorage
    const raw = storage.getItem(storageKey)
    if (raw) {
      const stored: unknown = JSON.parse(raw)
      if (
        stored &&
        typeof stored === 'object' &&
        'id' in stored &&
        typeof stored.id === 'string' &&
        stored.id.length > 0 &&
        stored.id.length <= 96 &&
        'payload' in stored &&
        stored.payload === payload &&
        (!('state' in stored) || stored.state !== 'completed') &&
        !completedOperations.has(stored.id)
      ) {
        return { id: stored.id, payload, storageKey }
      }
    }

    const id =
      globalThis.crypto?.randomUUID?.() ??
      `${kind}-${Date.now()}-${Math.random().toString(16).slice(2)}`
    const serialized = JSON.stringify({ id, payload })
    storage.setItem(storageKey, serialized)
    // Some storage implementations silently reject writes. Do not dispatch
    // until the retry identity can actually survive a page reload.
    if (storage.getItem(storageKey) !== serialized) throw new Error()
    return { id, payload, storageKey }
  } catch {
    throw new Error(
      t(
        'Browser storage is unavailable. Enable it before creating codes so retries cannot create duplicates.'
      )
    )
  }
}

export function completeCodeCreateOperation(
  operation: CodeCreateOperation
): void {
  completedOperations.add(operation.id)
  try {
    const storage = window.sessionStorage
    const raw = storage.getItem(operation.storageKey)
    if (!raw) {
      completedOperations.delete(operation.id)
      return
    }
    const stored: unknown = JSON.parse(raw)
    if (
      stored &&
      typeof stored === 'object' &&
      'id' in stored &&
      stored.id === operation.id
    ) {
      // A late success for an older payload must not erase a newer operation.
      const marker = JSON.stringify({
        id: operation.id,
        payload: operation.payload,
        state: 'completed',
      })
      storage.setItem(operation.storageKey, marker)
      if (storage.getItem(operation.storageKey) !== marker) return
      storage.removeItem(operation.storageKey)
      if (storage.getItem(operation.storageKey) !== null) return
    }
    completedOperations.delete(operation.id)
  } catch {
    // The server already confirmed success. Preserve the keys for export even
    // if browser storage has become unavailable since the request started.
  }
}
