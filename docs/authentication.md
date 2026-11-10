# 用户鉴权与登录会话

面板鉴权采用短期 Access Token、HttpOnly Refresh Cookie 与服务端登录会话控制面的组合。面板请求不再依赖 Gin session，也不再要求 `New-Api-User` 请求头。

## 鉴权模型

- Access Token 是有效期 15 分钟的 JWT，只保存在浏览器内存中，通过 `Authorization: Bearer <token>` 发送。
- Refresh Token 是随机不透明值，有效期最长 30 天。浏览器只通过 `HttpOnly`、`SameSite=Strict` Cookie 持有它；服务端仅保存 HMAC 摘要，并在每次刷新时轮换。
- `new_api_has_session` 是 Refresh Cookie 的会话提示，值恒为 `1`，`Path=/`、非 `HttpOnly`，与 Refresh Cookie 同时写入、同时清除、同一过期时间。它只声明"曾签发过 Refresh Cookie"，不含任何凭据，也不参与任何鉴权判定；伪造它唯一的效果是自费一次注定失败的 refresh。它存在的原因是 Refresh Cookie 被 `HttpOnly` 和 `Path=/api/user/auth` 双重限制，`/` 上的页面无法判断自己是否匿名，否则每次冷启动都要发一次注定 401 的 refresh，而该请求还会占用按 IP 计数的 `CriticalRateLimit` 配额。
- `user_sessions` 是登录会话控制面，记录设备、IP、登录方式、最后活跃时间、到期时间和撤销状态。数据库中的 Session 状态是最终权威；撤销传播速度取决于下文所述的 Redis 拓扑。
- 用户的密码、状态、角色或安全因子发生安全相关变化时，`auth_version` 会递增并使旧登录会话失效。订阅带来的分组升降级只刷新授权缓存，不会退出任何登录设备。
- Redis 缓存保存用户鉴权快照和登录会话快照。版本栅栏和撤销 tombstone 防止旧缓存重新授权；Session 快照使用跟随 `SYNC_FREQUENCY` 的短 TTL，缓存未命中或未启用 Redis 时回退到数据库校验。

`SESSION_SECRET` 用于派生 Access Token、Security Proof、Refresh Token 摘要和 AuthFlow 摘要的不同用途密钥。生产环境及多节点部署必须在所有节点配置相同的高强度随机值；更换该值会使现有登录、临时鉴权流程和 Security Proof 全部失效。

## 多节点 Redis 拓扑

多节点部署必须共用同一主数据库。登录 Session、账户级活跃 Session 上限和签发窗口计数都以数据库为权威，因此这些限制在应用节点间全局生效。Redis 中的 Session Hash（包含 `revoking`/`revoked` tombstone）只是缓存，其 TTL 为 Session 剩余寿命与有效 `SYNC_FREQUENCY` 中的较小值；`SYNC_FREQUENCY` 默认及非法值回退均为 `60` 秒。读取缓存不会续期，过期后会按 SID 回源数据库。延迟完成的 active 缓存回写只能使用其数据库观察窗口尚未消耗的 TTL，不能在撤销 tombstone 到期后重新启动一个完整缓存周期。

| Redis 部署方式 | Session 状态传播 | 限流语义 |
| --- | --- | --- |
| 所有节点共享 Redis | 正常撤销和版本发布通过同一缓存即时传播 | Redis 限流额度在所有节点间共享 |
| 每个节点使用独立 Redis | 最迟在该节点 Session 缓存 TTL 到期后回源收敛，即不超过有效 `SYNC_FREQUENCY`；版本轮换期间，新 Token 在持有旧缓存的节点上可能短暂返回 401 | 每个节点独立计数，集群总额度最坏约为单节点阈值乘以节点数 |
| 不使用 Redis | 每次 Session 校验直接读取数据库 | 使用各节点的内存限流器，额度同样按节点独立 |

`SYNC_FREQUENCY` 越大，独立 Redis 部署的陈旧窗口越长；值越小，每个活跃 SID 在每个节点上回源数据库的频率越高。默认配置下，持续活跃的 Session 每个节点最多约每 60 秒增加一次数据库主键点查。共享 Redis 时，撤销 tombstone 和版本发布仍保持即时传播。

