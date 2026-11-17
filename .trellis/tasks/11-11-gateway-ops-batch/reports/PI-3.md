# PI-3 · R4 登录网关头像

## 改动文件

后端
- `relaykit/dto/user_settings.go`：`UserSetting` 增 `AvatarUrl`（`json:"avatar_url,omitempty"`）；头像存用户 `setting` JSON，不改表结构。
- `oauth/types.go`：`OAuthUser.AvatarURL`。
- `oauth/provider.go`：新增可选接口 `AvatarProvider`（`SupportsAvatar()`），表达「该 provider 会去读图片字段」。
- `oauth/avatar.go`（新）：`AvatarURLMaxLength = 1024`、`NormalizeAvatarURL`（仅 https 绝对地址、有 host、无内嵌凭据、无控制字符、长度上限）、`SyncUserAvatar`（读改写 setting 并写库）。
- `oauth/oidc.go`：userinfo 的 `picture` → `AvatarURL`；实现 `SupportsAvatar`。
- `oauth/generic.go`：按 `picture` / `avatar` / `avatar_url` 取第一个非空值 → `AvatarURL`；实现 `SupportsAvatar`。
- `controller/oauth.go`：`findOrCreateOAuthUser` 之后插入 1 行 `oauth.SyncUserAvatar(c.Request.Context(), user, provider, oauthUser.AvatarURL)`。
- `controller/user.go`：`buildSelfUserData` 插入 1 行 `"avatar_url": userSetting.AvatarUrl`（登录响应与 `/api/user/self` 共用此 DTO）。

前端
- `web/src/lib/avatar.ts`：`AVATAR_URL_MAX_LENGTH`、`resolveUserAvatarUrl`（与后端同口径的展示前校验）。
- `web/src/components/user-avatar.tsx`（新）：共享头像组件，有可用地址显示图片（`referrerPolicy='no-referrer'`），否则首字母色块；加载失败由 Base UI Avatar 的 Fallback 兜底。
- `web/src/components/profile-dropdown.tsx`、`web/src/components/layout/components/mobile-drawer.tsx`、`web/src/features/profile/components/profile-header.tsx`：改用 `UserAvatar`（移动抽屉原来的静态 `/avatars/01.png` 占位一并去掉）。
- `web/src/stores/auth-store.ts`、`web/src/features/profile/types.ts`：加 `avatar_url?: string`。
- 测试：`oauth/avatar_test.go`、`web/src/lib/__tests__/avatar.test.ts`、`web/src/components/__tests__/user-avatar.test.tsx`（均新建）。
- `$T/i18n/PI-3.json`：`{}`（本工作包无新增文案，`alt` 用用户名）。

## 决定（prd 未写死处）
1. 存储按 prd 的 [假设] 放进 `users.setting` JSON：需要给 `relaykit/dto.UserSetting` 加字段（relaykit 公共结构变更，已在模块内 `GOWORK=off go build ./...` 验证）。`PUT /api/user/setting` 的请求结构不含该字段且从已存设置起改，`PUT /api/user/self`（侧边栏/语言）同样是读改写，因此用户既不能写入也不会把它冲掉。
2. 同步时机：每次 OAuth/OIDC 登录（`handleOAuthLogin`）在找到/创建用户之后同步一次；绑定（bind）与其它 provider 不参与。
3. 清除语义：provider 报告了图片字段但为空 → 清空（网关侧删头像能反映出来）；地址非空但不合法 → 保留旧值并记 warn；不支持头像的 provider（GitHub/Discord/LinuxDO/Telegram/微信）完全不参与，避免同一账号换 provider 登录互相清空。
4. generic provider 的头像字段没做成可配置项（`model.CustomOAuthProvider` 与管理界面不在本包所有权内），改为按 `picture`/`avatar`/`avatar_url` 依次取第一个非空值，与网关 userinfo 的 `picture` 一致。要做任意路径，建议后续加 `avatar_field`（默认 `picture`）。
5. 同步调用放在「用户状态检查」之前：被禁/未完成二步验证的登录会顺带刷新一次头像（provider 已认证该用户，不泄露信息）。如需严格只对成功登录生效，可移到 `setupLoginAtAuthVersion` 之后，但那会漏掉二步验证路径。

