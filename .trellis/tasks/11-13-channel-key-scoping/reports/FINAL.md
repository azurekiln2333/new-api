# FINAL：渠道 Key 级授权与分组模型可见性

## 提交与推送

- 提交：`1d81870d4` — `feat(channel): scope channel keys by model, user and group`（40 个文件，+2633/−120）
- 推送：`git push fork HEAD:main` → `7cf22ef88..1d81870d4  HEAD -> main`，exit 0，无强推、无 `--amend`、未改 git 配置
- 预期 tag（由 `docker-fork.yml` 自动创建，未手动推）：`custom-20261113-1d81870`
- 提交信息无任何 AI / 工具署名；`git show -s --format=%B` 已核对

## AC 表

| AC | 结论 | 依据 |
|---|---|---|
| AC1 绑定 key 独占、本人优先 | 通过 | `TestMultiKeyKeyScopeSelection`（绑定独占/优先）、`TestSelectChannelForRequestRoutesBoundUserThenFallsBackOnRetry`、端到端 `TestDistributeRoutesBoundKeyAndFallsBackWhenNoKeyServes`（他人请求 503，拿不到 key） |
| AC2 跨分组可用 + 模型列表可见 + 解绑即恢复 | 通过 | 端到端测试（渠道 group=vip、用户 group=default 仍命中 9401）；`TestGetUserModelsIncludesUserBoundModels`；解绑走 `set_key_user(0)` → `Channel.Update()` → `InitChannelCache()` 立即重建索引 |
| AC3 全部 key 已绑定 → 不再对分组提供模型 | 通过 | `TestChannelAbilitiesExcludeUserBoundKeys`（abilities 为空）+ `TestChannelCacheRoutingHonorsGroupModelScope` 同源推导 |
| AC4 分组白名单：列表无 B、请求不路由 | 通过 | `TestChannelAbilitiesHonorGroupModelScope`、`TestChannelCacheRoutingHonorsGroupModelScope`；模型列表走 `abilities` 表（`GetGroupEnabledModels`），路由表用同一推导 |
| AC5 单 key 转多 key，原 key 为首 key | 通过 | `TestUpdateChannelConvertsSingleKeyChannel`（append/仅保留/replace 三例 + Codex 拒绝） |
| AC6 key 级模型 + 渠道测试选对 key | 通过 | `TestMultiKeyKeyScopeSelection`（A 只用 key#1、B 只用 key#2）；`controller/channel-test.go` 测试路径置 `id=0` → 管理端语义，按测试模型选 key |
| AC7 增删改 key 后配置不串位 | 通过 | `TestMultiKeyManageKeepsKeyScopesOnTheirKey`（删中间 key 后 scope/binding 跟原 key 走）+ `remapKeyScopes` 覆盖 `Channel.Update`/`UpdateChannel`/`ManageMultiKeys` |
| AC8 无可用 key 时可重试、不自动禁用 | 通过 | 新错误码 `channel_key_scope_unsatisfied`（503、非 `channel:` 前缀）+ 端到端测试：distributor 换到下一渠道并成功，9402 仍为 enabled |
| AC9 未用新配置的渠道行为不变 | 通过 | `TestMultiKeyKeyScopeSelection` 末例（`PublicModels()==GetModels()`、`GetNextEnabledKeyFor` 与旧 `GetNextEnabledKey` 同结果）；abilities 推导对正常渠道逐条相同（偏差见下） |
| AC10 无表结构变更、七语言齐全、本机 CI 对应检查通过 | 通过 | `git diff` 无 gorm 标签/迁移/原生 SQL；`i18n:sync` 七语言 missing=0；后端 build/vet/test 与前端 check/test/build 全绿（lint 见「未验证」） |
| AC11 渠道卡片跳转按钮 | 通过 | `data-table-row-actions.tsx` 复用现有 Tooltip+Button(lucide `ExternalLink`)，仅 `http(s)` 且有 host 时渲染，`noopener,noreferrer`；`channel-address-button.test.tsx` 6 例；文案七语言齐全 |

## 实际执行的验证（Windows，`GOWORK=off`）

- `gofmt -l` 全部改动 Go 文件 → 空
- `go build ./...` → exit 0；`go vet ./...` → exit 0
- `relaykit/`：`go build ./...` / `go vet ./...` → exit 0；`tokenkit/`：`go build ./...` → exit 0
- `go test ./model/... ./service/... ./middleware/...` → ok（middleware 9.1s）
- `go test ./controller/...` → ok（195s，含新增端到端用例）
- `go test ./relay/...` → ok
- `bun check -b`（web）→ ✓ No type errors in 1,466 files
- `bun run test`（web）→ 230 files / 2673 tests 全通过
- `bun run build`（web）→ exit 0
- `bun run i18n:sync` → 七语言 missing=0 extras=0；逐 key 复核 en 的 9 个后端文案在七语言均存在且已翻译

