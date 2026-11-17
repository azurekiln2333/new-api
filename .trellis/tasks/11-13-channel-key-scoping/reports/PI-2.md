# PI-2 报告：abilities 生成、用户绑定路由、模型列表

## 改动的文件

| 文件 | 改动 |
|---|---|
| `relaykit/dto/channel_settings.go` | `ChannelOtherSettings` 新增 `GroupModels map[string][]string`（`group_models`） |
| `model/ability.go` | 新增 `channelAbilities()`（abilities 的唯一推导）与 `filterGroupModels()`；`AddAbilities`/`UpdateAbilities` 改为共用推导，二者不再各写一套 |
| `model/channel_cache.go` | `group2model2channels` 改用同一推导（`PublicModels()` ∩ `GroupModels`）；新增用户绑定索引、`GetUserBoundChannel`、`GetUserBoundModels`；`CacheUpdateChannel` 同步刷新索引；顺手修掉该循环在「分组没有 abilities 行」时的 nil map panic |
| `service/channel_select.go` | 首发尝试（retry==0 且无失败渠道）先按用户绑定选渠道，命中即返回并打标记；新增 `UserBoundChannelRouted` |
| `middleware/distributor.go` | 选 key 改 `GetNextEnabledKeyFor(modelName, c.GetInt("id"))`；绑定路由成功时不写粘滞 |
| `controller/model.go` | `ListModels` 并入 `GetUserBoundModels(userId)`（同套令牌限制/计费过滤，去重） |
| `controller/user.go` | `GetUserModels`（控制台用户可用模型）并入绑定模型 |
| `relay/relay_task.go` | remix 锁渠道选 key 改 `GetNextEnabledKeyFor(OriginModelName, UserId)`（跨包，已记 `dispatch/cross-edits.md`） |
| `model/model_meta.go` | 批量删模型时补 select `channel_info`/`settings`（跨包，已记 cross-edits） |
| 测试 | 新增 `model/ability_test.go`；扩展 `service/channel_select_auto_groups_test.go`、`controller/model_list_test.go` |

## 假设核实（design §11）

- **A4 成立**：`ChannelOtherSettings` 实际在 `relaykit/dto/channel_settings.go`（root `dto` 无此文件，`model` 以别名导入 relaykit/dto），已在 relaykit 模块内 `GOWORK=off go build/vet ./...` 验证。`Channel.Update()` 无条件 `UpdateAbilities`，`UpdateChannel`/`ManageMultiKeys`/`DeleteChannel`/批量标签之后都调 `InitChannelCache()`，故 settings 变化必然重建 abilities 与路由表，未新增触发点。
- **A5 成立**：唯一刷新函数是 `model.InitChannelCache()`（启动、周期同步、所有渠道 CRUD 与多 key 动作后）。`MemoryCacheEnabled=false` 时它原本直接 return，选渠道走 abilities 表，因此我把索引重建放在该分支之前并加 TTL 惰性重建。
- **A6 部分与设计不同**：`SelectChannelForRequest`（`service/channel_select.go`）才是 HTTP/任务插件/Responses WS 三种传输共用的首发选渠道入口，内部顺序是 pin → 粘滞 → 分组。绑定路由放进这里（pin 之后、粘滞之前）即可同时满足「在管理员指定渠道之后」「绑定优先于粘滞」，且一处覆盖三种传输；放 distributor 只能覆盖 HTTP。重试路径（`controller/relay.go:305/555`）直接走 `CacheGetRandomSatisfiedChannel`，不受影响，天然满足「重试走分组路由」。

## 索引刷新方案

`用户 ID → 渠道 ID 列表`，内存 `map[int][]int`，独立 `sync.RWMutex`（不与 `channelSyncLock` 嵌套，避免与 pricing 锁的 AB-BA）。构建时在 Go 里遍历渠道行的 `channel_info.multi_key_users` 过滤，不写任何 JSON SQL、不加表列。刷新点：`InitChannelCache()`（cache 开：复用已加载的渠道列表；cache 关：自取一次全表）、`CacheUpdateChannel()`（单渠道更新）。`MemoryCacheEnabled=false` 时无周期同步，`GetUserBoundChannel`/`GetUserBoundModels` 在索引超过 10s 时惰性重建（`userBoundChannelIndexTTL`）；此时渠道状态与绑定仍以 DB 为准，`CacheGetChannel` 直读 DB。