所有节点必须使用相同的 `SESSION_SECRET`。当多个节点连接同一个 Redis 时，还必须使用相同的 `CRYPTO_SECRET`，否则节点生成的缓存键摘要不一致，无法正确共享缓存。上述保证只覆盖登录 Session 鉴权的有界陈旧语义；限流额度及其他 Redis 缓存仍会受到 Redis 拓扑影响，不能据此认为整个控制面与拓扑无关。

## 浏览器接口

登录成功后，密码登录、2FA、Passkey、OAuth、WeChat 和 Telegram 登录均返回统一数据：

```json
{
  "success": true,
  "data": {
    "access_token": "...",
    "token_type": "Bearer",
    "access_expires_at": 1730000000,
    "user": {},
    "session": {
      "sid": "...",
      "current": true,
      "login_method": "password",
      "ip": "...",
      "user_agent": "...",
      "created_at": 1730000000,
      "last_active_at": 1730000000,
      "expires_at": 1732592000
    }
  }
}
```

会话相关接口：

| 接口 | 鉴权 | 用途 |
| --- | --- | --- |
| `POST /api/user/auth/refresh` | Refresh Cookie；Secure 模式附加 Origin 校验 | 轮换 Refresh Token 并签发新的 Access Token |
| `POST /api/user/auth/logout` | Refresh Cookie；Secure 模式附加 Origin 校验，可同时携带 Bearer | 撤销当前登录会话并清除 Cookie |
| `GET /api/user/sessions` | Bearer | 查看当前鉴权版本的有效登录会话，当前会话优先，最多 100 条 |
| `DELETE /api/user/sessions/:sid` | Bearer | 撤销指定登录会话，包括当前会话 |
| `POST /api/user/sessions/revoke-others` | Bearer | 保留当前会话并撤销其他会话 |

客户端内存中已有会话时，应在 refresh/logout 请求中发送 `X-Auth-Session: <sid>`。Refresh Cookie 与该 SID 不一致时，两个端点都返回 `409 AUTH_SESSION_MISMATCH`，且不会轮换、撤销或清除任何会话；客户端先通过 refresh 清除本标签页的旧 SID、恢复 Cookie 当前对应的会话，再重试 logout。冷启动尚无内存会话时可以省略该请求头。

并发使用同一个 Refresh Token 时，服务端通过确定性轮换恢复同一个后继 Token，多个浏览器标签页不会因丢失“胜者”响应而被迫退出。最近一代 Refresh Token 在短暂容错窗口结束后再次出现会撤销对应会话；无法识别的更早代或随机 Token 只会被拒绝，不会允许攻击者凭猜测踢掉会话。

前端使用 Web Locks 串行化同一浏览器配置文件中的刷新，并通过 BroadcastChannel（不支持时回退到 `storage` 事件）仅同步会话标识和登录/退出事件；Access Token 与 Refresh Token 都不会通过跨标签页消息传递或持久化到 Web Storage。

前端将冷启动状态与登录状态分开管理。网络或服务端临时故障允许后续导航重试 refresh；服务端确认 Refresh Cookie 无效时才进入已完成的匿名状态。内存 SID 与 Cookie SID 不一致时，客户端清除旧内存身份并在不携带旧 SID 的情况下重试一次。

公开页面的冷启动会先读 `new_api_has_session`：提示不存在且内存中没有任何身份时跳过 refresh，直接按匿名渲染，且**不**把这次跳过记为已完成的匿名判定——跳过只是延后，不是服务端结论。会依据鉴权结果做跳转的位置（受保护路由与登录页）不看提示，内存为空时一律回源。因此提示缺失但 Refresh Cookie 有效的用户（该 Cookie 上线前建立的会话，或只清理了 `/` 站点数据的浏览器）会在公开页显示为匿名，并在进入上述任一位置时自动恢复登录态，不需要重新输入密码。提示因服务端撤销而过期时，那次 refresh 返回 401 并在同一响应里清除提示，浪费的请求只发生一次。

## Session 签发限额与保留策略

服务端在所有登录方式的统一 Session 签发出口执行两级账户限制：

