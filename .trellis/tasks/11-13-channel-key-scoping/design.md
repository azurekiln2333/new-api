# 技术设计

下文 `$T` = `.trellis/tasks/11-13-channel-key-scoping`。本文件是四个工作包共同遵守的契约；字段名、JSON 名、方法签名不得各自改动。

## 1. 总体思路

三类新配置全部放进已有的 JSON 列，不改表结构：

| 配置 | 位置 | 列 |
|---|---|---|
| key → 可用模型 | `model.ChannelInfo` | `channels.channel_info` |
| key → 绑定用户 | `model.ChannelInfo` | `channels.channel_info` |
| 分组 → 可见模型 | `dto.ChannelOtherSettings` | `channels.settings` |

路由仍然靠 abilities 表（分组 × 模型 × 渠道）。本设计让 abilities 保持「如实」：一条 ability 存在，就意味着这个分组确实能用公共 key 在该渠道跑这个模型。于是分组可见性、模型列表、定价页的分组展示都自动跟着对，不需要逐处加过滤。用户绑定是 abilities 之外的一条独立路径。

## 2. 数据契约

```go
// model/channel.go — ChannelInfo 新增
MultiKeyModels map[int][]string `json:"multi_key_models,omitempty"` // key 下标 -> 该 key 可用模型；缺失或空 = 渠道全部模型
MultiKeyUsers  map[int]int      `json:"multi_key_users,omitempty"`  // key 下标 -> 独占该 key 的用户 ID；缺失或 0 = 公共 key

// dto/channel_settings.go — ChannelOtherSettings 新增
GroupModels map[string][]string `json:"group_models,omitempty"` // 分组 -> 该分组可见模型；缺失或空 = 渠道全部模型
```

三处都是白名单，并且总是与 `channel.Models` 取交集：名单里已不存在于渠道的模型一律忽略，不报错（按标签批量改模型、上游模型自动同步之后不会留下坏状态）。模型名用客户端请求的名字（即 `channel.Models` 里的名字，模型重定向之前）。

## 3. 方法契约（PI-1 实现，放 `model/channel.go`；PI-2 调用）

```go
// 为一次请求选 key。
// modelName == "" 不按模型过滤。
// userId > 0：转发请求。若存在绑定给该用户、启用、且覆盖 modelName 的 key，只在这些 key 里选；否则只在公共 key 里选。
// userId <= 0：管理端调用（渠道测试等），不按用户过滤，已绑定的 key 也可选。
// 选择方式沿用渠道的 MultiKeyMode（随机 / 轮询）。非多 key 渠道直接返回 channel.Key。
func (channel *Channel) GetNextEnabledKeyFor(modelName string, userId int) (string, int, *types.NewAPIError)

// 公共 key（未绑定用户）能服务的模型，已与 channel.Models 取交集。非多 key 渠道 = channel.GetModels()。
// 不看 key 的启用状态（abilities 不随 key 的临时禁用变化）。
func (channel *Channel) PublicModels() []string

// 绑定给 userId 且启用的 key 能服务的模型，已与 channel.Models 取交集；该用户在本渠道无绑定时返回 nil。
func (channel *Channel) UserBoundModels(userId int) []string
```

`GetNextEnabledKey()` 保留，行为与现在完全相同，供余额查询、凭据刷新等不涉及模型和用户的管理路径继续使用。

**没有可选 key 时的错误**：
- 所有 key 都被禁用：沿用现有错误和现有后果。
- 有启用的 key，但没有一把符合本次的模型/用户条件：返回一个可重试、且不会触发渠道自动禁用的错误。PI-1 先查清 `ErrorCodeChannelNoAvailableKey` 现在在哪里被消费、会不会导致自动禁用，再决定是复用还是新增错误码。这是硬性不变量（AC8）。

## 4. key 下标稳定性（PI-1）

`MultiKeyModels` / `MultiKeyUsers` 以 key 下标为键，而删除、追加、覆盖 key 都会改变下标。规则：

