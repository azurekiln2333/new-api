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
import { ChevronDown, ExternalLink, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { ScrollArea } from '@/components/ui/scroll-area'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  getOfficialServiceStatus,
  OFFICIAL_STATUS_PROVIDERS,
  type OfficialProviderStatus,
  type OfficialStatusIncident,
} from './api'

const FRESHNESS_LIMIT = 30 * 60_000

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
  postmortem: 'Postmortem',
}

const STATUS_TONES: Record<string, StatusVariant> = {
  operational: 'success',
  degraded_performance: 'warning',
  partial_outage: 'danger',
  major_outage: 'danger',
  under_maintenance: 'info',
}

const FETCH_ERROR_LABELS: Record<string, string> = {
  unverified_source: 'No verified public status feed is available.',
  invalid_timestamp: 'The last successful fetch time is invalid.',
  timeout: 'The official feed request timed out.',
  dns_error: 'The official feed hostname could not be resolved.',
  tls_error: 'The secure connection to the official feed failed.',
  transport_error:
    'The connection to the official feed could not be completed.',
  http_error: 'The official feed returned an HTTP error.',
  invalid_feed: 'The official feed response could not be validated.',
  invalid_source: 'The official feed address is invalid.',
  redirect_rejected: 'The official feed redirect was rejected.',
  read_error: 'The official feed response could not be read.',
  body_limit: 'The official feed response exceeded the size limit.',
}

function StatusTimestamp(props: { value: string | number | undefined }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const date = new Date(props.value || Number.NaN)
  if (
    !Number.isFinite(date.getTime()) ||
    (typeof props.value === 'number' && props.value <= 0) ||
    date.getTime() > Date.now()
  ) {
    return <span>{t('Unknown')}</span>
  }
  return (
    <time dateTime={date.toISOString()}>{date.toLocaleString(locale)}</time>
  )
}

function FeedFailure(props: { code?: string; httpStatus?: number }) {
  const { t, i18n } = useTranslation()
  if (!props.code) return null
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <p className='text-muted-foreground text-xs break-words'>
      {t(
        Object.hasOwn(FETCH_ERROR_LABELS, props.code)
          ? FETCH_ERROR_LABELS[props.code]
          : 'Official feed data is unavailable.'
      )}
      {props.httpStatus !== undefined &&
        props.httpStatus >= 100 &&
        props.httpStatus <= 599 && (
          <>
            {' '}
            ·{' '}
            {t('HTTP {{status}}', {
              status: formatNumber(props.httpStatus, locale),
            })}
          </>
        )}
    </p>
  )
}

function IncidentList(props: { incidents: OfficialStatusIncident[] }) {
  const { t } = useTranslation()
  return (
    <ul className='flex flex-col gap-2'>
      {props.incidents.map((incident, index) => (
        <li
          key={incident.id ? `id:${incident.id}` : `incident:${index}`}
          className='bg-muted/50 flex min-w-0 flex-col gap-1 rounded-md p-2'
        >
          <p className='text-sm break-words'>{incident.name}</p>
          <p className='text-muted-foreground text-xs break-words'>
            {t(
              Object.hasOwn(STATUS_LABELS, incident.status)
                ? STATUS_LABELS[incident.status]
                : 'Unknown'
            )}{' '}
            · <StatusTimestamp value={incident.updated_at} />
          </p>
        </li>
      ))}
    </ul>
  )
}

