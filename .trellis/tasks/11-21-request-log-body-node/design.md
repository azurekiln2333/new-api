# 请求正文与节点诊断技术设计

## 1. 边界和提案状态

目标是通用日志的原始提示内容及来源节点。方案供父任务一次性最终审阅，未开始写产品代码。保持既有消费计费、错误开关、角色裁剪及日志存储选择。

最小行为缺口位于入站正文生命周期、通用日志落盘和详情读取三处：仅改详情 UI 会缺数据；正文塞 other 会让列表及本地日志读取/打印正文。

## 2. 捕获契约

新增宿主内可序列化的 RequestContentSnapshot，包含 ref、request_id、user_id、submit_node_name、created_at、capture_status、captured_bytes、truncated、payload。ref 使用已有 common.GetUUID 生成的不透明随机标识，绝不使用列表显示 id。submit_node_name 在入站时冻结，只表示原始请求/任务的提交来源。

每条日志的 actual processing node 是另一项数据：other.admin_info.node_name 在该次业务操作执行时从 common.NodeName 取得并随日志事件传递，不从正文快照或 TaskPrivateData 的提交来源回填；异步落盘也不能改成日志写入器所在实例。HTTP 请求在本机重试时两者通常相同；HK 提交、US poll/settle 时，后续日志 node_name 是 US，快照 submit_node_name 仍是 HK。查询日志的节点不影响两者。

仅在 relay 推理入口、鉴权成功后安装日志捕获上下文；不标记登录/用户管理/认证入口。在首次 common.GetRequestBody 创建原始 BodyStorage 时、任何协议/ingress/plugin/参数覆盖改写之前读取允许字段并复制到受限快照。用 common.ReadBodyJSONFields 或有界 reader，不保存 BodyStorage 指针；正文替换和 BodyStorageCleanup 可以照常关闭存储。

捕获器属于 common/ 只读基础设施，不依赖 service 或 model，避免包循环；路由/服务层传入是否启用、最大长度和所选协议。只复用原有已限制大小的读取；对于在读取正文之前即拒绝的请求不额外强制读取潜在超大正文，状态为 unavailable 而不是虚构空内容。

允许字段按入站协议选择：

| 协议 | 字段 |
| --- | --- |
| OpenAI Chat/Completions | messages、prompt |
| Responses/compact | input、instructions |
| Claude | messages、system |
| Gemini | contents、systemInstruction |
| multipart 图片/音视频 | prompt 等现有协议允许的文本表单字段 |

保留角色、顺序、文本数组和必要对象结构；值里的 data: 图片/音频、base64、文件内容替换为类型/字节数占位。普通文本 URL 可以原样保留，不能把请求 URL 的查询认证字段单独采集进来。不得用字符串截断整个 JSON 导致无效 JSON：按字段/片段预算截取 UTF-8 文本，元数据说明丢弃的片段及截断；最大保存值包含最终 JSON 开销。

REQUEST_CONTENT_LOG_ENABLED 默认 true；REQUEST_CONTENT_LOG_MAX_BYTES 默认 262144，正整数且不超过现有请求体上限。不为这些技术配置新建前端设置页。

## 3. 日志存储与关联

新增 log_request_details 表到实际 LOG_DB：

| 字段 | 契约 |
| --- | --- |
| ref | 有界不透明字符串；关系库主键；ClickHouse 查询键 |
| request_id / user_id | 原始服务端请求关联和所有者 |
| submit_node_name | 原始请求/任务的提交来源，后续 poll/settle 不替换 |
| created_at | 原入站时间，只作归属/分区，不单独决定删除 |
| capture_status | recorded / truncated / no_content / unsupported / invalid / unavailable / disabled |
| captured_bytes / truncated | 受限保存量及明确状态 |
| payload | UTF-8 JSON 文本，只存已筛选提示内容 |

关系库采用 GORM 模型。payload 的数据库类型按方言为 SQLite/PostgreSQL TEXT、MySQL LONGTEXT，避免 MySQL TEXT 64 KiB 上限与 256 KiB 捕获上限不符。除 ClickHouse 必需的显式 DDL 外使用 GORM；不要求数据库 JSON 运算或外键。

在实际出现消费/错误日志时首次保存快照，同一入站请求通过受同步保护的保存状态复用 ref；重试的每条错误日志和最终成功日志共享同一 ref。成功、失败开关都关闭时不落正文。保存只与现有日志落盘生命周期关联，不引入新的跨节点 RPC 或把正文写入 params.Other。

日志 other.admin_info 增加：

- node_name：该条日志对应业务操作的实际处理实例；在异步 poll/settle 时取当前执行节点，绝不取原任务提交节点。
- submit_node_name：可选的轻量来源字段，取快照或任务保存的提交节点，便于无需读取正文就展示两种身份；绝不用于覆盖 node_name。
- request_content：轻量对象，含 ref、status、truncated、captured_bytes；不含 payload。

仅将轻量元数据传给 RecordConsumeLogParams/RecordErrorLog，避免 model/log.go:375 的普通进程日志序列化泄露正文。详情写失败时仍可写业务日志，其轻量状态为 storage_failed；错误输出不含原始 payload。log 写失败产生的孤立正文由清理任务回收，不能以为关系库/ClickHouse 总有共同事务。

