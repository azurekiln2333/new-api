# 执行计划

`$T` = `.trellis/tasks/11-13-channel-key-scoping`。四个 pi 在 herdr 工作区 `new-api`（`w4C`）的同一个工作目录 `D:\My2KProjects\new-api`、同一个分支 `custom/rc45-stats` 上并行工作，没有各自的 worktree。调度者在 `w4C:p8`，不要打扰它。

| herdr 名 | 工作包 | 范围 | 完成标志 |
|---|---|---|---|
| `na-pi-1` | PI-1 | 后端：key 元数据、选 key、单转多、多 key 管理接口、渠道测试 | `$T/reports/PI-1.md` |
| `na-pi-2` | PI-2 | 后端：abilities 生成、用户绑定路由、模型列表 | `$T/reports/PI-2.md` |
| `na-pi-3` | PI-3 | 前端：编辑抽屉、多 key 管理对话框、文案 | `$T/reports/PI-3.md` |
| `na-pi-4` | PI-4 | 盲区排查、巡检、集成检查、提交与发布 | `$T/reports/FINAL.md` |

## 共同规则

1. 开工前完整阅读：仓库根 `AGENTS.md`、`$T/prd.md`、`$T/design.md`、本文件。动 `web/` 的还要完整读 `web/AGENTS.md`。AGENTS.md 里的规则全部适用（JSON 封装、三库兼容、现代 Go 写法、测试规范、前端复用规则、受保护标识等）。
2. 先调研再动手。调研只看自己范围内的代码，核实 `design.md` 第 11 节里属于自己的假设，把结论（文件:行号、现状、与设计的出入）写到 `$T/research/<包ID>.md`，不超过 60 行。
3. 只改自己名下的文件（见各包「文件」）。确实要动别人名下的文件时：改动在几行以内且对方报告尚未写出，就先在 `$T/dispatch/cross-edits.md` 追加一行（谁、哪个文件、改了什么），再做精确的小改动；否则写进自己的报告，留给 PI-4。
4. 修改已有文件一律用精确替换的编辑方式，禁止整文件覆盖写入——别人可能正在改同一个文件的其它位置。
5. 构建或测试失败且出错位置在别人名下的文件：那是对方改到一半，等 1–2 分钟重试，不要替对方修。连续 10 分钟仍然失败，写进 `$T/dispatch/cross-edits.md` 并继续做不依赖它的部分。
6. PI-1、PI-2、PI-3 禁止任何 git 写操作（`add / commit / push / stash / reset / checkout / restore / clean / tag`）。只有 PI-4 在发布步骤里可以执行本文件列出的那几条。
7. 契约（`design.md` 第 2、3 节）不得单方面修改。必须调整时在 `$T/dispatch/contract-notes.md` 追加说明后再继续。
8. 测试：一个小功能最多新增一个测试文件，优先扩展现有测试文件；用 `testify` 的 `require` / `assert`；只测可观察的行为和不变量，不堆覆盖率。
9. 不新增 `docs/` 下的文件；不改表结构；不改计费文件（非改不可时按 `design.md` 第 10 节处理）。
10. 全程不向用户提问、不等待确认。遇到需求没写明的地方，按最贴近 `prd.md` 意图、改动最小的方式自行决定，并写进报告。
11. 后端新增的面向控制台的英文文案 key，一行一个写进 `$T/i18n/<包ID>.txt`（写完文案就落盘，不要攒到最后）。
12. 报告 `$T/reports/<包ID>.md` 是完成标志，全部做完并自检通过后才写。内容不超过 80 行：改了哪些文件、各假设的核实结论、与设计的偏差及原因、实际跑过的验证命令和结果、遗留问题。没跑的验证如实写「未验证」。

## 自检命令（PI-1、PI-2）

- `gofmt -l <自己改过的文件>` 输出为空。
- `go build ./...`
- `go vet ./<改过的包>/...`
- `go test ./<改过的包>/... -run <相关用例>`，报告里贴出命令和结果摘要。
- `$T/research/PI-4-verify.md` 出现后，按其中列出的 CI 对应命令补跑属于自己范围的部分。

