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
 * Theme customization constants and types.
 *
 * Lives in `lib/` (not `context/`) so it can be imported alongside the
 * provider without breaking React Fast Refresh boundaries.
 */

export const THEME_PRESETS = [
  {
    value: 'default',
    name: 'Default',
    swatches: ['oklch(0.72 0.18 250)', 'oklch(0.7 0.12 280)'],
  },
  {
    // Inspired by Anthropic's official brand language: warm cream canvas
    // (#faf9f5) paired with clay/coral (#d97757) as the single accent.
    // Swatches preview the canvas → accent gradient that defines the system.
    value: 'anthropic',
    name: 'Anthropic',
    swatches: ['oklch(0.984 0.005 95)', 'oklch(0.685 0.142 38)'],
  },
  {
    value: 'simple-large',
    name: 'Simple Large-font',
    swatches: ['oklch(0.15 0 0)', 'oklch(0.99 0 0)'],
  },
  {
    value: 'underground',
    name: 'Underground',
    swatches: ['oklch(0.5315 0.0694 156.19)', 'oklch(0.5748 0.0862 336.52)'],
  },
  {
    value: 'rose-garden',
    name: 'Rose Garden',
    swatches: ['oklch(0.5827 0.2418 12.23)', 'oklch(0.8131 0.1129 5.67)'],
  },
  {
    value: 'lake-view',
    name: 'Lake View',
    swatches: ['oklch(0.765 0.177 163.22)', 'oklch(0.551 0.0899 200.52)'],
  },
  {
    value: 'sunset-glow',
    name: 'Sunset Glow',
    swatches: ['oklch(0.5591 0.1882 25.33)', 'oklch(0.7938 0.1248 42.42)'],
  },
  {
    value: 'forest-whisper',
    name: 'Forest Whisper',
    swatches: ['oklch(0.5276 0.1072 182.22)', 'oklch(0.5236 0.0505 250.18)'],
  },
  {
    value: 'ocean-breeze',
    name: 'Ocean Breeze',
    swatches: ['oklch(0.5461 0.2152 262.88)', 'oklch(0.5854 0.2041 277.12)'],
  },
  {
    value: 'lavender-dream',
    name: 'Lavender Dream',
    swatches: ['oklch(0.5709 0.1808 306.89)', 'oklch(0.811 0.0589 201.14)'],
  },
] as const

export type ThemePreset = (typeof THEME_PRESETS)[number]['value']
export type ThemeRadius = 'default' | 'none' | 'sm' | 'md' | 'lg' | 'xl'
export type ThemeScale = 'default' | 'sm' | 'lg' | 'xl'
export type ContentLayout = 'full' | 'centered'

/**
 * Font axis for the theme.
 *
 * - `default` — use the browser's sans-serif unless the active preset
 *   specifies its own font (Anthropic uses serif).
 * - `sans` — explicitly use Public Sans.
 * - `serif` — use Lora + CJK fallbacks. Monospace contexts keep their
 *   own family via Tailwind preflight and `.font-mono`.
 */
export type ThemeFont = 'default' | 'sans' | 'serif'

/**
 * The resolved (non-`default`) font value applied to the DOM. The provider
 * always sets `data-theme-font` to one of these concrete values so CSS only
 * needs simple attribute selectors (no `:not()` gymnastics, no per-preset
 * font branches).
 */
export type ResolvedThemeFont = 'system' | Exclude<ThemeFont, 'default'>

export type ThemeCustomization = {
  preset: ThemePreset
  font: ThemeFont
  radius: ThemeRadius
  scale: ThemeScale
  contentLayout: ContentLayout
}

export const DEFAULT_THEME_CUSTOMIZATION: ThemeCustomization = {
  preset: 'default',
  font: 'default',
  radius: 'default',
  scale: 'default',
  contentLayout: 'full',
}

export const THEME_PRESET_VALUES = new Set(
  THEME_PRESETS.map((p) => p.value)
) as ReadonlySet<ThemePreset>

export const THEME_FONT_VALUES: ReadonlySet<ThemeFont> = new Set([
  'default',
  'sans',
  'serif',
])

export const THEME_RADIUS_VALUES: ReadonlySet<ThemeRadius> = new Set([
  'default',
  'none',
  'sm',
  'md',
  'lg',
  'xl',
])

export const THEME_SCALE_VALUES: ReadonlySet<ThemeScale> = new Set([
  'default',
  'sm',
  'lg',
  'xl',
])

export const CONTENT_LAYOUT_VALUES: ReadonlySet<ContentLayout> = new Set([
  'full',
  'centered',
])

/**
 * Preset → default font mapping. Used by the provider to resolve the user's
 * `font: 'default'` preference against the active preset.
 *
 * Presets without a font override use the browser's sans-serif. Public
 * Sans is reserved for an explicit user choice.
 */
export const PRESET_DEFAULT_FONT: Partial<
  Record<ThemePreset, ResolvedThemeFont>
> = {
  anthropic: 'serif',
}

/**
 * Resolve a user font preference + active preset into the concrete font that
 * should drive the DOM. Pure function so it's safe to call inside both the
 * effect that applies the attribute and the UI preview that hints at what
 * `default` will render as.
 */
