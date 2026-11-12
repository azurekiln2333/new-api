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
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { STORAGE_KEYS } from '../../constants'
import type { Message } from '../../types'
import { usePlaygroundState } from '../use-playground-state'

function userMessage(content: string, key: string): Message {
  return { key, from: 'user', versions: [{ id: `${key}-v`, content }] }
}

async function renderPlaygroundState() {
  const view = renderHook(() => usePlaygroundState())
  await waitFor(() => {
    expect(view.result.current.isLoadingMessages).toBe(false)
  })

  return view
}

beforeEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.reset()
  useAuthStore.getState().auth.setUser({ id: 1, username: 'A', role: 1 })
})

afterEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.reset()
})

describe('usePlaygroundState conversations', () => {
  test('shows legacy history and a warning when migration storage is full', async () => {
    const messages = [userMessage('keep legacy history', 'legacy')]
    localStorage.setItem(
      `${STORAGE_KEYS.MESSAGES}:1`,
      JSON.stringify({ version: 1, data: messages })
    )
    const setItem = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new DOMException('quota exceeded', 'QuotaExceededError')
      })

    const view = await renderPlaygroundState()

    expect(view.result.current.messages).toEqual(messages)
    expect(view.result.current.storageFailure).toBe('quota')
    expect(localStorage.getItem(`${STORAGE_KEYS.MESSAGES}:1`)).not.toBeNull()
    setItem.mockRestore()
    view.unmount()
    expect(localStorage.getItem(`${STORAGE_KEYS.MESSAGES}:1`)).toBeNull()

    const reloaded = await renderPlaygroundState()
    expect(reloaded.result.current.messages).toEqual(messages)
    reloaded.unmount()
  })

  test('preserves unsaved messages across chat switches and retries them after storage recovers', async () => {
    const view = await renderPlaygroundState()
    const firstId = view.result.current.activeConversationId ?? ''
    act(() => {
      view.result.current.updateMessages([userMessage('first', 'first')])
    })
    let secondId = ''
    act(() => {
      secondId = view.result.current.createConversation()
    })
    act(() => {
      view.result.current.updateMessages([userMessage('second', 'second')])
    })
    act(() => {
      view.result.current.switchConversation(firstId)
    })

    const originalSetItem = Storage.prototype.setItem
    const setItem = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(function (this: Storage, key, value) {
        if (key.startsWith(`${STORAGE_KEYS.CONVERSATION_MESSAGES}:`)) {
          throw new DOMException('quota exceeded', 'QuotaExceededError')
        }
        originalSetItem.call(this, key, value)
      })
    const firstUpdate = [userMessage('unsaved first', 'first-new')]
    const secondUpdate = [userMessage('unsaved second', 'second-new')]
    act(() => {
      view.result.current.updateMessages(firstUpdate)
    })
    act(() => {
      view.result.current.switchConversation(secondId)
    })
    act(() => {
      view.result.current.updateMessages(secondUpdate)
    })
    act(() => {
      view.result.current.switchConversation(firstId)
    })

    expect(view.result.current.storageFailure).toBe('quota')
    expect(view.result.current.messages).toEqual(firstUpdate)
    act(() => {
      view.result.current.switchConversation(secondId)
    })
    expect(view.result.current.messages).toEqual(secondUpdate)

    setItem.mockRestore()
    act(() => {
      view.result.current.switchConversation(firstId)
    })
    expect(view.result.current.storageFailure).toBeNull()
    view.unmount()

    const reloaded = await renderPlaygroundState()
    expect(reloaded.result.current.messages).toEqual(firstUpdate)
    act(() => {
      reloaded.result.current.switchConversation(secondId)
    })
    expect(reloaded.result.current.messages).toEqual(secondUpdate)
    reloaded.unmount()
  })

  test('keeps each chat history separate and restores it after a reload', async () => {
    const view = await renderPlaygroundState()
    const firstId = view.result.current.activeConversationId

    act(() => {
      view.result.current.updateMessages([
        userMessage('first chat message', 'first'),
      ])
    })

    let secondId = ''
    act(() => {
      secondId = view.result.current.createConversation()
    })
    act(() => {
      view.result.current.updateMessages([
        userMessage('second chat message', 'second'),
      ])
    })

    expect(secondId).not.toBe(firstId)
    expect(view.result.current.messages[0].key).toBe('second')

    act(() => {
      view.result.current.switchConversation(firstId ?? '')
    })
    expect(view.result.current.messages[0].key).toBe('first')

    view.unmount()

    const reloaded = await renderPlaygroundState()
    expect(reloaded.result.current.conversations).toHaveLength(2)
    expect(reloaded.result.current.activeConversationId).toBe(firstId)
    expect(reloaded.result.current.messages[0].key).toBe('first')

    act(() => {
      reloaded.result.current.switchConversation(secondId)
    })
    expect(reloaded.result.current.messages[0].key).toBe('second')
  })

  test('deleting the active chat opens the remaining one', async () => {
    const view = await renderPlaygroundState()
    const firstId = view.result.current.activeConversationId ?? ''

    act(() => {
      view.result.current.updateMessages([userMessage('keep me', 'keep')])
    })
    let secondId = ''
    act(() => {
      secondId = view.result.current.createConversation()
    })

    act(() => {
      view.result.current.deleteConversation(secondId)
    })

    expect(view.result.current.conversations).toHaveLength(1)
    expect(view.result.current.activeConversationId).toBe(firstId)
    expect(view.result.current.messages[0].key).toBe('keep')
  })

  test('archiving the only open chat starts a new empty chat', async () => {
    const view = await renderPlaygroundState()
    const archivedId = view.result.current.activeConversationId ?? ''

    act(() => {
      view.result.current.updateMessages([userMessage('archived chat', 'a')])
    })
    act(() => {
      view.result.current.archiveConversation(archivedId, true)
    })

    const conversations = view.result.current.conversations
    expect(conversations).toHaveLength(2)
    expect(
      conversations.find((conversation) => conversation.id === archivedId)
        ?.archived
    ).toBe(true)
    expect(
      conversations.find((conversation) => conversation.id === archivedId)
        ?.title
    ).toBe('archived chat')
    expect(view.result.current.activeConversationId).not.toBe(archivedId)
    expect(view.result.current.messages).toEqual([])
    view.unmount()

    const reloaded = await renderPlaygroundState()
    expect(
      reloaded.result.current.conversations.find(
        (conversation) => conversation.id === archivedId
      )?.title
    ).toBe('archived chat')
    reloaded.unmount()
  })
})
