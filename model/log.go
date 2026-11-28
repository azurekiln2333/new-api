package model

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
)

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

// LogMatchMode selects how the usage-log text filters compare stored values.
type LogMatchMode string

const (
	// LogMatchFuzzy keeps rows whose text column contains the filter value.
	LogMatchFuzzy LogMatchMode = "fuzzy"
	// LogMatchExact compares text columns for equality, which is how the log
	// search behaved before the match mode existed.
	LogMatchExact LogMatchMode = "exact"
)

// buildLogContainsCondition builds a literal substring match for one column.
// LIKE metacharacters in the value are escaped so a search for "50%" finds that
// text instead of matching everything.
func buildLogContainsCondition(column string, value string) (string, string) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse LIKE escapes with a backslash and has no ESCAPE clause.
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `%`, `\%`)
		value = strings.ReplaceAll(value, `_`, `\_`)
		return column + " LIKE ?", "%" + value + "%"
	}
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	value = strings.ReplaceAll(value, "_", "!_")
	return column + " LIKE ? ESCAPE '!'", "%" + value + "%"
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

type Log struct {
	Id                int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2"`
	UserId            int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	UseTime           int    `json:"use_time" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	ChannelId         int    `json:"channel" gorm:"index"`
	ChannelName       string `json:"channel_name" gorm:"->"`
	TokenId           int    `json:"token_id" gorm:"default:0;index"`
	Group             string `json:"group" gorm:"index"`
	Ip                string `json:"ip" gorm:"index;default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	Other             string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
	LogTypeLogin   = 7
)

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

var errRequestLogWrite = errors.New("request log write failed")

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

// createRequestLog keeps the body out of log parameters and lists. Diagnostic
// failure must not prevent the ordinary consume/error log from being written.
func createRequestLog(c *gin.Context, log *Log, other *LogOther) error {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = context.WithoutCancel(c.Request.Context())
	}
	return writeRequestLog(ctx, log, other, common.RequestContent(c))
}

// writeRequestLog adds the diagnostic node/content metadata and persists the
// log. The processing node is always the instance executing this operation; an
// async poll/settle log must never inherit the task submission node.
func writeRequestLog(ctx context.Context, log *Log, other *LogOther, snapshot *common.RequestContentSnapshot) error {
	if other == nil {
		other = NewLogOther()
	} else {
		// The relay also uses Other for plugin/report metadata. Do not add
		// internal detail links to that caller-owned value.
		copy := *other
		copy.adminInfo = copyLogOtherMap(other.adminInfo)
		other = &copy
	}
	other.SetAdmin("node_name", common.NodeName)
	ensureLogRequestId(log)
	if snapshot != nil && (snapshot.UserID != log.UserId || snapshot.RequestID != log.RequestId) {
		snapshot = nil
	}
	return WithLogRequestDetail(ctx, snapshot, func(logDB *gorm.DB, detailErr error) error {
		if snapshot != nil {
			if snapshot.SubmitNodeName != "" {
				other.SetAdmin("submit_node_name", snapshot.SubmitNodeName)
			}
			metadata := requestContentLogLink{RequestContentMetadata: common.RequestContentMetadata{
				Status: snapshot.Status, CapturedBytes: snapshot.CapturedBytes, Truncated: snapshot.Truncated,
			}}
			switch {
			case detailErr != nil:
				metadata.Status = common.RequestContentStatusStorageFail
			case requestContentShouldExist(snapshot):
				metadata.Ref = snapshot.Ref
				metadata.LinkID, _ = logDB.Statement.Context.Value(requestContentLinkKey{}).(string)
			}
			other.SetAdmin("request_content", metadata)
		}
		log.Other = other.JSONString()
		// Do not echo a parameterized driver's SQL or payload in process logs.
		if err := logDB.Create(log).Error; err != nil {
			return errRequestLogWrite
		}
		return nil
	})
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelName = ""
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityUser)
	}
	assignDisplayLogIds(logs, startIdx)
}

// FormatAdminLogs removes root-only diagnostics while retaining operational
// admin_info. Root callers must not pass their results through this formatter.
func FormatAdminLogs(logs []*Log) {
	for i := range logs {
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityAdmin)
	}
}

