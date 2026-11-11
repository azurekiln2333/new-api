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
import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

import { UserAvatar } from '../user-avatar'

const avatarURL = 'https://cdn.example.com/avatar/1.png'

/**
 * Replaces the browser image loader: jsdom never loads images, so the avatar
 * would always stay in its loading state.
 */
function stubImageLoading(naturalWidth: number) {
  class StubImage {
    onload: (() => void) | null = null
    onerror: (() => void) | null = null
    referrerPolicy = ''
    crossOrigin: string | null = null
    sizes = ''
    srcset = ''
    complete = false
    naturalWidth = 0
    private source = ''

    set src(value: string) {
      this.source = value
      this.complete = true
      this.naturalWidth = naturalWidth
    }

    get src() {
      return this.source
    }
  }
  Object.defineProperty(window, 'Image', {
    configurable: true,
    writable: true,
    value: StubImage,
  })
}

afterEach(() => {
  Reflect.deleteProperty(window, 'Image')
})

describe('UserAvatar', () => {
  test('shows the provider picture without sending a referrer', () => {
    stubImageLoading(64)

    render(<UserAvatar name='alice' avatarUrl={avatarURL} />)

    const image = screen.getByRole('img', { name: 'alice' })
    expect(image.getAttribute('src')).toBe(avatarURL)
    expect(image.getAttribute('referrerpolicy')).toBe('no-referrer')
    expect(screen.queryByText('A')).toBeNull()
  })

  test('shows the initials block when the account has no avatar address', () => {
    render(<UserAvatar name='alice' />)

    expect(screen.getByText('A')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })

  test('shows the initials block when the address is not a usable https URL', () => {
    render(<UserAvatar name='alice' avatarUrl='http://cdn.example.com/1.png' />)

    expect(screen.getByText('A')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })

  test('shows the initials block when the picture fails to load', () => {
    stubImageLoading(0)

    render(<UserAvatar name='alice' avatarUrl={avatarURL} />)

    expect(screen.getByText('A')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })
})
