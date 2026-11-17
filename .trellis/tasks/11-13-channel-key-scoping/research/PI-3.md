# PI-3 调研：前端（编辑抽屉、多 key 对话框、卡片跳转按钮）

## A7-1 编辑抽屉的表单结构（`components/drawers/channel-mutate-drawer.tsx`，5288 行）
- 单文件承载全部区块：`basicSection:2629`、`modelsSection:3193`、`connectionSection:3623`、`routingFields:2237`。
- 表单类型与 schema 在 `lib/channel-form.ts`：`channelFormSchema` 已含 `key_mode: 'append'|'replace'`、`multi_key_mode`（创建用 single/batch/multi_to_single）、`multi_key_type`（random/polling）。
- `transformChannelToFormDefaults:590` 是唯一「后端渠道 → 表单」入口；`transformFormDataToUpdatePayload:923` 是唯一「表单 → PUT payload」入口，`use-channel-mutate-form.ts:73` 在其上补 `key_mode`/`multi_key_mode`（仅 `isMultiKeyChannel` 且可编辑敏感字段时）。
- `isMultiKeyChannel` 判定在 :582（`isEditing && channel_info.is_multi_key`）。单 key 编辑时 key 输入框提示「Leave empty to keep existing key」；已有 `key_mode` 选择器 :4666、`multi_key_type` 选择器 :4719，二者都受 `sensitiveLocked` 限制。
- 分组字段：`modelsSection` 内 `name='group'` 的 `MultiSelect`（:3592），`currentGroups = formValues.group`（string[]），`currentModelsArray = parseModelsString(models)`（:767）。→ 「分组可见模型」UI 放这里最自然。
- 递归风险：`form.reset` 由 `loadedForm.snapshot !== JSON.stringify(form.getValues())` 保护（:1051）；新增表单字段若在 watch 中派生出新对象会触发重置循环，因此 `group_models` 以表单字段形式参与 `getValues()` 快照，值必须稳定（只在用户操作时改）。

## A7-2 `settings` 字符串的组装（`lib/channel-form.ts`）
- `buildSettingJSON`（:648）组装的是 `setting` 列（channel.setting，另一列），与本任务无关。
- `buildSettingsJSON`（:769，私有）才是 `settings` 列：**先 `JSON.parse(formData.settings)` 保留原样，再逐字段增删**。因此 `group_models` 默认会被原样透传，不会丢其它字段（`advanced_custom`、`vertex_*`、`upstream_model_update_*` 均走同一模式）。
- 但「分组或模型被删掉时同步清掉失效项」需要显式处理：必须在 `buildSettingsJSON` 里对 `group_models` 做交集裁剪，否则删模型后白名单残留（后端会忽略，但前端展示与实际不一致）。
- `transformChannelToFormDefaults` 反向读 `settings` 时也只挑已知字段，需补 `group_models`；`ChannelOtherSettings`（`types.ts:88`）目前无该字段。

## A7-3 多 key 管理对话框（`components/dialogs/multi-key-manage-dialog.tsx`）
- 数据源：`getMultiKeyStatus(channelId, page, size, status)` → `POST /api/channel/multi_key/manage { action:'get_key_status' }`（`api.ts:449`）；表格用 `StaticDataTable`（`getRowKey={(key)=>key.index}`），列在组件内联定义。
- 行操作抽在 `multi-key-table-row-actions.tsx`（Enable/Disable/Delete 三个 `Button variant='outline'|'destructive' size='sm'`），确认弹窗统一走 `ConfirmDialog` + `MultiKeyConfirmAction`。
- 请求类型 `MultiKeyManageParams.action` 目前只有 7 个动作，需新增 `set_key_models` / `set_key_user`；`KeyStatus` 需新增 `models` / `user_id` / `username`。
- 渠道模型来源：`useChannels().currentRow.models`（逗号分隔）→ `parseModelsList`。

## A7-4 可复用组件（结论）
- 多选：`@/components/multi-select`（chips + 搜索 + `allowCreate`），抽屉里已用，键模型多选直接复用。
- 确认：`@/components/confirm-dialog`；弹窗布局：`@/components/dialog`。
- **没有用户选择组件**：`src/components` 只有 `user-avatar` / `model-group-selector`，`src/features/users/api.ts` 有 `searchUsers` 但无选择器封装。按 design §9 的兜底：用户 ID 数字输入 + 列表里显示接口返回的 `username`。不新写通用选择器（会与设计兜底冲突且无第二个调用方）。

## A7-5（R5）渠道卡片跳转按钮
- `channel-card.tsx` 卡片本身不写按钮，按钮全部来自列的 cell：`flexRender(cells.actions)` → `data-table-row-actions.tsx`。该组件用 `ChannelRowActionsLayoutContext`（`'card' | 'table'`）区分两种视图，卡片专属按钮已存在（`layout === 'card'` 的 “Test Channel Connection”）。
- 写法模板（与本卡片其它按钮完全一致）：
  `<Tooltip><TooltipTrigger render={<Button variant='ghost' size='icon-sm' aria-label={t(...)} />}><Icon className='size-4' /></TooltipTrigger><TooltipContent>{t(...)}</TooltipContent></Tooltip>`；图标来自 `lucide-react`（已有 `ExternalLink` 在别处使用）。
- 地址字段：`Channel.base_url`（`types.ts:49`，`string | nullish`）。→ 空值不渲染；`http://` / `https://` 之外（含 `doubao-coding-plan` 这类占位值）不渲染。判定放 `lib/channel-utils.ts` 便于单测。
- 点击需 `e.stopPropagation()`（卡片行有点击行为）；`window.open(url, '_blank', 'noopener,noreferrer')`。
