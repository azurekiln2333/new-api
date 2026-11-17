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
import { useQueryClient } from '@tanstack/react-query'
import {
  Loader2,
  RefreshCw,
  Trash2,
  Power,
  PowerOff,
  CloudDownload,
} from 'lucide-react'
import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getMultiKeyStatus,
  enableMultiKey,
  disableMultiKey,
  deleteMultiKey,
  enableAllMultiKeys,
  disableAllMultiKeys,
  deleteDisabledMultiKeys,
  setMultiKeyModels,
  setMultiKeyUser,
  syncMultiKeyModels,
} from '../../api'
import { MULTI_KEY_FILTER_OPTIONS } from '../../constants'
import {
  channelsQueryKeys,
  formatTimestamp,
  getMultiKeyStatusConfig,
  getMultiKeyConfirmMessage,
  isDestructiveAction,
  parseModelsList,
} from '../../lib'
import type {
  KeyStatus,
  MultiKeyConfirmAction,
  MultiKeyModelSyncResult,
} from '../../types'
import { useChannels } from '../channels-provider'
import { MultiKeyModelsEditor } from './multi-key-models-editor'
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
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const canEditSensitive = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
  )

  // Data state
  const [isLoading, setIsLoading] = useState(false)
  const [keys, setKeys] = useState<KeyStatus[]>([])
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

  // Per-key model scope and user binding editors
  const [modelsEditor, setModelsEditor] = useState<{
    keyIndex: number
    models: string[]
  } | null>(null)
  const [userEditor, setUserEditor] = useState<{
    keyIndex: number
    userId: string
  } | null>(null)
  const [unbindKeyIndex, setUnbindKeyIndex] = useState<number | null>(null)
  const [isSavingAssignment, setIsSavingAssignment] = useState(false)

  // Upstream model sync preview
  const [syncOpen, setSyncOpen] = useState(false)
  const [isSyncing, setIsSyncing] = useState(false)
  const [isApplyingSync, setIsApplyingSync] = useState(false)
  const [syncPreview, setSyncPreview] =
    useState<MultiKeyModelSyncResult | null>(null)
  const [syncError, setSyncError] = useState<string | null>(null)

  const channelModels = parseModelsList(currentRow?.models ?? '')
  const parsedUserId = Number.parseInt(userEditor?.userId ?? '')
  const userIdValid = Number.isInteger(parsedUserId) && parsedUserId > 0

  // Reset and load data when dialog opens
  useEffect(() => {
    if (open && currentRow) {
      setCurrentPage(1)
      setStatusFilter(null)
      loadKeyStatus(1, pageSize, null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentRow?.id])

  const loadKeyStatus = async (
    page: number = currentPage,
    size: number = pageSize,
    status: number | null = statusFilter
  ) => {
    if (!currentRow) return

    setIsLoading(true)
    try {
      const response = await getMultiKeyStatus(
        currentRow.id,
        page,
        size,
        status === null ? undefined : status
      )

      if (response.success && response.data) {
        setKeys(response.data.keys || [])
        setTotal(response.data.total || 0)
        setCurrentPage(response.data.page || 1)
        setPageSize(response.data.page_size || 10)
        setTotalPages(response.data.total_pages || 0)
        setEnabledCount(response.data.enabled_count || 0)
        setManualDisabledCount(response.data.manual_disabled_count || 0)
        setAutoDisabledCount(response.data.auto_disabled_count || 0)
      } else {
        handleServerError(response, t('Failed to load key status'))
      }
    } catch (error: unknown) {
      handleServerError(error, t('Failed to load key status'))
    } finally {
      setIsLoading(false)
    }
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
    if (!confirmAction || !currentRow) return
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
      const { type, keyIndex } = confirmAction
      let response

      // Execute the appropriate action
      if (type === 'enable' && keyIndex !== undefined) {
        response = await enableMultiKey(currentRow.id, keyIndex)
      } else if (type === 'disable' && keyIndex !== undefined) {
        response = await disableMultiKey(currentRow.id, keyIndex)
      } else if (type === 'delete' && keyIndex !== undefined) {
        response = await deleteMultiKey(currentRow.id, keyIndex)
      } else if (type === 'enable-all') {
        response = await enableAllMultiKeys(currentRow.id)
      } else if (type === 'disable-all') {
        response = await disableAllMultiKeys(currentRow.id)
      } else if (type === 'delete-disabled') {
        response = await deleteDisabledMultiKeys(currentRow.id)
      }

      if (response?.success) {
        toast.success(getServerMessage(response) || t('Operation successful'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })

        // Reload data - reset to page 1 for bulk actions
        const isBulkAction = type.includes('all') || type === 'delete-disabled'
        if (isBulkAction) {
          setCurrentPage(1)
          loadKeyStatus(1, pageSize)
        } else {
          loadKeyStatus(currentPage, pageSize)
        }
      } else {
        handleServerError(response, t('Operation failed'))
      }
    } catch (error: unknown) {
      handleServerError(error, t('Operation failed'))
    } finally {
      setIsPerformingAction(false)
      setConfirmAction(null)
    }
  }

  // Restrict a key to a subset of the channel models; an empty list restores
  // "every channel model". Both actions rebuild abilities on the server.
  const saveKeyAssignment = async (
    request: () => Promise<{ success: boolean; message?: string }>
  ) => {
    setIsSavingAssignment(true)
    try {
      const response = await request()
      if (response.success) {
        toast.success(getServerMessage(response) || t('Operation successful'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        setModelsEditor(null)
        setUserEditor(null)
        await loadKeyStatus()
      } else {
        handleServerError(response, t('Operation failed'))
      }
    } catch (error: unknown) {
      handleServerError(error, t('Operation failed'))
    } finally {
      setIsSavingAssignment(false)
    }
  }

  const saveModels = (models: string[]) => {
    if (!modelsEditor || !currentRow) return
    const { keyIndex } = modelsEditor
    void saveKeyAssignment(() =>
      setMultiKeyModels(currentRow.id, keyIndex, models)
    )
  }

  const saveUser = () => {
    if (!userEditor || !currentRow || !userIdValid) return
    const { keyIndex } = userEditor
    void saveKeyAssignment(() =>
      setMultiKeyUser(currentRow.id, keyIndex, parsedUserId)
    )
  }

  const unbindUser = async () => {
    if (unbindKeyIndex === null || !currentRow) return
    const keyIndex = unbindKeyIndex
    setUnbindKeyIndex(null)
    await saveKeyAssignment(() => setMultiKeyUser(currentRow.id, keyIndex, 0))
  }

  // Preview which models every enabled key serves before writing anything.
  const openSync = async () => {
    if (!currentRow) return
    setSyncOpen(true)
    setSyncPreview(null)
    setSyncError(null)
    setIsSyncing(true)
    try {
      const response = await syncMultiKeyModels(currentRow.id, false)
      if (!response.success || !response.data) {
        setSyncError(response.message || t('Failed to fetch models'))
        return
      }
      setSyncPreview(response.data)
    } catch (error: unknown) {
      setSyncError(getServerMessage(error) || t('Failed to fetch models'))
    } finally {
      setIsSyncing(false)
    }
  }

  const applySync = async () => {
    if (!currentRow) return
    setIsApplyingSync(true)
    try {
      const response = await syncMultiKeyModels(currentRow.id, true)
      if (!response.success || !response.data) {
        handleServerError(response, t('Operation failed'))
        return
      }
      setSyncPreview(response.data)
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
      handleServerError(error, t('Operation failed'))
    } finally {
      setIsApplyingSync(false)
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

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={onOpenChange}
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
        <div className='flex min-h-0 flex-1 flex-col gap-3 overflow-hidden'>
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

            <div className='flex items-center gap-2'>
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
                disabled={isLoading || isSyncing}
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

              {manualDisabledCount + autoDisabledCount > 0 && (
                <Button
                  variant='default'
                  size='sm'
                  onClick={() => setConfirmAction({ type: 'enable-all' })}
                >
                  <Power className='mr-2 h-4 w-4' />
                  {t('Enable All')}
                </Button>
              )}

              {enabledCount > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => setConfirmAction({ type: 'disable-all' })}
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
                    setConfirmAction({ type: 'delete-disabled' })
                  }}
                  disabled={!canEditSensitive}
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
                key={`${modelsEditor.keyIndex}:${modelsEditor.models.join(',')}`}
                channelId={currentRow.id}
                keyIndex={modelsEditor.keyIndex}
                channelModels={channelModels}
                initialModels={modelsEditor.models}
                isSaving={isSavingAssignment}
                onCancel={() => setModelsEditor(null)}
                onSave={saveModels}
              />
            </div>
          )}

          {/* Per-key owner */}
          {userEditor && (
            <div className='shrink-0 space-y-2 rounded-md border p-3'>
              <div className='flex items-center justify-between gap-2'>
                <p className='text-sm font-medium'>
                  {t('Bind User')} #{userEditor.keyIndex + 1}
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
            {isLoading && (
              <div className='flex items-center justify-center py-12'>
                <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
              </div>
            )}
            {!isLoading && keys.length === 0 && (
              <div className='text-muted-foreground py-12 text-center'>
                {t('No keys found')}
              </div>
            )}
            {!isLoading && !(keys.length === 0) && (
              <StaticDataTable
                className='overflow-visible rounded-none border-0'
                tableClassName='min-w-[800px]'
                tableProps={{ withContainer: false }}
                data={keys}
                getRowKey={(key) => key.index}
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
                    id: 'status',
                    header: t('Status'),
                    className: `${stickyHeadClassName} w-32`,
                    cell: (key) => renderStatusBadge(key.status),
                  },
                  {
                    id: 'models',
                    header: t('Allowed Models'),
                    className: `${stickyHeadClassName} min-w-[160px]`,
                    cellClassName: 'text-sm',
                    cell: (key) =>
                      key.models?.length
                        ? key.models.join(', ')
                        : t('All models'),
                  },
                  {
                    id: 'user',
                    header: t('Bound User'),
                    className: `${stickyHeadClassName} min-w-[140px]`,
                    cellClassName: 'text-sm',
                    cell: (key) => {
                      if (!key.user_id) return '-'
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
                    cell: (key) => key.reason || '-',
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
                        keyIndex={key.index}
                        status={key.status}
                        boundUserId={key.user_id}
                        canManage={canEditSensitive}
                        onAction={setConfirmAction}
                        onEditModels={(keyIndex) => {
                          setUserEditor(null)
                          setModelsEditor({
                            keyIndex,
                            models:
                              keys.find((item) => item.index === keyIndex)
                                ?.models ?? [],
                          })
                        }}
                        onEditUser={(keyIndex) => {
                          setModelsEditor(null)
                          setUserEditor({
                            keyIndex,
                            userId: String(
                              keys.find((item) => item.index === keyIndex)
                                ?.user_id ?? ''
                            ),
                          })
                        }}
                        onUnbind={setUnbindKeyIndex}
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

      {/* Confirmation Dialog */}
      <ConfirmDialog
        open={confirmAction !== null}
        onOpenChange={(open) => !open && setConfirmAction(null)}
        title={t('Confirm Action')}
        desc={t(getMultiKeyConfirmMessage(confirmAction))}
        destructive={isDestructiveAction(confirmAction)}
        isLoading={isPerformingAction}
        handleConfirm={performAction}
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
        open={unbindKeyIndex !== null}
        onOpenChange={(open) => !open && setUnbindKeyIndex(null)}
        title={t('Unbind User')}
        desc={t('This key returns to the public key pool.')}
        isLoading={isSavingAssignment}
        handleConfirm={() => void unbindUser()}
      />
    </>
  )
}
