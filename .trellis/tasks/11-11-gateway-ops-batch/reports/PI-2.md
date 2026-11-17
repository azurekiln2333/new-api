# PI-2 · R6 缓存命中率卡片 + R7 日志模糊匹配

## 改动文件
后端
- `model/log.go`：`LogMatchMode`（`fuzzy`/`exact`）、`buildLogContainsCondition`、`logTextFilter`/`textFilters`、`LogFilter.UserId`/`.MatchMode`、`apply()` 重写、`Stat` 新增 `CacheReadTokens`/`InputTokens`、`SumUsedQuota(LogFilter)` 重写、`logCacheTokenSelect()`。
- `controller/log.go`：`logMatchMode`、`logFilterFromQuery` 透传 `MatchMode`、`GetLogsStat`/`GetLogsSelfStat` 改用 `SumUsedQuota(LogFilter)`、`logStatResponse` 输出 `cache_read_tokens`/`input_tokens`。
- `model/log_filter_test.go`（新增）：SQLite/MySQL/PostgreSQL/ClickHouse 四库矩阵的匹配模式与缓存汇总测试。
- `controller/log_export_test.go`：`model_name=gpt-%` 用例固定为 `match_mode=exact`，并补模糊（含字面 `%`）用例。

前端（`web/src/features/usage-logs/`）
- `types.ts`、`constants.ts`、`lib/utils.ts`、`lib/filter.ts`、`lib/format.ts`（`formatCacheHitRate`）
- `components/logs-match-mode-toggle.tsx`（新增，base-ui `ToggleGroup`）
- `components/common-logs-filter-bar.tsx`、`components/common-logs-stats.tsx`
- `routes/_authenticated/usage-logs/$section.tsx`（schema 增加 `match`）
- 新增测试：`lib/__tests__/format-cache-hit-rate.test.ts`、`components/__tests__/common-logs-stats.test.tsx`、`components/__tests__/match-mode-filter.test.tsx`
- `.trellis/tasks/11-11-gateway-ops-batch/i18n/PI-2.json`（`Cache Hit Rate`、`Text match mode`，6 语言）

## 根因（代码为准）
- R7：`LogFilter.apply()` 对 `model_name`/`username` 用 `applyExplicitLogTextFilter`（值含 `%` 时按 LIKE 通配，否则等值），`token_name`/`request_id`/`upstream_request_id`/`group` 一律等值。前端列筛选点选徽标传的就是整值，因此只能「精确」，无法包含匹配；同时 `%` 会被当通配符。
- R6：`SumUsedQuota` 只算 `sum(quota)` 与 `rpm/tpm`，`other` 里的 `cache_tokens` 从未参与汇总，前端拿不到分子/分母，无法算命中率。
- 附带缺陷：`GetLogsSelfStat` 用 `username` 字符串过滤，用户名变更后统计会错位；`SumUsedQuota` 也不支持 `request_id`/`upstream_request_id`，与列表筛选不一致。

## 改动
后端
- 匹配模式：`fuzzy` 为默认，`exact` 仅在 `match_mode=exact` 时启用。fuzzy 用字面包含（`LIKE '%值%'`），`%`/`_`/`!` 转义（非 CH 用 `ESCAPE '!'`，ClickHouse 用反斜杠且无 `ESCAPE`），因此搜 `50%` 是字面文本。exact 保留原语义（`model_name`/`username` 的 `%` 仍是通配，兼容旧调用）。
- 统一筛选：`LogFilter.textFilters()` 覆盖 model_name/username/token_name/request_id/upstream_request_id/group，列表、导出、统计共用同一份 `apply()`。
- 用户维度：`LogFilter.UserId`（`GetUserLogs`/`StreamUserLogs` 内部赋值），`GetLogsSelfStat` 改用 `User.Id` 而非用户名；同时保留非管理员的时间范围限制。
- 统计：`SumUsedQuota(filter)` 强制 `Type = LogTypeUnknown` 后加 `type = LogTypeConsume`（与原行为一致：统计只含消费日志，不受日志类型筛选影响）。
- 命中率口径：`cache_read_tokens = SUM(cache_tokens)`；`input_tokens = SUM(prompt_tokens + (usage_semantic == 'anthropic' ? cache_tokens + cache_write_tokens : 0))`，与 `RecordConsumeLog` 的结算口径一致（Anthropic 的 prompt 不含缓存，OpenAI 的 prompt 已含），不看模型名/端点。四种方言分别用 `JSON_VALID`/`JSON_EXTRACT`（MySQL 5.7）、`json_valid`/`json_extract`（SQLite）、`JSONExtractInt`（ClickHouse）、正则提取（PostgreSQL：`::json` 遇到历史非法文本会整条聚合报错，故规避）。
- 缓存查询是 best-effort：失败只 `common.SysError` 记录，不影响用量/rpm/tpm 卡片。