## 验证（实际命令与结果）
- `go build ./oauth/...`、`go vet ./oauth/...`：通过；`go test ./oauth/...`：ok（含新增用例：URL 校验表、存图并保留 language/sidebar、刷新、无图清空、无头像能力 provider 不清空、非法地址保留旧值、OIDC 与 generic 的 `picture` 提取）。
- `go build ./model/... ./service/... ./relay/... ./middleware/... ./setting/... ./i18n/...`：通过（消费 `dto.UserSetting` 的包均正常）。
- 根模块 `go build ./...` **失败**，原因不在本包：`controller/user_rate_limit.go:137:17: assignment mismatch: 2 variables but setting.GetUserRateLimit returns 3 values`（PI-4 正在改）。为确认我在 controller 的两行可编译，把 `controller/*.go`（排除 `user_rate_limit.go` 与 `_test.go`）复制到临时目录后 `go build`：通过（exit 0），临时目录已删除。
- `relaykit/` 内 `GOWORK=off go build ./...`、`go vet ./dto/...`：通过。
- `gofmt -l`（本次改动的 Go 文件）：无输出。
- 前端 `vitest run src/features/profile src/components/__tests__ src/lib/__tests__`：13 文件 / 98 用例全过（含新增 9 例）；`oxlint`、`oxfmt --check` 对改动文件通过；`bun check -b` 仅剩 1 个错误，在 CX-B 的 `src/features/invitation-codes/index.tsx(36,95)`，我的文件无类型错误。
- 数据库：无表结构/迁移/ORM 标签/raw SQL 变化，故未跑三库矩阵；SQLite 真实读写路径已由 oauth 测试覆盖。按 `AGENTS.md` 规则 8，本包不涉及需三库验证的改动。

## OWASP（R4 属认证相关）
- 引用：OWASP ASVS 5.0 V2（身份认证）/V3（会话）/V6（用户数据与凭据）、V12.3（传输安全）、V16（日志）；Authentication Cheat Sheet（不在认证路径新增旁路）。
- 已实现控制：头像只作为资料展示、不作为认证证据；地址只能由已认证的 OIDC/generic provider 提供，客户端接口不可写；仅 https 且拒绝带凭据 URL（避免凭据进入日志/前端历史）；长度与字符集限制；前端 `no-referrer`（不向第三方泄露网关页面地址）；日志只记 provider 名与用户 ID，不含地址本体。
- 遗留缺口：头像地址是第三方 URL，浏览器加载会向该域暴露用户 IP（prd 已接受）；OIDC ID Token 的 `picture` 未被使用（new-api 不校验 ID Token），只依赖 userinfo——若某 provider 仅在 ID Token 带 picture 则取不到。

## 需要他人处理
- PI-4：`controller/user_rate_limit.go:137` 与 `setting.GetUserRateLimit` 三返回值不匹配，阻塞根模块 `go build ./...` / `go vet ./controller`。
- CX-B：`web/src/features/invitation-codes/index.tsx:36` 类型错误，阻塞 `bun check -b`。
- INT：并入 `$T/i18n/PI-3.json`（空，无新 key）。建议补一条 controller 级回归「`PUT /api/user/setting` 后 `avatar_url` 仍在」——本包因上述编译阻塞未能运行 controller 测试。
- 事故说明：我执行过一次 `go fmt ./controller/...`，顺带 gofmt 了 CX-B 正在改的 `controller/invitation.go`、`login_verification.go`、`option.go`、`wechat.go` 与 `oauth.go`。纯格式化（结构体字段对齐，`git diff -w` 对比可见），无逻辑改动；其后只对自有文件做格式化。请 CX-B 知悉，无需回退。

---

# PI-3 · 计划 2026-11-12 第 3 节（日志移动端 / API 信息延迟 / Uptime 提示）

## 3.1 通用日志页移动端紧凑列表

改动文件（全部在 `web/src/features/usage-logs/`）
- `lib/mobile-log-view.ts`（新）：`MOBILE_LOG_VIEW_MODES`（`compact` / `card`）、`MOBILE_LOG_VIEW_STORAGE_KEY`（`usage-logs:mobile-view-mode`）、默认 `compact`；`readMobileLogViewMode` / `writeMobileLogViewMode` 对 localStorage 缺失、未知值、抛异常（隐私模式）三种情况都有兜底。
- `components/common-log-mobile-row.tsx`（新）：一条日志的紧凑行。第一行：时间（`MM-DD HH:mm:ss`）、类型徽标、模型（截断）、消耗额度、展开箭头；第二行：`↑输入 ↓输出 · 耗时 · 令牌名 · 用户名`（管理员视图有 `user` 列才加用户名）。整行是一个 `aria-expanded` 按钮，点开后在下方面板内渲染原有 `CommonLogMobileCard`（详情、弹窗、复制、用户信息全部复用，未改该组件）。
- `components/log-mobile-view-toggle.tsx`（新）：紧凑 / 卡片视图的分段切换控件，结构与现有 `DataTableViewModeToggle` 一致（`Button` + `Tooltip` + `aria-pressed`），没有引入新 UI 库。
- `components/usage-logs-mobile-card.tsx`：`common` 分类按视图模式分别渲染紧凑行或原卡片；新增紧凑骨架屏；切换控件渲染在列表上方右侧。task / drawing 两类日志的渲染路径完全未变。
- `components/usage-logs-provider.tsx`：上下文加 `mobileLogViewMode` / `setMobileLogViewMode`（初始值读 localStorage，写入时持久化）。
- `components/__tests__/mobile-card.test.tsx`：原有用例改为显式 `card` 模式（它们描述的是展开后的卡片），新增 5 例：默认紧凑行摘要、点击展开成完整卡片、切换并持久化、localStorage 读取兜底、操作类日志用内容摘要。

