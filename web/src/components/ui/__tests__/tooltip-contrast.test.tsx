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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'

import { Kbd } from '../kbd'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '../tooltip'

test('keyboard focus exposes a theme-paired tooltip without inverting its shortcut text', async () => {
  const user = userEvent.setup()
  render(
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger>Copy</TooltipTrigger>
        <TooltipContent>
          Copy value <Kbd>Ctrl+C</Kbd>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )

  await user.tab()

  const tooltip = await screen.findByText('Copy value')
  expect(screen.getByRole('button', { name: 'Copy' })).toHaveFocus()
  // A normal surface is necessary for nested foreground/muted text in both
  // themes. An inverse tooltip makes those children match its background.
  expect(tooltip).toHaveClass('bg-popover', 'text-popover-foreground')
  expect(tooltip).not.toHaveClass('bg-foreground', 'text-background')
  const shortcut = within(tooltip).getByText('Ctrl+C')
  expect(shortcut).toBeVisible()
  expect(shortcut).toHaveClass('bg-muted', 'text-muted-foreground')
  expect(shortcut).not.toHaveClass(
    'in-data-[slot=tooltip-content]:text-background'
  )
})
