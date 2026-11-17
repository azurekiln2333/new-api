// PI-3 throwaway fixture entry: mounts the REAL PlaygroundMessageContent inside
// the REAL chat column wrapper and exposes a hook so an external driver can feed
// incremental streaming content through real React updates.
import { createRoot } from 'react-dom/client'
import React, { useState } from 'react'

import { Message } from '@/components/ai-elements/message'

import { PlaygroundMessageContent } from '@/features/playground/components/message/playground-message-content'
import type {
  Message as MessageType,
  PlaygroundMessageLayoutMode,
} from '@/features/playground/types'

type FixtureState = {
  content: string
  from: MessageType['from']
  status: MessageType['status']
  layoutMode: PlaygroundMessageLayoutMode
  isReasoningStreaming?: boolean
  reasoning?: string
}

const initial: FixtureState = {
  content: '',
  from: 'assistant',
  status: 'streaming',
  layoutMode: 'alternating',
}

let setFixture: ((next: Partial<FixtureState>) => void) | null = null
let current: FixtureState = initial

function App() {
  const [state, setState] = useState<FixtureState>(initial)
  current = state
  setFixture = (next) => setState((prev) => ({ ...prev, ...next }))

  const message: MessageType = {
    key: 'fixture-1',
    from: state.from,
    status: state.status,
    versions: [{ id: 'v1', content: state.content }],
    reasoning: state.reasoning
      ? { content: state.reasoning, duration: 1 }
      : undefined,
    isReasoningStreaming: state.isReasoningStreaming ?? false,
  }

  const alignment =
    state.layoutMode === 'left' || state.from !== 'user' ? 'left' : 'right'

  return (
    <div id='conversation' className='mx-auto w-full max-w-4xl px-4 py-4'>
      <Message className='group flex-row-reverse py-2.5' from={state.from}>
        <div
          id='message-column'
          className='flex w-full min-w-0 flex-1 basis-full flex-col'
        >
          <PlaygroundMessageContent
            actions={null}
            alignment={alignment}
            message={message}
            versionContent={state.content}
          />
        </div>
      </Message>
    </div>
  )
}

// Driver hooks used by the CDP script: each call is a real React state update,
// so measurements observe the same incremental path the live app uses.
;(window as any).__fixture = {
  set: (next: Partial<FixtureState>) => setFixture?.(next),
  current: () => current,
}

const root = createRoot(document.getElementById('root')!)
root.render(<App />)
