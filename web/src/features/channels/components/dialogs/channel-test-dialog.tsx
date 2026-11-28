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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  ColumnDef,
  RowSelectionState,
  Table as TanStackTable,
} from '@tanstack/react-table'
import {
  Check,
  CheckCircle2,
  Copy,
  Gauge,
  Info,
  Loader2,
  Settings,
  Trash2,
} from 'lucide-react'
import {
  type ChangeEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  DataTableBulkActions as BulkActionsToolbar,
  DataTablePagination,
  DataTableView,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { useIsMobile } from '@/hooks/use-mobile'
import { handleServerError } from '@/lib/handle-server-error'
import { ADMIN_PERMISSION_ACTIONS, ADMIN_PERMISSION_RESOURCES, hasPermission } from '@/lib/admin-permissions'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { updateChannel, getMultiKeyStatus, setMultiKeyModels } from '../../api'
import { CHANNEL_TYPE_TASK_PLUGIN } from '../../constants'
import { useTaskPluginOption } from '../../hooks/use-task-plugin-option'
import {
  channelsQueryKeys,
  formatResponseTime,
  handleTestChannel,
  parseChannelSettings,
  supportsChannelDiagnosticEndpoint,
  supportsChannelUpstreamPool,
} from '../../lib'
import type {
  Channel,
  GetChannelsResponse,
  KeyStatus,
  SearchChannelsResponse,
} from '../../types'
import { useChannels } from '../channels-provider'

type ChannelTestDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type ChannelTestDialogContentProps = ChannelTestDialogProps & {
  currentRow: Channel
}

type ModelRow = {
  model: string
}

type TestStatus = 'idle' | 'testing' | 'success' | 'error'

type TestResult = {
  status: TestStatus
  responseTime?: number
  completedAt?: number
  error?: string
  errorCode?: string
  keyEntryId?: string
  keyRevision?: number
  outOfScope?: boolean
}

type BatchProgress = {
  total: number
  completed: number
  success: number
  failed: number
}

type ChannelTestCachePatch = {
  responseTime: number
  testTime: number
}

type LatestChannelTestCachePatch = {
  patch: ChannelTestCachePatch
  completedAt: number
}

type ChannelListCache = GetChannelsResponse | SearchChannelsResponse

function createChannelTestCachePatch(
  responseTime?: number,
  completedAt = Date.now()
): ChannelTestCachePatch | undefined {
  if (typeof responseTime !== 'number' || !Number.isFinite(responseTime)) {
    return undefined
  }

  return {
    responseTime,
    testTime: Math.floor(completedAt / 1000),
  }
}

function getLatestChannelTestCachePatch(
  results: TestResult[]
): ChannelTestCachePatch | undefined {
  const latest = results.reduce<LatestChannelTestCachePatch | undefined>(
    (latestPatch, result) => {
      const completedAt = result.completedAt ?? 0
      const patch = createChannelTestCachePatch(
        result.responseTime,
        completedAt
      )
      if (!patch) return latestPatch
      if (!latestPatch || completedAt >= latestPatch.completedAt) {
        return { patch, completedAt }
      }
      return latestPatch
    },
    undefined
  )

  return latest?.patch
}

// Capabilities come from the shared channel/Key intersection helper.
// Streaming is not a separate endpoint: supported HTTP conversions also use SSE.
const endpointTypeOptions: Array<{
  value: string
  label: string
  path?: string
}> = [
  { value: 'auto', label: 'Auto detect (default)' },
  {
    value: 'openai',
    label: 'OpenAI (/v1/chat/completions)',
    path: '/v1/chat/completions',
  },
  {
    // Responses is converted to Messages for a Messages-only upstream, including
    // the SSE variant. Responses WebSocket is a different path and is not offered
    // here.
    value: 'openai-response',
    label: 'OpenAI Responses (/v1/responses)',
    path: '/v1/responses',
  },
  {
    // Compaction has no Anthropic Messages equivalent and is refused before
    // dispatch, so it still needs a declared Responses upstream.
    value: 'openai-response-compact',
    label: 'OpenAI Response Compaction (/v1/responses/compact)',
    path: '/v1/responses/compact',
  },
  {
    value: 'anthropic',
    label: 'Anthropic (/v1/messages)',
    path: '/v1/messages',
  },
  {
    value: 'gemini',
    label: 'Gemini (/v1beta/models/{model}:generateContent)',
    path: '/v1beta/models/{model}:generateContent',
  },
  {
    value: 'jina-rerank',
    label: 'Jina Rerank (/v1/rerank)',
    path: '/v1/rerank',
  },
  {
    value: 'image-generation',
    label: 'Image Generation (/v1/images/generations)',
    path: '/v1/images/generations',
  },
  {
    value: 'embeddings',
    label: 'Embeddings (/v1/embeddings)',
    path: '/v1/embeddings',
  },
]

const STREAM_INCOMPATIBLE_ENDPOINTS = new Set([
  'embeddings',
  'image-generation',
  'jina-rerank',
  'openai-response-compact',
])

const MODEL_PRICE_ERROR_CODE = 'model_price_error'
const FAILURE_SUMMARY_MAX_LENGTH = 96
const BATCH_TEST_CONCURRENCY = 5
const BATCH_TEST_DELAY_MS = 100
const AUTO_TEST_KEY = 'auto'
const KEY_CONFIGURATION_CHANGED =
  'Channel key configuration changed, reload and retry'

// Explicit key scope controls both visible models and dispatch. An empty
// allowlist denies every model; only an all-model or unrestricted legacy key
// serves every configured channel model.
function keyServesModel(key: KeyStatus, model: string) {
  const models = key.models ?? []
  if (key.scope_mode === 'all') return true
  if (key.scope_mode === 'allowlist') return models.includes(model)
  return !models.length || models.includes(model)
}

function getKeyDisabledReason(key: KeyStatus) {
  return key.status === 1 ? undefined : key.reason
}

// Keeps the key option identifiable without revealing the key: its position, the
// masked preview the server already returns, and its current status.
function keyMenuLabel(
  key: KeyStatus,
  t: (key: string, options?: Record<string, unknown>) => string
) {
  const status = key.status === 1 ? t('Enabled') : t('Disabled')
  return `${key.index + 1} · ${key.key_preview || '-'} · ${status}`
}

type FailureStatusDisplay = {
  summary: string
  details?: string
}

type FailureDetailsState = {
  model: string
  summary: string
  details: string
}

function sleep(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms))
}

