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
/* eslint-disable react-refresh/only-export-components */
import { useQueryClient } from '@tanstack/react-query'
import type { OnChangeFn, SortingState } from '@tanstack/react-table'
import React, {
  createContext,
  useContext,
  useState,
  useCallback,
  useMemo,
  useEffect,
} from 'react'

import { useChannelUpstreamUpdates } from '../hooks/use-channel-upstream-updates'
import { useChannelConcurrency } from '../hooks/use-channel-concurrency'
import { channelsQueryKeys } from '../lib'
import type { Channel } from '../types'
import { ChannelConcurrencyContext } from './channel-concurrency-context'

// ============================================================================
// Types
// ============================================================================

type DialogType =
  | 'create-channel'
  | 'update-channel'
  | 'test-channel'
  | 'balance-query'
  | 'fetch-models'
  | 'ollama-models'
  | 'inference-status'
  | 'channel-details'
  | 'multi-key-manage'
  | 'tag-batch-edit'
  | 'edit-tag'
  | 'copy-channel'
  | null

type UpstreamUpdateState = ReturnType<typeof useChannelUpstreamUpdates>

type ChannelsContextType = {
  open: DialogType
  setOpen: (open: DialogType) => void
  currentRow: Channel | null
  setCurrentRow: (row: Channel | null) => void
  currentTag: string | null
  setCurrentTag: (tag: string | null) => void
  enableTagMode: boolean
  setEnableTagMode: (enabled: boolean) => void
  sorting: SortingState
  setSorting: OnChangeFn<SortingState>
  batchMode: boolean
  setBatchMode: (enabled: boolean) => void
  sensitiveVisible: boolean
  setSensitiveVisible: (visible: boolean) => void
  priorityActionContainer: HTMLDivElement | null
  setPriorityActionContainer: (container: HTMLDivElement | null) => void
  upstream: UpstreamUpdateState
  /**
   * Publishes the channel IDs the cards currently show. The provider owns the
   * single live-concurrency subscription so cards and the details drawer read
   * one sample instead of each polling on its own.
   */
  setConcurrencyIds: (ids: number[]) => void
}

// ============================================================================
// Context
// ============================================================================

const ChannelsContext = createContext<ChannelsContextType | undefined>(
  undefined
)

// ============================================================================
// Provider
// ============================================================================

export function ChannelsProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState<DialogType>(null)
  const [currentRow, setCurrentRow] = useState<Channel | null>(null)
  const [currentTag, setCurrentTag] = useState<string | null>(null)
  const [enableTagMode, setEnableTagMode] = useState(() => {
    return localStorage.getItem('enable-tag-mode') === 'true'
  })
  const [sorting, setSorting] = useState<SortingState>(() => [
    {
      id:
        localStorage.getItem('channels-id-sort') === 'true' ? 'id' : 'priority',
      desc: true,
    },
  ])
  const idSort = sorting[0]?.id === 'id'
  useEffect(() => {
    // Retain the existing ID-sort preference for returning users.
    localStorage.setItem('channels-id-sort', String(idSort))
  }, [idSort])
  const [batchMode, setBatchMode] = useState(false)
  const [sensitiveVisible, setSensitiveVisible] = useState(true)
  const [priorityActionContainer, setPriorityActionContainer] =
    useState<HTMLDivElement | null>(null)
  const [concurrencyIds, setConcurrencyIdsState] = useState<number[]>([])
  // Keyed setter: the table republishes ids on every data render, so it must
  // only cause a render when the actual visible set changed.
  const setConcurrencyIds = useCallback((ids: number[]) => {
    setConcurrencyIdsState((previous) => {
      if (previous.length === ids.length) {
        const same = previous.every((id, index) => id === ids[index])
        if (same) return previous
      }
      return ids
    })
  }, [])

  const queryClient = useQueryClient()
  const refreshChannels = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
  }, [queryClient])
  const upstream = useChannelUpstreamUpdates(refreshChannels)
  // The open drawer's channel joins the visible set, so opening details on a
  // visible card reuses the sample already on screen.
  const concurrency = useChannelConcurrency(concurrencyIds)

  // useState setters are stable, so the context value only needs to change when
  // an actual state value changes. Memoizing avoids handing every consumer
  // (including all channel cards/cells) a brand-new object on each render.
  const value = useMemo<ChannelsContextType>(
    () => ({
      open,
      setOpen,
      currentRow,
      setCurrentRow,
      currentTag,
      setCurrentTag,
      enableTagMode,
      setEnableTagMode,
      sorting,
      setSorting,
      batchMode,
      setBatchMode,
      sensitiveVisible,
      setSensitiveVisible,
      priorityActionContainer,
      setPriorityActionContainer,
      upstream,
      setConcurrencyIds,
    }),
    [
      open,
      currentRow,
      currentTag,
      enableTagMode,
      sorting,
      batchMode,
      sensitiveVisible,
      priorityActionContainer,
      upstream,
      setConcurrencyIds,
    ]
  )

  const concurrencyValue = useMemo(
    () => ({
      entriesById: concurrency.entriesById,
      isPending: concurrency.isPending,
      stale: concurrency.stale,
    }),
    [concurrency.entriesById, concurrency.isPending, concurrency.stale]
  )

  return (
    <ChannelsContext.Provider value={value}>
      <ChannelConcurrencyContext.Provider value={concurrencyValue}>
        {children}
      </ChannelConcurrencyContext.Provider>
    </ChannelsContext.Provider>
  )
}

// ============================================================================
// Hook
// ============================================================================

export function useChannels() {
  const context = useContext(ChannelsContext)
  if (!context) {
    throw new Error('useChannels must be used within ChannelsProvider')
  }
  return context
}
