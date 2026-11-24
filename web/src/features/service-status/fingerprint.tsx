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
import { useMutation, useQuery } from '@tanstack/react-query'
import { Loader2, Play, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { ModelSelector } from '@/components/model-group-selector'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getChannelOptions } from '@/features/channels/api'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  cancelChannelFingerprint,
  getChannelFingerprint,
  startChannelFingerprint,
  type FingerprintJob,
  type FingerprintProbe,
} from './fingerprint-api'

// Only endpoints whose reply text this build can read are offered; the gateway
// rejects the others. Protocol names stay untranslated, as in the channel test.
const ENDPOINTS = [
  { value: 'openai', label: 'Chat Completions' },
  { value: 'openai-response', label: 'Responses' },
  { value: 'anthropic', label: 'Messages' },
  { value: 'gemini', label: 'Gemini generateContent' },
] as const

const MAX_CHALLENGES = 3
const POLL_INTERVAL_MS = 1500

// The run may stop on a sentence the gateway wrote itself (no reply, out of
// time, unscorable replies, cancelled). Those are translated; anything else is
// an upstream or SDK message and is shown verbatim.
const RUN_STOP_REASONS = [
  'No challenge produced a reply.',
  'The check ran out of time before any challenge produced a reply.',
  'The replies could not be scored.',
  'Cancelled during the request.',
  'Cancelled before dispatch.',
] as const

const KNOWN_REPLY_REASON =
  'Consecutive numbers change differently from the language-model replies in the reference bank. Program-generated, sorted or shuffled numbers often do this. No ranking was produced.'

