# INT 集成总报告（2026-11-11）
已完成代码集成、翻译合并和本机验证；完整上线验收仍受真实数据库、外部联调及既有检查失败限制。未 commit / push，未改兑换码既有改动、受保护标识或 azurekiln-oauth2。
## 集成修改
- `middleware/access_token_routes.go`、`router/access_token_scope_test.go`：补齐 8 处登记；登录验证偏好仅浏览器会话，用户限流使用 user:read/write，分组预览使用 group:read，微信 POST 作为匿名入口。
- `web/src/features/usage-logs/lib/format.ts`：补全 5 个邀请码动作和登录验证偏好动作的审计翻译映射。
- `controller/login_verification.go`、`i18n/keys.go`、`i18n/locales/{en,zh-CN,zh-TW}.yaml`：新增偏好变更安全邮件，沿用已保存语言、现有邮件服务；失败进入审计与 notification_warning，不撤销已提交变更。
- `controller/auth_flow_test.go`：补启用/禁用偏好通知、投递失败后仍保存、头像不可由设置接口覆盖且保存后仍返回头像的回归；无真实外发邮件，HTTP 边界可控替代。
- `controller/access_token_audit_test.go`：关闭 SQLite 夹具及重复 InitDB 打开的连接，修复 Windows 清理失败；`controller/relay_task_plugin_test.go`：分别断言已输出停止和新请求本地拒绝，消除错误复用已输出上下文。
- `web/src/features/system-settings/request-limits/{rate-limit-section,rate-limit-visual-editor}.tsx` 及其 `__tests__/group-rate-limit-editor.test.tsx`：清用户覆盖改为页面保存成功后执行；保存失败不清除，删除草稿分组不再清除，补回归。两次服务端写仍非原子事务，清除失败保留待重试项。
- `model/log.go`、`model/log_filter_test.go`：修复 PostgreSQL 缓存统计对 JSON 键后空白的误过滤，测试夹具加入缓存写入和空白，SQLite 结果验证通过；PostgreSQL 实例仍未验证。
- 七个 `web/src/i18n/locales/*.json`：脚本合并六份片段及新增 `i18n/INT.json` 的 `unlimited`、`Save rate limit`；共 108 个批次 key，英文等于 key，冲突/插值错误/新增源码漏 key 均为 0，临时写入脚本已删除。
## 实际检查
- Go 1.26.10：`go build -p 2 ./...`、`go vet -p 2 ./...` 最新均通过；初次 vet 因 C 盘满失败，复制依赖缓存到 `%TEMP%/new-api-int-modcache` 并仅对当前命令设置 GOMODCACHE 后通过。
- `go test -p 2 ./controller ./service ./model ./relay ./router ./setting/... ./oauth/... ./i18n/... -count=1` 全部通过（controller 206.491s）；最新统计修复另跑 `go test -p 2 ./model -run 'TestLogFilter|TestSumUsedQuota|TestRequestPolicyDatabaseMatrix|TestGroupLevels' -count=1 -v` 通过，外部库子测试跳过。
- `go test -p 2 ./middleware -run 'TestModelRateLimit|TestAccessToken' -count=1` 通过；首次 middleware 全包仅既有 `TestAccessAndPanicLogsFollowLogRotation` 因 Windows 占用日志文件导致 TempDir 清理失败，未改其生产日志生命周期。
- `relaykit/` 内 `GOWORK=off go build ./...` 通过；tokenkit 无改动。所有本任务 Go 文件 gofmt 无输出，`git diff --check` 通过。
- web：Bun 1.4.4，`bun run typecheck`（1437 文件/2 项目）和 `bun run build` 最新通过；产物 67609.7 kB，gzip 20778.2 kB；未启动常驻服务。
- web：`node node_modules/vitest/vitest.mjs run --maxWorkers=2 src/features/auth src/features/invitation-codes src/features/security src/features/rankings src/features/usage-logs src/features/users src/features/system-settings/request-limits src/features/system-settings/request-policies src/features/system-settings/models src/features/profile src/components/__tests__ src/lib/__tests__` 最新 82 文件/925 测试通过；首次不限并发执行因内存不足退出。仅有 jsdom scrollTo/canvas 未实现提示。
- `bun run lint` 全量失败：151 个 error 位于本次未改动文件（含既有兑换码、通用组件、pricing/home 等）；本批 70 个修改/新增 TS(X) 文件定向 oxlint 通过，1 个既有 structuredClone warning。未扩大为全仓 lint 清理。
- `bun run i18n:sync`：七语言 missing/extras 全为 0；zh/ja/ru 的 3/4/5 个“未翻译”提示是既有技术名（Doubao、Responses WebSocket、SGLang 等），未修改。INT 前端修改用保留原版权头的 oxfmt 格式化。
## PRD 逐条验收
“通过”指本机确定性回归；涉及部署、外部提供方和数据库矩阵的未验证部分不由编译或模拟替代。R5–R7 未另编号，按原需求整条列出。
| 条目 | 状态 | 证据或限制 |
|---|---|---|
| AC1.1 | 通过 | 普通/任务/WS 共享失败跟踪，换渠道成功与下一请求绑定回归；真实提供方 WS 未联调。 |
| AC1.2 | 通过 | strict 阈值 0/1/2、成功清零、每次尝试只计一次；不绕过输出/不可重试/预算条件。 |
| AC1.3 | 通过 | 缓存开关、同档未失败优先、降档、跨组避让、耗尽后允许重复均有确定性回归。 |
| AC1.4 | 通过 | 配置保存回显、非法阈值拒绝、旧规则兼容与“全部继承”测试通过。 |
| AC2.1 | 通过 | 下拉使用现有分组；消失分组仍显示与可删除，前端测试通过。 |
| AC2.2 | 通过 | 服务端用户覆盖优先，同组其他用户不变；管理 API/中间件/抽屉测试通过。 |
| AC2.3 | 通过 | 只清目标分组；INT 补页面先保存后清除、保存失败不清除。 |
| AC2.4 | 未验证 | 内存和 miniredis 两分支测试通过；真实 Redis / 多节点限流未验证。 |
| AC3.1 | 通过 | 密码与 OAuth 默认/个人关闭时跳过二次验证的服务端回归通过。 |
| AC3.2 | 通过 | 个人启用/全站强制及 Passkey 第一因子既有流程测试通过。 |
| AC3.3 | 通过 | controller/service 高危操作、注册因子、proof 绑定及认证版本回归通过；不表示全面 ASVS 合规。 |
| AC3.4 | 通过 | 缺失/错 scope/过期/跨会话/重放拒绝，成功后认证轮转，PAT 被拒绝。 |
| AC4.1 | 未验证 | userinfo→存储→登录/self DTO→用户菜单/资料页链路及受控边界测试通过；未连接真实网关登录。 |
| AC4.2 | 通过 | 空/非法 URL、加载失败回退、https/长度限制、no-referrer 测试；非法 provider 更新保留旧合法头像。 |
| AC4.3 | 通过 | 自定义 OAuth 与 OIDC 的 picture 提取测试通过；ID Token-only picture 未支持。 |
| R5 | 通过 | 三处共用同一格式；读写成对、全零隐藏、窄屏换行契约测试；未做真实浏览器像素验收。 |
| R6 | 未验证 | 四卡与无数据显示通过，SQLite 输入语义含缓存读写汇总通过；MySQL/PG/CH 聚合及大表代价未实测。 |
| R7 | 未验证 | 模糊默认/URL恢复/列表统计共用/字面通配转义 SQLite 回归通过；MySQL/PG/CH 未验证。exact 保留历史 model/username 的 % 通配语义。 |
| AC8.1 | 通过 | 五级可见范围、手工授权并集、等级预览与前端保存测试通过；显式授予高组需先撤销才满足严格阶梯。 |
| AC8.2 | 通过 | 未设等级继续现有手工规则；signed int32 校验、非法值不覆盖旧配置。 |
| AC8.3 | 通过 | 唯一 resolver 供令牌/TokenAuth/分发/定价/Auto 共用；伪造或过期 Auto 分组过滤回归通过。 |
| AC9.1 | 通过 | 四组合密码/OAuth/微信服务端约束与注册页测试；已有账号免邀请码。 |
| AC9.2 | 未验证 | SQLite 并发单次核销与建号失败回滚通过；MySQL/PG 真实并发事务未验证。 |
| AC9.3 | 通过 | 邀请独开时密码注册服务端拒绝，不依赖前端隐藏。 |
| AC9.4 | 通过 | 批量、搜索、状态、启停/删除/清理、有效期、使用者、复制/导出及权限审计回归通过。 |
## 原样保留的 [假设]
- R2：用户级覆盖存成一个系统选项（用户 ID → `[总次数, 成功次数]`），不改表结构；实现者确认有更合适的落点可以换，但要说明理由。
- R4：尽量不改表结构（例如放进用户 `setting` JSON）；确实需要加列时按 `AGENTS.md` 的数据库验证要求办。
- R7：默认模糊，切到「精确」后恢复现在的行为；切换状态进 URL 参数；对文本类筛选（模型名、令牌名、用户名、分组、请求 ID 等）生效，数字 / 枚举类不变；统计卡片与列表用同一套筛选语义；`LIKE` 要转义通配符；ClickHouse 日志库分支同样支持。
- R9：任何方式都必须带有效邀请码（开放注册与邀请注册同时开启时）。以上四项均按既定假设实施，未新增待确认的产品选择。
## 数据库、安全与交付限制
- 真实 SQLite 3.50.4：邀请码新表/既有用户迁移/保留数据/重复迁移、配置持久化与回滚、日志过滤聚合、认证事务均通过；AuditDatabaseMatrix 的 fresh/代表性旧 schema 经 InitDB+InitLogDB 两次通过。未导入“最新发行版整库”升级；MySQL/PG/CH 无工具/实例/DSN，相关子测试明确跳过，不能声明跨库验收完成。
- 已核对 OWASP ASVS 5.0.0（稳定标签 v5.0.0_release；latest 是 Bleeding Edge，未当稳定版）V6 6.3.3/6.3.7/6.4.1/6.5.1–6.5.3、V7，以及 Authentication/Session Management/MFA/OAuth2 Cheat Sheets；补齐通知，proof 过期/单用/绑定、会话轮转、随机邀请码均有回归。更正 PI-3 的章节表述：ASVS 5.0 的认证/会话为 V6/V7。
- 认证安全验收仍未闭合：默认 MFA 可选按 R3 要求保留（高危操作继续验证、现有限流与会话失效作补偿），不宣称满足 6.3.3 的全面 MFA；既有 TOTP 同窗口防重放与账户枚举响应未全面改造；无邮箱账户无邮件送达渠道，投递失败仅告警与审计。
- 上线前仍需真实 MySQL/PG/CH、Redis 多节点、网关/WS 联调及浏览器验收，并处理全仓 lint / Windows 日志夹具基线；用户可据上述假设选择调整产品口径。本轮无需等待决定，交付以本报告标记的实际通过/未验证范围为准。
- 安全参考：[ASVS 5.0.0](https://github.com/OWASP/ASVS/releases/tag/v5.0.0_release)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)、[MFA](https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html)、[OAuth2](https://cheatsheetseries.owasp.org/cheatsheets/OAuth2_Cheat_Sheet.html)。
