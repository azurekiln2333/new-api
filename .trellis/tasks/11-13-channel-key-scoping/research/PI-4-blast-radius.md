# PI-4 · 盲区排查（只读，改动前基线）

基线 = 各包开始改动前的 `custom/rc45-stats` HEAD。每条写「位置 / 现状 / 归属 / 结论」。归属按 `implement.md` 的文件划分；不属于任何包的行给 PI-4。

## 1. `GetNextEnabledKey()` 的调用点（全仓，非测试）

| 位置 | 用途 | 归属 | 结论 |
|---|---|---|---|
| `model/channel.go:212` | 定义 | PI-1 | 保留，新增 `GetNextEnabledKeyFor` |
| `middleware/distributor.go:671` | `SetupContextForSelectedChannel`，HTTP 转发唯一选 key 点（重试也走这里，见 `controller/relay.go:298`） | PI-2 | 改 `GetNextEnabledKeyFor(modelName, userId)`。注意：渠道测试 `controller/channel-test.go:172` 也调用本函数，admin 上下文没有 user id 时必须退化成 0 |
| `relay/relay_task.go:101` | 任务轮询锁定原渠道后轮换 key | PI-2 | design 未点名，属「任务类入口」。应改用 `GetNextEnabledKeyFor(modelName, userId)`，否则 AC6/AC8 在任务路径不成立 |
| `controller/channel_inference.go:92` | 管理端推理测试 | PI-1 | `(model, 0)` |
| `controller/channel_upstream_update.go:391/440/470` | 上游模型探测 | 无 | 不涉及模型/用户，保留 `GetNextEnabledKey()` |
| `controller/ratio_sync.go:340` | 倍率同步 | 无 | 同上 |
| `controller/channel-billing.go:716/746` | 余额查询 | 无 | 同上 |
| `service/codex_credential_refresh_task.go:94`、`service/codex_channel_models.go:18`、`controller/codex_usage.go:80` | 直接按索引取 key（未用 `GetNextEnabledKey`） | 无 | 管理/凭据路径，不受模型范围影响 |

`relay/responses_websocket.go:562` 不选 key，只在重连时按 `lockedKeyIndex` 校验 key 仍启用。它依赖「下标 → key 内容」稳定，AC7 的重映射间接保护这里；PI-2 接入 `GetNextEnabledKeyFor` 后首次握手仍然走 `SetupContextForSelectedChannel`，无需另改。

## 2. 从 `channel.Models` × `channel.Group` 推导数据的位置

| 位置 | 现状 | 归属 | 结论 |
|---|---|---|---|
| `model/ability.go:225 AddAbilities` | models × groups 笛卡尔积 | PI-2 | design §7 已覆盖 |
| `model/ability.go:272 UpdateAbilities` | 同上（先删后建） | PI-2 | design §7 已覆盖 |
| **`model/channel_cache.go:59-70 InitChannelCache`** | **直接从 `channel.Group` × `channel.GetModels()` 建 `group2model2channels`，根本不读 abilities** | PI-2 | **design 未点名的最大缺口**。`MemoryCacheEnabled=true`（默认）时选渠道走这张表，只改 abilities 不改这里 → AC4 的「请求 B 不会路由到该渠道」不成立 |
| `model/channel_satisfy.go:10 IsChannelEnabledForGroupModel` / `IsChannelEnabledForAnyGroupModel:36` | 内存读 `group2model2channels`，DB 读 abilities | PI-2 | 供粘滞/亲和与 `SelectChannelForRequest` 使用；依赖上一行修好 |
| `model/channel.go:867 EditChannelByTag` | 改 models/group 后调用 `UpdateAbilities` | PI-2 | 无需单独改 |
| `model/model_meta.go:325-338` | 删除模型后 `UpdateAbilities(tx)` | PI-2 | 无需单独改；白名单残留由交集丢弃 |
| `controller/channel_upstream_update.go:543/1001` | 上游模型同步改 `channel.Models` 后重建 abilities | PI-2 | 同上；需确认重建调用的是 `UpdateAbilities` |
| `model/task_model_alias.go:132-150` | 由 `channel.GetModels()` + `model_mapping` 推导任务别名，不看分组 | 无 | 别名是全局概念，分组可见性不参与；不改，写进报告 |
| `controller/model_meta.go:292`、`model/pricing*.go`、`model/ability.go:33/57` | 读 abilities 表做定价/模型列表 | PI-2 | abilities 如实后自动正确 |

## 3. 修改 key 列表或 `channel_info` 的位置

