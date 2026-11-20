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
import { CheckSquare, RefreshCcw } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { ButtonGroup } from '@/components/ui/button-group'
import {
  buildPriceSyncChanges,
  getModelPricing,
  saveModelPricing,
  invalidateModelPricing,
  type ModelPricingConfig,
  type ModelPricingChange,
} from '@/features/model-pricing/api'
import {
  getSitePricingCurrency,
  isValidPricingCurrency,
  type PricingCurrency,
} from '@/features/model-pricing/currency'
import {
  syncSelectionPricing,
  type PricingValues,
} from '@/features/model-pricing/pricing'
import { handleServerError } from '@/lib/handle-server-error'
import {
  requireServerSuccess,
  createServerError,
  translateChannelManagementError,
} from '@/lib/server-error-message'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { fetchUpstreamRatios, getUpstreamChannels } from '../api'
import type {
  DifferencesMap,
  PricingSyncModels,
  PricingSyncValues,
  UpstreamChannel,
  UpstreamConfig,
} from '../types'
import { ChannelSelectorDialog } from './channel-selector-dialog'
import {
  ConflictConfirmDialog,
  type ConflictItem,
} from './conflict-confirm-dialog'
import {
  DEFAULT_ENDPOINT,
  MODELS_DEV_PRESET_ENDPOINT,
  MODELS_DEV_PRESET_ID,
  OFFICIAL_CHANNEL_ENDPOINT,
  OFFICIAL_CHANNEL_ID,
  OPENROUTER_CHANNEL_TYPE,
  OPENROUTER_ENDPOINT,
} from './constants'
import { PriceAdjustmentSelect } from './price-adjustment-select'
import {
  adjustSyncPrice,
  type PriceAdjustment,
} from './upstream-price-adjustment'
import {
  getUpstreamDisplayName,
  type PricingSourceSelection,
} from './upstream-ratio-sync-helpers'
import { UpstreamRatioSyncTable } from './upstream-ratio-sync-table'

function getDefaultEndpointForChannel(channel: UpstreamChannel): string {
  if (channel.id === MODELS_DEV_PRESET_ID) return MODELS_DEV_PRESET_ENDPOINT
  if (channel.id === OFFICIAL_CHANNEL_ID) return OFFICIAL_CHANNEL_ENDPOINT
  if (channel.type === OPENROUTER_CHANNEL_TYPE) return OPENROUTER_ENDPOINT
  return DEFAULT_ENDPOINT
}

type SyncPreview = {
  /** Confirmation rows frozen from the selection at apply time. */
  conflicts: ConflictItem[]
  changes: ModelPricingChange[]
  /** Raw upstream values, used to show and optimistically update the rows. */
  resolutions: Map<string, PricingSyncValues>
  exchangeRateSummary?: string
  /** Model names of the batch, used to refresh the touched rows. */
  models: string[]
}

const SYNC_PREPARE_CHUNK = 200

/** Model names may be `__proto__`, so lookups need own properties only. */
function ownGet<T>(record: Record<string, T>, name: string): T | undefined {
  return Object.hasOwn(record, name) ? record[name] : undefined
}

/** Model names may be `__proto__`, so selections need own-property maps. */
function ownKeys<T>(source: Record<string, T>): Record<string, T> {
  return Object.assign(Object.create(null), source) as Record<string, T>
}

function yieldToBrowser(): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, 0)
  })
}

/**
 * Confirmation rows freeze the raw selected and current values together with
 * the adjustment mode and the exchange rate that produced them, so a later
 * rate change cannot alter what the preview shows or what the save writes.
 */
function buildConflictItems(
  resolutions: Map<string, PricingSyncValues>,
  sources: Record<string, string>,
  adjustments: Record<string, PriceAdjustment>,
  currentPrices: PricingSyncModels,
  rate: number | null,
  currency: PricingCurrency | null
): ConflictItem[] {
  const validCurrency = isValidPricingCurrency(currency)
  const items: ConflictItem[] = []
  for (const [model, next] of resolutions) {
    items.push({
      model,
      source: ownGet(sources, model) ?? '',
      current: { ...ownGet(currentPrices, model)?.current },
      next,
      adjustment: adjustments[model] ?? 'keep',
      rate,
      // A site without a usable exchange rate shows source prices only, instead
      // of converting with a rate that the checkout would reject.
      siteCurrency: validCurrency ? { ...currency } : undefined,
    })
  }
  return items
}

