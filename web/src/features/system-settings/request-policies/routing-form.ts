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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseHttpStatusCodeRules } from '@/lib/http-status-code-rules'

export function createRoutingPolicySchema(t: TFunction) {
  const failureThresholdMessage = t(
    'affinity failure threshold must be an integer between 0 and 1000000'
  )
  const timeoutMessage = t(
    'Upstream first-byte timeout must be an integer between 0 and 86400'
  )
  return z.object({
    RetryTimes: z.number().int().min(0).max(99),
    UpstreamFirstByteTimeoutSeconds: z
      .number({ error: timeoutMessage })
      .int(timeoutMessage)
      .min(0, timeoutMessage)
      .max(86400, timeoutMessage),
    AutomaticRetryStatusCodes: z
      .string()
      .refine(
        (value) => parseHttpStatusCodeRules(value).ok,
        t('Invalid status code rules')
      ),
    channel_affinity_setting: z.object({
      enabled: z.boolean(),
      retry_avoid_failed_channels: z.boolean(),
      failure_threshold: z
        .number({ error: failureThresholdMessage })
        .int(failureThresholdMessage)
        .min(0, failureThresholdMessage)
        .max(1000000, failureThresholdMessage),
      session_mode: z.enum(['', 'off', 'prefer', 'strict']),
      switch_on_success: z.boolean(),
      keep_on_channel_disabled: z.boolean(),
      max_entries: z.number().int().min(0),
      default_ttl_seconds: z.number().int().min(0),
      rules: z.string().superRefine((value, context) => {
        try {
          const rules: unknown = JSON.parse(value)
          if (!Array.isArray(rules)) {
            context.addIssue({
              code: 'custom',
              message: t('Rules JSON must be an array'),
            })
          }
        } catch {
          context.addIssue({
            code: 'custom',
            message: t('Invalid rules JSON format'),
          })
        }
      }),
    }),
  })
}

export type RoutingPolicyFormValues = z.infer<
  ReturnType<typeof createRoutingPolicySchema>
>

export function routingPolicyFormValues(
  options: Record<string, string>
): RoutingPolicyFormValues {
  return {
    RetryTimes: Number(options.RetryTimes ?? '2'),
    UpstreamFirstByteTimeoutSeconds: Number(
      options.UpstreamFirstByteTimeoutSeconds ?? '0'
    ),
    AutomaticRetryStatusCodes: options.AutomaticRetryStatusCodes,
    channel_affinity_setting: {
      enabled: options['channel_affinity_setting.enabled'] === 'true',
      retry_avoid_failed_channels:
        options['channel_affinity_setting.retry_avoid_failed_channels'] !==
        'false',
      failure_threshold: Number(
        options['channel_affinity_setting.failure_threshold'] ?? '1'
      ),
      session_mode: (options['channel_affinity_setting.session_mode'] ||
        '') as RoutingPolicyFormValues['channel_affinity_setting']['session_mode'],
      switch_on_success:
        options['channel_affinity_setting.switch_on_success'] === 'true',
      keep_on_channel_disabled:
        options['channel_affinity_setting.keep_on_channel_disabled'] === 'true',
      max_entries: Number(options['channel_affinity_setting.max_entries']),
      default_ttl_seconds: Number(
        options['channel_affinity_setting.default_ttl_seconds']
      ),
      rules: options['channel_affinity_setting.rules'] || '[]',
    },
  }
}

export function routingPolicyOptions(
  values: RoutingPolicyFormValues
): Record<string, string> {
  return {
    RetryTimes: String(values.RetryTimes),
    UpstreamFirstByteTimeoutSeconds: String(
      values.UpstreamFirstByteTimeoutSeconds
    ),
    AutomaticRetryStatusCodes: values.AutomaticRetryStatusCodes,
    ...Object.fromEntries(
      Object.entries(values.channel_affinity_setting).map(([key, value]) => [
        `channel_affinity_setting.${key}`,
        String(value),
      ])
    ),
  }
}
