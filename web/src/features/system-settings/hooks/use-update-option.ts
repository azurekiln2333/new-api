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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerErrorSources,
  requireServerSuccess,
} from '@/lib/server-error-message'

import {
  updatePasskeyDomains,
  updateSystemOption,
  updateSystemOptionsBatch,
} from '../api'
import type {
  SystemOptionsResponse,
  UpdateOptionRequest,
  UpdatePasskeyDomainsRequest,
} from '../types'

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = new Set([
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'console_setting.sidebar_personal_settings_enabled',
  'console_setting.announcements',
  'console_setting.announcements_enabled',
  'Notice',
  'RegisterEnabled',
  'InviteRegisterEnabled',
  'RegisterAffiliateLinkEnabled',
  'LogConsumeEnabled',
  'log_setting.user_export_enabled',
  'log_setting.max_range_days',
  'QuotaPerUnit',
  'USDExchangeRate',
  'DisplayInCurrencyEnabled',
  'DisplayTokenStatEnabled',
  'general_setting.quota_display_type',
  'general_setting.custom_currency_symbol',
  'general_setting.custom_currency_exchange_rate',
  'system_update_setting.repository',
  'oidc.display_name',
  'ServerAddress',
  'passkey.enabled',
  'passkey.rp_id',
  'passkey.legacy_rp_ids',
  'passkey.origins',
  'QuotaRemindEnabled',
  'QuotaRemindThreshold',
])

// Keep security-domain confirmation, pricing locking and request-policy
// validation on their existing save paths instead of ordinary batch storage.
const SPECIALIZED_OPTION_KEYS = new Set([
  'ServerAddress',
  'passkey.rp_id',
  'passkey.legacy_rp_ids',
  'passkey.origins',
  'AudioCompletionRatio',
  'AudioRatio',
  'CacheRatio',
  'CompletionRatio',
  'CreateCacheRatio',
  'ImageRatio',
  'ModelPrice',
  'ModelRatio',
  'billing_setting.billing_expr',
  'billing_setting.billing_mode',
  'billing_setting.plugin_billing_expr',
  'AutomaticEnableChannelEnabled',
  'ChannelDisableThreshold',
  'monitor_setting.auto_test_channel_enabled',
  'monitor_setting.auto_test_channel_minutes',
  'monitor_setting.channel_test_concurrency',
  'monitor_setting.channel_test_mode',
  'RetryTimes',
  'UpstreamFirstByteTimeoutSeconds',
  'AutomaticRetryStatusCodes',
  'AutomaticDisableChannelEnabled',
  'AutomaticDisableStatusCodes',
  'AutomaticDisableKeywords',
])

function isSpecializedOption(key: string): boolean {
  return (
    SPECIALIZED_OPTION_KEYS.has(key) ||
    key.startsWith('channel_affinity_setting.')
  )
}

export function useUpdateOption() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (
      request: UpdateOptionRequest | UpdateOptionRequest[]
    ) => {
      if (!Array.isArray(request)) {
        return requireServerSuccess(await updateSystemOption(request))
      }
      const ordinary = request.filter(
        (option) => !isSpecializedOption(option.key)
      )
      // Domain/pricing/policy saves are deliberately not part of the ordinary
      // transaction. Their existing preview/confirmation and validation remain.
      for (const option of request.filter((option) =>
        isSpecializedOption(option.key)
      )) {
        requireServerSuccess(await updateSystemOption(option))
      }
      if (ordinary.length === 0) return { success: true, message: '' }
      return requireServerSuccess(await updateSystemOptionsBatch(ordinary))
    },
    onSuccess: (data, variables) => {
      if (Array.isArray(variables)) {
        if (variables.length === 0) return
        const saved = new Map(
          variables
            .filter((option) => !isSpecializedOption(option.key))
            .map((option) => [option.key, String(option.value)])
        )
        queryClient.setQueryData<SystemOptionsResponse>(
          ['system-options'],
          (previous) => {
            if (!previous?.data) return previous
            return {
              ...previous,
              data: previous.data.map((option) => {
                const value = saved.get(option.key)
                return value === undefined ? option : { ...option, value }
              }),
            }
          }
        )
        void queryClient.invalidateQueries({
          queryKey: ['system-options'],
          refetchType: variables.some((option) =>
            isSpecializedOption(option.key)
          )
            ? 'active'
            : 'none',
        })
        if (variables.some((option) => STATUS_RELATED_KEYS.has(option.key))) {
          void queryClient.invalidateQueries({ queryKey: ['status'] })
          try {
            window.localStorage.removeItem('status')
          } catch {
            /* Storage may be disabled. */
          }
        }
        toast.success(i18next.t('Setting updated successfully'))
        return
      }
      if (data.success) {
        // A single-field save commits exactly `variables.value`. Patch that one
        // cached entry so the form and every other settings surface see the
        // saved value without refetching the whole options payload. The query is
        // still marked stale so a later natural fetch reconciles it.
        const saved = String(variables.value)
        queryClient.setQueryData<SystemOptionsResponse>(
          ['system-options'],
          (previous) => {
            if (!previous?.data) return previous
            return {
              ...previous,
              data: previous.data.map((option) =>
                option.key === variables.key
                  ? { ...option, value: saved }
                  : option
              ),
            }
          }
        )
        queryClient.invalidateQueries({
          queryKey: ['system-options'],
          refetchType: 'none',
        })

        // If updating frontend-display-related config, also refresh status
        if (STATUS_RELATED_KEYS.has(variables.key)) {
          queryClient.invalidateQueries({ queryKey: ['status'] })
          try {
            window.localStorage.removeItem('status')
          } catch {
            /* empty */
          }
        }

        toast.success(i18next.t('Setting updated successfully'))
      } else {
        handleServerError(data, i18next.t('Failed to update setting'))
      }
    },
    onError: (error: Error, variables) => {
      if (
        Array.isArray(variables) &&
        variables.some((option) => isSpecializedOption(option.key))
      ) {
        // An earlier specialized save may have committed before another path
        // failed. Reconcile normalized results without pretending atomicity
        // across the separate security/pricing/policy workflows.
        void queryClient.invalidateQueries({ queryKey: ['system-options'] })
        void queryClient.invalidateQueries({ queryKey: ['status'] })
      }
      const failure = getServerErrorSources(error).find(
        (source) => typeof source.key === 'string'
      )
      if (typeof failure?.key === 'string') {
        handleServerError(error, undefined, {
          title: i18next.t('Could not save {{key}}: {{error}}', {
            key: failure.key,
            error: getServerErrorMessage(error),
            interpolation: { escapeValue: false },
            nest: false,
          }),
        })
        return
      }
      handleServerError(error, i18next.t('Failed to update setting'))
    },
  })
}

export function useUpdatePasskeyDomains() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (request: UpdatePasskeyDomainsRequest) => {
      const result = await updatePasskeyDomains(request)
      if (
        result.code === 'PASSKEY_RP_ID_REMOVAL_CONFIRMATION_REQUIRED' &&
        result.data
      ) {
        return result
      }
      return requireServerSuccess(result)
    },
    onSuccess: (result, request) => {
      if (request.preview || !result.success) return
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      queryClient.invalidateQueries({ queryKey: ['status'] })
      try {
        window.localStorage.removeItem('status')
      } catch {
        /* Storage may be disabled. */
      }
      toast.success(i18next.t('Setting updated successfully'))
    },
    onError: (error: Error) =>
      handleServerError(error, i18next.t('Failed to update setting')),
  })
}
