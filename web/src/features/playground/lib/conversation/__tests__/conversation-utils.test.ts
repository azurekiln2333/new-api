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
import { describe, expect, test } from 'vitest'

import type { Message, PlaygroundConversation } from '../../../types'
import { MAX_CONVERSATION_TITLE_CHARS } from '../../storage/storage-schema'
import {
  deriveConversationTitle,
  nextActiveConversationId,
  normalizeConversationTitle,
  patchConversation,
  sortConversationsByRecency,
  touchConversation,
} from '../conversation-utils'

function userMessage(content: string, key = 'm-1'): Message {
  return { key, from: 'user', versions: [{ id: `${key}-v`, content }] }
}

function conversation(
  id: string,
  overrides: Partial<PlaygroundConversation> = {}
): PlaygroundConversation {
  return {
    id,
    title: '',
    createdAt: 1_000,
    updatedAt: 1_000,
    ...overrides,
  }
}

describe('conversation title', () => {
  test('takes the beginning of the first user message', () => {
    expect(
      deriveConversationTitle([
        { key: 's', from: 'system', versions: [{ id: 's-v', content: 'sys' }] },
        userMessage('  Write a\n\nregex   for emails  '),
      ])
    ).toBe('Write a regex for emails')
  })

  test('returns an empty title when no user message exists yet', () => {
    expect(deriveConversationTitle([])).toBe('')
  })

  test('truncates a long title to the stored maximum', () => {
    const title = normalizeConversationTitle('x'.repeat(200))

    expect(title).toHaveLength(MAX_CONVERSATION_TITLE_CHARS)
  })
})

describe('touchConversation', () => {
  test('derives the automatic title and refreshes the update time', () => {
    const conversations = [conversation('a', { updatedAt: 1_000 })]

    const next = touchConversation(
      conversations,
      'a',
      [userMessage('Explain vector search')],
      5_000
    )

    expect(next[0].title).toBe('Explain vector search')
    expect(next[0].updatedAt).toBe(5_000)
  })

  test('keeps a manually renamed title', () => {
    const conversations = [
      conversation('a', { title: 'My name', titleCustom: true }),
    ]

    const next = touchConversation(
      conversations,
      'a',
      [userMessage('Explain vector search')],
      5_000
    )

    expect(next[0].title).toBe('My name')
  })

  test('returns the same list when nothing changed within the throttle', () => {
    const conversations = [
      conversation('a', { title: 'Explain vector search', updatedAt: 5_000 }),
    ]

    expect(
      touchConversation(
        conversations,
        'a',
        [userMessage('Explain vector search')],
        5_200
      )
    ).toBe(conversations)
  })
})

describe('conversation ordering', () => {
  test('sorts by most recently updated first', () => {
    const sorted = sortConversationsByRecency([
      conversation('old', { updatedAt: 1 }),
      conversation('new', { updatedAt: 3 }),
      conversation('mid', { updatedAt: 2 }),
    ])

    expect(sorted.map((item) => item.id)).toEqual(['new', 'mid', 'old'])
  })

  test('picks the most recent open conversation and skips archived ones', () => {
    const conversations = [
      conversation('archived-new', { archived: true, updatedAt: 9 }),
      conversation('open-old', { updatedAt: 1 }),
      conversation('open-new', { updatedAt: 5 }),
    ]

    expect(nextActiveConversationId(conversations)).toBe('open-new')
    expect(nextActiveConversationId(conversations, 'open-new')).toBe('open-old')
    expect(
      nextActiveConversationId([conversation('only', { archived: true })])
    ).toBeNull()
  })

  test('patches a single conversation without touching the others', () => {
    const conversations = [conversation('a'), conversation('b')]

    const next = patchConversation(conversations, 'b', { archived: true })

    expect(next[0]).toBe(conversations[0])
    expect(next[1].archived).toBe(true)
    expect(patchConversation(next, 'b', { archived: true })).toBe(next)
  })
})
