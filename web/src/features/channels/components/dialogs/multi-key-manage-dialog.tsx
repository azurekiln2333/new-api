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
import {
  Loader2,
  RefreshCw,
  Trash2,
  Power,
  PowerOff,
  CloudDownload,
  Layers,
  Plus,
} from 'lucide-react'
import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toIntlLocale } from '@/i18n/languages'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerMessage,
  requireServerSuccess,
  translateServerText,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getChannel,
  getMultiKeyStatus,
  getChannelBudget,
  enableMultiKey,
  disableMultiKey,
  deleteMultiKey,
  enableAllMultiKeys,
  disableAllMultiKeys,
  deleteDisabledMultiKeys,
  deduplicateMultiKeys,
  resetMultiKeyQuota,
  setMultiKeyModels,
  setMultiKeyQuota,
  setMultiKeyUser,
  syncMultiKeyModels,
  updateChannel,
} from '../../api'
import {
  CHANNEL_TYPE_TASK_PLUGIN,
  MULTI_KEY_FILTER_OPTIONS,
} from '../../constants'
import {
  canAppendStaticAllocation,
  channelsQueryKeys,
  formatTimestamp,
  getMultiKeyStatusConfig,
  getMultiKeyConfirmMessage,
  isDestructiveAction,
  parseModelsList,
} from '../../lib'
import { formatChannelBalanceSummary } from '../../lib/channel-balance'
import { isQuotaConflict, newQuotaOperationId } from '../../lib/quota-operation'
import type {
  KeyStatus,
  MultiKeyConfirmAction,
  MultiKeyDedupResult,
  MultiKeyModelSyncResult,
} from '../../types'
import { ChannelBalanceDetails } from '../channel-balance-details'
import { useChannels } from '../channels-provider'
import { MultiKeyDedupDialog } from './multi-key-dedup-dialog'
import { MultiKeyModelsEditor } from './multi-key-models-editor'
import { MultiKeyQuotaEditor } from './multi-key-quota-editor'
import { StatisticsCard } from './multi-key-statistics-card'
import { MultiKeySyncDialog } from './multi-key-sync-dialog'
import { MultiKeyTableRowActions } from './multi-key-table-row-actions'

// The table scrolls inside its own region, so the header sticks to that region
// and the actions column stays reachable without scrolling horizontally.
const stickyHeadClassName = 'sticky top-0 z-10 bg-popover'
const stickyActionsHeadClassName = `${stickyHeadClassName} right-0 z-20 text-right`
const stickyActionsCellClassName =
  'sticky right-0 z-10 w-auto max-w-none overflow-visible bg-popover text-right shadow-[-8px_0_10px_-10px_hsl(var(--foreground))] group-hover:[background-color:color-mix(in_oklch,var(--muted)_50%,var(--popover))]'

type MultiKeyManageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function MultiKeyManageDialog({
  open,
  onOpenChange,
}: MultiKeyManageDialogProps) {
  const { t, i18n } = useTranslation()
  const { currentRow, setCurrentRow } = useChannels()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  // A plugin channel queries the balance of each key.
  const keyBalances =
    currentRow?.type === CHANNEL_TYPE_TASK_PLUGIN
      ? currentRow.balance_detail?.keys
      : undefined
  const queryClient = useQueryClient()
  const singleKey = currentRow?.channel_info?.is_multi_key !== true
  const currentUser = useAuthStore((s) => s.auth.user)
  const canOperate = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.OPERATE
  )
  const canEditSensitive =
    canOperate &&
    hasPermission(
      currentUser,
      ADMIN_PERMISSION_RESOURCES.CHANNEL,
      ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
    )
  const canAppend =
    !!currentRow &&
    canEditSensitive &&
    canAppendStaticAllocation(currentRow) &&
    hasPermission(
      currentUser,
      ADMIN_PERMISSION_RESOURCES.CHANNEL,
      ADMIN_PERMISSION_ACTIONS.WRITE
    )
  const singleBudget = useQuery({
    queryKey: channelsQueryKeys.budget(currentRow?.id),
    queryFn: ({ signal }) => getChannelBudget(currentRow?.id ?? 0, signal),
    enabled: open && singleKey && canAppend,
    staleTime: 0,
    retry: false,
    meta: { errorToast: false },
  })

  // Data state
  const [isLoading, setIsLoading] = useState(false)
  const [keys, setKeys] = useState<KeyStatus[]>([])
  const [revision, setRevision] = useState<number | undefined>()
  const [quotaSupported, setQuotaSupported] = useState(false)
  const [statusError, setStatusError] = useState<string | null>(null)
  const [currentPage, setCurrentPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [total, setTotal] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [enabledCount, setEnabledCount] = useState(0)
  const [manualDisabledCount, setManualDisabledCount] = useState(0)
  const [autoDisabledCount, setAutoDisabledCount] = useState(0)

  // UI state
  const [statusFilter, setStatusFilter] = useState<number | null>(null)
  const [confirmAction, setConfirmAction] =
    useState<MultiKeyConfirmAction | null>(null)
  const [isPerformingAction, setIsPerformingAction] = useState(false)
  // A confirmed action reloads the table, which can remove the button that
  // asked for it. Focus then comes back into this dialog rather than to
  // whatever opened the dialog, so the next Escape closes only this dialog.
  const confirmOpenerRef = useRef<HTMLElement | null>(null)
  const bodyRef = useRef<HTMLDivElement>(null)
  const statusRequest = useRef(0)

  const openConfirm = (action: MultiKeyConfirmAction) => {
    const opener = document.activeElement
    confirmOpenerRef.current = opener instanceof HTMLElement ? opener : null
    setConfirmAction({ ...action, expectedRevision: revision })
  }

  const getConfirmFinalFocus = () => {
    const opener = confirmOpenerRef.current
    return opener && bodyRef.current?.contains(opener)
      ? opener
      : bodyRef.current
  }

  // Per-key model scope, user binding and budget editors
  const [modelsEditor, setModelsEditor] = useState<{
    keyStatus: KeyStatus
    models: string[]
  } | null>(null)
  const [userEditor, setUserEditor] = useState<{
    keyStatus: KeyStatus
    userId: string
  } | null>(null)
  const [unbindTarget, setUnbindTarget] = useState<KeyStatus | null>(null)
  const [isSavingAssignment, setIsSavingAssignment] = useState(false)

  // Per-key budget: editing a limit and resetting the used amount are
  // deliberately separate operations with different confirmation copy.
  const [quotaEditor, setQuotaEditor] = useState<KeyStatus | null>(null)
  const [isSavingQuota, setIsSavingQuota] = useState(false)
  const [resetQuotaTarget, setResetQuotaTarget] = useState<KeyStatus | null>(
    null
  )
  const [isResettingQuota, setIsResettingQuota] = useState(false)
  const [quotaResetOperationId, setQuotaResetOperationId] = useState('')

  // Explicit dedup: preview first, commit only on confirmation.
  const [dedupOpen, setDedupOpen] = useState(false)
  const [dedupPreview, setDedupPreview] = useState<MultiKeyDedupResult | null>(
    null
  )
  const [isDedupFetching, setIsDedupFetching] = useState(false)
  const [isDedupApplying, setIsDedupApplying] = useState(false)
  const [dedupError, setDedupError] = useState<string | null>(null)

  // Upstream model sync preview
  const [syncOpen, setSyncOpen] = useState(false)
  const [isSyncing, setIsSyncing] = useState(false)
  const [isApplyingSync, setIsApplyingSync] = useState(false)
  const [syncPreview, setSyncPreview] =
    useState<MultiKeyModelSyncResult | null>(null)
  const [syncError, setSyncError] = useState<string | null>(null)

  const channelModels = parseModelsList(currentRow?.models ?? '')
  const [appendOpen, setAppendOpen] = useState(false)
  const [appendKey, setAppendKey] = useState('')
  const [isAppending, setIsAppending] = useState(false)
  const ledgerEnabled = singleKey
    ? singleBudget.data?.ledger_enabled === true &&
      !singleBudget.isFetching &&
      !singleBudget.isError
    : quotaSupported
  const canSubmitAppend =
    canAppend &&
    ledgerEnabled &&
    (singleKey || revision != null) &&
    !!appendKey.trim() &&
    !isAppending
  const mutationInProgress =
    isPerformingAction ||
    isSavingAssignment ||
    isSavingQuota ||
    isResettingQuota ||
    isApplyingSync ||
    isDedupApplying ||
    isAppending
  const parsedUserId = Number(userEditor?.userId ?? '')
  const userIdValid =
    /^\d+$/.test(userEditor?.userId ?? '') &&
    Number.isSafeInteger(parsedUserId) &&
    parsedUserId > 0

  // Reset and load data when dialog opens
  useEffect(() => {
    setAppendKey('')
    if (open && currentRow && !singleKey) {
      setKeys([])
      setTotal(0)
      setTotalPages(0)
      setEnabledCount(0)
      setManualDisabledCount(0)
      setAutoDisabledCount(0)
      setRevision(undefined)
      setQuotaSupported(false)
      setModelsEditor(null)
      setUserEditor(null)
      setQuotaEditor(null)
      setResetQuotaTarget(null)
      setConfirmAction(null)
      setUnbindTarget(null)
      setDedupOpen(false)
      setSyncOpen(false)
      setAppendOpen(false)
      setCurrentPage(1)
      setStatusFilter(null)
      void loadKeyStatus(1, pageSize, null)
    }
    return () => {
      statusRequest.current += 1
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentRow?.id, singleKey])

  const loadKeyStatus = async (
    page: number = currentPage,
    size: number = pageSize,
    status: number | null = statusFilter
  ) => {
    if (!currentRow) return

    const requestId = ++statusRequest.current
    setIsLoading(true)
    setStatusError(null)
    try {
      const response = requireServerSuccess(
        await getMultiKeyStatus(
          currentRow.id,
          page,
          size,
          status === null ? undefined : status
        )
      )

      if (requestId !== statusRequest.current) return
      if (response.data) {
        const rows = response.data.keys ?? []
        setRevision(response.data.revision)
        setQuotaSupported(
          response.data.quota_supported ??
            rows.some((key) => key.quota?.ledger_enabled === true)
        )
        setKeys(rows)
        setTotal(response.data.total || 0)
        setCurrentPage(response.data.page || 1)
        setPageSize(response.data.page_size || 10)
        setTotalPages(response.data.total_pages || 0)
        setEnabledCount(response.data.enabled_count || 0)
        setManualDisabledCount(response.data.manual_disabled_count || 0)
        setAutoDisabledCount(response.data.auto_disabled_count || 0)
      } else {
        setStatusError(t('Failed to load key status'))
      }
    } catch (error: unknown) {
      if (requestId === statusRequest.current) {
        setStatusError(
          getServerErrorMessage(error, t('Failed to load key status'))
        )
      }
    } finally {
      if (requestId === statusRequest.current) setIsLoading(false)
    }
  }

  const reloadAfterConflict = async () => {
    setModelsEditor(null)
    setUserEditor(null)
    setQuotaEditor(null)
    setResetQuotaTarget(null)
    await loadKeyStatus()
    toast.error(t('Channel key configuration changed, reload and retry'))
  }

  const handleStatusFilterChange = (value: string) => {
    const newFilter = value === 'all' ? null : Number.parseInt(value)
    setStatusFilter(newFilter)
    setCurrentPage(1)
    loadKeyStatus(1, pageSize, newFilter)
  }

  const handlePageChange = (newPage: number) => {
    setCurrentPage(newPage)
    loadKeyStatus(newPage, pageSize)
  }

  const performAction = async () => {
    if (!confirmAction || !currentRow || !canOperate || isPerformingAction) {
      return
    }
    if (
      !canEditSensitive &&
      (confirmAction.type === 'delete' ||
        confirmAction.type === 'delete-disabled')
    ) {
      setConfirmAction(null)
      return
    }

    setIsPerformingAction(true)
    try {
      const { type, keyIndex, keyEntryId, expectedRev, expectedRevision } =
        confirmAction
      let response

      // Execute the appropriate action
      if (type === 'enable' && keyIndex !== undefined) {
        response = await enableMultiKey(
          currentRow.id,
          keyIndex,
          keyEntryId,
          expectedRev
        )
      } else if (type === 'disable' && keyIndex !== undefined) {
        response = await disableMultiKey(
          currentRow.id,
          keyIndex,
          keyEntryId,
          expectedRev
        )
      } else if (type === 'delete' && keyIndex !== undefined) {
        response = await deleteMultiKey(
          currentRow.id,
          keyIndex,
          keyEntryId,
          expectedRev
        )
      } else if (type === 'enable-all') {
        response = await enableAllMultiKeys(currentRow.id, expectedRevision)
      } else if (type === 'disable-all') {
        response = await disableAllMultiKeys(currentRow.id, expectedRevision)
      } else if (type === 'delete-disabled') {
        response = await deleteDisabledMultiKeys(
          currentRow.id,
          expectedRevision
        )
      }

      if (response?.success) {
        toast.success(getServerMessage(response) || t('Operation successful'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        // A plugin channel's edit drawer lists the same lines beside this dialog.
        queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.pluginCredentials(currentRow.id),
        })

        // Reload data - reset to page 1 for bulk actions
        const isBulkAction = type.includes('all') || type === 'delete-disabled'
        if (isBulkAction) {
          setCurrentPage(1)
          loadKeyStatus(1, pageSize)
        } else {
          loadKeyStatus(currentPage, pageSize)
        }
      } else if (isQuotaConflict(response)) {
        await reloadAfterConflict()
      } else {
        handleServerError(response, t('Operation failed'))
      }
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        await reloadAfterConflict()
      } else {
        handleServerError(error, t('Operation failed'))
      }
    } finally {
      setIsPerformingAction(false)
      setConfirmAction(null)
    }
  }

  // Owner and model-scope changes both rebuild abilities on the server.
  const saveKeyAssignment = async (
    request: () => Promise<{ success: boolean; message?: string }>
  ) => {
    if (!canEditSensitive || isSavingAssignment) return
    setIsSavingAssignment(true)
    try {
      const response = await request()
      if (response.success) {
        toast.success(getServerMessage(response) || t('Operation successful'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        setModelsEditor(null)
        setUserEditor(null)
        await loadKeyStatus()
      } else if (isQuotaConflict(response)) {
        await reloadAfterConflict()
      } else {
        handleServerError(response, t('Operation failed'))
      }
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        await reloadAfterConflict()
      } else {
        handleServerError(error, t('Operation failed'))
      }
    } finally {
      setIsSavingAssignment(false)
    }
  }

  const saveModels = (models: string[], scopeMode: 'all' | 'allowlist') => {
    if (!modelsEditor || !currentRow) return
    const keyStatus = modelsEditor.keyStatus
    void saveKeyAssignment(() =>
      setMultiKeyModels(
        currentRow.id,
        keyStatus.index,
        models,
        keyStatus.id,
        keyStatus.rev,
        scopeMode
      )
    )
  }

  const saveUser = () => {
    if (!userEditor || !currentRow || !userIdValid) return
    const keyStatus = userEditor.keyStatus
    void saveKeyAssignment(() =>
      setMultiKeyUser(
        currentRow.id,
        keyStatus.index,
        parsedUserId,
        keyStatus.id,
        keyStatus.rev
      )
    )
  }

  const unbindUser = async () => {
    if (!unbindTarget || !currentRow) return
    const keyStatus = unbindTarget
    setUnbindTarget(null)
    await saveKeyAssignment(() =>
      setMultiKeyUser(
        currentRow.id,
        keyStatus.index,
        0,
        keyStatus.id,
        keyStatus.rev
      )
    )
  }

  // Preview which models every enabled key serves before writing anything.
  const openSync = async () => {
    if (!currentRow || !canEditSensitive || isSyncing) return
    setSyncOpen(true)
    setSyncPreview(null)
    setSyncError(null)
    setIsSyncing(true)
    try {
      const response = await syncMultiKeyModels(currentRow.id, false, revision)
      if (!response.success || !response.data) {
        setSyncError(
          getServerErrorMessage(response, t('Failed to fetch models'))
        )
        return
      }
      setSyncPreview(response.data)
    } catch (error: unknown) {
      setSyncError(getServerErrorMessage(error, t('Failed to fetch models')))
    } finally {
      setIsSyncing(false)
    }
  }

  const applySync = async () => {
    if (
      !currentRow ||
      !canEditSensitive ||
      !syncPreview ||
      syncPreview.applied ||
      isApplyingSync
    ) {
      return
    }
    setIsApplyingSync(true)
    try {
      const response = requireServerSuccess(
        await syncMultiKeyModels(currentRow.id, true, syncPreview.revision)
      )
      if (!response.data?.applied) {
        setSyncError(t('Operation failed'))
        return
      }
      setSyncPreview(response.data)
      const models = [
        ...new Set([
          ...(response.data.channel_models ?? channelModels),
          ...response.data.added_models,
        ]),
      ]
      setCurrentRow({ ...currentRow, models: models.join(',') })
      toast.success(
        t('Added {{count}} model(s) to the channel', {
          count: response.data.added_models.length,
        })
      )
      // The applied sync edits the channel models, so the list and its usage
      // summaries must be refetched as well.
      queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      await loadKeyStatus()
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        setSyncOpen(false)
        setSyncPreview(null)
        await reloadAfterConflict()
      } else {
        setSyncError(getServerErrorMessage(error, t('Operation failed')))
      }
    } finally {
      setIsApplyingSync(false)
    }
  }

  // Limit edits preserve the current usage; the request carries both the
  // allocation revision and the budget account version the console rendered.
  const saveQuota = async (values: {
    enabled: boolean
    limitQuota: number
  }) => {
    if (
      !canEditSensitive ||
      isSavingQuota ||
      !quotaEditor?.id ||
      !quotaEditor.quota?.ledger_enabled ||
      quotaEditor.rev == null ||
      !currentRow
    ) {
      return
    }
    setIsSavingQuota(true)
    try {
      const response = await setMultiKeyQuota({
        channelId: currentRow.id,
        keyEntryId: quotaEditor.id,
        expectedRev: quotaEditor.rev,
        expectedConfigVersion: quotaEditor.quota.config_version,
        enabled: values.enabled,
        limitQuota: values.limitQuota,
      })
      if (response.success) {
        toast.success(getServerMessage(response) || t('Operation successful'))
        setQuotaEditor(null)
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        await loadKeyStatus()
        return
      }
      if (isQuotaConflict(response)) {
        await reloadAfterConflict()
        return
      }
      handleServerError(response, t('Operation failed'))
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        await reloadAfterConflict()
      } else {
        handleServerError(error, t('Operation failed'))
      }
    } finally {
      setIsSavingQuota(false)
    }
  }

  const resetQuota = async () => {
    if (
      !canEditSensitive ||
      isResettingQuota ||
      !resetQuotaTarget?.id ||
      !resetQuotaTarget.quota?.ledger_enabled ||
      resetQuotaTarget.rev == null ||
      !currentRow
    ) {
      return
    }
    setIsResettingQuota(true)
    try {
      const response = await resetMultiKeyQuota({
        channelId: currentRow.id,
        keyEntryId: resetQuotaTarget.id,
        expectedRev: resetQuotaTarget.rev,
        expectedConfigVersion: resetQuotaTarget.quota.config_version,
        operationId: quotaResetOperationId,
      })
      if (response.success) {
        toast.success(getServerMessage(response) || t('Operation successful'))
        setResetQuotaTarget(null)
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        await loadKeyStatus()
        return
      }
      if (isQuotaConflict(response)) {
        await reloadAfterConflict()
        return
      }
      handleServerError(response, t('Operation failed'))
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        await reloadAfterConflict()
      } else {
        handleServerError(error, t('Operation failed'))
      }
    } finally {
      setIsResettingQuota(false)
    }
  }

  const loadDedupPreview = async () => {
    if (!currentRow || !canEditSensitive || !ledgerEnabled || isDedupFetching) {
      return
    }
    setIsDedupFetching(true)
    setDedupPreview(null)
    setDedupError(null)
    try {
      const response = await deduplicateMultiKeys(currentRow.id, false)
      if (!response.success || !response.data) {
        setDedupError(
          getServerMessage(response) || t('Failed to load the preview')
        )
        return
      }
      setDedupPreview(response.data)
    } catch (error: unknown) {
      setDedupError(
        getServerErrorMessage(error, t('Failed to load the preview'))
      )
    } finally {
      setIsDedupFetching(false)
    }
  }

  const openDedup = () => {
    setDedupOpen(true)
    setDedupPreview(null)
    void loadDedupPreview()
  }

  const applyDedup = async () => {
    if (
      !currentRow ||
      !canEditSensitive ||
      !ledgerEnabled ||
      isDedupApplying ||
      isDedupFetching ||
      dedupPreview?.applied ||
      !dedupPreview?.preview_token ||
      dedupPreview.revision == null
    ) {
      return
    }
    setIsDedupApplying(true)
    try {
      const response = requireServerSuccess(
        await deduplicateMultiKeys(
          currentRow.id,
          true,
          dedupPreview.revision,
          dedupPreview.preview_token
        )
      )
      if (!response.data?.applied) {
        setDedupError(t('Operation failed'))
        return
      }
      const merged = response.data.merged_groups ?? 0
      const retired = response.data.retired_entries ?? 0
      const skipped = response.data.skipped_entries ?? 0
      toast.success(
        t(
          'Merged {{merged}} group(s), retired {{retired}} allocation(s), skipped {{skipped}}.',
          { merged, retired, skipped }
        )
      )
      setDedupPreview(null)
      setDedupOpen(false)
      queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      setCurrentPage(1)
      await loadKeyStatus(1, pageSize)
    } catch (error: unknown) {
      setDedupError(getServerErrorMessage(error, t('Operation failed')))
      setDedupPreview(null)
      if (isQuotaConflict(error)) await loadKeyStatus()
    } finally {
      setIsDedupApplying(false)
    }
  }

  const appendAllocation = async () => {
    if (!currentRow || !canSubmitAppend) return
    setIsAppending(true)
    try {
      const response = requireServerSuccess(
        await updateChannel(currentRow.id, {
          key: appendKey.trim(),
          key_mode: 'append',
          to_multi_key: singleKey ? true : undefined,
          expected_revision: singleKey
            ? currentRow.channel_info?.key_entry_revision
            : revision,
        })
      )
      setAppendKey('')
      setAppendOpen(false)
      if (singleKey) {
        // The PATCH reply may omit channel configuration; retain the session's
        // display data and let get_key_status supply the new authoritative IDs.
        setCurrentRow({
          ...currentRow,
          channel_info: {
            ...currentRow.channel_info,
            ...response.data?.channel_info,
            is_multi_key: true,
          },
        })
      }
      toast.success(
        t('Allocation added. Set its user, models and quota below.')
      )
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      if (!singleKey) {
        setStatusFilter(null)
        await loadKeyStatus(1, pageSize, null)
      }
    } catch (error: unknown) {
      if (isQuotaConflict(error)) {
        setAppendKey('')
        setAppendOpen(false)
        if (!singleKey) await reloadAfterConflict()
        else {
          try {
            const fresh = requireServerSuccess(
              await getChannel(currentRow.id)
            ).data
            if (fresh) setCurrentRow(fresh)
          } catch (refreshError) {
            handleServerError(refreshError, t('Failed to load channel'))
          }
          handleServerError(error, t('Operation failed'))
        }
        return
      }
      handleServerError(error, t('Operation failed'))
    } finally {
      setIsAppending(false)
    }
  }

  const renderStatusBadge = (status: number) => {
    const config = getMultiKeyStatusConfig(status)
    return (
      <StatusBadge
        label={t(config.label)}
        variant={config.variant}
        showDot
        copyable={false}
      />
    )
  }

  const formatKeyTimestamp = (timestamp?: number) => {
    if (!timestamp) return '-'
    return formatTimestamp(timestamp)
  }

  if (!currentRow) return null

  const appendInput = (
    <div className='grid gap-2'>
      <Label htmlFor='allocation-key'>{t('Upstream Key')}</Label>
      <Input
        id='allocation-key'
        type='password'
        autoComplete='off'
        value={appendKey}
        onChange={(event) => setAppendKey(event.target.value)}
        disabled={!canAppend || !ledgerEnabled || isAppending}
      />
    </div>
  )

  if (singleKey) {
    let body = appendInput
    if (!canAppend) {
      body = <p role='status'>{t('No permission to perform this action')}</p>
    } else if (singleBudget.isError) {
      body = (
        <ErrorState
          className='min-h-0 py-3'
          title={t('Failed to load the local budget')}
          onRetry={() => void singleBudget.refetch()}
        />
      )
    } else if (singleBudget.isPending || singleBudget.isFetching) {
      body = <LoadingState inline size='sm' message={t('Loading...')} />
    } else if (!ledgerEnabled) {
      body = (
        <p role='status'>
          {singleBudget.data
            ? t('Channel budget accounting is not enabled')
            : t('Local budget is not available for this channel yet.')}
        </p>
      )
    }
    return (
      <ConfirmDialog
        open={open}
        onOpenChange={(next) => {
          if (!isAppending) {
            onOpenChange(next)
            if (!next) setAppendKey('')
          }
        }}
        title={t('Add allocation')}
        desc={t(
          'The same static key can have separate allocations. The new allocation starts in the public pool with all channel models.'
        )}
        confirmText={t('Add')}
        handleConfirm={() => void appendAllocation()}
        disabled={!canSubmitAppend}
        isLoading={isAppending}
      >
        {body}
      </ConfirmDialog>
    )
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!mutationInProgress) onOpenChange(next)
        }}
        title={
          <>
            {t('Multi-Key Management')}
            <StatusBadge
              label={currentRow.name}
              variant='neutral'
              copyable={false}
            />
            {currentRow.channel_info?.multi_key_mode && (
              <StatusBadge
                label={
                  currentRow.channel_info.multi_key_mode === 'random'
                    ? t('Random')
                    : t('Polling')
                }
                variant='neutral'
                copyable={false}
              />
            )}
          </>
        }
        description={t(
          'Manage multi-key status and configuration for this channel'
        )}
        contentClassName='flex max-h-[min(90dvh,var(--dialog-available-height))] max-w-[calc(100%-2rem)] flex-col sm:max-w-[min(90vw,72rem)]'
        titleClassName='flex items-center gap-2'
        contentHeight='min(78dvh, 760px)'
        bodyClassName='flex h-full min-h-0 flex-col overflow-hidden'
      >
        <div
          ref={bodyRef}
          className='flex min-h-0 flex-1 flex-col gap-3 overflow-hidden'
        >
          {/* Statistics stay pinned above the scrolling key list */}
          <div className='grid shrink-0 grid-cols-3 gap-3'>
            <StatisticsCard
              label={t('Enabled')}
              count={enabledCount}
              total={total}
            />
            <StatisticsCard
              label={t('Manual Disabled')}
              count={manualDisabledCount}
              total={total}
            />
            <StatisticsCard
              label={t('Auto Disabled')}
              count={autoDisabledCount}
              total={total}
            />
          </div>

          {/* Toolbar */}
          <div className='flex shrink-0 flex-wrap items-center justify-between gap-2'>
            <Select
              items={MULTI_KEY_FILTER_OPTIONS.map((option) => ({
                value: option.value,
                label: t(option.label),
              }))}
              value={statusFilter === null ? 'all' : statusFilter.toString()}
              onValueChange={(v) => v !== null && handleStatusFilterChange(v)}
            >
              <SelectTrigger className='w-40'>
                <SelectValue placeholder={t('All Status')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {MULTI_KEY_FILTER_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>

            <div className='flex flex-wrap items-center gap-2'>
              {canAppend && (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => setAppendOpen(true)}
                  disabled={
                    !ledgerEnabled ||
                    isLoading ||
                    !!statusError ||
                    revision == null ||
                    mutationInProgress
                  }
                >
                  <Plus className='mr-2 size-4' aria-hidden />
                  {t('Add allocation')}
                </Button>
              )}
              <Button
                variant='outline'
                size='sm'
                onClick={() => loadKeyStatus()}
                disabled={isLoading}
                aria-label={t('Refresh')}
              >
                <RefreshCw className='h-4 w-4' />
              </Button>

              <Button
                variant='outline'
                size='sm'
                onClick={() => void openSync()}
                disabled={
                  isLoading ||
                  isSyncing ||
                  !canEditSensitive ||
                  !!statusError ||
                  mutationInProgress
                }
                title={
                  isLoading
                    ? t('Wait for the key status to load')
                    : t(
                        'Fetch the upstream models of every enabled key before changing anything'
                      )
                }
              >
                {isSyncing ? (
                  <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                ) : (
                  <CloudDownload className='mr-2 h-4 w-4' />
                )}
                {t('Sync models from upstream')}
              </Button>

              <Button
                variant='outline'
                size='sm'
                onClick={openDedup}
                disabled={
                  isLoading ||
                  !canEditSensitive ||
                  !ledgerEnabled ||
                  !!statusError ||
                  mutationInProgress
                }
                title={
                  canEditSensitive
                    ? t('Preview and merge equivalent allocations')
                    : t('No permission to perform this action')
                }
              >
                <Layers className='mr-2 h-4 w-4' />
                {t('Deduplicate Keys')}
              </Button>

              {manualDisabledCount + autoDisabledCount > 0 && (
                <Button
                  variant='default'
                  size='sm'
                  onClick={() => openConfirm({ type: 'enable-all' })}
                  disabled={
                    !canOperate ||
                    revision == null ||
                    isLoading ||
                    !!statusError ||
                    mutationInProgress
                  }
                >
                  <Power className='mr-2 h-4 w-4' />
                  {t('Enable All')}
                </Button>
              )}

              {enabledCount > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => openConfirm({ type: 'disable-all' })}
                  disabled={
                    !canOperate ||
                    revision == null ||
                    isLoading ||
                    !!statusError ||
                    mutationInProgress
                  }
                >
                  <PowerOff className='mr-2 h-4 w-4' />
                  {t('Disable All')}
                </Button>
              )}

              {autoDisabledCount > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => {
                    if (!canEditSensitive) return
                    openConfirm({ type: 'delete-disabled' })
                  }}
                  disabled={
                    !canEditSensitive ||
                    revision == null ||
                    isLoading ||
                    !!statusError ||
                    mutationInProgress
                  }
                  title={
                    canEditSensitive
                      ? undefined
                      : t('No permission to perform this action')
                  }
                >
                  <Trash2 className='mr-2 h-4 w-4' />
                  {t('Delete Auto-Disabled')}
                </Button>
              )}
            </div>
          </div>
          {!canEditSensitive && (
            <p className='text-muted-foreground text-xs'>
              {t('No permission to perform this action')}
            </p>
          )}

          {/* Per-key model scope, edited in place to keep the key list visible */}
          {modelsEditor && currentRow && (
            <div className='shrink-0'>
              <MultiKeyModelsEditor
                key={`${modelsEditor.keyStatus.id ?? modelsEditor.keyStatus.index}:${modelsEditor.models.join(',')}`}
                channelId={currentRow.id}
                keyIndex={modelsEditor.keyStatus.index}
                keyEntryId={modelsEditor.keyStatus.id}
                expectedRev={modelsEditor.keyStatus.rev}
                initialScopeMode={modelsEditor.keyStatus.scope_mode}
                channelModels={channelModels}
                initialModels={modelsEditor.models}
                isSaving={isSavingAssignment}
                onCancel={() => setModelsEditor(null)}
                onConflict={() => void reloadAfterConflict()}
                onSave={saveModels}
              />
            </div>
          )}

          {/* Per-key budget, edited in place beside the key list */}
          {quotaEditor && (
            <div className='shrink-0'>
              <MultiKeyQuotaEditor
                key={`${quotaEditor.id ?? quotaEditor.index}:${quotaEditor.quota?.config_version ?? 0}`}
                keyStatus={quotaEditor}
                isSaving={isSavingQuota}
                onCancel={() => setQuotaEditor(null)}
                onSave={(values) => void saveQuota(values)}
              />
            </div>
          )}

          {/* Per-key owner */}
          {userEditor && (
            <div className='shrink-0 space-y-2 rounded-md border p-3'>
              <div className='flex items-center justify-between gap-2'>
                <p className='text-sm font-medium'>
                  {t('Bind User')} #{userEditor.keyStatus.index + 1}
                </p>
                <div className='flex items-center gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={isSavingAssignment}
                    onClick={() => setUserEditor(null)}
                  >
                    {t('Cancel')}
                  </Button>
                  <Button
                    size='sm'
                    disabled={isSavingAssignment || !userIdValid}
                    onClick={saveUser}
                  >
                    {isSavingAssignment && (
                      <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                    )}
                    {t('Save')}
                  </Button>
                </div>
              </div>
              <Input
                type='number'
                min={1}
                value={userEditor.userId}
                onChange={(event) =>
                  setUserEditor((current) =>
                    current
                      ? { ...current, userId: event.target.value }
                      : current
                  )
                }
                placeholder={t('User ID')}
                aria-label={t('User ID')}
                disabled={isSavingAssignment}
              />
              <p className='text-muted-foreground text-xs'>
                {t('User ID must be a positive integer.')}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'The key is used only by this user and leaves the public key pool.'
                )}
              </p>
            </div>
          )}

          {/* Table: the only scrolling region of the dialog body */}
          <div
            role='region'
            aria-label={t('API Keys')}
            tabIndex={0}
            className='min-h-0 flex-1 overflow-auto overscroll-contain rounded-md border'
          >
            {isLoading && <LoadingState size='sm' className='min-h-20' />}
            {!isLoading && statusError && (
              <ErrorState
                className='min-h-0 py-3'
                title={statusError}
                onRetry={() => void loadKeyStatus()}
              />
            )}
            {!isLoading && !statusError && keys.length === 0 && (
              <div className='text-muted-foreground py-12 text-center'>
                {t('No keys found')}
              </div>
            )}
            {!isLoading && !statusError && keys.length > 0 && (
              <StaticDataTable
                className='overflow-visible rounded-none border-0'
                tableClassName='min-w-[1040px]'
                tableProps={{ withContainer: false }}
                data={keys}
                getRowKey={(key) => key.id ?? key.index}
                columns={[
                  {
                    id: 'index',
                    header: t('Index'),
                    className: `${stickyHeadClassName} w-40`,
                    cellClassName: 'font-mono text-xs sm:text-sm',
                    cell: (key) =>
                      `${key.index + 1}. ${key.key_preview || '-'}`,
                  },
                  {
                    id: 'entry-id',
                    header: t('Allocation ID'),
                    className: `${stickyHeadClassName} w-32`,
                    cellClassName: 'text-muted-foreground font-mono text-xs',
                    cell: (key) => (
                      <span
                        className='block max-w-28 truncate'
                        title={key.id ?? ''}
                      >
                        {key.id ?? '-'}
                      </span>
                    ),
                  },
                  {
                    id: 'status',
                    header: t('Status'),
                    className: `${stickyHeadClassName} w-32`,
                    cell: (key) => renderStatusBadge(key.status),
                  },
                  {
                    id: 'quota',
                    header: t('Quota'),
                    className: `${stickyHeadClassName} min-w-[180px]`,
                    cellClassName: 'text-xs',
                    cell: (key) => (
                      <KeyQuotaCell keyStatus={key} locale={locale} />
                    ),
                  },
                  ...(keyBalances
                    ? [
                        {
                          id: 'balance',
                          header: t('Balance'),
                          className: `${stickyHeadClassName} w-48`,
                          cellClassName: 'text-sm',
                          cell: (key: KeyStatus) => {
                            const result = keyBalances[String(key.index)]
                            if (!result) return '-'
                            // Wraps, so the key actions stay in view.
                            return (
                              <div className='max-w-56 space-y-0.5 whitespace-normal'>
                                <div className='font-medium'>
                                  {/* A key has no USD balance of its own. */}
                                  {formatChannelBalanceSummary(
                                    result,
                                    Number.NaN,
                                    { locale }
                                  )}
                                </div>
                                <ChannelBalanceDetails
                                  result={result}
                                  className='text-muted-foreground'
                                />
                              </div>
                            )
                          },
                        },
                      ]
                    : []),
                  {
                    id: 'models',
                    header: t('Allowed Models'),
                    className: `${stickyHeadClassName} min-w-[160px]`,
                    cellClassName: 'text-sm',
                    cell: (key) => {
                      if (key.scope_mode === 'all') return t('All models')
                      if (
                        key.scope_mode === 'allowlist' &&
                        !key.models?.length
                      ) {
                        return t('No models allowed')
                      }
                      return key.models?.length
                        ? key.models.join(', ')
                        : t('All models')
                    },
                  },
                  {
                    id: 'user',
                    header: t('Bound User'),
                    className: `${stickyHeadClassName} min-w-[140px]`,
                    cellClassName: 'text-sm',
                    cell: (key) => {
                      if (!key.user_id) return t('Public pool')
                      return key.username
                        ? `${key.username} (#${key.user_id})`
                        : `#${key.user_id}`
                    },
                  },
                  {
                    id: 'reason',
                    header: t('Disabled Reason'),
                    className: `${stickyHeadClassName} min-w-[200px]`,
                    cellClassName: 'max-w-xs truncate text-sm',
                    cell: (key) =>
                      key.reason ? translateServerText(t, key.reason) : '-',
                  },
                  {
                    id: 'disabled-time',
                    header: t('Disabled Time'),
                    className: `${stickyHeadClassName} w-44`,
                    cellClassName: 'text-muted-foreground text-sm',
                    cell: (key) => formatKeyTimestamp(key.disabled_time),
                  },
                  {
                    id: 'actions',
                    header: t('Actions'),
                    className: stickyActionsHeadClassName,
                    cellClassName: stickyActionsCellClassName,
                    cell: (key) => (
                      <MultiKeyTableRowActions
                        keyStatus={key}
                        canOperate={canOperate && !mutationInProgress}
                        canManage={canEditSensitive && !mutationInProgress}
                        quotaAvailable={
                          ledgerEnabled &&
                          key.quota?.ledger_enabled === true &&
                          !!key.id &&
                          key.rev != null
                        }
                        onAction={openConfirm}
                        onEditModels={(keyStatus) => {
                          setUserEditor(null)
                          setQuotaEditor(null)
                          setModelsEditor({
                            keyStatus,
                            models: keyStatus.models ?? [],
                          })
                        }}
                        onEditUser={(keyStatus) => {
                          setModelsEditor(null)
                          setQuotaEditor(null)
                          setUserEditor({
                            keyStatus,
                            userId: String(keyStatus.user_id ?? ''),
                          })
                        }}
                        onUnbind={setUnbindTarget}
                        onEditQuota={(keyStatus) => {
                          setModelsEditor(null)
                          setUserEditor(null)
                          setQuotaEditor(keyStatus)
                        }}
                        onResetQuota={(keyStatus) => {
                          setQuotaResetOperationId(newQuotaOperationId())
                          setResetQuotaTarget(keyStatus)
                        }}
                      />
                    ),
                  },
                ]}
              />
            )}
          </div>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className='flex shrink-0 items-center justify-between'>
              <div className='text-muted-foreground text-sm'>
                {t('Page {{current}} of {{total}}', {
                  current: currentPage,
                  total: totalPages,
                })}
              </div>
              <div className='flex gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => handlePageChange(currentPage - 1)}
                  disabled={currentPage === 1 || isLoading}
                >
                  {t('Previous')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => handlePageChange(currentPage + 1)}
                  disabled={currentPage >= totalPages || isLoading}
                >
                  {t('Next')}
                </Button>
              </div>
            </div>
          )}
        </div>
      </Dialog>

      <ConfirmDialog
        open={appendOpen}
        onOpenChange={(next) => {
          if (!isAppending) {
            setAppendOpen(next)
            if (!next) setAppendKey('')
          }
        }}
        title={t('Add allocation')}
        desc={t(
          'The same static key can have separate allocations. The new allocation starts in the public pool with all channel models.'
        )}
        confirmText={t('Add')}
        disabled={!canSubmitAppend}
        isLoading={isAppending}
        handleConfirm={() => void appendAllocation()}
      >
        {appendInput}
      </ConfirmDialog>

      {/* Confirmation Dialog */}
      <ConfirmDialog
        open={confirmAction !== null}
        onOpenChange={(open) => !open && setConfirmAction(null)}
        title={t('Confirm Action')}
        desc={t(getMultiKeyConfirmMessage(confirmAction))}
        destructive={isDestructiveAction(confirmAction)}
        isLoading={isPerformingAction}
        handleConfirm={performAction}
        finalFocus={getConfirmFinalFocus}
      />

      {/* Upstream model sync preview */}
      <MultiKeySyncDialog
        open={syncOpen}
        onOpenChange={(next) => {
          if (isApplyingSync) return
          setSyncOpen(next)
          if (!next) setSyncPreview(null)
        }}
        isFetching={isSyncing}
        isApplying={isApplyingSync}
        preview={syncPreview}
        error={syncError}
        onApply={() => void applySync()}
      />

      {/* Unbind confirmation */}
      <ConfirmDialog
        open={unbindTarget !== null}
        onOpenChange={(open) => !open && setUnbindTarget(null)}
        title={t('Unbind User')}
        desc={t('This key returns to the public key pool.')}
        isLoading={isSavingAssignment}
        handleConfirm={() => void unbindUser()}
      />

      {/* Reset one allocation's used amount; a separate action from editing. */}
      <ConfirmDialog
        open={resetQuotaTarget !== null}
        onOpenChange={(open) =>
          !open && !isResettingQuota && setResetQuotaTarget(null)
        }
        title={t('Reset used amount')}
        desc={t(
          'Reset current-period usage without changing live holds, lifetime usage or user charges.'
        )}
        confirmText={t('Reset')}
        isLoading={isResettingQuota}
        handleConfirm={() => void resetQuota()}
      />

      {/* Explicit dedup preview and apply */}
      <MultiKeyDedupDialog
        open={dedupOpen}
        onOpenChange={(next) => {
          if (isDedupApplying) return
          setDedupOpen(next)
          if (!next) setDedupPreview(null)
        }}
        isFetching={isDedupFetching}
        isApplying={isDedupApplying}
        preview={dedupPreview}
        error={dedupError}
        onRefresh={() => void loadDedupPreview()}
        onApply={() => void applyDedup()}
      />
    </>
  )
}

