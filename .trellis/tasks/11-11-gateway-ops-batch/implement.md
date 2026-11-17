# Implement：工作包与分派

任务目录：`.trellis/tasks/11-11-gateway-ops-batch/`（下文 `$T`）。需求与口径见 `$T/prd.md`。

| 工作包 | agent（herdr 名 / pane） | 内容 |
|---|---|---|
| CX-A | codex `na-cx-a`（`w4C:pD`） | R1 渠道失败切换；R8 分组等级 |
| CX-B | codex `na-cx-b`（`w4C:pF`） | R9 邀请码注册；R3 登录二次验证可选 |
| PI-1 | pi `na-pi-1`（`w4C:p9`） | R5 排行榜缓存用量样式 |
| PI-2 | pi `na-pi-2`（`w4C:pA`） | R6 缓存命中率卡片；R7 日志模糊筛选 |
| PI-3 | pi `na-pi-3`（`w4C:pB`） | R4 登录网关头像 |
| PI-4 | pi `na-pi-4`（`w4C:pC`） | R2 速率限制选分组 + 按用户覆盖 |
| INT（第二波） | codex `na-cx-a` | 合并 i18n、全量验证、总报告 |

巡检：claude `na-watcher`（`w4C:pE`），按 `$T/watcher.md` 执行。

## 所有 worker 的共同规则

1. **先读规矩再动手。** 用读文件工具完整读 `AGENTS.md`；改前端前再读 `web/AGENTS.md`；碰到计费（`AGENTS.md` 里 Billing rules 的定义）再读 `.agents/rules/billing.md`。然后读 `$T/prd.md` 里自己的需求和本文件里自己的工作包。任务已创建并启动，不要再建 Trellis 任务，也不要问用户要不要建。
2. **自己读代码。** 本文件给的文件路径和行号只是起点，是调度者粗查的结果，可能不全或有偏差。结论以代码为准。
3. **六个 agent 同时在同一个工作树里干活。** 只改自己「所有权」内的文件。所有权外的文件一律只读，需要别人改的写进报告的「需要他人处理」。
4. **共享热点文件**（多人都要插几行的）：`model/option.go`、`model/main.go`、`router/api-router.go`、`controller/misc.go`、`controller/option.go`、`common/constants.go`、`web/src/features/system-settings/types.ts`、`web/src/lib/api.ts`、导航 / 侧边栏配置、路由树生成文件。规则：只做最小的插入式修改；每次修改前重新读取该文件的当前内容；不整文件重写、不重排、不顺手改别人的行。能放进自己新建的文件就放新文件，再在热点文件里只加一行注册。
5. **翻译文件不要直接改。** `web/src/i18n/locales/*.json` 由第二波统一合并。你新增或修改的每个文案 key（包括后端 `common.NewMessage` / `ApiErrorT` 给控制台的英文原文）写进 `$T/i18n/<工作包ID>.json`，格式：
   ```json
   { "English source string": { "zh": "…", "zh-TW": "…", "fr": "…", "ru": "…", "ja": "…", "vi": "…" } }
   ```
   六种语言都要给，中文用词与现有界面一致。组件里照常写 `t('English source string')`。
6. **git 只读。** 不 `commit` / `push` / `stash` / `checkout` / `reset` / `restore` / `clean`，不切分支。工作树里已有一批未提交的兑换码改动（同批每用户限兑一次），不是你的，不要回退也不要「修」它。不要撤销别的 agent 的改动。
7. **验证自己跑、照实写。** 只跑和自己改动相关的检查（相关 Go 包的 `go build` / `go vet` / `go test`，前端的类型检查和相关测试）。别人正在改的文件可能让构建暂时失败：错误在你所有权之外时，等一会儿重试，仍失败就在报告里写明是谁的文件、什么错，不要去改。不要留下常驻的开发服务器或后台进程。
8. **数据库改动**按 `AGENTS.md` 的要求验证（SQLite / MySQL / PostgreSQL 真实实例、全新库与升级库、启动两次）。本机能起容器就做；做不了就在报告里明确写「未验证」和阻塞原因，不要声称兼容。
9. **认证相关改动**（R3、R9、R4 的 OAuth 部分）按 `AGENTS.md` 的 OWASP 条款执行，报告里写引用的条目、做过的验证、遗留缺口。
10. **遇到没说清的地方**：按最合理的方式实现，在报告里写明你的决定和理由。不要停下来等用户，也不要向用户提问。
11. **完成后写报告** `$T/reports/<工作包ID>.md`（不超过 60 行）：改了哪些文件；验证命令与实际结果（失败就写失败）；与 prd 口径的差异和你做的决定；未完成 / 有风险的点；需要他人处理的事。写完报告就结束，不要扩大范围。
12. 不在 `docs/` 下新增文件；不改项目与组织的名称、版权等受保护信息。

