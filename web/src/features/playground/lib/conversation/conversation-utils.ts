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
import type { Message, PlaygroundConversation } from '../../types'
import { MAX_CONVERSATION_TITLE_CHARS } from '../storage/storage-schema'

// Metadata refreshes are throttled so streaming does not rewrite the index for
// every chunk; the title still updates immediately when it actually changes.
const UPDATED_AT_THROTTLE_MS = 1000
const TITLE_WHITESPACE_PATTERN = /\s+/g

export function createConversationId(): string {
  const uuid = globalThis.crypto?.randomUUID?.()
  if (uuid) return uuid

  return `chat-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

export function normalizeConversationTitle(title: string): string {
  const collapsed = title.replace(TITLE_WHITESPACE_PATTERN, ' ').trim()
  if (collapsed.length <= MAX_CONVERSATION_TITLE_CHARS) {
    return collapsed
  }

  return collapsed.slice(0, MAX_CONVERSATION_TITLE_CHARS).trim()
}

/**
 * Default title of a chat: the beginning of its first user message.
 */
export function deriveConversationTitle(messages: Message[]): string {
  const firstUserMessage = messages.find((message) => message.from === 'user')
  if (!firstUserMessage) return ''

  const versions = firstUserMessage.versions
  const currentVersion = versions.at(-1)
  return normalizeConversationTitle(currentVersion?.content ?? '')
}

export function createConversationMeta(
  now: number = Date.now()
): PlaygroundConversation {
  return {
    id: createConversationId(),
    title: '',
    createdAt: now,
    updatedAt: now,
  }
}

/**
 * Refresh `updatedAt` (and the automatic title) of one conversation.
 * Returns the original array when nothing changed.
 */
export function touchConversation(
  conversations: PlaygroundConversation[],
  conversationId: string,
  messages: Message[],
  now: number = Date.now()
): PlaygroundConversation[] {
  let changed = false
  const next = conversations.map((conversation) => {
    if (conversation.id !== conversationId) return conversation

    const title = conversation.titleCustom
      ? conversation.title
      : deriveConversationTitle(messages) || conversation.title

    if (
      title === conversation.title &&
      now - conversation.updatedAt < UPDATED_AT_THROTTLE_MS
    ) {
      return conversation
    }

    changed = true
    return {
      ...conversation,
      title,
      updatedAt: Math.max(conversation.updatedAt, now),
    }
  })

  return changed ? next : conversations
}

/**
 * Patch one conversation. Returns the original array when nothing changed.
 */
export function patchConversation(
  conversations: PlaygroundConversation[],
  conversationId: string,
  patch: Partial<PlaygroundConversation>
): PlaygroundConversation[] {
  let changed = false
  const next = conversations.map((conversation) => {
    if (conversation.id !== conversationId) return conversation
    const keys = Object.keys(patch) as (keyof PlaygroundConversation)[]
    if (keys.every((key) => conversation[key] === patch[key])) {
      return conversation
    }

    changed = true
    return { ...conversation, ...patch }
  })

  return changed ? next : conversations
}

export function sortConversationsByRecency(
  conversations: PlaygroundConversation[]
): PlaygroundConversation[] {
  return [...conversations].sort(
    (left, right) => right.updatedAt - left.updatedAt
  )
}

/**
 * The chat shown after the current one is archived or deleted: the most
 * recently updated chat that is not archived.
 */
export function nextActiveConversationId(
  conversations: PlaygroundConversation[],
  excludeId?: string
): string | null {
  const candidates = conversations.filter(
    (conversation) => !conversation.archived && conversation.id !== excludeId
  )
  const [nextConversation] = sortConversationsByRecency(candidates)
  return nextConversation?.id ?? null
}