- `USER_SESSION_ACTIVE_LIMIT`（默认 `50`）：单用户未过期且状态为 active 的 Session 上限。达到上限时新登录返回 `409 AUTH_SESSION_LIMIT`。
- `USER_SESSION_ISSUANCE_LIMIT`（默认 `100`）和 `USER_SESSION_ISSUANCE_WINDOW_SECONDS`（默认 `86400`）：统计窗口内该用户创建的所有 Session，包含已撤销和旧鉴权版本的记录。达到上限时返回 `429 AUTH_SESSION_ISSUANCE_LIMIT`。
- 这两次计数与插入不加跨数据库锁；极端并发登录可能出现少量超额，但计数失败会拒绝签发，不会降级放行。

升级时已经超过活跃上限的账户不会被自动下线或挤掉旧会话；限制只作用于后续的新 Session 签发。

`USER_SESSION_REVOKED_RETENTION_DAYS`（默认 `7`）控制 revoked 行的审计保留期。签发计数依赖窗口内的行仍存在，因此签发窗口不得超过 revoked 保留期。如果配置超出，启动时会记录告警并将实际窗口钳制到保留期，避免提前删除 revoked 行导致限流计数被低估。

定时清理即使发现 `expires_at` 已过期，也不会删除 `created_at` 仍落在实际签发窗口内的行；尚未达到 revoked 保留期的撤销记录同样会继续保留。这样在扩大配置窗口时，过期清理不会静默削弱签发计数或审计保留。

活跃数量会计入状态仍为 active 但 `user_auth_version` 已过期的异常残留行，而设备列表只展示当前鉴权版本。因此遇到 `AUTH_SESSION_LIMIT` 时，应优先在仍已登录的设备上执行“撤销其他会话”，该操作会同时清理不可见的旧版本 active 行；没有可用设备时可使用密码重置撤销所有会话。密码重置不会清空签发窗口计数。

仅 master 节点每小时分批删除过期 Session 和超过保留期的 revoked Session。`USER_SESSION_HOURLY_ALERT_THRESHOLD`（默认 `5000`）只在最近一小时全局签发量异常时记录告警，不会形成可被滥用的全站登录拒绝开关。

## Refresh/Logout 的 Origin 校验

refresh/logout 的 Origin 防护与 Refresh Cookie 的 Secure 模式绑定：

- 未配置 `SESSION_COOKIE_SECURE` 或显式设为 `false` 时，Refresh Cookie 可用于本地 HTTP，refresh/logout 的 OriginGuard 关闭，并且不得配置 `SESSION_COOKIE_TRUSTED_URL`。这使 `http://localhost` 上不同端口的 Rsbuild/Vite 开发代理可以正常转发请求。该模式仅用于可信的本地开发环境，不应暴露到公网。
- `SESSION_COOKIE_SECURE=true` 时，Refresh Cookie 仅通过 HTTPS 发送，同时启用严格 OriginGuard。`POST /api/user/auth/refresh` 和 `POST /api/user/auth/logout` 会校验浏览器的 `Origin`；缺少 `Origin` 时只接受合法的单一 `Referer` 作为回退。允许来源包括请求自身的精确 Origin，以及 `SESSION_COOKIE_TRUSTED_URL` 中配置的精确 Origin。

Secure 模式的 Origin 校验不信任客户端直接发送的 `X-Forwarded-Proto`。TLS 在反向代理终止时，应将面板的公开 HTTPS Origin 明确写入 `SESSION_COOKIE_TRUSTED_URL`。

`SESSION_COOKIE_TRUSTED_URL` 现在具有明确的新语义：它是 refresh/logout Cookie 端点的可信 Origin 列表，不是 CORS 白名单。配置规则如下：

- 仅在 `SESSION_COOKIE_SECURE=true` 时配置；多个值用英文逗号分隔。
- 每项必须是精确的 HTTPS Origin，例如 `https://panel.example.com` 或 `https://panel.example.com:8443`。
- 不接受通配符、路径、查询参数、用户信息或域名后缀匹配。
- 不会修改 relay、旧 billing dashboard、`/api/usage/token` 或 `/api/log/token` 的 CORS 行为。浏览器使用 `sk-` key 直连 relay 的场景保持不变。

本地 HTTP 开发示例（OriginGuard 关闭）：

```env
SESSION_SECRET=<local-random-value>
SESSION_COOKIE_SECURE=false
# SESSION_COOKIE_TRUSTED_URL 不得设置
```

生产 HTTPS 示例（OriginGuard 开启）：

```env
SESSION_SECRET=<high-entropy-random-value>
SESSION_COOKIE_SECURE=true
SESSION_COOKIE_TRUSTED_URL=https://panel.example.com,https://admin.example.com
```

