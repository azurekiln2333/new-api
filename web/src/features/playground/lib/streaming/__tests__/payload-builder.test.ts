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

import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../../../constants'
import type {
  Message,
  ParameterEnabled,
  PlaygroundConfig,
} from '../../../types'
import { buildChatCompletionPayload } from '../payload-builder'

const messages: Message[] = [
  { key: 'm1', from: 'user', versions: [{ id: 'm1-v1', content: 'hi' }] },
]

function payloadFor(
  config: Partial<PlaygroundConfig>,
  parameterEnabled: Partial<ParameterEnabled> = {}
) {
  return buildChatCompletionPayload(
    messages,
    { ...DEFAULT_CONFIG, ...config },
    { ...DEFAULT_PARAMETER_ENABLED, ...parameterEnabled }
  )
}

describe('buildChatCompletionPayload reasoning effort', () => {
  test('sends the configured reasoning effort only when the parameter is enabled', () => {
    expect(
      payloadFor({ reasoning_effort: 'high' }, { reasoning_effort: true })
        .reasoning_effort
    ).toBe('high')

    expect(
      'reasoning_effort' in
        payloadFor({ reasoning_effort: 'high' }, { reasoning_effort: false })
    ).toBe(false)
    expect('reasoning_effort' in payloadFor({ reasoning_effort: 'high' })).toBe(
      false
    )
  })

  test('existing users keep sending no reasoning effort with the default config', () => {
    const payload = payloadFor({})

    expect(DEFAULT_CONFIG.reasoning_effort).toBe('medium')
    expect(DEFAULT_PARAMETER_ENABLED.reasoning_effort).toBe(false)
    expect('reasoning_effort' in payload).toBe(false)
  })
})