## 第一波（并行）

### CX-A · R1 渠道失败切换 + R8 分组等级（codex `na-cx-a`，重活）

所有权：
- 后端：`service/channel_affinity.go`、`service/channel_select.go`、`service/relay_error.go`、`service/request_policy.go`、`service/group.go`、`model/request_policy.go`、`model/channel_cache.go`、`model/channel_constraint.go`、`setting/operation_setting/channel_affinity_setting.go`、`setting/user_usable_group.go`、`setting/ratio_setting/group_ratio.go`、`controller/relay.go`、`controller/group.go`、`controller/pricing.go`、`middleware/distributor.go`、`relay/responses_websocket.go`，以及这些文件对应的测试。
- 前端：`web/src/features/system-settings/request-policies/**`、`web/src/features/system-settings/general/channel-affinity/**`、`web/src/features/system-settings/models/**` 里与分组有关的文件（`group-ratio-form`、`group-ratio-visual-editor`、`group-special-usable-editor` 及其测试）。
- `middleware/auth.go` 需要改时只做最小插入（CX-B 也可能碰）。

先做 R1：
1. 按 prd R1「已查明的原因」核对代码，确认或修正这三点，再动手。
2. 实现 prd R1 的三项改进和三个设置项。新选项要进 `model/request_policy.go` 的白名单与校验（`IsRequestPolicyOption`、`BuildRequestPolicy` 里对 affinity 字段的白名单），键名与现有风格一致。连续失败计数需要跨请求保存，按会话绑定键计，复用现有的 affinity 缓存设施，带过期时间。
3. 「避开已失败渠道」优先通过现有的渠道过滤机制实现（`GetChannelConstraints(c).Filters` / `filterCandidateIDs`），不要大改 `GetRandomSatisfiedChannel` 的签名；内存缓存关闭时走的 `GetChannel` 数据库路径也要一致。
4. 请求策略页：新设置放进重试 / 路由对应的分区，带说明；规则表显示实际生效模式和「全部改为继承默认」；决策记录新增的 reason 要有文案。
5. 测试：在现有测试文件里补（`controller/channel_pin_retry_test.go`、`service/` 下 affinity / channel select 的测试），覆盖 AC1.1–AC1.4，用确定性的假渠道，不用随机和 sleep。

再做 R8：
1. 找到「某用户分组可用哪些分组」的唯一计算点（`service/group.go` 的 `GetUserUsableGroups` 及其调用方：令牌分组校验、定价 / 模型可见性、`auto` 分组、`middleware/auth.go`、`middleware/distributor.go`），把等级规则注入这一处。
2. 等级存成新的系统选项（分组名 → 整数等级），在 `model/option.go` 注册（热点文件，最小插入），不改表结构。
3. 分组设置界面加等级编辑与可见范围预览。
4. 测试覆盖 AC8.1–AC8.3，补进现有的分组测试文件（`service/group_auto_groups_test.go` 等）。

报告：`$T/reports/CX-A.md`。

### CX-B · R9 邀请码注册 + R3 登录二次验证可选（codex `na-cx-b`，重活）

