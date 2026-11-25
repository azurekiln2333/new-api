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
import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

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
import {
  DEFAULT_UPDATE_SOURCE_REPOSITORY,
  isUpdateSourceRepository,
} from '@/features/system-update/releases'
import { SystemUpdateAction } from '@/features/system-update/system-update-action'
import { useStatus } from '@/hooks/use-status'
import { formatTimestamp } from '@/lib/format'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const updateSourceSchema = z.object({
  repository: z
    .string()
    .trim()
    .refine((value) => value === '' || isUpdateSourceRepository(value), {
      message: 'Enter the repository as owner/repo.',
    }),
})

type UpdateSourceFormValues = z.infer<typeof updateSourceSchema>

type UpdateCheckerSectionProps = {
  currentVersion?: string | null
  startTime?: number | null
  defaultValues?: {
    repository: string
  }
}

export function UpdateCheckerSection(props: UpdateCheckerSectionProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const updateOption = useUpdateOption()
  const uptime = props.startTime
    ? formatTimestamp(props.startTime)
    : t('Unknown')
  const version = status?.version || props.currentVersion || t('Unknown')
  const formDefaults = useMemo<UpdateSourceFormValues>(
    () => ({ repository: props.defaultValues?.repository ?? '' }),
    [props.defaultValues?.repository]
  )
  const form = useForm<UpdateSourceFormValues>({
    resolver: zodResolver(updateSourceSchema),
    defaultValues: formDefaults,
  })

  useEffect(() => {
    form.reset(formDefaults)
  }, [formDefaults, form])

  const onSubmit = async (values: UpdateSourceFormValues) => {
    const repository = values.repository.trim()
    if (repository === form.formState.defaultValues?.repository) return
    await updateOption.mutateAsync({
      key: 'system_update_setting.repository',
      value: repository,
    })
    form.reset({ repository })
  }

  return (
    <SettingsSection title={t('System maintenance')}>
      <div className='space-y-6'>
        <div className='grid gap-4 md:grid-cols-2'>
          <div className='rounded-lg border p-4'>
            <div className='text-muted-foreground text-sm'>
              {t('Current version')}
            </div>
            <div className='text-lg font-semibold break-all'>{version}</div>
          </div>
          <div className='rounded-lg border p-4'>
            <div className='text-muted-foreground text-sm'>
              {t('Uptime since')}
            </div>
            <div className='text-lg font-semibold'>{uptime}</div>
          </div>
        </div>
        <Form {...form}>
          <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
            <SettingsPageFormActions
              onSave={form.handleSubmit(onSubmit)}
              isSaving={updateOption.isPending}
              saveLabel='Save update source'
            />
            <FormField
              control={form.control}
              name='repository'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Update source repository')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      autoComplete='off'
                      placeholder={DEFAULT_UPDATE_SOURCE_REPOSITORY}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Enter the repository as owner/repo.')}{' '}
                    {t('Leave blank to follow the official release channel.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsForm>
        </Form>
        <SystemUpdateAction compact={false} />
      </div>
    </SettingsSection>
  )
}
