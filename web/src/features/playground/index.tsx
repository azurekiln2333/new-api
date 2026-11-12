import { PanelLeftIcon } from 'lucide-react'
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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import { PlaygroundChat } from './components/chat/playground-chat'
import {
  ConversationPanel,
  StorageWarningBanner,
} from './components/conversations'
import { PlaygroundInput } from './components/input/playground-input'
import {
  useChatHandler,
  usePlaygroundConversation,
  usePlaygroundOptions,
  usePlaygroundState,
} from './hooks'

export function Playground() {
  const { t } = useTranslation()
  const {
    config,
    parameterEnabled,
    conversations,
    activeConversationId,
    messages,
    isLoadingMessages,
    storageFailure,
    models,
    groups,
    updateMessages,
    setModels,
    setGroups,
    updateConfig,
    updateParameterEnabled,
    clearMessages,
    createConversation,
    switchConversation,
    renameConversation,
    deleteConversation,
    archiveConversation,
    dismissStorageFailure,
  } = usePlaygroundState()
  const [isHistoryOpen, setIsHistoryOpen] = useState(false)

  const { sendChat, stopGeneration, isGenerating } = useChatHandler({
    config,
    parameterEnabled,
    onMessageUpdate: updateMessages,
  })

  const {
    editingMessageKey,
    handleSendMessage,
    handleRegenerateMessage,
    handleEditMessage,
    handleEditOpenChange,
    applyEdit,
    handleDeleteMessage,
  } = usePlaygroundConversation({
    messages,
    updateMessages,
    sendChat,
  })

  const handleClearMessages = () => {
    handleEditOpenChange(false)
    clearMessages()
  }

  const { isLoadingModels } = usePlaygroundOptions({
    currentGroup: config.group,
    currentModel: config.model,
    setGroups,
    setModels,
    updateConfig,
  })

  // A reply streams into the conversation that is active when it starts, so a
  // conversation switch stops the generation instead of mixing the two chats.
  const runConversationAction = (action: () => void) => {
    if (isGenerating) stopGeneration()
    handleEditOpenChange(false)
    action()
    setIsHistoryOpen(false)
  }

  const handleSelectConversation = (conversationId: string) => {
    if (conversationId === activeConversationId) {
      setIsHistoryOpen(false)
      return
    }

    runConversationAction(() => switchConversation(conversationId))
  }

  const handleCreateConversation = () => {
    runConversationAction(() => {
      createConversation()
    })
  }

  const handleDeleteConversation = (conversationId: string) => {
    runConversationAction(() => deleteConversation(conversationId))
  }

  const handleArchiveConversation = (
    conversationId: string,
    archived: boolean
  ) => {
    runConversationAction(() => archiveConversation(conversationId, archived))
  }

  const activeConversation = conversations.find(
    (conversation) => conversation.id === activeConversationId
  )
  const activeTitle = activeConversation?.title || t('New chat')

  const conversationPanel = (
    <ConversationPanel
      activeConversationId={activeConversationId}
      conversations={conversations}
      onArchive={handleArchiveConversation}
      onCreate={handleCreateConversation}
      onDelete={handleDeleteConversation}
      onRename={renameConversation}
      onSelect={handleSelectConversation}
    />
  )

  return (
    <div className='flex size-full min-h-0 overflow-hidden'>
      <aside className='hidden w-64 shrink-0 flex-col border-e lg:flex'>
        {conversationPanel}
      </aside>

      <div className='flex min-w-0 flex-1 flex-col overflow-hidden'>
        <div className='flex items-center gap-2 border-b px-3 py-1.5 lg:hidden'>
          <Button
            aria-label={t('Open chat list')}
            size='icon'
            type='button'
            variant='ghost'
            onClick={() => setIsHistoryOpen(true)}
          >
            <PanelLeftIcon className='size-4' />
          </Button>
          <span className='min-w-0 flex-1 truncate text-sm font-medium'>
            {activeTitle}
          </span>
        </div>

        <StorageWarningBanner
          failure={storageFailure}
          onDismiss={dismissStorageFailure}
        />

        {/* Full-width scroll container: scrolling works even over side whitespace */}
        <div className='flex min-h-0 flex-1 flex-col overflow-hidden'>
          <PlaygroundChat
            messages={messages}
            isLoadingMessages={isLoadingMessages}
            onRegenerateMessage={handleRegenerateMessage}
            onEditMessage={handleEditMessage}
            onDeleteMessage={handleDeleteMessage}
            onSelectPrompt={handleSendMessage}
            isGenerating={isGenerating}
            editingKey={editingMessageKey}
            onCancelEdit={handleEditOpenChange}
            onSaveEdit={(newContent) => applyEdit(newContent, false)}
            onSaveEditAndSubmit={(newContent) => applyEdit(newContent, true)}
          />
        </div>

        {/* Input area: center content and constrain to the same container width */}
        <div className='mx-auto w-full max-w-4xl'>
          <PlaygroundInput
            config={config}
            disabled={isGenerating}
            groups={groups}
            groupValue={config.group}
            isGenerating={isGenerating}
            isModelLoading={isLoadingModels}
            modelValue={config.model}
            models={models}
            onGroupChange={(value) => updateConfig('group', value)}
            onConfigChange={updateConfig}
            onClearMessages={handleClearMessages}
            onModelChange={(value) => updateConfig('model', value)}
            onParameterEnabledChange={updateParameterEnabled}
            onStop={stopGeneration}
            onSubmit={handleSendMessage}
            parameterEnabled={parameterEnabled}
            hasMessages={messages.length > 0}
          />
        </div>
      </div>

      <Sheet open={isHistoryOpen} onOpenChange={setIsHistoryOpen}>
        <SheetContent side='left' className='w-72 gap-0 sm:max-w-xs'>
          <SheetHeader className='px-3 pt-3'>
            <SheetTitle>{t('Chat history')}</SheetTitle>
          </SheetHeader>
          <div className='flex min-h-0 flex-1 flex-col'>
            {conversationPanel}
          </div>
        </SheetContent>
      </Sheet>
    </div>
  )
}
