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
  completeLegacyMessageMigration,
  createConversationMeta,
  createProjectMeta,
  deleteConversationMessages,
  getInitialParameterEnabled,
  getInitialPlaygroundConfig,
  loadConversationIndex,
  loadConversationMessages,
  nextActiveConversationId,
  normalizeConversationTitle,
  normalizeProjectName,
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
  PlaygroundProject,
  PlaygroundStorageFailure,
  ModelOption,
  GroupOption,
} from '../types'

const MESSAGE_SAVE_DEBOUNCE_MS = 500

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
  const [projects, setProjects] = useState<PlaygroundProject[]>([])
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
  const projectsRef = useRef<PlaygroundProject[]>([])
  const activeConversationIdRef = useRef<string | null>(null)
  const messagesRef = useRef<Message[]>([])
  const pendingSavesRef = useRef(new Map<string, Message[]>())
  const pendingMigrationRef = useRef(false)
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

  const applyProjects = useCallback((nextProjects: PlaygroundProject[]) => {
    projectsRef.current = nextProjects
    setProjects(nextProjects)
  }, [])

  const applyActiveConversation = useCallback((conversationId: string) => {
    activeConversationIdRef.current = conversationId
    setActiveConversationId(conversationId)
    const loadedMessages =
      pendingSavesRef.current.get(conversationId) ??
      loadConversationMessages(conversationId) ??
      []
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

    if (!hasLoadedRef.current || pendingSavesRef.current.size === 0) return

    let nextConversations = conversationsRef.current
    let messageFailure = false
    for (const [conversationId, pendingMessages] of pendingSavesRef.current) {
      // A deleted conversation may still have a debounced save in flight.
      if (!nextConversations.some((item) => item.id === conversationId)) {
        pendingSavesRef.current.delete(conversationId)
        continue
      }
      nextConversations = touchConversation(
        nextConversations,
        conversationId,
        pendingMessages
      )
      const result = saveConversationMessages(conversationId, pendingMessages)
      reportStorageFailure(result)
      if (result !== 'ok') messageFailure = true
    }
    if (nextConversations !== conversationsRef.current) {
      applyConversations(nextConversations)
    }

    // Keep all unsaved messages in memory and leave the durable index alone
    // until its conversations have actually been written.
    if (messageFailure) return
    const indexResult = saveConversationIndex({
      conversations: nextConversations,
      activeId: activeConversationIdRef.current,
      projects: projectsRef.current,
    })
    reportStorageFailure(indexResult)

    if (indexResult === 'ok') {
      pendingSavesRef.current.clear()
      if (pendingMigrationRef.current) {
        completeLegacyMessageMigration()
        pendingMigrationRef.current = false
      }
      storageFailureDismissedRef.current = false
      setStorageFailure((previous) => (previous === null ? previous : null))
    }
  }, [applyConversations, reportStorageFailure])

  const scheduleSave = useCallback(
    (conversationId: string, nextMessages: Message[]) => {
      pendingSavesRef.current.set(conversationId, nextMessages)
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
      projectsRef.current = index.projects ?? []
      setProjects(index.projects ?? [])
      const loadedMessages =
        index.pendingMessages ??
        (index.activeId ? (loadConversationMessages(index.activeId) ?? []) : [])
      if (index.pendingMessages && index.activeId) {
        pendingSavesRef.current.set(index.activeId, index.pendingMessages)
        pendingMigrationRef.current = true
      }
      if (index.saveFailure) reportStorageFailure(index.saveFailure)
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
  }, [reportStorageFailure])

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
      if (pendingSavesRef.current.size > 0) return
      reportStorageFailure(
        saveConversationIndex({
          conversations: nextConversations,
          activeId,
          projects: projectsRef.current,
        })
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
      pendingSavesRef.current.delete(conversationId)
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
        persistIndex(conversationsRef.current, nextActiveId)
        return
      }

      // Every chat is archived now, so continue with a fresh one.
      flushPendingSave()
      const conversation = createConversationMeta()
      const withNewConversation = [conversation, ...conversationsRef.current]
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

  // Projects group chats; deleting a project only ungroups its chats.
  const createProject = useCallback(
    (name: string) => {
      const normalizedName = normalizeProjectName(name)
      if (!normalizedName) return null

      const project = createProjectMeta(normalizedName)
      applyProjects([...projectsRef.current, project])
      persistIndex(conversationsRef.current, activeConversationIdRef.current)
      return project.id
    },
    [applyProjects, persistIndex]
  )

  const renameProject = useCallback(
    (projectId: string, name: string) => {
      const normalizedName = normalizeProjectName(name)
      if (!normalizedName) return

      let changed = false
      const nextProjects = projectsRef.current.map((project) => {
        if (project.id !== projectId || project.name === normalizedName) {
          return project
        }

        changed = true
        return { ...project, name: normalizedName, updatedAt: Date.now() }
      })
      if (!changed) return

      applyProjects(nextProjects)
      persistIndex(conversationsRef.current, activeConversationIdRef.current)
    },
    [applyProjects, persistIndex]
  )

  const deleteProject = useCallback(
    (projectId: string) => {
      if (!projectsRef.current.some((project) => project.id === projectId)) {
        return
      }

      applyProjects(
        projectsRef.current.filter((project) => project.id !== projectId)
      )
      const nextConversations = conversationsRef.current.map((conversation) => {
        if (conversation.projectId !== projectId) return conversation

        const ungrouped: PlaygroundConversation = { ...conversation }
        delete ungrouped.projectId
        return ungrouped
      })
      applyConversations(nextConversations)
      persistIndex(nextConversations, activeConversationIdRef.current)
    },
    [applyConversations, applyProjects, persistIndex]
  )

  const moveConversationToProject = useCallback(
    (conversationId: string, projectId: string | null) => {
      if (
        projectId &&
        !projectsRef.current.some((project) => project.id === projectId)
      ) {
        return
      }

      const nextConversations = conversationsRef.current.map((conversation) => {
        if (conversation.id !== conversationId) return conversation
        if ((conversation.projectId ?? null) === projectId) return conversation

        const moved: PlaygroundConversation = { ...conversation }
        if (projectId) {
          moved.projectId = projectId
        } else {
          delete moved.projectId
        }
        return moved
      })
      if (
        nextConversations.every(
          (item, index) => item === conversationsRef.current[index]
        )
      ) {
        return
      }

      applyConversations(nextConversations)
      persistIndex(nextConversations, activeConversationIdRef.current)
    },
    [applyConversations, persistIndex]
  )

  return {
    // State
    config,
    parameterEnabled,
    conversations,
    projects,
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
    createProject,
    renameProject,
    deleteProject,
    moveConversationToProject,
    dismissStorageFailure,
  }
}
