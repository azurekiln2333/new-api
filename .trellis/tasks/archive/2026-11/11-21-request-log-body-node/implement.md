# 实施与验证计划

## 启动条件

父任务已获用户明确批准实施，四个 Pi 已分派。当前状态为 in_progress；共享文件和前端归属以父任务 dispatch.md 为准。

本子任务不依赖 Key 配额或手填额度；但 controller/relay.go、service/log_info_generate.go、TaskPrivateData 等共享文件不得同时交给多个实现者。父任务先定接口再串行整合共享文件。

## 执行顺序

1. 读取 AGENTS.md、web/AGENTS.md、适用 specs、前端 shadcn/i18n skill；权限读取接口按 AGENTS 阅读适用 OWASP 资料并记录实际控制。核对工作树，不动用户原有改动。
2. 复用既有回归文件先补最小失败用例：原始提示经插件改写仍可追溯、管理员可读/普通用户不可读、重复日志关联、节点在查询实例之外保持不变。必要时最多新增一个聚合后端测试文件，避免按层撒文件。
3. 确定 Snapshot、ref、轻量 admin_info.request_content、详情 DTO 的契约，完成捕获器的有界白名单提取及媒体占位；JSON 总输出上限不能被编码开销突破。
4. 增加日志详情存储模型及主库轻量保护模型并完成真实数据库迁移、幂等保存、按稳定 ref 读取和任务引用生命周期。按 design 第 6 节实现 holder/generation、可续租 writer 引用、持久化 pending 状态、逐 ref 互斥和恢复。先证明关系库与 ClickHouse 的单份不可变正文、故障重复写入读取、写入/回收交错及清理语义，再接 API；不增加正文版本和独立创建时间 TTL。
5. 在最早 relay 正文读取入口冻结快照，覆盖 HTTP、协议准备、Playground、multipart、异步任务和 Responses WebSocket response.create；关闭、无效/无文本/超限状态完整。
6. 接成功/尝试失败日志及鉴权后最终拒绝记录，保持各开关和显式错误排除。节点独立于正文开关；每条日志 node_name 取实际执行实例，快照/任务 submit_node_name 保持原提交来源。正文只保存一次、不进入 params/other/进程日志。
7. 增加 admin/root 详情 API、log:read 路由范围、服务端日志关联校验与 no-store。不会以显示 id 查询，也不开放普通 self/token/导出正文。
8. 接日志清理及后台孤立正文回收，覆盖 ClickHouse 日志 TTL 后回收；测试跨保留边界、同请求多行、异步任务延期完成、写失败孤立正文、写入与删除交错以及幂等清理。
9. 复用现有详情和共享组件，增加按需请求、角色消息展示、复制、加载/错误/截断状态、节点桌面列/移动显示。更新 schema/types/API/hooks 和七语文案。
10. 按下述矩阵完成检查，父任务做跨功能检查、整理真实命令/版本/结果；未过数据库矩阵不得标记兼容或完成。

## 测试归属与关键断言

- controller/relay_error_log_test.go：成功/失败、最终拒绝去重、开关与排除、参数无效和原始快照、正文不可用不影响实际错误。
- controller/log_export_test.go 或最合适现有日志接口测试：admin/root/PAT 范围及拒绝、伪造 ref、关联日志不存在、self/token/export 不携正文。
- model/log_other_test.go、model/log_format_test.go：轻量元数据裁剪、处理/提交节点区分及历史提交来源回退；旧任务节点不能填补未知处理节点。断言真实权限契约，不断言私有字段布局。
- model/clickhouse_log_test.go 及新详情模型的同一行为测试：真实写/读/清理；不能仅断言 DDL 字符串当作数据库验证。
- common 现有 body storage 测试：原 storage 被替换/关闭后快照仍可用、UTF-8 截断、JSON 总上限、二进制占位和 multipart boundary 不被改写影响。
- 异步 task/WebSocket 现有测试：HK 提交、US 轮询/结算时，快照及任务 submit_node_name/ref 保持 HK 来源，后续日志 node_name 为 US 执行实例；两个 response.create 的内容分离。
- web/src/features/usage-logs/components/__tests__/detail-preview.test.tsx 等现有合适文件：打开才加载，原始文本和角色展示、复制、失败重试、截断、无 ref 不发请求；现有移动卡片测试覆盖节点。
- 新/重写 Go 测试按 require/assert；新前端测试只在模块 __tests__ 中，明确测试用户可见行为，不用 sleep/随机压力循环。

## 真实数据库矩阵

在实施阶段先探测 Docker/本地服务及现有 TEST_* fixtures，写明实际版本和可复现命令。至少执行：

| 引擎 | 场景 |
| --- | --- |
| SQLite 当前支持版本 | 独立日志库/共用主库，新建和升级；两次迁移；正文大小、ref 唯一、主库保护行的原子条件写与并发清理、故障恢复 |
| MySQL >=5.7.8 | 同上，尤其 payload >64 KiB、LONGTEXT、索引、lockForUpdate 互斥和幂等写入 |
| PostgreSQL >=9.6 | 同上，TEXT、保护行锁、无方言专有 JSON/布尔默认迁移漂移 |
| ClickHouse 现有测试支持版本 | 独立 LOG_SQL_DSN，新表两次建表；重复故障提交按 ref 一致读取；日志 TTL 后正文回收、手工清理及异步任务跨期；并发写入不被回收 |

升级数据库必须来自最新已发布结构并包含现有日志、权限裁剪字段和异步任务；检查原日志/索引/数据均保留。没有服务或测试跳过必须作为阻塞，不可用 mock 替代。

## 命令基线

以下命令在完成实际实现和 fixtures 后执行并保存完整结果；现在未执行，不把它们冒称通过：

- gofmt -w 所有本任务修改 Go 文件。
- go test ./common ./model ./service ./controller ./middleware ./router（按现有共享状态/测试开销适当分包及精确 -run；失败修复后不机械重跑无关模块）。
- 使用真实 TEST_MYSQL_DSN、TEST_POSTGRES_DSN、ClickHouse fixtures 运行新增详情行为用例及启动两次验证；最终记录完整实际命令，不在任务文档存秘密 DSN。
- 如修改 relaykit/tokenkit API，分别在模块内 GOWORK=off go build ./...；纯宿主快照无需为了字段随意触及独立模块。
- web/ 下 bun run 对应现有 test 脚本运行受影响测试，bun check -b、受影响文件 lint、i18n 检查及生产 build；脚本名以 package.json 为准。

## 风险与回退验证

- 详情写入失败不能改变成功响应、收费、错误响应或退款；主日志仍含节点和准确保存状态。
- 列表/日志参数序列化不得携 payload，保障跨区列表大小及本地日志不重复泄露正文。
- 详情上限、所有权、PAT scope、清理和 ClickHouse 生命周期均是合并前门禁；保护行覆盖租约续期/过期、节点崩溃、pending 超时恢复和过期 holder 写回拒绝，不能以单进程锁代替。记录新增主库协调写次数及其跨区开销。
- 回退只停止新增正文/切版本，不删除新表、不清空历史账单。保存父任务最终审批、实际检查和剩余限制。
