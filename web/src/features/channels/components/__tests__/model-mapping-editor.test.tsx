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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { ModelMappingEditor } from '../model-mapping-editor'

test('external mapping changes update the editor without emitting an edit', () => {
  const onChange = vi.fn()
  const view = render(
    <ModelMappingEditor value='{"client-a":"upstream-a"}' onChange={onChange} />
  )
  expect(screen.getByDisplayValue('client-a')).toBeVisible()
  expect(screen.getByDisplayValue('upstream-a')).toBeVisible()

  view.rerender(
    <ModelMappingEditor value='{"client-b":"upstream-b"}' onChange={onChange} />
  )
  expect(screen.getByDisplayValue('client-b')).toBeVisible()
  expect(screen.getByDisplayValue('upstream-b')).toBeVisible()
  expect(screen.queryByDisplayValue('client-a')).not.toBeInTheDocument()
  expect(onChange).not.toHaveBeenCalled()
})

test('language changes preserve draft mappings and explain the same direction in JSON mode', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en, zh },
    keySeparator: false,
    interpolation: { escapeValue: false },
  })
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(
    <I18nextProvider i18n={i18n}>
      <ModelMappingEditor value='' onChange={onChange} />
    </I18nextProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  await user.type(screen.getByPlaceholderText('gpt-3.5-turbo'), 'client-alias')
  await user.type(
    screen.getByPlaceholderText('gpt-3.5-turbo-0125'),
    'provider-model'
  )

  await act(() => i18n.changeLanguage('zh'))
  expect(screen.getByText('请求模型名称')).toBeVisible()
  expect(screen.getByText('上游模型名称')).toBeVisible()
  expect(screen.getByDisplayValue('client-alias')).toBeVisible()
  expect(screen.getByDisplayValue('provider-model')).toBeVisible()

  await user.click(screen.getByRole('tab', { name: 'JSON' }))
  expect(
    screen.getByText('JSON 的键是请求模型名称，值是上游模型名称。')
  ).toBeVisible()
  expect(screen.getByRole('textbox', { name: '模型映射' })).toHaveValue(
    '{\n  "client-alias": "provider-model"\n}'
  )
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "client-alias": "provider-model"\n}'
  )
})

test('a new row focuses naturally sorted searchable choices without reordering catalogs, mapping rows or emitted keys', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const sourceModels = [
    'model-10',
    'beta-model',
    'alpha-model',
    'model-2',
    'Alpha-model',
  ]
  const targetModels = [
    'upstream-10',
    'z-upstream',
    'upstream-2',
    'alpha-upstream',
    'Alpha-upstream',
  ]
  Object.freeze(sourceModels)
  Object.freeze(targetModels)
  render(
    <ModelMappingEditor
      value='{"z-alias":"z-upstream","a-alias":"a-upstream"}'
      onChange={onChange}
      sourceModelOptions={sourceModels}
      targetModelOptions={targetModels}
    />
  )
  expect(onChange).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  const source = screen.getAllByRole('combobox', {
    name: 'Request Model Name',
  })[2]
  expect(source).toHaveFocus()
  expect(source).toHaveAttribute('aria-expanded', 'true')
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual([
    'Alpha-model',
    'alpha-model',
    'beta-model',
    'model-2',
    'model-10',
  ])
  expect(onChange).toHaveBeenCalledExactlyOnceWith(
    '{\n  "z-alias": "z-upstream",\n  "a-alias": "a-upstream"\n}'
  )
  await user.click(screen.getByRole('option', { name: 'model-2' }))
  expect(source).toHaveValue('model-2')
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "z-alias": "z-upstream",\n  "a-alias": "a-upstream",\n  "model-2": ""\n}'
  )

  const target = screen.getAllByRole('combobox', {
    name: 'Upstream Model Name',
  })[2]
  await user.click(target)
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual([
    'Alpha-upstream',
    'alpha-upstream',
    'upstream-2',
    'upstream-10',
    'z-upstream',
  ])
  await user.type(target, 'upstream-')
  expect(
    screen.getAllByRole('option').map((option) => option.textContent)
  ).toEqual(['upstream-2', 'upstream-10'])
  await user.click(screen.getByRole('option', { name: 'upstream-10' }))
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "z-alias": "z-upstream",\n  "a-alias": "a-upstream",\n  "model-2": "upstream-10"\n}'
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' }).map(
      (input) => (input as HTMLInputElement).value
    )
  ).toEqual(['z-alias', 'a-alias', 'model-2'])
  expect(
    screen.getAllByRole('combobox', { name: 'Upstream Model Name' }).map(
      (input) => (input as HTMLInputElement).value
    )
  ).toEqual(['z-upstream', 'a-upstream', 'upstream-10'])
  expect(sourceModels).toEqual([
    'model-10',
    'beta-model',
    'alpha-model',
    'model-2',
    'Alpha-model',
  ])
  expect(targetModels).toEqual([
    'upstream-10',
    'z-upstream',
    'upstream-2',
    'alpha-upstream',
    'Alpha-upstream',
  ])
})

