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
import i18next from 'i18next'
import { beforeAll, describe, expect, test, vi } from 'vitest'

import type { PlaygroundConversation } from '../../../types'
import { ConversationList } from '../conversation-list'

const keys = {
  Archived: 'Archived',
  Archive: 'Archive',
  Unarchive: 'Unarchive',
  Delete: 'Delete',
  Rename: 'Rename',
  'More options': 'More options',
  'Delete chat?': 'Delete chat?',
  'This chat will be permanently deleted. This cannot be undone.':
    'This chat will be permanently deleted. This cannot be undone.',
  'New chat': 'New chat',
  Cancel: 'Cancel',
}

function conversation(
  id: string,
  overrides: Partial<PlaygroundConversation> = {}
): PlaygroundConversation {
  return {
    id,
    title: `Chat ${id}`,
    createdAt: 1_700_000_000_000,
    updatedAt: 1_700_000_000_000,
    ...overrides,
  }
}

function renderList(
  conversations: PlaygroundConversation[],
  activeConversationId: string | null
) {
  const handlers = {
    onSelect: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onArchive: vi.fn(),
  }

  render(
    <ConversationList
      activeConversationId={activeConversationId}
      conversations={conversations}
      onArchive={handlers.onArchive}
      onDelete={handlers.onDelete}
      onRename={handlers.onRename}
      onSelect={handlers.onSelect}
    />
  )

  return handlers
}

beforeAll(() => {
  i18next.addResourceBundle('en', 'translation', keys)
})

describe('ConversationList', () => {
  test('lists open chats by recency and marks the active one', () => {
    renderList(
      [
        conversation('older', { updatedAt: 1_700_000_000_000 }),
        conversation('newer', { updatedAt: 1_700_000_500_000 }),
      ],
      'older'
    )

    const items = screen.getAllByRole('button', { name: /Chat (older|newer)/ })
    expect(items.map((item) => item.textContent)).toEqual([
      expect.stringContaining('Chat newer'),
      expect.stringContaining('Chat older'),
    ])
    expect(
      screen
        .getByRole('button', { name: /Chat older/ })
        .getAttribute('aria-current')
    ).toBe('true')
    expect(
      screen
        .getByRole('button', { name: /Chat newer/ })
        .getAttribute('aria-current')
    ).toBeNull()
  })

  test('keeps archived chats collapsed until the group is opened', async () => {
    const user = userEvent.setup()
    renderList([conversation('archived', { archived: true })], null)

    expect(screen.queryByRole('button', { name: /Chat archived/ })).toBeNull()

    await user.click(screen.getByRole('button', { name: /Archived/ }))

    expect(
      screen.getByRole('button', { name: /Chat archived/ })
    ).toBeInTheDocument()
  })

  test('selects a chat when its title is clicked', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a')], 'a')

    await user.click(screen.getByRole('button', { name: /Chat a/ }))

    expect(handlers.onSelect).toHaveBeenCalledWith('a')
  })

  test('archives a chat from its actions menu', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a')], 'a')

    await user.click(screen.getByRole('button', { name: 'More options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Archive' }))

    expect(handlers.onArchive).toHaveBeenCalledWith('a', true)
  })

  test('asks for confirmation before deleting a chat', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a')], 'a')

    await user.click(screen.getByRole('button', { name: 'More options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))

    expect(handlers.onDelete).not.toHaveBeenCalled()
    expect(await screen.findByText('Delete chat?')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Delete' }))

    expect(handlers.onDelete).toHaveBeenCalledWith('a')
  })
})
