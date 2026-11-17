// PI-3 throwaway geometry driver: real Chrome, real React state updates, real CSS.
// Feeds incremental assistant content through window.__fixture.set(...) so every
// measurement happens on the same incremental path the live app uses.
const CDP_PORT = 9333

const LONG_WORD =
  'https://example.com/' + 'a'.repeat(220) + '/path?query=' + 'b'.repeat(120)

const SCENARIOS = [
  { name: 'stream-short', from: 'assistant', chunks: ['Hello', ' there', ', this is a reply.'] },
  {
    name: 'stream-long-prose',
    from: 'assistant',
    chunks: [
      'Here is a long paragraph that keeps going so the reply has to wrap inside the reading column. '.repeat(4),
      ' And another sentence appended later during streaming. '.repeat(3),
    ],
  },
  {
    name: 'stream-long-url',
    from: 'assistant',
    chunks: ['See ', LONG_WORD.slice(0, 60), LONG_WORD],
  },
  {
    name: 'stream-unclosed-fence',
    from: 'assistant',
    chunks: [
      'Code below:\n\n```js\n',
      'const aVeryLongIdentifierName = ' + '"x".repeat(300)'.repeat(3) + '\n',
      'const more = "' + 'y'.repeat(240) + '"',
    ],
  },
  {
    name: 'stream-closed-fence',
    from: 'assistant',
    chunks: [
      'Code below:\n\n```js\n',
      'const aVeryLongIdentifierName = "' + 'x'.repeat(260) + '"\n',
      '```\n\nDone.',
    ],
  },
  { name: 'user-short', from: 'user', chunks: ['hi'] },
  {
    name: 'user-multiline',
    from: 'user',
    chunks: ['first line of the message\n', 'second line is longer than the first\n', 'third'],
  },
]

const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900, mobile: false },
  { name: 'mobile', width: 390, height: 844, mobile: true },
]

const MEASURE = `(() => {
  const doc = document.documentElement
  const conversation = document.getElementById('conversation')
  const column = document.getElementById('message-column')
  const bubbleRoot = column ? column.firstElementChild : null
  const bubble = bubbleRoot ? bubbleRoot.firstElementChild : null
  const rect = (el) => {
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: r.x, right: r.right, width: r.width, height: r.height }
  }
  // Widest single line inside the bubble: catches a wrap failure the box width hides.
  let widestLine = 0
  if (bubble) {
    const walker = document.createTreeWalker(bubble, NodeFilter.SHOW_TEXT)
    let node
    while ((node = walker.nextNode())) {
      if (!node.textContent || !node.textContent.trim()) continue
      const range = document.createRange()
      range.selectNodeContents(node)
      for (const line of range.getClientRects()) {
        if (line.width > widestLine) widestLine = line.width
      }
      range.detach()
    }
  }
  const codeScroller = bubble ? bubble.querySelector('.code-block-scroll') : null
  const bubbleStyle = bubble ? getComputedStyle(bubble) : null
  return JSON.stringify({
    innerWidth: window.innerWidth,
    docScrollWidth: doc.scrollWidth,
    docClientWidth: doc.clientWidth,
    horizontalOverflow: doc.scrollWidth - doc.clientWidth,
    conversation: rect(conversation),
    column: rect(column),
    bubble: rect(bubble),
    bubbleTextAlign: bubbleStyle ? bubbleStyle.textAlign : null,
    bubbleOverflowWidth: bubble ? bubble.scrollWidth : null,
    widestLine: Math.round(widestLine * 100) / 100,
    codeScrollable: codeScroller
      ? { scrollWidth: codeScroller.scrollWidth, clientWidth: codeScroller.clientWidth }
      : null,
    bodyOverflow: document.body.scrollWidth - document.body.clientWidth,
  })
})()`

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function connect() {
  const targets = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json()
  const page = targets.find((t) => t.type === 'page')
  if (!page) throw new Error('no page target')
  const ws = new WebSocket(page.webSocketDebuggerUrl)
  let id = 0
  const pending = new Map()
  await new Promise((res, rej) => {
    ws.onopen = res
    ws.onerror = rej
  })
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data)
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id)
      pending.delete(msg.id)
      msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result)
    }
  }
  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const msgId = ++id
      pending.set(msgId, { resolve, reject })
      ws.send(JSON.stringify({ id: msgId, method, params }))
    })
  return { send, close: () => ws.close() }
}

