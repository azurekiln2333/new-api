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
import { expect, test } from 'vitest'

import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from '../channel-form'
import { parseGroupModels, pruneGroupModels } from '../channel-group-models'

function settingsOf(payload: { settings?: string }) {
  return JSON.parse(payload.settings ?? '{}') as Record<string, unknown>
}

test('reads the per-group whitelist from the settings JSON', () => {
  expect(
    parseGroupModels(
      JSON.stringify({
        group_models: { vip: ['a', 'b'], default: [] },
        disable_store: true,
      })
    )
  ).toEqual({ vip: ['a', 'b'], default: [] })
  expect(parseGroupModels('{}')).toEqual({})
  expect(parseGroupModels('not json')).toEqual({})
})

test('keeps the whitelist in step with the channel groups and models', () => {
  const groupModels = {
    vip: ['a', 'b', 'a'],
    default: ['gone'],
    removed: ['a'],
  }

  expect(pruneGroupModels(groupModels, ['vip', 'default'], ['a', 'b'])).toEqual(
    {
      vip: ['a', 'b'],
    }
  )
  expect(pruneGroupModels({ vip: ['a'] }, ['vip'], ['b'])).toEqual({})
})

test('saving a channel preserves the other settings fields and prunes the whitelist', () => {
  const channel = channelSchema.parse({
    id: 1,
    name: 'scoped',
    type: 14,
    key: '',
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'a,b',
    group: 'vip',
    settings: JSON.stringify({
      claude_beta_query: true,
      group_models: { vip: ['a', 'stale-model'], gone: ['a'] },
    }),
  })

  const values = transformChannelToFormDefaults(channel)
  expect(values.group_models).toEqual({
    vip: ['a', 'stale-model'],
    gone: ['a'],
  })

  const settings = settingsOf(transformFormDataToUpdatePayload(values, 1))
  expect(settings.group_models).toEqual({ vip: ['a'] })
  expect(settings.claude_beta_query).toBe(true)
})

test('dropping every whitelist entry removes the settings key instead of leaving an empty map', () => {
  const values = {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'scoped',
    type: 14,
    models: 'a',
    group: ['vip'],
    claude_beta_query: true,
    settings: JSON.stringify({
      group_models: { vip: ['a'] },
      claude_beta_query: true,
    }),
  }

  const settings = settingsOf(transformFormDataToUpdatePayload(values, 1))
  expect('group_models' in settings).toBe(false)
  expect(settings.claude_beta_query).toBe(true)
})