export function resolveThemeFont(
  font: ThemeFont,
  preset: ThemePreset
): ResolvedThemeFont {
  if (font === 'default') {
    return PRESET_DEFAULT_FONT[preset] ?? 'system'
  }
  return font
}

/**
 * Administrator-wide theme pushed to every visitor through `/api/status`.
 *
 * Every field is optional: an absent field means "not set globally", so the
 * user's own stored preference (or the built-in default) still applies. An
 * empty object therefore behaves exactly like no global theme, which is why
 * clearing the setting is just an empty value.
 */
export type ThemeSettings = {
  mode?: 'system' | 'light' | 'dark'
  preset?: ThemePreset
  font?: ThemeFont
  radius?: ThemeRadius
  scale?: ThemeScale
  contentLayout?: ContentLayout
  sidebarVariant?: 'inset' | 'sidebar' | 'floating'
  collapsible?: 'offcanvas' | 'icon' | 'none'
  dir?: 'ltr' | 'rtl'
}

export const THEME_SETTINGS_OPTION_KEY = 'ThemeSettings'

export const THEME_MODE_VALUES: ReadonlySet<'system' | 'light' | 'dark'> =
  new Set(['system', 'light', 'dark'])

export const SIDEBAR_VARIANT_VALUES: ReadonlySet<
  NonNullable<ThemeSettings['sidebarVariant']>
> = new Set(['inset', 'sidebar', 'floating'])

export const COLLAPSIBLE_VALUES: ReadonlySet<
  NonNullable<ThemeSettings['collapsible']>
> = new Set(['offcanvas', 'icon', 'none'])

export const DIRECTION_VALUES: ReadonlySet<NonNullable<ThemeSettings['dir']>> =
  new Set(['ltr', 'rtl'])

/**
 * Keep only the fields that are present, correctly typed, and legal values.
 *
 * The option is free-form JSON written by an administrator, so a stale or
 * hand-edited value must not reach CSS attributes. Unknown members are dropped
 * rather than trusted, which also keeps `preset` etc. inside their unions.
 */
export function parseThemeSettings(
  value: string | null | undefined
): ThemeSettings {
  if (!value || value.trim() === '') return {}

  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    return {}
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return {}
  }

  const raw = parsed as Record<string, unknown>
  const settings: ThemeSettings = {}

  if (THEME_MODE_VALUES.has(raw.mode as 'system')) {
    settings.mode = raw.mode as ThemeSettings['mode']
  }
  if (THEME_PRESET_VALUES.has(raw.preset as ThemePreset)) {
    settings.preset = raw.preset as ThemePreset
  }
  if (THEME_FONT_VALUES.has(raw.font as ThemeFont)) {
    settings.font = raw.font as ThemeFont
  }
  if (THEME_RADIUS_VALUES.has(raw.radius as ThemeRadius)) {
    settings.radius = raw.radius as ThemeRadius
  }
  if (THEME_SCALE_VALUES.has(raw.scale as ThemeScale)) {
    settings.scale = raw.scale as ThemeScale
  }
  if (CONTENT_LAYOUT_VALUES.has(raw.contentLayout as ContentLayout)) {
    settings.contentLayout = raw.contentLayout as ContentLayout
  }
  if (SIDEBAR_VARIANT_VALUES.has(raw.sidebarVariant as 'inset')) {
    settings.sidebarVariant =
      raw.sidebarVariant as ThemeSettings['sidebarVariant']
  }
  if (COLLAPSIBLE_VALUES.has(raw.collapsible as 'icon')) {
    settings.collapsible = raw.collapsible as ThemeSettings['collapsible']
  }
  if (DIRECTION_VALUES.has(raw.dir as 'ltr')) {
    settings.dir = raw.dir as ThemeSettings['dir']
  }

  return settings
}

/** Values that mean "nothing set globally" for each field. */
const THEME_SETTINGS_DEFAULTS = {
  mode: 'system',
  preset: 'default',
  font: 'default',
  radius: 'default',
  scale: 'default',
  contentLayout: 'full',
  sidebarVariant: 'inset',
  collapsible: 'icon',
  dir: 'ltr',
} as const satisfies ThemeSettings

/**
 * Serialise a global theme for the option API.
 *
 * Fields left at their built-in default are dropped: writing a default is the
 * same as not setting the field, and dropping it keeps the stored JSON to what
 * an administrator actually chose. Resetting everything therefore stores an
 * empty string, which is what "no global theme" means.
 */
export function serializeThemeSettings(settings: ThemeSettings): string {
  const entries = Object.entries(settings).filter(([key, value]) => {
    if (value === undefined) return false
    const field = key as keyof typeof THEME_SETTINGS_DEFAULTS
    return value !== THEME_SETTINGS_DEFAULTS[field]
  })
  if (entries.length === 0) return ''
  return JSON.stringify(Object.fromEntries(entries))
}

/**
 * The theme every visitor sees while a global theme is set.
 *
 * Stored settings only hold the fields an administrator moved away from the
 * defaults, so an active global theme fills every other field with its default
 * rather than letting a visitor's older local choice show through. No stored
 * field means no global theme, and visitors keep their own preferences.
 */
export function resolveGlobalThemeSettings(
  settings: ThemeSettings
): ThemeSettings {
  if (Object.keys(settings).length === 0) return {}
  return { ...THEME_SETTINGS_DEFAULTS, ...settings }
}
