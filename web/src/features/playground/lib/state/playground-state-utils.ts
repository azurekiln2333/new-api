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
import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../../constants'
import type {
  Message,
  ParameterEnabled,
  PlaygroundConfig,
  PlaygroundConversation,
  PlaygroundProject,
} from '../../types'
import {
  loadConfig,
  loadMessages,
  loadParameterEnabled,
} from '../storage/storage'

export type MessageStateUpdater =
  | Message[]
  | ((previousMessages: Message[]) => Message[])

/**
 * One section of the chat list: a project with its open chats, or the chats
 * that have no project. Projects keep their stored order (oldest first).
 */
export type ConversationGroup = {
  /** Project id, or null for the ungrouped section. */
  projectId: string | null
  /** Project name, empty for the ungrouped section. */
  name: string
  conversations: PlaygroundConversation[]
}

export function groupConversationsByProject(
  conversations: PlaygroundConversation[],
  projects: PlaygroundProject[]
): ConversationGroup[] {
  const knownProjectIds = new Set(projects.map((project) => project.id))
  const byProject = new Map<string, PlaygroundConversation[]>()
  const ungrouped: PlaygroundConversation[] = []

  for (const conversation of conversations) {
    // A project deleted outside this session leaves chats in "Ungrouped".
    if (conversation.projectId && knownProjectIds.has(conversation.projectId)) {
      const group = byProject.get(conversation.projectId) ?? []
      group.push(conversation)
      byProject.set(conversation.projectId, group)
      continue
    }

    ungrouped.push(conversation)
  }

  const groups: ConversationGroup[] = []
  for (const project of [...projects].sort(
    (left, right) => left.createdAt - right.createdAt
  )) {
    const projectConversations = byProject.get(project.id)
    if (!projectConversations) continue
    groups.push({
      projectId: project.id,
      name: project.name,
      conversations: projectConversations,
    })
  }

  if (ungrouped.length > 0) {
    groups.push({ projectId: null, name: '', conversations: ungrouped })
  }

  return groups
}

export function getInitialPlaygroundConfig(): PlaygroundConfig {
  return { ...DEFAULT_CONFIG, ...loadConfig() }
}

export function getInitialParameterEnabled(): ParameterEnabled {
  return { ...DEFAULT_PARAMETER_ENABLED, ...loadParameterEnabled() }
}

export function getInitialMessages(): Message[] {
  return loadMessages() || []
}

export function applyMessageStateUpdate(
  previousMessages: Message[],
  updater: MessageStateUpdater
): Message[] {
  return typeof updater === 'function' ? updater(previousMessages) : updater
}
