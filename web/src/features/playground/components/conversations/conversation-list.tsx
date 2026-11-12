import { ChevronDownIcon } from 'lucide-react'
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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { cn } from '@/lib/utils'

import { sortConversationsByRecency } from '../../lib'
import type { PlaygroundConversation } from '../../types'
import { ConversationListItem } from './conversation-list-item'
import { ConversationRenameDialog } from './conversation-rename-dialog'

interface ConversationListProps {
  conversations: PlaygroundConversation[]
  activeConversationId: string | null
  onSelect: (conversationId: string) => void
  onRename: (conversationId: string, title: string) => void
  onDelete: (conversationId: string) => void
  onArchive: (conversationId: string, archived: boolean) => void
}

export function ConversationList(props: ConversationListProps) {
  const { t } = useTranslation()
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [archivedOpen, setArchivedOpen] = useState(false)

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
      {openConversations.map((conversation) => (
        <ConversationListItem
          key={conversation.id}
          active={conversation.id === props.activeConversationId}
          conversation={conversation}
          onDelete={() => setDeletingId(conversation.id)}
          onRename={() => setRenamingId(conversation.id)}
          onSelect={() => props.onSelect(conversation.id)}
          onToggleArchive={() => props.onArchive(conversation.id, true)}
        />
      ))}

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
                onDelete={() => setDeletingId(conversation.id)}
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
    </div>
  )
}
