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
import { MESSAGE_ROLES } from '../../constants'
import type { Message, PlaygroundMessageLayoutMode } from '../../types'

export type MessageAlignment = 'left' | 'right'

export function getMessageAlignment(
  message: Message,
  layoutMode: PlaygroundMessageLayoutMode
): MessageAlignment {
  if (layoutMode === 'left') {
    return 'left'
  }

  return message.from === MESSAGE_ROLES.USER ? 'right' : 'left'
}

/**
 * Column that holds the message content. It spans the whole conversation
 * width and positions the bubble, so a reply never forces a split layout.
 */
export function getMessageAlignmentClass(alignment: MessageAlignment): string {
  return alignment === 'right'
    ? 'items-end text-right'
    : 'items-start text-left'
}

/**
 * Reply block: left aligned, limited to a comfortable reading measure inside
 * the conversation column instead of stretching across the whole screen.
 */
export function getAssistantContentClass(): string {
  return 'w-full max-w-3xl'
}

/**
 * User bubble: sized by its content, capped at roughly 70% of the column.
 */
export function getUserContentClass(): string {
  return 'w-fit max-w-[85%] sm:max-w-[70%]'
}
