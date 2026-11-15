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
import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { Message } from '@/components/ai-elements/message'

import { getMessageAlignment } from '../../../lib'
import type {
  Message as MessageType,
  PlaygroundMessageLayoutMode,
} from '../../../types'
import { PlaygroundMessageContent } from '../playground-message-content'

function message(from: MessageType['from']): MessageType {
  return {
    key: `${from}-1`,
    from,
    versions: [{ id: 'v1', content: '你好\n第二行比较长的文字' }],
    status: 'complete',
  }
}

/**
 * The bubble column is the root element of the message content. jsdom does not
 * apply the Tailwind stylesheet, so real geometry is verified in the browser;
 * these assertions pin which alignment contract the markup must carry.
 */
function renderContent(
  from: MessageType['from'],
  layoutMode: PlaygroundMessageLayoutMode = 'alternating'
) {
  const current = message(from)
  const alignment = getMessageAlignment(current, layoutMode)
  const { container } = render(
    <Message className='group' from={from}>
      <PlaygroundMessageContent
        actions={null}
        alignment={alignment}
        message={current}
        versionContent={current.versions[0].content}
      />
    </Message>
  )

  const root = container.firstElementChild?.firstElementChild as HTMLElement
  const bubble = root.firstElementChild as HTMLElement

  return { alignment, bubble, root }
}

describe('PlaygroundMessageContent alignment', () => {
  test('sticks a user bubble to the right edge while its text stays left aligned', () => {
    const { root } = renderContent('user')

    expect(root).toHaveClass('self-end')
    expect(root).toHaveClass('text-left')
    expect(root).not.toHaveClass('text-right')
  })

  test('keeps the user bubble left aligned in the left layout mode', () => {
    const { alignment, root } = renderContent('user', 'left')

    expect(alignment).toBe('left')
    expect(root).toHaveClass('self-start')
    expect(root).not.toHaveClass('self-end')
  })

  test('keeps an assistant reply at the column start', () => {
    const { root } = renderContent('assistant')

    expect(root).toHaveClass('self-start')
    expect(root).toHaveClass('max-w-3xl')
    expect(root).not.toHaveClass('self-end')
  })

  test('does not cap the user bubble inside its own content sized column', () => {
    // A percentage cap here resolves against the shrink-to-fit parent, which
    // measurably shrank the bubble below the intended column limit.
    const { bubble } = renderContent('user')

    expect(bubble).toHaveClass('group-[.is-user]:max-w-none')
  })

  test('constrains a live assistant bubble while long link content arrives and after completion', () => {
    const url = `https://example.com/${'long-path-segment-'.repeat(24)}`
    const current = message('assistant')
    const content = (value: string, status: MessageType['status']) => (
      <Message from='assistant'>
        <PlaygroundMessageContent
          actions={null}
          alignment='left'
          message={{ ...current, status }}
          versionContent={value}
        />
      </Message>
    )
    const view = render(content('See ', 'streaming'))

    view.rerender(content(`See ${url}`, 'streaming'))

    const link = screen.getByRole('link', { name: url })
    const response = link.closest('.text-pretty')
    const bubble = response?.parentElement
    expect(link).toHaveAttribute('href', url)
    expect(bubble).toHaveClass('max-w-full')
    expect(response).toHaveClass('[overflow-wrap:anywhere]')

    view.rerender(content(`See ${url}`, 'complete'))

    expect(screen.getByRole('link', { name: url })).toHaveAttribute('href', url)
    expect(bubble).toHaveClass('max-w-full')
    expect(response).toHaveClass('[overflow-wrap:anywhere]')
  })

  test('constrains expanded reasoning while streaming and after completion without losing its content', () => {
    const url = `https://example.com/${'reasoning-path-'.repeat(24)}`
    const current = message('assistant')
    const content = (isStreaming: boolean) => (
      <Message from='assistant'>
        <PlaygroundMessageContent
          actions={null}
          alignment='left'
          message={{
            ...current,
            status: isStreaming ? 'streaming' : 'complete',
            reasoning: { content: `Checking ${url}`, duration: 1 },
            isReasoningStreaming: isStreaming,
          }}
          versionContent=''
        />
      </Message>
    )
    const view = render(content(true))
    const trigger = screen.getByRole('button', { expanded: true })

    expect(trigger.parentElement).toHaveClass('max-w-full')
    expect(screen.getByRole('link', { name: url })).toHaveAttribute('href', url)

    view.rerender(content(false))

    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(trigger.parentElement).toHaveClass('max-w-full')
    expect(screen.getByRole('link', { name: url })).toHaveAttribute('href', url)
  })
})
