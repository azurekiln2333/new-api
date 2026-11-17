# PI-2 调研：abilities 生成与用户绑定路由

## A4 `channels.settings` / `ChannelOtherSettings` / abilities 重建
- `dto.ChannelOtherSettings` 实际在 `relaykit/dto/channel_settings.go:112`（`model/channel.go` 以 `dto` 别名导入 relaykit/dto），**不在** 根模块 `dto/`。新增 `GroupModels` 要改 relaykit 模块，须在 `relaykit/` 里跑 `GOWORK=off go build ./...`。
- 读写：`Channel.GetOtherSettings/SetOtherSettings`（`model/channel.go:704/718`）走 `common.UnmarshalJsonStr/Marshal`，`settings` 列是 text。渠道 `Update()`（`model/channel.go:611`）无条件调 `UpdateAbilities(nil)`；`ManageMultiKeys` 各动作、`UpdateChannel`、`DeleteChannel`、批量标签等都在之后调 `model.InitChannelCache()`。→ abilities 与缓存都会重建，无需新增触发点。
- 唯一漏重建位置：`model/model_meta.go:326` 批量删模型时 `Select("id","models","status","group","priority","weight","tag")` 后调 `channel.UpdateAbilities(tx)`，缺 `channel_info`/`settings`，会让新推导用到空配置（公共 key=全部、分组白名单=空）。已按共同规则 3 在 `dispatch/cross-edits.md` 记录并补 select 列。

## A5 渠道缓存刷新与 MemoryCacheEnabled
- 刷新函数是 `model.InitChannelCache()`（`model/channel_cache.go:27`）：渠道增删改、多 key 动作、上游模型同步、`SyncChannelCache`（`MEMORY_CACHE_ENABLED=true` 时按 `SyncFrequency` 周期）都调它。启动时仅在 `main.go:103`（cache 开启分支）调用。
- `MemoryCacheEnabled=false` 时 `InitChannelCache` 直接 return：路由走 `GetChannel`（abilities 表）、`IsChannelEnabledForGroupModel` 走 `isChannelEnabledForGroupModelDB`（abilities 表）。abilities 表在两模式下都是分组×模型的真实来源；`group2model2channels` 是内存副本，但它是**从 channel.Models × channel.Group 现算**的（`model/channel_cache.go:57-68`），不是从 abilities 读，所以必须同样套用新推导，否则分组可见性在 cache 模式下失效。
- 用户绑定索引因此：cache 模式下随 `InitChannelCache` 重建；cache 关闭时用 TTL 惰性重建（`GetUserBoundChannel` 首次访问或过期后重建），因为该模式没有任何周期同步。

## A6 分发中间件顺序与粘滞/故障转移
`middleware/distributor.go:Distribute` 顺序：约束收集（path/task-plugin identity）→ `getModelRequest` → 若未 pin：令牌模型限制校验、playground 分组校验 → 若有 pin 或需选渠道：task-plugin endpoint 预选渠道 → `service.SelectChannelForRequest` → `SetupContextForSelectedChannel`（选 key）→ `c.Next()` → 成功则 `service.RecordChannelAffinity`。

`service.SelectChannelForRequest`（`service/channel_select.go:347`）内部顺序：pin → **affinity（仅 retry==0 且无失败渠道）** → `CacheGetRandomSatisfiedChannel`（auto 分组跨组 + 失败渠道规避）→ 过滤校验。三个传输共用它：HTTP distributor、`middleware/task_plugin.go:546`、`relay/responses_websocket.go:948`。重试路径（`controller/relay.go:305 getChannel`、任务提交 `controller/relay.go:555`）直接调 `CacheGetRandomSatisfiedChannel`，不经它。

结论与设计出入：
1. 接入点放在 `SelectChannelForRequest`（pin 之后、affinity 之前、带 `retry==0 && len(FailedChannelIDs)==0` 守卫）而不是 distributor，一处覆盖三种传输，且“绑定优先于粘滞、重试仍走分组路由”自动成立。
2. 命中绑定渠道时 `selectGroup` 返回 ""（与 pin 分支一致）；WS 与 distributor 都不使用该值做后续判断，仅重试错误信息用。

## 粘滞写入
`service.RecordChannelAffinity` 的 key 由规则配置的 key source 拼成（`context_int:id`、header、body 字段等），可被同 key source 的其他用户命中。绑定渠道经用户 A 绑定路由成功后若写入粘滞，用户 B 可能被“粘”到该渠道。B 仍只会选到公共 key（选 key 按用户过滤），模型只由 A 的绑定 key 提供时该渠道在 B 的分组里没有 ability，粘滞分支的 `IsChannelEnabledForGroupModel` 会拒绝。仍按设计执行更严格的约束：绑定路由成功的请求不写粘滞（绑定本身对同一用户是确定性的，不损失粘滞收益）。

## `GetNextEnabledKey()` 全部调用点
| 位置 | 性质 | 处理 |
|---|---|---|
| `middleware/distributor.go:671`（`SetupContextForSelectedChannel`） | 转发（首发+重试+WS+渠道测试共用） | 改 `GetNextEnabledKeyFor(modelName, c.GetInt("id"))` |
| `relay/relay_task.go:102` | 转发（remix 锁定原任务渠道） | 改 `GetNextEnabledKeyFor(info.OriginModelName, info.UserId)`（跨包小改，记 cross-edits） |
| `controller/channel_inference.go:92` | 管理端状态探测 | 保留 |
| `controller/channel_upstream_update.go:391/440/470` | 管理端上游同步 | 保留 |
| `controller/ratio_sync.go:340` | 管理端比率同步 | 保留 |
| `controller/channel-test.go:172` | 渠道测试（经 `SetupContextForSelectedChannel`） | 由 distributor 改动覆盖；其上下文 `id` 是模拟测试用户，与 design §3「测试用 0」有差异，见报告 |

## 从渠道推导「分组 × 模型」的全部位置
- `model/ability.go:225 AddAbilities`（用 `strings.Split(channel.Models)`，与 UpdateAbilities 不一致，统一）与 `:272 UpdateAbilities`：abilities 写入。
- `model/ability.go:391 FixAbility`：走 `AddAbilities`，随之覆盖。
- `model/channel_cache.go:57-68`：`group2model2channels` 内存路由表，需套用同一推导。
- `model/model_meta.go:183 GetConfiguredModelChannels`、`model/model_pricing_conversion.go:243`、`model/task_model_alias.go:138`：定价/元数据/任务别名，按 prd「不改模型广场与定价页」不处理。
