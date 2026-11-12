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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { MAX_CONVERSATION_TITLE_CHARS } from '../../lib/storage/storage-schema'

interface ConversationRenameDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialTitle: string
  onConfirm: (title: string) => void
}

export function ConversationRenameDialog(props: ConversationRenameDialogProps) {
  const { t } = useTranslation()
  const [title, setTitle] = useState(props.initialTitle)

  useEffect(() => {
    if (!props.open) return
    setTitle(props.initialTitle)
  }, [props.open, props.initialTitle])

  const handleConfirm = () => {
    if (!title.trim()) return
    props.onConfirm(title)
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Rename chat')}
      contentClassName='sm:max-w-md'
      bodyClassName='space-y-2'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={!title.trim()}>
            {t('Save')}
          </Button>
        </>
      }
    >
      <Label htmlFor='conversation-title'>{t('Chat title')}</Label>
      <Input
        id='conversation-title'
        maxLength={MAX_CONVERSATION_TITLE_CHARS}
        value={title}
        onChange={(event) => setTitle(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') handleConfirm()
        }}
      />
    </Dialog>
  )
}