前端
- 过滤栏新增「包含 / 精确」`ToggleGroup`（桌面主筛选区 + 高级筛选区 + 移动端抽屉），`match=exact` 写入 URL，fuzzy 为隐式默认；`buildApiParams` 对列表与统计接口都发 `match_mode`。
- 头部新增第 4 张卡「命中率」：`cache_read_tokens / input_tokens`，保留一位小数并封顶 100%，分母为 0 时显示 `-`；骨架屏同步改为 4 张。

## 与口径的差异 / 决定
1. **默认改为 fuzzy**。PRD 要求「模糊为主、精确可切换」。这会改变所有旧调用者的行为（包括导出接口、第三方脚本），故保留 `match_mode=exact` 作为逃生口；`controller/log_export_test.go` 里依赖 `%` 通配的旧用例已显式标注 `match_mode=exact`。
2. **exact 下 `%` 仍是通配**（仅 model_name/username）。这会与「精确」字面语义略有出入，但删掉它等于悄无声息地破坏既有 API 契约；fuzzy 下才是纯字面。若后续要彻底字面化，需要另立任务并做兼容公告。
3. **分母用 `prompt_tokens` 而不是 `other.input_tokens_total`**。后者由部分渠道写入且可能与结算列不一致；命中率卡片必须与「用量 token」卡片同源，否则两张卡互相矛盾。代价：显式上报 `input_tokens_total` 的行仍按 `prompt_tokens` 计。
4. **统计仍不受 `type` 筛选影响**（原行为），但已受 `request_id`/`upstream_request_id` 与用户维度约束，比原来更贴近列表。
5. 点击徽标做列筛选仍走当前匹配模式，因此 fuzzy 下点 `vip` 组会连 `vip2` 一起匹配；这是模糊语义的直接结果，用户可用切换按钮回到精确。未为此单独做「点选强制精确」的例外逻辑（会让同一控件两副语义）。

## 验证
- `web`: `node_modules/.bin/vitest.exe run src/features/usage-logs` → **27 files / 453 tests 通过**（仅 `window.scrollTo` 未实现的告警）。
- `web`: `oxlint -c .oxlintrc.json`（本包全部改动文件）→ 无输出。
- `web`: `oxfmt -c .oxfmtrc.json --check`（同上）→ 通过。
- 前端用例覆盖：命中率格式化（0 分母显示 `-`、封顶 100%、一位小数）、4 张卡渲染、切换按钮写入 URL、`match_mode` 随列表与统计请求发送、从 URL 恢复、默认 fuzzy 不写 URL。
- **Go 未编译/未测试**：本机无 Go 工具链（`go` 不在 PATH，无 `C:\Program Files\Go`、`~/go/bin`、scoop/choco），也无 docker/bun。`model/log_filter_test.go`、`controller/log_export_test.go` 与两处生产代码均为人工逐行核对（导入、对齐、命名返回、GORM 链式调用）。**四库矩阵未实跑**（无 MySQL/PostgreSQL/ClickHouse 实例与 DSN），仅 SQLite 分支逻辑可静态确信。

## 未完成 / 风险
- 上述 Go 侧编译与四库验证全部缺失，按项目规则本包不能宣称「数据库兼容已验证」。
- MySQL 5.7 的 `JSON_EXTRACT` 路径表达式、ClickHouse `greatest`/`JSONExtractInt` 组合、PostgreSQL 正则提取的实际结果集均未经真机验证；PG 正则对 `"cache_tokens" : 80`（空格）与负数已有意兼容，但对非对象 JSON 依赖 `LIKE` 前置判断。
- 大表上多一次 `other` 扫描（与既有 `SUM(quota)` 同量级），未做性能测量。
- 缓存查询失败时卡片显示 `-`，用户看不到失败原因，只在服务端日志可查。

## 需要他人处理
- INT：在本机补 `go build ./...`、`go vet ./model ./controller`、`go test ./model -run 'TestLogFilter|TestSumUsedQuota'`、`go test ./controller -run TestLogExport`，并 `gofmt -l` 本包两个 Go 文件（本机无 gofmt，已按 gofmt 规则手工对齐）。
- INT：按 `TEST_MYSQL_DSN`/`TEST_POSTGRES_DSN`/`TEST_CLICKHOUSE_DSN`/`AUDIT_*_DSN` 跑四库矩阵，重点看 `cache_read_tokens`/`input_tokens` 聚合列与 `LIKE ... ESCAPE '!'`。
- INT：合并 `i18n/PI-2.json`（2 个 key × 6 语言）。
- 非本包问题：`web/src/features/usage-logs/components/task-logs-filter-bar.tsx` 存在既有 `oxlint` 报错（`typescript(no-import-type-side-effects)`），与 PI-2 无关，未修改。
- 非本包问题：`web/src/routes/_authenticated/usage-logs/$section.tsx` 的 `oxfmt --check` 在 HEAD 上即已失败（PI-2 只加了一行 schema 字段，未重排全文）。
