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
import { FileText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatBytes } from '@/lib/format'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { getLogRequestContent } from '../../api'
import {
  canLoadRequestContent,
  getRequestContentFields,
  getRequestContentMessages,
  getRequestContentStatusLabel,
} from '../../lib/request-content'
import type { RequestContentMetadata } from '../../types'
import { DetailSection } from './log-detail-layout'

type RequestContentSectionProps = {
  metadata?: RequestContentMetadata
}

/**
 * The original prompt/messages of one request. The text is fetched on demand
 * for the open dialog only and never kept in the log list record, so a closed
 * or unauthorized view discloses nothing.
 */
export function RequestContentSection(props: RequestContentSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const metadata = props.metadata
  const retrievable = canLoadRequestContent(metadata)
  const request = useQuery({
    queryKey: ['log-request-content', metadata?.ref],
    queryFn: ({ signal }) => getLogRequestContent(metadata?.ref ?? '', signal),
    enabled: retrievable,
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })

  const detail = retrievable && request.isSuccess ? request.data : undefined
  const status = detail?.status ?? metadata?.status ?? 'not_recorded'
  const capturedBytes = detail?.captured_bytes ?? metadata?.captured_bytes ?? 0
  const payload = detail?.payload
  const payloadJson = payload == null ? '' : JSON.stringify(payload, null, 2)
  let body = null

  if (retrievable && request.isPending) {
    body = (
      <LoadingState
        inline
        size='sm'
        message={t('Loading original request content...')}
      />
    )
  } else if (retrievable && request.isError) {
    body = (
      <ErrorState
        className='min-h-0 py-3'
        title={
          getServerErrorStatus(request.error) === 404
            ? t('Original request content is no longer available')
            : t('Failed to load original request content')
        }
        onRetry={() => void request.refetch()}
      />
    )
  } else if (detail) {
    const fields = getRequestContentFields(payload)
    body = (
      <div className='min-w-0 space-y-2.5'>
        {fields.length > 0 ? (
          fields.map((field) => (
            <RequestContentField
              key={field.name}
              label={t(field.label)}
              value={field.value}
            />
          ))
        ) : (
          <p className='text-muted-foreground text-xs'>
            {t('No prompt content in this request')}
          </p>
        )}
      </div>
    )
  }

  return (
    <DetailSection
      icon={<FileText className='size-3.5' aria-hidden='true' />}
      iconTone='chart-3'
      label={t('Original Request')}
    >
      <div className='min-w-0 space-y-2'>
        <div className='flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1'>
          <StatusBadge
            label={t(getRequestContentStatusLabel(status))}
            variant={status === 'recorded' ? 'success' : 'neutral'}
            size='sm'
            copyable={false}
          />
          {capturedBytes > 0 && (
            <span className='text-muted-foreground text-xs tabular-nums'>
              {t('{{size}} stored', {
                size: formatBytes(capturedBytes, locale),
              })}
            </span>
          )}
          {(status === 'truncated' ||
            metadata?.truncated ||
            detail?.truncated) && (
            <span className='text-xs text-amber-600 dark:text-amber-400'>
              {t('Truncated to the configured limit')}
            </span>
          )}
          {payloadJson !== '' && (
            <CopyButton
              value={payloadJson}
              variant='ghost'
              size='sm'
              tooltip={t('Copy original request')}
              aria-label={t('Copy original request')}
              className='ms-auto'
            />
          )}
        </div>
        {body}
      </div>
    </DetailSection>
  )
}

function RequestContentField(props: { label: string; value: unknown }) {
  const { t } = useTranslation()
  const messages = getRequestContentMessages(props.value)

  if (messages.length > 0) {
    return (
      <div className='min-w-0 space-y-1.5'>
        <p className='text-muted-foreground text-xs font-medium'>
          {props.label}
        </p>
        <div className='space-y-1.5'>
          {messages.map((message) => (
            <div
              key={message.index}
              className='bg-background min-w-0 rounded border p-2'
            >
              <div className='mb-1 flex items-center gap-1.5'>
                <StatusBadge
                  label={message.role || t('Unknown role')}
                  variant='neutral'
                  size='sm'
                  copyable={false}
                />
              </div>
              {message.text !== '' && (
                <p className='text-xs leading-relaxed wrap-break-word whitespace-pre-wrap'>
                  {message.text}
                </p>
              )}
              {message.structure != null && (
                <CodeBlock
                  code={JSON.stringify(message.structure, null, 2)}
                  language='json'
                  maxExpandedLines={16}
                  showToolbar={false}
                  className='mt-1.5'
                />
              )}
            </div>
          ))}
        </div>
      </div>
    )
  }

  if (typeof props.value === 'string') {
    return (
      <div className='min-w-0 space-y-1.5'>
        <p className='text-muted-foreground text-xs font-medium'>
          {props.label}
        </p>
        <p className='bg-background rounded border p-2 text-xs leading-relaxed wrap-break-word whitespace-pre-wrap'>
          {props.value}
        </p>
      </div>
    )
  }

  // A structured field (for example Gemini contents) keeps its exact shape.
  return (
    <div className='min-w-0 space-y-1.5'>
      <p className='text-muted-foreground text-xs font-medium'>{props.label}</p>
      <CodeBlock
        code={JSON.stringify(props.value, null, 2)}
        language='json'
        maxExpandedLines={16}
        showToolbar={false}
      />
    </div>
  )
}
