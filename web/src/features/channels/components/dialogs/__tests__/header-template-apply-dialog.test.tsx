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
import { describe, expect, test, vi } from 'vitest'

import { HeaderTemplateApplyDialog } from '../header-template-apply-dialog'

function renderDialog(handlers: {
  onReplace?: () => void
  onMerge?: () => void
  onOpenChange?: (open: boolean) => void
}) {
  return render(
    <HeaderTemplateApplyDialog
      open
      templateName='OpenCode'
      onOpenChange={handlers.onOpenChange ?? (() => {})}
      onReplace={handlers.onReplace ?? (() => {})}
      onMerge={handlers.onMerge ?? (() => {})}
    />
  )
}

describe('HeaderTemplateApplyDialog', () => {
  test('shows the picked template name in the prompt', () => {
    renderDialog({})
    expect(screen.getByText(/OpenCode/)).toBeInTheDocument()
  })

  test('replace button reports a replace choice', async () => {
    const onReplace = vi.fn()
    renderDialog({ onReplace })
    await userEvent.click(screen.getByRole('button', { name: 'Replace' }))
    expect(onReplace).toHaveBeenCalledTimes(1)
  })

  test('merge button reports a merge choice', async () => {
    const onMerge = vi.fn()
    renderDialog({ onMerge })
    await userEvent.click(screen.getByRole('button', { name: 'Merge' }))
    expect(onMerge).toHaveBeenCalledTimes(1)
  })

  test('cancel closes without applying a template', async () => {
    const onReplace = vi.fn()
    const onMerge = vi.fn()
    const onOpenChange = vi.fn()
    renderDialog({ onReplace, onMerge, onOpenChange })
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onOpenChange.mock.calls[0]?.[0]).toBe(false)
    expect(onReplace).not.toHaveBeenCalled()
    expect(onMerge).not.toHaveBeenCalled()
  })
})
