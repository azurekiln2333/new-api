# R11 修正 R10：游乐场用户气泡居右，气泡内文字左对齐

执行：`na-pi-6`（`w4C:pK`，R9/R10 的原作者，熟悉这批文件）。报告：`reports/R11.md`，不超过 40 行。

## 需求（用户原话）

- R10 的来源：「用户的消息左对齐，而不是右对齐，看着很难受」。
- 本次纠正：「用户发的消息左对齐是消息内容左对齐不是气泡左对齐 做错了 气泡还是要居右。。气泡内容要左对齐」。

即：用户气泡贴对话列右边缘，气泡里的文字左对齐（多行时每行从左边起）。R10 把「文字」当成了「气泡」，方向做反了。AI 回复一侧本次不动。

    对话列右边缘 ->                         |
                         +------------------+
                         | 第一行文字        |
                         | 第二行比较长的文字 |
                         +------------------+
                                    12:00:01
    AI 回复从左边起，宽度上限不变

## 现状与成因（先自己核实，与实际有出入以实际为准，写进报告）

1. 用户气泡根节点（`components/message/playground-message-content.tsx` 的根 div）是 `w-fit max-w-[85%] sm:max-w-[70%]`。它的父级是 `chat/playground-chat.tsx` 里 `flex w-full ... flex-col` 的列，默认 stretch。带明确宽度的子项不被拉伸，落在列左边；根节点自己的 `items-end` 只管它内部的子项，挪不动它自己。结果：气泡在左。
2. `lib/message/message-layout-utils.ts` 的 `getMessageAlignmentClass('right')` 返回 `items-end text-right`。`text-right` 被气泡文字继承，多行文字右对齐，这就是「看着很难受」的来源。
3. `lib/message/__tests__/message-layout-utils.test.ts`「stacks messages through a full width column」只断言 class 字符串，没验证真实位置，所以 R10 没发现气泡跑到左边。
4. 待实测确认，可能是第二个问题：`components/ai-elements/message.tsx` 的 `flat` 变体给用户气泡加了 `group-[.is-user]:max-w-[80%]`。R10 之后它的包含块是 `w-fit` 的根节点，百分比对「收缩到内容」的容器是循环依赖，短消息可能被提前折行，或气泡比根节点窄。出现的话，宽度上限只保留在一处：优先在 `lib/message/message-styles.ts` 里用 `group-[.is-user]:max-w-none` 覆盖；不行再考虑把用户列恢复成 `w-full` + `items-end`、上限挂到气泡上。`ai-elements/message.tsx` 是共享组件，先查别处有没有在用，能不碰就不碰。

## 要做的事

A. 位置：用户根节点贴右（`self-end`，或让父列 `items-end`，选改动小且不影响编辑态 `PlaygroundMessageEditor` 的）。必须由 `alignment` 驱动，不要按角色写死：`messageLayoutMode === 'left'` 时用户气泡仍然靠左，这是现有行为，保留。
B. 文字：去掉 `text-right`，改为明确的 `text-left`，放哪一层自己定，保证气泡内多行文字、列表、引用都左对齐。
C. 气泡下面的时间/耗时行（`message-metadata.tsx`，已有 `justify-end`）和操作按钮继续贴气泡右边缘。实测位置不对再改，写进报告。
D. 不动：AI 回复的宽度和对齐、内容渲染、流式、工具调用展示、编辑态、窄屏布局（窄屏用户气泡仍靠右，上限 85%）、`getAssistantContentClass` / `getUserContentClass` 的宽度数值。

## 验收

1. **真实浏览器量坐标（必做）**。R10 就是只测了 class 字符串才出错。做法建议，能更简单就更简单：
   - `bun run build` 生成最新 CSS（`web/dist/static/css/index.*.css`）。
   - 用真组件出 HTML：在 vitest 的 jsdom 里用 RTL 渲染真实的 `PlaygroundChat`（沿用 `__tests__/index.test.tsx` 的 i18n/mock 写法），取 `container.innerHTML`。不要手写模拟标签。
   - 拼一个 html：引用上面的 CSS，放这段 HTML，再加一段内联脚本，加载后用 `getBoundingClientRect()` / `Range.getClientRects()` 量数据，写进 `<pre id=out>`。
   - 无头 Chrome 取结果：`& 'C:\Program Files\Google\Chrome\Application\chrome.exe' --headless=new --disable-gpu --user-data-dir=$env:TEMP\r11-chrome --window-size=1440,900 --virtual-time-budget=3000 --dump-dom file:///...`（必须带独立的 `--user-data-dir`，否则会挂到用户正在开的 Chrome 上直接退出；Edge 也可：`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`）。
   - 临时文件放 `$env:TEMP\r11-layout\`，做完删除，不进仓库。
   - 测试数据：短用户消息（「你好」）、长用户消息（约 200 字且含换行）、一条较长的 AI 回复。宽 1440 与 390 各跑一次。
   - 要看到的数字：用户气泡右边缘与它所在列右边缘相差 ≤1px；气泡宽度 ≤ 列宽 70%（390 宽时 ≤85%）；多行气泡里各行文字左边缘一致（±1px），`text-align` 计算值为 left/start；短消息「你好」只占一行（排除疑点 4 的提前折行）；AI 回复左边缘贴列左，宽度 ≤ 768px。把这些数字写进报告。
2. 单元/渲染测试（按 `web/AGENTS.md` 3.14，放模块专属 `__tests__/`，不新增分散文件）：
   - 改正 `message-layout-utils.test.ts` 里错误的契约：right 对齐必须含贴右类，不得再含 `text-right`；left 对齐贴左。
   - 渲染测试（扩展现有测试文件，或在 `components/message/__tests__/` 下加一个）：把 `PlaygroundMessageContent` 放进 `<Message from='user'>` 里，用户视角查询，断言贴右类、文字左对齐类存在、不含 `text-right`；`layoutMode='left'` 时贴左；assistant 不受影响。
3. 命令（贴结果摘要进报告）：`bun check -b`；`bun x oxlint` 与 `bun x oxfmt --check` 只对改动文件；`bun run test -- src/features/playground`；`bun run build`。
4. 无新增文案；若有，七种语言补齐。

## 共同规则

先完整读仓库根 `AGENTS.md` 和 `web/AGENTS.md`；修改已有文件只用精确替换，不整文件覆盖；不做任何 git 写操作（审查、提交、推送由调度者在用户确认后做）；不向用户提问，需求没写明的按最贴近用户原话、改动最小的方式自行决定并写进报告；做完报告写 `reports/R11.md`：改了什么、关键决定、跑过的命令与结果、浏览器实测的数字、没验证的部分。浏览器实测跑不起来时如实写「未验证」并说明原因，不得用 class 断言冒充。
