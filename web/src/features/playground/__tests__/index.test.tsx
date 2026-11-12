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
import {
  afterAll,
  beforeAll,
  beforeEach,
  describe,
  expect,
  test,
  vi,
} from 'vitest'

import { Playground } from '../index'
import type { PlaygroundConversation } from '../types'

// jsdom does not implement the Web Animations API that the scroll area reads.
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (getAnimationsDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      getAnimationsDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

const chatState = {
  config: {
    model: 'gpt-4o',
    group: 'default',
    temperature: 0.7,
    top_p: 1,
    max_tokens: 4096,
    frequency_penalty: 0,
    presence_penalty: 0,
    seed: null,
    stream: true,
  },
  parameterEnabled: {
    temperature: true,
    top_p: true,
    max_tokens: false,
    frequency_penalty: true,
    presence_penalty: true,
    seed: false,
  },
  conversations: [] as PlaygroundConversation[],
  activeConversationId: 'first',
  messages: [],
  isLoadingMessages: false,
  storageFailure: null,
  models: [],
  groups: [],
  setModels: vi.fn(),
  setGroups: vi.fn(),
  updateConfig: vi.fn(),
  updateParameterEnabled: vi.fn(),
  updateMessages: vi.fn(),
  clearMessages: vi.fn(),
  resetConfig: vi.fn(),
  createConversation: vi.fn(),
  switchConversation: vi.fn(),
  renameConversation: vi.fn(),
  deleteConversation: vi.fn(),
  archiveConversation: vi.fn(),
  dismissStorageFailure: vi.fn(),
}

const chatHandler = {
  sendChat: vi.fn(),
  stopGeneration: vi.fn(),
  isGenerating: false,
}

vi.mock('../hooks', () => ({
  usePlaygroundState: () => chatState,
  useChatHandler: () => chatHandler,
  usePlaygroundOptions: () => ({ isLoadingModels: false }),
  usePlaygroundConversation: () => ({
    editingMessageKey: null,
    handleSendMessage: vi.fn(),
    handleRegenerateMessage: vi.fn(),
    handleEditMessage: vi.fn(),
    handleEditOpenChange: vi.fn(),
    applyEdit: vi.fn(),
    handleDeleteMessage: vi.fn(),
  }),
}))

vi.mock('../components/chat/playground-chat', () => ({
  PlaygroundChat: () => <div data-testid='playground-chat' />,
}))

vi.mock('../components/input/playground-input', () => ({
  PlaygroundInput: () => <div data-testid='playground-input' />,
}))

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

beforeEach(() => {
  chatState.conversations = [conversation('first'), conversation('second')]
  chatState.activeConversationId = 'first'
  chatHandler.isGenerating = false
})

describe('Playground conversation switching', () => {
  test('switches to the selected chat', async () => {
    const user = userEvent.setup()
    render(<Playground />)

    await user.click(screen.getByRole('button', { name: /Chat second/ }))

    expect(chatState.switchConversation).toHaveBeenCalledWith('second')
  })

  test('stops the running generation before switching chats', async () => {
    const user = userEvent.setup()
    chatHandler.isGenerating = true
    render(<Playground />)

    await user.click(screen.getByRole('button', { name: /Chat second/ }))

    expect(chatHandler.stopGeneration).toHaveBeenCalledTimes(1)
    expect(chatHandler.stopGeneration.mock.invocationCallOrder[0]).toBeLessThan(
      chatState.switchConversation.mock.invocationCallOrder[0]
    )
  })

  test('opens the chat that was created with the new chat button', async () => {
    const user = userEvent.setup()
    chatState.createConversation.mockImplementation(() => 'created')
    render(<Playground />)

    await user.click(screen.getByRole('button', { name: 'New chat' }))

    expect(chatState.createConversation).toHaveBeenCalledTimes(1)
  })
})