function normalizeInlineError(errorText: string) {
  return errorText.replaceAll(/\s+/g, ' ').trim()
}

function getFirstErrorLine(errorText: string) {
  return errorText
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find(Boolean)
}

function truncateFailureSummary(summary: string) {
  if (summary.length <= FAILURE_SUMMARY_MAX_LENGTH) {
    return summary
  }

  return `${summary.slice(0, FAILURE_SUMMARY_MAX_LENGTH).trimEnd()}...`
}

function getFailureStatusDisplay({
  errorText,
  fallbackSummary,
  isModelPriceError,
  modelPriceSummary,
}: {
  errorText?: string
  fallbackSummary: string
  isModelPriceError: boolean
  modelPriceSummary: string
}): FailureStatusDisplay {
  const rawError = errorText?.trim()

  if (!rawError) {
    return { summary: fallbackSummary }
  }

  if (isModelPriceError) {
    return {
      summary: modelPriceSummary,
      details: rawError === modelPriceSummary ? undefined : rawError,
    }
  }

  const firstLine = getFirstErrorLine(rawError) ?? rawError
  const summary = truncateFailureSummary(normalizeInlineError(firstLine))
  const normalizedRawError = normalizeInlineError(rawError)

  return {
    summary,
    details: summary === normalizedRawError ? undefined : rawError,
  }
}

function getTestTableColumnClass(columnId: string) {
  switch (columnId) {
    case 'select':
      return 'w-10 min-w-10'
    case 'model':
      return 'w-auto min-w-48 whitespace-nowrap'
    case 'status':
      return 'w-28 min-w-28 whitespace-nowrap'
    case 'result':
      return 'w-80 min-w-80 max-w-80 whitespace-normal'
    case 'actions':
      return 'bg-popover w-px whitespace-nowrap'
    default:
      return undefined
  }
}

export function ChannelTestDialog({
  open,
  onOpenChange,
}: ChannelTestDialogProps) {
  const { currentRow } = useChannels()

  if (!currentRow) {
    return null
  }

  return (
    <ChannelTestDialogContent
      key={currentRow.id}
      open={open}
      onOpenChange={onOpenChange}
      currentRow={currentRow}
    />
  )
}

