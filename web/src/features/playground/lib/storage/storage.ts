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
import { useAuthStore } from '@/stores/auth-store'

import { MESSAGE_STATUS, STORAGE_KEYS } from '../../constants'
import type {
  PlaygroundConfig,
  ParameterEnabled,
  Message,
  PlaygroundConversation,
} from '../../types'
import {
  createConversationMeta,
  deriveConversationTitle,
} from '../conversation/conversation-utils'
import {
  finalizeMessage,
  isAssistantMessagePending,
  sanitizeMessagesOnLoad,
} from '../message/message-streaming-utils'
import { completeAssistantTiming } from '../message/message-timing-utils'
import { hasMessageContent } from '../message/message-utils'
import {
  MAX_LOADED_MESSAGE_CHARS,
  MAX_LOADED_MESSAGES_CHARS,
  MAX_STORED_MESSAGES,
  MAX_STORED_MESSAGES_BYTES,
  STORAGE_VERSION,
  conversationIndexSchema,
  messagesSchema,
  parameterEnabledSchema,
  playgroundConfigSchema,
} from './storage-schema'

type StoredEnvelope<T> = {
  version: number
  data: T
}

/** Outcome of a local storage write, surfaced to the UI when it fails. */
export type StorageSaveResult = 'ok' | 'quota' | 'error' | 'skipped'

export type StoredConversationIndex = {
  conversations: PlaygroundConversation[]
  activeId: string | null
}

type LoadedConversationIndex = StoredConversationIndex & {
  pendingMessages?: Message[]
  saveFailure?: StorageSaveResult
}

const TRUNCATED_CONTENT_SUFFIX = '\n\n[...]'
const MIN_PREFIX_COLLAPSE_LENGTH = 2000
const MIN_REPEATED_SECTION_COUNT = 3
const SECTION_HEADING_LINE_PATTERN = /^#{2,6}\s+\d+\.\s+.+$/gm

function getUserStorageKey(key: string): string | null {
  // Legacy data has no owner; never assign it to the next signed-in account.
  for (const legacyKey of Object.values(STORAGE_KEYS)) {
    localStorage.removeItem(legacyKey)
  }
  const userId = useAuthStore.getState().auth.user?.id
  return userId == null ? null : `${key}:${userId}`
}

function readStoredValue(baseKey: string): unknown | null {
  const key = getUserStorageKey(baseKey)
  if (!key) return null
  const saved = localStorage.getItem(key)
  if (!saved) return null

  return JSON.parse(saved) as unknown
}

function readStoredMessagesValue(baseKey: string): unknown | null {
  const key = getUserStorageKey(baseKey)
  if (!key) return null
  const saved = localStorage.getItem(key)
  if (!saved) return null

  if (saved.length > MAX_STORED_MESSAGES_BYTES) {
    localStorage.removeItem(key)
    return null
  }

  return JSON.parse(saved) as unknown
}

function unwrapStoredValue(value: unknown): unknown {
  if (!value || typeof value !== 'object') {
    return value
  }

  if ('version' in value && 'data' in value) {
    return (value as StoredEnvelope<unknown>).data
  }

  return value
}

function isQuotaExceededError(error: unknown): boolean {
  if (!(error instanceof DOMException)) return false

  return (
    error.name === 'QuotaExceededError' ||
    error.name === 'NS_ERROR_DOM_QUOTA_REACHED' ||
    error.code === 22 ||
    error.code === 1014
  )
}

function writeStoredValue<T>(
  baseKey: string,
  data: T,
  maxBytes?: number
): StorageSaveResult {
  const key = getUserStorageKey(baseKey)
  if (!key) return 'skipped'
  const payload: StoredEnvelope<T> = {
    version: STORAGE_VERSION,
    data,
  }

  try {
    const serialized = JSON.stringify(payload)
    // The loader refuses oversized payloads, so refusing them here avoids
    // writing a chat that would disappear on the next visit.
    if (maxBytes !== undefined && serialized.length > maxBytes) {
      return 'quota'
    }
    localStorage.setItem(key, serialized)
  } catch (error) {
    // Browser quota errors are expected for long chats with inline images, so
    // they are reported to the caller instead of being swallowed.
    return isQuotaExceededError(error) ? 'quota' : 'error'
  }

  return 'ok'
}