取舍
- 列显示设置仍然生效：紧凑行里每个字段都受 `cells.has(...)` 控制，隐藏列即不显示；筛选、分页、统计条未改动。
- 消耗额度在紧凑行用 `formatLogQuota` 纯文本，而不是复用表格里的 `LogCostDisplay` 单元格——后者含按钮/浮层，嵌进「整行按钮」会产生非法的嵌套交互元素。完整账单信息（附加费、计费来源）在展开后的卡片里仍在。
- 令牌名、用户名在「隐藏敏感信息」开启时显示 `••••`，与卡片行为一致。
- 操作类日志（充值/管理/系统/登录）没有模型与 token，第二行改为渲染本地化后的内容摘要（`renderLogContent` → 回退 `log.content`），否则这类日志的紧凑行会几乎为空。
- 视图切换入口放在列表顶部而不是筛选工具栏：工具栏在移动端已有「敏感信息 / 筛选 / 搜索 / 导出 / 列设置」五个控件，再塞一个会挤。代价是占约 40px 高度。

## 3.2 API 信息延迟测量

**原实现结论：延迟本来就是从用户浏览器发起的**，不是服务器测的。`features/dashboard/lib/api-info.ts` 的 `testUrlLatency` 用浏览器 `fetch(url, { method: 'HEAD', mode: 'no-cors', cache: 'no-cache' })` + `performance.now()` 计时；后端没有任何延迟探测接口，`/api/status` 只提供 `api_info` 配置项，`api-info-panel.tsx` 里的 `pingStatus` 是组件本地 state（刷新即丢）。

原实现的问题（本次修掉）
1. 没有超时：目标静默丢包时面板会永远停在「Testing...」，按钮一直禁用。
2. 只测一次，且这一次包含 DNS + TCP + TLS 建连开销，数字偏高。
3. 所有失败都显示同一个 `N/A`，无法区分「浏览器不让测」和「真的连不上」。

改动文件
- `features/dashboard/lib/api-info.ts`：改为「1 次预热（丢弃）+ 3 次采样取中位数」，每次采样 4s `AbortController` 超时；预热失败直接判失败，不再空跑 3 次超时。新增 `isBlockedInsecureTarget`：页面是 https 而目标是 http 时在发请求前就判定为不可测（混合内容，浏览器必然拦截）。
- `features/dashboard/types.ts`：`PingStatus` 加可选 `reason?: 'mixed-content' | 'timeout' | 'unreachable'`（唯一改动，Pi-2 若同时在改此文件请注意合并）。
- `features/dashboard/components/overview/api-info-item.tsx`：失败徽标由固定 `N/A` 改为按原因显示：`Unable to measure (insecure target)` / `Timed out` / `Unable to measure`。
- `features/dashboard/lib/__tests__/api-info.test.ts`（新）：6 例覆盖丢弃预热并取中位数、单次采样失败仍出结果、全部失败、超时中止、https 页面拒绝测 http 目标、http 页面正常测量。

未改：`api-info-panel.tsx` 的状态机、概览卡片、图表、区间选择器（属 Pi-2）。

## 3.3 「未配置正常运行时间监控」核实（未改代码）

结论：**是没配置，不是代码缺陷**。链路如下。
- `features/dashboard/components/overview/uptime-panel.tsx`：`empty={!groups.length}`，`groups` 取自 `GET /api/uptime/status` 的 `data`；空数组就显示 `No uptime monitoring configured`。
- `controller/uptime_kuma.go`：`GetUptimeKumaStatus` 在 `console_setting.GetUptimeKumaGroups()` 为空时直接返回 `[]`，即选项 `console_setting.uptime_kuma_groups` 为空串或空数组。
- `hooks/use-status-data.ts`：整个面板还要 `/api/status` 返回 `uptime_kuma_enabled !== false`（默认 true）才渲染；关掉开关是「面板消失」，不是「显示空提示」。

