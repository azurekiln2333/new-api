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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import { createInvitations, updateInvitation, type Invitation } from '../api'

type InvitationFormValues = { name: string; count: number; expires: string }

export function InvitationEditor(props: {
  invitation?: Invitation
  onClose: () => void
  onSaved: (keys: string[]) => void
}) {
  const { t } = useTranslation()
  const validationMessage = t(
    'Use a name of 1–80 characters and generate 1–100 invitation codes'
  )
  const schema = z.object({
    name: z
      .string()
      .trim()
      .min(1, validationMessage)
      .max(80, validationMessage),
    count: z
      .number()
      .int(validationMessage)
      .min(1, validationMessage)
      .max(100, validationMessage),
    expires: z.string(),
  })
  const form = useForm<InvitationFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: props.invitation?.name ?? '',
      count: 1,
      expires: props.invitation?.expired_time
        ? dayjs.unix(props.invitation.expired_time).format('YYYY-MM-DDTHH:mm')
        : '',
    },
  })
  const save = useMutation({
    mutationFn: async (values: InvitationFormValues) => {
      const expires = values.expires ? dayjs(values.expires).unix() : 0
      if (
        values.expires &&
        (!Number.isFinite(expires) || expires <= Date.now() / 1000)
      ) {
        form.setError('expires', {
          message: t('Expiration time cannot be earlier than current time'),
        })
        return
      }
      if (props.invitation) {
        await updateInvitation({
          id: props.invitation.id,
          name: values.name,
          expired_time: expires,
        })
        props.onSaved([])
      } else {
        props.onSaved(
          await createInvitations({
            name: values.name,
            count: values.count,
            expired_time: expires,
          })
        )
      }
    },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) props.onClose()
      }}
      title={
        props.invitation
          ? t('Edit invitation code')
          : t('Generate invitation codes')
      }
      contentHeight='auto'
      footer={
        <Button
          disabled={save.isPending}
          onClick={form.handleSubmit((values) => save.mutate(values))}
        >
          {t('Save')}
        </Button>
      }
    >
      <Form {...form}>
        <form
          className='space-y-4'
          onSubmit={form.handleSubmit((values) => save.mutate(values))}
        >
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Name')}</FormLabel>
                <FormControl>
                  <Input {...field} maxLength={80} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          {!props.invitation && (
            <FormField
              control={form.control}
              name='count'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Count')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      min={1}
                      max={100}
                      onChange={(event) =>
                        field.onChange(Number(event.target.value))
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
          <FormField
            control={form.control}
            name='expires'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Expiration time')}</FormLabel>
                <FormControl>
                  <Input {...field} type='datetime-local' />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <p className='text-muted-foreground text-sm'>
            {t('Leave empty for no expiration')}
          </p>
        </form>
      </Form>
    </Dialog>
  )
}
