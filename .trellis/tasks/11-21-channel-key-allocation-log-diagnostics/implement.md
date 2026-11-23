# 实施及验证顺序

1. 用 Trellis 开始经过审阅的任务，核对各子任务章程、上下文清单以及当前用户工作树。已经读取的 billing 规则需落实到计费、失败及任务路径。涉及权限和前端时按 AGENTS 加载对应 OWASP、UI 复用、React 及七语规则。
2. 实现条目 ID、迁移及完整管理/路由生命周期，接入共用预算账本。先验证添加、重排、删条目和异步回写不会串用，覆盖稳定 ID 和版本冲突。模型列表和路由共用允许模型的规则。
3. 把账本接到实际重试、最终结算与异步退款。实现并发准入、重置、改额、去重预览及确认。渠道子任务复用相同账本与余额管理组件，接口和前端可并行，账本及扣费链路只由一处维护。
4. 日志采集、日志库和详情界面独立实现。主任务统筹 controller/relay.go、model/main.go、Task 私有数据结构和共用结算日志写入，避免三个子任务并发改同一入口。将原始采集放到任何正文修改之前，将预算限制放到实际派发前。确认预算失败日志也能显示实际节点和可用的原始提示。
5. 完成集成验收后更新现有 spec 中按凭据重映射 Key 的旧要求。以逐节点上线、跨节点配置更新、安全回退为验证场景。真实数据库未过齐前不可宣告兼容或完成。

## 验证

- 后端针对受影响包运行 go test。优先扩展 controller/channel_multi_key_test.go、model/channel_status_test.go、model/channel_model_unavailability_test.go、controller/relay_error_log_test.go 和已有结算、任务、模型列表 fixture。重点验证跨组隔离、配置版本、并发拒绝、重试、实际超估、重置后迟到结算和退款，以及重复日志、详情越权和清理竞态。
- 用 SQLite 与真实 MySQL/PostgreSQL 服务执行实际迁移和账本/日志测试，独立 ClickHouse 使用真实日志存储。记录版本、命令、DSN 环境变量名与非跳过结果。每个引擎新建库和上一发布版代表性库都验证两次迁移。用真实状态校验保存数据、索引、唯一约束、有限精度和 failover。不在文档保存秘密连接串。
- 前端从 web/ 运行受影响的测试、类型检查、lint、i18n 检查及构建。复用现有多 Key 管理、QuotaDetailsPopover、ConfirmDialog 和日志详情组件。实际子任务命令写入结果后再验收。
- 以五节点的接入方式模拟两个区域。在没有开启预算和实际开启预算的配置下分别记录主库往返/处理开销，验证缓存更新、启停和日志处理节点。发布前列出仍有影响的约束。任何缺少实际数据库或工具链的验证明确报告。

[安全和验证依据](research/security-and-validation.md) · [代码依据](research/architecture-findings.md) · [条目工作表](../11-21-key-allocation-quotas/implement.md) · [渠道续表](../11-21-channel-manual-balance/implement.md) · [日志工作表](../11-21-request-log-body-node/implement.md)。

用户已明确批准实施，并要求分派四个 Pi。四个 Pi 已于 2026-11-21 启动；分工、接口和共享文件归属以 dispatch.md 为准，进程信息见 pi-runtime.json。实现与验证进行中。

## 2026-11-23 集成续接

- pi-billing：修复图片预处理心跳顺序，以及任务提交入库、用户/令牌扣费和持久结算意图之间的原子性；保留既有退款与流式语义。
- pi-keys：对齐预算准入与路由的模型名称匹配，补充两类活动任务阻止去重的回归。
- pi-logs：先只读排查 R8 的限流键、路由绑定、代理地址、Redis 与内存行为及前端错误来源，写入 research/rate-limit-429.md 后分配具体修复文件。
- pi-ui：只读复核 Realtime 预算生命周期及现有 CI/ClickHouse 验证覆盖。
- 主会话：恢复测试服务，汇总差异与验收证据，执行最终检查并按已有授权提交及发布。历史测试证据保留；变更后只重复受影响的验证。
