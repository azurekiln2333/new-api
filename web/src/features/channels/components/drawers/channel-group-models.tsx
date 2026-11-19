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
import { useTranslation } from 'react-i18next'

import { GroupBadge } from '@/components/group-badge'

import { resolveGroupModelVisibility } from '../../lib/channel-group-models'
import type { Channel } from '../../types'

/**
 * Models each channel group reaches through this channel, mirroring the
 * abilities the server derives: a group without a `group_models` entry sees
 * every public model, and a restricted group shows only the models that
 * survive the channel's own model list.
 */
export function ChannelGroupModels(props: { channel: Channel }) {
  const { t } = useTranslation()
  const rows = resolveGroupModelVisibility({
    models: props.channel.models,
    group: props.channel.group,
    settings: props.channel.settings,
    channel_info: props.channel.channel_info,
  })

  return (
    <section className='space-y-3' aria-label={t('Groups')}>
      <h3 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
        {t('Groups')}
      </h3>
      {rows.length > 0 ? (
        <dl className='divide-border divide-y rounded-lg border'>
          {rows.map((row) => (
            <div
              key={row.group}
              data-group-model-row={row.group}
              className='grid gap-1.5 px-3 py-2.5 sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)] sm:items-start sm:gap-3'
            >
              <dt className='min-w-0'>
                <GroupBadge group={row.group} size='sm' copyable={false} />
              </dt>
              <dd className='min-w-0'>
                {row.models.length > 0 ? (
                  <ul className='flex min-w-0 flex-wrap gap-1'>
                    {row.models.map((model) => (
                      <li
                        key={model}
                        className='bg-muted/50 rounded-md px-1.5 py-0.5 font-mono text-xs break-all'
                      >
                        {model}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <span className='text-muted-foreground text-xs'>-</span>
                )}
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className='text-muted-foreground text-sm'>-</p>
      )}
    </section>
  )
}
