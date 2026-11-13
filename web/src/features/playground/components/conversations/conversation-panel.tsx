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
import { PlusIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'

import type { PlaygroundConversation, PlaygroundProject } from '../../types'
import { ConversationList } from './conversation-list'

interface ConversationPanelProps {
  conversations: PlaygroundConversation[]
  projects: PlaygroundProject[]
  activeConversationId: string | null
  onSelect: (conversationId: string) => void
  onCreate: () => void
  onRename: (conversationId: string, title: string) => void
  onDelete: (conversationId: string) => void
  onArchive: (conversationId: string, archived: boolean) => void
  onMoveToProject: (conversationId: string, projectId: string | null) => void
  onCreateProject: (name: string) => void
  onRenameProject: (projectId: string, name: string) => void
  onDeleteProject: (projectId: string) => void
}

export function ConversationPanel(props: ConversationPanelProps) {
  const { t } = useTranslation()

  return (
    <div className='flex min-h-0 flex-1 flex-col'>
      <div className='flex items-center px-3 py-2'>
        <h2 className='truncate text-sm font-medium'>{t('Chats')}</h2>
      </div>

      <div className='px-2 pb-2'>
        <Button
          className='w-full justify-start gap-1.5'
          size='sm'
          type='button'
          variant='outline'
          onClick={props.onCreate}
        >
          <PlusIcon className='size-4' />
          {t('New chat')}
        </Button>
      </div>

      <ScrollArea className='min-h-0 flex-1'>
        <div className='px-2 pb-3'>
          <ConversationList
            activeConversationId={props.activeConversationId}
            conversations={props.conversations}
            projects={props.projects}
            onArchive={props.onArchive}
            onCreateProject={props.onCreateProject}
            onDelete={props.onDelete}
            onDeleteProject={props.onDeleteProject}
            onMoveToProject={props.onMoveToProject}
            onRename={props.onRename}
            onRenameProject={props.onRenameProject}
            onSelect={props.onSelect}
          />
        </div>
      </ScrollArea>
    </div>
  )
}
