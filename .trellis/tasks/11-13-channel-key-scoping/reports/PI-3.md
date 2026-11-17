# PI-3 报告：前端（编辑抽屉、多 key 对话框、卡片跳转按钮）

## 改动的文件

- `web/src/features/channels/types.ts`：`channelInfoSchema` 加 `multi_key_models` / `multi_key_users`；`ChannelOtherSettings` 加 `group_models`；`KeyStatus` 加 `models` / `user_id` / `username`；`MultiKeyManageParams` 加 `set_key_models` / `set_key_user` 与 `models` / `user_id`；新增 `ChannelUpdateRequest`（`key_mode` / `to_multi_key` / `multi_key_mode`）。
- `web/src/features/channels/api.ts`：`updateChannel` 改用 `ChannelUpdateRequest`；新增 `setMultiKeyModels` / `setMultiKeyUser`。
- `web/src/features/channels/lib/channel-group-models.ts`（新）：`parseGroupModels` / `pruneGroupModels`。
- `web/src/features/channels/lib/channel-form.ts`：schema 加 `to_multi_key` / `group_models`；默认值、`transformChannelToFormDefaults` 读 `settings.group_models`；`buildSettingsJSON` 写入前按当前分组/模型裁剪，为空则删除该键。
- `web/src/features/channels/lib/channel-utils.ts`：`resolveChannelAddressUrl`（只接受 `http(s)://` 且有 host）。
- `web/src/features/channels/lib/index.ts`：导出新模块。
- `web/src/features/channels/hooks/use-channel-mutate-form.ts`：单 key → 多 key 时带 `to_multi_key`、`multi_key_mode`、`key_mode`。
- `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx`：「转为多 Key」开关 + 分组模型可见范围 UI。
- `web/src/features/channels/components/dialogs/multi-key-manage-dialog.tsx`：每行显示可用模型/绑定用户；行内模型与用户编辑器；解绑确认。
- `web/src/features/channels/components/dialogs/multi-key-table-row-actions.tsx`：新增 Set Models / Bind User / Unbind。
- `web/src/features/channels/components/data-table-row-actions.tsx`（R5）：卡片视图的「Open Channel Address」按钮。
- 文案：`web/src/i18n/locales/*.json`（7 个）新增 24 个 key（16 个本包 UI + 8 个合并自 `$T/i18n/PI-1.txt`；`User does not exist` 已存在未重复添加）。

## 假设核实（详见 `research/PI-3.md`）

- A7 成立：`settings` 列由 `buildSettingsJSON` 先 parse 再增删，未识别字段自动保留；表单 → payload 的唯一入口是 `transformFormDataToUpdatePayload`，唯一回填入口是 `transformChannelToFormDefaults`。
- A7（用户选择组件）**不成立**：仓库无用户选择组件（`src/components` 只有 `user-avatar` / `model-group-selector`，`users/api.ts` 只有 `searchUsers`）。按 design §9 兜底：用户 ID 数字输入，列表里显示接口返回的 `username`。
- R5：卡片按钮全部来自列 cell（`data-table-row-actions.tsx`），卡片专属按钮用 `layout === 'card'` 区分，写法为 `Tooltip + TooltipTrigger render={<Button variant='ghost' size='icon-sm' />} + TooltipContent` + lucide 图标 `size-4`，本包完全照抄。

## 与设计的偏差及原因

1. **多 key 行内编辑器代替嵌套弹窗**：design §9 未规定编辑器形态。先用嵌套 `Dialog` 实现，实测「选中模型后弹窗内容被 `aria-hidden`」——原因是多选组件选择后弹层保持打开，嵌套弹窗层级下无法用常规查询/点击继续操作。改为在主对话框内、工具栏与表格之间的行内面板（保留「设置可用模型」「绑定用户」「解绑」三个行操作，两个面板互斥打开），无嵌套、无 portal 遮挡，行为可测。
2. **分组可见范围放在分组选择器下方的同一张卡片内**（`modelsSection`），而非新开区块：与「已选分组」上下文相邻，减少跨区块跳转；仍是逐分组多选、空 = 全部可见。
3. **「转为多 Key」开关按 `supportsMultiKeyAddMode` 隐藏**（Codex、Vertex + `api_key`），与创建路径的多 key 限制一致，避免前端提交后必然被后端拒绝。
4. **未新增通用 UI 组件**：多选复用 `@/components/multi-select`，确认复用 `@/components/confirm-dialog`，弹窗复用 `@/components/dialog`，分组展示复用 `@/components/group-badge`。行内编辑器由上述组件 + `Form*` 原语组合，属 feature 组合，无新增通用实现。
5. 本包无后端新增文案，故未创建 `$T/i18n/PI-3.txt`。

## 验证（Windows PowerShell，`$bun = D:\AppData\Local\Temp\new-api-cx-a-tools\bun144\bun-windows-x64\bun.exe`，全部在 `web/` 下）

- `bun check -b` → `✓ No type errors in 1,466 files across 2 projects`。
- `bun run test src/features/channels src/i18n` → `Test Files 36 passed (36) / Tests 415 passed (415)`。
- `bun run test src/features/channels/lib/__tests__/channel-group-models.test.ts` → 4 passed（读取/裁剪/落盘保留其它 settings 字段/清空即删除键）。
- `bun run test src/features/channels/components/__tests__/multi-key-key-scope.test.tsx` → 3 passed（行内显示可用模型与绑定用户；`set_key_models` 参数；`set_key_user` 参数 + 非法用户 ID 禁用保存）。
- `bun run test src/features/channels/components/__tests__/channel-address-button.test.tsx` → 6 passed（卡片视图新标签页打开且带 `noopener,noreferrer`；空值/占位值/非 http(s)/表格视图不显示按钮）。
- `bun run test src/features/channels/components/__tests__/channel-configuration.test.tsx` → 106 passed（含新增 3 例：单转多提交 `to_multi_key`/`key_mode`/`multi_key_mode`；多 key 渠道不带 `to_multi_key`；保存时裁掉失效的分组可见模型）。
- `bun run i18n:sync` → 7 个语言 `missing=0 extras=0`；`git diff --stat` 每个文件仅 +24 个 key。
- `bun x oxlint -c .oxlintrc.json <本包改动文件>` → 无输出（无 error/warning）。
- `bun x oxfmt -c .oxfmtrc.json --check src/features/channels` → 仅剩 `dialogs/inference-status-dialog.tsx`（本包未改动，改动前即如此）。
- `bun run build` → 成功（产物 67.7 MB，仅本地 `web/dist`，已被 git 忽略）。

## 遗留问题

1. 未覆盖的自动化：`MultiKeyManageDialog` 的解绑确认、`channels-table` 之外的其它入口未加测试；行内编辑器未做键盘/Esc 回归（无自定义键盘逻辑，仅原生按钮）。
2. 权限：新行操作与编辑器沿用 `ChannelSensitiveWrite`（`canEditSensitive`）禁用；后端 `to_multi_key` 也要求同一权限（见 PI-1 报告偏差 2），两侧一致。
3. 卡片跳转按钮只在卡片视图出现（按 implement.md 2a「表格视图不在本次范围内」）。
4. 后端若在收尾阶段新增 `$T/i18n/PI-2.txt` 文案，需由 PI-4 合入七语言（本包收尾时该文件尚不存在）。
