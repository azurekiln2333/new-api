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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { STORAGE_KEYS } from '../../../constants'
import type { Message, PlaygroundConversation } from '../../../types'
import {
  clearPlaygroundData,
  deleteConversationMessages,
  loadConversationIndex,
  loadConversationMessages,
  saveConversationIndex,
  saveConversationMessages,
} from '../storage'
import { MAX_STORED_MESSAGES_BYTES } from '../storage-schema'

const USER_ID = 1
const legacyMessagesKey = `${STORAGE_KEYS.MESSAGES}:${USER_ID}`

const legacyMessages: Message[] = [
  {
    key: 'legacy-user',
    from: 'user',
    versions: [{ id: 'legacy-user-1', content: 'How do I deploy this?' }],
    createdAt: 1_700_000_000_000,
  },
  {
    key: 'legacy-assistant',
    from: 'assistant',
    versions: [{ id: 'legacy-assistant-1', content: 'Run the binary.' }],
    createdAt: 1_700_000_001_000,
  },
]

function conversation(
  id: string,
  overrides: Partial<PlaygroundConversation> = {}
): PlaygroundConversation {
  return {
    id,
    title: `Chat ${id}`,
    createdAt: 1_700_000_000_000,
    updatedAt: 1_700_000_000_000,
    ...overrides,
  }
}

beforeEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.reset()
  useAuthStore.getState().auth.setUser({ id: USER_ID, username: 'A', role: 1 })
})

afterEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.reset()
})

describe('playground conversation storage', () => {
  test('migrates the previous single conversation into the first chat', () => {
    localStorage.setItem(
      legacyMessagesKey,
      JSON.stringify({ version: 1, data: legacyMessages })
    )

    const index = loadConversationIndex()

    expect(index.conversations).toHaveLength(1)
    expect(index.conversations[0].title).toBe('How do I deploy this?')
    expect(index.activeId).toBe(index.conversations[0].id)
    expect(loadConversationMessages(index.conversations[0].id)).toEqual(
      legacyMessages
    )
    expect(localStorage.getItem(legacyMessagesKey)).toBeNull()
  })

  test.each(['messages', 'index'] as const)(
    'preserves legacy history when the migration %s cannot be stored',
    (failedWrite) => {
      localStorage.setItem(
        legacyMessagesKey,
        JSON.stringify({ version: 1, data: legacyMessages })
      )
      const originalSetItem = Storage.prototype.setItem
      const setItem = vi
        .spyOn(Storage.prototype, 'setItem')
        .mockImplementation(function (this: Storage, key, value) {
          const fails =
            failedWrite === 'messages'
              ? key.startsWith(`${STORAGE_KEYS.CONVERSATION_MESSAGES}:`)
              : key === `${STORAGE_KEYS.CONVERSATIONS}:${USER_ID}`
          if (fails) {
            throw new DOMException('quota exceeded', 'QuotaExceededError')
          }
          originalSetItem.call(this, key, value)
        })

      loadConversationIndex()

      expect(localStorage.getItem(legacyMessagesKey)).not.toBeNull()
      expect(
        localStorage.getItem(`${STORAGE_KEYS.CONVERSATIONS}:${USER_ID}`)
      ).toBeNull()

      setItem.mockRestore()
      const restored = loadConversationIndex()
      expect(loadConversationMessages(restored.activeId ?? '')).toEqual(
        legacyMessages
      )
      expect(localStorage.getItem(legacyMessagesKey)).toBeNull()
    }
  )

  test('keeps the history of every conversation separate', () => {
    saveConversationIndex({
      conversations: [conversation('a'), conversation('b')],
      activeId: 'a',
    })
    saveConversationMessages('a', legacyMessages)
    saveConversationMessages('b', [legacyMessages[0]])

    expect(loadConversationMessages('a')).toHaveLength(2)
    expect(loadConversationMessages('b')).toHaveLength(1)

    deleteConversationMessages('a')
    expect(loadConversationMessages('a')).toBeNull()
    expect(loadConversationMessages('b')).toHaveLength(1)
  })

  test('restores an open conversation when the stored active chat is gone', () => {
    saveConversationIndex({
      conversations: [
        conversation('archived', { archived: true }),
        conversation('open'),
      ],
      activeId: 'deleted',
    })

    expect(loadConversationIndex().activeId).toBe('open')
  })

  test('reports a full browser storage instead of throwing', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('quota exceeded', 'QuotaExceededError')
    })

    expect(saveConversationMessages('a', legacyMessages)).toBe('quota')
  })

  test('refuses to store a chat larger than the load limit', () => {
    const oversizedMessages: Message[] = [
      {
        key: 'huge',
        from: 'user',
        versions: [
          { id: 'huge-v', content: 'x'.repeat(MAX_STORED_MESSAGES_BYTES) },
        ],
      },
    ]

    expect(saveConversationMessages('a', oversizedMessages)).toBe('quota')
    expect(loadConversationMessages('a')).toBeNull()
  })

  test('clearing playground data removes every conversation history', () => {
    saveConversationIndex({
      conversations: [conversation('a'), conversation('b')],
      activeId: 'a',
    })
    saveConversationMessages('a', legacyMessages)
    saveConversationMessages('b', legacyMessages)

    clearPlaygroundData()

    expect(loadConversationIndex().conversations).toHaveLength(1)
    expect(loadConversationMessages('a')).toBeNull()
    expect(loadConversationMessages('b')).toBeNull()
  })
})
