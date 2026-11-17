# CX-B：R9 邀请码注册 / R3 登录二次验证可选

2026-11-11：实现与相关回归已落地；仍有跨所有权集成、数据库验证和安全通知缺口，整体验收未闭合。未提交或推送。

## 实现与文件
- R9：新增邀请码管理、权限、迁移、管理页、导航；支持批量生成、名称/有效期、搜索、状态、启停、删除/清理、使用者/时间、复制与 TXT 导出。
- 邀请码由 crypto/rand.Text 生成；条件更新、lockForUpdate 与建号/绑定同事务，并发仅成功一次，建号失败不消耗；管理审计不记录码值。
- 密码、内置/自定义 OAuth、微信服务端执行四种注册模式；已有账号登录无需邀请码。OAuth 码存服务端 auth flow；微信新增 POST，避免码进入 URL。
- R3：个人偏好默认关，存 Option 的 LoginVerificationUser:<id>，每次登录查主库；全局 LoginVerificationRequiredEnabled 默认关，只影响 login scope。
- 开启需已录入 TOTP/Passkey；关闭需一次性、过期校验、scope/session 绑定的 auth.login_verification.disable proof。变更递增 auth version、轮转当前会话；PAT 不可修改。
- 个人卡片复用安全验证流程，因子状态随认证轮转刷新；全局策略、个人偏好均有界面。新全局选项先持久化再生效；个人 Option 对通用设置接口隐藏且禁止修改。
- 新增后端：model/invitation.go、controller/invitation.go、controller/invitation_test.go。
- 修改后端：controller/{user,oauth,wechat,login_verification,auth_flow_test,security_enrollment_test}.go；model/login_verification.go；service/{login_verification,security_verification}.go。
- 共享最小插入：common/constants.go、model/{option,main}.go、controller/{misc,option}.go、router/api-router.go；保留 PI-3 的 OAuth 头像同步。
- 前端：features/auth 下 api/types、OAuth 组件/hook、secure-verification/types、sign-in、sign-up、login-preferences 及相关测试；features/invitation-codes/** 及其路由。
- 前端接入：features/security/{index.tsx,__tests__/page.test.tsx}；system-settings/auth/{basic-auth-section,index,section-registry}.tsx、system-settings/types.ts；hooks/use-sidebar-{data,config}.ts、routeTree.gen.ts。
- 复用 DataTablePage、Dialog、ConfirmDialog、CopyButton、MaskedValueDisplay、ErrorState；现有兑换码导出器绑定 quota，邀请码另实现业务 TXT 导出，未修改兑换码文件。
- 翻译仅写 i18n/CX-B.json：66 个英文 key，zh/zh-TW/fr/ru/ja/vi 六语完整；未直接修改 locale 文件。

## 验证命令与实际结果
- Go 可执行文件：C:/Users/lichengyu/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.10.windows-amd64/bin/go.exe；下列 Go 命令在仓库根目录执行。
- Bun 可执行文件：D:/AppData/Local/Temp/new-api-cx-a-tools/bun-windows-x64/bun.exe；下列 Bun 命令在 web/ 执行。
- `go build ./controller ./service ./model ./router`、`go vet ./controller ./service ./model ./router`：通过，当前工作树直接验证，无 overlay。
- `go test ./controller ./router -run 'TestInvitation|TestSecurityLogin|TestSecurityEnrollment|TestOAuth|TestGenerateOAuth|TestRegistrationAndLoginPolicies|TestAccessTokenCatalogCoversEveryPermission|TestAccessTokenVerificationScopesAreAllListed' -count=1`：通过（controller 45.762s，router 0.130s），无 overlay。
- `go test ./service ./model -run 'TestSecurityProof|TestAuthFlow|TestExternalAuthAssertion|TestConsumeAuthFlow|TestSecurityFactorMutations|TestUserAuthVersionInvalidates' -count=1`：通过。
- 后端覆盖四种注册模式、并发核销/回滚、失效码、OAuth state 防替换/篡改、自定义 OAuth/微信/已有用户、管理校验及审计不含码值；个人/全局策略、无因子拒绝、proof 缺失/错 scope/过期/跨 session/重放、会话轮转、既有高危验证与 Passkey 第一因子。
- `bun run test src/features/auth src/features/invitation-codes src/features/security`：21 个测试文件、164 个测试通过；`bun x --package typescript tsc -b`：本轮通过。
- 23 个相关前端文件 oxlint：0 errors、0 warnings；格式复核无漂移；CX-B 改动的 `git diff --check` 通过；翻译复核无缺漏。
- `go test ./router -run '^TestAccessTokenRouteRulesCoverEveryDashboardRoute$' -count=1`：失败，缺 8 条登记（CX-B 3 条、CX-A group/preview 1 条、PI-4 rate_limit 4 条），详见下文。
- 真实 SQLite 3.50.4：邀请码新表、已有用户旁迁移、邀请码数据保留及重复迁移回归通过（TestInvitation*）；MySQL/PostgreSQL、最新版发布库升级、完整启动两次均未验证。
- 数据库阻塞：本机无 Docker/Podman、MySQL/PostgreSQL 可执行文件或运行实例，亦无 DSN；不得据此宣称三库兼容。全量构建/统一 i18n 检查留给 INT。

## 口径、决定与安全边界
- 沿用 PRD [假设]：开放注册和邀请注册同时开时，“任何方式都必须带有效邀请码”；邀请独开时密码注册拒绝，注册页只显示邀请码与 OAuth/SSO 入口。
- 管理员主动建号保留既有权限/高危验证，不按自助注册入口处理；Telegram 保持仅已绑定账户登录。legacy GitHub binding migration 仍强制因子验证。
- 不扩展 user.setting 结构：个人偏好独立存 Option，避免普通资料保存覆盖未知字段；邀请码为管理复制/导出保留明文，仅管理员且有对应权限可读，不进入审计日志。
- 已阅读 OWASP Authentication、Session Management、MFA、OAuth Cheat Sheets；核对 ASVS 5.0.0（v5.0.0_release），相关条目 6.3.1、6.3.3、6.3.4、6.3.7、6.3.8、6.4.1、6.5.1–6.5.3。
- 已验证服务端强制策略、现有限流接入、随机单用邀请、服务端 OAuth state、proof 绑定/过期/单用、auth-version 与会话轮转。按需求默认 MFA 可选，不宣称满足 ASVS 6.3.3 的全面 MFA 要求。
- 遗留缺口：偏好变更未接邮件通知（6.3.7）；既有 TOTP 同窗口防重放、注册账户枚举响应未全面改造。不能宣称完整 ASVS 合规或安全验收完成。

## 需要他人处理（INT）
- 在 middleware/access_token_routes.go 为 GET、PUT /api/user/login-verification 登记 accessTokenSessionRule；PUT handler 已拒绝 PAT，新增禁用偏好验证 scope 已登记为禁止 PAT。
- 在 router/access_token_scope_test.go 的匿名豁免列表加入 POST /api/oauth/wechat；另合并 CX-A 的 POST /api/group/preview 及 PI-4 的 GET/PUT/DELETE /api/user/:id/rate_limit、POST /api/user/rate_limit/clear_group 登记。
- 邀请码路由已用 handlePermissionRoute 自动登记 PAT scope，权限目录与验证 scope 目录测试已通过，无需另补邀请码登记。
- 在 PI-2 所有的 web/src/features/usage-logs/lib/format.ts 的 AUDIT_TEMPLATES 加以下映射（后端模板及六语文案均已准备）：
  - invitation.create → Created invitation codes；invitation.update → Updated an invitation code。
  - invitation.delete → Deleted an invitation code；invitation.delete_batch → Batch deleted invitation codes。
  - invitation.delete_invalid → Deleted invalid invitation codes；user.login_verification_update → Login verification preference update。
- 合并 i18n/CX-B.json 到七个 locale 并跑统一 sync/full build；补个人偏好变更邮件通知及后端邮件翻译；完成三数据库、升级库与重复启动验证后再关闭对应验收项。
