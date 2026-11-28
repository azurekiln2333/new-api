package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// PrepareRelayUsageReport freezes one reporting occurrence, not one request.
// Call only after the final reporting values are known. Negative cache/input
// arguments denote unavailable measures, not a verified zero. Retain the returned
// report for confirmation and optional logging even when persistence fails.
func PrepareRelayUsageReport(c *gin.Context, info *relaycommon.RelayInfo, source model.UsageEventSourceKind, modelName, tokenName string, prompt, completion, cacheRead, cacheWrite int, semantic string, reportedInput, quota int) *model.UsageSourceReport {
	if info == nil {
		return nil
	}
	input := model.UsageEventInput{
		UserID:           int64(info.UserId),
		TokenID:          int64(info.TokenId),
		ModelName:        modelName,
		TokenName:        tokenName,
		GroupName:        info.UsingGroup,
		WriterNode:       common.NodeName,
		ExportNode:       common.NodeName,
		PromptTokens:     int64(prompt),
		CompletionTokens: int64(completion),
		QuotaMagnitude:   int64(quota),
		KnownMask: model.UsageEventKnownUserID | model.UsageEventKnownTokenID |
			model.UsageEventKnownModelName | model.UsageEventKnownTokenName |
			model.UsageEventKnownGroupName | model.UsageEventKnownWriterNode |
			model.UsageEventKnownExportNode | model.UsageEventKnownPromptTokens |
			model.UsageEventKnownCompletionTokens | model.UsageEventKnownQuota,
	}
	if info.ChannelMeta != nil {
		input.ChannelID = int64(info.ChannelId)
		input.KnownMask |= model.UsageEventKnownChannelID
	}
	if c != nil {
		// These are the exact historical strings consumed by RecordConsumeLog;
		// an absent Gin value is a known empty reporting dimension.
		input.Username = c.GetString("username")
		input.RequestID = c.GetString(common.RequestIdKey)
		if input.RequestID == "" {
			// The optional log writer otherwise allocates this fallback later.
			// Freeze it once so linkage does not depend on which sink runs first.
			input.RequestID = common.NewRequestId()
			c.Set(common.RequestIdKey, input.RequestID)
		}
		input.UpstreamRequestID = c.GetString(common.UpstreamRequestIdKey)
		input.KnownMask |= model.UsageEventKnownUsername | model.UsageEventKnownRequestID | model.UsageEventKnownUpstreamRequestID
	}
	if cacheRead >= 0 {
		input.CacheReadTokens = int64(cacheRead)
		input.KnownMask |= model.UsageEventKnownCacheReadTokens
	}
	if cacheWrite >= 0 {
		input.CacheWriteTokens = int64(cacheWrite)
		input.KnownMask |= model.UsageEventKnownCacheWriteTokens
	}
	if reportedInput >= 0 {
		input.ReportedInputTokens = int64(reportedInput)
		input.KnownMask |= model.UsageEventKnownReportedInputTokens
	}
	switch semantic {
	case "":
		input.UsageSemantic = model.UsageSemanticUnknown
	case "openai":
		input.UsageSemantic = model.UsageSemanticOpenAI
	case "anthropic":
		input.UsageSemantic = model.UsageSemanticAnthropic
	default:
		input.UsageSemantic = model.UsageSemanticOtherExplicit
	}
	if semantic != "" {
		input.KnownMask |= model.UsageEventKnownUsageSemantic
	}
	return prepareUsageSourceReport(relayUsageReportContext(c), source, model.UsageEventKindConsume, input)
}

// PrepareTaskSubmissionUsageReport allocates the reporting occurrence before
// insertion/settlement. The task insert binds the key in the primary transaction;
// a streamed response with a failed insert retains this exact same occurrence.
func PrepareTaskSubmissionUsageReport(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) {
	if c == nil || info == nil || task == nil || task.UsageReport != nil {
		return
	}
	if task.PrivateData.UsageSubmissionKey != "" {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(relayUsageReportContext(c)), 5*time.Second)
		defer cancel()
		report, err := model.LoadUsageSourceReport(ctx, task.PrivateData.UsageSubmissionKey)
		if err != nil || report.Input.Identity.SourceKind != model.UsageEventSourceTaskSubmission ||
			report.Input.Kind != model.UsageEventKindConsume || report.Input.UserID != int64(task.UserId) ||
			report.Input.ChannelID != int64(task.ChannelId) || report.Input.TokenID != int64(task.PrivateData.TokenId) ||
			report.Input.QuotaMagnitude != int64(task.Quota) {
			logger.LogError(ctx, common.LogText("usage statistics persistence failed: stage=%s", "task_submission_restore"))
			return
		}
		// Loading a key preserves the occurrence only. The existing source
		// owner must still confirm its observed outcome; recovery never does.
		task.UsageReport = report
		return
	}
	task.UsageReport = PrepareRelayUsageReport(c, info, model.UsageEventSourceTaskSubmission,
		info.OriginModelName, c.GetString("token_name"), 0, 0, 0, 0, "openai", -1, task.Quota)
}