// FormatRootLogs normalizes legacy metadata into the current scoped shape
// without removing root-only diagnostics.
func FormatRootLogs(logs []*Log) {
	for i := range logs {
		logs[i].Other = formatLogOtherJSON(logs[i].Other, logOtherVisibilityRoot)
	}
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content *common.Message) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	other := NewLogOther()
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   other.setContent([]*common.Message{content}),
		Other:     other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog(common.LogText("failed to record log: %s", err.Error()))
	}
}

// RecordLogWithAdminInfo stores operator metadata under other.admin_info and
// an optional, user-visible operation descriptor under other.op for localization.
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo *AuditAdminInfo, operation *AuditOperation, request ...*gin.Context) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if logType == LogTypeManage {
		var c *gin.Context
		if len(request) > 0 {
			c = request[0]
		}
		actorRole := 0
		if c != nil {
			actorRole = c.GetInt("role")
		}
		RecordAuditLog(c, AuditLog{UserId: userId, Username: username, ActorRole: actorRole, Category: AuditCategoryOperation, Content: content, Other: AuditOther{AdminInfo: adminInfo, Op: operation}, Success: true})
		return
	}
	if len(request) > 0 && request[0] != nil {
		log.RequestId = request[0].GetString(common.RequestIdKey)
	}
	if adminInfo != nil || operation != nil {
		data, err := common.Marshal(AuditOther{AdminInfo: adminInfo, Op: operation})
		if err != nil {
			common.SysError(common.LogText("failed to encode log admin info: %s", err.Error()))
			return
		}
		log.Other = string(data)
	}
	if err := createLog(log); err != nil {
		common.SysLog(common.LogText("failed to record log: %s", err.Error()))
	}
}

// RecordLoginLog writes new login events to the independent audit table.
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出）；action+params 供前端本地化渲染。
// other 包含 login_method、user_agent 等结构化信息。
func RecordLoginLog(userId, actorRole int, username string, content string, ip string, action string, params map[string]any, other AuditOther, request ...*gin.Context) {
	other.Op = &AuditOperation{Action: action, Params: params}
	var c *gin.Context
	if len(request) > 0 {
		c = request[0]
	}
	RecordAuditLog(c, AuditLog{UserId: userId, Username: username, ActorRole: actorRole, Category: AuditCategoryLogin, Action: action, Content: content, Ip: ip, Other: other, Success: true})
}

// RecordOperationAuditLog writes new operation/security events to the audit table.
// logUserId 为日志归属者，管理审计日志应归属实际操作者；目标资源/用户放入
// action params。username 内部按 logUserId 查询。content 为英文兜底文本（供导出使用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId, actorRole int, content string, ip string, action string, params map[string]any, adminInfo *AuditAdminInfo, auditInfo *AuditRequestInfo, request ...*gin.Context) {
	username, _ := GetUsernameById(logUserId, false)
	other := AuditOther{
		Op:        &AuditOperation{Action: action, Params: params},
		AdminInfo: adminInfo,
		AuditInfo: auditInfo,
	}
	var c *gin.Context
	if len(request) > 0 {
		c = request[0]
	}
	category := AuditCategoryOperation
	if adminInfo == nil {
		category = AuditCategorySecurity
	}
	status, success := 200, true
	if auditInfo != nil {
		status = auditInfo.Status
		success = auditInfo.Success
	}
	RecordAuditLog(c, AuditLog{UserId: logUserId, Username: username, ActorRole: actorRole, Category: category, Action: action, Content: content, Ip: ip, Status: status, Success: success, Other: other})
}

