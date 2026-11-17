# PI-1 · R5 排行榜缓存用量样式

## 改动文件
- `web/src/features/rankings/components/cache-token-details.tsx`（唯一共享渲染点）
- `web/src/features/rankings/__tests__/cache-tokens.test.tsx`
- `.trellis/tasks/11-11-gateway-ops-batch/i18n/PI-1.json`（`{}`，无新增文案 key）
- 本报告

## 根因（代码为准）
- 总量：`features/rankings/lib/format.ts` 的 `formatTokens`，硬编码 `T/B/M/K`，**语言无关**（同页总量、图表轴、tooltip 全用它）。
- 缓存：`@/lib/format` 的 `formatCompactNumber`（Intl `notation: compact`），中文下输出 `亿/萬`。→ 于是 `8.24B` 与 `30.2亿` 并存。
- 布局：外层 `flex-wrap`，每对 `dt/dd` 也 `flex-wrap + gap-x-1`，导致标签与数值可断行分离、两个条目各占一列。

## 改动
- 缓存值改用 `formatTokens`（与总量同一套单位）；精确值仍放 `title`，用 `@/lib/format` 的 `formatNumber(value, locale)`，locale 经 `toIntlLocale`。
- `<dl>` 改为 `flex flex-wrap items-baseline justify-end gap-x-3 gap-y-0.5 text-[11px]`；每个条目是一个 `flex items-baseline gap-1 whitespace-nowrap` 的 pair → 标签与数值永不分离，读写同处一行，窄屏整体换行但每对完整、不横向溢出。
- 两者都为 0 时 `return null`（不渲染）。
- 三处使用点（`market-share-section` / `model-leaderboard` / `models-section`）共用该组件，自动一起生效；未改各节容器。

## 改后排版
宽屏（模型行 / 供应商行右侧）：
```
8.24B tokens
↓66.9%
Cache Read 3.02B   Cache Write 0
```
小节头部（models-section）：
```
8.24B
TOKENS
Cache Read 3.02B   Cache Write 0
```
窄屏（同结构，两对折行，不溢出）：
```
8.24B tokens
↓66.9%
Cache Read 3.02B
Cache Write 0
```

## 与口径的差异 / 决定
1. 「两者都为 0 时不显示」按字面实现：仅两者皆为 0 隐藏；单个为 0 仍显示为 `0`，使读写成对、语义完整。
2. 数字格式：`AGENTS.md` 要求优先 `@/lib/format`，但 R5 明确要「与总量用同一套」。总量本身用的是本 feature 的 `formatTokens`，故缓存值也用它，`title` 精确值仍用 `@/lib/format`。这是有意偏差，理由：避免改动总量格式超出 R5 范围、保证同页一致。若日后要全局改用 `@/lib/format`，应连总量一起改（另立任务）。
3. 显示值改为语言无关（不再随界面语言变 `亿/萬`，即本需求目标）；`title` 仍随 locale。

## 验证
- `node_modules/.bin/vitest.exe run src/features/rankings` → 1 file / **10 tests 通过**。
- `oxlint -c .oxlintrc.json`（两文件）→ 无输出（0 error）。
- `oxfmt -c .oxfmtrc.json --check`（两文件）→ 通过。
- 测试覆盖：与总量同单位（`3.02B`/`1.50M`）、切换 zhCN/zhTW 不再变 `30.2亿`、两者为 0 不渲染、单个 0 保留成对、标签值与数值同属 `whitespace-nowrap` 对、title 精确值、三处使用点。
- **未跑类型检查**：本机无 `bun`、无 `typescript`/`tsgo`，`bun check -b` 无法执行；已人工核对类型。建议 INT 在有 bun 的环境补跑。
- 未起 dev server，无残留进程。

## 未完成 / 风险
- 类型检查未在本机验证（见上）。
- 极窄屏 + 较长译文（fr/ja）下单个 nowrap pair 接近容器宽度：按现有标签长度估算在 `max-w-[55%]` 内仍可容纳，未做像素级实测。

## 需要他人处理
- INT：`i18n/PI-1.json` 为空对象，无待合并 key（复用现有 `Cache Read` / `Cache Write`）。
- INT：补跑前端类型检查（`bun check -b`）。