test('the row filter appears from six mappings and narrows the visible rows by either name', async () => {
  const user = userEvent.setup()
  const five = JSON.stringify(
    Object.fromEntries([1, 2, 3, 4, 5].map((n) => [`alpha-${n}`, `up-${n}`]))
  )
  const view = render(<ModelMappingEditor value={five} onChange={vi.fn()} />)
  expect(
    screen.queryByRole('textbox', { name: 'Filter mappings' })
  ).not.toBeInTheDocument()

  const six = JSON.stringify({ ...JSON.parse(five), 'beta-6': 'up-6' })
  view.rerender(<ModelMappingEditor value={six} onChange={vi.fn()} />)
  await user.type(
    screen.getByRole('textbox', { name: 'Filter mappings' }),
    'beta'
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })
  ).toHaveLength(1)
  expect(screen.getByDisplayValue('beta-6')).toBeVisible()
  expect(screen.getByText('Showing 1 of 6 mappings')).toBeVisible()

  await user.clear(screen.getByRole('textbox', { name: 'Filter mappings' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Filter mappings' }),
    'up-2'
  )
  expect(screen.getByDisplayValue('alpha-2')).toBeVisible()
  expect(screen.getByText('Showing 1 of 6 mappings')).toBeVisible()
})

test('the batch button is only offered when the caller handles it', async () => {
  const user = userEvent.setup()
  const onBatchAdd = vi.fn()
  const view = render(<ModelMappingEditor value='' onChange={vi.fn()} />)
  expect(
    screen.queryByRole('button', { name: 'Batch Add' })
  ).not.toBeInTheDocument()

  view.rerender(
    <ModelMappingEditor value='' onChange={vi.fn()} onBatchAdd={onBatchAdd} />
  )
  await user.click(screen.getByRole('button', { name: 'Batch Add' }))
  expect(onBatchAdd).toHaveBeenCalledTimes(1)
})

test('a draft request with a known request name appends a row, focuses the upstream field, and stays unsaved until completed', async () => {
  const onChange = vi.fn()
  const handled = vi.fn()
  render(
    <ModelMappingEditor
      value=''
      onChange={onChange}
      draftRequest={{ from: 'alias', to: '', token: 1 }}
      onDraftRequestHandled={handled}
    />
  )
  await waitFor(() =>
    expect(
      screen.getByRole('combobox', { name: 'Upstream Model Name' })
    ).toHaveFocus()
  )
  expect(
    screen.getByRole('combobox', { name: 'Request Model Name' })
  ).toHaveValue('alias')
  expect(handled).toHaveBeenCalledTimes(1)
  expect(onChange).not.toHaveBeenCalled()
})

test('a draft request with only the upstream name focuses the request field, and a known request name reuses its existing row', async () => {
  const view = render(
    <ModelMappingEditor
      value='{"alias":"old"}'
      onChange={vi.fn()}
      draftRequest={{ from: '', to: 'up-1', token: 1 }}
    />
  )
  // The row is committed before its passive effect moves focus.
  await waitFor(() => {
    const requestFields = screen.getAllByRole('combobox', {
      name: 'Request Model Name',
    })
    expect(requestFields).toHaveLength(2)
    expect(requestFields[1]).toHaveFocus()
  })
  expect(screen.getByDisplayValue('up-1')).toBeVisible()

  view.rerender(
    <ModelMappingEditor
      value='{"alias":"old"}'
      onChange={vi.fn()}
      draftRequest={{ from: 'alias', to: '', token: 2 }}
    />
  )
  await waitFor(() =>
    expect(
      screen.getAllByRole('combobox', { name: 'Upstream Model Name' })[0]
    ).toHaveFocus()
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })
  ).toHaveLength(2)
})

test('a draft request can ask for the request field of an existing row', async () => {
  render(
    <ModelMappingEditor
      value='{"alias":"old"}'
      onChange={vi.fn()}
      draftRequest={{ from: 'alias', to: 'old', focus: 'from', token: 1 }}
    />
  )
  await waitFor(() =>
    expect(
      screen.getByRole('combobox', { name: 'Request Model Name' })
    ).toHaveFocus()
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })
  ).toHaveLength(1)
})

test('commit fires once an edit settles on Enter or when focus leaves, not per keystroke', async () => {
  const user = userEvent.setup()
  const onCommit = vi.fn()
  render(
    <>
      <ModelMappingEditor
        value='{"alias":""}'
        onChange={vi.fn()}
        onCommit={onCommit}
      />
      <button type='button'>outside</button>
    </>
  )
  const upstream = screen.getByRole('combobox', { name: 'Upstream Model Name' })
  await user.type(upstream, 'up-1')
  expect(onCommit).not.toHaveBeenCalled()
  await user.keyboard('{Enter}')
  expect(onCommit).toHaveBeenCalledTimes(1)
  expect(onCommit).toHaveBeenLastCalledWith('{\n  "alias": "up-1"\n}')

  await user.click(screen.getByRole('combobox', { name: 'Request Model Name' }))
  expect(onCommit).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('button', { name: 'outside' }))
  expect(onCommit).toHaveBeenCalledTimes(2)
})