该开关只控制面板 Refresh Cookie 和 refresh/logout 的 OriginGuard，不会修改 relay、旧 billing dashboard、`/api/usage/token` 或 `/api/log/token` 的 CORS 行为。

## 多域名 OAuth 登录

自定义 OAuth 提供商、内置 OIDC 和 Discord 支持从多个站点域名分别发起授权并返回原域名。后台的“服务器地址”作为默认允许的 Origin；超级管理员在“系统设置 → 身份验证 → OAuth 登录域名”（`/system-settings/auth/oauth-domains`）中添加额外站点，每行一个完整网站地址，例如：

```text
https://panel.example.com
https://panel.example.net
```

域名列表保存到共享数据库的 `oauth.allowed_origins` 选项，当前节点立即生效；其他节点按 `SYNC_FREQUENCY` 自动读取，默认每 60 秒同步。保存失败不会应用新列表；清空后仅保留默认服务器地址。无需设置 `OAUTH_ALLOWED_ORIGINS` 环境变量。多个节点使用同一主数据库、相同的 `SESSION_SECRET`。每个域名都需要将前端页面及 `/api/` 转发到这些节点，并配置有效的 HTTPS 证书。

HTTPS 部署仍需一次性启用 `SESSION_COOKIE_SECURE=true`，并在 `SESSION_COOKIE_TRUSTED_URL` 中至少配置主站 HTTPS 地址；Docker Compose 应将这些变量传入容器。面板中的域名列表也参与 HTTPS refresh/logout 来源校验，后续添加域名无需再次修改这些环境变量。该列表不改变 relay CORS。

SSO 客户端也必须逐一登记完整回调地址。例如自定义提供商的标识（slug）为 `my-sso`，则登记：

```text
https://panel.example.com/oauth/my-sso
https://panel.example.net/oauth/my-sso
```

内置 OIDC 的回调路径是 `/oauth/oidc`，Discord 是 `/oauth/discord`。SSO 仍须精确校验回调 URL，授权和换 token 请求中的 `redirect_uri` 必须相同。该功能不会改变 GitHub、LinuxDO 或 Telegram 的固定回调配置；如果这些平台作为自建 SSO 的上游，它们仍回调自建 SSO，由自建 SSO 返回对应的 new-api 域名。

服务端在 `POST /api/oauth/state` 时校验浏览器 `Origin`，将该 Origin 下的 `/oauth/<provider>` 保存到一次性 AuthFlow，换 token 时只使用保存的回调地址。允许列表精确匹配协议、主机和有效端口，不支持通配符、域名后缀、路径、用户信息、查询参数或片段。公网只允许 HTTPS；本地开发可显式配置 `http://localhost:<port>` 或回环 IP。缺少 `Origin` 的自建客户端只使用“服务器地址”，必须保存响应 Cookie 并在回调 API 请求时带回；不会根据 `Host`、转发头或 `redirect_uri` 请求参数选择额外域名。配置错误的来源在发起授权时返回 `403` 和 `Callback URL configuration error`。

新的登录 AuthFlow 同时绑定一个仅该域名可用的 HttpOnly、SameSite=Lax Cookie，HTTPS 使用 Secure 和 `__Host-` 前缀。每个流程使用独立 Cookie，可并行打开多个登录窗口；数据库只保存随机 Cookie 值的 SHA-256 摘要。回调必须通过浏览器绑定校验，且通过既有 state 的 provider、有效期、一次性消费校验后才能完成登录。账号绑定和二次验证继续绑定原登录会话和对应的安全凭据，不会因为增加域名而绕过这些检查。不同域名各自建立登录会话，浏览器 Cookie 不跨域共享。

升级前已创建、没有回调快照的 AuthFlow 在其剩余有效期内沿用原“服务器地址”。新流程的回调快照在十分钟有效期内保持不变；修改域名配置仅影响后续创建的流程。滚动升级期间旧节点不识别新快照，应先升级所有节点，再启用额外域名。部署必须使用包含此修改的自建镜像；直接拉取官方镜像不会包含本地修改。