关系库 ref 唯一可幂等插入；ClickHouse 用 MergeTree（按日期分区、ORDER BY (created_at, ref)）并以 ref + request_id + user_id 读取不可变快照，兼容故障重试造成的重复提交，不假设唯一索引或事务。不为每条关联日志复制一次大正文或增加正文版本。写入和读取都沿用实际日志库配置；migrateLOGDB 中的 ClickHouse 分支显式创建详情表，清理遵循第 6 节，不给正文设置独立的创建时间 TTL。

## 4. 读取 API 与权限

新增 GET /api/log/request/:ref，注册顺序避免被通配路由吞掉。使用 DisableCache + AdminAuth，并加入 middleware/access_token_routes.go 的 log:read 规则；管理员个人访问令牌沿用现有日志权限。正文读取不新增允许普通模型令牌的路径。

服务端按 ref 读取详情，再核实实际日志中存在匹配 request_id + user_id 的关联 consume/error 行；不能仅凭 ref 返回孤立正文，也不能按前端页内 id 查库。API 响应：ref、request_id、submit_node_name、status、truncated、captured_bytes、payload；该 API 不伪造某一条关联日志的处理节点，详情的处理节点直接来自用户所打开日志的 admin_info.node_name。格式错误/无权限/不存在不回显正文。返回 Cache-Control: no-store。

普通用户 self/token 列表通过现有 other 裁剪拿不到 admin_info.request_content；现有列表、统计、export 不包含正文。admin/root 的节点可见；旧任务 root_info.node_name 仅在原权限允许时作为历史提交节点回退，不扩大其原权限，也不当作实际处理节点。

## 5. 成功、失败与异步生命周期

正常成功和渠道尝试失败在统一日志辅助函数接入快照。补一个鉴权后请求最终错误记录器：跟踪安全的结构化失败分类/状态，在参数校验、分发失败、无可用渠道、模型限流等最终拒绝后写一次通用错误日志。已有最终错误记录时设置标记防重；单次上游重试错误依然保留其尝试日志，不错误标记整个请求已最终失败。

ERROR_LOG_ENABLED 和既有 IsRecordErrorLog 排除继续生效，不为显式跳过的失败重新强制写。对于 JSON 无效、超大、未读取正文的请求，允许错误日志保存 node/status 而无正文，明确 unavailable/invalid。鉴权之前的失败不在此范围。

异步任务提交阶段只把已保存 ref、原始 request_id、submit_node_name 和轻量采集状态放入宿主 Task.PrivateData；现有日志开关允许当前或后续任务日志引用正文时，在提交完成前持久化快照。两类日志都关闭时不因任务提交绕过开关存正文；任务仍保存提交节点及准确采集状态。轮询、退款、后续结算重用正文 ref、请求 ID 和提交来源，不重读已释放 Gin context；各操作产生的日志仍独立取当次执行实例作为 node_name。兼容读取旧 TaskPrivateData.NodeName 时只赋予提交节点语义，不回填为新日志的处理节点。

Responses WebSocket 每个内部 response.create runner 构造各自的原始选定字段快照和 ref；只捕获 HTTP 握手不满足需求。Realtime 只覆盖其能提供的初始文本元数据并显示 unsupported/no_content；不声称记录完整连续会话。

## 6. 清理和迁移

正文不新增独立保留天数；有任一关联消费/错误日志或活动任务时保留，没有引用后回收。关联按稳定 request_id + user_id 及服务端 ref 核实，利用现有 logs.request_id 索引批量查询，不依赖跨方言 JSON 运算或页内显示 id。原始 created_at 保持不变，不将它改写成最后日志时间。

DeleteOldLogBatch 在删除日志后触发有界孤立正文回收；另由既有后台任务机制定期补偿，覆盖 ClickHouse 自身 TTL、写正文成功但写日志失败、人工清理中断等场景。分批选择候选，复核实际关联日志与活动任务后才删除；同一个请求的部分重试日志被清理，不影响剩余日志读取。活动任务引用从持久化任务状态读取；主库或日志库检查失败时推迟删除，不能把查询失败视为没有引用。

ClickHouse 的 logs 继续使用原有 LOG_SQL_CLICKHOUSE_TTL_DAYS，详情表不设置不安全的创建时间 TTL。日志 TTL 实际移除最后引用后，补偿任务用有界同步 mutation 批量回收对应正文。正文物理删除允许存在一个清理调度间隔的延迟，读取 API 已无关联日志时立即拒绝返回正文。这样不复制大正文版本，也不会因为旧版本 TTL 提前删除共享内容。

写入与回收采用统一的轻量主库保护行，避免日志库类型改变协调语义。候选表 log_request_content_protections 只存 ref 主键、request_id/user_id、holder_id、generation、lease_until、phase/updated_at 等协调元数据，绝不存正文；放在主库 DB，所以日志库是独立关系库或 ClickHouse 时仍可用。holder_id 标识本次持有者，generation 用于拒绝旧持有者续租/释放；不使用 Redis 正确性假设。

