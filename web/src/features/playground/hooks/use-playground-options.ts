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

import { hasPermission } from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getPlaygroundChannels, getUserGroups, getUserModels } from '../api'
import {
  getGroupFallback,
  getModelFallback,
  getOptionLoadErrorMessage,
  shouldClearModelForGroup,
} from '../lib'
import type { GroupOption, ModelOption, PlaygroundConfig } from '../types'

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
  const canSelectChannel =
    (user?.role ?? 0) >= ROLE.ADMIN && hasPermission(user, 'channel', 'read')
  const channelsQuery = useQuery({
    queryKey: ['playground-channels', user?.id, currentGroup],
    queryFn: () => getPlaygroundChannels(currentGroup),
    enabled: canSelectChannel && currentGroup !== '',
    staleTime: 30_000,
  })
  const selectedChannel = channelsQuery.data?.find(
    (channel) => channel.id === currentChannel
  )
  const channels = useMemo(
    () =>
      canSelectChannel
        ? (channelsQuery.data ?? []).map((channel) => ({
            label: channel.name,
            value: channel.id,
          }))
        : undefined,
    [canSelectChannel, channelsQuery.data]
  )

  const {
    data: modelsData,
    error: modelsError,
    isError: isModelsError,
    isLoading: isLoadingModels,
  } = useQuery({
    queryKey: ['playground-models', user?.id, currentGroup],
    queryFn: async () =>
      requireServerSuccess(await getUserModels(currentGroup)),
    enabled: currentGroup !== '',
  })

  const {
    data: groupsData,
    error: groupsError,
    isError: isGroupsError,
  } = useQuery({
    queryKey: ['playground-groups', user?.id],
    queryFn: async () => requireServerSuccess(await getUserGroups()),
  })

  const availableModels = useMemo(() => {
    if (!currentChannel) return modelsData
    if (!canSelectChannel || channelsQuery.isError || !selectedChannel) {
      return []
    }
    return selectedChannel.models.map((model) => ({
      label: model,
      value: model,
    }))
  }, [
    canSelectChannel,
    channelsQuery.isError,
    currentChannel,
    modelsData,
    selectedChannel,
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
    if (!availableModels) return

    setModels(availableModels)
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
    setModels,
    updateConfig,
  ])

  useEffect(() => {
    if (!groupsData) return

    setGroups(groupsData)
    const fallback = getGroupFallback(groupsData, currentGroup)

    if (fallback) {
      updateConfig('group', fallback)
    }
  }, [groupsData, currentGroup, setGroups, updateConfig])

  return {
    isLoadingModels,
    channels,
    channelUnavailable,
    canSend: Boolean(
      !isLoadingModels &&
      !channelUnavailable &&
      availableModels?.some((model) => model.value === currentModel)
    ),
  }
}