/**
 * Limit, used and remaining of one allocation. An absent budget view means the
 * ledger could not answer for this key, which is never rendered as unlimited.
 */
function KeyQuotaCell(props: { keyStatus: KeyStatus; locale?: string }) {
  const { t } = useTranslation()
  const quota = props.keyStatus.quota
  if (!quota) {
    return <span className='text-muted-foreground'>-</span>
  }
  if (!quota.ledger_enabled) {
    return <span className='text-muted-foreground'>{t('Not enabled')}</span>
  }
  const formatQuota = (value: number | null | undefined) =>
    value == null
      ? '-'
      : formatQuotaWithCurrency(value, {
          digitsLarge: 2,
          digitsSmall: 4,
          locale: props.locale,
        })
  const limitText = quota.enabled
    ? formatQuota(quota.limit_quota)
    : t('Unlimited')
  const remainingText = quota.enabled
    ? formatQuota(quota.remaining_quota)
    : t('Unlimited')
  return (
    <div className='space-y-0.5'>
      <div className='flex items-center gap-1'>
        <span className='text-muted-foreground'>{t('Limit')}</span>
        <span className='font-mono tabular-nums'>{limitText}</span>
        {quota.enabled && quota.exhausted && (
          <StatusBadge
            label={t('Exhausted')}
            variant='red'
            size='sm'
            copyable={false}
            showDot
          />
        )}
      </div>
      <div className='text-muted-foreground'>
        {t('Used')}{' '}
        <span className='text-foreground font-mono tabular-nums'>
          {formatQuota(quota.period_used_quota)}
        </span>
        {' / '}
        {t('Remaining')}{' '}
        <span className='text-foreground font-mono tabular-nums'>
          {remainingText}
        </span>
      </div>
      {quota.reserved_quota > 0 && (
        <div className='text-muted-foreground'>
          {t('Reserved')}{' '}
          <span className='font-mono tabular-nums'>
            {formatQuota(quota.reserved_quota)}
          </span>
        </div>
      )}
    </div>
  )
}
