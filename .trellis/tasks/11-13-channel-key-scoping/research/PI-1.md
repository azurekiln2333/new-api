# PI-1 调研：key 元数据与管理接口

基线 = 工作包开始时的 `custom/rc45-stats` HEAD（PI-2 已并行改了 `middleware/distributor.go` 等处，本文档只记录与本包契约相关的事实）。

## A1 假设：`channel_info` 能容纳两个新 map，且不影响既有数据

- `model/channel.go:55` `ChannelInfo ChannelInfo \`gorm:"type:json"\``，`Value()`（`:175`）用 `common.Marshal` 返回 `string`（PG simple protocol 兼容），`Scan()`（`:184`）用 `common.Unmarshal`。JSON 列无 DDL，新增 `map[int][]string` / `map[int]int` 只是 JSON 内容变化。
- 既有三张表（状态/禁用原因/禁用时间）与 `MultiKeyPollingIndex` 均未改动语义。
- 结论：**成立**。新增字段为 `MultiKeyModels`、`MultiKeyUsers`，均 `omitempty`。

## A2 假设：列表/详情接口返回 `channel_info`，且不返回完整 key

- `controller/channel.go:194 GetAllChannels` → `GetAllChannels(...)` 用 `Omit("key")`（`model/channel.go:466`），`channel_info` 随 struct 序列化返回。
- `controller/channel.go:473 GetChannel` → `GetChannelById(id, false)` 同样 `Omit("key")`，随后 `clearChannelInfo` 只清 `MultiKeyDisabledReason/Time`。
- 完整 key 只由 `GetChannelKey`（`:497`，需安全验证 + 审计）返回。
- 结论：**成立**。新字段只含模型名与用户 ID，无凭据。

## A3 假设：渠道测试如何选 key、失败后何时自动禁用

- `controller/channel-test.go:172` 渠道测试调用 `middleware.SetupContextForSelectedChannel(c, channel, testModel)`，选 key 实际发生在 `middleware/distributor.go:671`（PI-2 已改为 `GetNextEnabledKeyFor(modelName, c.GetInt("id"))`）。测试上下文在 `:169` 设 `c.Set("id", testUserID)`，即会按「测试用户」过滤绑定 key。
- 失败后果：`controller/channel-test.go:936 shouldBanChannel = service.ShouldDisableChannel(err)`；`:958-959` 满足 `allowDisable && isChannelEnabled && shouldBanChannel && channel.GetAutoBan()` 时 `processChannelError` → `service.DisableChannel` → `model.UpdateChannelStatus(..., UsingKey)`（多 key 时只禁用该 key）。
- `service.ShouldDisableChannel`（`service/channel.go:60`）：`types.IsChannelError(err)` → true；`relaykit/types/error.go:371` 用 `strings.HasPrefix(code,"channel:")` 判定。

## `ErrorCodeChannelNoAvailableKey` 的消费与后果（AC8 硬不变量）

- 定义 `relaykit/types/error.go:55` = `channel:no_available_key`；只由 `model/channel.go` 的 `GetNextEnabledKey(For)` 在「无 key」「无启用 key」时返回。
- 消费链：`service/relay_error.go:94`、`service/request_policy.go:126`、`controller/channel-test.go:936` 都经 `ShouldDisableChannel` → `IsChannelError` 命中 `channel:` 前缀 → **返回 true → 自动禁用**。
- 结论：**不能复用**。新增 `types.ErrorCodeChannelKeyScopeUnsatisfied = "channel_key_scope_unsatisfied"`（刻意不带 `channel:` 前缀），StatusCode 503：默认重试范围含 500–503（`setting/operation_setting/status_code_ranges.go:21`），默认自动禁用范围只有 401（`:17`），固定英文文案不命中 `AutomaticDisableKeywords`。因此可重试、不自动禁用。
- 现有「所有 key 都被禁用」场景继续返回 `channel:no_available_key`，后果不变。

## 会改变 key 列表的全部路径（design §4 要求）

| 位置 | 是否经 `Channel.Update()` | 处理 |
|---|---|---|
| `controller/channel.go` `UpdateChannel` 的 `key_mode` 追加/覆盖 | 是 | `Update()` 内统一重映射 |
| `ManageMultiKeys` `delete_key` / `delete_disabled_keys` | 是（各自先重排三张旧表） | 同上；旧表重排逻辑未动 |
| `model/channel.go` `Channel.Update()` 重算 `MultiKeySize` | — | 挂载点，见实现 |
| `controller/channel.go AddChannel` → `BatchInsertChannels` | 否（新行） | 无旧 key，无需映射 |
| `service/codex_credential_refresh_task.go:70-94` | 否（按下标写回，下标不变） | 无需映射，保持原样 |
| `controller/channel.go:1582 CopyChannel` | 否（新行） | 见报告：深拷贝两张 map，保留绑定 |
| 未发现：导入/导出、按标签批量改 key | — | 无 |

## `GetNextEnabledKey()` 其它调用点（blast radius 复核）

`controller/channel_inference.go:92`、`controller/channel_upstream_update.go:391/440/470`、`controller/ratio_sync.go:340`、`controller/channel-billing.go:716/746` 均与模型/用户无关，保留 `GetNextEnabledKey()`；它现在等价于 `GetNextEnabledKeyFor("", 0)`（无过滤），行为逐字不变。
