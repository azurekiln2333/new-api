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
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeAll, describe, expect, test, vi } from 'vitest'

import type { PlaygroundConversation, PlaygroundProject } from '../../../types'
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
  'Move to project': 'Move to project',
  Ungrouped: 'Ungrouped',
  'New project': 'New project',
  'Project name': 'Project name',
  'Rename project': 'Rename project',
  'Delete project?': 'Delete project?',
  'Chats in this project will be moved to Ungrouped instead of being deleted.':
    'Chats in this project will be moved to Ungrouped instead of being deleted.',
  Save: 'Save',
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

function project(id: string, overrides: Partial<PlaygroundProject> = {}) {
  return {
    id,
    name: `Project ${id}`,
    createdAt: 1_700_000_000_000,
    updatedAt: 1_700_000_000_000,
    ...overrides,
  }
}

function renderList(
  conversations: PlaygroundConversation[],
  activeConversationId: string | null,
  projects: PlaygroundProject[] = []
) {
  const handlers = {
    onSelect: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onArchive: vi.fn(),
    onMoveToProject: vi.fn(),
    onCreateProject: vi.fn(),
    onRenameProject: vi.fn(),
    onDeleteProject: vi.fn(),
  }

  render(
    <ConversationList
      activeConversationId={activeConversationId}
      conversations={conversations}
      projects={projects}
      onArchive={handlers.onArchive}
      onCreateProject={handlers.onCreateProject}
      onDelete={handlers.onDelete}
      onDeleteProject={handlers.onDeleteProject}
      onMoveToProject={handlers.onMoveToProject}
      onRename={handlers.onRename}
      onRenameProject={handlers.onRenameProject}
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

  test('groups chats under their project and keeps ungrouped chats last', () => {
    renderList(
      [
        conversation('free', { updatedAt: 1_700_000_900_000 }),
        conversation('inside', { projectId: 'p1' }),
      ],
      null,
      [project('p1', { name: 'Research' })]
    )

    const projectHeader = screen.getByRole('button', { name: /Research/ })
    const ungroupedChat = screen.getByRole('button', { name: /Chat free/ })

    expect(
      projectHeader.compareDocumentPosition(ungroupedChat) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(
      within(
        projectHeader.closest('[data-slot=collapsible]') as HTMLElement
      ).getByRole('button', { name: /Chat inside/ })
    ).toBeInTheDocument()
  })

  test('collapses a project so its chats are hidden', async () => {
    const user = userEvent.setup()
    renderList([conversation('inside', { projectId: 'p1' })], null, [
      project('p1'),
    ])

    const projectHeader = screen.getByRole('button', { name: /Project p1/ })
    expect(projectHeader.getAttribute('aria-expanded')).toBe('true')

    await user.click(projectHeader)

    expect(
      screen
        .getByRole('button', { name: /Project p1/ })
        .getAttribute('aria-expanded')
    ).toBe('false')
    expect(
      screen.queryByRole('button', { name: /Chat inside/ })
    ).not.toBeInTheDocument()
  })

  test('moves an ungrouped chat into a project from its actions menu', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a')], 'a', [project('p1')])

    await user.click(screen.getByRole('button', { name: 'More options' }))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Move to project' })
    )
    fireEvent.click(await screen.findByRole('menuitem', { name: /Project p1/ }))

    expect(handlers.onMoveToProject).toHaveBeenCalledWith('a', 'p1')
  })

  test('moves a project chat back to ungrouped', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a', { projectId: 'p1' })], 'a', [
      project('p1'),
    ])

    await user.click(
      screen
        .getAllByRole('button', { name: 'More options' })
        .at(-1) as HTMLElement
    )
    await user.click(
      await screen.findByRole('menuitem', { name: 'Move to project' })
    )
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Ungrouped' }))

    expect(handlers.onMoveToProject).toHaveBeenCalledWith('a', null)
  })

  test('creates a project from the project name dialog', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a')], 'a')

    await user.click(screen.getByRole('button', { name: 'New project' }))
    await user.type(await screen.findByLabelText('Project name'), 'Research')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(handlers.onCreateProject).toHaveBeenCalledWith('Research')
  })

  test('asks for confirmation before deleting a project', async () => {
    const user = userEvent.setup()
    const handlers = renderList([conversation('a', { projectId: 'p1' })], 'a', [
      project('p1'),
    ])

    await user.click(screen.getAllByRole('button', { name: 'More options' })[0])
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))

    expect(handlers.onDeleteProject).not.toHaveBeenCalled()
    expect(await screen.findByText('Delete project?')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Delete' }))

    expect(handlers.onDeleteProject).toHaveBeenCalledWith('p1')
    expect(handlers.onDelete).not.toHaveBeenCalled()
  })
})