安全设计参考 OWASP ASVS 5.0.0 的 10.1.2、10.2.1、10.2.2，以及 Authentication、Session Management、OAuth2 和 CSRF Prevention Cheat Sheets：本次验证范围为回调来源、事务与浏览器/会话绑定、过期和重放防护，不代表对所有 OAuth/OIDC 能力的完整 ASVS 合规认证。现有自定义 OAuth 和内置 OIDC 授权请求不发送 PKCE 参数；SSO 客户端若强制 PKCE，需另行实现对应支持，不能仅靠添加域名解决。

本次验证记录（2026-11-10，Windows，Go 1.26.10、Bun 1.4.4）：

- SQLite 3.50.4、MySQL 8.4.7、PostgreSQL 15.15 均在真实、隔离的本地数据库运行 `go test -p 1 -ldflags=-w ./controller -run '^TestOAuthDomainPanelSettings$|^TestOAuthMultiDomain' -count=1 -v` 并通过。分别设置 `TEST_SECURITY_DIALECT=sqlite/mysql/postgres`；MySQL 使用 `TEST_MYSQL_DSN=root@tcp(127.0.0.1:23316)/?parseTime=true`，PostgreSQL 使用 `TEST_POSTGRES_DSN=postgresql://oauth_test@127.0.0.1:25432/postgres?sslmode=disable`。覆盖重复保存、数据库重载、非法列表拒绝、写入失败保持原设置，以及登录、会话绑定、过期和重放；本次没有修改数据库结构。
- `go test -p 1 -ldflags=-w ./controller ./oauth ./middleware -run 'OAuth|SecurityEnrollment|SecurityAccount|SessionCookieOriginGuard' -count=1` 和 `go test -p 1 -ldflags=-w ./oauth -count=1` 通过。为适应本机可用内存，运行时设置 `GOMAXPROCS=1`、`GOGC=20`、`GOMEMLIMIT=768MiB`。
- 在 `web/` 运行 `bun run test src/features/system-settings/auth/__tests__/oauth-domains.test.tsx --maxWorkers=1`，5 个用例通过：保存、非法输入、失败保留编辑、清空、提交中禁用及重置。`bun check -b`、变更文件的 `oxlint -c .oxlintrc.json`、格式检查和 `git diff --check` 通过。复用 `SettingsSection`、`SettingsForm`、`SettingsPageFormActions`、共享表单控件及 `useUpdateOption`，没有新增通用控件。
- `bun run build` 成功；构建后的文件体积统计因本机内存不足产生警告，产物已生成。全库 lint 存在本次未改动文件的既有错误，变更文件 lint 无错误。七种语言已通过 `bun run i18n:sync` 同步。
- SSO token/userinfo 端点在回归测试中由本地 HTTP fixture 模拟，尚未针对用户真实 SSO、域名和反向代理进行部署验收。

## 可信代理与 IP 限流

Gin 默认会信任所有代理提供的客户端 IP 请求头。本项目改为兼顾常见反代拓扑和公网直连安全的三态配置：

- 未配置、空字符串或纯空白的 `TRUSTED_PROXIES` 默认信任 `127.0.0.0/8`、`::1`、`10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16` 和 `fc00::/7`，并输出启动告警。该默认值覆盖同机 Nginx、Docker Compose 和常见内网反代；公网直连地址不在列表中，其伪造的 `X-Forwarded-For` 会被忽略。
- `TRUSTED_PROXIES=none`（大小写不敏感且必须单独使用）启用严格直连模式，不信任任何代理，`ClientIP()` 只使用 TCP 直连地址。
- 其他非空值按英文逗号解析为代理 IP/CIDR，并完全替代默认列表。应填写反向代理自身的地址而不是客户端网段；非法 CIDR、空列表或将 `none` 与其他值混用都会阻止服务启动。

Gin 只在请求的直连来源属于可信代理时解析客户端 IP 请求头，并从转发链右侧向左寻找首个非可信地址。因此常见 Nginx `$proxy_add_x_forwarded_for` 链中的公网客户端地址会阻止更左侧的伪造前缀生效。默认信任私网的残余风险是：能够从同一私网直接访问应用的其他机器或容器仍可伪造这些请求头；需要消除此风险时应使用 `none` 或配置精确代理地址。

Redis 限流使用原子 Lua 固定窗口，替代旧的近似滑动窗口 List 实现。这是有意的语义变化：窗口边界两侧可分别打满一次，极短时间内通过量最高约为配置值的两倍。例如 `20 次/20 分钟` 在边界可通过约 40 次。帐户级 Session 上限和签发窗口继续控制数据库增长；如未来需要严格抑制边界突发，需单独迁移为 ZSET 滑动窗口。