function ChannelTestDialogContent({
  open,
  onOpenChange,
  currentRow,
}: ChannelTestDialogContentProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentChannelId = currentRow.id
  const batchStopRequestedRef = useRef(false)
  const batchProgressToastIdRef = useRef<ReturnType<
    typeof toast.loading
  > | null>(null)
  const [endpointType, setEndpointType] = useState('auto')
  const [isStreamTest, setIsStreamTest] = useState(true)
  const [selectedTestKey, setSelectedTestKey] = useState<KeyStatus | null>(null)
  const [testAllChannelModels, setTestAllChannelModels] = useState(false)
  const currentUser = useAuthStore((state) => state.auth.user)
  const canAddKeyModels = hasPermission(currentUser, ADMIN_PERMISSION_RESOURCES.CHANNEL, ADMIN_PERMISSION_ACTIONS.OPERATE) &&
    hasPermission(currentUser, ADMIN_PERMISSION_RESOURCES.CHANNEL, ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE)
  const [searchTerm, setSearchTerm] = useState('')
  const [testResults, setTestResults] = useState<Record<string, TestResult>>({})
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({})
  const [testingModels, setTestingModels] = useState<Set<string>>(
    () => new Set()
  )
  const [isBatchTesting, setIsBatchTesting] = useState(false)
  const [isBatchStopRequested, setIsBatchStopRequested] = useState(false)
  const [batchProgress, setBatchProgress] = useState<BatchProgress | null>(null)
  const [removedModels, setRemovedModels] = useState<Set<string>>(
    () => new Set()
  )
  const [isDeleteFailedDialogOpen, setIsDeleteFailedDialogOpen] =
    useState(false)
  const [isDeletingFailed, setIsDeletingFailed] = useState(false)
  const [failureDetails, setFailureDetails] =
    useState<FailureDetailsState | null>(null)
  const [pagination, setPagination] = useState({
    pageIndex: 0,
    pageSize: 30,
  })
  const settings = useMemo(
    () => parseChannelSettings(currentRow.setting),
    [currentRow.setting]
  )
  const plugin = useTaskPluginOption(
    currentRow.type === CHANNEL_TYPE_TASK_PLUGIN
      ? settings?.task_plugin_key
      : undefined
  )
  const relayPaths = plugin?.relayPaths
  // The existing management endpoint also exposes the stable single-Key
  // allocation so diagnostics can honor its upstream restriction.
  const isMultiKeyChannel =
    currentRow.channel_info.is_multi_key &&
    currentRow.channel_info.multi_key_size > 0
  const requiresSingleKeyStatus =
    !isMultiKeyChannel && supportsChannelUpstreamPool(currentRow.type)
  const keyStatusQuery = useQuery({
    queryKey: channelsQueryKeys.multiKeyStatus(currentChannelId),
    queryFn: async () =>
      requireServerSuccess(
        await getMultiKeyStatus(
          currentChannelId,
          1,
          Math.max(1, currentRow.channel_info.multi_key_size)
        )
      ),
    enabled:
      open &&
      (isMultiKeyChannel || supportsChannelUpstreamPool(currentRow.type)),
    staleTime: 30_000,
  })
  const channelKeys = useMemo(
    () => keyStatusQuery.data?.data?.keys ?? [],
    [keyStatusQuery.data]
  )
  // Keep the allocation snapshot, not its mutable position in the key list.
  // A reordered/revised/deleted allocation requires an explicit new selection.
  const selectedKey = selectedTestKey
    ? channelKeys.find(
        (key) =>
          key.id === selectedTestKey.id &&
          key.rev === selectedTestKey.rev &&
          key.index === selectedTestKey.index
      )
    : undefined
  const selectedKeyValue = selectedTestKey
    ? `${selectedTestKey.id}:${selectedTestKey.rev}:${selectedTestKey.index}`
    : AUTO_TEST_KEY
  const selectedKeyUnavailable = selectedTestKey !== null && !selectedKey
  const singleKeyUnavailable =
    requiresSingleKeyStatus &&
    (!keyStatusQuery.isSuccess || channelKeys.length !== 1)
  const listedKeyCount = keyStatusQuery.data?.data?.total ?? channelKeys.length
  const keySelectItems = useMemo(
    () => [
      { value: AUTO_TEST_KEY, label: t('Auto (by tested model)') },
      ...(selectedKeyUnavailable
        ? [
            {
              value: selectedKeyValue,
              label: t(KEY_CONFIGURATION_CHANGED),
              disabled: true,
            },
          ]
        : []),
      ...channelKeys.map((key) => ({
        value: `${key.id}:${key.rev}:${key.index}`,
        label: keyMenuLabel(key, t),
        disabled:
          !key.id || !/^[1-9]\d*$/.test(key.id) || !key.rev || key.rev < 1,
      })),
    ],
    [channelKeys, selectedKeyUnavailable, selectedKeyValue, t]
  )

  const addKeyModel = useMutation({
    mutationFn: async (selection: { key: KeyStatus; model: string }) => {
      if (!canAddKeyModels || !selection.key.id || !selection.key.rev) throw new Error(KEY_CONFIGURATION_CHANGED)
      return requireServerSuccess(await setMultiKeyModels(
        currentChannelId, selection.key.index, [selection.model], selection.key.id, selection.key.rev,
        undefined, { addModels: true }
      ))
    },
    onSuccess: () => {
      toast.success(t('Key models saved'))
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.multiKeyStatus(currentChannelId) })
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
    },
    onError: (error) => {
      handleServerError(error, t('Operation failed'))
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.multiKeyStatus(currentChannelId) })
    },
  })
  const diagnosticKey = isMultiKeyChannel ? selectedKey : channelKeys[0]
  const selectedProtocol = diagnosticKey?.upstream_protocol || 'inherit'
  const endpointSelectItems = useMemo(
    () =>
      endpointTypeOptions
        .filter((option) => {
          if (relayPaths && option.path && !relayPaths.includes(option.path)) {
            return false
          }
          return supportsChannelDiagnosticEndpoint(
            currentRow.type,
            settings,
            selectedProtocol,
            option.value
          )
        })
        .map((option) => ({ value: option.value, label: t(option.label) })),
    [currentRow.type, settings, selectedProtocol, relayPaths, t]
  )
  const effectiveEndpointType = endpointSelectItems.some(
    (option) => option.value === endpointType
  )
    ? endpointType
    : endpointSelectItems[0]?.value ?? 'auto'

  const dismissBatchProgressToast = useCallback(() => {
    if (batchProgressToastIdRef.current === null) return

    toast.dismiss(batchProgressToastIdRef.current)
    batchProgressToastIdRef.current = null
  }, [])

  useEffect(() => {
    if (!batchProgress) {
      dismissBatchProgressToast()
      return
    }

    const title = isBatchStopRequested
      ? t('Stopping batch test...')
      : t('Batch testing models...')
    const completedText = t('{{completed}}/{{total}} completed', {
      completed: batchProgress.completed,
      total: batchProgress.total,
    })
    const resultText = t('{{success}} succeeded, {{failed}} failed', {
      success: batchProgress.success,
      failed: batchProgress.failed,
    })

    batchProgressToastIdRef.current = toast.loading(title, {
      id: batchProgressToastIdRef.current ?? undefined,
      description: `${completedText} · ${resultText}`,
    })
  }, [batchProgress, dismissBatchProgressToast, isBatchStopRequested, t])

  useEffect(() => dismissBatchProgressToast, [dismissBatchProgressToast])

  const resetState = useCallback(() => {
    batchStopRequestedRef.current = true
    setEndpointType('auto')
    setIsStreamTest(true)
    setSelectedTestKey(null)
    setTestAllChannelModels(false)
    setSearchTerm('')
    setTestResults({})
    setRowSelection({})
    setTestingModels(() => new Set())
    setIsBatchTesting(false)
    setIsBatchStopRequested(false)
    setBatchProgress(null)
    setRemovedModels(() => new Set())
    setIsDeleteFailedDialogOpen(false)
    setIsDeletingFailed(false)
    setFailureDetails(null)
    setPagination({ pageIndex: 0, pageSize: 30 })
  }, [])

  const streamDisabled = STREAM_INCOMPATIBLE_ENDPOINTS.has(
    effectiveEndpointType
  )
  const effectiveStreamTest = !streamDisabled && isStreamTest

  const handleEndpointTypeChange = useCallback((value: string | null) => {
    if (value === null) return

    setEndpointType(value)
    if (STREAM_INCOMPATIBLE_ENDPOINTS.has(value)) {
      setIsStreamTest(false)
    }
  }, [])

  const handleKeyChange = useCallback(
    (value: string | null) => {
      if (value === null) return
      const key = channelKeys.find(
        (item) => `${item.id}:${item.rev}:${item.index}` === value
      )
      if (
        value !== AUTO_TEST_KEY &&
        (!key?.id || !/^[1-9]\d*$/.test(key.id) || !key.rev || key.rev < 1)
      ) {
        return
      }
      setSelectedTestKey(key ? { ...key } : null)
      setTestResults({})
      setFailureDetails(null)
      setIsDeleteFailedDialogOpen(false)
      setRowSelection({})
      setPagination((prev) =>
        prev.pageIndex === 0 ? prev : { ...prev, pageIndex: 0 }
      )
    },
    [channelKeys]
  )

  const handleSearchTermChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      setSearchTerm(event.target.value)
      setPagination((prev) =>
        prev.pageIndex === 0 ? prev : { ...prev, pageIndex: 0 }
      )
    },
    []
  )

  const modelsValue = currentRow.models
  const defaultTestModel = currentRow.test_model?.trim()

  const baseModels = useMemo(() => {
    if (!modelsValue) return []
    return modelsValue
      .split(',')
      .map((model) => model.trim())
      .filter(Boolean)
  }, [modelsValue])

  const models = useMemo(
    () => baseModels.filter((model) => !removedModels.has(model)),
    [baseModels, removedModels]
  )

  const availableModels = useMemo(() => {
    if (singleKeyUnavailable || endpointSelectItems.length === 0) return []
    if (selectedTestKey === null) return models
    if (!selectedKey) return []
    if (testAllChannelModels) return models
    return models.filter((model) => keyServesModel(selectedKey, model))
  }, [
    models,
    selectedKey,
    selectedTestKey,
    testAllChannelModels,
    singleKeyUnavailable,
    endpointSelectItems.length,
  ])

  const successModels = useMemo(
    () =>
      availableModels.filter(
        (model) => testResults[model]?.status === 'success'
      ),
    [availableModels, testResults]
  )

  const failedModels = useMemo(
    () =>
      availableModels.filter((model) => testResults[model]?.status === 'error'),
    [availableModels, testResults]
  )

  const filteredModels = useMemo(() => {
    if (!searchTerm) return availableModels
    const keyword = searchTerm.toLowerCase()
    return availableModels.filter((model) =>
      model.toLowerCase().includes(keyword)
    )
  }, [availableModels, searchTerm])

  const tableData = useMemo<ModelRow[]>(
    () => filteredModels.map((model) => ({ model })),
    [filteredModels]
  )

  const markModelTesting = useCallback((key: string, isTesting: boolean) => {
    setTestingModels((prev) => {
      const next = new Set(prev)
      if (isTesting) {
        next.add(key)
      } else {
        next.delete(key)
      }
      return next
    })
  }, [])

  const updateTestResult = useCallback((key: string, result: TestResult) => {
    setFailureDetails((current) => (current?.model === key ? null : current))
    setTestResults((prev) => ({
      ...prev,
      [key]: result,
    }))
  }, [])

  const updateChannelTestCache = useCallback(
    (patch?: ChannelTestCachePatch) => {
      if (!patch) return

      queryClient.setQueriesData<ChannelListCache>(
        { queryKey: channelsQueryKeys.lists() },
        (oldData) => {
          const data = oldData?.data
          if (!oldData || !data?.items.length) return oldData

          let changed = false
          const nextItems = data.items.map((channel) => {
            if (channel.id !== currentChannelId) return channel

            changed = true
            return {
              ...channel,
              response_time: patch.responseTime,
              test_time: patch.testTime,
            }
          })

          if (!changed) return oldData

          return {
            ...oldData,
            data: {
              ...data,
              items: nextItems,
            },
          }
        }
      )
    },
    [currentChannelId, queryClient]
  )

  const refreshChannelLists = useCallback(
    (patch?: ChannelTestCachePatch) => {
      updateChannelTestCache(patch)
      void queryClient
        .invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        .then(() => updateChannelTestCache(patch))
        .catch(() => undefined)
    },
    [queryClient, updateChannelTestCache]
  )

  const testSingleModel = useCallback(
    async (
      model: string,
      silent = false,
      refreshList = true
    ): Promise<TestResult | undefined> => {
      if (
        !currentRow ||
        singleKeyUnavailable ||
        endpointSelectItems.length === 0
      ) return
      if (
        selectedTestKey !== null &&
        (!selectedKey || (!testAllChannelModels && !keyServesModel(selectedKey, model)))
      ) {
        return
      }

      markModelTesting(model, true)
      updateTestResult(model, { status: 'testing' })
      let finalResult: TestResult | undefined

      try {
        await handleTestChannel(
          currentRow.id,
          {
            channelName: currentRow.name,
            testModel: model,
            endpointType:
              effectiveEndpointType === 'auto'
                ? undefined
                : effectiveEndpointType,
            stream: effectiveStreamTest || undefined,
            keyIndex: selectedTestKey?.index,
            keyEntryId: selectedTestKey?.id,
            expectedRev: selectedTestKey?.rev,
            modelScope: selectedTestKey && testAllChannelModels ? 'all' : undefined,
            silent,
          },
          (success, responseTime, error, errorCode) => {
            const completedAt = Date.now()
            finalResult = {
              status: success ? 'success' : 'error',
              responseTime,
              completedAt,
              error,
              errorCode,
              keyEntryId: selectedTestKey?.id,
              keyRevision: selectedTestKey?.rev,
              outOfScope: !!selectedKey && testAllChannelModels && !keyServesModel(selectedKey, model),
            }
            updateTestResult(model, finalResult)
          }
        )
      } catch (error: unknown) {
        finalResult = {
          status: 'error',
          completedAt: Date.now(),
          error: error instanceof Error ? error.message : t('Test failed'),
        }
        updateTestResult(model, finalResult)
      } finally {
        markModelTesting(model, false)
        if (refreshList) {
          refreshChannelLists(
            createChannelTestCachePatch(
              finalResult?.responseTime,
              finalResult?.completedAt
            )
          )
        }
      }
      return finalResult
    },
    [
      currentRow,
      selectedKey,
      selectedTestKey,
      testAllChannelModels,
      singleKeyUnavailable,
      endpointSelectItems.length,
      effectiveEndpointType,
      effectiveStreamTest,
      markModelTesting,
      refreshChannelLists,
      t,
      updateTestResult,
    ]
  )

  const handleStopBatchTest = useCallback(() => {
    if (!isBatchTesting || isBatchStopRequested) return

    batchStopRequestedRef.current = true
    setIsBatchStopRequested(true)
  }, [isBatchStopRequested, isBatchTesting])

  const handleBatchTest = useCallback(
    async (modelsToTest: string[]) => {
      const uniqueModels = [
        ...new Set(
          modelsToTest
            .map((model) => model.trim())
            .filter((model) => availableModels.includes(model))
        ),
      ]
      if (!uniqueModels.length) return

      batchStopRequestedRef.current = false
      setIsBatchTesting(true)
      setIsBatchStopRequested(false)
      setBatchProgress({
        total: uniqueModels.length,
        completed: 0,
        success: 0,
        failed: 0,
      })

      let resultPatch: ChannelTestCachePatch | undefined
      const results: TestResult[] = []
      let completedCount = 0
      let successCount = 0
      let failedCount = 0

      try {
        const createFallbackResult = (error?: unknown): TestResult => ({
          status: 'error',
          completedAt: Date.now(),
          error: error instanceof Error ? error.message : t('Test failed'),
        })

        const recordBatchResult = (result: TestResult) => {
          results.push(result)
          completedCount += 1
          if (result.status === 'success') {
            successCount += 1
          }
          failedCount = completedCount - successCount

          setBatchProgress({
            total: uniqueModels.length,
            completed: completedCount,
            success: successCount,
            failed: failedCount,
          })
        }

        for (
          let startIndex = 0;
          startIndex < uniqueModels.length;
          startIndex += BATCH_TEST_CONCURRENCY
        ) {
          if (batchStopRequestedRef.current) {
            break
          }

          const batch = uniqueModels.slice(
            startIndex,
            startIndex + BATCH_TEST_CONCURRENCY
          )
          const batchPromises = batch.map(async (modelName) => {
            try {
              const result = await testSingleModel(modelName, true, false)
              const finalResult = result ?? createFallbackResult()
              if (!result) {
                updateTestResult(modelName, finalResult)
              }
              recordBatchResult(finalResult)
              return finalResult
            } catch (error: unknown) {
              const fallbackResult = createFallbackResult(error)
              updateTestResult(modelName, fallbackResult)
              recordBatchResult(fallbackResult)
              return fallbackResult
            }
          })

          await Promise.allSettled(batchPromises)

          if (
            batchStopRequestedRef.current ||
            startIndex + BATCH_TEST_CONCURRENCY >= uniqueModels.length
          ) {
            break
          }

          await sleep(BATCH_TEST_DELAY_MS)
        }

        resultPatch = getLatestChannelTestCachePatch(results)
        const stopped =
          batchStopRequestedRef.current && completedCount < uniqueModels.length

        dismissBatchProgressToast()
        if (stopped) {
          toast.info(
            t(
              'Batch test stopped: {{completed}}/{{total}} completed, {{success}} succeeded, {{failed}} failed',
              {
                completed: completedCount,
                total: uniqueModels.length,
                success: successCount,
                failed: failedCount,
              }
            )
          )
        } else if (failedCount > 0) {
          toast.error(
            t(
              'Batch test completed: {{success}} succeeded, {{failed}} failed',
              {
                success: successCount,
                failed: failedCount,
              }
            )
          )
        } else {
          toast.success(
            t('Batch test completed: {{count}} succeeded', {
              count: successCount,
            })
          )
        }
      } finally {
        batchStopRequestedRef.current = false
        setIsBatchTesting(false)
        setIsBatchStopRequested(false)
        setBatchProgress(null)
        setRowSelection({})
        refreshChannelLists(resultPatch)
      }
    },
    [
      availableModels,
      dismissBatchProgressToast,
      refreshChannelLists,
      t,
      testSingleModel,
      updateTestResult,
    ]
  )

  const handleSelectSuccessfulModels = useCallback(() => {
    setRowSelection(() => {
      const next: RowSelectionState = {}
      for (const model of successModels) {
        next[model] = true
      }
      return next
    })
  }, [successModels])

  const handleDeleteFailedModels = useCallback(async () => {
    const failed = failedModels
    if (!failed.length) {
      setIsDeleteFailedDialogOpen(false)
      return
    }

    const failedSet = new Set(failed)
    const remaining = models.filter((model) => !failedSet.has(model))

    setIsDeletingFailed(true)
    try {
      const response = await updateChannel(currentRow.id, {
        models: remaining.join(','),
      })
      if (response.success) {
        setRemovedModels((prev) => {
          const next = new Set(prev)
          for (const model of failed) next.add(model)
          return next
        })
        setTestResults((prev) => {
          const next = { ...prev }
          for (const model of failed) delete next[model]
          return next
        })
        setRowSelection((prev) => {
          const next = { ...prev }
          for (const model of failed) delete next[model]
          return next
        })
        toast.success(
          t('Deleted {{count}} failed models', { count: failed.length })
        )
        refreshChannelLists()
        setIsDeleteFailedDialogOpen(false)
      } else {
        handleServerError(response, t('Failed to delete failed models'))
      }
    } catch (error: unknown) {
      handleServerError(error, t('Failed to delete failed models'))
    } finally {
      setIsDeletingFailed(false)
    }
  }, [currentRow.id, failedModels, models, refreshChannelLists, t])

  const handleClose = useCallback(() => {
    resetState()
    onOpenChange(false)
  }, [onOpenChange, resetState])

  const handleDialogOpenChange = useCallback(
    (nextOpen: boolean) => {
      if (!nextOpen) {
        handleClose()
      }
    },
    [handleClose]
  )

  const isAnyTesting = testingModels.size > 0 || isBatchTesting || addKeyModel.isPending
  const isFilteringModels = searchTerm.trim().length > 0
  const testAllButtonLabel = isFilteringModels
    ? t('Test {{count}} matching models', { count: filteredModels.length })
    : t('Test all {{count}} models', { count: filteredModels.length })

  const columns = useMemo<ColumnDef<ModelRow>[]>(
    () => [
      {
        id: 'select',
        header: ({ table }) => (
          <Checkbox
            checked={table.getIsAllRowsSelected()}
            indeterminate={
              table.getIsSomeRowsSelected() && !table.getIsAllRowsSelected()
            }
            onCheckedChange={(value) => table.toggleAllRowsSelected(!!value)}
            aria-label={t('Select all models')}
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label={t('Select model {{model}}', {
              model: row.original.model,
            })}
          />
        ),
        enableSorting: false,
        enableHiding: false,
        size: 40,
      },
      {
        accessorKey: 'model',
        header: t('Model'),
        cell: ({ row }) => {
          const model = row.original.model
          const isDefault = defaultTestModel === model

          return (
            <div className='flex w-max items-center gap-2 whitespace-nowrap'>
              <span className='font-medium whitespace-nowrap' title={model}>
                {model}
              </span>
              {isDefault && (
                <StatusBadge
                  label={t('Default')}
                  variant='info'
                  size='sm'
                  copyable={false}
                />
              )}
            </div>
          )
        },
      },
      {
        id: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const model = row.original.model
          const result = testResults[model]
          return <TestStatusCell result={result} />
        },
        enableSorting: false,
        size: 112,
      },
      {
        id: 'result',
        header: t('Result'),
        cell: ({ row }) => {
          const model = row.original.model
          const result = testResults[model]
          return (
            <TestResultCell
              result={result}
              model={model}
              onOpenDetails={setFailureDetails}
            />
          )
        },
        enableSorting: false,
        size: 320,
      },
      {
        id: 'actions',
        header: t('Actions'),
        cell: ({ row }) => {
          const model = row.original.model
          const isTestingModel = testingModels.has(model)

          return (
            <div className='flex items-center gap-1'>
            {canAddKeyModels && selectedKey && testResults[model]?.status === 'success' && testResults[model]?.outOfScope && testResults[model]?.keyEntryId === selectedKey.id && testResults[model]?.keyRevision === selectedKey.rev && !keyServesModel(selectedKey, model) && (
              <Button
                variant='outline'
                size='sm'
                disabled={isAnyTesting}
                onClick={() => addKeyModel.mutate({ key: { ...selectedKey }, model })}
              >
                {t('Add to key')}
              </Button>
            )}
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => testSingleModel(model)}
                    disabled={isTestingModel || isBatchTesting || addKeyModel.isPending}
                    aria-label={t('Test Connection')}
                  />
                }
              >
                {isTestingModel ? (
                  <Loader2 className='size-4 animate-spin' />
                ) : (
                  <Gauge className='size-4' />
                )}
              </TooltipTrigger>
              <TooltipContent>{t('Test Connection')}</TooltipContent>
            </Tooltip>
            </div>
          )
        },
        enableSorting: false,
      },
    ],
    [
      defaultTestModel,
      isBatchTesting,
      isAnyTesting,
      addKeyModel,
      canAddKeyModels,
      selectedKey,
      t,
      testResults,
      testingModels,
      testSingleModel,
    ]
  )

  const { table } = useDataTable({
    data: tableData,
    columns,
    rowSelection,
    pagination,
    enableRowSelection: true,
    getRowId: (row) => row.model,
    onRowSelectionChange: setRowSelection,
    onPaginationChange: setPagination,
    withFilteredRowModel: false,
    withSortedRowModel: false,
    withFacetedRowModel: false,
  })

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleDialogOpenChange}
        title={
          <span className='inline-flex max-w-full min-w-0 items-center gap-1.5'>
            <span className='shrink-0'>{t('Test Channel Connection')}:</span>
            <span className='min-w-0 truncate'>{currentRow.name}</span>
          </span>
        }
        contentClassName='h-[min(90dvh,var(--dialog-available-height))] min-h-0 gap-2 overflow-hidden sm:max-w-4xl [@media(max-height:600px)]:gap-1 [@media(max-height:600px)]:p-2 [&>div.overscroll-contain]:flex-1 [&>div.overscroll-contain]:overflow-hidden'
        bodyClassName='flex h-full min-h-0 flex-col overflow-hidden'
        footerClassName='py-2 sm:py-2'
        footer={
          <Button variant='outline' onClick={handleClose}>
            {t('Close')}
          </Button>
        }
      >
        <div className='flex min-h-0 flex-1 flex-col gap-2'>
          {selectedTestKey && (
            <div className='flex shrink-0 items-center gap-2'>
              <Switch
                id='test-all-channel-models'
                checked={testAllChannelModels}
                disabled={isAnyTesting || selectedKeyUnavailable}
                onCheckedChange={(checked) => {
                  setTestAllChannelModels(checked)
                  setTestResults({})
                  setRowSelection({})
                  setFailureDetails(null)
                  setIsDeleteFailedDialogOpen(false)
                  setPagination((previous) => ({ ...previous, pageIndex: 0 }))
                }}
              />
              <Label htmlFor='test-all-channel-models'>{t('All channel models')}</Label>
            </div>
          )}
          {requiresSingleKeyStatus && keyStatusQuery.isPending && (
            <LoadingState inline size='sm' message={t('Loading...')} />
          )}
          {requiresSingleKeyStatus &&
            !keyStatusQuery.isPending &&
            singleKeyUnavailable && (
              <ErrorState
                className='min-h-0 py-3'
                title={t('Failed to load key status')}
                onRetry={() => void keyStatusQuery.refetch()}
              />
            )}
          {!singleKeyUnavailable && endpointSelectItems.length === 0 && (
            <p role='status'>
              {t(
                'This channel does not support a compatible upstream endpoint for this request'
              )}
            </p>
          )}
          <div className='flex shrink-0 items-center gap-2'>
            {isMultiKeyChannel && (
              <div className='min-w-0 flex-1'>
                <Label htmlFor='test-key' className='sr-only'>
                  {t('Tested key')}
                </Label>
                <Combobox
                  options={keySelectItems}
                  value={selectedKeyValue}
                  onValueChange={handleKeyChange}
                  disabled={isAnyTesting}
                  id='test-key'
                  className='w-full min-w-0'
                  placeholder={t('Auto (by tested model)')}
                />
              </div>
            )}
            <Input
              placeholder={t('Filter models...')}
              value={searchTerm}
              onChange={handleSearchTermChange}
              className='min-w-0 flex-1'
            />
            <Popover>
              <PopoverTrigger
                render={
                  <Button
                    variant='outline'
                    size='icon-sm'
                    aria-label={t('Settings')}
                  />
                }
              >
                <Settings />
              </PopoverTrigger>
              <PopoverContent
                align='end'
                className='w-80 max-w-[calc(100vw-2rem)] gap-3'
              >
                <div className='grid gap-2'>
                  <Label htmlFor='endpoint-type'>{t('Endpoint Type')}</Label>
                  <Combobox
                    options={endpointSelectItems}
                    value={effectiveEndpointType}
                    onValueChange={handleEndpointTypeChange}
                    id='endpoint-type'
                    className='w-full min-w-0'
                    placeholder={t('Auto detect (default)')}
                  />
                </div>
                <div className='flex items-center justify-between gap-2'>
                  <Label htmlFor='stream-toggle'>{t('Stream Mode')}</Label>
                  <Switch
                    id='stream-toggle'
                    checked={effectiveStreamTest}
                    onCheckedChange={setIsStreamTest}
                    disabled={streamDisabled}
                  />
                </div>
                {listedKeyCount > channelKeys.length && (
                  <p className='text-muted-foreground text-xs'>
                    {t('Only the first {{count}} keys are listed.', {
                      count: channelKeys.length,
                    })}
                  </p>
                )}
                {selectedKey && getKeyDisabledReason(selectedKey) && (
                  <p className='text-muted-foreground text-xs wrap-break-word'>
                    {t('Disabled Reason')}: {getKeyDisabledReason(selectedKey)}
                  </p>
                )}
              </PopoverContent>
            </Popover>
          </div>

          <div className='flex min-h-0 flex-1 flex-col gap-2'>
            <div className='flex shrink-0 flex-wrap items-center gap-2'>
              {isBatchTesting ? (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={handleStopBatchTest}
                  disabled={isBatchStopRequested}
                >
                  {isBatchStopRequested ? t('Stopping...') : t('Stop testing')}
                </Button>
              ) : (
                <>
                  <Button
                    size='sm'
                    onClick={() => handleBatchTest(filteredModels)}
                    disabled={isAnyTesting || filteredModels.length === 0}
                  >
                    {testAllButtonLabel}
                  </Button>
                  {successModels.length > 0 && (
                    <Button
                      variant='outline'
                      size='icon-sm'
                      onClick={handleSelectSuccessfulModels}
                      aria-label={t('Select successful models ({{count}})', {
                        count: successModels.length,
                      })}
                    >
                      <CheckCircle2 />
                    </Button>
                  )}
                  {failedModels.length > 0 && (
                    <Button
                      variant='outline'
                      size='icon-sm'
                      onClick={() => setIsDeleteFailedDialogOpen(true)}
                      aria-label={t('Delete failed models ({{count}})', {
                        count: failedModels.length,
                      })}
                    >
                      <Trash2 />
                    </Button>
                  )}
                </>
              )}
            </div>

            <div className='flex min-h-0 flex-1 flex-col gap-2'>
              <DataTableView
                table={table}
                containerClassName='min-h-0 flex-1 rounded-md'
                containerProps={{
                  role: 'region',
                  'aria-label': t('Channel models'),
                }}
                tableContainerClassName='h-full min-h-0'
                splitHeader
                splitHeaderScrollClassName='h-full'
                bodyContainerClassName='[scrollbar-gutter:stable]'
                tableClassName='w-max min-w-full table-auto'
                pinnedColumns={[
                  {
                    columnId: 'actions',
                    side: 'right',
                    cellClassName: 'bg-popover',
                  },
                ]}
                colgroup={
                  <colgroup>
                    <col className='w-10 min-w-10' />
                    <col className='w-auto' />
                    <col className='w-28' />
                    <col className='w-80' />
                    <col className='w-px' />
                  </colgroup>
                }
                getColumnClassName={(columnId) =>
                  getTestTableColumnClass(columnId)
                }
                emptyContent={
                  models.length
                    ? t('No models matched your search.')
                    : t('This channel has no configured models.')
                }
                emptyCellClassName='text-muted-foreground h-16 text-center text-sm'
              />

              <div className='shrink-0'>
                <DataTablePagination table={table} />
              </div>
            </div>

            <TestModelsBulkActions table={table} />
          </div>
        </div>
      </Dialog>
      <ConfirmDialog
        open={isDeleteFailedDialogOpen}
        onOpenChange={setIsDeleteFailedDialogOpen}
        title={t('Delete failed models')}
        desc={t(
          'This removes {{count}} failed models from this channel. This action cannot be undone.',
          { count: failedModels.length }
        )}
        destructive
        isLoading={isDeletingFailed}
        confirmText={t('Delete')}
        handleConfirm={handleDeleteFailedModels}
      />
      <FailureDetailsSheet
        details={failureDetails}
        onOpenChange={(sheetOpen) => {
          if (!sheetOpen) {
            setFailureDetails(null)
          }
        }}
      />
    </>
  )
}

