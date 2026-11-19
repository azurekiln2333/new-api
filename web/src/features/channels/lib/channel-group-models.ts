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
import type { ChannelInfo } from '../types'
import {
  parseChannelOtherSettings,
  parseGroupsList,
  parseModelsList,
} from './channel-utils'

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
 * Malformed entries are dropped instead of failing the whole form. The result
 * has no prototype, so a group literally named `__proto__` (or any other
 * inherited key) becomes an own entry instead of mutating the lookup table.
 */
export function parseGroupModels(
  settingsJSON: string | null | undefined
): GroupModels {
  const raw: unknown = parseChannelOtherSettings(settingsJSON).group_models
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return {}
  const groupModels: GroupModels = Object.create(null) as GroupModels
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
  const pruned: GroupModels = Object.create(null) as GroupModels
  for (const [group, visible] of Object.entries(groupModels)) {
    if (!knownGroups.has(group)) continue
    const kept = [...new Set(visible)].filter((model) => knownModels.has(model))
    if (kept.length > 0) pruned[group] = kept
  }
  return pruned
}

/** One channel group and the models it reaches through this channel. */
export type GroupModelVisibility = {
  group: string
  /** Effective models in channel order; empty means the group reaches none. */
  models: string[]
}

/**
 * Models the channel serves through keys that are not bound to one user,
 * mirroring `Channel.PublicModels()` on the server: a plain channel serves its
 * model list, while a multi-key channel serves only the models its unbound
 * keys may use. Duplicates in the stored list are collapsed.
 */
export function resolvePublicModels(
  models: string,
  channelInfo:
    | Pick<
        ChannelInfo,
        | 'is_multi_key'
        | 'multi_key_size'
        | 'multi_key_models'
        | 'multi_key_users'
      >
    | null
    | undefined
): string[] {
  const channelModels = [...new Set(parseModelsList(models))]
  if (!channelInfo?.is_multi_key) return channelModels

  const scopes = channelInfo.multi_key_models ?? {}
  const owners = channelInfo.multi_key_users ?? {}
  const publicScopes: (string[] | undefined)[] = []
  for (let index = 0; index < channelInfo.multi_key_size; index += 1) {
    if (owners[String(index)]) continue
    publicScopes.push(scopes[String(index)])
  }
  if (publicScopes.length === 0) return []

  // A public key without an explicit scope serves every channel model.
  return channelModels.filter((model) =>
    publicScopes.some((scope) => !scope?.length || scope.includes(model))
  )
}

/**
 * Models each channel group can actually reach, matching the abilities the
 * server derives:
 *
 * - a group without an entry, or with an empty list, sees every model the
 *   channel serves through its public keys (`filterGroupModels` keeps every
 *   model when the whitelist is empty);
 * - a non-empty whitelist restricts the group to the models it names, even
 *   when none of them is reachable through a public key. An empty result is a
 *   real answer and must never widen back to every model;
 * - whitelist names are matched against the channel's own model list, so a
 *   stale name simply restricts the group to nothing instead of being dropped.
 */
export function resolveGroupModelVisibility(channel: {
  models: string
  group: string
  settings?: string | null
  channel_info?: Parameters<typeof resolvePublicModels>[1]
}): GroupModelVisibility[] {
  const groups = parseGroupsList(channel.group)
  const publicModels = resolvePublicModels(channel.models, channel.channel_info)
  const declared = parseGroupModels(channel.settings)

  return groups.map((group) => {
    if (!Object.hasOwn(declared, group)) {
      return { group, models: publicModels }
    }
    const allowed = new Set(declared[group])
    if (allowed.size === 0) return { group, models: publicModels }
    return {
      group,
      models: publicModels.filter((model) => allowed.has(model)),
    }
  })
}
