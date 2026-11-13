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
import {
  ChevronDownIcon,
  FolderIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

import {
  groupConversationsByProject,
  sortConversationsByRecency,
} from '../../lib'
import type { PlaygroundConversation, PlaygroundProject } from '../../types'
import { ConversationListItem } from './conversation-list-item'
import { ConversationRenameDialog } from './conversation-rename-dialog'
import { ProjectNameDialog } from './project-name-dialog'

interface ConversationListProps {
  conversations: PlaygroundConversation[]
  projects: PlaygroundProject[]
  activeConversationId: string | null
  onSelect: (conversationId: string) => void
  onRename: (conversationId: string, title: string) => void
  onDelete: (conversationId: string) => void
  onArchive: (conversationId: string, archived: boolean) => void
  onMoveToProject: (conversationId: string, projectId: string | null) => void
  onCreateProject: (name: string) => void
  onRenameProject: (projectId: string, name: string) => void
  onDeleteProject: (projectId: string) => void
}

export function ConversationList(props: ConversationListProps) {
  const { t } = useTranslation()
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [archivedOpen, setArchivedOpen] = useState(false)
  const [collapsedProjectIds, setCollapsedProjectIds] = useState<
    ReadonlySet<string>
  >(() => new Set())
  const [renamingProjectId, setRenamingProjectId] = useState<string | null>(
    null
  )
  const [deletingProjectId, setDeletingProjectId] = useState<string | null>(
    null
  )
  const [creatingProject, setCreatingProject] = useState(false)

  const openConversations = useMemo(
    () =>
      sortConversationsByRecency(
        props.conversations.filter((conversation) => !conversation.archived)
      ),
    [props.conversations]
  )
  const archivedConversations = useMemo(
    () =>
      sortConversationsByRecency(
        props.conversations.filter((conversation) => conversation.archived)
      ),
    [props.conversations]
  )
  const openGroups = useMemo(
    () => groupConversationsByProject(openConversations, props.projects),
    [openConversations, props.projects]
  )

  const renamingProject = props.projects.find(
    (project) => project.id === renamingProjectId
  )
  const deletingProject = props.projects.find(
    (project) => project.id === deletingProjectId
  )

  const renamingConversation = props.conversations.find(
    (conversation) => conversation.id === renamingId
  )
  const deletingConversation = props.conversations.find(
    (conversation) => conversation.id === deletingId
  )

  if (props.conversations.length === 0) {
    return (
      <p className='text-muted-foreground px-2 py-6 text-center text-xs'>
        {t('No chats yet')}
      </p>
    )
  }

  return (
    <div className='flex flex-col gap-0.5'>
      {openGroups.map((group) => {
        if (group.projectId === null) {
          return (
            <div className='flex flex-col gap-0.5' key='ungrouped'>
              {group.conversations.map((conversation) => (
                <ConversationListItem
                  key={conversation.id}
                  active={conversation.id === props.activeConversationId}
                  conversation={conversation}
                  projects={props.projects}
                  onDelete={() => setDeletingId(conversation.id)}
                  onMoveToProject={(projectId) =>
                    props.onMoveToProject(conversation.id, projectId)
                  }
                  onRename={() => setRenamingId(conversation.id)}
                  onSelect={() => props.onSelect(conversation.id)}
                  onToggleArchive={() => props.onArchive(conversation.id, true)}
                />
              ))}
            </div>
          )
        }

        const projectId = group.projectId
        const isOpen = !collapsedProjectIds.has(projectId)

        return (
          <Collapsible
            className='mt-1'
            key={projectId}
            open={isOpen}
            onOpenChange={(open) => {
              setCollapsedProjectIds((currentIds) => {
                const nextIds = new Set(currentIds)
                if (open) {
                  nextIds.delete(projectId)
                } else {
                  nextIds.add(projectId)
                }
                return nextIds
              })
            }}
          >
            <div className='group/project hover:bg-accent/40 flex items-center gap-0.5 rounded-md px-1'>
              <CollapsibleTrigger
                render={<button type='button' />}
                className='text-muted-foreground hover:text-foreground flex min-w-0 flex-1 cursor-pointer items-center gap-1 rounded-md px-1 py-1.5 text-xs'
              >
                <ChevronDownIcon
                  className={cn(
                    'size-3.5 shrink-0 transition-transform',
                    !isOpen && '-rotate-90'
                  )}
                />
                <FolderIcon className='size-3.5 shrink-0' />
                <span className='truncate font-medium'>{group.name}</span>
                <span className='ms-auto shrink-0 tabular-nums'>
                  {group.conversations.length}
                </span>
              </CollapsibleTrigger>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      className='size-7 shrink-0 opacity-0 group-hover/project:opacity-100 focus-visible:opacity-100 aria-expanded:opacity-100'
                      size='icon'
                      type='button'
                      variant='ghost'
                    />
                  }
                >
                  <MoreHorizontalIcon className='size-4' />
                  <span className='sr-only'>{t('More options')}</span>
                </DropdownMenuTrigger>
                <DropdownMenuContent align='end' className='w-40'>
                  <DropdownMenuItem
                    onClick={() => setRenamingProjectId(projectId)}
                  >
                    <PencilIcon />
                    {t('Rename')}
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    variant='destructive'
                    onClick={() => setDeletingProjectId(projectId)}
                  >
                    <Trash2Icon />
                    {t('Delete')}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <CollapsibleContent className='mt-0.5 flex flex-col gap-0.5 ps-3'>
              {group.conversations.map((conversation) => (
                <ConversationListItem
                  key={conversation.id}
                  active={conversation.id === props.activeConversationId}
                  conversation={conversation}
                  projects={props.projects}
                  onDelete={() => setDeletingId(conversation.id)}
                  onMoveToProject={(nextProjectId) =>
                    props.onMoveToProject(conversation.id, nextProjectId)
                  }
                  onRename={() => setRenamingId(conversation.id)}
                  onSelect={() => props.onSelect(conversation.id)}
                  onToggleArchive={() => props.onArchive(conversation.id, true)}
                />
              ))}
            </CollapsibleContent>
          </Collapsible>
        )
      })}

      <button
        className='text-muted-foreground hover:text-foreground mt-1 flex w-full cursor-pointer items-center gap-1 rounded-md px-2 py-1.5 text-xs'
        type='button'
        onClick={() => setCreatingProject(true)}
      >
        <PlusIcon className='size-3.5' />
        {t('New project')}
      </button>

      {archivedConversations.length > 0 ? (
        <Collapsible
          className='mt-2'
          open={archivedOpen}
          onOpenChange={setArchivedOpen}
        >
          <CollapsibleTrigger
            render={<button type='button' />}
            className='text-muted-foreground hover:text-foreground flex w-full cursor-pointer items-center gap-1 rounded-md px-2 py-1.5 text-xs'
          >
            <ChevronDownIcon
              className={cn(
                'size-3.5 transition-transform',
                !archivedOpen && '-rotate-90'
              )}
            />
            <span>{t('Archived')}</span>
            <span className='ms-auto tabular-nums'>
              {archivedConversations.length}
            </span>
          </CollapsibleTrigger>
          <CollapsibleContent className='mt-0.5 flex flex-col gap-0.5'>
            {archivedConversations.map((conversation) => (
              <ConversationListItem
                key={conversation.id}
                active={conversation.id === props.activeConversationId}
                conversation={conversation}
                projects={props.projects}
                onDelete={() => setDeletingId(conversation.id)}
                onMoveToProject={(projectId) =>
                  props.onMoveToProject(conversation.id, projectId)
                }
                onRename={() => setRenamingId(conversation.id)}
                onSelect={() => props.onSelect(conversation.id)}
                onToggleArchive={() => props.onArchive(conversation.id, false)}
              />
            ))}
          </CollapsibleContent>
        </Collapsible>
      ) : null}

      <ConversationRenameDialog
        initialTitle={renamingConversation?.title ?? ''}
        open={renamingConversation !== undefined}
        onConfirm={(title) => {
          if (!renamingConversation) return
          props.onRename(renamingConversation.id, title)
        }}
        onOpenChange={(open) => {
          if (!open) setRenamingId(null)
        }}
      />

      <ConfirmDialog
        destructive
        open={deletingConversation !== undefined}
        onOpenChange={(open) => {
          if (!open) setDeletingId(null)
        }}
        title={t('Delete chat?')}
        desc={t(
          'This chat will be permanently deleted. This cannot be undone.'
        )}
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (!deletingConversation) return
          props.onDelete(deletingConversation.id)
          setDeletingId(null)
        }}
      />

      <ProjectNameDialog
        initialName=''
        open={creatingProject}
        onConfirm={props.onCreateProject}
        onOpenChange={setCreatingProject}
      />

      <ProjectNameDialog
        initialName={renamingProject?.name ?? ''}
        open={renamingProject !== undefined}
        onConfirm={(name) => {
          if (!renamingProject) return
          props.onRenameProject(renamingProject.id, name)
        }}
        onOpenChange={(open) => {
          if (!open) setRenamingProjectId(null)
        }}
      />

      <ConfirmDialog
        destructive
        open={deletingProject !== undefined}
        onOpenChange={(open) => {
          if (!open) setDeletingProjectId(null)
        }}
        title={t('Delete project?')}
        desc={t(
          'Chats in this project will be moved to Ungrouped instead of being deleted.'
        )}
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (!deletingProject) return
          props.onDeleteProject(deletingProject.id)
          setDeletingProjectId(null)
        }}
      />
    </div>
  )
}