- 这两张表必须跟着 key 的内容走：在任何改变 key 列表的路径上，用「旧 key 字符串 → 新下标」重新映射（同内容的 key 保留其配置；找不到对应旧 key 的新 key 没有配置；被删掉的 key 的配置丢弃）。
- 已有的三张表（状态、禁用原因、禁用时间）现有的重排逻辑不要动。
- PI-1 需要先列出全部会改变 key 列表的路径（至少：`ManageMultiKeys` 的删除类动作、`UpdateChannel` 的 `key_mode` 追加/覆盖、`Channel.Update()` 里重算 `MultiKeySize` 的那段），逐一覆盖，并用测试证明「删中间一把 key 后绑定和模型范围不串位」（AC7）。

## 5. 单 key 转多 key（PI-1）

`PUT /api/channel/` 的请求体（`controller/channel.go` 的 `PatchChannel`）新增 `to_multi_key *bool`。当它为 true 且库里的渠道是单 key：

- 置 `IsMultiKey = true`；`MultiKeyMode` 取请求里的 `multi_key_mode`，没给就用随机。
- key 列表：`key_mode` 为追加（默认）时 = 原 key + 本次提交的新 key；为覆盖时 = 本次提交的新 key；本次没提交 key 时 = 仅原 key。去重后重算 `MultiKeySize`。
- 创建渠道时不允许多 key 或 key 本身是多行/JSON 结构的渠道类型，这里同样拒绝；校验逻辑照搬创建路径（`controller/channel.go` 里 `multi_to_single` 附近），不要另写一套。
- 渠道已是多 key 时忽略该字段。不提供反向转换。

## 6. 多 key 管理接口扩展（PI-1）

`POST /api/channel/multi_key/manage`（`ManageMultiKeys`）新增动作，沿用现有请求结构和权限（`authz.ChannelOperate`）：

| action | 参数 | 说明 |
|---|---|---|
| `set_key_models` | `key_index`, `models []string` | 空数组 = 清除限制；模型必须属于渠道模型 |
| `set_key_user` | `key_index`, `user_id int` | 0 = 解绑；用户必须存在 |

现有的 key 状态列表动作，每行追加返回 `models`、`user_id`、`username`。

两个新动作成功后都要：保存 `channel_info` → 重建该渠道 abilities（公共可服务模型可能变化）→ 按现有动作同样的方式刷新渠道缓存 → 写审计记录（不含 key 内容）。

面向控制台的提示文案按 AGENTS.md：英文原文 + `common.NewMessage` / `common.ApiErrorT` / `common.ApiSuccessT`，新增的英文 key 逐行写入 `$T/i18n/PI-1.txt`。

## 7. abilities 生成（PI-2，`model/ability.go`）

一个渠道的 abilities = 对渠道的每个分组 g：

```
模型集合 = channel.PublicModels() ∩ (GroupModels[g] 非空 ? GroupModels[g] : 全部)
```

- `AddAbilities`、`UpdateAbilities` 以及其它任何从渠道推导「分组 × 模型」的地方（修复 abilities、按标签批量编辑等，PI-2 负责找全）必须用同一份推导逻辑。
- 未使用新配置的渠道，生成结果必须与现在逐条相同（AC9）。
- 渠道 `settings` 变化后要确保 abilities 被重建；PI-2 确认现有更新路径是否已经无条件重建，没有就补上。

## 8. 用户绑定路由（PI-2）

**索引**：`用户 ID → 有该用户绑定 key 的渠道 ID 列表`，放内存。约束：
- 由渠道数据在 Go 里过滤生成，不写任何方言相关的 JSON SQL，不加表不加列。
- 渠道增删改、多 key 管理动作之后要及时刷新；多实例部署下靠现有的周期同步追上。
- `MemoryCacheEnabled` 关闭时也必须能用（例如带过期时间的惰性重建）。具体做法由 PI-2 选最小的一种，写进报告。

**查找**：`model.GetUserBoundChannel(userId int, modelName string) *Channel` —— 在索引里取该用户的渠道，保留「渠道启用且 `modelName ∈ channel.UserBoundModels(userId)`」的，按现有优先级/权重规则选一个；没有返回 nil。

**接入点**：`middleware/distributor.go` 的分发流程里，位置在「令牌校验、令牌模型限制、管理员令牌指定渠道」之后、「按分组选渠道」之前。命中时直接使用该渠道，并跳过会因为「该分组没有这个模型」而拒绝请求的那些检查。未命中时后续流程一行不变。

