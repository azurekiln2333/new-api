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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { LoadingState } from '@/components/loading-state'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type LogStorageSummaryData = {
  tables: { name: string; database: string; bytes: number | null; estimated: boolean }[]
  capture_enabled: boolean
  capture_max_bytes: number
  capture_enabled_override: boolean
  capture_max_bytes_override: boolean
}

export function LogStorageSummary() {
  const { t, i18n } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const summary = useQuery({
    queryKey: ['log-storage-summary', userId],
    queryFn: async () => {
      const response = await api.get<{ success: boolean; data: LogStorageSummaryData }>('/api/option/log_storage')
      return requireServerSuccess(response.data).data
    },
    staleTime: 30000,
    retry: false,
    meta: { errorToast: false },
  })

  return (
    <div className='text-muted-foreground flex min-w-0 flex-col gap-2 text-sm' aria-live='polite'>
      {summary.isPending && <LoadingState inline message={t('Loading...')} />}
      {!summary.isPending && !summary.data && <p>{t('Storage metadata unavailable')}</p>}
      {summary.data && (
        <>
          <p>{t('Effective capture on this node: {{enabled}}, limit {{limit}}', { enabled: summary.data.capture_enabled ? t('Enabled') : t('Disabled'), limit: formatBytes(summary.data.capture_max_bytes, locale) })}</p>
          {(summary.data.capture_enabled_override || summary.data.capture_max_bytes_override) && <p>{t('Environment overrides are active on this node. Saved capture settings apply only where no override is configured.')}</p>}
          <dl className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1'>
            {summary.data.tables.map((table) => (
              <div key={table.name} className='contents'>
                <dt className='break-all'>{table.name} ({table.database})</dt>
                <dd>{table.bytes == null ? t('Unknown') : formatBytes(table.bytes, locale)}{table.estimated && table.bytes != null ? ` (${t('Estimated')})` : ''}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
      <p>{t('Storage sizes may be unavailable or estimated. PostgreSQL deletion makes space reusable; returning it to the OS requires a separate VACUUM FULL or pg_repack operation, never run automatically.')}</p>
    </div>
  )
}
