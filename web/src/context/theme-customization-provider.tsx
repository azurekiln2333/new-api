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
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'

import {
  useCanEditGlobalTheme,
  useGlobalTheme,
  useUpdateGlobalTheme,
} from '@/hooks/use-global-theme'
import {
  CONTENT_LAYOUT_VALUES,
  type ContentLayout,
  DEFAULT_THEME_CUSTOMIZATION,
  resolveThemeFont,
  THEME_FONT_VALUES,
  THEME_PRESET_VALUES,
  THEME_RADIUS_VALUES,
  THEME_SCALE_VALUES,
  type ThemeCustomization,
  type ThemeFont,
  type ThemePreset,
  type ThemeRadius,
  type ThemeScale,
} from '@/lib/theme-customization'
import {
  readThemePreference,
  THEME_STORAGE_KEYS,
  writeThemePreference,
} from '@/lib/theme-storage'

function applyAttribute(name: string, value: string | null) {
  if (typeof document === 'undefined') return
  const body = document.body
  if (!body) return
  if (value === null) {
    body.removeAttribute(name)
  } else {
    body.setAttribute(name, value)
  }
}

type ThemeCustomizationContextType = {
  defaults: ThemeCustomization
  customization: ThemeCustomization
  setPreset: (preset: ThemePreset) => void
  setFont: (font: ThemeFont) => void
  setRadius: (radius: ThemeRadius) => void
  setScale: (scale: ThemeScale) => void
  setContentLayout: (contentLayout: ContentLayout) => void
  resetCustomization: () => void
}

// Fallback used when a consumer renders outside the provider (e.g. an error
// route mounted before providers are ready, or stale HMR boundaries). Keeping
// it permissive prevents the whole tree from crashing — the UI just behaves
// like the defaults until the real provider re-mounts.
const FALLBACK_CONTEXT: ThemeCustomizationContextType = {
  defaults: DEFAULT_THEME_CUSTOMIZATION,
  customization: DEFAULT_THEME_CUSTOMIZATION,
  setPreset: () => {},
  setFont: () => {},
  setRadius: () => {},
  setScale: () => {},
  setContentLayout: () => {},
  resetCustomization: () => {},
}

const ThemeCustomizationContext =
  createContext<ThemeCustomizationContextType>(FALLBACK_CONTEXT)

