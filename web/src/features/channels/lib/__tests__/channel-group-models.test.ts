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
import {
  parseGroupModels,
  pruneGroupModels,
  resolveGroupModelVisibility,
  resolvePublicModels,
} from '../channel-group-models'

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

test('a group without a whitelist entry reaches every channel model', () => {
  expect(
    resolveGroupModelVisibility({
      models: 'a,b',
      group: 'default,vip',
      settings: JSON.stringify({ group_models: { vip: ['a'] } }),
    })
  ).toEqual([
    { group: 'default', models: ['a', 'b'] },
    { group: 'vip', models: ['a'] },
  ])
})

test('a whitelist only narrows the models the channel itself still lists', () => {
  expect(
    resolveGroupModelVisibility({
      models: 'a,b',
      group: 'vip',
      settings: JSON.stringify({
        group_models: { vip: ['b', 'removed-model', 'b'] },
      }),
    })
  ).toEqual([{ group: 'vip', models: ['b'] }])
})

test('a whitelist reachable only through non-public keys stays empty', () => {
  // Root review case: the group is restricted to a private model, but the
  // channel's public key only serves `public`. The intersection is empty and
  // must not fall back to every public model.
  expect(
    resolveGroupModelVisibility({
      models: 'public,private',
      group: 'default',
      settings: JSON.stringify({ group_models: { default: ['private'] } }),
      channel_info: {
        is_multi_key: true,
        multi_key_size: 2,
        // Key 0 is public and scoped to `public`; key 1 belongs to a user.
        multi_key_models: { '0': ['public'], '1': ['private'] },
        multi_key_users: { '1': 7 },
      },
    })
  ).toEqual([{ group: 'default', models: [] }])
})

test('a whitelist of removed models restricts the group to nothing', () => {
  expect(
    resolveGroupModelVisibility({
      models: 'a,b',
      group: 'vip',
      settings: JSON.stringify({ group_models: { vip: ['removed-model'] } }),
    })
  ).toEqual([{ group: 'vip', models: [] }])
})

test('an empty whitelist list keeps every public model like the server', () => {
  expect(
    resolveGroupModelVisibility({
      models: 'a,b',
      group: 'vip',
      settings: JSON.stringify({ group_models: { vip: [] } }),
    })
  ).toEqual([{ group: 'vip', models: ['a', 'b'] }])
})

test('a group named like an inherited key stays an own entry', () => {
  // Built from raw JSON: an object literal would set the prototype instead of
  // defining the `__proto__` key, which is exactly what the parser must handle.
  const settings =
    '{"group_models":{"__proto__":["a"],"constructor":["b"],"toString":["a"]}}'
  const declared = parseGroupModels(settings)
  expect(Object.hasOwn(declared, '__proto__')).toBe(true)
  expect(Object.getPrototypeOf(declared)).toBeNull()
  expect(declared['__proto__']).toEqual(['a'])
  expect(declared.constructor).toEqual(['b'])
  expect(declared.toString).toEqual(['a'])

  expect(
    resolveGroupModelVisibility({
      models: 'a,b',
      group: '__proto__,constructor,toString',
      settings,
    })
  ).toEqual([
    { group: '__proto__', models: ['a'] },
    { group: 'constructor', models: ['b'] },
    { group: 'toString', models: ['a'] },
  ])

  // A group with no entry still resolves to every public model, not to an
  // inherited member of the lookup table.
  expect(
    resolveGroupModelVisibility({ models: 'a,b', group: 'valueOf', settings })
  ).toEqual([{ group: 'valueOf', models: ['a', 'b'] }])
})

test('groups the channel no longer serves stay out of the details list', () => {
  expect(
    resolveGroupModelVisibility({
      models: 'a',
      group: 'vip',
      settings: JSON.stringify({
        group_models: { vip: ['a'], retired: ['a'] },
      }),
    })
  ).toEqual([{ group: 'vip', models: ['a'] }])
})

test('a channel without groups or models reports no rows', () => {
  expect(
    resolveGroupModelVisibility({ models: '', group: '', settings: '{}' })
  ).toEqual([])
})

test('multi-key channels serve only the models their unbound keys may use', () => {
  const singleKey = resolvePublicModels('a,b,a', null)
  expect(singleKey).toEqual(['a', 'b'])

  const multiKey = resolvePublicModels('a,b,c', {
    is_multi_key: true,
    multi_key_size: 3,
    // Key 0 is public and scoped to `a`; key 1 is public without a scope;
    // key 2 is owned by a user and must not widen the public model set.
    multi_key_models: { '0': ['a'], '2': ['b'] },
    multi_key_users: { '2': 7 },
  })
  expect(multiKey).toEqual(['a', 'b', 'c'])
})

test('a multi-key channel with only owned keys serves no public models', () => {
  const models = resolvePublicModels('a,b', {
    is_multi_key: true,
    multi_key_size: 1,
    multi_key_users: { '0': 7 },
  })
  expect(models).toEqual([])
})