- `SetupContextForSelectedChannel` 以及转发路径上其它调用 `GetNextEnabledKey()` 的地方，改为 `GetNextEnabledKeyFor(请求模型, 用户ID)`。PI-2 负责找全转发路径上的调用点（含重试、任务类、实时类入口）；渠道测试路径归 PI-1，改为 `GetNextEnabledKeyFor(测试模型, 0)`。
- 重试逻辑不改：首发用绑定渠道，重试走现有的分组选渠道。
- 本仓库有渠道粘滞/故障转移的定制逻辑。绑定路由优先于粘滞；绑定渠道不得写进会被其他用户命中的粘滞缓存。PI-2 核实后在报告里写明结论。
- 计费用的分组保持为用户/令牌当前分组，不要动任何计费字段。

**模型列表**：`/v1/models`（`controller/model.go` 的 `ListModels` 及其它格式的同类列表）和控制台的用户可用模型接口，在现有结果上并入 `model.GetUserBoundModels(userId int) []string`（该用户所有绑定渠道的 `UserBoundModels` 并集）。令牌模型限制的过滤照旧生效。

## 9. 前端（PI-3，`web/`）

- **渠道编辑抽屉**
  - 编辑单 key 渠道时提供「转为多 Key」开关；打开后可选多 key 模式（随机/轮询）、可填要追加的 key。提交时带 `to_multi_key`、`multi_key_mode`、`key_mode`。创建渠道的界面不变。
  - 「分组可见模型」：对已选的每个分组，可从本渠道的模型里多选；不选 = 全部可见。读写 `settings.group_models`，并保证 `settings` 里其它字段在读写过程中不丢。渠道的分组或模型被删掉时，同步清掉失效项。
- **多 Key 管理对话框**：每行显示该 key 的可用模型和绑定用户；行操作增加「设置可用模型」（从渠道模型多选）和「绑定用户」（优先复用仓库里已有的用户选择组件；没有就用用户 ID 输入，并显示接口返回的用户名）、「解绑」。
- 类型、请求封装、七种语言文案（含 `$T/i18n/PI-1.txt`、`$T/i18n/PI-2.txt` 里后端新增的英文 key）。
- 严格按 `web/AGENTS.md`：先复用现有组件，说明任何新写的通用 UI 的理由。

## 10. 不变量与红线

1. 不新增表、列、GORM 标签、迁移。发现非改不可时停下来，写进报告，不要自行加。
2. 不改计费相关文件（清单见 AGENTS.md 的 Billing rules）。发现非改不可时，先完整读 `.agents/rules/billing.md`，并在报告里单列说明。
3. 不涉及登录、会话、令牌鉴权流程本身。
4. 绑定给用户的 key 在任何转发路径上都不能被其他用户选到。
5. 未使用新配置的渠道行为零变化。
6. 「暂时没有符合条件的 key」不会自动禁用渠道。
7. 日志、审计、接口返回里不出现完整 key。
8. 受保护的项目标识不得改动（AGENTS.md Project Governance）。

## 11. 需要在调研阶段核实的假设

各包先在自己的范围内核实，结果写进 `$T/research/<包ID>.md`。假设不成立时：属于本包内部的，用最接近设计意图的最小做法并在报告里记录偏差；涉及第 2、3 节契约的，在 `$T/dispatch/contract-notes.md` 追加一条说明（写清改了什么、为什么），再继续。

- A1（PI-1）`channel_info` 列的类型和序列化方式能容纳新增的两个 map，新增字段不改变既有数据的读写。
- A2（PI-1）渠道列表/详情接口返回 `channel_info`，前端能读到新字段；返回里不带完整 key。
- A3（PI-1）渠道测试路径如何选 key、失败后在什么条件下自动禁用 key 或渠道。
- A4（PI-2）`channels.settings` 对应 `dto.ChannelOtherSettings`，更新渠道时 abilities 会被重建。
- A5（PI-2）渠道增删改之后渠道缓存的刷新方式和时机；`MemoryCacheEnabled` 关闭时选渠道走的路径。
- A6（PI-2）分发中间件里各检查的先后顺序、自动分组与跨分组逻辑、粘滞/故障转移逻辑各自的位置。
- A7（PI-3）前端如何组装 `settings` 字符串、多 key 管理对话框的数据来源、是否已有用户选择组件。
- A8（PI-4）CI 实际执行哪些检查；推送 `main` 之后发布流程如何产生版本号和 tag。
