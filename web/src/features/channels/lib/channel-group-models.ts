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
import { parseChannelOtherSettings } from './channel-utils'

/**
 * Group -> models visible to that group, as stored in `settings.group_models`.
 * A group without an entry (or with an empty list) sees every channel model.
 */
export type GroupModels = Record<string, string[]>

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
}

/**
 * Read the per-group model visibility map out of the channel `settings` JSON.
 * Malformed entries are dropped instead of failing the whole form.
 */
export function parseGroupModels(
  settingsJSON: string | null | undefined
): GroupModels {
  const raw: unknown = parseChannelOtherSettings(settingsJSON).group_models
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return {}
  const groupModels: GroupModels = {}
  for (const [group, models] of Object.entries(raw)) {
    if (group.trim() && isStringArray(models)) {
      groupModels[group.trim()] = models
    }
  }
  return groupModels
}

/**
 * Keep only entries that still refer to a channel model of a channel group,
 * and drop empty lists: an empty list means "every model is visible", which is
 * the same as having no entry at all.
 */
export function pruneGroupModels(
  groupModels: GroupModels,
  groups: string[],
  models: string[]
): GroupModels {
  const knownGroups = new Set(groups)
  const knownModels = new Set(models)
  const pruned: GroupModels = {}
  for (const [group, visible] of Object.entries(groupModels)) {
    if (!knownGroups.has(group)) continue
    const kept = [...new Set(visible)].filter((model) => knownModels.has(model))
    if (kept.length > 0) pruned[group] = kept
  }
  return pruned
}