配置步骤（管理后台）
1. 系统设置 → 控制台（Console）→「Uptime Kuma」分区。
2. 打开该分区的 `Enabled` 开关。
3. `Add Group` 填三项：`Uptime Kuma URL`（如 `https://status.example.com`，不带结尾 `/` 也行）、`Status Page Slug`（Uptime Kuma 状态页的标识，例如 `default`）、`Category Name`（显示在面板里的分组标题）。
4. `Save Settings`。

后端实际请求的是 `<url>/api/status-page/<slug>` 与 `<url>/api/status-page/heartbeat/<slug>`，由网关服务器发起（单请求 10s、整体 30s 超时），因此要求状态页是公开的，且**网关能访问到 Uptime Kuma**（用户浏览器能访问不等于网关能访问）。监控项的在线率取自 `uptimeList[<id>_24]`，状态取自最近一次心跳。

两个注意点（未改，仅记录）
- 只要 `uptime_kuma_groups` 里有条目，即使 url/slug 填错或不可达，后端仍会返回该分组（`monitors` 为空）。此时提示消失、面板里只有一个 `0` 的分组标题——「提示消失」不等于配置成功。
- `uptime-panel.tsx` 的 `.catch(() => setGroups([]))` 把「接口请求失败」显示成「未配置」，属于信息误导；修它要动概览面板（Pi-2 范围），本包未改。

## 验证（实际命令与结果）
- `bun run test src/features/usage-logs`：27 文件 / 458 用例全过。
- `bun run test src/features/dashboard/lib/__tests__/api-info.test.ts`：6 用例全过。
- 全量 `bun run test`：216 文件 / 2564 用例全过（在我改动完成后、Pi-2 改动中间态之前跑的）。
- `oxlint -c .oxlintrc.json` 与 `oxfmt --check` 对本次改动的 9 个文件：无输出 / All matched files use the correct format。
- `bun check -b`：**当前仍有错误，但全部在别人的文件里**——`channels/components/drawers/channel-mutate-drawer.tsx`（Pi-1）、`dashboard/components/flow/flow-charts.tsx`、`dashboard/components/models/log-stat-cards.tsx`、`dashboard/components/models/models-filter-dialog.tsx`、`dashboard/lib/charts.ts`（Pi-2）。我的 9 个文件无类型错误（中途 Pi-2 修完后曾整体通过一次）。
- `dashboard/lib/__tests__/charts.test.ts` 当时 9 例失败，是 Pi-2 改 `lib/charts.ts` 的中间态，与本包无关。
- 未跑完整生产构建（`rsbuild build`）——按通用规则 3 不并发跑；未启动 dev server。
- 后端与数据库：本包只改前端，无 Go 代码、无表结构/迁移/ORM/SQL 变化，故不涉及 SQLite / MySQL / PostgreSQL 三库验证。

## 未完成 / 未验证
1. 没有真机或浏览器视觉走查：紧凑行在 320px 级窄屏的实际换行、展开动画、暗色主题下的对比度都未用眼睛确认，只做了 DOM 级断言。
2. `formatTokens` 在超大数值（≥1e9）下的显示沿用现有实现，未单独验证。
3. API 延迟只做了逻辑单测：没有在真实浏览器 + 真实上游上跑过。`no-cors` 的 HEAD 若被上游 CDN/WAF 拒绝，会表现为「无法测量」，这是浏览器侧测量无法绕过的限制。
4. 移动端视图偏好只存在 localStorage，未做跨设备同步（计划未要求）。

## 需要用户决定
1. 视图切换入口位置：现在是列表顶部右侧的分段控件（约 40px 高度）。是否接受，或改到筛选工具栏里？
2. 紧凑行第二行超长时现在是单行截断；是否允许折成两行（信息更全，但行高不再一致）。
3. 3.3 里「接口失败被显示成未配置」是否要修——需要协调 Pi-2 的概览面板范围。
4. i18n：本包新增 4 个 key（`Compact view`、`Unable to measure`、`Unable to measure (insecure target)`、`Timed out`），译文已写入 `$T/i18n/PI-3.json`（zh / zh-TW / fr / ru / ja / vi），需并入 `web/src/i18n/locales/*.json`。另注：`Card view`、`View mode`、`Table view` 这三个已存在于代码的 key 目前**七个语言文件里都没有**（既有缺口，非本次引入），一并处理更省事。
