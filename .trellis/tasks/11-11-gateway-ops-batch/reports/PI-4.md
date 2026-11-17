# PI-4 报告：R2 速率限制选分组 + 按用户覆盖

## 改了哪些文件

后端
- `setting/rate_limit.go`：新增用户级覆盖表 `ModelRequestRateLimitUser`（`{"用户ID":[总次数,成功次数]}`）、`GetUserRateLimit` / `GetUserRateLimitCopy` / `ModelRequestRateLimitUser2JSONString` / `UpdateModelRequestRateLimitUserByJSONString` / `CheckModelRequestRateLimitUser`。
- `middleware/model-rate-limit.go`：`ModelRequestRateLimit` 先查用户覆盖（命中则用覆盖值、计数主体 `<uid>:user`），否则沿用分组 / 全局口径。
- `controller/user_rate_limit.go`（新建）：`GET/PUT/DELETE /api/user/:id/rate_limit`、`POST /api/user/rate_limit/clear_group`；复用 `canManageTargetRole` 角色层级限制；写审计（复用现有 `option.update` action）。
- `controller/user_rate_limit_test.go`（新建）：覆盖 AC2.2 后端接线 + AC2.3 只清目标分组。
- `model/option.go`（热点，最小插入）：注册 `ModelRequestRateLimitUser`（InitOptionMap / updateOptionMap / validateOptionValue）。
- `router/api-router.go`（热点，最小插入）：4 条 admin 路由。
- `middleware/model_rate_limit_test.go`：新增 2 个用例（覆盖优先于分组且不影响同组他人；清除覆盖回到分组），memory + redis 两后端。

前端
- `web/src/features/system-settings/request-limits/rate-limit-dialog.tsx`：分组名改为 Combobox 选现有分组；编辑态保留只读分组名、分组已消失时给出提示；新增「同时清除该分组内用户的手动限流」勾选项。
- `.../rate-limit-visual-editor.tsx`：拉取分组列表、给已消失分组的条目加「分组已不存在」标记、保存时按勾选调用清除接口。
- `.../rate-limit-section.tsx`：加一行说明「用户默认跟随分组，可在用户管理中单独设置」。
- `web/src/features/users/api.ts`、`types.ts`：新增 4 个接口与类型。
- `web/src/features/users/components/user-rate-limit-section.tsx`（新建）：用户编辑抽屉里的「速率限制」区块（跟随分组 / 单独设置 + 保存）。
- `web/src/features/users/components/users-mutate-drawer.tsx`：挂载上面的区块（仅编辑态）。
- 测试：`request-limits/__tests__/group-rate-limit-editor.test.tsx`（3 例：分组下拉可选项、消失分组仍显示且可编辑/删除、勾选后调用清除接口）、`users/components/__tests__/user-rate-limit-section.test.tsx`（4 例：显示分组速率、保存单独速率、取消覆盖、非法值拦截）。
- `$T/i18n/PI-4.json`：17 个新 key × 6 语言。

## 验证命令与实际结果

本机没有 Go 工具链（PATH、常见安装目录、WSL、docker 均无）。我下载官方 `go1.26.10.windows-amd64` 到 `%TEMP%\go-pi4` 后执行（未改仓库内任何文件）：
- `go build ./...` → exit 0。
- `go vet ./setting/... ./middleware/... ./controller/... ./model/... ./router/...` → exit 0。
- `go test ./middleware/ -run "TestModelRateLimit"` → ok（含 2 个新用例，memory/redis 均通过）。
- `go test ./controller/ -run "TestUpdateUserRateLimit|TestClearGroupUserRateLimits"` → ok。
- `go test ./setting/... ./middleware/...` → setting 全 ok；middleware 仅 `TestAccessAndPanicLogsFollowLogRotation` 失败，是 Windows 上 TempDir 无法删除仍被占用的日志文件（既有环境问题，与本改动无关）。
- `go test ./model/...` → ok。
- `go test ./controller/` 全量：15 个失败，全部是①同一 Windows TempDir 清理问题（各种 `*DatabaseMatrix`）或②CX-A 正在改的中继重试断言（`TestRespondTaskSubmissionErrorWithoutCause` 期望 `local_rejection` 得到 `response_started`）。我所有权内的 3 个用例全通过。
- 临时路由冲突检查：注册 admin 用户路由（含新增 4 条）`require.NotPanics` 通过（验证 gin 静态/参数同层不冲突），临时测试文件已删除。