function TestStatusCell({ result }: { result?: TestResult }) {
  const { t } = useTranslation()

  if (!result || result.status === 'idle') {
    return (
      <StatusBadge label={t('Not tested')} variant='neutral' copyable={false} />
    )
  }

  if (result.status === 'testing') {
    return (
      <StatusBadge variant='info' copyable={false}>
        <Loader2 className='size-3.5 shrink-0 animate-spin' />
        <span className='min-w-0 truncate leading-normal'>
          {t('Testing...')}
        </span>
      </StatusBadge>
    )
  }

  if (result.status === 'success') {
    return (
      <StatusBadge label={t('Success')} variant='success' copyable={false} />
    )
  }

  return <StatusBadge label={t('Failed')} variant='danger' copyable={false} />
}

function TestResultCell({
  result,
  model,
  onOpenDetails,
}: {
  result?: TestResult
  model: string
  onOpenDetails: (details: FailureDetailsState) => void
}) {
  const { t } = useTranslation()

  if (!result || result.status === 'idle') {
    return <span className='text-muted-foreground text-sm'>-</span>
  }

  if (result.status === 'testing') {
    return (
      <div className='text-muted-foreground flex min-w-0 items-center gap-2 text-sm'>
        <Loader2 className='size-4 shrink-0 animate-spin' />
        <span className='truncate'>{t('Testing...')}</span>
      </div>
    )
  }

  if (result.status === 'success') {
    return typeof result.responseTime === 'number' ? (
      <span className='text-muted-foreground text-sm'>
        {formatResponseTime(result.responseTime, t)}
      </span>
    ) : (
      <span className='text-muted-foreground text-sm'>-</span>
    )
  }

  return (
    <FailureResultContent
      result={result}
      model={model}
      onOpenDetails={onOpenDetails}
    />
  )
}