const STATE_TONES: Record<string, StatusVariant> = {
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

// probeSummary describes one finished or failed challenge: how many integers it
// returned and what it cost, or why it produced nothing.
function probeSummary(
  probe: FingerprintProbe,
  t: (key: string, options?: Record<string, unknown>) => string,
  locale: Intl.LocalesArgument
) {
  if (probe.state === 'complete') {
    return ` · ${probe.parsed_count}/${probe.expected_count} · ${formatNumber(
      probe.prompt_tokens + probe.output_tokens,
      locale
    )} ${t('tokens')} · ${t('output limit {{tokens}}', {
      tokens: formatNumber(probe.max_tokens, locale),
    })}`
  }
  return probe.error ? ` · ${translateRunMessage(probe.error, t)}` : ''
}

// translateRunMessage translates the fixed sentences the gateway and the SDK
// emit in known situations, and keeps provider or scoring internals verbatim.
function translateRunMessage(
  message: string,
  t: (key: string, options?: Record<string, unknown>) => string
) {
  if ((RUN_STOP_REASONS as readonly string[]).includes(message)) {
    return t(message)
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

// A fingerprint check needs channel operate rights. Reading the live identity
// here means a demotion, a permission edit or an account switch never keeps an
// earlier admin's channel names, models or running job on screen, because the
// console below is keyed by exactly those facts and is unmounted when they
// change.
function fingerprintAccessKey(
  user: ReturnType<typeof useAuthStore.getState>['auth']['user']
) {
  if (!user || user.role < ROLE.ADMIN) return null
  if (!hasPermission(user, 'channel', 'operate')) return null
  const channelGrants = JSON.stringify(
    user.permissions?.admin_permissions?.channel ?? null
  )
  return `${user.id}:${user.role}:${channelGrants}`
}

export function ChannelFingerprint() {
  const user = useAuthStore((state) => state.auth.user)
  const accessKey = fingerprintAccessKey(user)
  if (!accessKey) return null
  return <FingerprintConsole key={accessKey} identity={accessKey} />
}

function FingerprintConsole({ identity }: { identity: string }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [channelId, setChannelId] = useState('')
  const [model, setModel] = useState('')
  const [endpoint, setEndpoint] = useState<string>(ENDPOINTS[0].value)
  const [count, setCount] = useState(MAX_CHALLENGES)
  const [tokenBudget, setTokenBudget] = useState(24000)
  const [job, setJob] = useState<FingerprintJob | null>(null)
  const [startError, setStartError] = useState('')
  const [pollError, setPollError] = useState('')
  // Set when a poll fails: the run may have expired or been cancelled elsewhere,
  // so this page stops polling it and allows a new run to start.
  const [pollStopped, setPollStopped] = useState(false)
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  // Bumped by the retry button so a stopped poll starts again.
  const [pollAttempt, setPollAttempt] = useState(0)

  const channelsQuery = useQuery({
    // The dropdown is populated from the lightweight channel options endpoint,
    // which never returns every channel: it filters by keyword and pages, and
    // the current selection is passed back through `ids` so a chosen channel
    // outside the page still resolves with its models. The identity is part of
    // the key so a demotion or account switch cannot read another identity's
    // cached channel names.
    queryKey: ['fingerprint-channels', identity, channelId, keyword, page],
    queryFn: ({ signal }) =>
      getChannelOptions(
        {
          p: page,
          page_size: 30,
          ...(keyword.trim() ? { keyword: keyword.trim() } : {}),
          ...(channelId ? { ids: channelId } : {}),
        },
        signal
      ),
    // A stale page or keyword must not flash the previous result set, but an
    // account switch must not keep showing the previous identity's channels, so
    // the placeholder is only reused while the identity part of the key matches.
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === identity ? previousData : undefined,
    staleTime: 60_000,
    meta: { errorToast: false },
  })
  const optionsPage = channelsQuery.data?.data
  const channels = optionsPage?.items ?? []
  const hasMore = optionsPage?.has_more ?? false
  const selectedChannel = channels.find(
    (channel) => String(channel.id) === channelId
  )
  // A page change can drop the chosen channel from the visible list. Only the
  // channel chosen by id is remembered, so a valid model survives that reload;
  // changing channel still clears it because the channel itself is different.
  const [chosenModels, setChosenModels] = useState<string[]>([])
  useEffect(() => {
    if (!selectedChannel) return
    setChosenModels(selectedChannel.models ?? [])
  }, [selectedChannel])
  const channelModels = useMemo(() => {
    const names = selectedChannel
      ? (selectedChannel.models ?? [])
      : chosenModels
    // A repeated alias must not appear twice, and the alias text itself must
    // stay exactly as configured.
    return [...new Set(names)].map((name) => ({ label: name, value: name }))
  }, [chosenModels, selectedChannel])
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

  const startMutation = useMutation({
    mutationFn: () => {
      const name = model.trim()
      if (!channelId || !modelServable) {
        return Promise.reject(
          new Error('Choose a model offered by the channel')
        )
      }
      return startChannelFingerprint(Number(channelId), {
        model: name,
        endpoint_type: endpoint,
        count,
        token_budget: tokenBudget,
      })
    },
    onSuccess: (started) => {
      setStartError('')
      setPollError('')
      // A previous run may have stopped polling; a new run must poll again.
      setPollStopped(false)
      setJob(started)
    },
    onError: (error) => setStartError(getServerErrorMessage(error)),
  })

  const cancelMutation = useMutation({
    mutationFn: () => {
      if (!job) return Promise.reject(new Error('No check to cancel'))
      return cancelChannelFingerprint(job.channel_id, job.id)
    },
    onSuccess: (cancelled) =>
      setJob((current) => (current?.id === cancelled.id ? cancelled : current)),
  })

  const jobId = job?.id
  const jobChannelId = job?.channel_id
  const running = job?.state === 'running'
  // One query drives the poll instead of a bare interval: it never overlaps a
  // request with the next tick, and stops as soon as the run is terminal.
  const pollQuery = useQuery({
    queryKey: ['fingerprint-job', identity, jobChannelId, jobId, pollAttempt],
    queryFn: ({ signal }) =>
      getChannelFingerprint(jobChannelId as number, jobId as string, signal),
    enabled: !!running && !pollStopped && !!jobId && !!jobChannelId,
    refetchInterval: POLL_INTERVAL_MS,
    refetchIntervalInBackground: false,
    retry: false,
    meta: { errorToast: false },
  })
  useEffect(() => {
    const latest = pollQuery.data
    if (!latest) return
    setPollError('')
    setPollStopped(false)
    setJob(latest)
  }, [pollQuery.data])
  useEffect(() => {
    if (!pollQuery.isError) return
    setPollError(getServerErrorMessage(pollQuery.error))
    setPollStopped(true)
  }, [pollQuery.isError, pollQuery.error])

  const retryPoll = () => {
    setPollError('')
    setPollStopped(false)
    setPollAttempt((attempt) => attempt + 1)
  }

  const completedProbes =
    job?.probes.filter((probe) => probe.state === 'complete').length ?? 0
  const progress = job
    ? Math.round((completedProbes / job.probes.length) * 100)
    : 0
  const canStart =
    !!channelId &&
    modelServable &&
    !startMutation.isPending &&
    (!running || pollStopped)
  // Editing is allowed while a stopped run is still shown, so the operator can
  // pick another channel or model and start a fresh check.
  const controlsDisabled = (running && !pollStopped) || startMutation.isPending

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

          <Card>
            <CardHeader>
              <CardTitle>{t('Run a check')}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-channel'>{t('Channel')}</Label>
                  <Select
                    value={channelId}
                    onValueChange={(value) => {
                      setChannelId(value ?? '')
                      setModel('')
                      setChosenModels([])
                    }}
                    disabled={controlsDisabled}
                  >
                    <SelectTrigger id='fingerprint-channel'>
                      <SelectValue placeholder={t('Select a channel')} />
                    </SelectTrigger>
                    <SelectContent>
                      {channels.map((channel) => (
                        <SelectItem key={channel.id} value={String(channel.id)}>
                          {`#${channel.id} ${channel.name}`}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {/* The endpoint returns one bounded page per keyword, so the
                    search box narrows the result set and paging walks it. */}
                  <Input
                    id='fingerprint-channel-search'
                    value={keyword}
                    onChange={(event) => {
                      setKeyword(event.target.value)
                      setPage(1)
                    }}
                    placeholder={t('Search channels')}
                    disabled={controlsDisabled}
                  />
                  <div className='flex items-center gap-2'>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        setPage((current) => Math.max(1, current - 1))
                      }
                      disabled={controlsDisabled || page <= 1}
                    >
                      {t('Previous')}
                    </Button>
                    <span className='text-muted-foreground text-xs tabular-nums'>
                      {page}
                    </span>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => setPage((current) => current + 1)}
                      disabled={
                        controlsDisabled || !hasMore || channelsQuery.isFetching
                      }
                    >
                      {t('Next')}
                    </Button>
                  </div>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-model'>{t('Model')}</Label>
                  <ModelSelector
                    triggerId='fingerprint-model'
                    className='w-full justify-start'
                    selectedModel={model}
                    models={channelModels}
                    onModelChange={setModel}
                    disabled={controlsDisabled || !channelId}
                  />
                  {!channelId && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Choose a channel to list its models')}
                    </p>
                  )}
                  {!!channelId && !channelModels.length && (
                    <p className='text-muted-foreground text-xs'>
                      {t('This channel declares no models')}
                    </p>
                  )}
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-endpoint'>{t('Endpoint')}</Label>
                  <Select
                    value={endpoint}
                    onValueChange={(value) =>
                      setEndpoint(value ?? ENDPOINTS[0].value)
                    }
                    disabled={controlsDisabled}
                  >
                    <SelectTrigger id='fingerprint-endpoint'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {ENDPOINTS.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-count'>{t('Challenges')}</Label>
                  <Input
                    id='fingerprint-count'
                    type='number'
                    min={1}
                    max={MAX_CHALLENGES}
                    value={count}
                    onChange={(event) =>
                      setCount(
                        Math.min(
                          MAX_CHALLENGES,
                          Math.max(1, Number(event.target.value) || 1)
                        )
                      )
                    }
                    disabled={controlsDisabled}
                  />
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-budget'>
                    {t('Estimated token budget')}
                  </Label>
                  <Input
                    id='fingerprint-budget'
                    type='number'
                    min={1000}
                    step={1000}
                    value={tokenBudget}
                    onChange={(event) =>
                      setTokenBudget(
                        Math.max(1000, Number(event.target.value) || 1000)
                      )
                    }
                    disabled={controlsDisabled}
                  />
                </div>
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Each challenge is one request whose output is hard-limited to at most {{tokens}} tokens, narrowed to the remaining budget. The run stops once reported or estimated usage reaches the budget. Input tokens are estimated, and a channel can inject prompts the gateway cannot see, so the real total can exceed the budget.',
                  { tokens: 4096 }
                )}
              </p>
              {startError && (
                <p role='alert' className='text-destructive text-sm'>
                  {startError}
                </p>
              )}
              <div className='flex flex-wrap gap-2'>
                <Button
                  onClick={() => startMutation.mutate()}
                  disabled={!canStart}
                >
                  {startMutation.isPending ? (
                    <Loader2 className='animate-spin' aria-hidden='true' />
                  ) : (
                    <Play aria-hidden='true' />
                  )}
                  {t('Start check')}
                </Button>
                {running && job && (
                  <Button
                    variant='outline'
                    onClick={() => cancelMutation.mutate()}
                    disabled={cancelMutation.isPending}
                  >
                    <X aria-hidden='true' />
                    {t('Cancel')}
                  </Button>
                )}
              </div>
            </CardContent>
          </Card>

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
                  <Progress value={progress} />
                  <p className='text-muted-foreground text-xs'>
                    {t('{{done}} of {{total}} challenges complete', {
                      done: completedProbes,
                      total: job.probes.length,
                    })}
                    {' · '}
                    {t('{{used}} of {{budget}} tokens used', {
                      used: formatNumber(job.tokens_used, locale),
                      budget: formatNumber(job.token_budget, locale),
                    })}
                    {' · '}
                    {t('{{used}} of {{budget}} requests sent', {
                      used: job.requests_used,
                      budget: job.request_budget,
                    })}
                  </p>
                </div>
                {pollError && (
                  <div
                    role='alert'
                    className='flex flex-wrap items-center gap-2'
                  >
                    <p className='text-destructive text-sm'>{pollError}</p>
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
                {/* One card per requested challenge. The slots are fixed at the
                  maximum challenge count, so a run that used fewer probes
                  still shows every slot with an explicit "not run" state and
                  never implies a probe that did not happen. */}
                <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
                  {Array.from({ length: PROBE_SLOT_COUNT }, (_, index) => {
                    const probe = job.probes[index]
                    if (!probe) {
                      return (
                        <Card key={`probe-slot-${index}`} data-probe-slot='unused'>
                          <CardHeader className='pb-2'>
                            <CardTitle className='text-sm'>
                              {t('Challenge {{index}}', { index: index + 1 })}
                            </CardTitle>
                          </CardHeader>
                          <CardContent>
                            <p className='text-muted-foreground text-xs'>
                              {t('Not run')}
                            </p>
                          </CardContent>
                        </Card>
                      )
                    }
                    return (
                      <Card key={probe.id || `probe-slot-${index}`} data-probe-slot='probe'>
                        <CardHeader className='pb-2'>
                          <CardTitle className='text-sm'>
                            {t('Challenge {{index}}', { index: index + 1 })}
                          </CardTitle>
                          <div className='flex items-center gap-2'>
                            <StatusBadge
                              label={t(STATE_LABELS[probe.state] ?? 'Unknown')}
                              variant={
                                probe.state === 'complete'
                                  ? 'success'
                                  : probe.state === 'failed'
                                    ? 'danger'
                                    : probe.state === 'cancelled'
                                      ? 'neutral'
                                      : 'info'
                              }
                              size='sm'
                              copyable={false}
                            />
                          </div>
                        </CardHeader>
                        <CardContent className='space-y-3'>
                          <div className='space-y-1'>
                            <p className='text-muted-foreground text-xs font-medium'>
                              {t('Prompt')}
                            </p>
                            <p className='max-h-32 overflow-y-auto text-xs break-words whitespace-pre-wrap'>
                              {probe.prompt || '-'}
                            </p>
                            {probe.prompt_truncated && (
                              <p className='text-muted-foreground text-xs'>
                                {t('Truncated')}
                              </p>
                            )}
                          </div>
                          <div className='space-y-1'>
                            <p className='text-muted-foreground text-xs font-medium'>
                              {t('Output')}
                            </p>
                            <p className='max-h-32 overflow-y-auto text-xs break-words whitespace-pre-wrap'>
                              {probe.output || '-'}
                            </p>
                            {probe.output_truncated && (
                              <p className='text-muted-foreground text-xs'>
                                {t('Truncated')}
                              </p>
                            )}
                          </div>
                          <p className='text-muted-foreground text-xs'>
                            {t(STATE_LABELS[probe.state] ?? 'Unknown')}
                            {probeSummary(probe, t, locale)}
                          </p>
                        </CardContent>
                      </Card>
                    )
                  })}
                </div>
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

          {channelsQuery.isError && (
            <ErrorState
              description={getServerErrorMessage(channelsQuery.error)}
            />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