建议流程：

1. 首次需要持久化正文前创建保护行并注册当前入站请求的有界可续租 writer 引用；重试期间续租，最终日志写完后释放。异步提交在同一主库事务内保存 TaskPrivateData 的 ref 并移交/释放入站 writer 引用，未终结/未完成结算的任务不会只因入站租约过期而失去保护。崩溃后的租约可过期，活动任务引用仍需实际复核。
2. 正文/日志/任务关联写入和清理，在短事务内竞争同一主库保护行。MySQL/PostgreSQL 使用 lockForUpdate(tx)；SQLite 在同一事务内用带 holder_id/generation 条件的原子 UPDATE 获得写锁和检查 RowsAffected，不能误以为跳过 FOR UPDATE 就仍有行锁。主库与日志库实际同库时，正文/日志直接使用同一个事务连接，不能持有 SQLite 写锁又从另一个连接写同一文件。独立日志库的跨库操作先提交带操作标识的 write_pending/delete_pending 状态，再重新取得该行保护执行有界 I/O；其它持有者看见 pending 只能等待恢复，不能仅因租约过期接管删除。长推理或流式输出依靠可续租引用，不长期占数据库事务。
3. 清理器获得保护后复核 writer 租约、关联日志和持久化活动任务；任一存在即跳过。无引用才按上述 pending 协议执行对应日志库的同步删除，确认完成后以同代条件标记完成。恢复中的写入者必须重新取得保护，若旧正文已被删除则从自己持有的不可变快照重新幂等保存后再关联，不能凭一次过期的本地“已保存”标记继续写。
4. 该行是跨库互斥/恢复协议，并非跨库原子事务。日志库操作超时且是否落地不确定时保留 write_pending/delete_pending/uncertain 状态及操作标识，禁止仅凭 lease_until 到期就把不确定操作当作完成；后台协调器核查对应操作及现存引用后重试或收敛。无法确认时延后物理清理并报告可诊断错误，不影响推理/计费，也不冒称正文已可靠保存。所有结果标记使用 holder_id + generation 条件更新，过期持有者不能覆盖新状态。

此机制增加轻量主库协调开销，实施须测量每条记录的写次数及故障路径；可以批量续租，但不能退化为单进程锁。真实数据库门禁覆盖同库/独立库的写入删除交错、SQLite 并发、租约过期与续租、节点崩溃、ClickHouse 同步 mutation 超时后的恢复。正常路径与可恢复故障均通过后才可交付，当前只给出实施候选，没有宣称跨库原子性或已通过验证。

迁移无历史回填：旧日志显示未记录。真实 SQLite/MySQL/PostgreSQL 验证新库和当前已发布结构升级，每次启动/迁移至少两次。ClickHouse 新表、日志 TTL 后回收、重复故障写入、非事务失败与同步清理必须真实验证。

## 7. 前端复用

沿用 details-dialog.tsx 和 log-detail-layout 的 DetailSection/DetailRow。打开且 admin/root 且有 ref 时用 TanStack Query 获取详情；关闭时不主动读取或写入持久化缓存。请求内容区域按 role/字段分组，结构复杂内容用已有 CodeBlock；复制用现有 CopyButton；加载/失败用 LoadingState/ErrorState。现有 PromptDialog 属于绘图日志，不复制为另一套弹窗。

新增“处理节点”列到 common-logs-columns.tsx，并在移动卡片/紧凑行展示，取 other.admin_info.node_name；无值显示未记录。详情分别展示“处理节点”和“提交节点”：后者取快照/轻量 submit_node_name，旧任务 root_info.node_name 仅按现有 root 权限作为历史提交来源回退，不用于填补处理节点。

重用共享 CodeBlock 的折叠、滚动能力，避免新建自制 JSON 查看器；不会执行或 HTML 渲染正文。格式化字节数/时间通过现有 format 和 toIntlLocale。新文案必须执行 i18n skill 并同步七个 locale。新增详情 API 读取不让整个列表预加载所有正文。

## 8. 安全和校验边界

实施前按 AGENTS 要求读取适用 OWASP ASVS 当前稳定版、Authentication/Session Management/Authorization/Logging 指南，确定已有身份、PAT 范围、对象关联和缓存控制的回归用例，不宣称无证据的合规。这里只计划服务端权限，不修改登录/会话方案。

## 9. 影响文件与回滚

预期根模块文件包括 common/body_storage.go 或原始 storage 入口、constant context keys、relay/router 入口、日志生成/错误边界、model 新详情及主库保护模型、model/main.go、model/log.go 清理、controller/log.go、router/api-router.go、middleware/access_token_routes.go、TaskPrivateData 及 WebSocket runner。前端仅 usage-logs 类型/API/详情/节点列表和七语文案；必须说明每个新增文件实际能力缺口。

旧版本忽略 other 的新诊断字段；新增详情表不破坏旧日志。关闭正文开关可止新增内容，节点继续记录；保留迁移出的表以允许降级读旧摘要。不删除新正文表或改旧账单，回滚先停新写入再切版本。与 Key 工作共享 relay/日志文件由父任务串行整合。所有检查未执行，不能声明完成。