---

## PI-1 · key 元数据与管理接口

**文件**：`model/channel.go`、`controller/channel.go`、`controller/channel-test.go`、`controller/channel_multi_key_test.go`（及 `model/` 下与 key 状态相关的现有测试文件）、必要时 `types/` 里的错误码。

**步骤**

1. 调研（假设 A1、A2、A3，以及 `design.md` 第 4 节要求的「全部会改变 key 列表的路径」清单、`ErrorCodeChannelNoAvailableKey` 的消费位置和后果）→ `$T/research/PI-1.md`。
2. **先落契约**：在 `ChannelInfo` 加两个字段，实现 `GetNextEnabledKeyFor`、`PublicModels`、`UserBoundModels`（`design.md` 第 2、3 节）。`go build ./...` 通过后创建空文件 `$T/dispatch/contract.ready`。这一步要尽快完成，PI-2 在等它。
3. key 下标重映射（第 4 节）。
4. 单 key 转多 key（第 5 节）。
5. `ManageMultiKeys` 的两个新动作和列表返回字段（第 6 节）。动作成功后调用现有动作所用的同一个渠道缓存刷新函数——PI-2 会把用户绑定索引的重建挂在那个函数上，你不需要另外调用别的东西。
6. 渠道测试路径改用 `GetNextEnabledKeyFor(测试模型, 0)`。
7. 测试（扩展 `controller/channel_multi_key_test.go` 或 `model/` 现有测试文件）：按模型选 key；绑定 key 对他人不可见、对本人优先；删中间 key 后配置不串位；单转多；「有启用 key 但无符合条件的 key」返回的错误不触发自动禁用。
8. 写报告前若 `$T/research/PI-4-blast-radius.md` 已存在，读它，把属于本包的遗漏项补上。
9. 自检 → `$T/reports/PI-1.md`。

## PI-2 · abilities 与用户绑定路由

**文件**：`dto/channel_settings.go`、`model/ability.go`、`model/channel_cache.go`、`middleware/distributor.go`、`service/channel_select.go`、`controller/relay.go`、`controller/model.go`、控制台用户可用模型接口所在文件、一个测试文件。

**步骤**

1. 调研（假设 A4、A5、A6；转发路径上全部 `GetNextEnabledKey()` 调用点；全部从渠道推导「分组 × 模型」的位置）→ `$T/research/PI-2.md`。
2. `ChannelOtherSettings` 加 `GroupModels`。
3. 等 `$T/dispatch/contract.ready` 出现后再做依赖 PI-1 方法的部分（等待期间继续调研和第 2 步；每分钟看一次，不要空转）。
4. abilities 生成（`design.md` 第 7 节）。
5. 用户绑定索引、`GetUserBoundChannel`、`GetUserBoundModels`、分发中间件接入、转发路径改用 `GetNextEnabledKeyFor`（第 8 节）。索引重建必须挂在「渠道增删改和多 key 管理动作之后都会调用的那个缓存刷新函数」上，并且在 `MemoryCacheEnabled` 关闭时同样生效。
6. 模型列表并入绑定模型（第 8 节末）。
7. 测试（扩展 `model/` 下现有的 ability 或渠道缓存测试；没有合适的就新增一个文件）：分组白名单生效；全部 key 已绑定时不产生 ability；未用新配置的渠道 abilities 与原来逐条相同；绑定用户跨分组命中绑定渠道；非绑定用户选不到该渠道的绑定 key。
8. 写报告前若 `$T/research/PI-4-blast-radius.md` 已存在，读它，把属于本包的遗漏项补上。
9. 自检 → `$T/reports/PI-2.md`。报告里必须写明：索引刷新方案、粘滞/故障转移逻辑的核实结论、是否碰过任何计费文件（应为否）。

## PI-3 · 前端

**文件**：`web/src/features/channels/**`、渠道相关的类型与请求封装、`web/src/i18n/locales/*.json`。

