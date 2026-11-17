# PI-1 报告：key 元数据与管理接口

## 改动的文件

- `model/channel.go`：`ChannelInfo` 新增 `MultiKeyModels` / `MultiKeyUsers`；新增 `GetNextEnabledKeyFor`、`PublicModels`、`UserBoundModels`、`keyServesModel`、`modelsServedByKeys`、`remapKeyScopes`；`GetNextEnabledKey()` 改为 `GetNextEnabledKeyFor("", 0)`（行为不变）；`Channel.Update()` 在重算 `MultiKeySize` 后重映射两张新表。
- `relaykit/types/error.go`：新增 `ErrorCodeChannelKeyScopeUnsatisfied = "channel_key_scope_unsatisfied"`（见「偏差 1」）。
- `controller/channel.go`：`PatchChannel.ToMultiKey`；`UpdateChannel` 的单 key → 多 key 转换；`ManageMultiKeys` 新增 `set_key_models` / `set_key_user`；key 状态行追加 `models` / `user_id` / `username`；`CopyChannel` 深拷贝 channel_info 的五个 map。
- `controller/channel_authz.go`：`to_multi_key` 归入敏感字段（与 `key_mode` 同类）并加显式判定。
- `controller/channel-test.go`：渠道测试选 key 走管理端语义。
- `controller/channel_multi_key_test.go`：新增 `TestMultiKeyKeyScopeSelection`、`TestMultiKeyManageKeepsKeyScopesOnTheirKey`、`TestUpdateChannelConvertsSingleKeyChannel`（复用该文件已有的 fixture 与 `putUpdateChannel`）。
- `$T/i18n/PI-1.txt`：9 个新后端文案 key。

## 假设核实（详见 `research/PI-1.md`）

- A1 成立：JSON 列存入两个 map，无 DDL。
- A2 成立：列表/详情接口返回 `channel_info`，`Omit("key")` 保证不带完整 key；新字段只含模型名与用户 ID。
- A3 成立：渠道测试的选 key 在 `middleware/distributor.go:671`；失败后由 `ShouldDisableChannel` 决定是否 `processChannelError` → 禁用 key/渠道。

## 与设计的偏差及原因

1. **新增错误码而不是复用**：`ErrorCodeChannelNoAvailableKey` 是 `channel:` 前缀，`types.IsChannelError` → `ShouldDisableChannel` 返回 true（`service/channel.go:60` + `relaykit/types/error.go:371`），复用必然触发自动禁用。新码 `channel_key_scope_unsatisfied`（503）在默认 500–503 重试范围内、不在 401 自动禁用范围内、文案不命中禁用关键词，因此可重试且不禁用。测试用 `service.ShouldDisableChannel` 直接钉住这两者差异（AC8）。
2. **`to_multi_key` 判为敏感字段**：`channelHasSensitiveChanges` 对未知字段 fail-closed，不分类会让所有带该字段的请求被当成敏感变更；同时转换与 `key_mode` 同类（改变 key 的用法），故要求 `ChannelSensitiveWrite`。
3. **转换的类型校验**：创建路径后端无显式类型限制（`multi_to_single` 只做 Vertex 数组解析），限制在 `web/.../channel-mutate-drawer.tsx`（type 57、Vertex + api_key）。转换按同一集合拒绝（Codex、Vertex 且 `vertex_key_type=api_key`），文案 `This channel type does not support multiple keys`。
4. **`CopyChannel` 保留 key 绑定并深拷贝**：blast-radius 建议「拷贝不带绑定」。未采纳：复制出的渠道里是同一把凭据，解绑等于把这把用户专属 key 变成公共 key，违反 R1；且同内容的 key 在同一用户的两个渠道上绑定不违反「一个 key 只绑一个用户」。只把五个 map 深拷贝，避免与原渠道共享可变状态。
5. **渠道测试临时把 `id` 置 0**：`SetupContextForSelectedChannel` 用 `c.GetInt("id")` 取用户（PI-2 的改法），测试上下文里该值是 testUserID。为了让「已绑定的 key 也可选」（design §3 的 userId<=0 口径），在调用前后包一层置 0/还原，计费与日志的 id 不受影响。
6. **单 key 转多 key 的 key 列表**：转换后默认 `key_mode=append`，直接复用既有追加逻辑（去重、`MultiKeySize` 重算），Vertex 非 api_key 渠道按行切分（不解析 JSON 数组，创建路径的数组解析只用于建渠道）。
7. **重映射只按 key 内容**：`remapKeyScopes` 以「旧 key 字符串 → 旧下标」匹配。同一渠道存在内容完全相同的重复 key 时，只有首个下标能带配置；这是 design §4 规则的直接结果。

## 验证（本机 PowerShell，`$go = D:\AppData\Local\Temp\new-api-cx-a-tools\go\bin\go.exe`，`GOWORK=off`）

- `gofmt -l model/channel.go controller/channel.go controller/channel_authz.go controller/channel-test.go controller/channel_multi_key_test.go relaykit/types/error.go` → 无输出。
- `go build ./...`（root）→ 通过。
- `go vet ./...`（root）→ 通过。
- `go vet ./...` + `go build ./...`（`relaykit/`）→ 通过（AGENTS.md 要求）。
- `go test ./controller/ -run "TestMultiKeyKeyScopeSelection|TestMultiKeyManageKeepsKeyScopesOnTheirKey|TestUpdateChannelConvertsSingleKeyChannel" -v` → 全 PASS（含按模型选 key、绑定独占/优先、全部绑定无公共模型、无符合 key 的错误码与不禁用、删除中间 key 后配置不串位、单转多三种 key 组合、Codex 拒绝转换）。
- `go test ./controller/ -run "TestMultiKey|TestChannelFieldsAreClassified|TestAddChannelReturnsCreatedIDs|TestChannelStatus|TestChannelCreate"` → ok（既有用例无回归）。
- `go test ./model/... -run "TestAbility|TestChannel"` → ok。
- `go test ./relaykit/types/...`（relaykit 内）→ no test files。
- 数据库：无表结构/GORM 标签/迁移/原生 SQL 变化，只改 JSON 列内容；测试在 SQLite（3.50.4，临时库）实跑。MySQL / PostgreSQL 未验证（本机无实例；本包无方言相关改动）。

## 遗留问题（交给 PI-2 / PI-4）

1. `middleware/distributor.go:134` 仍忽略 `SetupContextForSelectedChannel` 的返回值。新错误码是 503 且不自动禁用，但该处若不处理，首次选渠道的请求会带着空 key 继续转发而不是重试换渠道——AC8「能换渠道」需要 PI-2 处理（属其名下文件）。
2. `handlerMultiKeyUpdate`（按 key 写状态）未按模型的可用性做判断：某 key 因失败被自动禁用后，abilities 不会变化（按设计，abilities 不看 key 启用状态），因此「唯一支持该模型的 key 被禁用」时仍会选到该渠道，最终由上面的新错误码兜底重试。
3. 多 key 渠道的 `MultiKeyModels` / `MultiKeyUsers` 只能通过 `multi_key/manage` 修改；`PUT /api/channel/` 会用库里的 `channel_info` 覆盖请求里的值（既有逻辑，A2 与 design §6 一致），前端不要试图在编辑渠道时直接写这两张表。