## 未验证 / 遗留风险

- **MySQL / PostgreSQL 未验证**：本任务无表结构、GORM 标签、迁移、原生 SQL 变化（只改 `channel_info` / `settings` 两个 JSON 列的内容），按 AGENTS.md 不需三库矩阵；本机也无实例。SQLite 实跑覆盖了全部新增用例。
- **`bun run lint` 未通过，但与本任务无关**：143 个 error 全部落在未改动的文件（如 `src/lib/utils.ts`、`src/assets/*`、`scripts/sync-i18n.mjs`），改动前即存在；本次改动的 22 个前端文件无 error。CI 不执行 lint。
- 设计偏差（均已在报告中记录）：
  1. 新增错误码 `ErrorCodeChannelKeyScopeUnsatisfied` 而非复用 `ErrorCodeChannelNoAvailableKey`（后者会触发自动禁用，违背 AC8）。
  2. 绑定路由接入点从 `middleware/distributor.go` 挪到 `service/channel_select.go`（三种传输共用入口），distributor 只改选 key 与粘滞写入。
  3. `CopyChannel` 保留 key 绑定并深拷贝 channel_info 五个 map（解绑会把用户专属 key 变成公共 key）。
  4. abilities 推导改用 `GetModels()`；`models` 为空的退化渠道不再产生 `model=""` 的 ability。
- 我补的集成改动：`middleware/distributor.go` 原样忽略 `SetupContextForSelectedChannel` 的错误（会带空 key 转发），改为「无符合 key 时记录失败渠道并换下一个候选渠道，最多 3 次，仍失败则返回 503」；`multiKeyActionRequiresSensitiveWrite` 纳入 `set_key_models` / `set_key_user`（绑定 key 等于授权，服务端必须要求敏感写权限，与前端一致）。
- 已知小瑕疵：`UserBoundChannelRouted` 标记在请求内不复位，绑定路由后若再重试到普通渠道，该次重试不写粘滞（只影响粘滞收益，不影响正确性）。

## 巡检记录

- 催过 `na-pi-1` 1 次、`na-pi-3` 1 次（两者都停在「询问用户是否继续」，违反共同规则第 10 条）；`na-pi-2` 未催。
- 未接手任何工作包；三份报告齐全，无 `contract-notes.md`（契约未被单方面修改），`cross-edits.md` 两条均为 PI-2 的小改并已复核。
- 阶段 A 产物：`research/PI-4-verify.md`（CI 对照与本机命令）、`research/PI-4-blast-radius.md`（盲区清单）。清单中「`InitChannelCache` 直接从 `channel.Models`×`Group` 建路由表」这条 PI-2 已处理，其余项均已覆盖或注明不改的理由。
- 规范沉淀：`.trellis/spec/backend/database-guidelines.md` 新增「Per-key and per-group channel configuration」小节（本地规范，不入库）。

## CI 修复

**现象**：CI backend job 在 `make test` 阶段失败，`router` 包 `TestGetOpenAIVideoRouteRendersJimengTask`、`TestGetOpenAIVideoRouteHandsNativeXAITaskToPluginQuery/task_xai_openai` 返回 500 `{code:get_channel_failed, message:'No available channel for model  under group default (distributor)'}`（模型名为空），随后 panic。

**根因**（两处，同在 `1d81870d4` 里我加的代码）：
1. `middleware/distributor.go`：`SetupContextForSelectedChannel` 的返回值原本被忽略，我改成「只要 `setupErr != nil` 就判定失败」。任务查询类路由（`/v1/videos/{id}` 等）`shouldSelectChannel=false`，`channel` 为 nil，该函数必然返回 `ErrorCodeGetChannelFailed`，于是这类路由从「照常继续」变成 500；错误分支还调用 `i18n.T`，测试环境未 `i18n.Init()` 时 nil localizer 直接 panic。
2. `model/channel_cache.go`：`userBoundChannelIDs` 在关闭内存缓存（测试默认）时按 10s TTL 在**请求路径**上 `DB.Find(&channels)` 全表重扫，日志里的 `no such table: channels` 即由此而来。