用户级模型成功请求限流仍使用原有 Redis List 近似滑动窗口，但列表时间戳统一写为 UTC。滚动升级期间，旧节点写入的本地时间字符串和新节点写入的 UTC 字符串无法从格式上区分，可能在一个模型限流窗口内临时误放行或误拒绝。所有节点升级完成并经过一个完整窗口后会自然收敛；本次升级不会切换 Key 或主动删除现有列表。

开放注册仍会受 Critical IP 限流保护，但分布式 IP 多账号攻击不能仅靠 IP 限流阻止。公网开放注册的部署应同时启用 Turnstile 和邮箱验证；更强的设备或多维风控需作为独立安全项目设计。

## PAT 调用契约

面板 PAT（个人访问令牌）通过 `Authorization: Bearer <pat>` 发送，也兼容单值 `Authorization: <pat>`；不接受 URL 参数。`New-Api-User` 不参与鉴权。

- 令牌格式为 `nap_` 加 43 位 base62 随机串（约 256 bit）。明文只在创建时返回一次，数据库只保存 SHA-256 摘要，该摘要同时作为审计记录中的 `token_ref`。
- 每个用户最多 20 个令牌，存放在 `user_access_tokens` 表。创建时必须选择 scope 和过期时间：默认 30 天，最短 1 小时，也允许永不过期（界面会提示风险）。过期令牌立即失效，超过保留期后由 master 节点清理。
- scope 形如 `resource:action`。个人权限（资料、API 令牌、用量、钱包、账户安全）和部分后台权限由令牌目录定义；受 Casbin 保护的后台权限直接复用管理员权限矩阵的 resource 与 action。只能授予自己当前拥有的权限，并且每个请求都会重新读取角色和权限：撤回某项管理员权限后，携带该 scope 的令牌立即失去对应能力。
- 每条面板路由都必须声明所需 scope。未声明的路由一律拒绝（403 `ACCESS_TOKEN_ROUTE_UNDECLARED`），scope 不足返回 403 `ACCESS_TOKEN_SCOPE_DENIED`，令牌过期返回 401 `ACCESS_TOKEN_EXPIRED`。
- PAT 不能创建、改名或撤销 PAT，也不能管理登录会话；这些接口只接受浏览器登录会话。
- PAT 可以完成二次验证，Proof 绑定到该令牌，并且令牌必须持有该操作对应的 scope：`channel.key.read` 需要 `channel:secret_view`，`admin.user.*` 需要 `user:write`，本人的密码、2FA、Passkey、账号绑定和注销需要 `account_security:write`。PAT 不能使用 OAuth 验证方式。
- 通过 PAT 完成的安全变更（如修改密码、2FA、Passkey）会让该用户的所有浏览器登录会话下线，令牌本身保持可用。
- 用户被禁用期间，其令牌全部不可用，重新启用后恢复；注销或删除用户时，其令牌被删除。
- 最后使用时间和 IP 每个令牌每分钟最多更新一次；更新失败不影响请求。

## 临时鉴权流程与二次验证

OAuth state、2FA pending、Passkey ceremony、Telegram bind 等临时状态存放在 `auth_flows`。客户端只持有随机 `flow_token`，数据库仅保存 HMAC 摘要；流程具有用途、provider、intent、用户和登录会话绑定，并且只能原子消费一次。OAuth 注册的 affiliate code 也随登录 AuthFlow 保存。

标准 OAuth 绑定回调由 popup 通过同源 `postMessage` 交给 opener；只有 opener 使用自身内存中的 Bearer 调用后端绑定接口。Telegram 绑定先由已登录前端创建绑定 AuthFlow，再让 widget 回调携带路径中的 `flow_token`，回调时会重新确认原登录会话仍有效。Telegram 的已签名 widget assertion 也会登记为一次性凭据，重复回放会被拒绝。

敏感操作使用有效期 1 分钟的 `X-Security-Proof`：

- `channel.key.read`：查看渠道密钥；
- `passkey.register`：注册 Passkey；
- `passkey.delete`：删除 Passkey。

管理员对其他用户执行的高风险操作同样要求 Proof，且 scope 的 context 绑定被操作用户，签发给某个用户的 Proof 不能改用于另一个用户或另一种动作：