**步骤**

1. 调研（假设 A7；渠道编辑抽屉里 key、分组、模型、`settings` 各自的表单结构；多 key 管理对话框的接口调用；仓库里可复用的多选、用户选择、确认对话框组件）→ `$T/research/PI-3.md`。`channel-mutate-drawer.tsx` 很大，按需分段阅读。
2. 按 `design.md` 第 9 节实现。接口字段以 `design.md` 第 2、5、6 节为准，不要等后端写完。
2a. **追加（`prd.md` R5 / AC11）渠道卡片跳转按钮**：先调研 `web/src/features/channels/components/channel-card.tsx` 里现有操作按钮的写法（用的哪个按钮组件、尺寸与变体、图标来源、提示文案的写法）以及渠道地址字段，把结论补进 `$T/research/PI-3.md`。然后照同样的写法加一个按钮：在新标签页打开渠道地址，带 `noopener,noreferrer`；只接受 `http://` / `https://` 开头的地址，其它一律不显示按钮；地址为空不显示。复用现有按钮组件和图标库，不新写通用组件，不改其它按钮的布局。表格视图不在本次范围内。
3. 文案：七个语言文件全部补齐；用 `bun run i18n:sync` 等现有工具校验。收尾时把 `$T/i18n/*.txt` 里已有的后端 key 一并合入（之后才出现的留给 PI-4）。
4. 自检：按 `web/AGENTS.md` 规定的类型检查、lint、测试、构建命令执行；为新增的表单逻辑补充或扩展现有测试。
5. `$T/reports/PI-3.md`。报告里按前端规则写明：复用了哪些现有组件；如有新写的通用 UI，说明现有组件为什么不够。

## PI-4 · 盲区排查、巡检、集成、发布

你是最后一道关，也是唯一可以提交和推送的人。

**阶段 A（立即开始，目标 15 分钟内完成）**

1. `$T/research/PI-4-verify.md`：读 `.github/workflows/ci.yml`，列出 CI 实际执行的检查以及在本机（Windows，PowerShell / Git Bash）对应的命令。其他三个包会用它自检。
2. `$T/research/PI-4-blast-radius.md`：只读排查，列出 `design.md` 可能漏掉的位置，每条写文件:行号和归属包。至少覆盖：所有调用 `GetNextEnabledKey()` 的位置；所有从 `channel.Models` × `channel.Group` 推导数据的位置；所有修改渠道 key 列表或 `channel_info` 的位置（含复制渠道、按标签批量编辑、上游模型自动同步、导入导出）；所有读取 `settings` / `ChannelOtherSettings` 并可能在保存时丢字段的位置。
3. 阶段 A 不改任何项目文件。

**阶段 B（巡检，直到三份报告齐全）**

循环执行 `python .trellis/tasks/11-13-channel-key-scoping/dispatch/wait_lanes.py`，它会阻塞到有事发生再输出一行：

- `ALL_DONE`：进入阶段 C。
- `STILL_WAITING ...`：再执行一次。
- `STALLED <herdr名> <包ID> <状态>`：该 agent 没在工作且没写报告。先 `herdr agent read <herdr名> --source recent-unwrapped --lines 60` 看停在哪，再发：
  `herdr agent prompt <herdr名> "继续执行你的工作包 <包ID>（见 .trellis/tasks/11-13-channel-key-scoping/implement.md），从中断处接着做，不要重头来；做完按共同规则写报告 reports/<包ID>.md。"`
  限流、超时、网络错误同样发这条。它在提问时，回复「按共同规则第 10 条自行决定并写进报告，不要等待」。然后再执行脚本。
- 同一个 agent 最多催 5 次。5 次后仍无报告，就把它的工作包剩余部分接过来自己做完，并在 `FINAL.md` 里说明。
- 只操作 `na-pi-1`、`na-pi-2`、`na-pi-3`；不要碰 `w4C:p8` 和其它工作区。

**阶段 C（集成检查，此时只有你在改代码）**

