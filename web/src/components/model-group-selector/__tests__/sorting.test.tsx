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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import {
  ModelGroupSelector,
  ModelSelector,
} from '@/components/model-group-selector'
import { compareModelNames } from '@/lib/model-options'

// JSDOM does not implement the pointer-capture API used by the drawer.
const pointerCapture = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'setPointerCapture'
)

beforeEach(() => {
  Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', {
    configurable: true,
    value: () => undefined,
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  if (pointerCapture) {
    Object.defineProperty(
      HTMLElement.prototype,
      'setPointerCapture',
      pointerCapture
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'setPointerCapture')
  }
})

describe.each([
  { viewport: 'desktop', width: 1024 },
  { viewport: 'mobile', width: 390 },
])('model ordering on $viewport', ({ width }) => {
  test('switches between groups and channels and clears the explicit channel when returning', async () => {
    vi.stubGlobal('innerWidth', width)
    const user = userEvent.setup()
    const onChannelChange = vi.fn()
    const props = {
      models: [{ label: 'gpt-2', value: 'gpt-2' }],
      selectedModel: 'gpt-2',
      onModelChange: vi.fn(),
      groups: [{ label: 'Default', value: 'default' }],
      selectedGroup: 'default',
      onGroupChange: vi.fn(),
      channels: [{ label: 'Channel A', value: 12 }],
      onChannelChange,
    }
    const view = render(<ModelGroupSelector {...props} />)
    await user.click(screen.getByRole('combobox'))
    await user.click(screen.getByRole('button', { name: 'Channels' }))
    await user.click(screen.getByRole('button', { name: 'Channel A' }))
    expect(onChannelChange).toHaveBeenCalledWith(12)

    view.rerender(<ModelGroupSelector {...props} selectedChannel={12} />)
    await user.click(screen.getByRole('button', { name: 'Groups' }))
    expect(onChannelChange).toHaveBeenLastCalledWith(undefined)
    expect(screen.getByRole('button', { name: 'Default' })).toBeVisible()
    expect(props.onModelChange).not.toHaveBeenCalled()
  })

  test('does not expose channels when the caller has no channel navigation permission', async () => {
    vi.stubGlobal('innerWidth', width)
    const user = userEvent.setup()
    render(
      <ModelGroupSelector
        models={[]}
        selectedModel=''
        onModelChange={vi.fn()}
        groups={[]}
        selectedGroup=''
        onGroupChange={vi.fn()}
      />
    )
    await user.click(screen.getByRole('combobox'))
    expect(
      screen.queryByRole('button', { name: 'Channels' })
    ).not.toBeInTheDocument()
  })

  test('sorts names naturally with raw model ID ties without changing the input or saved selection', async () => {
    vi.stubGlobal('innerWidth', width)
    const user = userEvent.setup()
    const onModelChange = vi.fn()
    const models = [
      { label: 'gpt-10', value: 'gpt-10' },
      { label: 'gpt-2', value: 'model-2' },
      { label: 'GPT-2', value: 'model-10' },
      { label: 'claude-1', value: 'claude-1' },
    ]
    Object.freeze(models)
    render(
      <ModelGroupSelector
        models={models}
        selectedModel='gpt-10'
        onModelChange={onModelChange}
        groups={[{ label: 'Default', value: 'default' }]}
        selectedGroup='default'
        onGroupChange={vi.fn()}
      />
    )
    const trigger = screen.getByRole('combobox')

    await user.click(trigger)

    expect(
      screen.getAllByRole('option').map((item) => item.textContent)
    ).toEqual(['claude-1', 'GPT-2', 'gpt-2', 'gpt-10'])
    expect(models.map((model) => model.value)).toEqual([
      'gpt-10',
      'model-2',
      'model-10',
      'claude-1',
    ])
    expect(trigger).toHaveTextContent('gpt-10')
    expect(onModelChange).not.toHaveBeenCalled()
  })

  test('keeps filtered models in natural order and selects the requested model ID', async () => {
    vi.stubGlobal('innerWidth', width)
    const user = userEvent.setup()
    const onModelChange = vi.fn()
    render(
      <ModelGroupSelector
        models={[
          { label: 'gpt-10', value: 'gpt-10' },
          { label: 'claude-1', value: 'claude-1' },
          { label: 'gpt-2', value: 'gpt-2' },
        ]}
        selectedModel='gpt-10'
        onModelChange={onModelChange}
        groups={[]}
        selectedGroup=''
        onGroupChange={vi.fn()}
      />
    )
    await user.click(screen.getByRole('combobox'))

    await user.type(screen.getByPlaceholderText('Search models...'), 'gpt')

    expect(
      screen.getAllByRole('option').map((item) => item.textContent)
    ).toEqual(['gpt-2', 'gpt-10'])
    expect(onModelChange).not.toHaveBeenCalled()
    await user.click(screen.getByRole('option', { name: 'gpt-2' }))
    expect(onModelChange).toHaveBeenCalledExactlyOnceWith('gpt-2')
  })

  test('sorts options within the existing category order without changing selection or the source list', async () => {
    vi.stubGlobal('innerWidth', width)
    const user = userEvent.setup()
    const onModelChange = vi.fn()
    const models = [
      { label: 'gpt-10', value: 'gpt-10', category: 'Beta' },
      { label: 'gpt-2', value: 'gpt-2', category: 'Beta' },
      { label: 'claude-10', value: 'claude-10', category: 'Alpha' },
      { label: 'claude-2', value: 'claude-2', category: 'Alpha' },
    ]
    Object.freeze(models)
    render(
      <ModelSelector
        models={models}
        selectedModel='gpt-10'
        onModelChange={onModelChange}
      />
    )

    const trigger = screen.getByRole('combobox')
    await user.click(trigger)

    expect(
      screen.getAllByRole('option').map((item) => item.textContent)
    ).toEqual(['gpt-2', 'gpt-10', 'claude-2', 'claude-10'])
    expect(models.map((model) => model.value)).toEqual([
      'gpt-10',
      'gpt-2',
      'claude-10',
      'claude-2',
    ])
    expect(trigger).toHaveTextContent('gpt-10')
    expect(onModelChange).not.toHaveBeenCalled()
  })
})

test('string model choices use the same natural ordering and raw-ID ties without mutating the catalog', () => {
  const models = ['model-10', 'model-2', 'Model-2', 'alpha']
  Object.freeze(models)
  expect([...models].sort(compareModelNames)).toEqual([
    'alpha',
    'Model-2',
    'model-2',
    'model-10',
  ])
  expect(models).toEqual(['model-10', 'model-2', 'Model-2', 'alpha'])
})
