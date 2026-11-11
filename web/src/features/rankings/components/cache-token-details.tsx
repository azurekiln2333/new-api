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

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatTokens } from '../lib/format'
import type { RankingTokenTotals } from '../types'

/**
 * Cache read / write token breakdown shown under a token total.
 *
 * Both quantities use the same compact token format as the total (see
 * `formatTokens`) so a row reads `8.24B … Cache Read 3.02B` rather than
 * mixing unit styles. Each label sits next to its value in a non-wrapping
 * pair; the two pairs share a single row and wrap together only when the
 * container is too narrow. The block renders nothing when both quantities
 * are zero. The exact value is available in the element title.
 */
export function CacheTokenDetails(
  props: Pick<RankingTokenTotals, 'cache_read_tokens' | 'cache_creation_tokens'>
) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  if (props.cache_read_tokens <= 0 && props.cache_creation_tokens <= 0) {
    return null
  }

  const entries = [
    { label: t('Cache Read'), value: props.cache_read_tokens },
    { label: t('Cache Write'), value: props.cache_creation_tokens },
  ]

  return (
    <dl className='text-muted-foreground/80 mt-1 flex flex-wrap items-baseline justify-end gap-x-3 gap-y-0.5 text-[11px] leading-tight'>
      {entries.map((entry) => (
        <div
          key={entry.label}
          className='flex items-baseline gap-1 whitespace-nowrap'
        >
          <dt>{entry.label}</dt>
          <dd
            className='text-foreground/80 font-mono tabular-nums'
            title={formatNumber(entry.value, locale)}
          >
            {formatTokens(entry.value)}
          </dd>
        </div>
      ))}
    </dl>
  )
}
