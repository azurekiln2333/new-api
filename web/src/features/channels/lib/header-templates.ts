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
 * Ready-made request header overrides that make a channel look like a
 * mainstream coding client. Some upstreams reject requests unless the
 * User-Agent / originator belongs to a known CLI.
 *
 * The values below were read from the real clients:
 * - Codex CLI 0.164.0: `originator: codex_cli_rs` and a
 *   `codex_cli_rs/<version> (<platform>)` User-Agent. `codex exec` uses the
 *   `codex_exec` originator instead.
 * - Claude Code 2.1.299: `claude-cli/<version> (external, cli)` User-Agent,
 *   `x-app: cli` and the Anthropic Messages API version header.
 * - OpenCode 1.19.5: `opencode/<version>` User-Agent.
 * - Pi 1.5.0: `pi/<version> (<platform>; <runtime>; <arch>)` User-Agent.
 *
 * Versions and platform fingerprints change over time, so they live in one
 * place and are interpolated into the templates.
 */
export const CLIENT_HEADER_TEMPLATE_VERSIONS = {
  codex: '0.164.0',
  claudeCode: '2.1.299',
  openCode: '1.19.5',
  pi: '1.5.0',
  /** Platform fingerprint Codex puts inside its User-Agent. */
  codexPlatform: 'Windows 10.0.19045; x86_64',
  /** Platform, runtime and architecture Pi puts inside its User-Agent. */
  piPlatform: 'win32; node/v24.15.0; x64',
} as const

type ClientHeaderTemplateId = 'codex' | 'claude-code' | 'opencode' | 'pi'

export type ClientHeaderTemplate = {
  id: ClientHeaderTemplateId
  /** i18n key shown in the template picker. */
  labelKey: string
  headers: Record<string, string>
}

export const CLIENT_HEADER_TEMPLATES: ClientHeaderTemplate[] = [
  {
    id: 'codex',
    labelKey: 'Codex CLI',
    headers: {
      'User-Agent': `codex_cli_rs/${CLIENT_HEADER_TEMPLATE_VERSIONS.codex} (${CLIENT_HEADER_TEMPLATE_VERSIONS.codexPlatform})`,
      originator: 'codex_cli_rs',
    },
  },
  {
    id: 'claude-code',
    labelKey: 'Claude Code',
    headers: {
      'User-Agent': `claude-cli/${CLIENT_HEADER_TEMPLATE_VERSIONS.claudeCode} (external, cli)`,
      'x-app': 'cli',
      'anthropic-version': '2023-06-01',
    },
  },
  {
    id: 'opencode',
    labelKey: 'OpenCode',
    headers: {
      'User-Agent': `opencode/${CLIENT_HEADER_TEMPLATE_VERSIONS.openCode}`,
    },
  },
  {
    id: 'pi',
    labelKey: 'Pi',
    headers: {
      'User-Agent': `pi/${CLIENT_HEADER_TEMPLATE_VERSIONS.pi} (${CLIENT_HEADER_TEMPLATE_VERSIONS.piPlatform})`,
    },
  },
]

export function getClientHeaderTemplate(
  id: string
): ClientHeaderTemplate | undefined {
  return CLIENT_HEADER_TEMPLATES.find((template) => template.id === id)
}

function parseHeaderOverrideObject(
  value: string | undefined
): Record<string, unknown> | null {
  const trimmed = value?.trim()
  if (!trimmed) return null
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    // Invalid JSON still counts as content the user must confirm replacing.
  }
  return null
}

/**
 * Whether the header override field already holds something the user would
 * lose. Empty object literals and literal `null` do not count as content.
 */
export function hasHeaderOverrideContent(value: string | undefined): boolean {
  const trimmed = value?.trim()
  if (!trimmed || trimmed === 'null') return false
  const parsed = parseHeaderOverrideObject(trimmed)
  if (parsed) return Object.keys(parsed).length > 0
  return true
}

/**
 * Build the header override JSON for a template. `replace` drops existing
 * entries; `merge` keeps them and lets the template win on key conflicts.
 * Invalid or absent existing content behaves like `replace`.
 */
export function buildHeaderOverrideJson(
  template: ClientHeaderTemplate,
  mode: 'replace' | 'merge',
  existing?: string
): string {
  if (mode === 'merge') {
    const parsed = parseHeaderOverrideObject(existing)
    if (parsed) {
      const templateHeaderNames = new Set(
        Object.keys(template.headers).map((key) => key.trim().toLowerCase())
      )
      // The relay normalizes header names before applying overrides. Remove
      // equivalent keys so the old value cannot win during map iteration.
      for (const key of Object.keys(parsed)) {
        if (templateHeaderNames.has(key.trim().toLowerCase())) {
          delete parsed[key]
        }
      }
      return JSON.stringify({ ...parsed, ...template.headers }, null, 2)
    }
  }
  return JSON.stringify(template.headers, null, 2)
}
