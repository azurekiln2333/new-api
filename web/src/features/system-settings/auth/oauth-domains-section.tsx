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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Textarea } from '@/components/ui/textarea'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  oauthDomainsSchema,
  type OAuthDomainsFormValues,
} from './lib/oauth-domains'

type OAuthDomainsSectionProps = {
  allowedOrigins: string
  serverAddress: string
}

export function OAuthDomainsSection(props: OAuthDomainsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = useForm<OAuthDomainsFormValues>({
    resolver: zodResolver(oauthDomainsSchema),
    defaultValues: { allowedOrigins: props.allowedOrigins },
  })
  useResetForm(form, { allowedOrigins: props.allowedOrigins })

  const onSubmit = async (values: OAuthDomainsFormValues) => {
    try {
      await updateOption.mutateAsync({
        key: 'oauth.allowed_origins',
        value: values.allowedOrigins.trim(),
      })
      form.reset({ allowedOrigins: values.allowedOrigins.trim() })
    } catch (error) {
      form.setError('allowedOrigins', {
        message: getServerErrorMessage(error, t('Failed to update setting')),
      })
    }
  }

  return (
    <SettingsSection title={t('OAuth login domains')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => form.reset()}
            isSaving={form.formState.isSubmitting}
            isSaveDisabled={!form.formState.isDirty}
            isResetDisabled={!form.formState.isDirty}
          />
          <p className='text-muted-foreground text-sm'>
            {t('The primary website is always allowed')}:{' '}
            <span className='break-all'>
              {props.serverAddress || t('Not configured')}
            </span>
          </p>
          <FormField
            control={form.control}
            name='allowedOrigins'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Allowed login websites')}</FormLabel>
                <FormControl>
                  <Textarea
                    {...field}
                    rows={5}
                    placeholder={
                      'https://api.example.com\nhttps://api.example.net'
                    }
                    disabled={form.formState.isSubmitting}
                    autoCapitalize='none'
                    autoCorrect='off'
                    spellCheck={false}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Enter one HTTPS website per line, including its port if needed. Do not include paths or wildcards. Leave empty to allow only the primary website.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <p className='text-muted-foreground text-sm'>
            {t(
              'Applies to custom OAuth, OIDC, and Discord. Register each website’s full callback URL with your SSO provider as well.'
            )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Changes apply immediately on this node. Other nodes sharing the database synchronize automatically.'
            )}
          </p>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