async function evaluate(send, expression) {
  const res = await send('Runtime.evaluate', {
    expression,
    returnByValue: true,
    awaitPromise: true,
  })
  if (res.exceptionDetails) {
    throw new Error('eval failed: ' + JSON.stringify(res.exceptionDetails))
  }
  return res.result.value
}

async function main() {
  const fileUrl =
    'file:///' + 'D:/AppData/Local/Temp/pi3-fixture/dist/index.html'
  const { send, close } = await connect()
  await send('Page.enable')
  await send('Runtime.enable')

  const results = []
  for (const vp of VIEWPORTS) {
    for (const sc of SCENARIOS) {
      await send('Emulation.setDeviceMetricsOverride', {
        width: vp.width,
        height: vp.height,
        deviceScaleFactor: 1,
        mobile: vp.mobile,
      })
      // Fresh mount per scenario so no state leaks between measurements.
      await send('Page.navigate', { url: fileUrl })
      await sleep(500)
      const ready = await evaluate(send, `typeof window.__fixture !== 'undefined'`)
      if (!ready) {
        results.push({ viewport: vp.name, scenario: sc.name, error: 'fixture not ready' })
        continue
      }
      await evaluate(
        send,
        `window.__fixture.set({ from: ${JSON.stringify(sc.from)}, status: 'streaming', content: '', reasoning: '', isReasoningStreaming: false })`
      )
      await sleep(120)

      for (let i = 0; i < sc.chunks.length; i++) {
        await evaluate(
          send,
          `window.__fixture.set({ content: window.__fixture.current().content + ${JSON.stringify(sc.chunks[i])} })`
        )
        await sleep(180)
        const snapshot = JSON.parse(await evaluate(send, MEASURE))
        results.push({
          viewport: vp.name,
          scenario: sc.name,
          step: `chunk-${i + 1}/${sc.chunks.length}`,
          status: 'streaming',
          ...snapshot,
        })
      }

      // Completion: the same mounted tree flips status, no remount.
      await evaluate(send, `window.__fixture.set({ status: 'complete' })`)
      await sleep(220)
      results.push({
        viewport: vp.name,
        scenario: sc.name,
        step: 'complete',
        status: 'complete',
        ...JSON.parse(await evaluate(send, MEASURE)),
      })

      // Restored history: a fresh mount with the final content.
      await send('Page.navigate', { url: fileUrl })
      await sleep(500)
      await evaluate(
        send,
        `window.__fixture.set({ from: ${JSON.stringify(sc.from)}, status: 'complete', content: ${JSON.stringify(sc.chunks.join(''))} })`
      )
      await sleep(220)
      results.push({
        viewport: vp.name,
        scenario: sc.name,
        step: 'restored',
        status: 'complete',
        ...JSON.parse(await evaluate(send, MEASURE)),
      })
    }
  }

  close()
  const out = process.argv[2] || 'D:/AppData/Local/Temp/pi3-fixture/geometry.json'
  const { writeFileSync } = await import('node:fs')
  writeFileSync(out, JSON.stringify(results, null, 1))
  console.log('MEASURED', results.length, 'snapshots ->', out)

  const violations = results.filter(
    (r) => !r.error && (r.horizontalOverflow > 0 || r.bodyOverflow > 0)
  )
  console.log('OVERFLOW VIOLATIONS:', violations.length)
  for (const v of violations.slice(0, 12)) {
    console.log(
      `  ${v.viewport} ${v.scenario} ${v.step}: docOverflow=${v.horizontalOverflow} bodyOverflow=${v.bodyOverflow} bubbleScroll=${v.bubbleOverflowWidth}`
    )
  }
}

main().catch((e) => {
  console.error('DRIVER FAILED:', e.message)
  process.exit(1)
})
