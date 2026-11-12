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
import { useCallback, useEffect, useRef, useState } from 'react'

import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../constants'
import {
  applyMessageStateUpdate,
  createConversationMeta,
  deleteConversationMessages,
  getInitialParameterEnabled,
  getInitialPlaygroundConfig,
  loadConversationIndex,
  loadConversationMessages,
  nextActiveConversationId,
  normalizeConversationTitle,
  patchConversation,
  saveConfig,
  saveConversationIndex,
  saveConversationMessages,
  saveParameterEnabled,
  touchConversation,
  type MessageStateUpdater,
  type StorageSaveResult,
} from '../lib'
import type {
  Message,
  PlaygroundConfig,
  ParameterEnabled,
  PlaygroundConversation,
  PlaygroundStorageFailure,
  ModelOption,
  GroupOption,
} from '../types'

const MESSAGE_SAVE_DEBOUNCE_MS = 500

type PendingConversationSave = {
  conversationId: string
  messages: Message[]
}

function toStorageFailure(result: StorageSaveResult): PlaygroundStorageFailure {
  return result === 'quota' || result === 'error' ? result : null
}

/**
 * Main state management hook for playground.
 *
 * Model and parameter settings stay global (one playground configuration),
 * while messages belong to the active conversation.
 */
export function usePlaygroundState() {
  // Load initial state from localStorage
  const [config, setConfig] = useState<PlaygroundConfig>(
    getInitialPlaygroundConfig
  )

  const [parameterEnabled, setParameterEnabled] = useState<ParameterEnabled>(
    getInitialParameterEnabled
  )

  const [conversations, setConversations] = useState<PlaygroundConversation[]>(
    []
  )
  const [activeConversationId, setActiveConversationId] = useState<
    string | null
  >(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [isLoadingMessages, setIsLoadingMessages] = useState(true)
  const [storageFailure, setStorageFailure] =
    useState<PlaygroundStorageFailure>(null)

  const [models, setModels] = useState<ModelOption[]>([])
  const [groups, setGroups] = useState<GroupOption[]>([])

  const conversationsRef = useRef<PlaygroundConversation[]>([])
  const activeConversationIdRef = useRef<string | null>(null)
  const messagesRef = useRef<Message[]>([])
  const pendingSaveRef = useRef<PendingConversationSave | null>(null)
  const saveTimerRef = useRef<number | null>(null)
  const hasLoadedRef = useRef(false)
  const storageFailureDismissedRef = useRef(false)

  useEffect(() => {
    messagesRef.current = messages
  }, [messages])

  const applyConversations = useCallback(
    (nextConversations: PlaygroundConversation[]) => {
      conversationsRef.current = nextConversations
      setConversations(nextConversations)
    },
    []
  )

  const applyActiveConversation = useCallback((conversationId: string) => {
    activeConversationIdRef.current = conversationId
    setActiveConversationId(conversationId)
    const loadedMessages = loadConversationMessages(conversationId) ?? []
    messagesRef.current = loadedMessages
    setMessages(loadedMessages)
  }, [])

  const reportStorageFailure = useCallback(
    (result: StorageSaveResult): PlaygroundStorageFailure => {
      const failure = toStorageFailure(result)
      if (!failure) return null
      if (!storageFailureDismissedRef.current) {
        setStorageFailure(failure)
      }
      return failure
    },
    []
  )

  const flushPendingSave = useCallback(() => {
    if (saveTimerRef.current !== null) {
      window.clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }

    const pending = pendingSaveRef.current
    if (!pending) return
    pendingSaveRef.current = null

    // A deleted conversation may still have a debounced save in flight.
    if (
      !hasLoadedRef.current ||
      !conversationsRef.current.some(
        (conversation) => conversation.id === pending.conversationId
      )
    ) {
      return
    }

    const nextConversations = touchConversation(
      conversationsRef.current,
      pending.conversationId,
      pending.messages
    )
    if (nextConversations !== conversationsRef.current) {
      applyConversations(nextConversations)
    }

    const messageFailure = reportStorageFailure(
      saveConversationMessages(pending.conversationId, pending.messages)
    )
    const indexFailure = reportStorageFailure(
      saveConversationIndex({
        conversations: nextConversations,
        activeId: activeConversationIdRef.current,
      })
    )

    if (!messageFailure && !indexFailure) {
      storageFailureDismissedRef.current = false
      setStorageFailure((previous) => (previous === null ? previous : null))
    }
  }, [applyConversations, reportStorageFailure])

  const scheduleSave = useCallback(
    (conversationId: string, nextMessages: Message[]) => {
      pendingSaveRef.current = {
        conversationId,
        messages: nextMessages,
      }
      if (!hasLoadedRef.current) return
      if (saveTimerRef.current !== null) return

      saveTimerRef.current = window.setTimeout(() => {
        saveTimerRef.current = null
        flushPendingSave()
      }, MESSAGE_SAVE_DEBOUNCE_MS)
    },
    [flushPendingSave]
  )

  useEffect(() => {
    let cancelled = false

    window.setTimeout(() => {
      const index = loadConversationIndex()
      if (cancelled) return

      conversationsRef.current = index.conversations
      setConversations(index.conversations)
      const loadedMessages = index.activeId
        ? (loadConversationMessages(index.activeId) ?? [])
        : []
      activeConversationIdRef.current = index.activeId
      setActiveConversationId(index.activeId)
      messagesRef.current = loadedMessages
      setMessages(loadedMessages)
      hasLoadedRef.current = true
      setIsLoadingMessages(false)
    }, 0)

    return () => {
      cancelled = true
    }
  }, [])

  useEffect(
    () => () => {
      flushPendingSave()
    },
    [flushPendingSave]
  )

  // Update config with automatic save
  const updateConfig = useCallback(
    <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) => {
      setConfig((prev) => {
        const updated = { ...prev, [key]: value }
        saveConfig(updated)
        return updated
      })
    },
    []
  )

  // Update parameter enabled with automatic save
  const updateParameterEnabled = useCallback(
    (key: keyof ParameterEnabled, value: boolean) => {
      setParameterEnabled((prev) => {
        const updated = { ...prev, [key]: value }
        saveParameterEnabled(updated)
        return updated
      })
    },
    []
  )

  // Update messages of the active conversation with automatic save
  const updateMessages = useCallback(
    (updater: MessageStateUpdater) => {
      const conversationId = activeConversationIdRef.current
      if (!conversationId) return

      setMessages((prev) => {
        const newMessages = applyMessageStateUpdate(prev, updater)
        if (newMessages !== prev) {
          scheduleSave(conversationId, newMessages)
        }
        return newMessages
      })
    },
    [scheduleSave]
  )

  // Clear all messages of the active conversation
  const clearMessages = useCallback(() => {
    updateMessages([])
  }, [updateMessages])

  // Reset config to defaults
  const resetConfig = useCallback(() => {
    setConfig(DEFAULT_CONFIG)
    setParameterEnabled(DEFAULT_PARAMETER_ENABLED)
    saveConfig(DEFAULT_CONFIG)
    saveParameterEnabled(DEFAULT_PARAMETER_ENABLED)
  }, [])

  const persistIndex = useCallback(
    (nextConversations: PlaygroundConversation[], activeId: string | null) => {
      reportStorageFailure(
        saveConversationIndex({ conversations: nextConversations, activeId })
      )
    },
    [reportStorageFailure]
  )

  // Start a new chat; reuse the current one when it is still empty
  const createConversation = useCallback(() => {
    const current = conversationsRef.current.find(
      (conversation) => conversation.id === activeConversationIdRef.current
    )
    if (current && messagesRef.current.length === 0) {
      return current.id
    }

    flushPendingSave()
    const conversation = createConversationMeta()
    const nextConversations = [conversation, ...conversationsRef.current]
    applyConversations(nextConversations)
    applyActiveConversation(conversation.id)
    persistIndex(nextConversations, conversation.id)

    return conversation.id
  }, [
    applyActiveConversation,
    applyConversations,
    flushPendingSave,
    persistIndex,
  ])

  const switchConversation = useCallback(
    (conversationId: string) => {
      if (conversationId === activeConversationIdRef.current) return
      if (
        !conversationsRef.current.some(
          (conversation) => conversation.id === conversationId
        )
      ) {
        return
      }

      flushPendingSave()
      applyActiveConversation(conversationId)
      persistIndex(conversationsRef.current, conversationId)
    },
    [applyActiveConversation, flushPendingSave, persistIndex]
  )

  const renameConversation = useCallback(
    (conversationId: string, title: string) => {
      const normalizedTitle = normalizeConversationTitle(title)
      if (!normalizedTitle) return

      const nextConversations = patchConversation(
        conversationsRef.current,
        conversationId,
        { title: normalizedTitle, titleCustom: true }
      )
      if (nextConversations === conversationsRef.current) return

      applyConversations(nextConversations)
      persistIndex(nextConversations, activeConversationIdRef.current)
    },
    [applyConversations, persistIndex]
  )

  const deleteConversation = useCallback(
    (conversationId: string) => {
      if (pendingSaveRef.current?.conversationId === conversationId) {
        pendingSaveRef.current = null
      }
      deleteConversationMessages(conversationId)

      const remaining = conversationsRef.current.filter(
        (conversation) => conversation.id !== conversationId
      )
      const wasActive = activeConversationIdRef.current === conversationId

      if (remaining.length === 0) {
        const conversation = createConversationMeta()
        applyConversations([conversation])
        applyActiveConversation(conversation.id)
        persistIndex([conversation], conversation.id)
        return
      }

      applyConversations(remaining)
      if (!wasActive) {
        persistIndex(remaining, activeConversationIdRef.current)
        return
      }

      const nextActiveId =
        nextActiveConversationId(remaining) ?? remaining[0].id
      applyActiveConversation(nextActiveId)
      persistIndex(remaining, nextActiveId)
    },
    [applyActiveConversation, applyConversations, persistIndex]
  )

  const archiveConversation = useCallback(
    (conversationId: string, archived: boolean) => {
      const nextConversations = patchConversation(
        conversationsRef.current,
        conversationId,
        { archived }
      )
      if (nextConversations === conversationsRef.current) return
      applyConversations(nextConversations)

      if (!archived || activeConversationIdRef.current !== conversationId) {
        persistIndex(nextConversations, activeConversationIdRef.current)
        return
      }

      const nextActiveId = nextActiveConversationId(nextConversations)
      if (nextActiveId) {
        flushPendingSave()
        applyActiveConversation(nextActiveId)
        persistIndex(nextConversations, nextActiveId)
        return
      }

      // Every chat is archived now, so continue with a fresh one.
      flushPendingSave()
      const conversation = createConversationMeta()
      const withNewConversation = [conversation, ...nextConversations]
      applyConversations(withNewConversation)
      applyActiveConversation(conversation.id)
      persistIndex(withNewConversation, conversation.id)
    },
    [
      applyActiveConversation,
      applyConversations,
      flushPendingSave,
      persistIndex,
    ]
  )

  const dismissStorageFailure = useCallback(() => {
    storageFailureDismissedRef.current = true
    setStorageFailure(null)
  }, [])

  return {
    // State
    config,
    parameterEnabled,
    conversations,
    activeConversationId,
    messages,
    isLoadingMessages,
    storageFailure,
    models,
    groups,

    // Setters
    setModels,
    setGroups,

    // Actions
    updateConfig,
    updateParameterEnabled,
    updateMessages,
    clearMessages,
    resetConfig,
    createConversation,
    switchConversation,
    renameConversation,
    deleteConversation,
    archiveConversation,
    dismissStorageFailure,
  }
}