1. 读三份报告、`$T/dispatch/contract-notes.md`、`$T/dispatch/cross-edits.md`（存在的话）。
2. 对照 `git diff` 完整审一遍：
   - `prd.md` 的 AC1–AC11 逐条找到对应的实现和测试；缺测试的关键不变量（AC1、AC4、AC7、AC8、AC9）补上。
   - `design.md` 第 10 节的八条红线逐条核对。
   - 阶段 A 盲区清单里的每一项都已处理或有明确的不处理理由。
   - AGENTS.md 规则：`common.*` JSON 封装、无方言专属 SQL、现代 Go 写法、无单次调用的机械辅助函数、测试文件没有分散、前端复用规则、七语言文案齐全（含 `$T/i18n/*.txt` 全部 key）、受保护标识未动。
   - 确认 `git diff --stat` 里没有表结构、迁移、计费文件的改动。
3. 跑 `PI-4-verify.md` 里的全部命令（后端构建、vet、测试；前端类型检查、lint、测试、构建）。失败的直接修，修完重跑，直到全过。与本任务无关、改动前就存在的失败，用 `git stash` 以外的方式确认（例如只看失败用例是否涉及本次改动的文件），如实记录，不要去修无关代码。
4. 端到端确认：用 SQLite 起一个临时实例或写一个贯穿分发流程的 Go 测试，至少实际走通 AC1、AC2、AC4、AC6。
5. 数据库验证口径：本任务没有表结构、GORM 标签、迁移、原生 SQL 的变化。如果审查发现实际上有，按 AGENTS.md 要求做三库真实验证；本机没有 MySQL / PostgreSQL 实例时，如实写「未验证」并说明影响，不得声称三库兼容。
6. 规范沉淀：在 `.trellis/spec/backend/` 里合适的文件追加一小节，记录 `design.md` 第 2、3、4、7 节的契约要点（该目录不入库，只是本地规范）。

**阶段 D（提交与发布）**

1. `git status --short` 逐行确认：只有本任务的文件，没有构建产物、临时实例的数据库文件、密钥。
2. `git add -A`（`.trellis/` 已被本地排除，不会进提交），再 `git diff --cached --stat` 确认一遍。
3. 提交。规范：
   - Conventional Commits，英文，风格与 `git log` 近几条一致；标题不超过 72 字符，例如
     `feat(channel): add per-key model scope, user-bound keys and per-group model visibility`
   - 正文用几行短句说明四项能力和「无表结构变更」。
   - **不得出现任何 AI 或工具署名**：没有 `Co-Authored-By`、没有 `Generated with`、不提 pi / Claude / agent。
   - 不改 git 配置，不用 `--no-verify`，不用 `--amend`。
4. 推送：`git push fork HEAD:main`。不许 `--force`。被拒绝（非快进等）就停在这里，把原始报错写进 `FINAL.md`，不要 pull、rebase 或强推。
5. Tag：推送到 `fork/main` 会触发 `docker-fork.yml`，它先跑 CI，再构建镜像，并自行创建名为 `custom-<提交日期>-<提交哈希前7位>` 的 tag 和 release。手动推任何 tag 会额外触发 `docker-build.yml` 和 `release.yml`（两者对所有 tag 推送生效），与上述流程重复并争用同名 release。因此**不要手动创建或推送 tag**，只在 `FINAL.md` 里写出预期的 tag 名（`git show -s --format=%cs HEAD` 去掉横线 + `git rev-parse --short=7 HEAD`）。例外：`$T/dispatch/manual-tag.txt` 存在时，按该文件里的指示办。
6. 推送后不查看、不等待云端构建状态，用户自己检查。
7. 写 `$T/reports/FINAL.md`（不超过 60 行）：提交哈希、推送结果、预期 tag；AC1–AC11 表格（通过 / 未验证 + 依据）；与设计的偏差；催过谁几次、接手过什么；实际执行的验证命令与结果；未验证项和遗留风险。写完即结束。
