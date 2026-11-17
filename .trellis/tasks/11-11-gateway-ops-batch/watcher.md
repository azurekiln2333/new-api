# 巡检说明（给 `na-watcher`）

你在 herdr 工作区 `new-api`（`w4C`）里负责盯住 6 个干活的 agent，保证他们不会因为意外停下而卡住。你自己不写代码、不改项目文件。任务目录 `.trellis/tasks/11-11-gateway-ops-batch/`（下文 `$T`）。

## 盯谁

| herdr 名 | 类型 | 工作包 | 完成标志（文件存在即完成） |
|---|---|---|---|
| `na-cx-a` | codex | CX-A | `$T/reports/CX-A.md` |
| `na-cx-b` | codex | CX-B | `$T/reports/CX-B.md` |
| `na-pi-1` | pi | PI-1 | `$T/reports/PI-1.md` |
| `na-pi-2` | pi | PI-2 | `$T/reports/PI-2.md` |
| `na-pi-3` | pi | PI-3 | `$T/reports/PI-3.md` |
| `na-pi-4` | pi | PI-4 | `$T/reports/PI-4.md` |

名字失效时用 `herdr agent list` 按 `cwd` 为 `D:\My2KProjects\new-api` 重新对应 pane。不要碰其它工作区的 agent，也不要碰 `w4C:p8`（调度者）。

## 每轮怎么查

每 4 分钟查一轮（用你自己的定时能力，比如 CronCreate 的周期任务；不要用前台 sleep 干等）。每轮：

1. `herdr agent list`，看这 6 个的 `agent_status`；再看 `$T/reports/` 下有哪些报告。
2. 对每个 agent：
   - `working`：不动它。
   - 报告已存在：它完成了，不再理它。
   - `idle` / `done` / `unknown` 且报告不存在：它意外停了。先 `herdr agent read <名字> --source recent-unwrapped --lines 60` 看停在哪，然后发续跑指令：
     `herdr agent prompt <名字> "继续执行你的工作包 <ID>（见 .trellis/tasks/11-11-gateway-ops-batch/implement.md），从中断处接着做，不要重头来；做完按共同规则写报告 reports/<ID>.md。"`
     如果屏幕上是限流（429）、超时、网络错误这类，同样发这条，让它接着干。
   - `blocked`（它在等确认或提问）：先读屏幕。
     - 它在问需求怎么定：回复「按 implement.md 共同规则第 10 条，自行按最合理的方式决定并写进报告，不要等待」。
     - 它在请求执行命令的许可：只读操作、构建、测试、在本仓库内写文件，可以批准；涉及 `git commit / push / reset / checkout / stash / clean`、删除目录、访问仓库外路径（`D:\MyNewWebProjects\azurekiln-oauth2` 的只读访问除外）、安装全局软件的，一律不批准，回复让它换不需要该操作的做法。
3. 同一个 agent 每轮最多发一次指令。连续 5 轮都在同一个地方停下的，不再催，记下来最后汇报。
4. 每轮只输出一行状态，例如：`02:40 cx-a 工作中 | cx-b 工作中 | pi-1 完成 | pi-2 已续跑(429) | pi-3 工作中 | pi-4 完成`。不要长篇输出。

## 六份报告齐了以后

1. 检查 `$T/dispatch/wave2.sent` 是否存在。不存在就：读 `$T/dispatch/wave2-int.md` 的全文，用 `herdr agent prompt na-cx-a "<全文>"` 发给 `na-cx-a`，然后创建空文件 `$T/dispatch/wave2.sent`。只发一次。
2. 继续按上面的办法只盯 `na-cx-a`，它的完成标志是 `$T/reports/FINAL.md`。
3. `FINAL.md` 出现后：停掉你的定时任务，输出一段不超过 10 行的收尾说明（谁完成了、谁被催过几次、有没有卡死的），然后结束。
