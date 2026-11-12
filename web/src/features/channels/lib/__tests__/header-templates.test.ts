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
  buildHeaderOverrideJson,
  CLIENT_HEADER_TEMPLATE_VERSIONS,
  CLIENT_HEADER_TEMPLATES,
  getClientHeaderTemplate,
  hasHeaderOverrideContent,
} from '../header-templates'

describe('client header templates', () => {
  test('every template sets a User-Agent', () => {
    for (const template of CLIENT_HEADER_TEMPLATES) {
      expect(template.headers['User-Agent']).toBeTruthy()
    }
  })

  test('codex template uses the captured originator and version', () => {
    const template = getClientHeaderTemplate('codex')
    expect(template?.headers.originator).toBe('codex_cli_rs')
    expect(template?.headers['User-Agent']).toContain(
      `codex_cli_rs/${CLIENT_HEADER_TEMPLATE_VERSIONS.codex}`
    )
  })

  test('[CC] template matches the CLI user agent and Anthropic version', () => {
    const template = getClientHeaderTemplate('claude-code')
    expect(template?.headers['User-Agent']).toBe(
      `claude-cli/${CLIENT_HEADER_TEMPLATE_VERSIONS.claudeCode} (external, cli)`
    )
    expect(template?.headers['x-app']).toBe('cli')
    expect(template?.headers['anthropic-version']).toBe('2023-06-01')
  })

  test('opencode and pi use their own product prefixes', () => {
    expect(getClientHeaderTemplate('opencode')?.headers['User-Agent']).toBe(
      `opencode/${CLIENT_HEADER_TEMPLATE_VERSIONS.openCode}`
    )
    expect(getClientHeaderTemplate('pi')?.headers['User-Agent']).toContain(
      `pi/${CLIENT_HEADER_TEMPLATE_VERSIONS.pi}`
    )
  })

  test('unknown template id returns undefined', () => {
    expect(getClientHeaderTemplate('magpie')).toBeUndefined()
  })
})

describe('hasHeaderOverrideContent', () => {
  test.each([
    ['undefined', undefined, false],
    ['empty string', '', false],
    ['whitespace only', '  \n', false],
    ['literal null', 'null', false],
    ['empty object', '{}', false],
    ['single entry', '{"X-Foo":"bar"}', true],
    ['passthrough rule', '{"*":true}', true],
    ['invalid JSON', '{"X-Foo":', true],
    ['JSON array', '["x"]', true],
  ] as const)('reports %s as %s', (_label, value, expected) => {
    expect(hasHeaderOverrideContent(value)).toBe(expected)
  })
})

describe('buildHeaderOverrideJson', () => {
  const template = getClientHeaderTemplate('opencode')

  test('replace keeps only the template headers', () => {
    if (!template) throw new Error('missing opencode template')
    const json = buildHeaderOverrideJson(
      template,
      'replace',
      '{"Authorization":"Bearer {api_key}"}'
    )
    expect(JSON.parse(json)).toEqual(template.headers)
  })

  test('merge keeps existing entries and lets the template win conflicts', () => {
    if (!template) throw new Error('missing opencode template')
    const json = buildHeaderOverrideJson(
      template,
      'merge',
      '{"Authorization":"Bearer {api_key}","User-Agent":"stale"}'
    )
    expect(JSON.parse(json)).toEqual({
      Authorization: 'Bearer {api_key}',
      ...template.headers,
    })
  })

  test('merge replaces differently cased and padded header names while preserving passthrough rules', () => {
    const codex = getClientHeaderTemplate('codex')
    if (!codex) throw new Error('missing codex template')

    const json = buildHeaderOverrideJson(
      codex,
      'merge',
      JSON.stringify({
        'user-agent': 'stale',
        ' USER-AGENT ': 'also stale',
        Originator: 'other-client',
        Authorization: 'Bearer {api_key}',
        '*': true,
        're:^X-Trace-.*$': true,
      })
    )

    expect(JSON.parse(json)).toEqual({
      Authorization: 'Bearer {api_key}',
      '*': true,
      're:^X-Trace-.*$': true,
      ...codex.headers,
    })
  })

  test('merge falls back to replace when existing JSON is invalid', () => {
    if (!template) throw new Error('missing opencode template')
    expect(
      JSON.parse(buildHeaderOverrideJson(template, 'merge', '{"X-Foo":'))
    ).toEqual(template.headers)
  })

  test('output is pretty-printed with two-space indentation', () => {
    if (!template) throw new Error('missing opencode template')
    expect(buildHeaderOverrideJson(template, 'replace')).toBe(
      JSON.stringify(template.headers, null, 2)
    )
  })
})
