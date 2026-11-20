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
import React, { useState, useCallback, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import useDialogState from '@/hooks/use-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { fetchTokenKey, fetchTokenKeysBatch } from '../api'
import { ERROR_MESSAGES } from '../constants'
import type { ApiKey, ApiKeysDialogType } from '../types'

type ApiKeysContextType = {
  open: ApiKeysDialogType | null
  setOpen: (str: ApiKeysDialogType | null) => void
  currentRow: ApiKey | null
  setCurrentRow: React.Dispatch<React.SetStateAction<ApiKey | null>>
  refreshTrigger: number
  triggerRefresh: () => void
  resolvedKey: string
  setResolvedKey: React.Dispatch<React.SetStateAction<string>>
  resolveRealKey: (id: number) => Promise<string | null>
  resolveRealKeysBatch: (ids: number[]) => Promise<Record<number, string>>
  resolvedKeys: Record<number, string>
  forgetResolvedKey: (id: number) => void
  loadingKeys: Record<number, boolean>
  copiedKeyId: number | null
  markKeyCopied: (id: number) => void
}

const ApiKeysContext = React.createContext<ApiKeysContextType | null>(null)

export function ApiKeysProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const [open, setOpen] = useDialogState<ApiKeysDialogType>(null)
  const [currentRow, setCurrentRow] = useState<ApiKey | null>(null)
  const [refreshTrigger, setRefreshTrigger] = useState(0)
  const [resolvedKey, setResolvedKey] = useState('')

  const [resolvedKeys, setResolvedKeys] = useState<Record<number, string>>({})
  const resolvedKeysRef = useRef<Record<number, string>>({})
  const [loadingKeys, setLoadingKeys] = useState<Record<number, boolean>>({})
  const pendingRequests = useRef<Record<number, Promise<string | null>>>({})
  // A monotonic epoch per token invalidates cached and in-flight reveals. A
  // rotation bumps it so a reveal started against the old credential cannot
  // publish that credential after the replacement was issued.
  const keyEpoch = useRef<Record<number, number>>({})

  const [copiedKeyId, setCopiedKeyId] = useState<number | null>(null)
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)

  useEffect(() => {
    return () => clearTimeout(copiedTimerRef.current)
  }, [])

  const markKeyCopied = useCallback((id: number) => {
    setCopiedKeyId(id)
    clearTimeout(copiedTimerRef.current)
    copiedTimerRef.current = setTimeout(() => setCopiedKeyId(null), 2000)
  }, [])

  const triggerRefresh = useCallback(() => {
    setRefreshTrigger((prev) => prev + 1)
  }, [])

  const forgetResolvedKey = useCallback((id: number) => {
    keyEpoch.current[id] = (keyEpoch.current[id] ?? 0) + 1
    delete pendingRequests.current[id]
    const next = { ...resolvedKeysRef.current }
    delete next[id]
    resolvedKeysRef.current = next
    setResolvedKeys(next)
    setResolvedKey('')
    setCopiedKeyId((current) => (current === id ? null : current))
    setLoadingKeys((prev) => {
      const loading = { ...prev }
      delete loading[id]
      return loading
    })
  }, [])

  const resolveRealKey = useCallback(
    async (id: number): Promise<string | null> => {
      if (resolvedKeysRef.current[id]) return resolvedKeysRef.current[id]
      if (id in pendingRequests.current) return pendingRequests.current[id]
      const epoch = keyEpoch.current[id] ?? 0

      const request = (async () => {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
        try {
          const res = await fetchTokenKey(id)
          if ((keyEpoch.current[id] ?? 0) !== epoch) return null
          if (res.success && res.data?.key) {
            const fullKey = `sk-${res.data.key}`
            resolvedKeysRef.current = {
              ...resolvedKeysRef.current,
              [id]: fullKey,
            }
            setResolvedKeys(resolvedKeysRef.current)
            return fullKey
          }
          handleServerError(res, t(ERROR_MESSAGES.UNEXPECTED))
          return null
        } catch (error) {
          if ((keyEpoch.current[id] ?? 0) === epoch) {
            handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
          }
          return null
        } finally {
          if ((keyEpoch.current[id] ?? 0) === epoch) {
            delete pendingRequests.current[id]
            setLoadingKeys((prev) => {
              const next = { ...prev }
              delete next[id]
              return next
            })
          }
        }
      })()

      pendingRequests.current[id] = request
      return request
    },
    [t]
  )

  const resolveRealKeysBatch = useCallback(
    async (ids: number[]): Promise<Record<number, string>> => {
      const epochs: Record<number, number> = {}
      for (const id of ids) epochs[id] = keyEpoch.current[id] ?? 0
      const uncachedIds = ids.filter((id) => !resolvedKeysRef.current[id])
      if (uncachedIds.length === 0) {
        const result: Record<number, string> = {}
        for (const id of ids) result[id] = resolvedKeysRef.current[id]
        return result
      }

      for (const id of uncachedIds) {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
      }

      try {
        const res = await fetchTokenKeysBatch(uncachedIds)
        if (res.success && res.data?.keys) {
          const newKeys: Record<number, string> = {}
          for (const [idStr, key] of Object.entries(res.data.keys)) {
            const id = Number(idStr)
            if ((keyEpoch.current[id] ?? 0) !== epochs[id]) continue
            newKeys[id] = `sk-${key}`
          }
          resolvedKeysRef.current = { ...resolvedKeysRef.current, ...newKeys }
          setResolvedKeys(resolvedKeysRef.current)

          const result: Record<number, string> = { ...newKeys }
          for (const id of ids) {
            if ((keyEpoch.current[id] ?? 0) !== epochs[id]) continue
            if (resolvedKeysRef.current[id]) {
              result[id] = resolvedKeysRef.current[id]
            }
          }
          return result
        }
        handleServerError(res, t(ERROR_MESSAGES.UNEXPECTED))
        return {}
      } catch (error) {
        handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
        return {}
      } finally {
        for (const id of uncachedIds) {
          if ((keyEpoch.current[id] ?? 0) !== epochs[id]) continue
          setLoadingKeys((prev) => {
            const next = { ...prev }
            delete next[id]
            return next
          })
        }
      }
    },
    [t]
  )

  return (
    <ApiKeysContext
      value={{
        open,
        setOpen,
        currentRow,
        setCurrentRow,
        refreshTrigger,
        triggerRefresh,
        resolvedKey,
        setResolvedKey,
        resolveRealKey,
        resolveRealKeysBatch,
        resolvedKeys,
        forgetResolvedKey,
        loadingKeys,
        copiedKeyId,
        markKeyCopied,
      }}
    >
      {children}
    </ApiKeysContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useApiKeys = () => {
  const apiKeysContext = React.useContext(ApiKeysContext)

  if (!apiKeysContext) {
    throw new Error('useApiKeys has to be used within <ApiKeysContext>')
  }

  return apiKeysContext
}
