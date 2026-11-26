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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface OfficialStatusIncident {
  id: string
  name: string
  status: string
  updated_at: string
}

export const OFFICIAL_STATUS_PROVIDERS = [
  { id: 'openai', name: 'OpenAI', source_url: 'https://status.openai.com/' },
  {
    id: 'anthropic',
    name: 'Anthropic',
    source_url: 'https://status.claude.com/',
  },
  {
    id: 'gemini',
    name: 'Gemini API',
    source_url: 'https://aistudio.google.com/status',
  },
  { id: 'xai', name: 'xAI', source_url: 'https://status.x.ai/' },
  {
    id: 'deepseek',
    name: 'DeepSeek',
    source_url: 'https://status.deepseek.com/',
  },
  {
    id: 'moonshot',
    name: 'Moonshot',
    source_url: 'https://status.moonshot.cn/',
  },
  { id: 'minimax', name: 'MiniMax', source_url: 'https://status.minimax.io/' },
  { id: 'mistral', name: 'Mistral', source_url: 'https://status.mistral.ai/' },
] as const

export interface OfficialProviderStatus {
  id: string
  name: string
  source_url: string
  status: string
  source_updated_at: string
  fetched_at: number
  checked_at: number
  stale: boolean
  unavailable: boolean
  fetch_error?: string
  fetch_http_status?: number
  components: { id: string; name: string; status: string }[] | null
  incidents: OfficialStatusIncident[] | null
  recent_incidents?: OfficialStatusIncident[] | null
  history_fetched_at?: number
  history_checked_at?: number
  history_stale?: boolean
  history_unavailable?: boolean
  history_error?: string
  history_http_status?: number
}

export async function getOfficialServiceStatus(signal?: AbortSignal) {
  const response = await api.get<{
    success: boolean
    message?: string
    data: OfficialProviderStatus[]
  }>('/api/official-status', { signal })
  return requireServerSuccess(response.data).data
}