function removeStoredValue(baseKey: string): void {
  const key = getUserStorageKey(baseKey)
  if (!key) return
  localStorage.removeItem(key)
}

function trimMessages(messages: Message[]): Message[] {
  if (messages.length <= MAX_STORED_MESSAGES) {
    return messages
  }

  return messages.slice(-MAX_STORED_MESSAGES)
}

function getMessageSize(message: Message): number {
  const versionsSize = message.versions.reduce(
    (total, version) => total + version.content.length,
    0
  )
  const reasoningSize = message.reasoning?.content.length ?? 0

  return versionsSize + reasoningSize
}

function truncateText(text: string, maxLength: number): string {
  if (text.length <= maxLength) {
    return text
  }

  if (maxLength <= TRUNCATED_CONTENT_SUFFIX.length) {
    return text.slice(0, maxLength)
  }

  return `${text.slice(0, maxLength - TRUNCATED_CONTENT_SUFFIX.length)}${TRUNCATED_CONTENT_SUFFIX}`
}

type SectionOccurrence = {
  heading: string
  index: number
}

function getSectionOccurrences(text: string): SectionOccurrence[] {
  const occurrences: SectionOccurrence[] = []
  const matches = text.matchAll(SECTION_HEADING_LINE_PATTERN)
  for (const match of matches) {
    const index = match.index
    if (index === undefined) {
      continue
    }

    occurrences.push({
      heading: match[0],
      index,
    })
  }

  return occurrences
}

function getHeadingCounts(
  occurrences: SectionOccurrence[]
): Map<string, number> {
  const counts = new Map<string, number>()

  for (const occurrence of occurrences) {
    counts.set(occurrence.heading, (counts.get(occurrence.heading) ?? 0) + 1)
  }

  return counts
}

function findLastRepeatedSectionRunStart(text: string): number {
  const occurrences = getSectionOccurrences(text)
  const headingCounts = getHeadingCounts(occurrences)
  const lastRepeatedIndexes: number[] = []
  const seenHeadings = new Set<string>()

  for (let index = occurrences.length - 1; index >= 0; index--) {
    const occurrence = occurrences[index]
    const count = headingCounts.get(occurrence.heading) ?? 0

    if (
      count < MIN_REPEATED_SECTION_COUNT ||
      seenHeadings.has(occurrence.heading)
    ) {
      continue
    }

    seenHeadings.add(occurrence.heading)
    lastRepeatedIndexes.push(occurrence.index)
  }

  if (lastRepeatedIndexes.length === 0) {
    return -1
  }

  return Math.min(...lastRepeatedIndexes)
}

function collapseRepeatedSectionSnapshots(text: string): string {
  if (text.length < MIN_PREFIX_COLLAPSE_LENGTH) {
    return text
  }

  const lastRepeatedRunStart = findLastRepeatedSectionRunStart(text)
  if (lastRepeatedRunStart === -1) {
    return text
  }

  return text.slice(lastRepeatedRunStart)
}

function normalizeStoredMessageForLoad(message: Message): Message {
  let changed = false
  const versions = message.versions.map((version) => {
    const collapsedContent = collapseRepeatedSectionSnapshots(version.content)
    const content = truncateText(collapsedContent, MAX_LOADED_MESSAGE_CHARS)

    if (content === version.content && collapsedContent === version.content) {
      return version
    }

    changed = true
    return {
      ...version,
      content,
    }
  })

  const reasoning = message.reasoning
    ? {
        ...message.reasoning,
        content: truncateText(
          message.reasoning.content,
          MAX_LOADED_MESSAGE_CHARS
        ),
      }
    : undefined

  if (reasoning?.content !== message.reasoning?.content) {
    changed = true
  }

  const normalized = changed ? { ...message, versions, reasoning } : message

  if (!isAssistantMessagePending(normalized)) {
    return normalized
  }

  const hasContent = hasMessageContent(normalized)
  const hasReasoning = normalized.reasoning?.content.trim()

  if (!hasContent && !hasReasoning) {
    return normalized
  }

  const completedAt =
    normalized.completedAt ??
    normalized.reasoning?.completedAt ??
    normalized.startedAt ??
    normalized.createdAt ??
    Date.now()

  return completeAssistantTiming(
    {
      ...finalizeMessage(normalized),
      status: MESSAGE_STATUS.COMPLETE,
      isReasoningStreaming: false,
    },
    completedAt
  )
}