- `admin.user.delete`（`{"user_id"}`）：`DELETE /api/user/:id` 与 `POST /api/user/manage` 的 `delete`；
- `admin.user.manage`（`{"user_id","action"}`，action 为 `disable` / `enable` / `promote` / `demote`）：`POST /api/user/manage`；额度调整 `add_quota` 不要求 Proof；
- `admin.user.update`（`{"user_id"}`）：`PUT /api/user/` 在请求包含新密码或 `admin_permissions` 时要求；仅修改显示名、分组、备注不要求；
- `admin.user.create`（`{"role"}`）：`POST /api/user/` 创建管理员角色时要求；创建普通用户不要求；
- `admin.user.passkey.reset`（`{"user_id"}`）：`DELETE /api/user/:id/reset_passkey`；
- `admin.user.2fa.disable`（`{"user_id"}`）：`DELETE /api/user/:id/2fa`；
- `admin.user.binding.clear`（`{"user_id","binding_type"}` 或 `{"user_id","provider_id"}`）：`DELETE /api/user/:id/bindings/:binding_type` 与 `DELETE /api/user/:id/oauth/bindings/:provider_id`。

这些 `admin.user.*` scope 只对管理员及以上角色签发；已启用 2FA 或 Passkey 的管理员必须使用其中之一，未启用时回退到密码（或已绑定的 OAuth）重新认证；密码登录被关闭时不接受密码验证。通过 PAT 调用上述接口时，令牌需持有 `user:write` 并完成二次验证；旧版令牌不能调用。

Proof 同时绑定用户、登录会话、用户鉴权版本、会话版本和 scope，不能跨用户、跨会话或跨用途复用。对 PAT 而言，Proof 绑定的“登录会话”就是令牌本身。

启用了 2FA 的用户注册 Passkey 时，register begin 与 finish 都必须携带有效的 `passkey.register` Proof；finish 会在消费一次性 AuthFlow 之前重新验证 Proof。未启用 2FA 的首次 Passkey 注册不要求该请求头。

## 升级注意事项

- 旧 `session` Cookie 不再使用；升级后现有面板登录会失效，用户需要重新登录。
- 数据库迁移会新增 `user_sessions`、`auth_flows`、`external_identity_claims` 和 `users.auth_version`，并为已有用户初始化鉴权版本、回填 Telegram 账号唯一归属；若历史数据中同一 Telegram ID 已绑定多个用户，迁移会拒绝继续启动，需先消除歧义。
- 数据库迁移会为 Session 签发计数和分批清理新增索引；已有 `user_sessions` 很大时应为首次启动预留维护窗口。
- `user_sessions.previous_refresh_hash` 会从定长 `char(64)` 迁移为 `varchar(64)`。应用会兼容读取历史定长字段留下的空格填充；迁移后的目标结构必须保持幂等，连续启动不应反复执行列类型变更。
- 仅 master 节点定时清理过期登录会话、超过配置保留期的 revoked 会话和已过保留期的 AuthFlow。
- 未配置 `TRUSTED_PROXIES` 时会兼容信任回环和常见私网代理；使用公网负载均衡器、`100.64.0.0/10`、链路本地地址或自定义 CNI 网段的部署仍需显式配置。需要严格忽略所有转发头时设置为 `none`。
- Redis 限流从近似滑动窗口改为原子固定窗口，存在明确的边界双倍突发语义。
- 用户级模型成功请求限流的 UTC 时间戳在滚动升级期间存在一个窗口的混合格式过渡，期间可能临时误放行或误拒绝。
- 自建客户端应按新的 AuthBundle、`flow_token` 和 Security Proof 契约升级；PAT 客户端可直接移除 `New-Api-User`。
- 旧版 PAT 保留在 `users.access_token` 中，自新版首次启动起 30 天内照常可用，之后自动停用并返回 401 `ACCESS_TOKEN_LEGACY_RETIRED`。停用日期记录在 `options` 表的 `LegacyAccessTokenRetireAt`，由服务端写入，不能通过设置接口修改。过渡期内不能再生成旧版令牌，但可以撤销。
- 删除了 `/api/user/token*` 接口，改为 `/api/user/access_tokens`。
- 降级到旧版本后，新令牌不可用；未撤销的旧版令牌会重新可用，因为旧版本不认停用日期。
