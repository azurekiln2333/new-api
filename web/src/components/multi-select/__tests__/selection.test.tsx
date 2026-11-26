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
import { useState } from 'react'
import { describe, expect, it } from 'vitest'

import { MultiSelect } from '@/components/multi-select'
import { compareModelOptions } from '@/lib/model-options'

const options = [
  { value: 'a', label: 'a' },
  { value: 'c', label: 'c' },
]

function Fixture(props: {
  initialSelected?: string[]
  allowCreate?: boolean
  disabled?: boolean
  options?: { label: string; value: string }[]
  compareOptions?: typeof compareModelOptions
}) {
  const [selected, setSelected] = useState(props.initialSelected ?? [])

  return (
    <>
      <MultiSelect
        options={props.options ?? options}
        selected={selected}
        onChange={setSelected}
        compareOptions={props.compareOptions}
        allowCreate={props.allowCreate ?? true}
        disabled={props.disabled}
      />
      <output aria-label='Selected values'>{selected.join(',')}</output>
    </>
  )
}

describe('multi-select display ordering', () => {
  const modelOptions = [
    { value: 'model-10', label: 'Model 10' },
    { value: 'model-2', label: 'Model 2' },
    { value: 'alias-b', label: 'ALPHA' },
    { value: 'alias-a', label: 'Alpha' },
  ]

  it.each([false, true])(
    'sorts only offered and selected-only options when opted in=%s, leaving chips and emitted values in selection order',
    async (sorted) => {
      const user = userEvent.setup()
      const selected = ['model-10', 'beta-1']
      const originalOptions = modelOptions.map((option) => ({ ...option }))
      Object.freeze(modelOptions)
      Object.freeze(selected)
      const view = render(
        <Fixture
          options={modelOptions}
          initialSelected={selected}
          allowCreate={false}
          compareOptions={sorted ? compareModelOptions : undefined}
        />
      )
      const chips = () =>
        [...view.container.querySelectorAll('[data-slot="combobox-chip"]')].map(
          (chip) => chip.textContent
        )
      expect(chips()).toEqual(['Model 10', 'beta-1'])
      await user.click(screen.getByRole('combobox'))
      expect(
        screen.getAllByRole('option').map((option) => option.textContent)
      ).toEqual(
        sorted
          ? ['Alpha', 'ALPHA', 'beta-1', 'Model 2', 'Model 10']
          : ['Model 10', 'Model 2', 'ALPHA', 'Alpha', 'beta-1']
      )
      expect(modelOptions).toEqual(originalOptions)
      expect(selected).toEqual(['model-10', 'beta-1'])
      expect(screen.getByLabelText('Selected values')).toHaveTextContent(
        /^model-10,beta-1$/
      )
      await user.click(screen.getByRole('option', { name: 'Model 2' }))
      expect(screen.getByLabelText('Selected values')).toHaveTextContent(
        /^model-10,beta-1,model-2$/
      )
      expect(chips()).toEqual(['Model 10', 'beta-1', 'Model 2'])
    }
  )

  it('keeps the create action after sorted matching options and appends its value without reordering selections', async () => {
    const user = userEvent.setup()
    render(
      <Fixture
        options={[
          { value: 'model-10', label: 'model-10' },
          { value: 'model-2', label: 'model-2' },
        ]}
        initialSelected={['legacy-model']}
        compareOptions={compareModelOptions}
      />
    )
    const input = screen.getByRole('combobox')
    await user.type(input, 'model-1')
    expect(
      screen.getAllByRole('option').map((option) => option.textContent)
    ).toEqual(['model-10', 'Add "model-1"'])
    await user.click(screen.getByRole('option', { name: 'Add "model-1"' }))
    expect(screen.getByLabelText('Selected values')).toHaveTextContent(
      /^legacy-model,model-1$/
    )
    expect(input).toHaveValue('')
  })
})

describe('multi-select batch paste', () => {
  it.each(['a,b,c', 'a，b，c', 'a\nb\nc', 'a\r\nb\r\nc'])(
    'adds every pasted value including the final item without a trailing separator: %j',
    async (text) => {
      const user = userEvent.setup()
      render(<Fixture />)
      const input = screen.getByRole('combobox')

      await user.click(input)
      await user.paste(text)

      expect(screen.getByLabelText('Selected values')).toHaveTextContent(
        /^a,b,c$/
      )
      expect(input).toHaveValue('')
      expect(input).toHaveFocus()
    }
  )

  it('preserves existing selections and ignores duplicate, blank, and trailing entries', async () => {
    const user = userEvent.setup()
    render(<Fixture initialSelected={['existing', 'a']} />)
    const input = screen.getByRole('combobox')

    await user.click(input)
    await user.paste(' a,, b，b\r\nc, ')

    expect(screen.getByLabelText('Selected values')).toHaveTextContent(
      /^existing,a,b,c$/
    )
    expect(input).toHaveValue('')
  })

  it('replaces the selected draft text when pasting a batch and preserves surrounding text', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    const input = screen.getByRole<HTMLInputElement>('combobox')

    await user.type(input, 'model-old-end')
    input.setSelectionRange(6, 9)
    await user.paste('a,b')

    expect(screen.getByLabelText('Selected values')).toHaveTextContent(
      /^model-a,b-end$/
    )
    expect(input).toHaveValue('')
  })

  it('keeps the last manually typed value as a draft until a separator is typed', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    const input = screen.getByRole('combobox')

    await user.type(input, 'a,b,c')

    expect(screen.getByLabelText('Selected values')).toHaveTextContent(/^a,b$/)
    expect(input).toHaveValue('c')

    await user.type(input, ',')

    expect(screen.getByLabelText('Selected values')).toHaveTextContent(
      /^a,b,c$/
    )
    expect(input).toHaveValue('')
  })

  it('keeps a single pasted value available for search and explicit selection', async () => {
    const user = userEvent.setup()
    render(<Fixture />)
    const input = screen.getByRole('combobox')

    await user.click(input)
    await user.paste('c')

    expect(screen.getByLabelText('Selected values')).toBeEmptyDOMElement()
    expect(input).toHaveValue('c')

    await user.click(screen.getByRole('option', { name: 'c' }))

    expect(screen.getByLabelText('Selected values')).toHaveTextContent(/^c$/)
    expect(input).toHaveValue('')
  })

  it('leaves pasted text as search input when custom creation is disabled', async () => {
    const user = userEvent.setup()
    render(<Fixture allowCreate={false} />)
    const input = screen.getByRole('combobox')

    await user.click(input)
    await user.paste('a,b,c')

    expect(screen.getByLabelText('Selected values')).toBeEmptyDOMElement()
    expect(input).toHaveValue('a,b,c')
  })

  it('does not add pasted values when the control is disabled', async () => {
    const user = userEvent.setup()
    render(<Fixture disabled />)
    const input = screen.getByRole('combobox')

    await user.click(input)
    await user.paste('a,b,c')

    expect(input).toBeDisabled()
    expect(screen.getByLabelText('Selected values')).toBeEmptyDOMElement()
    expect(input).toHaveValue('')
  })
})