export function ThemeCustomizationProvider(props: {
  children: React.ReactNode
}) {
  const [presetValue, _setPreset] = useState<ThemePreset>(() =>
    readThemePreference<ThemePreset>(
      THEME_STORAGE_KEYS.preset,
      THEME_PRESET_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.preset
    )
  )
  const [fontValue, _setFont] = useState<ThemeFont>(() =>
    readThemePreference<ThemeFont>(
      THEME_STORAGE_KEYS.font,
      THEME_FONT_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.font
    )
  )
  const [radiusValue, _setRadius] = useState<ThemeRadius>(() =>
    readThemePreference<ThemeRadius>(
      THEME_STORAGE_KEYS.radius,
      THEME_RADIUS_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.radius
    )
  )
  const [scaleValue, _setScale] = useState<ThemeScale>(() =>
    readThemePreference<ThemeScale>(
      THEME_STORAGE_KEYS.scale,
      THEME_SCALE_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.scale
    )
  )
  const [contentLayoutValue, _setContentLayout] = useState<ContentLayout>(() =>
    readThemePreference<ContentLayout>(
      THEME_STORAGE_KEYS.contentLayout,
      CONTENT_LAYOUT_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.contentLayout
    )
  )

  // An administrator-wide theme wins over the local preference, field by
  // field: a field the administrator left unset keeps the user's own choice.
  const globalTheme = useGlobalTheme()
  const canEditGlobalTheme = useCanEditGlobalTheme()
  const updateGlobalTheme = useUpdateGlobalTheme()

  const preset = globalTheme.preset ?? presetValue
  const font = globalTheme.font ?? fontValue
  const radius = globalTheme.radius ?? radiusValue
  const scale = globalTheme.scale ?? scaleValue
  const contentLayout = globalTheme.contentLayout ?? contentLayoutValue

  // Mirror state to the <body> via data-* attributes so theme-presets.css can
  // override CSS variables at the right cascade layer.
  useEffect(() => {
    applyAttribute(
      'data-theme-preset',
      preset === DEFAULT_THEME_CUSTOMIZATION.preset ? null : preset
    )
  }, [preset])

  // Resolve Auto into the preset font or the browser's sans-serif while
  // keeping an explicit Sans choice distinct from that browser fallback.
  useEffect(() => {
    applyAttribute('data-theme-font', resolveThemeFont(font, preset))
  }, [font, preset])

  useEffect(() => {
    applyAttribute(
      'data-theme-radius',
      radius === DEFAULT_THEME_CUSTOMIZATION.radius ? null : radius
    )
  }, [radius])

  useEffect(() => {
    applyAttribute(
      'data-theme-scale',
      scale === DEFAULT_THEME_CUSTOMIZATION.scale ? null : scale
    )
  }, [scale])

  useEffect(() => {
    applyAttribute('data-theme-content-layout', contentLayout)
  }, [contentLayout])

  // A root user's edits become the shared theme; everyone else's stay local.
  // Local state is updated first so the drawer reflects the click before the
  // status round-trip completes.
  const setPreset = useCallback(
    (value: ThemePreset) => {
      _setPreset(value)
      writeThemePreference(
        THEME_STORAGE_KEYS.preset,
        value === DEFAULT_THEME_CUSTOMIZATION.preset ? null : value
      )
      if (canEditGlobalTheme) updateGlobalTheme({ preset: value })
    },
    [canEditGlobalTheme, updateGlobalTheme]
  )

  const setFont = useCallback(
    (value: ThemeFont) => {
      _setFont(value)
      writeThemePreference(
        THEME_STORAGE_KEYS.font,
        value === DEFAULT_THEME_CUSTOMIZATION.font ? null : value
      )
      if (canEditGlobalTheme) updateGlobalTheme({ font: value })
    },
    [canEditGlobalTheme, updateGlobalTheme]
  )

  const setRadius = useCallback(
    (value: ThemeRadius) => {
      _setRadius(value)
      writeThemePreference(
        THEME_STORAGE_KEYS.radius,
        value === DEFAULT_THEME_CUSTOMIZATION.radius ? null : value
      )
      if (canEditGlobalTheme) updateGlobalTheme({ radius: value })
    },
    [canEditGlobalTheme, updateGlobalTheme]
  )

  const setScale = useCallback(
    (value: ThemeScale) => {
      _setScale(value)
      writeThemePreference(
        THEME_STORAGE_KEYS.scale,
        value === DEFAULT_THEME_CUSTOMIZATION.scale ? null : value
      )
      if (canEditGlobalTheme) updateGlobalTheme({ scale: value })
    },
    [canEditGlobalTheme, updateGlobalTheme]
  )

  const setContentLayout = useCallback(
    (value: ContentLayout) => {
      _setContentLayout(value)
      writeThemePreference(
        THEME_STORAGE_KEYS.contentLayout,
        value === DEFAULT_THEME_CUSTOMIZATION.contentLayout ? null : value
      )
      if (canEditGlobalTheme) updateGlobalTheme({ contentLayout: value })
    },
    [canEditGlobalTheme, updateGlobalTheme]
  )

  const resetCustomization = useCallback(() => {
    // Reset local state and storage directly instead of calling the setters:
    // those would each send a separate patch, and concurrent patches merged
    // from the same snapshot can leave a stale field behind.
    _setPreset(DEFAULT_THEME_CUSTOMIZATION.preset)
    _setFont(DEFAULT_THEME_CUSTOMIZATION.font)
    _setRadius(DEFAULT_THEME_CUSTOMIZATION.radius)
    _setScale(DEFAULT_THEME_CUSTOMIZATION.scale)
    _setContentLayout(DEFAULT_THEME_CUSTOMIZATION.contentLayout)
    writeThemePreference(THEME_STORAGE_KEYS.preset, null)
    writeThemePreference(THEME_STORAGE_KEYS.font, null)
    writeThemePreference(THEME_STORAGE_KEYS.radius, null)
    writeThemePreference(THEME_STORAGE_KEYS.scale, null)
    writeThemePreference(THEME_STORAGE_KEYS.contentLayout, null)

    if (canEditGlobalTheme) {
      updateGlobalTheme({
        preset: DEFAULT_THEME_CUSTOMIZATION.preset,
        font: DEFAULT_THEME_CUSTOMIZATION.font,
        radius: DEFAULT_THEME_CUSTOMIZATION.radius,
        scale: DEFAULT_THEME_CUSTOMIZATION.scale,
        contentLayout: DEFAULT_THEME_CUSTOMIZATION.contentLayout,
      })
    }
  }, [canEditGlobalTheme, updateGlobalTheme])

  const value = useMemo<ThemeCustomizationContextType>(
    () => ({
      defaults: DEFAULT_THEME_CUSTOMIZATION,
      customization: { preset, font, radius, scale, contentLayout },
      setPreset,
      setFont,
      setRadius,
      setScale,
      setContentLayout,
      resetCustomization,
    }),
    [
      preset,
      font,
      radius,
      scale,
      contentLayout,
      setPreset,
      setFont,
      setRadius,
      setScale,
      setContentLayout,
      resetCustomization,
    ]
  )

  return (
    <ThemeCustomizationContext.Provider value={value}>
      {props.children}
    </ThemeCustomizationContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useThemeCustomization() {
  return useContext(ThemeCustomizationContext)
}
