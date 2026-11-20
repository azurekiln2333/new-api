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
import { expect, test, vi } from 'vitest'

import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../../../constants'
import { PlaygroundInputTools } from '../playground-input-tools'
import { PlaygroundParameterPanel } from '../playground-parameter-panel'

test('changing reasoning effort reports the selected value', async () => {
  const onConfigChange = vi.fn()
  const user = userEvent.setup()
  render(
    <PlaygroundParameterPanel
      config={{ ...DEFAULT_CONFIG, reasoning_effort: 'medium' }}
      parameterEnabled={{
        ...DEFAULT_PARAMETER_ENABLED,
        reasoning_effort: true,
      }}
      onConfigChange={onConfigChange}
      onParameterEnabledChange={vi.fn()}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Parameters' }))
  const select = await screen.findByLabelText('Reasoning Effort')

  expect(select).toHaveValue('medium')
  await user.selectOptions(select, 'xhigh')

  expect(onConfigChange).toHaveBeenCalledWith('reasoning_effort', 'xhigh')
})

test('the reasoning effort control follows the parameter enable switch', async () => {
  const onParameterEnabledChange = vi.fn()
  const user = userEvent.setup()
  render(
    <PlaygroundParameterPanel
      config={{ ...DEFAULT_CONFIG, reasoning_effort: 'medium' }}
      parameterEnabled={{
        ...DEFAULT_PARAMETER_ENABLED,
        reasoning_effort: false,
      }}
      onConfigChange={vi.fn()}
      onParameterEnabledChange={onParameterEnabledChange}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Parameters' }))
  const select = await screen.findByLabelText('Reasoning Effort')

  expect(select).toBeDisabled()
  await user.click(
    screen.getByRole('switch', { name: 'Enable Reasoning Effort' })
  )

  expect(onParameterEnabledChange).toHaveBeenCalledWith(
    'reasoning_effort',
    true
  )
})

test('web search toggles explicitly and follows the input disabled state', async () => {
  const user = userEvent.setup()
  const onConfigChange = vi.fn()
  const props = {
    config: DEFAULT_CONFIG,
    parameterEnabled: DEFAULT_PARAMETER_ENABLED,
    onConfigChange,
    onParameterEnabledChange: vi.fn(),
  }
  const { rerender } = render(<PlaygroundInputTools {...props} />)
  const search = screen.getByRole('button', { name: 'Search' })
  expect(search).toHaveAttribute('aria-pressed', 'false')
  await user.click(search)
  expect(onConfigChange).toHaveBeenLastCalledWith('web_search', true)

  rerender(
    <PlaygroundInputTools
      {...props}
      config={{ ...DEFAULT_CONFIG, web_search: true }}
    />
  )
  expect(search).toHaveAttribute('aria-pressed', 'true')
  await user.click(search)
  expect(onConfigChange).toHaveBeenLastCalledWith('web_search', false)

  rerender(<PlaygroundInputTools {...props} disabled />)
  expect(search).toBeDisabled()
})
