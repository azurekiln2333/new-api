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
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Loader2, Play, RefreshCw, X } from 'lucide-react'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { ModelSelector } from '@/components/model-group-selector'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import {
  Combobox,
  ComboboxCollection,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxGroup,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
  useComboboxAnchor,
} from '@/components/ui/combobox'
import { getChannelOptions } from '@/features/channels/api'
import type { ChannelOption, KeyStatus } from '@/features/channels/types'
import { useDebounce } from '@/hooks/use-debounce'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  cancelChannelFingerprint,
  getChannelFingerprint,
  getFingerprintChannel,
  getFingerprintKeys,
  previewChannelFingerprint,
  retryChannelFingerprintProbe,
  startChannelFingerprint,
  type FingerprintContext,
  type FingerprintJob,
  type FingerprintPreview,
  type FingerprintProbe,
  type RetryFingerprintPayload,
  type StartFingerprintPayload,
} from './fingerprint-api'

const MAX_CHALLENGES = 3
const POLL_INTERVAL_MS = 1500

// Translate only fixed gateway/SDK messages. Unknown provider/scoring text
// stays literal, without interpreting it as a translation key or template.
const FIXED_FINGERPRINT_MESSAGES = [
  'No challenge produced a reply.',
  'The check ran out of time before any challenge produced a reply.',
  'The replies could not be scored.',
  'Cancelled during the request.',
  'Cancelled before dispatch.',
  'fingerprint request no longer matches the previewed prompt',
  'fingerprint request could not be completed',
  'fingerprint reference data is not available',
  'the reply contained no text to score',
  'fingerprint preview is unavailable or expired',
  'Fingerprint preview is unavailable. Change prompts and try again.',
  'fingerprint check was not found or has expired',
  'fingerprint check already finished',
  'too many fingerprint checks are running; wait for one to finish',
  'too many fingerprint previews; wait for one to expire',
  'fingerprint sample cannot be retried',
  'fingerprint retry budget is exhausted',
  'fingerprint checks require a supported protocol with an enforceable output token limit',
  'fingerprint endpoint type is not supported',
  'the selected model is not available on this channel',
  'the selected key is unavailable for this model',
  'invalid fingerprint key selection',
  'authenticated fingerprint owner is unavailable',
  'a fingerprint preview is required',
  'a model is required',
  'invalid channel id',
  'the challenge count must be between 1 and 3',
] as const

const KNOWN_REPLY_REASON =
  'Consecutive numbers change differently from the language-model replies in the reference bank. Program-generated, sorted or shuffled numbers often do this. No ranking was produced.'

const STATE_TONES: Record<string, StatusVariant> = {
  complete: 'success',
  completed: 'success',
  running: 'info',
  inconclusive: 'warning',
  failed: 'danger',
  cancelled: 'neutral',
}

// Human-readable labels keep the locale keys descriptive; the job and probe
// states themselves stay machine values.
const STATE_LABELS: Record<string, string> = {
  running: 'Running',
  completed: 'Completed',
  inconclusive: 'Inconclusive',
  failed: 'Failed',
  cancelled: 'Cancelled',
  queued: 'Queued',
  complete: 'Complete',
}

// Both retained probe errors and failed API operations use the same safe
// message contract; translate at render time so language changes update them.
function translateRunMessage(
  message: string,
  t: (key: string, options?: Record<string, unknown>) => string
) {
  if ((FIXED_FINGERPRINT_MESSAGES as readonly string[]).includes(message)) {
    return t(message, { skipInterpolation: true })
  }
  if (message === 'context deadline exceeded') return t('Timed out')
  if (message === 'context canceled') return t('Cancelled during the request.')
  const httpFailure = /^fingerprint request failed \(HTTP ([1-5]\d{2})\)$/.exec(
    message
  )
  if (httpFailure && httpFailure[0] === message) {
    return t('fingerprint request failed (HTTP {{status}})', {
      status: httpFailure[1],
    })
  }
  if (message === KNOWN_REPLY_REASON) {
    return t(KNOWN_REPLY_REASON)
  }
  const budgetStop =
    /^Stopped before reaching the (\d+) token budget; the remaining budget cannot cover another request\.$/.exec(
      message
    )
  if (budgetStop) {
    return t(
      'Stopped before reaching the {{budget}} token budget; the remaining budget cannot cover another request.',
      { budget: budgetStop[1] }
    )
  }
  const received =
    /^Received (\d+)\/3 valid samples\. Three valid samples are required\.$/.exec(
      message
    )
  if (received) {
    return t(
      'Received {{used}}/3 valid samples. Three valid samples are required.',
      { used: received[1] }
    )
  }
  const ranked =
    /^Ranked (\d+) valid samples?\. Confidence requires three valid samples\.$/.exec(
      message
    )
  if (ranked) {
    return t(
      'Ranked {{used}} valid samples. Confidence requires three valid samples.',
      { used: ranked[1] }
    )
  }
  const STATIC_REASONS = [
    'Confidence is a closed-set probability within the reference bank.',
    'The confidence calibration does not match this detector. Confidence is unavailable.',
    'No valid samples. This round cannot be scored.',
  ]
  if (STATIC_REASONS.includes(message)) {
    return t(message)
  }
  const strict =
    /^Strict mode requires 3\/3 successful samples; received (\d+)\/3\.$/.exec(
      message
    )
  if (strict) {
    return t(
      'Strict mode requires 3/3 successful samples; received {{used}}/3.',
      { used: strict[1] }
    )
  }
  return message
}

const PROBE_SLOT_COUNT = 3

