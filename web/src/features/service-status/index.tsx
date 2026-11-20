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
import { ExternalLink, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { getOfficialServiceStatus } from './api'

const STATUS_LABELS: Record<string, string> = {
  active: 'Active',
  operational: 'Operational',
  degraded_performance: 'Degraded performance',
  partial_outage: 'Partial outage',
  major_outage: 'Major outage',
  under_maintenance: 'Under maintenance',
  investigating: 'Investigating',
  identified: 'Identified',
  monitoring: 'Monitoring',
  resolved: 'Resolved',
}

const STATUS_TONES: Record<string, StatusVariant> = {
  operational: 'success',
  degraded_performance: 'warning',
  partial_outage: 'danger',
  major_outage: 'danger',
  under_maintenance: 'info',
}

export function OfficialServiceStatus() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['official-service-status'],
    queryFn: ({ signal }) => getOfficialServiceStatus(signal),
    staleTime: 300_000,
    refetchInterval: 300_000,
    refetchIntervalInBackground: false,
    retry: false,
    meta: { errorToast: false },
  })
  const timestamp = (value: string | number) => {
    if (!value) return t('Unknown')
    const date = new Date(value)
    if (!Number.isFinite(date.getTime())) return t('Unknown')
    return date.toLocaleString(locale)
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Official status')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw aria-hidden='true' />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <Alert>
            <AlertDescription>
              {t(
                'Data comes from official vendor status pages and may be delayed. This page does not indicate the availability of this site.'
              )}
            </AlertDescription>
          </Alert>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Official feeds are cached for five minutes. Google status covers the Vertex Gemini API, not AI Studio.'
            )}
          </p>
          {query.isPending && <LoadingState />}
          {query.isError && (
            <ErrorState description={getServerErrorMessage(query.error)} />
          )}
          {query.isSuccess && query.data.length === 0 && <EmptyState />}
          <div className='grid grid-cols-1 gap-4 lg:grid-cols-2'>
            {query.data?.map((provider) => (
              <Card key={provider.id} className='min-w-0'>
                <CardHeader>
                  <div className='flex flex-wrap items-center justify-between gap-2'>
                    <CardTitle className='min-w-0 break-words'>
                      {provider.name}
                    </CardTitle>
                    <StatusBadge
                      copyable={false}
                      variant={
                        provider.unavailable
                          ? 'neutral'
                          : (STATUS_TONES[provider.status] ?? 'neutral')
                      }
                      label={t(
                        provider.unavailable
                          ? 'Unavailable'
                          : (STATUS_LABELS[provider.status] ?? 'Unknown')
                      )}
                    />
                  </div>
                  <a
                    href={provider.source_url}
                    target='_blank'
                    rel='noopener noreferrer'
                    className='text-muted-foreground inline-flex w-fit items-center gap-1 text-sm underline underline-offset-4'
                  >
                    {t('Official source')}
                    <ExternalLink className='size-3' aria-hidden='true' />
                  </a>
                </CardHeader>
                <CardContent className='space-y-4'>
                  {provider.stale && (
                    <p role='status' className='text-warning text-sm'>
                      {t(
                        'Showing the last successful update; the official feed is currently unavailable.'
                      )}
                    </p>
                  )}
                  <dl className='text-muted-foreground grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs'>
                    <dt>{t('Last successful fetch')}</dt>
                    <dd className='break-words'>
                      {timestamp(provider.fetched_at)}
                    </dd>
                    <dt>{t('Last checked')}</dt>
                    <dd className='break-words'>
                      {timestamp(provider.checked_at)}
                    </dd>
                    <dt>{t('Source updated')}</dt>
                    <dd className='break-words'>
                      {timestamp(provider.source_updated_at)}
                    </dd>
                  </dl>
                  {!!provider.components?.length && (
                    <ul className='divide-border divide-y'>
                      {provider.components.map((component, index) => (
                        <li
                          key={
                            component.id
                              ? `id:${component.id}`
                              : `component:${index}`
                          }
                          className='flex flex-wrap items-center justify-between gap-2 py-2 text-sm'
                        >
                          <span className='min-w-0 break-words'>
                            {component.name}
                          </span>
                          <span className='text-muted-foreground text-xs'>
                            {t(STATUS_LABELS[component.status] ?? 'Unknown')}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                  {!!provider.incidents?.length && (
                    <section aria-label={t('Incidents')} className='space-y-2'>
                      <h3 className='text-sm font-medium'>{t('Incidents')}</h3>
                      {provider.incidents.map((incident, index) => (
                        <div
                          key={
                            incident.id
                              ? `id:${incident.id}`
                              : `incident:${index}`
                          }
                          className='bg-muted/50 space-y-1 rounded-md p-3'
                        >
                          <p className='text-sm break-words'>{incident.name}</p>
                          <p className='text-muted-foreground text-xs'>
                            {t(STATUS_LABELS[incident.status] ?? 'Unknown')} ·{' '}
                            {timestamp(incident.updated_at)}
                          </p>
                        </div>
                      ))}
                    </section>
                  )}
                </CardContent>
              </Card>
            ))}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
