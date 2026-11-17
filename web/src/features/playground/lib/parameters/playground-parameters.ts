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
import type {
  PlaygroundConfig,
  ParameterEnabled,
  ReasoningEffort,
} from '../../types'

type ParameterValue = PlaygroundConfig[keyof PlaygroundConfig]

export type PlaygroundParameterKey = keyof ParameterEnabled

export type PlaygroundParameterControl = {
  key: PlaygroundParameterKey
  labelKey: string
  descriptionKey: string
  valueType: 'slider' | 'number' | 'select'
  min?: number
  max?: number
  step?: number
}

export const REASONING_EFFORT_OPTIONS: readonly {
  value: ReasoningEffort
  labelKey: string
}[] = [
  { value: 'none', labelKey: 'None' },
  { value: 'minimal', labelKey: 'Minimal' },
  { value: 'low', labelKey: 'Low' },
  { value: 'medium', labelKey: 'Medium' },
  { value: 'high', labelKey: 'High' },
  { value: 'xhigh', labelKey: 'Extra high' },
]

export const PLAYGROUND_PARAMETER_CONTROLS = [
  {
    key: 'temperature',
    labelKey: 'Temperature',
    descriptionKey: 'Controls randomness and creativity',
    valueType: 'slider',
    min: 0.1,
    max: 1,
    step: 0.1,
  },
  {
    key: 'top_p',
    labelKey: 'Top P',
    descriptionKey: 'Limits token selection to a probability mass',
    valueType: 'slider',
    min: 0.1,
    max: 1,
    step: 0.1,
  },
  {
    key: 'frequency_penalty',
    labelKey: 'Frequency Penalty',
    descriptionKey: 'Reduces repeated wording',
    valueType: 'slider',
    min: -2,
    max: 2,
    step: 0.1,
  },
  {
    key: 'presence_penalty',
    labelKey: 'Presence Penalty',
    descriptionKey: 'Encourages new topics',
    valueType: 'slider',
    min: -2,
    max: 2,
    step: 0.1,
  },
  {
    key: 'max_tokens',
    labelKey: 'Max Tokens',
    descriptionKey: 'Caps the response length',
    valueType: 'number',
    min: 0,
    max: 200000,
    step: 1,
  },
  {
    key: 'seed',
    labelKey: 'Seed',
    descriptionKey: 'Keeps compatible responses more repeatable',
    valueType: 'number',
    min: 0,
    max: 2147483647,
    step: 1,
  },
  {
    key: 'reasoning_effort',
    labelKey: 'Reasoning Effort',
    descriptionKey: 'How much thinking the model may spend before answering',
    valueType: 'select',
  },
] as const satisfies readonly PlaygroundParameterControl[]

export const PLAYGROUND_PARAMETER_PANEL_SCROLL_CLASS =
  'max-h-[min(28rem,calc(100vh-10rem))] overflow-y-auto pr-1'

export function normalizeParameterNumberValue(
  key: PlaygroundParameterKey,
  value: string | number
): number | null {
  if (value === '') {
    return key === 'seed' ? null : 0
  }

  // The base type: a select control has no numeric range.
  const control: PlaygroundParameterControl | undefined =
    PLAYGROUND_PARAMETER_CONTROLS.find((item) => item.key === key)
  const parsed = typeof value === 'number' ? value : Number.parseFloat(value)

  if (!control || Number.isNaN(parsed)) {
    return key === 'seed' ? null : 0
  }

  const { min = 0, max = 0, step = 1 } = control
  const clamped = Math.min(max, Math.max(min, parsed))

  if (step >= 1) {
    return Math.trunc(clamped)
  }

  const precision = Math.max(0, String(step).split('.')[1]?.length ?? 0)
  return Number(clamped.toFixed(precision))
}

export function isReasoningEffort(value: unknown): value is ReasoningEffort {
  return REASONING_EFFORT_OPTIONS.some((option) => option.value === value)
}

export function getParameterControlValueText(
  key: PlaygroundParameterKey,
  value: ParameterValue
): string {
  if (key === 'seed' && value === null) {
    return 'Not set'
  }

  if (key === 'reasoning_effort') {
    const option = REASONING_EFFORT_OPTIONS.find((item) => item.value === value)
    return option?.labelKey ?? String(value)
  }

  return String(value)
}