前端（PATH 无 bun/tsc）：用 node + 临时 typescript（`%TEMP%\tscheck-pi4`）跑 `tsc -p tsconfig.app.json --noEmit`（等价 `bun check -b`）→ exit 0（8775 文件）。
- `oxfmt --check`（我改的文件）→ 通过；`oxlint`（我改的文件）→ 无输出（无 error/warning）。
- `node node_modules/vitest/vitest.mjs run src/features/users src/features/system-settings/request-limits` → 7 文件 / 33 用例全通过（含新增 7 例）。

## 与 prd 口径的差异 / 我的决定

- **[假设] 采用 prd 建议落点**：用户级覆盖存成系统选项 `ModelRequestRateLimitUser`（用户 ID → `[总次数, 成功次数]`），不改表结构；多节点靠既有 `model.SyncOptions` 轮询选项表传播。
- 覆盖启用时计数主体用 `<uid>:user`，与分组桶（`<uid>:<group>`）独立：覆盖不会消耗同组别的额度，清除覆盖后回到分组桶（新桶，从头计数）。
- 分组下拉取 `GET /api/group/`（分组设置里定义的分组）。已消失分组的条目：表格显示「分组已不存在」标记，仍可编辑参数、可删除；编辑弹窗给提示。
- 「同时覆盖该分组内已手动设置的用户」做成编辑弹窗里的勾选项，保存该条目时调用 `POST /api/user/rate_limit/clear_group`（服务端按 users 表分组解析，只清该组用户；GORM 结构体条件，跨库无方言差异）。注意：分组速率值本身仍随页面「保存」提交，因此「改分组速率」和「清用户覆盖」是两次独立写操作，不是同一事务。
- 管理员接口沿用 admin 用户路由的既有做法：仅 `middleware.AdminAuth()` + `canManageTargetRole` 角色层级校验，未接 authz 细粒度权限；因此管理员不能给同级或更高角色用户设速率（与 `GET /api/user/:id` 一致）。
- 校验两处：`setting.CheckModelRequestRateLimitUser` 注册进 `model.validateOptionValue`（覆盖通用选项接口路径），控制器另给一条更友好的 `ApiErrorT` 文案（总次数 ≥ 0、成功次数 ≥ 1），属少量有意重复。
- 审计沿用现有 `option.update` action（`controller/audit.go` 的 action 模板表不在我所有权内，未新增 action 名）。
- 未改 `web/src/features/system-settings/types.ts` / `security/section-registry.tsx`：用户级覆盖不在系统设置页展示，无需扩展 `SecuritySettings`。

## 未完成 / 有风险

- MySQL / PostgreSQL 未实测（本机无实例、无容器）：本改动无 schema 变更、无手写 SQL，仅一条 GORM 结构体条件查询，理论上跨库一致，但按 AGENTS 数据库条款仍属**未验证**。
- 未跑 `bun run build` 与全量 `bun run lint`：仓库基线在我未触碰的文件（`users/lib/user-form.ts`、`users/lib/user-actions.ts`、`users/components/data-table-bulk-actions.tsx`）已有 oxlint error。
- 跨节点并发管理操作是最后写入生效（选项语义）；单节点用互斥锁串行化读-改-写。

## 需要他人处理（INT）

- 合并 `$T/i18n/PI-4.json`（17 个 key）进 7 个 locale 文件。
- 跑 `bun run build`、全量 lint、以及 `bun check -b` 复核。
- 若希望速率覆盖有独立审计动作名（如 `user.rate_limit_update`）并带专属模板，需要改 `controller/audit.go`（不在我所有权内）。
