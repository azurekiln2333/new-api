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
import { useNavigate } from '@tanstack/react-router'
import { ExternalLink, Globe, Monitor } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { useStatus } from '@/hooks/use-status'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { fetchActiveChatKey } from '../hooks/use-active-chat-key'
import { useChatPresets } from '../hooks/use-chat-presets'
import {
  chatLinkRequiresApiKey,
  resolveChatUrl,
  type ChatPreset,
} from '../lib/chat-links'

function clientDestination(template: string): string | null {
  try {
    const url = new URL(template)
    if (
      [
        'javascript:',
        'data:',
        'vbscript:',
        'file:',
        'blob:',
        'about:',
      ].includes(url.protocol)
    ) {
      return null
    }
    return url.origin === 'null' ? url.protocol : url.origin
  } catch {
    return null
  }
}

export function AccessCards() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { loading, error } = useStatus()
  const { chatPresets, serverAddress } = useChatPresets()
  const [pendingPreset, setPendingPreset] = useState<ChatPreset | null>(null)
  const [isConnecting, setIsConnecting] = useState(false)
  const connecting = useRef(false)
  const operation = useRef(0)

  useEffect(
    () => () => {
      operation.current += 1
    },
    []
  )

  const clients = useMemo(
    () => chatPresets.filter((preset) => preset.type !== 'fluent'),
    [chatPresets]
  )

  async function connect(preset: ChatPreset) {
    if (connecting.current) return
    if (preset.type === 'web') {
      setPendingPreset(null)
      await navigate({ to: '/chat/$chatId', params: { chatId: preset.id } })
      return
    }

    // The landing page never resolves a credential until the user confirms.
    const currentOperation = ++operation.current
    const userId = useAuthStore.getState().auth.user?.id
    connecting.current = true
    setIsConnecting(true)
    try {
      const activeKey = chatLinkRequiresApiKey(preset.url)
        ? await fetchActiveChatKey()
        : undefined
      if (
        operation.current !== currentOperation ||
        useAuthStore.getState().auth.user?.id !== userId
      ) {
        return
      }
      const url = resolveChatUrl({
        template: preset.url,
        apiKey: activeKey,
        serverAddress,
      })
      if (!url) {
        toast.error(t('Invalid chat link. Please contact the administrator.'))
        return
      }
      window.open(url, '_blank', 'noopener,noreferrer')
      setPendingPreset(null)
    } catch (error) {
      if (operation.current === currentOperation) {
        handleServerError(
          error,
          t(
            'Unable to prepare chat link. Please ensure you have an enabled API key.'
          )
        )
      }
    } finally {
      if (operation.current === currentOperation) {
        connecting.current = false
        setIsConnecting(false)
      }
    }
  }

  function requestConnect(preset: ChatPreset) {
    if (connecting.current) return
    if (!clientDestination(preset.url)) {
      toast.error(t('Invalid chat link. Please contact the administrator.'))
      return
    }
    if (chatLinkRequiresApiKey(preset.url)) {
      setPendingPreset(preset)
      return
    }
    void connect(preset)
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Access configuration')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <p className='text-muted-foreground mb-4 text-sm'>
          {t('Choose a configured client to connect to this API.')}
        </p>
        {loading ? <LoadingState /> : null}
        {!loading && error ? (
          <ErrorState title={t('Failed to load client configuration')} />
        ) : null}
        {!loading && !error && clients.length === 0 ? (
          <EmptyState
            icon={Monitor}
            title={t('No clients configured')}
            description={t('Ask your administrator to configure a client.')}
          />
        ) : null}
        {!loading && !error && clients.length > 0 ? (
          <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3'>
            {clients.map((preset) => (
              <Card key={preset.id} className='min-w-0'>
                <CardHeader className='flex flex-row items-center gap-3'>
                  {preset.type === 'web' ? (
                    <Globe className='size-5 shrink-0' aria-hidden='true' />
                  ) : (
                    <Monitor className='size-5 shrink-0' aria-hidden='true' />
                  )}
                  <div className='min-w-0'>
                    <CardTitle className='break-words'>{preset.name}</CardTitle>
                    <p className='text-muted-foreground mt-1 text-xs break-all'>
                      {clientDestination(preset.url)}
                    </p>
                  </div>
                </CardHeader>
                <CardFooter className='mt-auto'>
                  <Button
                    className='w-full'
                    disabled={isConnecting}
                    aria-label={t('Connect to {{client}}', {
                      client: preset.name,
                    })}
                    onClick={() => requestConnect(preset)}
                  >
                    <ExternalLink className='size-4' aria-hidden='true' />
                    {t('Connect')}
                  </Button>
                </CardFooter>
              </Card>
            ))}
          </div>
        ) : null}
        <ConfirmDialog
          open={pendingPreset !== null}
          onOpenChange={(open) => {
            if (!open && !connecting.current) setPendingPreset(null)
          }}
          title={t('Connect to {{client}}?', {
            client: pendingPreset?.name ?? '',
          })}
          desc={
            <div className='space-y-2'>
              <p>
                {t(
                  'This will share an enabled API key with {{client}}. Only continue if you trust this client.',
                  { client: pendingPreset?.name ?? '' }
                )}
              </p>
              <p className='break-all'>
                {pendingPreset ? clientDestination(pendingPreset.url) : ''}
              </p>
            </div>
          }
          confirmText={
            isConnecting ? t('Preparing your chat link…') : t('Connect')
          }
          isLoading={isConnecting}
          handleConfirm={() => {
            if (pendingPreset) void connect(pendingPreset)
          }}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