**改动**（最小修复，4 个文件 +65/−44）：
- 仅当错误码为 `ErrorCodeChannelKeyScopeUnsatisfied` 才进入绑定/回退逻辑；其余任何 setup 结果保持 `7cf22ef88` 的行为（含 nil channel 的任务查询路由）。
- `userBoundChannelIDs` 改为纯读索引；索引只在 `InitChannelCache()`（启动与每次渠道变更）重建，请求路径不再查库。删除随之无用的 `userBoundChannelsBuiltAt` 与 `userBoundChannelIndexTTL`。
- 回归用例：`middleware/distributor_test.go` 新增 `TestDistributeKeepsModelLessTaskRoutesOffTheKeyScopePath`（无模型名的 `/v1/videos/:task_id` 必须走到 relay handler 且 200，且不构建索引也能跑）；`model/ability_test.go` 补 `GetUserBoundChannel(boundUserId, "")` 返回 nil 的断言。

**验证（按 `.github/workflows/ci.yml` 实际命令，`GOWORK=off`、先建 `web/dist` 占位）**：

| 步骤 | 命令 | 结果 |
|---|---|---|
| 复现 | `go test ./router/ -run TestGetOpenAIVideoRoute -count=1 -v` | 修复前：FAIL + panic（`distributor.go:167`）；修复后：`ok 6.017s` |
| Vet root | `go vet ./...` | exit 0 |
| Vet relaykit | `cd relaykit && go vet ./...` | exit 0 |
| Vet tokenkit | `cd tokenkit && go vet ./...` | exit 0 |
| Build root | `go build ./...` | exit 0 |
| Build relaykit | `cd relaykit && go build ./...` | exit 0 |
| Build tokenkit | `cd tokenkit && go build ./...` | exit 0 |
| Test (make test 等价) | `go test $(go list -e ./... \| grep -vx $(go list -m))` → 82 个包 | exit 0，47 个包 `ok`，无 `FAIL`（含 **router**、model、service、middleware、controller、relay） |
| Test relaykit | `cd relaykit && go test ./...` | exit 0，无 FAIL |
| Test tokenkit | `cd tokenkit && go test ./...` | exit 0，无 FAIL |
| Frontend install | `cd web && bun install --frozen-lockfile` | 1269 installs, no changes |
| Frontend typecheck | `bun check -b` | ✓ 1,466 files, no type errors |
| Frontend test | `bun run test` | 230 files / 2673 tests passed |
| Frontend build | `bun run build` | `built in 5.00s` |

上一份报告只跑了改动相关包，漏了 `router`，这次是全量。

**提交**：`f24cf12dc` — `fix(channel): keep model-less task routes off the bound-key path`（4 files, +65/−44），无 amend、无 AI/工具署名；`git push fork HEAD:main` → `1d81870d4..f24cf12dc`，exit 0，未强推、未推 tag。

**预期 tag**（`docker-fork.yml` 自动创建）：`custom-20261113-f24cf12`。

## R6–R10

### 交付内容

| 项 | 内容 | 状态 |
|---|---|---|
| R6 多 Key 列表脱敏预览 | 后端 `maskKeyPreview` 生成 `key_preview`：普通 key 前 3 + `…` + 后 4（<12 位只给 `…` + 后 2，总数 ≤ 8）；JSON key（Vertex 服务账号）只显示 `client_email`，没有则 `project_id`，都没有则回落普通规则。只作为接口字段返回，不进日志/审计/错误信息。前端序号列用等宽字体显示 `序号. 预览`，空显示 `-` | 通过 |
| R7 显示当前密钥两把 key 混成一串 | 结论是「只是显示错，库里是对的」：单行 `Input` 吞掉换行。改为多 key 渠道用只读 `Textarea` 逐行显示（顺序 = key 下标顺序），单 key 渠道不变；`channelKey` 原值仍供复制。不改 key 直接保存不提交 `key` 字段，后端按 key 内容重映射，key 列表/状态/模型范围/绑定用户均不变 | 通过 |
| R8 渠道测试可选 key | 多 key 渠道的测试对话框多出「Tested key」：自动（按测试模型选 key，与原行为一致）或指定某把（序号 · 预览 · 状态）。后端新增可选 `key_index`，先校验范围与 `MultiKeyModels`（不通过则本地报错、不发上游请求），允许测已禁用 key；指定 key 的测试不改渠道级 `response_time`，失败只禁用被测 key、渠道仍启用 | 通过 |
| R10 游乐场 PC 消息布局 | 成因是 `PlaygroundMessageContent` 根节点写死 `w-full`，内容永远铺满整列。改为按角色分宽度：回复 `w-full max-w-3xl` 左对齐，用户气泡 `w-fit max-w-[85%] sm:max-w-[70%]` 右对齐；消息列不再被满宽内容撑满。窄屏布局不变 | 通过（未做浏览器目视） |
| R9 游乐场「项目」 | 项目新建/重命名/删除（删前确认，只把会话移到未分组、不删会话）、会话移入/移出项目、列表按项目分组可折叠、未分组排最后。数据存浏览器 localStorage，与 `6ab43bdb9` 的会话同处、同按用户隔离，**未改任何表结构** | 通过（无服务端迁移） |

