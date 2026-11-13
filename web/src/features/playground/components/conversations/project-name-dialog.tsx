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

import { MAX_PROJECT_NAME_CHARS } from '../../lib/storage/storage-schema'

interface ProjectNameDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Empty when creating a project, the current name when renaming. */
  initialName: string
  onConfirm: (name: string) => void
}

export function ProjectNameDialog(props: ProjectNameDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState(props.initialName)

  useEffect(() => {
    if (!props.open) return
    setName(props.initialName)
  }, [props.open, props.initialName])

  const isEditing = props.initialName.trim().length > 0

  const handleConfirm = () => {
    if (!name.trim()) return
    props.onConfirm(name)
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={isEditing ? t('Rename project') : t('New project')}
      contentClassName='sm:max-w-md'
      bodyClassName='space-y-2'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={!name.trim()}>
            {t('Save')}
          </Button>
        </>
      }
    >
      <Label htmlFor='playground-project-name'>{t('Project name')}</Label>
      <Input
        id='playground-project-name'
        maxLength={MAX_PROJECT_NAME_CHARS}
        value={name}
        onChange={(event) => setName(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') handleConfirm()
        }}
      />
    </Dialog>
  )
}
