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

import type { Message } from '../../../types'
import {
  getAssistantContentClass,
  getMessageAlignment,
  getMessageAlignmentClass,
  getUserContentClass,
} from '../message-layout-utils'

function message(from: Message['from']): Message {
  return { key: `${from}-1`, from, versions: [{ id: 'v1', content: 'hi' }] }
}

describe('playground message layout', () => {
  test('aligns user messages to the right and replies to the left', () => {
    expect(getMessageAlignment(message('user'), 'alternating')).toBe('right')
    expect(getMessageAlignment(message('assistant'), 'alternating')).toBe(
      'left'
    )
  })

  test('keeps the left layout mode left aligned for every role', () => {
    expect(getMessageAlignment(message('user'), 'left')).toBe('left')
    expect(getMessageAlignment(message('assistant'), 'left')).toBe('left')
  })

  test('stacks messages through a full width column instead of a split row', () => {
    expect(getMessageAlignmentClass('right')).toContain('items-end')
    expect(getMessageAlignmentClass('left')).toContain('items-start')
  })

  test('caps the reply at a reading measure and the bubble at 70%', () => {
    // The reply must never stretch across the whole screen.
    expect(getAssistantContentClass()).toContain('max-w-3xl')
    expect(getAssistantContentClass()).not.toMatch(/\bw-full\b.*\bw-full\b/)

    // The user bubble shrinks to its content and stops at ~70% of the column.
    expect(getUserContentClass()).toContain('w-fit')
    expect(getUserContentClass()).toContain('sm:max-w-[70%]')
  })
})
