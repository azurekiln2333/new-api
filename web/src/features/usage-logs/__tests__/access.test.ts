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
import assert from 'node:assert/strict'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'

import { resolveLogsViewAccess } from '../components/usage-logs-provider'
import {
  buildApiParams,
  buildLogStatsParams,
  fetchLogsByCategory,
} from '../lib/utils'
import type { AdminLogUserScope } from '../types'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('usage log access tier', () => {
  test('derives viewer capabilities only from the authenticated role', () => {
    assert.equal(resolveLogsViewAccess(ROLE.USER), 'self')
    assert.equal(resolveLogsViewAccess(ROLE.ADMIN), 'admin')
    assert.equal(resolveLogsViewAccess(ROLE.SUPER_ADMIN), 'root')
  })

  for (const userScope of ['all', 'self', 'others'] as const) {
    test(`shares the administrator ${userScope} predicate with statistics`, () => {
      const config = {
        isAdmin: true,
        userScope,
        searchParams: {
          channel: '42',
          username: 'alice',
          group: 'premium',
          model: 'gpt',
          token: 'prod',
          requestId: 'req-1',
          upstreamRequestId: 'up-1',
          startTime: 1_700_000_000_000,
          endTime: 1_700_000_060_000,
          match: 'exact',
        },
      }
      const { p, page_size, ...pagePredicate } = buildApiParams({
        ...config,
        page: 3,
        pageSize: 50,
      })
      assert.equal(p, 3)
      assert.equal(page_size, 50)
      assert.equal(pagePredicate.user_scope, userScope)
      assert.equal(pagePredicate.channel, 42)
      assert.equal(pagePredicate.username, 'alice')
      assert.deepEqual(buildLogStatsParams(config), pagePredicate)
    })
  }

  test.each([
    { logCategory: 'common', isAdmin: true, endpoint: '/api/log' },
    { logCategory: 'common', isAdmin: false, endpoint: '/api/log/self' },
    { logCategory: 'drawing', isAdmin: true, endpoint: '/api/mj' },
    { logCategory: 'drawing', isAdmin: false, endpoint: '/api/mj/self' },
    { logCategory: 'task', isAdmin: true, endpoint: '/api/task' },
    { logCategory: 'task', isAdmin: false, endpoint: '/api/task/self' },
  ] as const)(
    'forwards list cancellation to $endpoint for $logCategory logs',
    async ({ logCategory, isAdmin, endpoint }) => {
      const controller = new AbortController()
      const get = vi.spyOn(api, 'get').mockResolvedValue({
        data: { success: true, data: { items: [], total: 0 } },
      })

      await fetchLogsByCategory({
        logCategory,
        isAdmin,
        userScope: 'others',
        page: 1,
        pageSize: 20,
        searchParams: { username: 'other-user', channel: '42' },
        columnFilters: [],
        signal: controller.signal,
      })

      expect(get).toHaveBeenCalledOnce()
      const [url, config] = get.mock.calls[0]
      const requestUrl = new URL(url, 'http://localhost')
      expect(requestUrl.pathname).toBe(endpoint)
      expect(config?.signal).toBe(controller.signal)
      if (logCategory === 'common') {
        const params = requestUrl.searchParams
        expect(params.get('user_scope')).toBe(isAdmin ? 'others' : null)
        expect(params.get('username')).toBe(isAdmin ? 'other-user' : null)
        expect(params.get('channel')).toBe(isAdmin ? '42' : null)
      }
    }
  )

  test('never sends administrator-only predicates from an ordinary view', () => {
    for (const userScope of ['all', 'self', 'others'] as AdminLogUserScope[]) {
      const params = buildApiParams({
        page: 1,
        pageSize: 50,
        isAdmin: false,
        userScope,
        searchParams: { channel: '42', username: 'someone-else' },
        columnFilters: [
          { id: 'channel', value: '99' },
          { id: 'username', value: 'another' },
        ],
      })
      assert.equal('user_scope' in params, false)
      assert.equal('channel' in params, false)
      assert.equal('username' in params, false)
    }
  })
})