function FailureResultContent({
  result,
  model,
  onOpenDetails,
}: {
  result: TestResult
  model: string
  onOpenDetails: (details: FailureDetailsState) => void
}) {
  const { t } = useTranslation()
  const errorText = result.error?.trim()
  const isModelPriceError = result.errorCode === MODEL_PRICE_ERROR_CODE
  const modelPriceSummary = t(
    'Model price is not configured. Please complete model pricing in settings.'
  )
  const { summary, details } = getFailureStatusDisplay({
    errorText,
    fallbackSummary: t('Test failed'),
    isModelPriceError,
    modelPriceSummary,
  })

  return (
    <div className='flex min-w-0 items-center gap-2 text-xs whitespace-normal'>
      <p className='text-muted-foreground line-clamp-2 min-w-0 flex-1 leading-snug wrap-break-word'>
        {summary}
      </p>
      <div className='flex shrink-0 flex-wrap items-center justify-end gap-1.5'>
        {isModelPriceError && (
          <Button
            variant='outline'
            size='sm'
            className='h-7 w-fit px-2 text-xs'
            onClick={() =>
              window.open('/system-settings/billing/model-pricing', '_blank')
            }
          >
            <Settings className='mr-1 h-3 w-3 shrink-0' />
            {t('Go to Settings')}
          </Button>
        )}
        {details && (
          <Button
            variant='ghost'
            size='sm'
            className='h-7 w-fit px-2 text-xs'
            aria-haspopup='dialog'
            onClick={() => onOpenDetails({ model, summary, details })}
          >
            <Info className='mr-1 h-3 w-3 shrink-0' />
            {t('Details')}
          </Button>
        )}
      </div>
    </div>
  )
}