function trimMessagesByContentSize(messages: Message[]): Message[] {
  let totalSize = 0
  const result: Message[] = []

  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]
    const messageSize = getMessageSize(message)

    if (
      result.length > 0 &&
      totalSize + messageSize > MAX_LOADED_MESSAGES_CHARS
    ) {
      break
    }

    totalSize += messageSize
    result.push(message)
  }

  return result.reverse()
}

/**
 * Load playground config from localStorage
 */
export function loadConfig(): Partial<PlaygroundConfig> {
  try {
    const saved = readStoredValue(STORAGE_KEYS.CONFIG)
    if (!saved) return {}

    return playgroundConfigSchema.parse(unwrapStoredValue(saved))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load config:', error)
  }
  return {}
}

/**
 * Save playground config to localStorage
 */
export function saveConfig(config: Partial<PlaygroundConfig>): void {
  try {
    const parsed = playgroundConfigSchema.parse(config)
    writeStoredValue(STORAGE_KEYS.CONFIG, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save config:', error)
  }
}

/**
 * Load parameter enabled state from localStorage
 */
export function loadParameterEnabled(): Partial<ParameterEnabled> {
  try {
    const saved = readStoredValue(STORAGE_KEYS.PARAMETER_ENABLED)
    if (!saved) return {}

    return parameterEnabledSchema.parse(unwrapStoredValue(saved))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load parameter enabled:', error)
  }
  return {}
}

/**
 * Save parameter enabled state to localStorage
 */
export function saveParameterEnabled(
  parameterEnabled: Partial<ParameterEnabled>
): void {
  try {
    const parsed = parameterEnabledSchema.parse(parameterEnabled)
    writeStoredValue(STORAGE_KEYS.PARAMETER_ENABLED, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save parameter enabled:', error)
  }
}

/**
 * Load messages from localStorage
 */
export function loadMessages(): Message[] | null {
  return loadMessagesFromBaseKey(STORAGE_KEYS.MESSAGES)
}

function loadMessagesFromBaseKey(baseKey: string): Message[] | null {
  try {
    const saved = readStoredMessagesValue(baseKey)
    if (!saved) return null

    const parsed = messagesSchema.parse(unwrapStoredValue(saved)) as Message[]
    const normalized = parsed.map(normalizeStoredMessageForLoad)
    const normalizedChanged = normalized.some(
      (message, index) => message !== parsed[index]
    )
    const trimmed = trimMessages(normalized)
    const sizeTrimmed = trimMessagesByContentSize(trimmed)
    const sanitized = sanitizeMessagesOnLoad(sizeTrimmed)

    if (
      normalizedChanged ||
      trimmed !== normalized ||
      sizeTrimmed !== trimmed ||
      sanitized !== sizeTrimmed
    ) {
      saveMessagesToBaseKey(baseKey, sanitized)
    }

    return sanitized
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load messages:', error)
  }
  return null
}

function trimAndValidateMessages(messages: Message[]): Message[] {
  return messagesSchema.parse(trimMessages(messages)) as Message[]
}

/**
 * Save messages to localStorage
 */
export function saveMessages(messages: Message[]): void {
  saveMessagesToBaseKey(STORAGE_KEYS.MESSAGES, messages)
}

function saveMessagesToBaseKey(
  baseKey: string,
  messages: Message[]
): StorageSaveResult {
  try {
    const parsed = trimAndValidateMessages(messages)
    return writeStoredValue(baseKey, parsed, MAX_STORED_MESSAGES_BYTES)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save messages:', error)
    return 'error'
  }
}

function conversationMessagesBaseKey(conversationId: string): string {
  return `${STORAGE_KEYS.CONVERSATION_MESSAGES}:${conversationId}`
}

function pickActiveConversationId(
  conversations: PlaygroundConversation[],
  preferredId?: string | null
): string | null {
  if (preferredId && conversations.some((item) => item.id === preferredId)) {
    return preferredId
  }

  const openConversation =
    conversations.find((item) => !item.archived) ?? conversations[0]
  return openConversation?.id ?? null
}

function seedConversations(): StoredConversationIndex {
  const conversation = createConversationMeta()
  return { conversations: [conversation], activeId: conversation.id }
}

/**
 * Move the pre-multi-conversation history into the first conversation so an
 * upgrade never loses the chat the user already had.
 */
function migrateLegacyMessages(): LoadedConversationIndex {
  const legacyMessages = loadMessagesFromBaseKey(STORAGE_KEYS.MESSAGES)
  const messages = legacyMessages ?? []
  const createdAt =
    messages.find((message) => message.createdAt)?.createdAt ?? Date.now()
  const conversation: PlaygroundConversation = {
    ...createConversationMeta(createdAt),
    title: deriveConversationTitle(messages),
    updatedAt: Date.now(),
  }
  const index: StoredConversationIndex = {
    conversations: [conversation],
    activeId: conversation.id,
  }

  const messageResult = saveConversationMessages(conversation.id, messages)
  if (messageResult !== 'ok') {
    return { ...index, pendingMessages: messages, saveFailure: messageResult }
  }

  const indexResult = saveConversationIndex(index)
  if (indexResult !== 'ok') {
    // The legacy key remains authoritative until both writes succeed. Avoid
    // accumulating orphan copies when the browser rejects the index write.
    deleteConversationMessages(conversation.id)
    return { ...index, pendingMessages: messages, saveFailure: indexResult }
  }

  if (legacyMessages !== null) completeLegacyMessageMigration()

  return index
}

export function completeLegacyMessageMigration(): void {
  try {
    removeStoredValue(STORAGE_KEYS.MESSAGES)
  } catch {
    // Retaining the redundant legacy copy is safe when storage is unavailable.
  }
}

/**
 * Load the conversation index, creating it (and migrating legacy messages) on
 * first use.
 */
export function loadConversationIndex(): LoadedConversationIndex {
  let stored: unknown = null
  try {
    stored = readStoredValue(STORAGE_KEYS.CONVERSATIONS)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load conversations:', error)
  }

  if (stored) {
    const parsed = conversationIndexSchema.safeParse(unwrapStoredValue(stored))
    if (parsed.success && parsed.data.conversations.length > 0) {
      const conversations = parsed.data.conversations
      return {
        conversations,
        activeId: pickActiveConversationId(conversations, parsed.data.activeId),
      }
    }
  }

  const index = migrateLegacyMessages()
  return index.conversations.length > 0 ? index : seedConversations()
}

export function saveConversationIndex(
  index: StoredConversationIndex
): StorageSaveResult {
  try {
    const parsed = conversationIndexSchema.parse(index)
    return writeStoredValue(STORAGE_KEYS.CONVERSATIONS, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save conversations:', error)
    return 'error'
  }
}

export function loadConversationMessages(
  conversationId: string
): Message[] | null {
  return loadMessagesFromBaseKey(conversationMessagesBaseKey(conversationId))
}

export function saveConversationMessages(
  conversationId: string,
  messages: Message[]
): StorageSaveResult {
  return saveMessagesToBaseKey(
    conversationMessagesBaseKey(conversationId),
    messages
  )
}

export function deleteConversationMessages(conversationId: string): void {
  try {
    removeStoredValue(conversationMessagesBaseKey(conversationId))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to delete conversation messages:', error)
  }
}

/**
 * Clear the signed-in user's playground data
 */
export function clearPlaygroundData(): void {
  try {
    const userId = useAuthStore.getState().auth.user?.id
    for (const baseKey of Object.values(STORAGE_KEYS)) {
      const key = getUserStorageKey(baseKey)
      if (key) localStorage.removeItem(key)
    }
    if (userId == null) return

    // Per-conversation message keys are dynamic, so sweep them by prefix.
    const conversationPrefix = `${STORAGE_KEYS.CONVERSATION_MESSAGES}:`
    const userSuffix = `:${userId}`
    const conversationKeys: string[] = []
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index)
      if (key?.startsWith(conversationPrefix) && key.endsWith(userSuffix)) {
        conversationKeys.push(key)
      }
    }
    for (const key of conversationKeys) {
      localStorage.removeItem(key)
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to clear playground data:', error)
  }
}
