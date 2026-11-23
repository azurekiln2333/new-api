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
 * Original request content: the capture states a log carries, and the shape of
 * the payload the detail endpoint returns. Nothing here stores the prompt in a
 * list record; the text is fetched only while an authorized detail is open.
 */
import type { RequestContentMetadata } from '../types'

/**
 * One label per capture state. The status describes what was stored, never
 * whether the request succeeded, so a failed request can still be `recorded`.
 */
export const REQUEST_CONTENT_STATUS_LABELS: Record<string, string> = {
  not_recorded: 'Not recorded',
  recorded: 'Captured',
  truncated: 'Captured (truncated)',
  no_content: 'No prompt content in this request',
  unsupported: 'Content type not captured',
  invalid: 'Request body could not be parsed',
  unavailable: 'Request body was not read',
  disabled: 'Prompt capture is disabled',
  storage_failed: 'Prompt content could not be stored',
}

/** The two states for which the detail endpoint actually holds a payload. */
const RETRIEVABLE_STATUSES: ReadonlySet<string> = new Set([
  'recorded',
  'truncated',
])

export function getRequestContentStatusLabel(status?: string): string {
  if (status && Object.hasOwn(REQUEST_CONTENT_STATUS_LABELS, status)) {
    return REQUEST_CONTENT_STATUS_LABELS[status]
  }
  return REQUEST_CONTENT_STATUS_LABELS.unavailable
}

/**
 * A reference without a retrievable status never reaches the server: a cleared
 * ref (storage failure) or a body that was never captured cannot be fetched.
 * Legacy logs carry no metadata at all.
 */
export function canLoadRequestContent(
  metadata: RequestContentMetadata | undefined
): metadata is RequestContentMetadata & { ref: string } {
  if (!metadata?.ref) return false
  return !metadata.status || RETRIEVABLE_STATUSES.has(metadata.status)
}

/** Field name -> label for the prompt fields the server captures per protocol. */
export const REQUEST_CONTENT_FIELD_LABELS: Record<string, string> = {
  messages: 'Messages',
  prompt: 'Prompt',
  input: 'Input',
  instructions: 'Instructions',
  system: 'System',
  contents: 'Contents',
  systemInstruction: 'System instruction',
  system_instruction: 'System instruction',
}

export type RequestContentField = {
  name: string
  label: string
  value: unknown
}

/** Keep the server's field order stable and ignore unknown extras. */
export function getRequestContentFields(
  payload: unknown
): RequestContentField[] {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    return []
  }
  const record = payload as Record<string, unknown>
  return Object.entries(REQUEST_CONTENT_FIELD_LABELS)
    .filter(([name]) => Object.hasOwn(record, name))
    .map(([name, label]) => ({ name, label, value: record[name] }))
}

export type RequestContentMessage = {
  index: number
  role: string
  /** Flattened text; empty when the message carries only non-text parts. */
  text: string
  /** The parts this message holds, in order, when they are not plain text. */
  structure: unknown
}

/**
 * Flatten one OpenAI-style message. Text parts are joined in their original
 * order; any other part (an image, a tool call) is kept as structure so the
 * renderer can show a code block instead of silently dropping it.
 */
function toRequestContentMessage(value: unknown, index: number) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null
  }
  const message = value as Record<string, unknown>
  const role = typeof message.role === 'string' ? message.role : ''
  const content = message.content ?? message.parts
  if (typeof content === 'string') {
    return { index, role, text: content, structure: null }
  }
  if (!Array.isArray(content)) {
    // A message without content (for example a tool call) is shown whole.
    return { index, role, text: '', structure: message }
  }
  const texts: string[] = []
  let hasOtherParts = false
  for (const part of content) {
    if (
      part &&
      typeof part === 'object' &&
      !Array.isArray(part) &&
      typeof (part as Record<string, unknown>).text === 'string'
    ) {
      texts.push((part as Record<string, unknown>).text as string)
    } else {
      hasOtherParts = true
    }
  }
  return {
    index,
    role,
    text: texts.join('\n'),
    structure: hasOtherParts ? content : null,
  }
}

export function getRequestContentMessages(
  value: unknown
): RequestContentMessage[] {
  if (!Array.isArray(value)) return []
  const messages: RequestContentMessage[] = []
  for (let index = 0; index < value.length; index++) {
    const message = toRequestContentMessage(value[index], index)
    if (message) messages.push(message)
  }
  return messages
}
