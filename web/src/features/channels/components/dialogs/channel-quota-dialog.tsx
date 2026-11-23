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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, RefreshCw, Wallet } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerMessage,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getChannelBudget,
  resetChannelBudget,
  setChannelBudget,
} from '../../api'
import { channelsQueryKeys } from '../../lib'
import {
  isQuotaConflict,
  newQuotaOperationId,
  parseLocalQuotaLimit,
} from '../../lib/quota-operation'
import type { ChannelBudgetView } from '../../types'
import { useChannels } from '../channels-provider'

function formatQuota(value: number | null | undefined, locale?: string) {
  if (value == null) return '-'
  return formatQuotaWithCurrency(value, {
    digitsLarge: 2,
    digitsSmall: 4,
    locale,
  })
}

type ChannelQuotaDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * The channel's local billed budget. It is deliberately separate from the
 * upstream balance dialog: a provider without a balance endpoint still gets a
 * local limit, and refreshing upstream data never touches this ledger.
 */
export function ChannelQuotaDialog(props: ChannelQuotaDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { currentRow } = useChannels()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const canOperate = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )

  const channelId = currentRow?.id
  const budgetQuery = useQuery({
    queryKey: channelsQueryKeys.budget(channelId),
    queryFn: ({ signal }) => getChannelBudget(channelId ?? 0, signal),
    enabled: props.open && channelId != null,
    staleTime: 0,
    retry: false,
    meta: { errorToast: false },
  })

  const [draft, setDraft] = useState<{
    enabled: boolean
    limit: string
    version: number
  } | null>(null)
  const [isSaving, setIsSaving] = useState(false)
  const [resetTarget, setResetTarget] = useState<{
    version: number
    operationId: string
  } | null>(null)
  const [isResetting, setIsResetting] = useState(false)

  if (!currentRow) return null

  const view = budgetQuery.data
  const enabled = draft?.enabled ?? view?.enabled ?? false
  const limitText = draft?.limit ?? String(view?.limit_quota ?? 0)
  const draftLimit = enabled ? parseLocalQuotaLimit(limitText) : 0
  const canSubmit =
    canOperate &&
    !!view?.ledger_enabled &&
    draft != null &&
    draftLimit != null &&
    !budgetQuery.isFetching &&
    !isSaving &&
    !isResetting &&
    (enabled !== view.enabled ||
      draftLimit !== view.limit_quota ||
      draft.version !== view.config_version)

  const updateDraft = (
    values: Partial<Pick<NonNullable<typeof draft>, 'enabled' | 'limit'>>
  ) => {
    if (!view) return
    setDraft((previous) => ({
      ...(previous ?? {
        enabled: view.enabled,
        limit: String(view.limit_quota),
        version: view.config_version,
      }),
      ...values,
    }))
  }

  const refresh = async () => {
    await budgetQuery.refetch()
    setDraft(null)
  }

  const invalidateAfterChange = () => {
    void queryClient.invalidateQueries({
      queryKey: channelsQueryKeys.budgets(),
    })
  }

  const save = async () => {
    if (!canSubmit || !draft || draftLimit == null) return
    setIsSaving(true)
    try {
      const response = await setChannelBudget(currentRow.id, {
        enabled,
        limit_quota: draftLimit,
        expected_config_version: draft.version,
      })
      if (response.success) {
        toast.success(getServerMessage(response) || t('Local budget saved'))
        if (response.data) {
          queryClient.setQueryData(
            channelsQueryKeys.budget(currentRow.id),
            response.data
          )
        }
        setDraft(null)
        invalidateAfterChange()
        return
      }
      if (isQuotaConflict(response)) {
        await refresh()
        toast.error(t('The budget changed meanwhile. Reloaded values.'))
        return
      }
      handleServerError(response, t('Failed to save the local budget'))
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        await refresh()
        toast.error(t('The budget changed meanwhile. Reloaded values.'))
      } else {
        handleServerError(error, t('Failed to save the local budget'))
      }
    } finally {
      setIsSaving(false)
    }
  }

  const reset = async () => {
    if (
      !resetTarget ||
      !view?.ledger_enabled ||
      !canOperate ||
      isSaving ||
      isResetting
    ) {
      return
    }
    setIsResetting(true)
    try {
      const response = await resetChannelBudget(currentRow.id, {
        expected_config_version: resetTarget.version,
        operation_id: resetTarget.operationId,
      })
      if (response.success) {
        toast.success(getServerMessage(response) || t('Used amount reset'))
        // A replay returns its original result, not the current live counters.
        setResetTarget(null)
        await queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.budgets(),
        })
        setDraft(null)
        return
      }
      if (isQuotaConflict(response)) {
        setResetTarget(null)
        await refresh()
        toast.error(t('The budget changed meanwhile. Reloaded values.'))
        return
      }
      handleServerError(response, t('Failed to reset the used amount'))
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        setResetTarget(null)
        await refresh()
        toast.error(t('The budget changed meanwhile. Reloaded values.'))
      } else {
        handleServerError(error, t('Failed to reset the used amount'))
      }
    } finally {
      setIsResetting(false)
    }
  }

  let body = (
    <p className='text-muted-foreground text-sm'>
      {t('Local budget is not available for this channel yet.')}
    </p>
  )

  if (budgetQuery.isPending) {
    body = <LoadingState size='sm' className='min-h-20' />
  } else if (budgetQuery.isError) {
    body = (
      <ErrorState
        className='min-h-0 py-3'
        title={t('Failed to load the local budget')}
        description={getServerErrorMessage(budgetQuery.error, '') || undefined}
        onRetry={() => void refresh()}
      />
    )
  } else if (view && !view.ledger_enabled) {
    body = (
      <p role='status' className='text-muted-foreground text-sm'>
        {t('Channel budget accounting is not enabled')}
      </p>
    )
  } else if (view) {
    const startedAt = view.accounting_started_at
    body = (
      <div className='space-y-4 py-1'>
        <div className='bg-muted/40 min-w-0 space-y-3 rounded-lg border p-3'>
          <div className='flex flex-wrap items-center gap-2'>
            <StatusBadge
              label={view.enabled ? t('Limit enforced') : t('Unlimited')}
              variant={view.enabled ? 'success' : 'neutral'}
              size='sm'
              copyable={false}
            />
            {view.enabled && view.exhausted && (
              <StatusBadge
                label={t('Exhausted')}
                variant='red'
                size='sm'
                copyable={false}
                showDot
              />
            )}
          </div>
          <BudgetFacts view={view} locale={locale} />
          <p className='text-muted-foreground text-xs'>
            {t('Accounting starts at:')}{' '}
            {startedAt > 0
              ? formatTimestampToDate(startedAt)
              : t('Not started')}
          </p>
        </div>

        <div className='min-w-0 space-y-3'>
          <div className='flex items-center justify-between gap-3'>
            <Label
              htmlFor='channel-budget-enabled'
              className='text-sm font-medium'
            >
              {t('Enforce a local limit')}
            </Label>
            <Switch
              id='channel-budget-enabled'
              checked={enabled}
              onCheckedChange={(checked) => updateDraft({ enabled: checked })}
              disabled={!canOperate || isSaving || isResetting}
            />
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Requests skip this channel once its local budget is used up. Upstream balance and user charging are unaffected.'
            )}
          </p>

          <div className='space-y-1.5'>
            <Label htmlFor='channel-budget-limit' className='text-sm'>
              {t('Local limit (site quota units)')}
            </Label>
            <Input
              id='channel-budget-limit'
              inputMode='numeric'
              value={enabled ? limitText : '0'}
              onChange={(event) => updateDraft({ limit: event.target.value })}
              disabled={!canOperate || isSaving || isResetting || !enabled}
              aria-invalid={draftLimit == null}
              aria-describedby='channel-budget-limit-hint'
            />
            <p
              id='channel-budget-limit-hint'
              className='text-muted-foreground text-xs'
            >
              {draftLimit == null
                ? t('Enter a whole, non-negative number.')
                : t('Display value: {{amount}}', {
                    amount: formatQuota(draftLimit, locale),
                  })}
            </p>
          </div>

          <div className='flex flex-wrap items-center gap-2'>
            <Button onClick={() => void save()} disabled={!canSubmit}>
              {isSaving && <Loader2 className='mr-2 size-4 animate-spin' />}
              {t('Save')}
            </Button>
            <Button
              variant='outline'
              onClick={() =>
                setResetTarget({
                  version: view.config_version,
                  operationId: newQuotaOperationId(),
                })
              }
              disabled={
                !canOperate ||
                budgetQuery.isFetching ||
                view.period_used_quota <= 0 ||
                isSaving ||
                isResetting
              }
            >
              <RefreshCw className='mr-2 size-4' />
              {t('Reset used amount')}
            </Button>
          </div>
          {!canOperate && (
            <p className='text-muted-foreground text-xs'>
              {t('No permission to perform this action')}
            </p>
          )}
        </div>
      </div>
    )
  }

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={(open) => {
          if (!isSaving && !isResetting) props.onOpenChange(open)
        }}
        title={
          <>
            {t('Local Budget')}
            <StatusBadge
              label={currentRow.name}
              variant='neutral'
              copyable={false}
            />
          </>
        }
        titleClassName='flex flex-wrap items-center gap-2'
        description={t(
          'A local billed budget for this channel, separate from the upstream balance.'
        )}
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={isSaving || isResetting}
          >
            {t('Close')}
          </Button>
        }
      >
        <div className='flex items-start gap-2 rounded-md border border-dashed p-3'>
          <Wallet
            className='text-muted-foreground mt-0.5 size-4 shrink-0'
            aria-hidden='true'
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Wallet or subscription charges and API token quotas still apply.'
            )}
          </p>
        </div>
        {body}
      </Dialog>

      <ConfirmDialog
        open={resetTarget !== null}
        onOpenChange={(open) => {
          if (!open && !isResetting) setResetTarget(null)
        }}
        title={t('Reset used amount')}
        desc={t(
          'Reset current-period usage without changing live holds, lifetime usage or user charges.'
        )}
        confirmText={t('Reset')}
        isLoading={isResetting}
        handleConfirm={() => void reset()}
      />
    </>
  )
}

function BudgetFacts(props: { view: ChannelBudgetView; locale?: string }) {
  const { t } = useTranslation()
  const view = props.view
  const facts = [
    {
      label: t('Limit'),
      value: view.enabled ? formatQuota(view.limit_quota, props.locale) : '-',
    },
    {
      label: t('Used this period'),
      value: formatQuota(view.period_used_quota, props.locale),
    },
    {
      label: t('Reserved'),
      value: formatQuota(view.reserved_quota, props.locale),
    },
    {
      label: t('Remaining'),
      value: view.enabled
        ? formatQuota(view.remaining_quota, props.locale)
        : t('Unlimited'),
    },
    {
      label: t('Lifetime used'),
      value: formatQuota(view.lifetime_used_quota, props.locale),
    },
  ]
  return (
    <dl className='grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3'>
      {facts.map((fact) => (
        <div key={fact.label} className='min-w-0'>
          <dt className='text-muted-foreground truncate text-[11px]'>
            {fact.label}
          </dt>
          <dd className='truncate font-mono text-sm tabular-nums'>
            {fact.value}
          </dd>
        </div>
      ))}
    </dl>
  )
}
