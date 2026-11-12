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
import { afterEach, beforeEach, describe, expect, test } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

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
    expect(view.result.current.activeConversationId).not.toBe(archivedId)
    expect(view.result.current.messages).toEqual([])
  })
})
