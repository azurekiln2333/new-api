# CX-A · R1 渠道失败切换 / R8 分组等级
日期：2026-11-11。实现已落盘；完整验证仍有下述限制。未提交、未推送、未改语言文件、未启动常驻服务。

## 实现与文件
- R1 设置：`setting/operation_setting/channel_affinity_setting.go`、`model/request_policy.go`；默认避开失败渠道、失败阈值 1，内置规则继承默认，保留旧规则 JSON。
- R1 路由：`model/channel_cache.go`、`model/channel_constraint.go`、`service/channel_select.go`；先同档未失败渠道，再低档；跨组重试优先后续组未失败渠道，耗尽后允许重复。
- R1 绑定：`service/channel_affinity.go`、`service/request_policy.go`、`service/relay_error.go`；跨请求失败计数、内存锁 / Redis Lua 原子解绑、TTL、成功重绑、决策记录。
- R1 入口：`controller/relay.go`、`relay/responses_websocket.go`；普通转发、任务提交、WS 握手共享失败跟踪；不重放已输出响应、已接受任务及不可重试错误。
- R8 后端：`setting/user_usable_group.go`、`service/group.go`、`controller/group.go`；GroupLevels 校验、唯一可用分组计算点、管理员预览接口。
- 共享热点仅插入：`model/option.go` 注册 / 校验 / 持久化 GroupLevels；`router/api-router.go` 在既有 AdminAuth 分组注册 POST `/api/group/preview`。
- 后端测试：`controller/channel_pin_retry_test.go`、`model/request_policy_test.go`、`service/channel_affinity_template_test.go`、`service/channel_select_auto_groups_test.go`、`service/relay_error_test.go`、`service/group_auto_groups_test.go`。
- 前端请求策略：`web/src/features/system-settings/request-policies/{defaults.ts,policy-label.ts,retry-section.tsx,routing-form.ts,routing-section.tsx,__tests__/routing.test.tsx}`。
- 前端会话规则：`web/src/features/system-settings/general/channel-affinity/{constants.ts,index.tsx,session-rules-table.tsx,types.ts}`；显示旧版严格模式，批量改为继承后由管理员保存。
- 前端分组：`web/src/features/system-settings/models/{group-ratio-visual-editor.tsx,group-special-usable-editor.tsx,__tests__/group-settings.test.tsx,__tests__/group-pricing-decimals.test.tsx}`。
- 文案：`i18n/CX-A.json`，21 个 key，各含 zh / zh-TW / fr / ru / ja / vi；不直接写七个语言文件。

## 实际验证
- 工具：Go 1.26.10、Bun 1.4.4；便携工具位于 `%TEMP%/new-api-cx-a-tools`。
- 通过：`go build ./controller ./service ./model ./relay ./setting ./setting/operation_setting`。
- 通过：`go vet ./controller ./service ./model ./relay ./setting ./setting/operation_setting`。
- 通过：`go test ./controller ./service ./model ./relay -run 'Test(ChannelAffinity|AffinityFailover|RetryAvoids|RequestPolicy|GroupLevels|ShouldRetry|SessionRules|ResponsesWS|DecideRelayRetry|CacheGetRandomSatisfiedChannel|GetRequestAutoGroups)' -count=1`，四包均通过。
- 通过：`go test ./service ./model -run 'Test(ChannelAffinityConsecutiveFailures|RetryAvoids|RequestPolicyDatabaseMatrix|GroupLevels)' -count=1 -v`；SQLite 3.50.4；MySQL / PostgreSQL 子测试因无 DSN 跳过。
- 覆盖：阈值 0/1/2、每次尝试只计一次、成功清零、关闭成功切换不清零旧渠道、非重试错误仍停止、TTL、同档优先、耗尽后重复、关闭避让恢复旧路由、缓存开关及跨组避让。
- 覆盖：五级可见范围、未设等级兼容、手工授权并集、伪造 / 过期 Auto 分组过滤、非法等级保持原配置、选项保存与重复载入及保存失败回滚。
- Redis 分支通过 miniredis 执行 Lua 与时间推进验证；未连接真实 Redis / 多节点实例。
- `web/` 通过：`node node_modules/vitest/vitest.mjs run src/features/system-settings/request-policies/__tests__ src/features/system-settings/models/__tests__/group-settings.test.tsx src/features/system-settings/models/__tests__/group-pricing-decimals.test.tsx`，5 文件 / 71 测试。
- 14 个 CX-A 前端文件经保护原版权头的 oxfmt 格式化；`bun x oxlint -c .oxlintrc.json` 定向检查零 error，保留 constants.ts 原有 prefer-structured-clone warning。
- 失败且已重试：`cd web; bun run typecheck`，仅剩 PI-4 的 `src/features/system-settings/request-limits/__tests__/group-rate-limit-editor.test.tsx:22`：TS6133，useState 未使用；未越权修改。
- 通过：CX-A 文件 `git diff --check`、修改 Go 文件 gofmt 检查；新增 t() key 对照现有语言文件 / 暂存翻译无遗漏，21 key 的六语言与插值检查通过。
- 早期 controller 编译被 PI-4 user_rate_limit.go 返回值数量阻塞，后续已由外部修复；重跑通过。既有模型管理夹具泄漏 SQLite 连接导致 Windows 清理失败，已仅在自有测试文件采用复用数据库创建函数且显式关闭连接的路由夹具。

## 决定与口径
- 核对确认 PRD R1 的三个原因：旧规则覆盖默认为 strict；失败不解绑；重试按档随机且未排除失败渠道。
- 不改所有权外的 DTO：新选择入口复用 filterCandidateIDs / ChannelSatisfiesFilters；数据库路径采用相同谓词，保留 GetRandomSatisfiedChannel 签名与权重算法。
- 重试预算仍按原有每组计数；固定渠道约束优先。阈值只移除绑定，不绕过不可重试、已输出及预算停止条件。
- 失败阈值限定 0..1000000；等级为有符号 32 位整数，空值表示移除等级，auto 不参与等级。
- 等级与手工授权取并集，包括已有特殊可用分组；五级严格递增示例需先取消手工对高等级的显式授权，等级不能撤销管理员已授予的权限。
- 复核令牌分组校验、TokenAuth、Playground、定价和 Auto 均使用既有共享可用分组计算；无需改认证流程。
- 等级独立保存，避免修改无所有权的 billing/section-registry.tsx；预览使用当前等级 / 可见性草稿与服务端已保存的定价分组，新建分组须先保存定价表。
- UI 复用 SettingsSwitchField、Form、Input、Combobox、LoadingState、ErrorState、Card 及现有设置查询 / 保存 hook，未新增通用交互替代组件。

## 未验证 / 需要他人处理
- MySQL、PostgreSQL 真实实例及完整数据库矩阵未验证：无 TEST_MYSQL_DSN / TEST_POSTGRES_DSN、无 Docker，WSL Ubuntu 返回 HCS_E_HYPERV_NOT_INSTALLED；未宣称三数据库兼容性验证完成。
- 无表结构变更；已验证 SQLite 新建选项表、既有选项更新 / 两次载入及失败回滚，未做最近发行版整库升级演练或真实提供方端到端测试。
- PI-4 / INT 修复上述前端测试类型错误并重跑全局 typecheck；INT 合并 i18n/CX-A.json 后执行七语言同步、全量检查与生产构建。
- 完整生产 Redis 多节点故障切换、真实 WS 提供方联调及 MySQL / PostgreSQL 实例验证留给集成环境；本报告只声明上述已实际执行的检查结果。
