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
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { sideDrawerContentClassName } from '@/components/drawer-layout'
import { IconBadge } from '@/components/ui/icon-badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
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
import { formatPercent } from '@/lib/format'

import { PanelWrapper } from '../ui/panel-wrapper'
import { ApiInfoItemComponent } from './api-info-item'

/**
 * Filtered node detail for one API entry. It shows only the approved fields the
 * public summary already carries: bound domains, coarse state and resource
 * percentages. Unknown values are stated explicitly rather than shown as 0.
 */
function NodeDetailPanel(props: {
  item: ApiInfoItem
  instance?: InstanceResourceSummary
}) {
  const { t } = useTranslation()
  const instance = props.instance

  let stateLabel = t('Unknown')
  if (instance?.status === 'healthy') stateLabel = t('Healthy')
  else if (instance?.status === 'stale') stateLabel = t('Stale')
  else if (instance?.status === 'disabled') stateLabel = t('Disabled')

  return (
    <dl className='flex flex-col gap-3 text-sm'>
      <DetailRow label={t('API URL')} value={props.item.url} mono />
      <DetailRow label={t('Route')} value={props.item.route} mono />
      {instance ? (
        <>
          <DetailRow label={t('Status')} value={stateLabel} />
          <DetailRow
            label={t('CPU')}
            value={formatPercentValue(instance.resources.cpu_percent, t)}
          />
          <DetailRow
            label={t('Memory')}
            value={formatPercentValue(instance.resources.memory_percent, t)}
          />
          <DetailRow
            label={t('Storage')}
            value={formatPercentValue(instance.resources.storage_percent, t)}
          />
          <DetailRow
            label={t('Domains')}
            value={
              instance.domains.length > 0
                ? instance.domains.join('\n')
                : t('Unknown')
            }
            mono
          />
        </>
      ) : (
        <DetailRow label={t('Status')} value={t('Unknown')} />
      )}
    </dl>
  )
}

function formatPercentValue(value: number | null, t: (key: string) => string): string {
  return value === null ? t('Unknown') : formatPercent(value)
}

function DetailRow(props: { label: string; value: string; mono?: boolean }) {
  return (
    <div className='flex flex-col gap-0.5'>
      <dt className='text-muted-foreground/70 text-xs font-medium tracking-wide uppercase'>
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

  // Each configured API entry is mapped to its instance by the configured
  // public domain, which the summary already exposes. No stored node identity
  // is involved, and reordering entries cannot re-associate resources.
  const instanceByDomain = useMemo(() => {
    const map = new Map<string, InstanceResourceSummary>()
    for (const instance of instances) {
      for (const domain of instance.domains) {
        map.set(domain, instance)
      }
    }
    return map
  }, [instances])

  const detailItem = entries.find((entry) => entry.key === detailKey)?.item
  // Derived on every render, so the detail always reflects the newest sample.
  const detailInstance = detailItem
    ? instanceByDomain.get(detailItem.url)
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
      contentClassName='p-0'
    >
      <ScrollArea className='h-72'>
        <div className='divide-border/60 divide-y'>
          {entries.map(({ item, key }) => {
            const instance = instanceByDomain.get(item.url)
            return (
              <div key={key}>
                <ApiInfoItemComponent
                  item={item}
                  status={pingStatus[item.url] || getDefaultPingStatus()}
                  onTest={handleTest}
                  resources={instance?.resources ?? null}
                  instanceStatus={instance?.status ?? null}
                  onOpenDetail={() => setDetailKey(key)}
                />
              </div>
            )
          })}
        </div>
      </ScrollArea>

      <Sheet
        open={detailItem !== undefined}
        onOpenChange={(next) => {
          if (!next) setDetailKey(null)
        }}
      >
        <SheetContent
          side='right'
          className={sideDrawerContentClassName('sm:max-w-md')}
        >
          <SheetHeader>
            <SheetTitle className='font-mono text-sm'>
              {detailItem?.route}
            </SheetTitle>
            <SheetDescription>{detailItem?.description}</SheetDescription>
          </SheetHeader>
          <div className='flex-1 overflow-y-auto px-4 py-4'>
            {detailItem ? (
              <NodeDetailPanel item={detailItem} instance={detailInstance} />
            ) : null}
          </div>
        </SheetContent>
      </Sheet>
    </PanelWrapper>
  )
}
