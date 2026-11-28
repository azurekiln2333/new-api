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
import { useQuery } from '@tanstack/react-query'
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
import { useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getPlaygroundChannels, getUserGroups, getUserModels } from '../api'
import {
  canSelectPlaygroundChannel,
  getGroupFallback,
  getModelFallback,
  getOptionLoadErrorMessage,
  shouldClearModelForGroup,
} from '../lib'
import type { GroupOption, ModelOption, PlaygroundConfig } from '../types'

const EMPTY_MODELS: ModelOption[] = []
const EMPTY_GROUPS: GroupOption[] = []

type UsePlaygroundOptionsParams = {
  currentGroup: string
  currentModel: string
  currentChannel?: number
  setGroups: (groups: GroupOption[]) => void
  setModels: (models: ModelOption[]) => void
  updateConfig: <K extends keyof PlaygroundConfig>(
    key: K,
    value: PlaygroundConfig[K]
  ) => void
}

export function usePlaygroundOptions({
  currentGroup,
  currentModel,
  currentChannel,
  setGroups,
  setModels,
  updateConfig,
}: UsePlaygroundOptionsParams) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canSelectChannel = canSelectPlaygroundChannel(user)
  const channelsQuery = useQuery({
    queryKey: [
      'playground-channels',
      user?.id,
      user?.role,
      canSelectChannel,
      currentGroup,
    ],
    queryFn: ({ signal }) => getPlaygroundChannels(currentGroup, signal),
    enabled: canSelectChannel && currentGroup !== '',
    staleTime: 30_000,
    meta: { errorToast: false },
  })
  const selectedChannel = canSelectChannel
    ? channelsQuery.data?.find((channel) => channel.id === currentChannel)
    : undefined
  const channels = useMemo(
    () =>
      canSelectChannel && !channelsQuery.isError
        ? channelsQuery.data?.map((channel) => ({
            label: channel.name,
            value: channel.id,
          }))
        : undefined,
    [canSelectChannel, channelsQuery.data, channelsQuery.isError]
  )
  const channelOptions = canSelectChannel
    ? {
        channels: channels ?? [],
        isPending: currentGroup === '' || channelsQuery.isPending,
        error: channelsQuery.isError
          ? getServerErrorMessage(
              channelsQuery.error,
              t('Failed to load playground channels')
            )
          : undefined,
        onRetry: () => {
          void channelsQuery.refetch()
        },
      }
    : undefined

  const {
    data: modelsData,
    error: modelsError,
    isError: isModelsError,
    isLoading: isLoadingModels,
  } = useQuery({
    queryKey: [
      'playground-models',
      user?.id,
      user?.role,
      canSelectChannel,
      currentGroup,
    ],
    queryFn: async ({ signal }) =>
      requireServerSuccess(await getUserModels(currentGroup, signal)),
    enabled: !!user && currentGroup !== '',
  })

  const {
    data: groupsData,
    error: groupsError,
    isError: isGroupsError,
  } = useQuery({
    queryKey: ['playground-groups', user?.id, user?.role, canSelectChannel],
    queryFn: async ({ signal }) =>
      requireServerSuccess(await getUserGroups(signal)),
    enabled: !!user,
  })
  const groups =
    user && !isGroupsError ? (groupsData ?? EMPTY_GROUPS) : EMPTY_GROUPS

  // Rendering uses the current query source, not the state hook's previous
  // catalog. Its effects may synchronize copies only after this commit.
  const availableModels = useMemo(() => {
    if (!user) return EMPTY_MODELS
    if (!currentChannel) {
      return isModelsError ? EMPTY_MODELS : (modelsData ?? EMPTY_MODELS)
    }
    if (!canSelectChannel || channelsQuery.isError || !selectedChannel) {
      return EMPTY_MODELS
    }
    return selectedChannel.models.map((model) => ({
      label: model,
      value: model,
    }))
  }, [
    canSelectChannel,
    channelsQuery.isError,
    currentChannel,
    isModelsError,
    modelsData,
    selectedChannel,
    user,
  ])
  const channelUnavailable = Boolean(
    currentChannel &&
    (!canSelectChannel ||
      channelsQuery.isError ||
      (!channelsQuery.isPending && !selectedChannel))
  )

  useEffect(() => {
    if (canSelectChannel && channelsQuery.isError) {
      handleServerError(
        channelsQuery.error,
        t('Failed to load playground channels')
      )
    }
  }, [canSelectChannel, channelsQuery.error, channelsQuery.isError, t])

  useEffect(() => {
    if (!isModelsError) return

    handleServerError(
      modelsError,
      getOptionLoadErrorMessage(
        modelsError,
        t('Failed to load playground models')
      )
    )
  }, [isModelsError, modelsError, t])

  useEffect(() => {
    if (!isGroupsError) return

    handleServerError(
      groupsError,
      getOptionLoadErrorMessage(
        groupsError,
        t('Failed to load playground groups')
      )
    )
  }, [isGroupsError, groupsError, t])

  useEffect(() => {
    setModels(availableModels)
    if (!currentChannel && (!modelsData || isModelsError)) return
    if (currentChannel && (!selectedChannel || channelUnavailable)) return
    const fallback = getModelFallback(availableModels, currentModel)

    if (fallback) {
      updateConfig('model', fallback)
      return
    }

    if (shouldClearModelForGroup(availableModels, currentModel)) {
      updateConfig('model', '')
    }
  }, [
    availableModels,
    channelUnavailable,
    currentChannel,
    selectedChannel,
    currentModel,
    isModelsError,
    modelsData,
    setModels,
    updateConfig,
  ])

  useEffect(() => {
    setGroups(groups)
    if (!groupsData || isGroupsError) return

    const fallback = getGroupFallback(groups, currentGroup)

    if (fallback) {
      updateConfig('group', fallback)
    }
  }, [groups, groupsData, isGroupsError, currentGroup, setGroups, updateConfig])

  return {
    models: availableModels,
    groups,
    isLoadingModels,
    channels,
    channelOptions,
    channelUnavailable,
    canSend: Boolean(
      !isLoadingModels &&
      !channelUnavailable &&
      availableModels.some((model) => model.value === currentModel)
    ),
  }
}