function ProviderStatusCard(props: {
  provider: OfficialProviderStatus
  now: number
  refreshFailed: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [expanded, setExpanded] = useState(false)
  const provider = props.provider
  const hasStatus =
    Number.isFinite(provider.fetched_at) &&
    provider.fetched_at > 0 &&
    provider.fetched_at <= props.now
  const stale = hasStatus && props.now - provider.fetched_at > FRESHNESS_LIMIT
  const lastKnown =
    hasStatus && (stale || provider.unavailable || props.refreshFailed)
  const historyFetchedAt = provider.history_fetched_at ?? 0
  const hasHistory =
    Number.isFinite(historyFetchedAt) &&
    historyFetchedAt > 0 &&
    historyFetchedAt <= props.now
  const historyStale =
    hasHistory && props.now - historyFetchedAt > FRESHNESS_LIMIT
  const components = hasStatus ? (provider.components ?? []) : []
  const active = hasStatus ? (provider.incidents ?? []) : []
  const activeIds = new Set(
    active.map((incident) => incident.id).filter(Boolean)
  )
  const recent = (hasHistory ? (provider.recent_incidents ?? []) : []).filter(
    (incident) => !incident.id || !activeIds.has(incident.id)
  )
  let freshnessLabel = t('Unknown')
  if (hasStatus) {
    freshnessLabel = t(stale ? 'Stale' : 'Fresh')
  }
  let historyFreshnessLabel = t('Unknown')
  if (hasHistory) {
    historyFreshnessLabel = t(historyStale ? 'Stale' : 'Fresh')
  }
  let headlineStatus = 'Unknown'
  let headlineTone: StatusVariant = 'neutral'
  if (hasStatus) {
    if (Object.hasOwn(STATUS_LABELS, provider.status)) {
      headlineStatus = STATUS_LABELS[provider.status]
    }
    if (!lastKnown && Object.hasOwn(STATUS_TONES, provider.status)) {
      headlineTone = STATUS_TONES[provider.status]
    }
  }

  return (
    <Card
      role='article'
      aria-labelledby={`${provider.id}-status-title`}
      className='h-[46rem] min-w-0 gap-3 overflow-hidden sm:h-[42rem]'
    >
      <CardHeader className='shrink-0'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <CardTitle className='min-w-0 break-words'>
            <h2 id={`${provider.id}-status-title`}>{provider.name}</h2>
          </CardTitle>
          <StatusBadge
            copyable={false}
            variant={headlineTone}
            label={t(headlineStatus)}
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
      <CardContent className='flex min-h-0 flex-1 flex-col gap-3 overflow-hidden'>
        <ScrollArea
          role='region'
          aria-label={t('Status data')}
          className='h-44 shrink-0 sm:h-40'
        >
          <div className='flex flex-col gap-2 pr-3'>
            <div className='flex flex-wrap items-center gap-2'>
              <StatusBadge
                copyable={false}
                variant={stale ? 'warning' : 'neutral'}
                label={freshnessLabel}
              />
              {provider.unavailable && (
                <StatusBadge
                  copyable={false}
                  variant='neutral'
                  label={t('Unavailable')}
                />
              )}
              {lastKnown && (
                <span className='text-muted-foreground text-xs'>
                  {t('Last known status')}
                </span>
              )}
            </div>
            {!hasStatus && (
              <p className='text-muted-foreground text-xs'>
                {t('No successful fetch yet.')}
              </p>
            )}
            <FeedFailure
              code={provider.fetch_error}
              httpStatus={provider.fetch_http_status}
            />
            <dl className='text-muted-foreground grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs'>
              <dt>{t('Last successful fetch')}</dt>
              <dd className='break-words'>
                <StatusTimestamp value={provider.fetched_at} />
              </dd>
              <dt>{t('Last checked')}</dt>
              <dd className='break-words'>
                <StatusTimestamp value={provider.checked_at} />
              </dd>
              <dt>{t('Source updated')}</dt>
              <dd className='break-words'>
                <StatusTimestamp value={provider.source_updated_at} />
              </dd>
            </dl>
          </div>
        </ScrollArea>
        <Collapsible
          open={expanded}
          onOpenChange={setExpanded}
          className='shrink-0'
        >
          <CollapsibleTrigger
            disabled={components.length === 0}
            render={
              <Button
                variant='outline'
                size='sm'
                className='w-full justify-between'
              />
            }
          >
            {t('Components')} ({formatNumber(components.length, locale)})
            <ChevronDown
              aria-hidden='true'
              className={cn(
                'size-4 transition-transform',
                expanded && 'rotate-180'
              )}
            />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ScrollArea
              role='region'
              aria-label={t('Components')}
              className='h-40'
            >
              <ul className='divide-border divide-y pr-3'>
                {components.map((component, index) => (
                  <li
                    key={
                      component.id ? `id:${component.id}` : `component:${index}`
                    }
                    className='flex min-w-0 flex-wrap items-center justify-between gap-2 py-2 text-sm'
                  >
                    <span className='min-w-0 break-words'>
                      {component.name}
                    </span>
                    <span className='text-muted-foreground text-xs'>
                      {t(
                        Object.hasOwn(STATUS_LABELS, component.status)
                          ? STATUS_LABELS[component.status]
                          : 'Unknown'
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            </ScrollArea>
          </CollapsibleContent>
        </Collapsible>
        <section
          role='region'
          aria-label={t('Incidents')}
          className='flex min-h-0 flex-1 flex-col gap-2 overflow-hidden'
        >
          <div className='flex shrink-0 flex-wrap items-center justify-between gap-2'>
            <h3 className='text-sm font-medium'>{t('Incidents')}</h3>
            <div className='flex flex-wrap gap-2'>
              <StatusBadge
                copyable={false}
                variant={historyStale ? 'warning' : 'neutral'}
                label={historyFreshnessLabel}
              />
              {provider.history_unavailable && (
                <StatusBadge
                  copyable={false}
                  variant='neutral'
                  label={t('Unavailable')}
                />
              )}
            </div>
          </div>
          <ScrollArea className='min-h-0 flex-1'>
            <div className='flex flex-col gap-2 pr-3'>
              <h4 className='text-muted-foreground text-xs font-medium'>
                {t('Active incidents')}
              </h4>
              {hasStatus && active.length > 0 && (
                <IncidentList incidents={active} />
              )}
              {hasStatus && active.length === 0 && (
                <p className='text-muted-foreground text-xs'>
                  {t('No active incidents reported.')}
                </p>
              )}
              {!hasStatus && (
                <p className='text-muted-foreground text-xs'>
                  {t('Active incident data is unavailable.')}
                </p>
              )}
              <h4 className='text-muted-foreground text-xs font-medium'>
                {t('Recent incidents')}
              </h4>
              {(!hasHistory || provider.history_unavailable) && (
                <p className='text-muted-foreground text-xs'>
                  {t('Recent incident data is unavailable.')}
                </p>
              )}
              <FeedFailure
                code={provider.history_error}
                httpStatus={provider.history_http_status}
              />
              {hasHistory && recent.length > 0 && (
                <IncidentList incidents={recent} />
              )}
              {hasHistory &&
                recent.length === 0 &&
                !provider.history_unavailable && (
                  <p className='text-muted-foreground text-xs'>
                    {t('No additional recent incidents reported.')}
                  </p>
                )}
              <dl className='text-muted-foreground grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs'>
                <dt>{t('History last successful fetch')}</dt>
                <dd className='break-words'>
                  <StatusTimestamp value={provider.history_fetched_at} />
                </dd>
                <dt>{t('History last checked')}</dt>
                <dd className='break-words'>
                  <StatusTimestamp value={provider.history_checked_at} />
                </dd>
              </dl>
            </div>
          </ScrollArea>
        </section>
      </CardContent>
    </Card>
  )
}

export function OfficialServiceStatus() {
  const { t } = useTranslation()
  const [now, setNow] = useState(Date.now)
  const query = useQuery({
    queryKey: ['official-service-status'],
    queryFn: ({ signal }) => getOfficialServiceStatus(signal),
    staleTime: 300_000,
    refetchInterval: 300_000,
    refetchIntervalInBackground: false,
    retry: false,
    meta: { errorToast: false },
  })
  useEffect(() => {
    const update = () => setNow(Date.now())
    const timer = window.setInterval(update, 30_000)
    window.addEventListener('focus', update)
    document.addEventListener('visibilitychange', update)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('focus', update)
      document.removeEventListener('visibilitychange', update)
    }
  }, [])

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
        <div className='flex flex-col gap-4'>
          <Alert>
            <AlertDescription>
              {t(
                'Data comes from official vendor status pages and may be delayed. This page does not indicate the availability of this site.'
              )}
            </AlertDescription>
          </Alert>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.'
            )}
          </p>
          {query.isPending && <LoadingState />}
          {query.isError && (
            <ErrorState description={getServerErrorMessage(query.error)} />
          )}
          {query.isSuccess && query.data.length === 0 && <EmptyState />}
          <div className='grid min-w-0 grid-cols-1 items-start gap-4 lg:grid-cols-2'>
            {OFFICIAL_STATUS_PROVIDERS.map((source) => {
              const provider: OfficialProviderStatus = {
                status: 'unknown',
                source_updated_at: '',
                fetched_at: 0,
                checked_at: 0,
                stale: false,
                unavailable: true,
                components: null,
                incidents: null,
                history_fetched_at: 0,
                history_checked_at: 0,
                history_unavailable: true,
                ...query.data?.find((entry) => entry.id === source.id),
                ...source,
              }
              return (
                <ProviderStatusCard
                  key={source.id}
                  provider={provider}
                  now={Math.max(now, query.dataUpdatedAt)}
                  refreshFailed={query.isError}
                />
              )
            })}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
