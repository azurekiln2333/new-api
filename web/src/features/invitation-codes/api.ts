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

export type Invitation = {
  id: number
  key: string
  name: string
  status: number
  created_time: number
  expired_time: number
  used_time: number
  used_user_id: number
}

export type InvitationInput = {
  name: string
  count: number
  expired_time: number
}

export async function listInvitations(
  page: number,
  pageSize: number,
  keyword: string,
  status: string
) {
  const response = await api.get<{
    success: boolean
    data: { items: Invitation[]; total: number }
  }>('/api/invitation/', {
    params: { p: page, page_size: pageSize, keyword, status },
  })
  requireServerSuccess(response.data)
  return response.data.data
}

export async function createInvitations(input: InvitationInput) {
  const response = await api.post<{ success: boolean; data: string[] }>(
    '/api/invitation/',
    input
  )
  requireServerSuccess(response.data)
  return response.data.data
}

export async function updateInvitation(input: {
  id: number
  name?: string
  expired_time?: number
  status?: number
}) {
  const response = await api.put('/api/invitation/', input)
  requireServerSuccess(response.data)
}

export async function deleteInvitations(ids: number[] | 'invalid') {
  const response =
    ids === 'invalid'
      ? await api.delete('/api/invitation/invalid')
      : await api.post('/api/invitation/batch', { ids })
  requireServerSuccess(response.data)
}