export function UpstreamRatioSync() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)
  const currency = useMemo(
    () => getSitePricingCurrency(currencyConfig),
    [currencyConfig]
  )
  const rate = isValidPricingCurrency(currency) ? currency.exchangeRate : null
  const [pricingBaseline, setPricingBaseline] =
    useState<ModelPricingConfig | null>(null)
  const [channelDialogOpen, setChannelDialogOpen] = useState(false)
  const [conflictDialogOpen, setConflictDialogOpen] = useState(false)
  const [selectedChannelIds, setSelectedChannelIds] = useState<number[]>([])
  const [channelEndpoints, setChannelEndpoints] = useState<
    Record<number, string>
  >({})
  const [differences, setDifferences] = useState<DifferencesMap>({})
  const [prices, setPrices] = useState<PricingSyncModels>({})
  const [selections, setSelections] = useState<
    Record<string, { source: string; adjustment: PriceAdjustment }>
  >({})
  const selectedSources = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(selections).map(([model, entry]) => [
          model,
          entry.source,
        ])
      ),
    [selections]
  )
  const adjustments = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(selections).map(([model, entry]) => [
          model,
          entry.adjustment,
        ])
      ),
    [selections]
  )
  const [preview, setPreview] = useState<SyncPreview | null>(null)
  const [loadingBaseline, setLoadingBaseline] = useState(false)
  const [preparing, setPreparing] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  // The preparing state is not visible to a second click in the same tick.
  const preparingRef = useRef(false)
  // Every preparation job owns a number. A job whose number is stale (the
  // selection changed, the source reloaded, or the page unmounted) may finish
  // computing but never replaces the preview the user is looking at.
  const prepareJob = useRef(0)
  useEffect(
    () => () => {
      prepareJob.current += 1
    },
    []
  )
  useEffect(() => {
    prepareJob.current += 1
  }, [selections, prices, pricingBaseline])
  // The pricing config is one shared React Query entry, so a refresh after a
  // save reads it once instead of racing a second full GET from the editor.
  const refreshPricingBaseline = useCallback(async () => {
    const refreshed = await queryClient.fetchQuery({
      queryKey: ['model-pricing-config'],
      queryFn: () => getModelPricing(),
      staleTime: 0,
    })
    setPricingBaseline(refreshed)
  }, [queryClient])
  const { data: channelsData } = useQuery({
    queryKey: ['upstream-channels'],
    queryFn: async () => requireServerSuccess(await getUpstreamChannels()),
    enabled: channelDialogOpen,
  })
  const channels = useMemo(() => channelsData?.data ?? [], [channelsData?.data])
  useEffect(() => {
    if (!channels.length) return
    setChannelEndpoints((previous) => {
      const next = { ...previous }
      for (const channel of channels) {
        next[channel.id] ??= getDefaultEndpointForChannel(channel)
      }
      return next
    })
  }, [channels])
  const adjustedPrices = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(selections).map(([model, entry]) => [
          model,
          adjustSyncPrice(
            ownGet(ownGet(prices, model)?.upstreams ?? {}, entry.source) ?? {},
            entry.adjustment,
            rate
          ),
        ])
      ),
    [selections, prices, rate]
  )
  const hasAdjustmentErrors = Object.values(adjustedPrices).some(
    (result) => result.error
  )
  const adjustedCount = Object.values(selections).filter(
    (entry) => entry.adjustment !== 'keep'
  ).length
  const fetchMutation = useMutation({
    mutationFn: async (request: Parameters<typeof fetchUpstreamRatios>[0]) => {
      const response = await fetchUpstreamRatios(request)
      if (!response.success || !response.data?.prices) {
        throw createServerError(response, t('Failed to fetch upstream prices'))
      }
      const results = response.data.test_results
      if (
        results.length &&
        results.every((result) => result.status === 'error')
      ) {
        throw new Error(
          results
            .map((result) => {
              const error = translateChannelManagementError(
                t,
                result.error ?? ''
              )
              return `${getUpstreamDisplayName(result.name, t)}: ${error}`
            })
            .join(', ')
        )
      }
      return response
    },
    onMutate: () => {
      setPrices({})
      setDifferences({})
      setSelections({})
    },
    onSuccess: (response) => {
      const errors = response.data.test_results.filter(
        (result) => result.status === 'error'
      )
      if (errors.length) {
        toast.warning(
          t('Some channels failed: {{errorMsg}}', {
            errorMsg: errors
              .map((result) => {
                const error = translateChannelManagementError(
                  t,
                  result.error ?? ''
                )
                return `${getUpstreamDisplayName(result.name, t)}: ${error}`
              })
              .join(', '),
          })
        )
      }
      setDifferences(response.data.differences)
      setPrices(response.data.prices)
      if (!Object.keys(response.data.prices).length) {
        toast.success(t('No price differences found'))
      }
    },
    onError: (error: Error) =>
      handleServerError(error, t('Failed to fetch upstream prices')),
  })
  const syncMutation = useMutation({
    mutationFn: async (confirmed: SyncPreview) => {
      const saved = await saveModelPricing(confirmed.changes)
      // The save is one atomic batch: when a deployment omits updated_models,
      // every requested model was accepted, so its frozen price is current.
      return {
        saved: saved.length ? saved : confirmed.models,
        resolutions: confirmed.resolutions,
      }
    },
    onSuccess: async ({ saved, resolutions }) => {
      // The save is committed here. A later baseline refresh failure must not
      // be reported as a failed save.
      const savedNames = new Set(saved)
      setPrices((previous) =>
        Object.fromEntries(
          Object.entries(previous).map(([name, row]) => [
            name,
            savedNames.has(name) && resolutions.get(name)
              ? { ...row, current: resolutions.get(name) ?? row.current }
              : row,
          ])
        )
      )
      setSelections({})
      setConflictDialogOpen(false)
      setPricingBaseline(null)
      toast.success(t('Prices synced successfully'))
      setRefreshing(true)
      try {
        // Start all dependent refreshes, then join the same in-flight baseline
        // request. Unrelated views must not hold this save in the pending state.
        void invalidateModelPricing(queryClient).catch((error) =>
          handleServerError(error, t('Reload pricing'))
        )
        await refreshPricingBaseline()
      } catch (error) {
        handleServerError(error, t('Reload pricing'))
      } finally {
        setRefreshing(false)
      }
    },
    onError: (error: Error) =>
      handleServerError(error, t('Failed to sync prices')),
  })
  const handleConfirmChannelSelection = async (selectedIds: number[]) => {
    const selected = channels.filter((channel) =>
      selectedIds.includes(channel.id)
    )
    if (!selected.length) {
      toast.warning(t('Please select at least one channel'))
      return
    }
    const upstreams: UpstreamConfig[] = selected.map((channel) => ({
      id: channel.id,
      name: channel.name,
      base_url: channel.base_url,
      endpoint:
        channelEndpoints[channel.id] || getDefaultEndpointForChannel(channel),
    }))
    setLoadingBaseline(true)
    setPrices({})
    setDifferences({})
    fetchMutation.reset()
    setPricingBaseline(null)
    setSelections({})
    syncMutation.reset()
    try {
      await refreshPricingBaseline()
      fetchMutation.mutate({ upstreams, timeout: 10, include_unchanged: true })
    } catch (error) {
      handleServerError(error, t('Failed to load model pricing'))
    } finally {
      setLoadingBaseline(false)
    }
  }
  const handleSelectPrices = useCallback(
    (selected: PricingSourceSelection[]) => {
      setSelections((previous) => {
        const next = ownKeys(previous)
        for (const entry of selected) {
          if (
            !ownGet(ownGet(prices, entry.model)?.upstreams ?? {}, entry.source)
          ) {
            continue
          }
          if (ownGet(next, entry.model)?.source === entry.source) continue
          // defineProperty keeps a model literally named `__proto__`.
          Object.defineProperty(next, entry.model, {
            value: { source: entry.source, adjustment: 'keep' },
            enumerable: true,
            writable: true,
            configurable: true,
          })
        }
        return next
      })
    },
    [prices]
  )
  const handleUnselectPrices = useCallback((models: string[]) => {
    setSelections((previous) => {
      const next = ownKeys(previous)
      for (const model of models) delete next[model]
      return next
    })
  }, [])
  const handleAdjustPrices = useCallback(
    (models: string[], adjustment: PriceAdjustment) => {
      setSelections((previous) => {
        const next = ownKeys(previous)
        for (const model of models) {
          const entry = ownGet(next, model)
          if (!entry) continue
          Object.defineProperty(next, model, {
            value: { ...entry, adjustment },
            enumerable: true,
            writable: true,
            configurable: true,
          })
        }
        return next
      })
    },
    []
  )
  const handleApplySync = async () => {
    if (!pricingBaseline || hasAdjustmentErrors || preparingRef.current) return
    // Freeze every input before yielding, so the preview and the payload come
    // from one consistent snapshot even if the user keeps editing.
    const baseline = pricingBaseline
    const frozenAdjusted = adjustedPrices
    const frozenPrices = prices
    const frozenSources = selectedSources
    const frozenAdjustments = adjustments
    const frozenRate = rate
    const frozenCurrency = currency
    const models = Object.keys(selections)
    const job = prepareJob.current + 1
    prepareJob.current = job
    preparingRef.current = true
    setPreparing(true)
    try {
      // Let the preparing state paint before the catalog work starts.
      await yieldToBrowser()
      const resolutions = new Map<string, PricingSyncValues>()
      const validated = new Map<string, PricingValues>()
      for (const [index, model] of models.entries()) {
        if (job !== prepareJob.current) return
        const selected = ownGet(frozenAdjusted, model)?.values
        // A selected model that lost its upstream row must abort the whole
        // preview: silently dropping it would save a partial selection.
        if (!ownGet(frozenPrices, model) || !selected) {
          handleServerError(
            new Error(t('This provider is unavailable for this model.')),
            t('Failed to sync prices')
          )
          return
        }
        resolutions.set(model, { ...selected })
        validated.set(model, syncSelectionPricing(model, selected))
        if (index % SYNC_PREPARE_CHUNK === SYNC_PREPARE_CHUNK - 1) {
          await yieldToBrowser()
        }
      }
      if (job !== prepareJob.current) return
      setPreview({
        conflicts: buildConflictItems(
          resolutions,
          frozenSources,
          frozenAdjustments,
          frozenPrices,
          frozenRate,
          frozenCurrency
        ),
        resolutions,
        models,
        changes: buildPriceSyncChanges(baseline, baseline.options, validated),
        exchangeRateSummary: isValidPricingCurrency(frozenCurrency)
          ? t('Current exchange rate: 1 USD = {{rate}} {{currency}}', {
              rate: String(frozenRate),
              currency: frozenCurrency.label,
            })
          : undefined,
      })
      syncMutation.reset()
      setConflictDialogOpen(true)
    } catch (error) {
      handleServerError(error, t('Failed to sync prices'))
    } finally {
      // A newer job cannot start while this flag is set, so clearing it here
      // cannot release the guard of a still-running job.
      preparingRef.current = false
      setPreparing(false)
    }
  }
  // Page-wide work: the catalog is being loaded or upstream prices are being
  // fetched. Preparing the preview and saving are scoped to their buttons.
  const isLoading = loadingBaseline || fetchMutation.isPending
  const selectedModels = Object.keys(selections)
  const batchAdjustmentDisabled =
    isLoading || preparing || selectedModels.length === 0 || rate === null
  return (
    <div className='flex h-full min-h-0 flex-col gap-3'>
      <div className='flex shrink-0 flex-wrap items-center gap-2'>
        <PriceAdjustmentSelect
          value={null}
          currency={currency}
          label={t('Set selected prices')}
          disabled={isLoading || !Object.keys(selections).length}
          onChange={(mode) => handleAdjustPrices(Object.keys(selections), mode)}
        />
        <span className='text-muted-foreground text-xs'>
          {isValidPricingCurrency(currency)
            ? t('Current exchange rate: 1 USD = {{rate}} {{currency}}', {
                rate: String(rate),
                currency: currency.label,
              })
            : t('A valid site exchange rate is required.')}
        </span>
      </div>
      <div className='min-h-0 flex-1'>
        {fetchMutation.isError ? (
          <ErrorState
            description={fetchMutation.error.message}
            action={
              <Button
                variant='outline'
                onClick={() =>
                  void handleConfirmChannelSelection(selectedChannelIds)
                }
              >
                {t('Retry')}
              </Button>
            }
          />
        ) : (
          <UpstreamRatioSyncTable
            toolbar={
              <Button
                variant='outline'
                onClick={() => setChannelDialogOpen(true)}
                disabled={isLoading}
              >
                <RefreshCcw className='size-4' aria-hidden />
                {t('Select price sources')}
              </Button>
            }
            prices={prices}
            differences={differences}
            selectedSources={selectedSources}
            adjustments={adjustments}
            adjustedPrices={adjustedPrices}
            currency={currency}
            onAdjustPrices={handleAdjustPrices}
            isDisabled={isLoading}
            isSyncing={fetchMutation.isPending || loadingBaseline}
            onSelectPrices={handleSelectPrices}
            onUnselectPrices={handleUnselectPrices}
          />
        )}
      </div>
      <div className='flex shrink-0 flex-wrap items-center justify-between gap-2 border-t pt-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-muted-foreground text-sm' role='status'>
            {t('{{count}} selected models', {
              count: Object.keys(selectedSources).length,
            })}
          </span>
          {adjustedCount > 0 && (
            <span className='text-muted-foreground text-sm'>
              {t('Exchange rate adjustments: {{count}}', {
                count: adjustedCount,
              })}
            </span>
          )}
          <ButtonGroup aria-label={t('Set selected prices')}>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='tabular-nums'
              disabled={batchAdjustmentDisabled}
              aria-label={t('Multiply by exchange rate (× {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              title={t('Multiply by exchange rate (× {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              onClick={() => handleAdjustPrices(selectedModels, 'multiply')}
            >
              × {rate ?? '—'}
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='tabular-nums'
              disabled={batchAdjustmentDisabled}
              aria-label={t('Divide by exchange rate (÷ {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              title={t('Divide by exchange rate (÷ {{rate}})', {
                rate: String(rate ?? '—'),
              })}
              onClick={() => handleAdjustPrices(selectedModels, 'divide')}
            >
              ÷ {rate ?? '—'}
            </Button>
          </ButtonGroup>
          {Object.keys(selectedSources).length > 0 && (
            <Button
              variant='ghost'
              size='sm'
              disabled={isLoading}
              onClick={() => setSelections({})}
            >
              {t('Clear selection')}
            </Button>
          )}
        </div>
        <Button
          onClick={() => {
            void handleApplySync()
          }}
          disabled={
            !pricingBaseline ||
            !Object.keys(selectedSources).length ||
            hasAdjustmentErrors ||
            isLoading ||
            preparing ||
            refreshing ||
            syncMutation.isPending
          }
        >
          <CheckSquare className='size-4' aria-hidden />
          {preparing ? t('Preparing conversion...') : t('Apply Sync')}
        </Button>
      </div>
      <ChannelSelectorDialog
        open={channelDialogOpen}
        onOpenChange={setChannelDialogOpen}
        channels={channels}
        selectedChannelIds={selectedChannelIds}
        onSelectedChannelIdsChange={setSelectedChannelIds}
        channelEndpoints={channelEndpoints}
        onChannelEndpointsChange={setChannelEndpoints}
        onConfirm={handleConfirmChannelSelection}
      />
      <ConflictConfirmDialog
        open={conflictDialogOpen}
        onOpenChange={(open) => {
          if (!syncMutation.isPending) setConflictDialogOpen(open)
        }}
        conflicts={preview?.conflicts ?? []}
        exchangeRateSummary={preview?.exchangeRateSummary}
        onConfirm={() => {
          if (preview) syncMutation.mutate(preview)
        }}
        isLoading={syncMutation.isPending}
        error={syncMutation.error?.message}
        onReload={() => {
          setConflictDialogOpen(false)
          setSelections({})
          setPrices({})
          setDifferences({})
          setPricingBaseline(null)
          syncMutation.reset()
          setChannelDialogOpen(true)
        }}
      />
    </div>
  )
}