所有权：
- 后端：`controller/user.go`、`controller/oauth.go`、`controller/wechat.go`、`controller/twofa.go`、`controller/passkey.go`、`controller/login_verification.go`、`service/login_verification.go`、`service/security_verification.go`、`service/account_security.go`、`model/login_verification.go`、`model/twofa.go`、`model/auth_flow.go`，新建的 `model/invitation*.go`、`controller/invitation*.go`，以及对应测试（`controller/auth_flow_test.go`、`controller/security_enrollment_test.go` 等）。
- 前端：`web/src/features/auth/**`、`web/src/features/system-settings/auth/**`、`web/src/features/system-settings/security/**`、个人资料里安全设置相关的组件、新建的 `web/src/features/invitation-codes/**` 及其路由文件。
- 只读参考：`model/redemption.go`、`controller/redemption.go`、`controller/redemption_batch_test.go`、`web/src/features/redemption-codes/**`（有别人未提交的改动，**不要改**）。
- 注意：PI-3 会在 `controller/oauth.go` 里 `findOrCreateOAuthUser` 调用点之后插入一行头像同步调用，并在 `oauth/` 下加头像字段提取。看到这类改动请保留。

先做 R9：
1. 通读兑换码的模型、接口、路由、权限注册、管理页，照同样的结构做邀请码（新表、新接口、新管理页、导航与权限注册）。表在 `model/main.go` 的迁移列表里登记（热点文件，最小插入）。
2. 新开关 `InviteRegisterEnabled`（命名与 `RegisterEnabled` 一致），在 `common/constants.go`、`model/option.go`、`controller/misc.go` 的状态接口里登记（都是热点文件）。
3. 服务端按 prd R9 的表强制执行：`controller/user.go` 的 `Register`、`controller/oauth.go` 的 `findOrCreateOAuthUser`（现在在 `!common.RegisterEnabled` 处直接拒绝）、`controller/wechat.go`，以及其它任何会新建用户的入口，自己搜全。邀请码随 OAuth 往返的方式参考 `oauthStateRequest.Aff` → `oauthFlowPayload.AffiliateCode`。
4. 核销与建号同事务，行锁用 `lockForUpdate(tx)`，并发只成功一次。
5. 前端：登录页注册按钮、注册页表单按表显示；系统设置里加开关。
6. 测试覆盖 AC9.1–AC9.3，集中在尽量少的测试文件里。

再做 R3：
1. 入口是 `service/security_verification.go` 的 `securityVerificationPolicy`（`VerificationScopeLogin` 分支）和 `service/login_verification.go` 的 `StartLoginVerification`。只改登录这一处的判定，其它 scope 不动。
2. 个人开关与全站策略的存放位置自己定（尽量不改表；要改表就按规则 8 验证），关闭个人开关要走现有的安全验证证明机制。
3. 前端：个人安全设置加开关（未录入因子时禁用并说明），系统设置加全站策略。
4. 测试覆盖 AC3.1–AC3.4。

报告：`$T/reports/CX-B.md`。

### PI-1 · R5 排行榜缓存用量样式（pi `na-pi-1`，小活）

所有权：`web/src/features/rankings/**`。
- 入口：`web/src/features/rankings/components/cache-token-details.tsx`，被 `market-share-section.tsx`、`model-leaderboard.tsx`、`models-section.tsx` 使用；测试在 `web/src/features/rankings/__tests__/cache-tokens.test.tsx`。
- 按 prd R5 的口径重排。先弄清总量和缓存各用了哪个格式化函数、为什么一个是 `8.24B` 一个是 `30.2亿`，统一成一套（遵守 `AGENTS.md` 的数字格式化规定）。
- 改完更新测试，跑该目录的测试和类型检查。在报告里用文字画出改后的排版（宽屏 / 窄屏各一）。

报告：`$T/reports/PI-1.md`。

### PI-2 · R6 缓存命中率卡片 + R7 日志模糊筛选（pi `na-pi-2`，小活）

所有权：`web/src/features/usage-logs/**`、`controller/log.go`、`model/log.go` 及其测试。
- R6：前端入口 `web/src/features/usage-logs/components/common-logs-stats.tsx`（`StatBadge`），数据来自日志统计接口（`controller/log.go` → `model/log.go` 的 `SumUsedQuota`，返回 `quota / rpm / tpm`）。先查清日志行里缓存 token 存在哪（独立列还是 `other` JSON），参考上一个提交 `2a1df8ae3` 对缓存 token 的处理和「结算语义」的判断方式，再决定怎么聚合；三种数据库加 ClickHouse 日志库都要能跑。聚合代价太大或做不到准确时，在报告里写清原因和你选的折中，不要硬凑数字。
- R7：`model/log.go` 里 `LogFilter.apply` 目前多为 `=`，已有 `buildLogLikeCondition` / `applyExplicitLogTextFilter` 可复用。按 prd R7 加模糊 / 精确切换，接口加参数，前端筛选区加切换控件，状态进 URL。
- 测试补进现有文件（`model/log*_test.go`、`web/src/features/usage-logs/components/__tests__/`）。

