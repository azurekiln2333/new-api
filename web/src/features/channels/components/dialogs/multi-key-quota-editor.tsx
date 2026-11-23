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
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'

import { parseLocalQuotaLimit } from '../../lib/quota-operation'
import type { KeyStatus } from '../../types'

type MultiKeyQuotaEditorProps = {
  keyStatus: KeyStatus
  isSaving: boolean
  onCancel: () => void
  onSave: (values: { enabled: boolean; limitQuota: number }) => void
}

function formatQuota(value: number, locale?: string) {
  return formatQuotaWithCurrency(value, {
    digitsLarge: 2,
    digitsSmall: 4,
    locale,
  })
}

/**
 * Edit one allocation's budget in place, keeping the key list visible. The
 * editor never shows a limit it cannot enforce: without a budget view the
 * actions are unavailable instead of suggesting free capacity.
 */
export function MultiKeyQuotaEditor(props: MultiKeyQuotaEditorProps) {
  const { t, i18n } = useTranslation()
  const quota = props.keyStatus.quota
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [enabled, setEnabled] = useState(quota?.enabled ?? false)
  const [limitText, setLimitText] = useState(String(quota?.limit_quota ?? 0))
  const draftLimit = enabled ? parseLocalQuotaLimit(limitText) : 0
  const changed =
    enabled !== (quota?.enabled ?? false) ||
    draftLimit !== (quota?.limit_quota ?? 0)
  const canSave =
    draftLimit != null &&
    changed &&
    !props.isSaving &&
    quota?.ledger_enabled === true

  if (!quota?.ledger_enabled) {
    return (
      <div className='space-y-2 rounded-md border p-3'>
        <p className='text-sm font-medium'>
          {t('Per-key budget is not available yet')}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t('Channel budget accounting is not enabled')}
        </p>
        <Button variant='outline' size='sm' onClick={props.onCancel}>
          {t('Close')}
        </Button>
      </div>
    )
  }

  return (
    <div className='space-y-2 rounded-md border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-sm font-medium'>
          {t('Key {{id}} budget', {
            id: props.keyStatus.id ?? `#${props.keyStatus.index + 1}`,
          })}
        </p>
        <div className='flex items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={props.isSaving}
            onClick={props.onCancel}
          >
            {t('Cancel')}
          </Button>
          <Button
            size='sm'
            disabled={!canSave}
            onClick={() => {
              if (draftLimit != null) {
                props.onSave({ enabled, limitQuota: draftLimit })
              }
            }}
          >
            {props.isSaving && <Loader2 className='mr-2 size-4 animate-spin' />}
            {t('Save')}
          </Button>
        </div>
      </div>

      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-1.5'>
          <div className='flex items-center justify-between gap-3'>
            <Label htmlFor='multi-key-quota-enabled' className='text-xs'>
              {t('Enforce a local limit')}
            </Label>
            <Switch
              id='multi-key-quota-enabled'
              checked={enabled}
              onCheckedChange={setEnabled}
              disabled={props.isSaving}
            />
          </div>
          <Input
            inputMode='numeric'
            value={enabled ? limitText : '0'}
            onChange={(event) => setLimitText(event.target.value)}
            disabled={props.isSaving || !enabled}
            aria-label={t('Local limit (site quota units)')}
            aria-invalid={draftLimit == null}
          />
          <p className='text-muted-foreground text-xs'>
            {draftLimit == null
              ? t('Enter a whole, non-negative number.')
              : t('Display value: {{amount}}', {
                  amount: formatQuota(draftLimit, locale),
                })}
          </p>
        </div>
        <dl className='grid grid-cols-2 gap-x-3 gap-y-1 text-xs'>
          <div>
            <dt className='text-muted-foreground'>{t('Used this period')}</dt>
            <dd className='font-mono tabular-nums'>
              {formatQuota(quota.period_used_quota, locale)}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Reserved')}</dt>
            <dd className='font-mono tabular-nums'>
              {formatQuota(quota.reserved_quota, locale)}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Remaining')}</dt>
            <dd className='font-mono tabular-nums'>
              {quota.remaining_quota != null && quota.enabled
                ? formatQuota(quota.remaining_quota, locale)
                : t('Unlimited')}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Lifetime used')}</dt>
            <dd className='font-mono tabular-nums'>
              {formatQuota(quota.lifetime_used_quota, locale)}
            </dd>
          </div>
        </dl>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Editing the limit keeps the used amount. Reset is a separate action.'
        )}
      </p>
    </div>
  )
}