| 位置 | 说明 | 归属 |
|---|---|---|
| `controller/channel.go:1185-1300` | `UpdateChannel` 的 `key_mode` 追加/覆盖，`channel.Key = strings.Join(allKeys,"\n")` + 去重重算 `MultiKeySize` | PI-1（§4 重映射 + §5 单转多都要在这里） |
| `controller/channel.go:1988-2084` | `ManageMultiKeys` 的 `delete_key` / `delete_disabled_keys`，重排 `MultiKeyStatusList/…Time/…Reason` | PI-1 |
| `model/channel.go:566-604` | `Channel.Update()` 按当前 key 重算 `MultiKeySize` 并裁剪越界状态 | PI-1（design 已点名） |
| `controller/channel.go:743-880 AddChannel` | 建渠道时算 `MultiKeySize` | PI-1（单转多的校验照搬路径） |
| `controller/channel.go:1143-1181` | `UpdateChannel` 用 `originChannel.ChannelInfo` 覆盖请求里的 ChannelInfo | PI-1：新字段靠这里保留，不要改成从请求取 |
| **`controller/channel.go:1582 CopyChannel`** | `clone := *origin` 浅拷贝 + `clone.Insert()`：`ChannelInfo` 是值类型但里面的 map 与 origin 共享，且 `MultiKeyUsers` 会被复制过去 | PI-1：至少要让拷贝不带绑定用户（否则用户会同时命中两个渠道，违背 R1「一个 key 只给一个用户」的独占语义），在报告里写明选择 |
| `model/channel.go:681 handlerMultiKeyUpdate` / `:746 UpdateChannelStatus` | 按 key 写状态 | PI-1 不动 |
| `model/channel.go:358-378 Save/saveStatusState` | 多 key 时把 `channel_info` 一起更新 | 无 |
| `service/codex_credential_refresh_task.go:70-94` | 只读 `channel_info` 后按索引写回 key | 无：索引重排后这里读的是最新数据，不影响 |

未发现「导入/导出渠道」功能，也没有按标签批量改 key 的路径；按标签批量编辑只改 models/group/tag（见第 2 节）。

## 4. 读 `settings` / `ChannelOtherSettings` 可能丢字段的位置

- `dto.ChannelOtherSettings` 实际定义在 `relaykit/dto/channel_settings.go:112`（root `dto` 是别名），新增 `GroupModels` 属 relaykit 公共 DTO 变更 → **PI-2 必须跑 `GOWORK=off go build ./...` 于 `relaykit/`**（AGENTS.md 硬要求）。
- `model/channel.go:1075/1091 Get/SetOtherSettings` 全量 unmarshal/marshal，无字段白名单 → 新增字段自动透传。
- `controller/channel_upstream_update.go:484/505/952/989/1067`、`controller/channel.go:1425`、`controller/channel-billing.go:548`、`controller/channel-test.go:408`：都是 `GetOtherSettings()` 取整结构体再 `SetOtherSettings()`，不丢字段。
- **前端**：`web/src/features/channels/**` 是否会「用表单字段重建 settings 字符串」是唯一可能丢 `group_models` 的地方 → PI-3 负责（design §9 已要求「其它字段不丢」）。集成阶段用 `rg "group_models|settings"` 复核。
- `middleware/distributor.go:645` 把 settings 放进上下文供 relay 读，只读不写。

## 5. 其它值得注意的点

1. `model/channel_cache.go:41-131`（PI-2 新增的用户绑定索引）必须在「渠道增删改 + 多 key 管理动作」后刷新；`CopyChannel`、`AddChannel`、`ManageMultiKeys` 目前都只调 `model.InitChannelCache()` 或 `CacheUpdateChannel()`，PI-2 的挂载点要覆盖这两条。
2. `CacheUpdateChannel()` 不更新 `group2model2channels`（只更新 `channelsIDM`）→ 分组可见性变化后如果只调它，路由表是旧的。检查 PI-2 是否在多 key 动作里调用了 `InitChannelCache()`。
3. `MemoryCacheEnabled=false` 时选渠道走 `model.GetChannel`（读 abilities），因此 abilities 如实后 AC4 自带正确性；`IsChannelEnabledForGroupModel` 的 DB 分支同理。
4. 错误码 `ErrorCodeChannelNoAvailableKey` 的消费点（`processChannelError` / `service.DisableChannel`）决定 AC8：PI-1 必须验证「有启用 key 但无符合条件的 key」用的错误码不会触发渠道自动禁用。
5. `ChannelInfo` 是 `gorm:"type:json"` 列，新增两个 map 字段只有 JSON 序列化影响，无 DDL。SQLite/MySQL/PG 均按 TEXT/JSON 存字符串，`Value()` 已固定返回 `string`（PG simple protocol 兼容）。