// prepareTaskAdjustmentUsage freezes historical reporting dimensions before
// money changes. It reads names only, never credentials, request bodies or Other.
func prepareTaskAdjustmentUsage(ctx context.Context, task *model.Task, total int, refund bool) (*model.TaskUsageTransition, error) {
	delta := total - task.Quota
	quota := delta
	if quota < 0 {
		quota = -quota
	}
	node := task.PrivateData.NodeName
	if node == "" {
		node = common.NodeName
	}
	input := model.UsageEventInput{
		UserID: int64(task.UserId), TokenID: int64(task.PrivateData.TokenId), ChannelID: int64(task.ChannelId),
		ModelName: taskModelName(task), GroupName: task.Group, WriterNode: common.NodeName, ExportNode: node,
		QuotaMagnitude: int64(quota), UsageSemantic: model.UsageSemanticOpenAI,
		KnownMask: model.UsageEventKnownUserID | model.UsageEventKnownTokenID | model.UsageEventKnownChannelID |
			model.UsageEventKnownModelName | model.UsageEventKnownGroupName | model.UsageEventKnownWriterNode |
			model.UsageEventKnownExportNode | model.UsageEventKnownPromptTokens | model.UsageEventKnownCompletionTokens |
			model.UsageEventKnownCacheReadTokens | model.UsageEventKnownCacheWriteTokens | model.UsageEventKnownQuota |
			model.UsageEventKnownUsageSemantic | model.UsageEventKnownRequestID | model.UsageEventKnownUpstreamRequestID,
	}
	if task.PrivateData.RequestContent != nil {
		input.RequestID = task.PrivateData.RequestContent.RequestID
	} else if task.PrivateData.Execution != nil {
		input.RequestID = task.PrivateData.Execution.RequestID
	}
	if input.RequestID == "" {
		input.RequestID = common.NewRequestId()
	}
	lookup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var user model.User
	if err := model.DB.WithContext(lookup).Select("username").First(&user, task.UserId).Error; err == nil {
		input.Username = user.Username
		input.KnownMask |= model.UsageEventKnownUsername
	}
	if task.PrivateData.TokenId == 0 {
		input.KnownMask |= model.UsageEventKnownTokenName
	} else {
		var token model.Token
		if err := model.DB.WithContext(lookup).Select("name").First(&token, task.PrivateData.TokenId).Error; err == nil {
			input.TokenName = token.Name
			input.KnownMask |= model.UsageEventKnownTokenName
		}
	}
	return model.NewTaskUsageTransition(task, total, refund, input)
}

// claimTaskAdjustmentUsage reuses a CAS-bound obligation or claims a direct
// operation. Ledger operations prepare inside their existing primary transaction.
// A storage outage before Prepare cannot justify replaying money or inventing
// usage: preserve the original source path once and expose its coverage gap.
func claimTaskAdjustmentUsage(ctx context.Context, task *model.Task, total int, refund bool) error {
	if existing := task.UsageTransition; existing != nil {
		if existing.TargetQuota != total || existing.PreviousQuota != task.Quota || existing.Refund != refund {
			return model.ErrUsageEventStateConflict
		}
		return nil
	}
	transition, err := prepareTaskAdjustmentUsage(ctx, task, total, refund)
	if err != nil {
		logger.LogError(ctx, common.LogText("usage statistics persistence failed: stage=%s", "task_adjustment_freeze"))
		return nil
	}
	if task.PrivateData.BudgetReservationId == 0 {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = model.PrepareTaskUsageTransition(persist, task, transition)
		if err != nil {
			if errors.Is(err, model.ErrUsageEventNotReady) || errors.Is(err, model.ErrUsageEventStateConflict) ||
				errors.Is(err, model.ErrUsageEventPayloadConflict) || errors.Is(err, model.ErrUsageEventIdentityConflict) {
				return err
			}
			logger.LogError(ctx, common.LogText("usage statistics persistence failed: stage=%s", "task_adjustment_prepare"))
			return nil
		}
	}
	task.UsageTransition = transition
	return nil
}

// ConfirmRelayUsageReport is called only by the source owner that observed the
// reporting outcome. Recovery must never use it to infer a monetary outcome.
func ConfirmRelayUsageReport(c *gin.Context, report *model.UsageSourceReport, state model.UsageEventSourceState, evidence string) {
	confirmUsageSourceReport(relayUsageReportContext(c), report, state, evidence)
}

func relayUsageReportContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func prepareUsageSourceReport(parent context.Context, source model.UsageEventSourceKind, kind model.UsageEventKind, input model.UsageEventInput) *model.UsageSourceReport {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	report, err := model.NewUsageSourceReport(ctx, source, kind, input)
	if err != nil {
		logger.LogError(ctx, common.LogText("usage statistics persistence failed: stage=%s", "freeze"))
		return nil
	}
	// An ambiguous acknowledgement retries only the frozen numeric occurrence.
	// It must not allocate another UID or replay any funding/counter operation.
	for range 2 {
		if err = model.PrepareUsageSourceReport(ctx, report); err == nil {
			return report
		}
		if ctx.Err() != nil {
			break
		}
	}
	logger.LogError(ctx, common.LogText("usage statistics persistence failed: stage=%s event=%s", "prepare", report.Input.EventKey))
	return report
}

func confirmUsageSourceReport(parent context.Context, report *model.UsageSourceReport, state model.UsageEventSourceState, evidence string) {
	if report == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	for range 2 {
		if report.Receipt.EventKey == "" {
			if err := model.PrepareUsageSourceReport(ctx, report); err != nil {
				if ctx.Err() != nil {
					break
				}
				continue
			}
		}
		if err := model.ConfirmUsageSourceReport(ctx, report, state, evidence); err == nil {
			return
		} else if errors.Is(err, model.ErrUsageEventNotFound) {
			// A task transaction may have rolled back after assigning its
			// transient receipt. Retry only this frozen occurrence, not money.
			report.Receipt = model.UsageEventReceipt{}
		}
		if ctx.Err() != nil {
			break
		}
	}
	// Do not print driver errors, historical dimensions or arbitrary evidence.
	logger.LogError(ctx, common.LogText("usage statistics persistence failed: stage=%s event=%s", "confirm", report.Input.EventKey))
}