function FailureDetailsSheet({
  details,
  onOpenChange,
}: {
  details: FailureDetailsState | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const isMobile = useIsMobile()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  return (
    <Sheet open={Boolean(details)} onOpenChange={onOpenChange}>
      <SheetContent
        side={isMobile ? 'bottom' : 'right'}
        className={
          isMobile
            ? sideDrawerContentClassName('h-auto max-h-[85dvh] rounded-t-xl')
            : sideDrawerContentClassName('sm:max-w-lg')
        }
      >
        {details && (
          <>
            <SheetHeader className={sideDrawerHeaderClassName('sm:px-5')}>
              <SheetTitle className='pr-10'>{t('Details')}</SheetTitle>
              <SheetDescription className='pr-10 wrap-break-word'>
                {details.model}
              </SheetDescription>
            </SheetHeader>
            <div className={sideDrawerFormClassName('gap-4 sm:px-5')}>
              <section className='space-y-1'>
                <div className='text-muted-foreground text-xs font-medium'>
                  {t('Model')}
                </div>
                <p className='text-sm font-medium break-all'>{details.model}</p>
              </section>
              <section className='space-y-1'>
                <div className='text-muted-foreground text-xs font-medium'>
                  {t('Failed')}
                </div>
                <p className='text-muted-foreground text-sm leading-relaxed wrap-break-word'>
                  {details.summary}
                </p>
              </section>
              <section className='space-y-2'>
                <div className='text-muted-foreground text-xs font-medium'>
                  {t('Details')}
                </div>
                <pre className='bg-muted/30 text-muted-foreground m-0 max-w-full rounded-md border p-3 text-xs leading-relaxed wrap-break-word whitespace-pre-wrap'>
                  {details.details}
                </pre>
              </section>
            </div>
            <SheetFooter className={sideDrawerFooterClassName('sm:px-5')}>
              <Button
                variant='outline'
                className='w-full sm:w-auto'
                onClick={() => copyToClipboard(details.details)}
              >
                {copiedText === details.details ? (
                  <Check className='mr-2 h-4 w-4 text-green-600' />
                ) : (
                  <Copy className='mr-2 h-4 w-4' />
                )}
                {t('Copy')}
              </Button>
            </SheetFooter>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

function TestModelsBulkActions({ table }: { table: TanStackTable<ModelRow> }) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedModels = selectedRows.map((row) => row.original.model)

  const handleCopySelected = useCallback(() => {
    if (selectedModels.length === 0) return
    void copyToClipboard(selectedModels.join(','))
  }, [copyToClipboard, selectedModels])

  return (
    <BulkActionsToolbar table={table} entityName='model' placement='inline'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              size='sm'
              onClick={handleCopySelected}
              disabled={selectedModels.length === 0}
            />
          }
        >
          <Copy data-icon='inline-start' />
          {t('Copy selected models')}
        </TooltipTrigger>
        <TooltipContent>
          <p>{t('Copy selected models separated by commas (e.g. a,b)')}</p>
        </TooltipContent>
      </Tooltip>
    </BulkActionsToolbar>
  )
}
