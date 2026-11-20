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
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query'
import { t } from 'i18next'

import { pluginExpressionsEqual } from '@/features/pricing/lib/plugin-pricing'
import type {
  BillingUsageSchema,
  BillingUsageExample,
} from '@/features/pricing/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { createServerError } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  PRICING_KEYS,
  pricingValuesByModel,
  pricingValuesSubset,
  type PricingOptions,
  type PricingValues,
  type CacheWriteMode,
  type LegacyBillingDetails,
} from './pricing'

export type ModelPricingDescription = {
  billing_details?: LegacyBillingDetails
  effective: PricingValues
  cache_write_mode?: CacheWriteMode
}

export type ModelPricingPluginVariant = {
  plugin_key: string
  plugin_name: string
  icon?: string
  usage_schema: BillingUsageSchema
  usage_examples?: BillingUsageExample[]
  configured: string
  effective: string
  compatible: boolean
  // The plugin also relays the model: a model-level price prices its tasks
  // only per call or with u().
  relay?: boolean
  stale?: boolean
}

export type ModelPricingEntry = ModelPricingDescription & {
  plugin_variants?: ModelPricingPluginVariant[]
  model_name: string
  version: string
  configured: PricingValues
  usage_schema?: BillingUsageSchema
}

export type ModelPricingConfig = {
  entries: ModelPricingEntry[]
  options: PricingOptions
  empty_version: string
}
export type ModelPricingChange = {
  model_name: string
  expected_version: string
  pricing: PricingValues
  reset?: boolean
}

export type ModelPricingConversion = Partial<ModelPricingDescription> & {
  expression?: string
  unsupported_reason?: string
}

export async function previewModelPricingConversion(request: {
  model_name: string
  pricing: PricingValues
}): Promise<ModelPricingConversion> {
  const response = await api.post('/api/option/model_pricing/convert', request)
  if (!response.data.success) {
    throw createServerError(
      response.data,
      t('Failed to prepare pricing conversion')
    )
  }
  return response.data.data
}

export async function previewModelPricing(request: {
  model_name: string
  pricing: PricingValues
}): Promise<{
  effective: PricingValues
  cacheWriteMode?: CacheWriteMode
  billingDetails?: LegacyBillingDetails
}> {
  const response = await api.post('/api/option/model_pricing/preview', request)
  if (!response.data.success) {
    throw createServerError(response.data, t('Failed to load model pricing'))
  }
  return {
    effective: response.data.data.effective,
    cacheWriteMode: response.data.data.cache_write_mode,
    billingDetails: response.data.data.billing_details,
  }
}

export function useCanEditModelPricing() {
  return useAuthStore((state) => state.auth.user?.role === ROLE.SUPER_ADMIN)
}

export async function getModelPricing(
  names: string[] = []
): Promise<ModelPricingConfig> {
  const params = new URLSearchParams()
  for (const name of names) params.append('model', name)
  const res = await api.get('/api/option/model_pricing', { params })
  if (!res.data.success) {
    throw createServerError(res.data, t('Failed to load model pricing'))
  }
  return res.data.data
}

export function useModelPricing(names: string[] = [], enabled = true) {
  const canEdit = useCanEditModelPricing()
  return useQuery({
    queryKey: ['model-pricing-config', ...names],
    queryFn: () => getModelPricing(names),
    enabled: enabled && canEdit,
    refetchOnWindowFocus: false,
  })
}

export async function invalidateModelPricing(client: QueryClient) {
  await Promise.all([
    client.invalidateQueries({ queryKey: ['model-pricing-config'] }),
    client.invalidateQueries({ queryKey: ['model-pricing-preview'] }),
    client.invalidateQueries({ queryKey: ['system-options'] }),
    client.invalidateQueries({ queryKey: ['pricing'] }),
    client.invalidateQueries({ queryKey: ['models'] }),
  ])
}

export async function saveModelPricing(
  changes: ModelPricingChange[]
): Promise<string[]> {
  if (!changes.length) return []
  const res = await api.patch('/api/option/model_pricing', { changes })
  if (!res.data.success) {
    throw createServerError(res.data, t('Failed to save model pricing'))
  }
  const updated = res.data.data?.updated_models
  return Array.isArray(updated) ? updated : []
}

export function useSaveModelPricing() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: saveModelPricing,
    onSuccess: () => invalidateModelPricing(client),
  })
}

// Only dirty model fields are applied to stored configuration. Display-only
// built-in expressions for other models never become administrator overrides.
function dirtyPricing(
  configured: PricingValues | undefined,
  previous: PricingValues,
  next: PricingValues
): PricingValues | undefined {
  const dirty = PRICING_KEYS.filter((key) =>
    key === 'billing_setting.plugin_billing_expr'
      ? !pluginExpressionsEqual(previous[key], next[key])
      : previous[key] !== next[key]
  )
  if (!dirty.length) return undefined
  const pricing = structuredClone(configured ?? {})
  for (const key of dirty) {
    delete pricing[key]
    if (next[key] !== undefined) {
      Object.assign(pricing, { [key]: structuredClone(next[key]) })
    }
  }
  if (next['billing_setting.billing_mode'] === 'tiered_expr') {
    pricing['billing_setting.billing_mode'] = 'tiered_expr'
    pricing['billing_setting.billing_expr'] =
      next['billing_setting.billing_expr']
  }
  return pricing
}

export function buildPricingChanges(
  snapshot: ModelPricingConfig,
  before: PricingOptions,
  after: PricingOptions
): ModelPricingChange[] {
  return buildChangesFromValues(
    snapshot,
    pricingValuesByModel(before),
    pricingValuesByModel(after)
  )
}

function buildChangesFromValues(
  snapshot: ModelPricingConfig,
  previous: Map<string, PricingValues>,
  next: Map<string, PricingValues>
): ModelPricingChange[] {
  const entries = new Map(
    snapshot.entries.map((entry) => [entry.model_name, entry])
  )
  const changes: ModelPricingChange[] = []
  for (const name of new Set([...previous.keys(), ...next.keys()])) {
    const entry = entries.get(name)
    const pricing = dirtyPricing(
      entry?.configured,
      previous.get(name) ?? {},
      next.get(name) ?? {}
    )
    if (!pricing) continue
    changes.push({
      model_name: name,
      expected_version: entry?.version ?? snapshot.empty_version,
      pricing,
    })
  }
  return changes
}

/**
 * Sync payload for already-validated selections. It reads only the selected
 * models from the saved catalog, so preparing a large batch never serializes
 * the whole catalog, and no model is written twice.
 */
export function buildPriceSyncChanges(
  snapshot: ModelPricingConfig,
  before: PricingOptions,
  resolutions: Map<string, PricingValues>
): ModelPricingChange[] {
  const previous = pricingValuesSubset(before, resolutions.keys())
  const next = new Map(resolutions)
  const pluginKey = 'billing_setting.plugin_billing_expr'
  for (const [name, values] of resolutions) {
    const overrides = previous.get(name)?.[pluginKey]
    if (values[pluginKey] === undefined && overrides !== undefined) {
      // Upstream model prices do not replace provider-specific overrides.
      next.set(name, { ...values, [pluginKey]: overrides })
    }
  }
  return buildChangesFromValues(snapshot, previous, next)
}
