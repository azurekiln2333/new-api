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
import { act, renderHook } from '@testing-library/react'
import type { PropsWithChildren } from 'react'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ApiKeysProvider, useApiKeys } from '../api-keys-provider'

function wrapper(props: PropsWithChildren) {
  return <ApiKeysProvider>{props.children}</ApiKeysProvider>
}

function pendingReply() {
  let resolve!: (reply: { data: unknown }) => void
  const promise = new Promise<{ data: unknown }>((finish) => {
    resolve = finish
  })
  return { promise, resolve }
}

it('drops a cached credential immediately when rotation invalidates it', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({ data: { success: true, data: { key: 'old' } } })
    .mockResolvedValueOnce({ data: { success: true, data: { key: 'new' } } })
  const { result } = renderHook(useApiKeys, { wrapper })
  await act(async () => {
    await result.current.resolveRealKey(7)
  })
  expect(result.current.resolvedKeys[7]).toBe('sk-old')
  await act(async () => {
    result.current.forgetResolvedKey(7)
    expect(await result.current.resolveRealKey(7)).toBe('sk-new')
  })
  expect(result.current.resolvedKeys[7]).toBe('sk-new')
  expect(post).toHaveBeenCalledTimes(2)
})

it('does not let an old reveal clear or replace the reveal started after rotation', async () => {
  const old = pendingReply()
  const fresh = pendingReply()
  const post = vi
    .spyOn(api, 'post')
    .mockReturnValueOnce(old.promise)
    .mockReturnValueOnce(fresh.promise)
  const { result } = renderHook(useApiKeys, { wrapper })
  let first!: Promise<string | null>
  let second!: Promise<string | null>
  act(() => {
    first = result.current.resolveRealKey(7)
  })
  act(() => {
    result.current.forgetResolvedKey(7)
    second = result.current.resolveRealKey(7)
  })
  await act(async () => {
    old.resolve({ data: { success: true, data: { key: 'old' } } })
    expect(await first).toBeNull()
  })
  expect(result.current.loadingKeys[7]).toBe(true)
  let duplicate!: Promise<string | null>
  act(() => {
    duplicate = result.current.resolveRealKey(7)
  })
  expect(post).toHaveBeenCalledTimes(2)
  await act(async () => {
    fresh.resolve({ data: { success: true, data: { key: 'new' } } })
    expect(await second).toBe('sk-new')
    expect(await duplicate).toBe('sk-new')
  })
  expect(result.current.resolvedKeys[7]).toBe('sk-new')
})

it('excludes a rotated cached credential from an in-flight mixed batch reveal', async () => {
  const batch = pendingReply()
  vi.spyOn(api, 'post')
    .mockResolvedValueOnce({ data: { success: true, data: { key: 'old' } } })
    .mockReturnValueOnce(batch.promise)
  const { result } = renderHook(useApiKeys, { wrapper })
  await act(async () => {
    await result.current.resolveRealKey(7)
  })
  let pending!: Promise<Record<number, string>>
  act(() => {
    pending = result.current.resolveRealKeysBatch([7, 8])
  })
  act(() => {
    result.current.forgetResolvedKey(7)
  })
  await act(async () => {
    batch.resolve({ data: { success: true, data: { keys: { 8: 'other' } } } })
    expect(await pending).toEqual({ 8: 'sk-other' })
  })
  expect(result.current.resolvedKeys[7]).toBeUndefined()
})

it('excludes a retired credential from an in-flight uncached batch reveal', async () => {
  const batch = pendingReply()
  vi.spyOn(api, 'post').mockReturnValueOnce(batch.promise)
  const { result } = renderHook(useApiKeys, { wrapper })
  let pending!: Promise<Record<number, string>>
  act(() => {
    pending = result.current.resolveRealKeysBatch([7, 8])
  })
  act(() => {
    result.current.forgetResolvedKey(7)
  })
  await act(async () => {
    batch.resolve({
      data: { success: true, data: { keys: { 7: 'old', 8: 'other' } } },
    })
    expect(await pending).toEqual({ 8: 'sk-other' })
  })
  expect(result.current.resolvedKeys[7]).toBeUndefined()
})
