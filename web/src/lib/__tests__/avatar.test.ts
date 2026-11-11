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

import {
  AVATAR_URL_MAX_LENGTH,
  getUserAvatarFallback,
  resolveUserAvatarUrl,
} from '../avatar'

describe('resolveUserAvatarUrl', () => {
  test('returns an absolute https address unchanged', () => {
    expect(resolveUserAvatarUrl('https://cdn.example.com/avatar/1.png')).toBe(
      'https://cdn.example.com/avatar/1.png'
    )
  })

  test('trims surrounding whitespace', () => {
    expect(
      resolveUserAvatarUrl('  https://cdn.example.com/avatar/1.png ')
    ).toBe('https://cdn.example.com/avatar/1.png')
  })

  test('returns an empty string for values that cannot be displayed', () => {
    const unusable: unknown[] = [
      undefined,
      null,
      42,
      '',
      '   ',
      'http://cdn.example.com/avatar/1.png',
      '/uploads/avatar/1.png',
      'data:image/png;base64,AAAA',
      'https://',
      'https://user:secret@cdn.example.com/avatar/1.png',
      'https://cdn.example.com/avatar/1 .png',
      `https://cdn.example.com/${'a'.repeat(AVATAR_URL_MAX_LENGTH)}`,
    ]

    for (const value of unusable) {
      expect(resolveUserAvatarUrl(value)).toBe('')
    }
  })
})

describe('getUserAvatarFallback', () => {
  test('uses the upper-cased first letter of the name', () => {
    expect(getUserAvatarFallback('alice')).toBe('A')
    expect(getUserAvatarFallback('  bob ')).toBe('B')
  })

  test('falls back to a placeholder for an empty name', () => {
    expect(getUserAvatarFallback('')).toBe('?')
  })
})
