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
import { Route } from 'lucide-react'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ResourceUsage } from '@/components/resource-usage'
import { IconBadge } from '@/components/ui/icon-badge'
import { useInstanceResourceSummary } from '@/features/dashboard/hooks/use-instance-resources'
import { useApiInfo } from '@/features/dashboard/hooks/use-status-data'
import {
  testUrlLatency,
  getDefaultPingStatus,
} from '@/features/dashboard/lib/api-info'
import type {
  PingStatusMap,
  ApiInfoItem,
  InstanceResourceSummary,
} from '@/features/dashboard/types'

import { PanelWrapper } from '../ui/panel-wrapper'
import { ApiInfoItemComponent } from './api-info-item'

/** Only the approved public summary fields enter this API-specific detail. */
function NodeDetailPanel(props: {
  item: ApiInfoItem
  instance?: InstanceResourceSummary
}) {
  const { t } = useTranslation()
  const instance = props.instance

  let stateLabel = t('Unknown')
  if (instance?.status === 'healthy') stateLabel = t('Online')
  else if (instance?.status === 'stale') stateLabel = t('Offline')
  else if (instance?.status === 'disabled') stateLabel = t('Disabled')

  return (
    <dl className='flex flex-col gap-3 text-sm'>
      <DetailRow label={t('API URL')} value={props.item.url} mono />
      <DetailRow label={t('Route')} value={props.item.route} mono />
      <DetailRow label={t('Status')} value={stateLabel} />
      {instance && (
        <>
          <DetailRow
            label={t('CPU')}
            value={
              <ResourceUsage
                value={instance.resources.cpu_percent}
                unknownLabel={t('Unknown')}
              />
            }
          />
          <DetailRow
            label={t('Memory')}
            value={
              <ResourceUsage
                value={instance.resources.memory_percent}
                unknownLabel={t('Unknown')}
              />
            }
          />
          <DetailRow
            label={t('Storage')}
            value={
              <ResourceUsage
                value={instance.resources.storage_percent}
                unknownLabel={t('Unknown')}
              />
            }
          />
        </>
      )}
      <DetailRow
        label={t('Domains')}
        value={
          instance?.domains.length ? instance.domains.join('\n') : t('Unknown')
        }
        mono
      />
    </dl>
  )
}

function DetailRow(props: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <div className='flex flex-col gap-0.5'>
      <dt className='text-muted-foreground text-xs font-medium'>
        {props.label}
      </dt>
      <dd
        className={
          props.mono ? 'font-mono text-xs break-all whitespace-pre-line' : ''
        }
      >
        {props.value}
      </dd>
    </div>
  )
}

export function ApiInfoPanel() {
  const { t } = useTranslation()
  const { items: list, loading } = useApiInfo()
  const { instances } = useInstanceResourceSummary()
  const [pingStatus, setPingStatus] = useState<PingStatusMap>({})
  const [detailKey, setDetailKey] = useState<string | null>(null)
  // Saved entries have persistent ids. Legacy entries use their public content
  // so routes sharing a URL remain distinct and selection survives reordering.
  const entries = useMemo(() => {
    const occurrences = new Map<string, number>()
    return list.map((item) => {
      const identity =
        item.id > 0
          ? `api-${item.id}`
          : JSON.stringify([item.url, item.route, item.description, item.color])
      const occurrence = occurrences.get(identity) ?? 0
      occurrences.set(identity, occurrence + 1)
      return { item, key: `${identity}:${occurrence}` }
    })
  }, [list])

  // Map by configured API entry id, never by public domain, so an unbound
  // entry sharing a URL with a bound one never receives that node's resource
  // readings. Legacy entries without a persistent id have no entry id here
  // and stay unbound.
  const instanceByEntryId = useMemo(() => {
    const map = new Map<number, InstanceResourceSummary>()
    for (const instance of instances) {
      for (const entryId of instance.entry_ids ?? []) {
        if (entryId > 0) map.set(entryId, instance)
      }
    }
    return map
  }, [instances])

  const detailItem = entries.find((entry) => entry.key === detailKey)?.item
  // Derived on every render, so the detail always reflects the newest sample.
  const detailInstance =
    detailItem?.id != null && detailItem.id > 0
      ? instanceByEntryId.get(detailItem.id)
      : undefined

  const handleTest = useCallback(async (url: string) => {
    setPingStatus((prev) => ({
      ...prev,
      [url]: { latency: null, testing: true, error: false },
    }))

    const result = await testUrlLatency(url)
    setPingStatus((prev) => ({ ...prev, [url]: result }))
  }, [])

  return (
    <>
      <PanelWrapper
        title={
          <span className='flex items-center gap-2'>
            <IconBadge tone='info' size='sm'>
              <Route />
            </IconBadge>
            {t('API Info')}
          </span>
        }
        description={t('Configured routes, latency checks, and node resources')}
        loading={loading}
        empty={!list.length}
        emptyMessage={t('No API routes configured')}
        height='h-72'
        contentClassName='grid gap-3 sm:grid-cols-2'
      >
        {entries.map(({ item, key }) => {
          const instance = item.id > 0 ? instanceByEntryId.get(item.id) : undefined
          return (
            <ApiInfoItemComponent
              key={key}
              item={item}
              status={pingStatus[item.url] || getDefaultPingStatus()}
              onTest={handleTest}
              resources={instance?.resources ?? null}
              instanceStatus={instance?.status ?? null}
              onOpenDetail={() => setDetailKey(key)}
            />
          )
        })}
      </PanelWrapper>
      <Dialog
        open={detailItem !== undefined}
        onOpenChange={(next) => {
          if (!next) setDetailKey(null)
        }}
        title={detailItem?.route ?? t('API Info')}
        description={detailItem?.description}
      >
        {detailItem && (
          <NodeDetailPanel item={detailItem} instance={detailInstance} />
        )}
      </Dialog>
    </>
  )
}
