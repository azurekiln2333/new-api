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
import { searchChannels } from '@/features/channels/api'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

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

export function ChannelFingerprint() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [channelId, setChannelId] = useState('')
  const [model, setModel] = useState('')
  const [endpoint, setEndpoint] = useState<string>(ENDPOINTS[0].value)
  const [count, setCount] = useState(1)
  const [tokenBudget, setTokenBudget] = useState(24000)
  const [job, setJob] = useState<FingerprintJob | null>(null)
  const [startError, setStartError] = useState('')
  const [pollError, setPollError] = useState('')
  // Set when a poll fails: the run may have expired or been cancelled elsewhere,
  // so this page stops polling it and allows a new run to start.
  const [pollStopped, setPollStopped] = useState(false)
  const [channelKeyword, setChannelKeyword] = useState('')
  const [debouncedKeyword, setDebouncedKeyword] = useState('')
  // Bumped by the retry button so a stopped poll starts again.
  const [pollAttempt, setPollAttempt] = useState(0)

  useEffect(() => {
    const timer = window.setTimeout(
      () => setDebouncedKeyword(channelKeyword.trim()),
      300
    )
    return () => window.clearTimeout(timer)
  }, [channelKeyword])

  const changeChannelKeyword = (value: string) => {
    setChannelKeyword(value)
    // A filtered list may not contain the current channel any more, so the
    // selection is cleared instead of pointing at an option that is gone.
    setChannelId('')
    setModel('')
  }

  const channelsQuery = useQuery({
    // Search uses the existing channel search endpoint, so a deployment with
    // more channels than one page still allows selecting any of them.
    queryKey: ['fingerprint-channels', debouncedKeyword],
    queryFn: () =>
      searchChannels({ keyword: debouncedKeyword, p: 1, page_size: 30 }),
    staleTime: 60_000,
    meta: { errorToast: false },
  })
  const channels = channelsQuery.data?.data?.items ?? []
  const selectedChannel = channels.find(
    (channel) => String(channel.id) === channelId
  )
  const channelModels = useMemo(
    () =>
      (selectedChannel?.models ?? '')
        .split(',')
        .map((name) => name.trim())
        .filter(Boolean),
    [selectedChannel?.models]
  )

  const startMutation = useMutation({
    mutationFn: () =>
      startChannelFingerprint(Number(channelId), {
        model: model.trim(),
        endpoint_type: endpoint,
        count,
        token_budget: tokenBudget,
      }),
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
    queryKey: ['fingerprint-job', jobChannelId, jobId, pollAttempt],
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
    !!model.trim() &&
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
                  <Input
                    aria-label={t('Search channels')}
                    value={channelKeyword}
                    placeholder={t('Search channels')}
                    onChange={(event) =>
                      changeChannelKeyword(event.target.value)
                    }
                    disabled={controlsDisabled}
                  />
                  <Select
                    value={channelId}
                    onValueChange={(value) => {
                      setChannelId(value ?? '')
                      setModel('')
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
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='fingerprint-model'>{t('Model')}</Label>
                  <Input
                    id='fingerprint-model'
                    list='fingerprint-channel-models'
                    value={model}
                    placeholder={t('Model name')}
                    onChange={(event) => setModel(event.target.value)}
                    disabled={controlsDisabled}
                  />
                  <datalist id='fingerprint-channel-models'>
                    {channelModels.map((name) => (
                      <option key={name} value={name} />
                    ))}
                  </datalist>
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
                <ul className='divide-border divide-y'>
                  {job.probes.map((probe, index) => (
                    <li
                      key={probe.id || index}
                      className='flex flex-wrap items-center justify-between gap-2 py-2 text-sm'
                    >
                      <span className='min-w-0 break-words'>
                        {t('Challenge {{index}}', { index: index + 1 })}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {t(STATE_LABELS[probe.state] ?? 'Unknown')}
                        {probeSummary(probe, t, locale)}
                      </span>
                    </li>
                  ))}
                </ul>
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
