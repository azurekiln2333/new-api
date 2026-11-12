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
import { useTranslation } from 'react-i18next'

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'

type HeaderTemplateApplyDialogProps = {
  open: boolean
  /** Display name of the template the user picked. */
  templateName: string
  onOpenChange: (open: boolean) => void
  onReplace: () => void
  onMerge: () => void
}

/**
 * Asks how to apply a client header template when the override field already
 * has content. The shared ConfirmDialog only offers one confirm action, so
 * this dialog exists to expose both "Replace" and "Merge".
 */
export function HeaderTemplateApplyDialog(
  props: HeaderTemplateApplyDialogProps
) {
  const { t } = useTranslation()
  return (
    <AlertDialog open={props.open} onOpenChange={props.onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader className='text-start'>
          <AlertDialogTitle>
            {t('Apply client header template')}
          </AlertDialogTitle>
          <AlertDialogDescription render={<div />}>
            {t(
              'The request header override already has content. Replace it with the {{template}} template, or merge the template into the existing headers?',
              { template: props.templateName }
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
          <Button variant='outline' onClick={props.onMerge}>
            {t('Merge')}
          </Button>
          <Button variant='destructive' onClick={props.onReplace}>
            {t('Replace')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