## 粘滞/故障转移结论

- 绑定路由在粘滞之前，命中即返回，因此**绑定优先于粘滞**；粘滞的 `IsChannelEnabledForGroupModel` 校验用的是同一份「公共 key ∩ 分组白名单」路由表，只由绑定 key 提供模型的渠道对其他用户不可命中。
- 绑定路由成功的请求**不写粘滞**（`UserBoundChannelRouted` 守卫），避免独占渠道进入可能被他人命中的 key source；对同一用户绑定本身是确定性的，不损失粘滞收益。
- 绑定渠道失败后的重试完全走原逻辑（`CacheGetRandomSatisfiedChannel` + 失败渠道规避 + 跨分组重试），未改一行。
- `MemoryCacheEnabled=false` 分支同样成立（`GetUserBoundChannel` 直读 DB）。

## 与设计的偏差

1. 接入点从 `middleware/distributor.go` 挪到 `service/channel_select.go`（理由见 A6）。distributor 仍改了选 key 与粘滞写入两处。
2. 渠道测试路径的 `userId`：`SetupContextForSelectedChannel` 取 `c.GetInt("id")`，而 `controller/channel-test.go:163` 把该值设为「操作者本人或 root 用户」。design §3 要求测试用 0。未改该文件（PI-1 名下）——影响：管理员自己拥有绑定 key 时测试会用该 key；某模型只有别人的绑定 key 可服务时，管理员测试会报 `channel_key_scope_unsatisfied`（503，不触发自动禁用，属误报）。已在 `research/PI-2.md` 与本节记录，留给 PI-4 决定是否让 PI-1 在测试路径显式传 0。
3. abilities 推导改用 `GetModels()`（原 `AddAbilities` 用 `strings.Split(channel.Models, ",")`）：`models` 为空串的退化渠道不再产生一条 `model=""` 的 ability。正常渠道结果逐条相同。
4. `InitChannelCache` 的路由表循环加了 nil-map 保护（原先「渠道分组在 abilities 里没有任何行」会 panic），并对空模型集提前 `continue`；不改变任何正常配置的结果。

## 是否触碰计费文件

**否**。未修改 `pkg/billingexpr/`、`service/quota.go`、`relay/helper/price.go`、`relay/common/relay_info.go` 等任何计费文件，未读写 `PriceData`/`OtherRatios`，未改计费分组。

## 实际执行的验证（Windows，`GOWORK=off`）

```
gofmt -l <本包改动的全部 .go>            → 空
go build ./...                          → exit 0
go vet ./model/... ./controller/... ./middleware/... ./service/... ./relay/ → exit 0
(relaykit) GOWORK=off go build ./... ; go vet ./... → exit 0
go test ./model/... ./service/... ./middleware/... ./controller/... ./relay/... → 全部 ok，无 FAIL
  · controller 217s / model 8s / service 9s / middleware+relay 通过
  · 新增用例：TestChannelAbilitiesHonorGroupModelScope、TestChannelCacheRoutingHonorsGroupModelScope、
    TestChannelAbilitiesExcludeUserBoundKeys、TestGetUserBoundChannelReachesBoundUserOutsideGroup、
    TestSelectChannelForRequestRoutesBoundUserThenFallsBackOnRetry、TestGetUserModelsIncludesUserBoundModels 全 PASS
```

未验证：MySQL / PostgreSQL 实例（本任务无表结构、GORM 标签、迁移、原生 SQL 变化，故未做三库矩阵）；前端相关（PI-3）。

## 遗留

- 渠道测试路径的 userId 偏差（上面第 2 条）。
- 新建渠道（`AddChannel`）后不调用 `InitChannelCache()`（既有行为），绑定索引与路由表一样要等周期同步；创建接口无法写入绑定，实际影响仅限「用 API 直接提交 `channel_info.multi_key_users` 建渠道」。
- 绑定路由只按客户端请求的模型名精确匹配，未走 `RoutingMatchModelName` 别名归一（design §3 口径为「`modelName ∈ UserBoundModels`」）。