### 审查中发现并修掉的集成问题

- `model.Channel.KeyServesModel` 原本只是 `keyServesModel` 的单调用方机械包装，违反 AGENTS.md；改为直接导出 `keyServesModel` 本体（内部调用点同步改名）。
- `testChannel` 里 `c.Set("id", testUserID)` 在 if/else 两支各写一次、外面又写一次，收敛为一处，并让失败结果统一走同一段 `keyIndex` 归属逻辑。
- `multi-key-key-scope.test.tsx`、`channel-mutate-drawer.tsx` 未通过 `oxfmt --check`，已格式化。

### 验证（按 `.github/workflows/ci.yml` 实际命令，`GOWORK=off`，先建 `web/dist` 占位）

| 步骤 | 命令 | 结果 |
|---|---|---|
| gofmt | `gofmt -l` 全部改动 Go 文件 | 无输出 |
| Vet root / relaykit / tokenkit | `go vet ./...`（三个模块各自） | exit 0 |
| Build root / relaykit / tokenkit | `go build ./...`（三个模块各自） | exit 0 |
| Test（make test 等价） | `go test` 全量 82 个包（排除主模块） | exit 0，47 包 ok，无 FAIL；**`router` 明确 `ok 30.705s`** |
| Test relaykit / tokenkit | `cd relaykit && go test ./...`、`cd tokenkit && go test ./...` | exit 0，无 FAIL |
| 前端 install | `bun install --frozen-lockfile` | 1269 installs, no changes |
| 前端 typecheck | `bun check -b` | ✓ 1,469 files, no type errors |
| 前端 test | `bun run test` | 232 files / 2692 tests passed |
| 前端 build | `bun run build` | `built in 5.20s` |
| i18n | `bun run i18n:sync` | 七语言 missingCount=0、extrasCount=0；本次新增 key 在七语言均有译文（ja/ru/zh 的 untranslated 计数是品牌名/URL 等既有不译项） |
| lint（改动文件） | `oxlint` 30 个改动前端文件 | exit 0 |
| format（改动文件） | `oxfmt --check` 37 个改动前端文件 | 通过（修了 2 个文件） |

**改动前就存在的失败（如实记录）**：`bun run lint` 全仓 exit 1，错误均在未改动文件（`src/assets/logo.tsx`、`src/features/auth/lib/validation.ts`、`scripts/sync-i18n.mjs` 等），本次改动的 30 个文件无 error。CI 的 frontend job 不执行 lint / format:check。

### 提交与推送

| 提交 | 标题 | 文件 |
|---|---|---|
| `71ce2a660` | `feat(channel): show masked key preview and fix merged key display in multi-key channels` | 21 files, +1096/−227（后端 + channels 前端 + 七语言文案） |
| `7ede67aa2` | `feat(playground): group chats into projects and fix PC message layout` | 21 files, +862/−42（playground 全部） |

- 两次提交均无 amend、无 AI/工具署名；i18n 文件由两组共用，整体随第一次提交落地（第二次提交的 key 已在其中，无缺 key）。
- 第一次提交单独验证过：`go build ./...`、`go vet`（controller/middleware/model）、`bun check -b` 全绿。
- `git push fork HEAD:main` → `f24cf12dc..7ede67aa2`，exit 0；未强推、未手动推 tag。

**预期 tag**（`docker-fork.yml` 自动创建）：`custom-20261113-7ede67a`。

### 未验证 / 遗留

- R9/R10 未做浏览器目视确认（宽屏/窄屏最终观感、超长代码块横向滚动）；折叠状态不持久化，未实现拖拽（无现成组件）。
- R8 未做真实上游多 key 渠道端到端手测（无可用上游凭据），正确性由单测覆盖。
- R9 存浏览器本地，无表结构变更，故无 MySQL/PostgreSQL 迁移验证需求；本轮全部改动均无 schema 变化。