function FingerprintSampleCard(props: {
  index: number
  probe?: FingerprintProbe
  previewProbe?: { id: string; prompt: string; expected_count: number }
  onRetry?: () => void
  retryPending?: boolean
  statusUnavailable?: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [expanded, setExpanded] = useState(false)
  const promptId = useId()
  const titleId = useId()
  const probe = props.probe
  const [now, setNow] = useState(() => Date.now())
  const startedAt = probe?.started_at
  const isRunning = probe?.state === 'running' && !props.statusUnavailable
  useEffect(() => {
    if (!isRunning || !startedAt) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [isRunning, startedAt])
  const elapsed = startedAt ? Math.max(0, Math.floor((now - startedAt) / 1000)) : 0
  const prompt = probe?.prompt ?? props.previewProbe?.prompt
  let stateLabel = t('Not run')
  if (props.previewProbe) stateLabel = t('Ready')
  if (probe) stateLabel = t(STATE_LABELS[probe.state] ?? 'Unknown')
  if (
    props.statusUnavailable &&
    (probe?.state === 'running' || probe?.state === 'queued')
  ) {
    stateLabel = t('Status unavailable')
  }

  return (
    <Card
      role='region'
      aria-labelledby={titleId}
      data-probe-slot={probe ? 'probe' : 'unused'}
    >
      <CardHeader className='border-b'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <CardTitle id={titleId}>
            {t('Challenge {{index}}', { index: props.index + 1 })}
          </CardTitle>
          <StatusBadge
            label={stateLabel}
            variant={probe ? (STATE_TONES[probe.state] ?? 'neutral') : 'neutral'}
            copyable={false}
          />
        </div>
      </CardHeader>
      <CardContent className='flex min-w-0 flex-col gap-4'>
        <div className='flex min-w-0 flex-col gap-2'>
          <div className='flex items-center justify-between gap-2'>
            <span className='text-muted-foreground text-xs font-medium'>
              {t('Prompt')}
            </span>
            {prompt && <CopyButton value={prompt} size='sm' />}
          </div>
          <p
            id={promptId}
            className={cn(
              'min-h-20 text-sm leading-5 [overflow-wrap:anywhere] whitespace-pre-wrap',
              !expanded && 'line-clamp-4'
            )}
          >
            {prompt || '-'}
          </p>
          {prompt && (
            <Button
              variant='ghost'
              size='sm'
              className='self-start'
              aria-expanded={expanded}
              aria-controls={promptId}
              onClick={() => setExpanded((value) => !value)}
            >
              {t(expanded ? 'Collapse' : 'Expand')}
            </Button>
          )}
          {prompt && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Each prompt independently uses Chinese, English, Japanese, Korean, or French.'
              )}
            </p>
          )}
          {probe?.prompt_truncated && (
            <span className='text-muted-foreground text-xs'>
              {t('Truncated')}
            </span>
          )}
        </div>
        <div className='flex min-w-0 flex-col gap-2 border-t pt-4'>
          <span className='text-muted-foreground text-xs font-medium'>
            {t('Output')}
          </span>
          {isRunning && startedAt && (
            <p
              role='status'
              className='text-muted-foreground flex items-center gap-2 text-xs'
            >
              <Loader2 className='size-3 animate-spin' aria-hidden='true' />
              {t(
                'Waiting for reply: {{seconds}}s elapsed; request timeout {{timeout}}s.',
                {
                  seconds: formatNumber(elapsed, locale),
                  timeout: formatNumber(probe?.timeout_seconds ?? 0, locale),
                }
              )}
            </p>
          )}
          <p className='bg-muted/30 min-h-40 max-h-80 overflow-y-auto rounded-lg p-3 text-sm leading-6 [overflow-wrap:anywhere] whitespace-pre-wrap'>
            {probe?.output || '-'}
          </p>
          {probe?.output_truncated && (
            <span className='text-muted-foreground text-xs'>
              {t('Truncated')}
            </span>
          )}
          <span
            className='text-muted-foreground text-xs tabular-nums'
            aria-live='polite'
          >
            {t('Parsed numbers: {{count}}', {
              replace: { count: formatNumber(probe?.parsed_count ?? 0, locale) },
            })}
          </span>
          {probe?.error && (
            <p role='alert' className='text-destructive text-xs'>
              {translateRunMessage(probe.error, t)}
            </p>
          )}
          {props.onRetry && (
            <div className='space-y-2'>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Retries only this failed sample using the same prompt. This sends one additional paid request within the remaining run budget.'
                )}
              </p>
              <Button
                variant='outline'
                size='sm'
                onClick={props.onRetry}
                disabled={props.retryPending}
              >
                <RefreshCw aria-hidden='true' />
                {t('Retry sample')}
              </Button>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

// A fingerprint check needs channel operate rights. Reading the live identity
// here means a demotion, a permission edit or an account switch never keeps an
// earlier admin's channel names, models or running job on screen, because the
// console below is keyed by exactly those facts and is unmounted when they
// change.
function fingerprintAccessKey(
  user: ReturnType<typeof useAuthStore.getState>['auth']['user'],
  sessionID?: string
) {
  if (!user || user.role < ROLE.ADMIN) return null
  if (!hasPermission(user, 'channel', 'operate')) return null
  const channelGrants = JSON.stringify(
    user.permissions?.admin_permissions?.channel ?? null
  )
  return `${user.id}:${user.role}:${channelGrants}:${sessionID ?? ''}`
}

export function ChannelFingerprint() {
  const user = useAuthStore((state) => state.auth.user)
  const sessionID = useAuthStore((state) => state.auth.session?.sid)
  const accessKey = fingerprintAccessKey(user, sessionID)
  if (!accessKey) return null
  return <FingerprintConsole key={accessKey} identity={accessKey} />
}

function FingerprintConsole({ identity }: { identity: string }) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [channelId, setChannelId] = useState('')
  const [model, setModel] = useState('')
  const count = MAX_CHALLENGES
  const [run, setRun] = useState<{
    context: string
    job: FingerprintJob
  } | null>(null)
  const [selectedTestKey, setSelectedTestKey] = useState<KeyStatus | null>(null)
  const [previewAttempt, setPreviewAttempt] = useState(0)
  // A Start retry must use the submitted opaque handle and exact selection,
  // not mutable query data that a cache refresh can replace after acceptance.
  const [submittedPreview, setSubmittedPreview] = useState<{
    context: string
    preview: FingerprintPreview
  } | null>(null)
  const currentContext = useRef<string | null>(null)
  const startController = useRef<AbortController | null>(null)
  const cancelController = useRef<AbortController | null>(null)
  const retryController = useRef<AbortController | null>(null)
  const [startError, setStartError] = useState<{
    context: string
    message: string
  } | null>(null)
  const [pollError, setPollError] = useState('')
  // A failed read stops polling; Start only replays the saved preview/job.
  // Only an explicit Change prompts action authorizes a fresh challenge set.
  const [pollStopped, setPollStopped] = useState(false)
  const [channelOpen, setChannelOpen] = useState(false)
  const channelAnchor = useComboboxAnchor()
  const [keyword, setKeyword] = useState('')
  const debouncedKeyword = useDebounce(keyword, 300)
  const searchPending = keyword !== debouncedKeyword
  // Bumped by the retry button so a stopped poll starts again.
  const [pollAttempt, setPollAttempt] = useState(0)

  const channelsQuery = useInfiniteQuery({
    // Each search has its own page snapshot; obsolete pages cannot append to
    // another search. Selection hydration stays bounded to the chosen id.
    queryKey: ['fingerprint-channels', identity, channelId, debouncedKeyword],
    initialPageParam: 1,
    queryFn: async ({ signal, pageParam }) =>
      requireServerSuccess(
        await getChannelOptions(
          {
            p: pageParam,
            page_size: 30,
            ...(debouncedKeyword.trim()
              ? { keyword: debouncedKeyword.trim() }
              : {}),
            ...(channelId ? { ids: channelId } : {}),
          },
          signal
        )
      ),
    getNextPageParam: (lastPage) =>
      lastPage.data?.has_more ? lastPage.data.page + 1 : undefined,
    enabled: !searchPending,
    // Only an id hydration within the same search may retain its old snapshot.
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === identity &&
      previousQuery.queryKey[3] === debouncedKeyword
        ? previousData
        : undefined,
    staleTime: 60_000,
    meta: { errorToast: false },
  })
  const channels = useMemo(() => {
    const byId = new Map<number, ChannelOption>()
    for (const page of channelsQuery.data?.pages ?? []) {
      for (const channel of page.data?.items ?? []) {
        byId.set(channel.id, channel)
      }
    }
    return [...byId.values()]
  }, [channelsQuery.data])
  const selectedChannel = channels.find(
    (channel) => String(channel.id) === channelId
  )
  const selectedChannelUnavailable =
    !!channelId &&
    !searchPending &&
    channelsQuery.isSuccess &&
    !channelsQuery.isFetching &&
    !channelsQuery.isPlaceholderData &&
    !selectedChannel
  const [chosenChannel, setChosenChannel] = useState<ChannelOption | null>(null)
  useEffect(() => {
    if (selectedChannelUnavailable) {
      setChosenChannel(null)
      return
    }
    if (selectedChannel) setChosenChannel(selectedChannel)
  }, [selectedChannel, selectedChannelUnavailable])
  const selectedChannelLabel = selectedChannelUnavailable
    ? `#${channelId} ${t('Unavailable')}`
    : `#${channelId} ${selectedChannel?.name ?? chosenChannel?.name ?? ''}`
  const channelItems = useMemo(() => {
    const items = (searchPending ? [] : channels).map((channel) => ({
      value: String(channel.id),
      label: `#${channel.id} ${channel.name}`,
    }))
    if (channelId && !items.some((item) => item.value === channelId)) {
      items.unshift({ value: channelId, label: selectedChannelLabel })
    }
    return items
  }, [channelId, channels, searchPending, selectedChannelLabel])
  const channelQuery = useQuery({
    queryKey: ['fingerprint-channel', identity, channelId],
    queryFn: ({ signal }) => getFingerprintChannel(Number(channelId), signal),
    enabled: !!channelId && !selectedChannelUnavailable,
    retry: false,
    meta: { errorToast: false },
  })
  const channel =
    channelQuery.data?.id === Number(channelId) ? channelQuery.data : undefined
  const isMultiKey = channel?.is_multi_key === true
  const keysQuery = useInfiniteQuery({
    queryKey: ['fingerprint-keys', identity, channelId],
    initialPageParam: 1,
    queryFn: ({ signal, pageParam }) =>
      getFingerprintKeys(Number(channelId), pageParam, signal),
    getNextPageParam: (lastPage) =>
      lastPage.page < lastPage.total_pages ? lastPage.page + 1 : undefined,
    enabled: isMultiKey && !selectedChannelUnavailable,
    retry: false,
    meta: { errorToast: false },
  })
  const keyPages = keysQuery.data?.pages ?? []
  const keyRevision = keyPages[0]?.revision
  const consistentKeys =
    keyRevision !== undefined &&
    Number.isSafeInteger(keyRevision) &&
    keyRevision > 0 &&
    keyPages.every((page) => page.revision === keyRevision)
  const keys = consistentKeys ? keyPages.flatMap((page) => page.keys) : []
  const selectedKey = selectedTestKey
    ? keys.find(
        (key) =>
          key.id === selectedTestKey.id &&
          key.rev === selectedTestKey.rev &&
          key.index === selectedTestKey.index
      )
    : undefined
  const keyUnavailable = selectedTestKey !== null && (!isMultiKey || !selectedKey)
  const selectedKeyValue = selectedTestKey
    ? `${selectedTestKey.id}:${selectedTestKey.rev}:${selectedTestKey.index}`
    : 'auto'
  const keyItems = [
    { value: 'auto', label: t('Auto (by tested model)') },
    ...(keyUnavailable
      ? [{ value: selectedKeyValue, label: t('Unavailable'), disabled: true }]
      : []),
    ...keys.map((key) => ({
      value: `${key.id}:${key.rev}:${key.index}`,
      label: `${key.index + 1} · ${key.key_preview || '-'} · ${t(key.status === 1 ? 'Enabled' : 'Disabled')}`,
      disabled:
        !key.id ||
        !/^[1-9]\d*$/.test(key.id) ||
        !key.rev ||
        !Number.isSafeInteger(key.rev) ||
        key.rev < 1 ||
        !Number.isSafeInteger(key.index) ||
        key.index < 0,
    })),
  ]
  const channelModels = useMemo(() => {
    if (selectedChannelUnavailable || keyUnavailable) return []
    // For multi-Key channels the server's automatic projection is the
    // authoritative union of models currently served by eligible Keys. A
    // missing projection must fail closed instead of silently offering a model
    // that automatic routing cannot dispatch.
    let names: string[]
    if (selectedTestKey) {
      names = channel?.models ?? []
    } else if (isMultiKey) {
      names = channel?.auto_models ?? []
    } else {
      names = channel?.models ?? []
    }
    return [...new Set(names)]
      .filter((name) => {
        if (!selectedKey) return selectedTestKey === null
        if (selectedKey.scope_mode === 'all') return true
        const models = selectedKey.models ?? []
        if (selectedKey.scope_mode === 'allowlist') return models.includes(name)
        return !models.length || models.includes(name)
      })
      .map((name) => ({ label: name, value: name }))
  }, [
    channel,
    isMultiKey,
    selectedChannelUnavailable,
    keyUnavailable,
    selectedKey,
    selectedTestKey,
  ])
  // A channel switch replaces the candidates, so a model the new channel does
  // not serve must not stay selected and be submitted against it.
  useEffect(() => {
    if (!channelId) return
    if (channelModels.some((option) => option.value === model)) return
    setModel(channelModels[0]?.value ?? '')
  }, [channelId, channelModels, model])
  // The effect that repairs a stale model runs after render, so the submit path
  // must reject a model the current channel cannot serve instead of relying on
  // it having caught up.
  const modelServable =
    !!channelModels.length &&
    channelModels.some((option) => option.value === model.trim())

  const contextReady =
    !!channel &&
    !channelQuery.isError &&
    !channelQuery.isFetching &&
    !selectedChannelUnavailable &&
    modelServable &&
    !keyUnavailable &&
    (!isMultiKey ||
      (keysQuery.isSuccess &&
        !keysQuery.isError &&
        !keysQuery.isFetching &&
        consistentKeys))
  const requestContext: FingerprintContext = {
    model: model.trim(),
    count,
    ...(selectedTestKey
      ? {
          key_entry_id: selectedTestKey.id,
          expected_rev: selectedTestKey.rev,
          key_index: selectedTestKey.index,
        }
      : {}),
  }
  const contextKey = JSON.stringify([channelId, requestContext, previewAttempt])
  currentContext.current = contextReady ? contextKey : null
  const isCurrentContext = useCallback(
    (context: string) => {
      const auth = useAuthStore.getState().auth
      return (
        currentContext.current === context &&
        fingerprintAccessKey(auth.user, auth.session?.sid) === identity
      )
    },
    [identity]
  )
  useEffect(() => {
    currentContext.current = contextReady ? contextKey : null
    return () => {
      currentContext.current = null
      startController.current?.abort()
      cancelController.current?.abort()
      retryController.current?.abort()
    }
  }, [contextKey, contextReady])
  const job = contextReady && run?.context === contextKey ? run.job : null
  const submitted =
    submittedPreview?.context === contextKey ? submittedPreview : null
  const previewQuery = useQuery({
    queryKey: ['fingerprint-preview', identity, contextKey],
    queryFn: async ({ signal }) => {
      signal.throwIfAborted()
      if (!isCurrentContext(contextKey)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const generated = await previewChannelFingerprint(
        Number(channelId),
        requestContext,
        signal
      )
      signal.throwIfAborted()
      if (!isCurrentContext(contextKey)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      return generated
    },
    enabled: contextReady && !submitted,
    staleTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchOnMount: false,
    meta: { errorToast: false },
  })
  let candidatePreview = contextReady ? previewQuery.data : undefined
  if (contextReady && submitted) candidatePreview = submitted.preview
  const preview =
    candidatePreview &&
    typeof candidatePreview.preview_id === 'string' &&
    !!candidatePreview.preview_id.trim() &&
    Number.isFinite(candidatePreview.expires_at) &&
    Array.isArray(candidatePreview.probes) &&
    candidatePreview.probes.length === count &&
    new Set(candidatePreview.probes.map((probe) => probe.id)).size === count &&
    candidatePreview.probes.every(
      (probe) =>
        typeof probe.id === 'string' &&
        !!probe.id.trim() &&
        typeof probe.prompt === 'string' &&
        !!probe.prompt.trim()
    ) &&
    candidatePreview.channel_id === Number(channelId) &&
    candidatePreview.model === requestContext.model &&
    !candidatePreview.endpoint_type &&
    candidatePreview.count === requestContext.count &&
    candidatePreview.key_entry_id === requestContext.key_entry_id &&
    candidatePreview.expected_rev === requestContext.expected_rev &&
    candidatePreview.key_index === requestContext.key_index
      ? candidatePreview
      : undefined

  const startMutation = useMutation({
    mutationFn: async (submission: {
      context: string
      channelId: number
      payload: StartFingerprintPayload
      signal: AbortSignal
    }) => {
      submission.signal.throwIfAborted()
      if (!isCurrentContext(submission.context)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const started = await startChannelFingerprint(
        submission.channelId,
        submission.payload,
        submission.signal
      )
      submission.signal.throwIfAborted()
      return started
    },
    retry: false,
    onSuccess: (started, submission) => {
      if (submission.signal.aborted || !isCurrentContext(submission.context)) {
        return
      }
      setStartError(null)
      setPollError('')
      setPollStopped(false)
      setRun((current) => {
        if (
          current?.context === submission.context &&
          current.job.id === started.id &&
          ((started.revision ?? 0) < (current.job.revision ?? 0) ||
            ((started.revision ?? 0) === (current.job.revision ?? 0) &&
              current.job.state !== 'running' &&
              started.state === 'running'))
        ) {
          return current
        }
        return { context: submission.context, job: started }
      })
    },
    onError: (error, submission) => {
      if (submission.signal.aborted || !isCurrentContext(submission.context)) {
        return
      }
      setStartError({
        context: submission.context,
        message: getServerErrorMessage(error),
      })
    },
  })

  const cancelMutation = useMutation({
    mutationFn: async (request: {
      context: string
      channelId: number
      jobId: string
      revision: number
      signal: AbortSignal
    }) => {
      request.signal.throwIfAborted()
      if (!isCurrentContext(request.context)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const cancelled = await cancelChannelFingerprint(
        request.channelId,
        request.jobId,
        request.signal,
        request.revision
      )
      request.signal.throwIfAborted()
      return cancelled
    },
    onSuccess: (cancelled, request) => {
      if (
        request.signal.aborted ||
        !isCurrentContext(request.context) ||
        cancelled.id !== request.jobId ||
        cancelled.channel_id !== request.channelId ||
        (cancelled.revision ?? 1) !== request.revision
      ) {
        return
      }
      // Disabling a query does not abort its GET. Cancel the old generation
      // before installing the acknowledgement; a still-running cancellation
      // may continue polling, but only with fresh reads in the next generation.
      void queryClient.cancelQueries({
        queryKey: [
          'fingerprint-job',
          identity,
          request.context,
          request.channelId,
          request.jobId,
        ],
        exact: false,
      })
      setPollAttempt((attempt) => attempt + 1)
      setPollError('')
      setPollStopped(false)
      setRun((current) =>
        current?.context === request.context &&
        current.job.id === cancelled.id &&
        current.job.channel_id === cancelled.channel_id &&
        (cancelled.revision ?? 1) === (current.job.revision ?? 1) &&
        (current.job.state === 'running' || cancelled.state !== 'running')
          ? { ...current, job: cancelled }
          : current
      )
    },
    onError: (error, request) => {
      if (request.signal.aborted || !isCurrentContext(request.context)) return
      handleServerError(error, undefined, {
        title: translateRunMessage(getServerErrorMessage(error), t),
      })
    },
  })
  const retryMutation = useMutation({
    mutationFn: async (submission: {
      context: string
      channelId: number
      jobId: string
      payload: RetryFingerprintPayload
      signal: AbortSignal
    }) => {
      submission.signal.throwIfAborted()
      if (!isCurrentContext(submission.context)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const retried = await retryChannelFingerprintProbe(
        submission.channelId,
        submission.jobId,
        submission.payload,
        submission.signal
      )
      submission.signal.throwIfAborted()
      return retried
    },
    retry: false,
    onSuccess: (retried, submission) => {
      if (submission.signal.aborted || !isCurrentContext(submission.context)) {
        return
      }
      void queryClient.cancelQueries({
        queryKey: ['fingerprint-job', identity, submission.context],
        exact: false,
      })
      setPollAttempt((attempt) => attempt + 1)
      setPollError('')
      setPollStopped(false)
      setStartError(null)
      setRun((current) =>
        current?.context === submission.context &&
        current.job.id === retried.id &&
        current.job.channel_id === retried.channel_id &&
        ((retried.revision ?? 0) > (current.job.revision ?? 0) ||
          ((retried.revision ?? 0) === (current.job.revision ?? 0) &&
            (current.job.state === 'running' || retried.state !== 'running')))
          ? { ...current, job: retried }
          : current
      )
    },
    onError: (error, submission) => {
      if (submission.signal.aborted || !isCurrentContext(submission.context)) {
        return
      }
      setStartError({
        context: submission.context,
        message: getServerErrorMessage(error),
      })
    },
  })
  const retrying =
    retryMutation.isPending &&
    retryMutation.variables?.context === contextKey &&
    !retryMutation.variables.signal.aborted
  const starting =
    startMutation.isPending &&
    startMutation.variables?.context === contextKey &&
    !startMutation.variables.signal.aborted

  const jobId = job?.id
  const jobChannelId = job?.channel_id
  const running = job?.state === 'running'
  // One query drives the poll instead of a bare interval: it never overlaps a
  // request with the next tick, and stops as soon as the run is terminal.
  const pollQuery = useQuery({
    queryKey: [
      'fingerprint-job',
      identity,
      contextKey,
      jobChannelId,
      jobId,
      pollAttempt,
      job?.revision,
    ],
    queryFn: async ({ signal }) => {
      signal.throwIfAborted()
      if (!isCurrentContext(contextKey)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const latest = await getChannelFingerprint(
        jobChannelId as number,
        jobId as string,
        signal
      )
      signal.throwIfAborted()
      if (!isCurrentContext(contextKey)) {
        throw new DOMException('Aborted', 'AbortError')
      }
      return latest
    },
    enabled: !!running && !pollStopped && !!jobId && !!jobChannelId,
    refetchInterval: POLL_INTERVAL_MS,
    refetchIntervalInBackground: false,
    retry: false,
    meta: { errorToast: false },
  })
  useEffect(() => {
    const latest = pollQuery.data
    if (!latest || !isCurrentContext(contextKey)) return
    setPollError('')
    setPollStopped(false)
    setRun((current) =>
      current?.context === contextKey &&
      current.job.id === latest.id &&
      current.job.channel_id === latest.channel_id &&
      (latest.revision ?? 0) === (current.job.revision ?? 0) &&
      (current.job.state === 'running' || latest.state !== 'running')
        ? { ...current, job: latest }
        : current
    )
  }, [contextKey, isCurrentContext, pollQuery.data])
  useEffect(() => {
    if (!pollQuery.isError || !isCurrentContext(contextKey)) return
    setPollError(getServerErrorMessage(pollQuery.error))
    setPollStopped(true)
  }, [contextKey, isCurrentContext, pollQuery.isError, pollQuery.error])

  const retryPoll = () => {
    setPollError('')
    setPollStopped(false)
    setPollAttempt((attempt) => attempt + 1)
  }

  const processedProbes =
    job?.probes.filter((probe) =>
      ['complete', 'failed', 'cancelled'].includes(probe.state)
    ).length ?? 0
  const progress = job?.probes.length
    ? Math.round((processedProbes / job.probes.length) * 100)
    : 0
  const progressLabel = t('{{done}} of {{total}} challenges processed', {
    done: formatNumber(processedProbes, locale),
    total: formatNumber(job?.probes.length ?? 0, locale),
  })
  const canStart =
    contextReady && !!preview && !starting && !retrying && (!job || pollStopped)
  const resetFingerprintRun = () => {
    setRun(null)
    setStartError(null)
    setPollError('')
    setPollStopped(false)
    setSubmittedPreview(null)
    setPreviewAttempt((attempt) => attempt + 1)
  }
  const startCheck = () => {
    if (!canStart || !preview) return
    if (!submitted && preview.expires_at <= Date.now()) {
      setStartError({
        context: contextKey,
        message:
          'Fingerprint preview is unavailable. Change prompts and try again.',
      })
      return
    }
    const payload = Object.freeze({
      preview_id: preview.preview_id,
      model: preview.model,
      count: preview.count,
      ...(preview.key_entry_id
        ? {
            key_entry_id: preview.key_entry_id,
            expected_rev: preview.expected_rev,
            key_index: preview.key_index,
          }
        : {}),
    })
    if (!submitted) {
      setSubmittedPreview({
        context: contextKey,
        preview: Object.freeze({
          ...payload,
          channel_id: preview.channel_id,
          expires_at: preview.expires_at,
          probes: preview.probes.map((probe) => Object.freeze({ ...probe })),
        }),
      })
    }
    startController.current?.abort()
    const controller = new AbortController()
    startController.current = controller
    startMutation.mutate({
      context: contextKey,
      channelId: preview.channel_id,
      payload,
      signal: controller.signal,
    })
  }
  // Editing is allowed while a stopped run is still shown, so the operator can
  // pick another channel or model and start a fresh check.
  const controlsDisabled = (running && !pollStopped) || starting || retrying
  const channelInput = channelOpen ? keyword : selectedChannelLabel

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Channel fingerprint')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <Alert>
            <AlertDescription>
              {t(
                'A fingerprint check sends paid requests to the selected channel. A match is a ranking against a reference bank and does not prove model identity, quality, or authenticity.'
              )}
            </AlertDescription>
          </Alert>

          <p className='text-muted-foreground text-xs'>
            {running && !pollStopped && `${t('Polling')} · `}
            {t(
              'Replies are not streamed. Status and elapsed request time refresh while each sample runs; output appears when its request finishes.'
            )}
          </p>
          <Card>
            <CardHeader>
              <CardTitle>{t('Run a check')}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-channel'>{t('Channel')}</Label>
                  <Combobox
                    items={channelItems}
                    value={
                      channelItems.find((item) => item.value === channelId) ??
                      null
                    }
                    inputValue={channelId || channelOpen ? channelInput : ''}
                    open={channelOpen && !controlsDisabled}
                    filter={null}
                    isItemEqualToValue={(item, value) =>
                      item.value === value.value
                    }
                    onOpenChange={(open) => {
                      setChannelOpen(open)
                      if (!open) setKeyword('')
                    }}
                    onInputValueChange={(value, details) => {
                      if (details.reason === 'input-change') setKeyword(value)
                    }}
                    onValueChange={(item) => {
                      if (!item || item.value === channelId) return
                      setChannelId(item.value)
                      setModel('')
                      setSelectedTestKey(null)
                      resetFingerprintRun()
                      setChosenChannel(
                        channels.find(
                          (channel) => String(channel.id) === item.value
                        ) ?? null
                      )
                    }}
                    disabled={controlsDisabled}
                  >
                    <div ref={channelAnchor} className='min-w-0'>
                      <ComboboxInput
                        id='fingerprint-channel'
                        placeholder={t('Search channels')}
                        disabled={controlsDisabled}
                        title={channelId ? selectedChannelLabel : undefined}
                        className='w-full'
                      />
                    </div>
                    <ComboboxContent
                      anchor={channelAnchor}
                      className='max-w-[calc(100vw-2rem)]'
                    >
                      <ComboboxEmpty>
                        {t(
                          searchPending || channelsQuery.isFetching
                            ? 'Loading'
                            : 'No results found'
                        )}
                      </ComboboxEmpty>
                      <ComboboxList>
                        <ComboboxGroup>
                          <ComboboxCollection>
                            {(item: { value: string; label: string }) => (
                              <ComboboxItem key={item.value} value={item}>
                                <span className='min-w-0 [overflow-wrap:anywhere] whitespace-normal'>
                                  {item.label}
                                </span>
                              </ComboboxItem>
                            )}
                          </ComboboxCollection>
                        </ComboboxGroup>
                      </ComboboxList>
                      {channelsQuery.hasNextPage && (
                        <div className='border-t p-2'>
                          <Button
                            variant='outline'
                            size='sm'
                            className='w-full'
                            onClick={() => void channelsQuery.fetchNextPage()}
                            disabled={
                              controlsDisabled ||
                              searchPending ||
                              channelsQuery.isFetching ||
                              channelsQuery.isPlaceholderData
                            }
                          >
                            {channelsQuery.isFetchingNextPage && (
                              <Loader2
                                className='animate-spin'
                                aria-hidden='true'
                              />
                            )}
                            {t('Load more')}
                          </Button>
                        </div>
                      )}
                    </ComboboxContent>
                  </Combobox>
                  {selectedChannelUnavailable && (
                    <p role='status' className='text-destructive text-xs'>
                      {t('Unavailable')}
                    </p>
                  )}
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-model'>{t('Model')}</Label>
                  <ModelSelector
                    triggerId='fingerprint-model'
                    className='w-full justify-start'
                    selectedModel={model}
                    models={channelModels}
                    onModelChange={(value) => {
                      if (value === model) return
                      setModel(value)
                      resetFingerprintRun()
                    }}
                    disabled={
                      controlsDisabled ||
                      !channelId ||
                      selectedChannelUnavailable
                    }
                  />
                  {!channelId && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Choose a channel to list its models')}
                    </p>
                  )}
                  {!!channelId &&
                    !selectedChannelUnavailable &&
                    !channelModels.length && (
                      <p className='text-muted-foreground text-xs'>
                        {t('This channel declares no models')}
                      </p>
                    )}
                </div>
                {(isMultiKey || selectedTestKey) && (
                  <div className='space-y-2'>
                    <Label htmlFor='fingerprint-key'>{t('Tested key')}</Label>
                    <Combobox
                      id='fingerprint-key'
                      options={keyItems}
                      value={selectedKeyValue}
                      onValueChange={(value) => {
                        if (!value || value === selectedKeyValue) return
                        const selected = keys.find(
                          (key) => `${key.id}:${key.rev}:${key.index}` === value
                        )
                        if (
                          value !== 'auto' &&
                          (!selected?.id ||
                            !/^[1-9]\d*$/.test(selected.id) ||
                            !selected.rev ||
                            !Number.isSafeInteger(selected.rev) ||
                            selected.rev < 1 ||
                            !Number.isSafeInteger(selected.index) ||
                            selected.index < 0)
                        ) {
                          return
                        }
                        setSelectedTestKey(selected ? { ...selected } : null)
                        setModel('')
                        resetFingerprintRun()
                      }}
                      disabled={controlsDisabled || keysQuery.isPending}
                      className='w-full'
                    />
                    {(keyUnavailable ||
                      (keysQuery.isSuccess && !consistentKeys)) && (
                      <div className='space-y-2'>
                        <p role='status' className='text-destructive text-xs'>
                          {t(
                            'Channel key configuration changed, reload and retry'
                          )}
                        </p>
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => void keysQuery.refetch()}
                          disabled={controlsDisabled || keysQuery.isFetching}
                        >
                          {t('Retry')}
                        </Button>
                      </div>
                    )}
                    {keysQuery.hasNextPage && (
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => void keysQuery.fetchNextPage()}
                        disabled={controlsDisabled || keysQuery.isFetching}
                      >
                        {t('Load more')}
                      </Button>
                    )}
                  </div>
                )}
              </div>
            </CardContent>
          </Card>

          <div className='grid items-start gap-4 md:grid-cols-3'>
            {Array.from({ length: PROBE_SLOT_COUNT }, (_, index) => {
              const probe = job?.probes[index]
              const previewProbe = preview?.probes[index]
              return (
                <FingerprintSampleCard
                  key={`${job?.id ?? preview?.preview_id ?? 'initial'}:${probe?.id ?? previewProbe?.id ?? index}`}
                  index={index}
                  probe={probe}
                  previewProbe={job ? undefined : previewProbe}
                  retryPending={retrying}
                  statusUnavailable={pollStopped}
                  onRetry={
                    job &&
                    preview &&
                    probe?.state === 'failed' &&
                    probe.retry_allowed &&
                    !running &&
                    !starting
                      ? () => {
                          retryController.current?.abort()
                          const controller = new AbortController()
                          retryController.current = controller
                          retryMutation.mutate({
                            context: contextKey,
                            channelId: job.channel_id,
                            jobId: job.id,
                            payload: {
                              preview_id: preview.preview_id,
                              model: preview.model,
                              count: preview.count,
                              ...(preview.key_entry_id
                                ? {
                                    key_entry_id: preview.key_entry_id,
                                    expected_rev: preview.expected_rev,
                                    key_index: preview.key_index,
                                  }
                                : {}),
                              probe_id: probe.id,
                              expected_attempt: probe.attempt ?? 1,
                            },
                            signal: controller.signal,
                          })
                        }
                      : undefined
                  }
                />
              )
            })}
          </div>
          {startError?.context === contextKey && contextReady && (
            <p role='alert' className='text-destructive text-sm'>
              {translateRunMessage(startError.message, t)}
            </p>
          )}
          {contextReady && previewQuery.isSuccess && !preview && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'Fingerprint preview is unavailable. Change prompts and try again.'
              )}
            </p>
          )}
          {contextReady && !submitted && previewQuery.isError && (
            <ErrorState
              description={translateRunMessage(
                getServerErrorMessage(previewQuery.error),
                t
              )}
              onRetry={() => void previewQuery.refetch()}
            />
          )}
          <div className='flex flex-wrap justify-end gap-2'>
            <Button
              variant='outline'
              onClick={resetFingerprintRun}
              disabled={
                !contextReady || controlsDisabled || previewQuery.isFetching
              }
            >
              <RefreshCw aria-hidden='true' />
              {t('Change prompts')}
            </Button>
            <Button onClick={startCheck} disabled={!canStart}>
              {starting ? (
                <Loader2 className='animate-spin' aria-hidden='true' />
              ) : (
                <Play aria-hidden='true' />
              )}
              {t('Start check')}
            </Button>
            {running && job && (
              <Button
                variant='outline'
                onClick={() => {
                  cancelController.current?.abort()
                  const controller = new AbortController()
                  cancelController.current = controller
                  cancelMutation.mutate({
                    context: contextKey,
                    channelId: job.channel_id,
                    jobId: job.id,
                    revision: job.revision ?? 1,
                    signal: controller.signal,
                  })
                }}
                disabled={
                  cancelMutation.isPending &&
                  cancelMutation.variables?.context === contextKey &&
                  !cancelMutation.variables.signal.aborted
                }
              >
                <X aria-hidden='true' />
                {t('Cancel')}
              </Button>
            )}
          </div>

          {job && (
            <Card>
              <CardHeader>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <CardTitle className='min-w-0 break-words'>
                    {`#${job.channel_id} ${job.channel_name} · ${job.model}`}
                  </CardTitle>
                  <StatusBadge
                    copyable={false}
                    variant={STATE_TONES[job.state] ?? 'neutral'}
                    label={t(
                      pollStopped
                        ? 'Status unavailable'
                        : (STATE_LABELS[job.state] ?? 'Unknown')
                    )}
                  />
                </div>
              </CardHeader>
              <CardContent className='space-y-4'>
                <div className='space-y-1'>
                  <Progress value={progress} aria-label={progressLabel} />
                  <p
                    className='text-muted-foreground text-xs'
                    aria-live='polite'
                  >
                    {progressLabel}
                  </p>
                </div>
                {pollError && (
                  <div
                    role='alert'
                    className='flex flex-wrap items-center gap-2'
                  >
                    <p className='text-destructive text-sm'>
                      {translateRunMessage(pollError, t)}
                    </p>
                    <Button variant='outline' size='sm' onClick={retryPoll}>
                      {t('Retry')}
                    </Button>
                  </div>
                )}
                {job.error && (
                  <p role='alert' className='text-warning text-sm'>
                    {translateRunMessage(job.error, t)}
                  </p>
                )}
                {job.analysis?.Reason && (
                  <p className='text-muted-foreground text-xs'>
                    {translateRunMessage(job.analysis.Reason, t)}
                  </p>
                )}
                {job.analysis && !!job.analysis.Results?.length && (
                  <div className='space-y-2'>
                    <h3 className='text-sm font-medium'>
                      {t('Closest matches')}
                    </h3>
                    <ol className='space-y-1'>
                      {job.analysis.Results.map((candidate) => (
                        <li
                          key={candidate.Model}
                          className='flex flex-wrap items-center justify-between gap-2 text-sm'
                        >
                          <span className='min-w-0 break-words'>
                            {candidate.DisplayName || candidate.Model}
                            {candidate.FamilyName && (
                              <span className='text-muted-foreground'>
                                {` · ${candidate.FamilyName}`}
                              </span>
                            )}
                          </span>
                          <span className='text-muted-foreground text-xs'>
                            {typeof candidate.Probability === 'number'
                              ? `${(candidate.Probability * 100).toFixed(1)}%`
                              : t('score {{score}} (uncalibrated)', {
                                  score: candidate.Score.toFixed(3),
                                })}
                          </span>
                        </li>
                      ))}
                    </ol>
                  </div>
                )}
                <dl className='text-muted-foreground grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs'>
                  <dt>{t('Reference bank built')}</dt>
                  <dd className='break-words'>
                    {job.analysis?.BankVersion ||
                      job.detector_version ||
                      t('Unknown')}
                  </dd>
                  {job.bank_sha256 && (
                    <>
                      <dt>{t('Bank checksum')}</dt>
                      <dd className='font-mono break-all'>{job.bank_sha256}</dd>
                    </>
                  )}
                  {job.detector_sha256 && (
                    <>
                      <dt>{t('Detector checksum')}</dt>
                      <dd className='font-mono break-all'>
                        {job.detector_sha256}
                      </dd>
                    </>
                  )}
                  <dt>{t('Started')}</dt>
                  <dd className='break-words'>
                    {new Date(job.started_at).toLocaleString(locale)}
                  </dd>
                </dl>
                <div className='text-muted-foreground space-y-1 text-xs'>
                  <p>
                    {t(
                      job.limitation ||
                        'A match is a ranking relative to the reference bank. It does not prove model identity, quality, or authenticity, and a model outside the bank still receives a closest candidate.'
                    )}
                  </p>
                  <p>
                    {t(
                      job.attribution ||
                        'Fingerprint scoring uses the MIT-licensed lm-detector reference bank and detector (Copyright (c) 2026 xqy2006, github.com/Ikaleio/lm-detector).'
                    )}
                  </p>
                </div>
              </CardContent>
            </Card>
          )}

          {channelQuery.isError && (
            <ErrorState
              description={translateRunMessage(
                getServerErrorMessage(channelQuery.error),
                t
              )}
              onRetry={() => void channelQuery.refetch()}
            />
          )}
          {isMultiKey && keysQuery.isError && (
            <ErrorState
              description={translateRunMessage(
                getServerErrorMessage(keysQuery.error),
                t
              )}
              onRetry={() => void keysQuery.refetch()}
            />
          )}
          {channelsQuery.isError && (
            <ErrorState
              description={translateRunMessage(
                getServerErrorMessage(channelsQuery.error),
                t
              )}
              onRetry={() => void channelsQuery.refetch()}
            />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
