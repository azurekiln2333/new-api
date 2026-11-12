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
  ArchiveIcon,
  ArchiveRestoreIcon,
  MoreHorizontalIcon,
  PencilIcon,
  Trash2Icon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampRelative } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { PlaygroundConversation } from '../../types'

interface ConversationListItemProps {
  conversation: PlaygroundConversation
  active: boolean
  onSelect: () => void
  onRename: () => void
  onToggleArchive: () => void
  onDelete: () => void
}

export function ConversationListItem(props: ConversationListItemProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const title = props.conversation.title || t('New chat')

  return (
    <div
      className={cn(
        'group/item flex items-center gap-1 rounded-md pe-1',
        props.active ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/60'
      )}
    >
      <button
        type='button'
        aria-current={props.active ? 'true' : undefined}
        className='min-w-0 flex-1 cursor-pointer py-2 ps-2 text-start'
        onClick={props.onSelect}
      >
        <span className='block truncate text-sm' title={title}>
          {title}
        </span>
        <span
          className='text-muted-foreground block truncate text-xs'
          title={t('Last updated')}
        >
          {formatTimestampRelative(
            props.conversation.updatedAt,
            'milliseconds',
            locale
          )}
        </span>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type='button'
              variant='ghost'
              size='icon'
              className='size-7 shrink-0 opacity-0 group-hover/item:opacity-100 focus-visible:opacity-100 aria-expanded:opacity-100'
            />
          }
        >
          <MoreHorizontalIcon className='size-4' />
          <span className='sr-only'>{t('More options')}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-40'>
          <DropdownMenuItem onClick={props.onRename}>
            <PencilIcon />
            {t('Rename')}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={props.onToggleArchive}>
            {props.conversation.archived ? (
              <ArchiveRestoreIcon />
            ) : (
              <ArchiveIcon />
            )}
            {props.conversation.archived ? t('Unarchive') : t('Archive')}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant='destructive' onClick={props.onDelete}>
            <Trash2Icon />
            {t('Delete')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