报告：`$T/reports/PI-2.md`。

### PI-3 · R4 登录网关头像（pi `na-pi-3`，小活）

所有权：`oauth/**`、`web/src/lib/avatar.ts`、`web/src/components/ui/avatar.tsx`、`web/src/components/profile-dropdown.tsx`、`web/src/components/layout/components/mobile-drawer.tsx`、`web/src/features/profile/components/profile-header.tsx`，以及前端当前用户类型 / 登录态 store 里加头像字段所需的最小改动。
- 只读：`D:\MyNewWebProjects\azurekiln-oauth2`（确认 `picture` 的来源和格式）。
- `controller/oauth.go` 与 `controller/user.go` 归 CX-B：你只能在 `controller/oauth.go` 里 `findOrCreateOAuthUser` 调用点之后插入**一行**头像同步调用，逻辑放在你自己新建的文件里；返回给前端的用户数据里加头像字段时同样最小插入。
- 自定义 OAuth 提供方（`oauth/generic.go`，按 gjson 路径取字段）和 OIDC（`oauth/oidc.go`）都要取头像；保存与校验口径见 prd R4。用户自己改设置的接口不能写这个字段，也不能把它冲掉。
- 前端显示图片时保留现有首字母色块作为回退。

报告：`$T/reports/PI-3.md`。

### PI-4 · R2 速率限制选分组 + 按用户覆盖（pi `na-pi-4`，小活）

所有权：`setting/rate_limit.go`、`middleware/model-rate-limit.go`、`middleware/model_rate_limit_test.go`、`web/src/features/system-settings/request-limits/**`、`web/src/features/users/**`，新建的 `controller/user_rate_limit.go`（用户级限流的管理接口放这里，不要改 `controller/user.go`）。
- 现状：`middleware/model-rate-limit.go` 的 `ModelRequestRateLimit` 按令牌分组（没有则用户分组）查 `setting.GetGroupRateLimit`；分组配置是选项 `ModelRequestRateLimitGroup`（`{"分组": [总次数, 成功次数]}`）；前端编辑在 `rate-limit-dialog.tsx` / `rate-limit-visual-editor.tsx`，分组名是手输的。
- 按 prd R2 实现：分组改下拉（找现有取分组列表的接口和组件复用）；用户级覆盖（新选项在 `model/option.go` 注册、路由在 `router/api-router.go` 注册，都是热点文件）；用户编辑抽屉里加「速率限制」一栏（跟随分组 / 单独设置）；分组限流编辑里加「同时覆盖该分组内已手动设置的用户」。
- 多节点下选项靠现有的选项同步传播，确认用户级覆盖也能传播到。
- 测试覆盖 AC2.1–AC2.4，补进现有测试文件。

报告：`$T/reports/PI-4.md`。

## 第二波：INT 集成（codex `na-cx-a`，六份报告齐后由巡检派发）

1. 读 `$T/reports/` 下六份报告，处理各自「需要他人处理」里属于集成的事项。
2. 把 `$T/i18n/*.json` 合并进 `web/src/i18n/locales/` 的七个文件（`en` 的值等于 key），保持文件现有的排序和格式，按 `web/AGENTS.md` 跑 i18n 工具检查缺失 / 多余的 key。
3. 全量验证：根模块 `go build ./...`、`go vet ./...`、改动涉及的包 `go test`；`relaykit/`、`tokenkit/` 若被改动，分别在模块内 `GOWORK=off go build ./...`；前端类型检查、lint、相关测试、`bun run build`。
4. 修复跨工作包的冲突和编译 / 类型错误（这一步可以改任何人的文件，改动写进报告）。
5. 对照 prd 逐条 AC 写总报告 `$T/reports/FINAL.md`：每条 AC 的状态（通过 / 未验证 / 未完成）、所有 **[假设]** 的清单、数据库验证做到了哪一步、需要用户决定的事。不提交、不推送。
