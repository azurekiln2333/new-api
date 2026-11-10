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
import * as z from 'zod'

// The server validates this list again before persisting it or trusting an origin.
export const oauthDomainsSchema = z.object({
  allowedOrigins: z.string().refine((value) => {
    if (!value.trim()) return true
    return value
      .split('\n')
      .filter((line) => line.trim())
      .every((line) =>
        line.split(',').every((entry) => {
          const raw = entry.trim()
          if (!raw || /[?#@*\\\s]/.test(raw)) return false
          try {
            const url = new URL(raw)
            if (!/^https?:\/\/[^/]+\/?$/.test(raw)) return false
            const loopback =
              url.hostname === 'localhost' ||
              url.hostname === '[::1]' ||
              /^127\.(\d{1,3}\.){2}\d{1,3}$/.test(url.hostname)
            return (
              url.protocol === 'https:' ||
              (url.protocol === 'http:' && loopback)
            )
          } catch {
            return false
          }
        })
      )
  }, 'Enter exact HTTPS websites without paths or wildcards'),
})

export type OAuthDomainsFormValues = z.infer<typeof oauthDomainsSchema>