func RecordTopupLog(userId int, content *common.Message, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	other := NewLogOther()
	other.MergeAdmin(map[string]any{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	})
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   other.setContent([]*common.Message{content}),
		Ip:        callerIp,
		Other:     other.JSONString(),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog(common.LogText("failed to record topup log: %s", err.Error()))
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other *LogOther) {
	logger.LogInfo(c, common.LogText("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := other.JSONString()
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createRequestLog(c, log, other)
	if err != nil {
		logger.LogError(c, common.LogText("failed to record log: %s", err.Error()))
	}
}

type RecordConsumeLogParams struct {
	UsageReport      *UsageSourceReport `json:"-"`
	ChannelId        int                `json:"channel_id"`
	PromptTokens     int                `json:"prompt_tokens"`
	CompletionTokens int                `json:"completion_tokens"`
	ModelName        string             `json:"model_name"`
	TokenName        string             `json:"token_name"`
	Quota            int                `json:"quota"`
	Content          []*common.Message  `json:"content"`
	TokenId          int                `json:"token_id"`
	UseTimeSeconds   int                `json:"use_time_seconds"`
	IsStream         bool               `json:"is_stream"`
	Group            string             `json:"group"`
	Other            *LogOther          `json:"other"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	// Request plugin report hooks read what the request settled, whether or
	// not consume logs are kept.
	common.SetContextKey(c, constant.ContextKeyConsumeLog, &params)
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, common.LogText("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	createdAt := common.GetTimestamp()
	other := params.Other
	content := other.setContent(params.Content)
	otherStr := other.JSONString()
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		Content:          content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	if params.UsageReport != nil {
		bound, bindErr := bindUsageSourceReport(log, other, params.UsageReport)
		if bindErr != nil {
			logger.LogError(c, common.LogText("usage statistics persistence failed: stage=%s", "consume_log_link"))
		} else {
			other = bound
			createdAt, username = log.CreatedAt, log.Username
		}
	}
	err := createRequestLog(c, log, other)
	if err != nil {
		logger.LogError(c, common.LogText("failed to record log: %s", err.Error()))
	}
	// Keep the legacy writer active until an explicit lineage fence is
	// installed. This preserves the old dashboard during rolling rollout while
	// the durable fact is captured independently. After the fence, fail closed
	// if its state cannot be read; adding an unbounded legacy row then could
	// overlap the attested baseline.
	legacyWriterAllowed := false
	if common.DataExportEnabled {
		var fenceErr error
		legacyWriterAllowed, fenceErr = LegacyQuotaDataWriterAllowed(context.Background())
		if fenceErr != nil {
			logger.LogError(c, common.LogText("usage statistics persistence failed: stage=%s", "legacy_writer_fence"))
		}
	}
	if legacyWriterAllowed && params.UsageReport != nil {
		if err := mirrorUsageLegacyReport(context.Background(), params.UsageReport); err != nil {
			logger.LogError(c, common.LogText("usage statistics persistence failed: stage=%s", "legacy_ownership"))
		}
		return
	}
	if legacyWriterAllowed {
		var cacheReadTokens, cacheCreationTokens int
		tokenUsed := params.PromptTokens + params.CompletionTokens
		if other != nil {
			cacheReadTokens, _ = other.public["cache_tokens"].(int)
			// The settlement path already normalizes the cache creation total.
			// Do not add the 5m/1h breakdown again or infer tokens from prices.
			cacheCreationTokens, _ = other.public["cache_write_tokens"].(int)
			// Anthropic prompt tokens exclude both cache categories. OpenAI
			// prompt tokens already include them, including converted responses.
			// Use settlement semantics, never the model name or request endpoint.
			if other.public["usage_semantic"] == "anthropic" {
				tokenUsed += max(0, cacheReadTokens) + max(0, cacheCreationTokens)
			}
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:              userId,
			Username:            username,
			ModelName:           params.ModelName,
			Quota:               params.Quota,
			CreatedAt:           createdAt,
			TokenUsed:           tokenUsed,
			CacheReadTokens:     int64(cacheReadTokens),
			CacheCreationTokens: int64(cacheCreationTokens),
			UseGroup:            params.Group,
			TokenID:             params.TokenId,
			ChannelID:           params.ChannelId,
			NodeName:            common.NodeName,
		})
	}
}

type RecordTaskBillingLogParams struct {
	UsageReport    *UsageSourceReport
	UserId         int
	LogType        int
	Content        []*common.Message
	ChannelId      int
	ModelName      string
	Quota          int
	TokenId        int
	Group          string
	Other          *LogOther
	NodeName       string // 任务发起节点；为空时回退当前节点
	RequestID      string
	RequestContent *TaskRequestContent
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	other := params.Other
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: createdAt,
		Type:      params.LogType,
		Content:   other.setContent(params.Content),
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		RequestId: params.RequestID,
		Other:     other.JSONString(),
	}
	if params.UsageReport != nil {
		bound, bindErr := bindUsageSourceReport(log, other, params.UsageReport)
		if bindErr != nil {
			common.SysError(common.LogText("usage statistics persistence failed: stage=%s", "task_log_link"))
		} else {
			other = bound
			createdAt, username = log.CreatedAt, log.Username
		}
	}
	snapshot := params.RequestContent.Snapshot(params.UserId)
	if log.RequestId == "" && snapshot != nil {
		log.RequestId = snapshot.RequestID
	}
	err := writeRequestLog(context.Background(), log, other, snapshot)
	if err != nil {
		common.SysLog(common.LogText("failed to record task billing log: %s", err.Error()))
	}
	legacyWriterAllowed := false
	if params.LogType == LogTypeConsume && common.DataExportEnabled {
		var fenceErr error
		legacyWriterAllowed, fenceErr = LegacyQuotaDataWriterAllowed(context.Background())
		if fenceErr != nil {
			common.SysError(common.LogText("usage statistics persistence failed: stage=%s", "legacy_writer_fence"))
		}
	}
	if legacyWriterAllowed && params.UsageReport != nil {
		if err := mirrorUsageLegacyReport(context.Background(), params.UsageReport); err != nil {
			common.SysError(common.LogText("usage statistics persistence failed: stage=%s", "legacy_ownership"))
		}
		return
	}
	if legacyWriterAllowed {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		})
	}
}

// LogUserScope chooses which users' rows are visible in an administrator log view.
type LogUserScope string

const (
	LogUserScopeAll    LogUserScope = "all"
	LogUserScopeSelf   LogUserScope = "self"
	LogUserScopeOthers LogUserScope = "others"
)

var errInvalidLogUserScope = errors.New("invalid usage log user scope")

// LogFilter is the usage-log search shared by the log list, the log export and
// the log statistics. Username and Channel are administrator filters. UserId
// scopes a self endpoint to its authenticated owner. UserScope/ScopeUserId are
// only populated by an administrator handler from that authenticated context.
type LogFilter struct {
	UserId            int
	UserScope         LogUserScope
	ScopeUserId       int
	Type              int
	StartTimestamp    int64
	EndTimestamp      int64
	ModelName         string
	Username          string
	TokenName         string
	Channel           int
	Group             string
	RequestId         string
	UpstreamRequestId string
	MatchMode         LogMatchMode
}

// logTextFilter is one text column the search filters on. legacyPattern keeps
// the historical exact-mode rule where a % in the model or username field is a
// wildcard pattern instead of literal text.
type logTextFilter struct {
	column        string
	value         string
	legacyPattern bool
}

func (f LogFilter) textFilters() []logTextFilter {
	return []logTextFilter{
		{column: "logs.model_name", value: f.ModelName, legacyPattern: true},
		{column: "logs.username", value: f.Username, legacyPattern: true},
		{column: "logs.token_name", value: f.TokenName},
		{column: "logs.request_id", value: f.RequestId},
		{column: "logs.upstream_request_id", value: f.UpstreamRequestId},
		{column: "logs." + logGroupCol, value: f.Group},
	}
}

func (f LogFilter) apply(tx *gorm.DB) (*gorm.DB, error) {
	var err error
	if f.UserScope != "" {
		switch f.UserScope {
		case LogUserScopeAll:
		case LogUserScopeSelf, LogUserScopeOthers:
			if f.ScopeUserId <= 0 {
				return nil, errInvalidLogUserScope
			}
		default:
			return nil, errInvalidLogUserScope
		}
	} else if f.ScopeUserId != 0 {
		return nil, errInvalidLogUserScope
	}
	if f.UserId != 0 {
		tx = tx.Where("logs.user_id = ?", f.UserId)
	}
	if f.ScopeUserId > 0 {
		switch f.UserScope {
		case LogUserScopeSelf:
			tx = tx.Where("logs.user_id = ?", f.ScopeUserId)
		case LogUserScopeOthers:
			tx = tx.Where("logs.user_id <> ?", f.ScopeUserId)
		}
	}
	if f.Type != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", f.Type)
	}
	for _, textFilter := range f.textFilters() {
		if textFilter.value == "" {
			continue
		}
		if f.MatchMode != LogMatchExact {
			condition, pattern := buildLogContainsCondition(textFilter.column, textFilter.value)
			tx = tx.Where(condition, pattern)
			continue
		}
		if textFilter.legacyPattern {
			if tx, err = applyExplicitLogTextFilter(tx, textFilter.column, textFilter.value); err != nil {
				return nil, err
			}
			continue
		}
		tx = tx.Where(textFilter.column+" = ?", textFilter.value)
	}
	if f.StartTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", f.StartTimestamp)
	}
	if f.EndTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", f.EndTimestamp)
	}
	if f.Channel != 0 {
		tx = tx.Where("logs.channel_id = ?", f.Channel)
	}
	return tx, nil
}

func GetAllLogs(filter LogFilter, startIdx int, num int) (logs []*Log, total int64, err error) {
	tx, err := filter.apply(LOG_DB)
	if err != nil {
		return nil, 0, err
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

func GetUserLogs(userId int, filter LogFilter, startIdx int, num int) (logs []*Log, total int64, err error) {
	filter.UserId = userId
	tx, err := filter.apply(LOG_DB)
	if err != nil {
		return nil, 0, err
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError(common.LogText("failed to count user logs: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to query logs")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError(common.LogText("failed to search user logs: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to query logs")
	}

	formatUserLogs(logs, startIdx)
	return logs, total, err
}

// StreamAllLogs calls fn with each log matching filter, newest first, as an
// administrator with role sees it. Rows are read one at a time, so an export
// never holds the whole result in memory.
func StreamAllLogs(ctx context.Context, filter LogFilter, role int, fn func(*Log) error) error {
	tx, err := filter.apply(LOG_DB.WithContext(ctx))
	if err != nil {
		return err
	}
	visibility := logOtherVisibilityAdmin
	if role >= common.RoleRootUser {
		visibility = logOtherVisibilityRoot
	}
	channelNames := make(map[int]string)
	return streamLogs(tx, "logs.created_at desc, logs.id desc", func(log *Log) error {
		if log.ChannelId != 0 {
			name, ok := channelNames[log.ChannelId]
			if !ok {
				if channel, err := CacheGetChannel(log.ChannelId); err == nil {
					name = channel.Name
				}
				channelNames[log.ChannelId] = name
			}
			log.ChannelName = name
		}
		log.Other = formatLogOtherJSON(log.Other, visibility)
		return fn(log)
	})
}

// StreamUserLogs is StreamAllLogs for one user's own logs, shaped like the
// user log list.
func StreamUserLogs(ctx context.Context, userId int, filter LogFilter, fn func(*Log) error) error {
	filter.UserId = userId
	tx, err := filter.apply(LOG_DB.WithContext(ctx))
	if err != nil {
		return err
	}
	count := 0
	return streamLogs(tx, "logs.id desc", func(log *Log) error {
		count++
		log.Id = count
		log.ChannelName = ""
		log.Other = formatLogOtherJSON(log.Other, logOtherVisibilityUser)
		return fn(log)
	})
}

func streamLogs(tx *gorm.DB, order string, fn func(*Log) error) error {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	rows, err := tx.Model(&Log{}).Order(order).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var log Log
		if err := LOG_DB.ScanRows(rows, &log); err != nil {
			return err
		}
		if err := fn(&log); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Stat is the usage-log summary behind the log header cards. CacheReadTokens
// and InputTokens feed the cache hit rate card; InputTokens is the total input
// under settlement semantics (see logCacheTokenSelect).
type Stat struct {
	Quota           int   `json:"quota"`
	Rpm             int   `json:"rpm"`
	Tpm             int   `json:"tpm"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	InputTokens     int64 `json:"input_tokens"`
}

// SumUsedQuota delegates business totals to durable primary statistics. Actual
// retained-row lists, counts and exports continue using LOG_DB and LogFilter.
func SumUsedQuota(filter LogFilter) (Stat, error) {
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return Stat{}, err
	}
	if !installed {
		legacy, legacyErr := sumUsedQuotaFromLogs(filter)
		// The three legacy queries must finish before checking the monotonic
		// marker again. Activation followed by cleanup may invalidate any of
		// their snapshots; retry the entire summary from durable facts then.
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return Stat{}, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	return SumUsageEventQuota(context.Background(), filter)
}

func sumUsedQuotaFromLogs(filter LogFilter) (stat Stat, err error) {
	filter.Type = LogTypeUnknown
	tx, err := filter.apply(LOG_DB.Table("logs").Select("COALESCE(sum(quota), 0) quota"))
	if err != nil {
		return stat, err
	}
	rpmTpmQuery, err := filter.apply(LOG_DB.Table("logs").Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm"))
	if err != nil {
		return stat, err
	}
	tx = tx.Where("logs.type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("logs.type = ?", LogTypeConsume)
	recent := common.GetTimestamp() - 60
	rpmTpmQuery = rpmTpmQuery.Where("logs.created_at >= ?", recent)
	if err := tx.Scan(&stat).Error; err != nil {
		return stat, common.NewMessage("Failed to query statistics")
	}
	var rateStat struct {
		Rpm int
		Tpm int
	}
	if err := rpmTpmQuery.Scan(&rateStat).Error; err != nil {
		return stat, common.NewMessage("Failed to query statistics")
	}
	stat.Rpm = rateStat.Rpm
	stat.Tpm = rateStat.Tpm
	cacheQuery, err := filter.apply(LOG_DB.Table("logs").Select(logCacheTokenSelect()))
	if err != nil {
		return stat, err
	}
	var cacheStat struct {
		CacheReadTokens int64
		InputTokens     int64
	}
	if err := cacheQuery.Where("logs.type = ?", LogTypeConsume).Scan(&cacheStat).Error; err == nil {
		stat.CacheReadTokens = cacheStat.CacheReadTokens
		stat.InputTokens = cacheStat.InputTokens
	}
	return stat, nil
}

// logCacheTokenSelect returns the aggregate behind the cache hit rate: the
// cache-read token total and the total input tokens under settlement
// semantics. Anthropic prompt tokens exclude both cache categories, so reads
// and writes are added back; OpenAI prompt tokens already include them,
// including converted responses. The rule matches the token accounting in
// RecordConsumeLog and never looks at the model name or the request endpoint.
//
// SQLite, MySQL and ClickHouse parse the other column with native JSON
// functions guarded so a non-JSON row counts as zero. PostgreSQL extracts the
// fields with a regular expression instead: casting legacy malformed text to
// json would fail the whole aggregate.
func logCacheTokenSelect() string {
	var (
		cacheRead  string
		cacheWrite string
		semantic   string
	)
	switch common.LogDatabaseType() {
	case common.DatabaseTypeMySQL:
		cacheRead = "CASE WHEN JSON_VALID(other) THEN GREATEST(0, COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(other, '$.cache_tokens')) AS SIGNED), 0)) ELSE 0 END"
		cacheWrite = "CASE WHEN JSON_VALID(other) THEN GREATEST(0, COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(other, '$.cache_write_tokens')) AS SIGNED), 0)) ELSE 0 END"
		semantic = "CASE WHEN JSON_VALID(other) THEN JSON_UNQUOTE(JSON_EXTRACT(other, '$.usage_semantic')) END"
	case common.DatabaseTypePostgreSQL:
		cacheRead = `GREATEST(0, COALESCE(substring(other from '"cache_tokens"[[:space:]]*:[[:space:]]*(-?[0-9]+)')::bigint, 0))`
		cacheWrite = `GREATEST(0, COALESCE(substring(other from '"cache_write_tokens"[[:space:]]*:[[:space:]]*(-?[0-9]+)')::bigint, 0))`
		semantic = `substring(other from '"usage_semantic"[[:space:]]*:[[:space:]]*"([a-z]+)"')`
	case common.DatabaseTypeClickHouse:
		cacheRead = "greatest(0, JSONExtractInt(other, 'cache_tokens'))"
		cacheWrite = "greatest(0, JSONExtractInt(other, 'cache_write_tokens'))"
		semantic = "JSONExtractString(other, 'usage_semantic')"
	default:
		cacheRead = "CASE WHEN json_valid(other) THEN MAX(0, COALESCE(CAST(json_extract(other, '$.cache_tokens') AS INTEGER), 0)) ELSE 0 END"
		cacheWrite = "CASE WHEN json_valid(other) THEN MAX(0, COALESCE(CAST(json_extract(other, '$.cache_write_tokens') AS INTEGER), 0)) ELSE 0 END"
		semantic = "CASE WHEN json_valid(other) THEN json_extract(other, '$.usage_semantic') END"
	}
	return "COALESCE(SUM(" + cacheRead + "), 0) AS cache_read_tokens, " +
		"COALESCE(SUM(prompt_tokens + CASE WHEN " + semantic + " = 'anthropic' THEN " + cacheRead + " + " + cacheWrite + " ELSE 0 END), 0) AS input_tokens"
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if ctx == nil || LOG_DB == nil || targetTimestamp <= 0 {
		return 0, ErrUsageEventInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}
	// Even a perfectly linked fact cannot replace a log while serving readers
	// still depend on that log. The immutable cutover marker is necessary in
	// addition to row-local ready/applied payload proof, never Complete history.
	installed, err := UsageReadLineageInstalled(ctx)
	if err != nil {
		return 0, err
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// A retained multiset checkpoint does not fence late writes while a
		// broad ClickHouse mutation runs. Keep application deletion closed
		// until that source-owner fence is implemented. Native TTL remains
		// a separate operator responsibility.
		return 0, ErrUsageStatisticsNotReady
	}

	// DELETE LIMIT is ignored by PostgreSQL. Materialize only one bounded page
	// of IDs first; this also avoids MySQL's LIMIT-in-IN-subquery restriction.
	var deleted int64
	err = LOG_DB.WithContext(ctx).Transaction(func(sourceTx *gorm.DB) error {
		if sourceTx.Dialector.Name() == "sqlite" {
			if err := sourceTx.Model(&Log{}).Where("1 = 0").UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
				return err
			}
		}
		var candidates []Log
		// Read numeric dimensions and transient metadata only; never Content.
		if err := lockForUpdateLogDB(sourceTx).Select("id", "user_id", "created_at", "type", "username", "token_name", "model_name", "quota", "prompt_tokens", "completion_tokens", "channel_id", "token_id", logGroupCol, "request_id", "upstream_request_id", "other").
			Where("created_at < ?", targetTimestamp).Order("created_at, id").Limit(min(limit, 100)).Find(&candidates).Error; err != nil {
			return err
		}
		proofDB := DB.WithContext(ctx)
		if LOG_DB == DB {
			proofDB = sourceTx
		}
		verifiedIDs := make([]int, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.Type == LogTypeConsume || candidate.Type == LogTypeRefund {
				if !installed {
					continue // serving legacy readers still require this numeric row
				}
				if err := verifyUsageLogDeletion(proofDB, candidate); err != nil {
					if errors.Is(err, ErrUsageStatisticsNotReady) {
						continue // leave this unproven row intact
					}
					return err
				}
			}
			verifiedIDs = append(verifiedIDs, candidate.Id)
		}
		if len(verifiedIDs) == 0 {
			if len(candidates) > 0 {
				return ErrUsageStatisticsNotReady
			}
			return nil
		}
		result := sourceTx.Where("id IN ? AND created_at < ?", verifiedIDs, targetTimestamp).Delete(&Log{})
		deleted = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return 0, err
	}
	if deleted > 0 {
		reclaimRequestContentAfterLogDeletion(ctx, targetTimestamp)
	}
	return deleted, nil
}

func reclaimRequestContentAfterLogDeletion(ctx context.Context, cutoff int64) {
	// Log deletion already committed. Budget one bounded pass for diagnostics;
	// periodic cleanup handles remaining candidates and independent log TTLs.
	ctx, cancel := context.WithTimeout(ctx, requestContentReadTimeout)
	defer cancel()
	if err := CleanupLogRequestDetails(ctx, cutoff, requestContentCleanupBatch); err != nil {
		common.SysError(common.LogText("failed to reclaim original request content: %s", err.Error()))
	}
}
