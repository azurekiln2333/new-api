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
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const rateLimitDialogSchema = z.object({
  groupName: z.string().min(1, 'Group name is required'),
  maxRequests: z
    .number()
    .min(0, 'Must be ≥ 0')
    .max(2147483647, 'Must be ≤ 2,147,483,647'),
  maxSuccess: z
    .number()
    .min(1, 'Must be ≥ 1')
    .max(2147483647, 'Must be ≤ 2,147,483,647'),
})

type RateLimitDialogFormValues = z.infer<typeof rateLimitDialogSchema>

const RATE_LIMIT_FORM_ID = 'rate-limit-form'

export type RateLimitEntryData = {
  groupName: string
  maxRequests: number
  maxSuccess: number
}

export type RateLimitDialogResult = RateLimitEntryData & {
  /**
   * When editing an existing group limit, also remove the manual limits that
   * users of this group were given, so they follow the group again.
   */
  overwriteUserOverrides: boolean
}

type RateLimitDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (data: RateLimitDialogResult) => Promise<void> | void
  editData?: RateLimitEntryData | null
  /** Groups defined in the group settings; the only choices when adding an entry. */
  groups: readonly string[]
  /** Kept entries may name a group that no longer exists; the dialog says so. */
  isGroupMissing?: boolean
}

export function RateLimitDialog(props: RateLimitDialogProps) {
  const { t } = useTranslation()
  const isEditMode = !!props.editData
  const [overwriteUserOverrides, setOverwriteUserOverrides] = useState(false)

  const form = useForm<RateLimitDialogFormValues>({
    resolver: zodResolver(rateLimitDialogSchema),
    defaultValues: {
      groupName: '',
      maxRequests: 0,
      maxSuccess: 1,
    },
  })

  useEffect(() => {
    if (props.editData) {
      form.reset(props.editData)
    } else {
      form.reset({
        groupName: '',
        maxRequests: 0,
        maxSuccess: 1,
      })
    }
    setOverwriteUserOverrides(false)
  }, [props.editData, form, props.open])

  const handleSubmit = async (values: RateLimitDialogFormValues) => {
    await props.onSave({ ...values, overwriteUserOverrides })
    form.reset()
    props.onOpenChange(false)
  }

  const groupOptions = props.groups.map((group) => ({
    value: group,
    label: group,
  }))

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        isEditMode ? t('Edit group rate limit') : t('Add group rate limit')
      }
      description={t(
        'Configure rate limiting rules for a specific user group.'
      )}
      contentClassName='sm:max-w-[500px]'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={RATE_LIMIT_FORM_ID}>
            {isEditMode ? t('Update') : t('Add')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={RATE_LIMIT_FORM_ID}
          onSubmit={form.handleSubmit(handleSubmit)}
          className='space-y-4'
        >
          <FormField
            control={form.control}
            name='groupName'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Group Name')}</FormLabel>
                <FormControl>
                  {isEditMode ? (
                    <Input {...field} disabled />
                  ) : (
                    <Combobox
                      options={groupOptions}
                      value={field.value}
                      onValueChange={(value) => field.onChange(value ?? '')}
                      placeholder={t('Select a group')}
                      searchPlaceholder={t('Search groups...')}
                      emptyText={t('No groups found')}
                      className='w-full'
                      aria-invalid={Boolean(form.formState.errors.groupName)}
                    />
                  )}
                </FormControl>
                <FormDescription>
                  {isEditMode
                    ? t('Group name cannot be changed when editing.')
                    : t(
                        'Choose one of the groups defined in the group settings.'
                      )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {props.isGroupMissing && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'This group no longer exists in the group settings. The entry is kept until you delete it.'
              )}
            </p>
          )}

          <FormField
            control={form.control}
            name='maxRequests'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max Requests (including failures)')}</FormLabel>
                <FormControl>
                  <div className='flex items-center gap-2'>
                    <Input
                      type='number'
                      min={0}
                      max={2147483647}
                      step={1}
                      {...field}
                      onChange={(e) =>
                        field.onChange(Number.parseInt(e.target.value) || 0)
                      }
                    />
                    <span className='text-muted-foreground text-sm'>
                      {t('times')}
                    </span>
                  </div>
                </FormControl>
                <FormDescription>
                  {t('Total requests allowed per period. 0 = unlimited.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='maxSuccess'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max Successful Requests')}</FormLabel>
                <FormControl>
                  <div className='flex items-center gap-2'>
                    <Input
                      type='number'
                      min={1}
                      max={2147483647}
                      step={1}
                      {...field}
                      onChange={(e) =>
                        field.onChange(Number.parseInt(e.target.value) || 1)
                      }
                    />
                    <span className='text-muted-foreground text-sm'>
                      {t('times')}
                    </span>
                  </div>
                </FormControl>
                <FormDescription>
                  {t('Only successful requests count toward this limit.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {isEditMode && (
            <div className='flex items-start gap-2'>
              <Checkbox
                id='overwrite-user-overrides'
                checked={overwriteUserOverrides}
                onCheckedChange={(checked) =>
                  setOverwriteUserOverrides(checked === true)
                }
              />
              <div className='space-y-1'>
                <Label
                  htmlFor='overwrite-user-overrides'
                  className='font-normal'
                >
                  {t('Also clear the manual limits of users in this group')}
                </Label>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Users in this group who were given their own rate limit fall back to the group limit.'
                  )}
                </p>
              </div>
            </div>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
