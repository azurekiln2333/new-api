package model

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// UsageLiveSourceNamespace and UsageLiveCollectionEpoch identify the live
// numeric codec. They are not deployment registration or a coverage attestation.
func UsageLiveSourceNamespace() string {
	return digestCanonical([]byte("new-api/live-usage-source/v1"))[:usageEventUIDLength]
}

func UsageLiveCollectionEpoch() string {
	return digestCanonical([]byte("new-api/live-usage-lineage/v1"))[:usageEventUIDLength]
}

// This immutable source protocol version means optional legacy dashboard writes
// carry exact per-occurrence ownership. It is not a capture-completeness claim.
const usageDashboardOwnershipVersion int64 = 1

// ValidateUsageLiveReadLineage checks membership against the codec actually
// emitted by live source callers. It does not validate historical manifests or
// attest capture completeness. Explicit foundation/import fixtures may retain
// their own namespaces; an installed production lineage must match this pair.
func ValidateUsageLiveReadLineage(lineage UsageReadLineage) error {
	if lineage.LiveSourceNamespace != UsageLiveSourceNamespace() || lineage.LiveCollectionEpoch != UsageLiveCollectionEpoch() {
		return ErrUsageEventStateConflict
	}
	return nil
}

// ValidateInstalledUsageLiveLineage rejects a conflicting installed lineage at
// startup on every writer. A missing manifest is not a reason to stop collecting
// live facts or to invent a fence: readers may expose only labelled partial
// live contributions. A database/invalid-manifest error is not absence.
func ValidateInstalledUsageLiveLineage(ctx context.Context) error {
	lineage, err := GetUsageReadLineage(ctx)
	if err != nil {
		return err
	}
	if err := ValidateUsageLiveReadLineage(lineage); err != nil {
		return err
	}
	if !lineage.IncludeLegacyQuotaData {
		return nil
	}
	// An older installed marker does not make unacknowledged dual writes
	// disjoint. Refuse serving an ambiguous activated store on restart too.
	var ambiguous int64
	if err := DB.WithContext(ctx).Model(&UsageEvent{}).
		Where("source_namespace = ? AND collection_epoch = ? AND (eligibility & ?) <> 0 AND activation_generation <> ?",
			lineage.LiveSourceNamespace, lineage.LiveCollectionEpoch, UsageEventProjectHours, usageDashboardOwnershipVersion).
		Count(&ambiguous).Error; err != nil {
		return err
	}
	if ambiguous != 0 {
		return ErrUsageStatisticsNotReady
	}
	return nil
}

// UsageSourceReport retains the same finalized occurrence through prepare,
// source confirmation and optional log delivery. It is not a finance receipt.
// Input and Receipt must not be edited by callers or shared between goroutines.
// The private snapshot rejects mutation rather than silently claiming a new fact.
type UsageSourceReport struct {
	Input   UsageEventInput   `json:"-"`
	Receipt UsageEventReceipt `json:"-"`
	frozen  UsageEventInput
	digest  string
}

// NewUsageSourceReport freezes an occurrence without database access. The live
// namespace/lineage identify this codec, not a registered deployment, writer
// fence, legacy source database or proof of historical coverage. Callers retain
// this object across ambiguous acknowledgements; request IDs are never identity.
func NewUsageSourceReport(ctx context.Context, source UsageEventSourceKind, kind UsageEventKind, input UsageEventInput) (*UsageSourceReport, error) {
	if ctx == nil || source < UsageEventSourceRelay || source > UsageEventSourceMidjourney {
		return nil, ErrUsageEventInvalid
	}
	if input.Identity.SourceKind != 0 && input.Identity.SourceKind != source {
		return nil, ErrUsageEventIdentityConflict
	}
	input.Identity.SourceKind = source
	if input.Identity.SourceNamespace == "" {
		input.Identity.SourceNamespace = UsageLiveSourceNamespace()
	}
	if input.Identity.OccurrenceUID == "" {
		uid, err := NewUsageOccurrenceUID()
		if err != nil {
			return nil, err
		}
		input.Identity.OccurrenceUID = uid
	}
	if input.CollectionEpoch == "" {
		input.CollectionEpoch = UsageLiveCollectionEpoch()
	}
	if input.ActivationGeneration == 0 {
		input.ActivationGeneration = usageDashboardOwnershipVersion
	}
	input.Kind = kind
	if input.OccurredAt == 0 {
		input.OccurredAt = common.GetTimestamp()
	}
	if kind == UsageEventKindConsume {
		input.Eligibility = UsageEventProjectHours
		if usageKnown(input.KnownMask, UsageEventKnownChannelID) && input.ChannelID > 0 {
			input.Eligibility |= UsageEventProjectChannel
		}
	} else {
		input.Eligibility = 0
	}
	finalized, _, digest, err := validateUsageEventInput(input, UsageEventSourcePending)
	if err != nil {
		return nil, err
	}
	return &UsageSourceReport{Input: finalized, frozen: finalized, digest: digest}, nil
}

func (report *UsageSourceReport) validate() error {
	if report == nil || report.Input != report.frozen {
		return ErrUsageEventPayloadConflict
	}
	_, _, digest, err := validateUsageEventInput(report.Input, UsageEventSourcePending)
	if err != nil {
		return err
	}
	if report.digest != digest {
		return ErrUsageEventPayloadConflict
	}
	if report.Receipt.EventKey != "" && report.Receipt != (UsageEventReceipt{
		EventKey: report.Input.EventKey, Identity: report.Input.Identity, PayloadDigest: digest,
	}) {
		return ErrUsageEventPayloadConflict
	}
	return nil
}

// LoadUsageSourceReport restores an existing numeric occurrence by a task's
// persisted key. It never creates an identity or infers a source disposition.
func LoadUsageSourceReport(ctx context.Context, key string) (*UsageSourceReport, error) {
	if ctx == nil || DB == nil || !isLowerHex(key, usageEventDigestLength) {
		return nil, ErrUsageEventInvalid
	}
	var event UsageEvent
	if err := DB.WithContext(ctx).Where("event_key = ?", key).Take(&event).Error; err != nil {
		return nil, err
	}
	if err := validateUsageEventRow(event); err != nil {
		return nil, err
	}
	input := usageEventInputFromRow(event)
	return &UsageSourceReport{Input: input, Receipt: usageEventReceipt(event), frozen: input,
		digest: normalizeFixedText(event.PayloadDigest)}, nil
}

// PrepareUsageSourceReport always prepares pending. Replaying a prepare cannot
// establish the source outcome, even if an earlier confirmation was ambiguous.
func PrepareUsageSourceReport(ctx context.Context, report *UsageSourceReport) error {
	if err := report.validate(); err != nil {
		return err
	}
	receipt, err := PrepareUsageEvent(ctx, report.Input, UsageEventSourcePending)
	if err != nil {
		return err
	}
	report.Receipt = receipt
	return nil
}

// ConfirmUsageSourceReport records the source owner's explicit outcome before
// projection. A failed Apply leaves a ready fact for bounded recovery. Neither
// this function nor recovery invokes funding, token or quota-counter operations.
func ConfirmUsageSourceReport(ctx context.Context, report *UsageSourceReport, state UsageEventSourceState, evidence string) error {
	if err := report.validate(); err != nil {
		return err
	}
	if report.Receipt.EventKey == "" {
		return ErrUsageEventNotFound
	}
	if err := ConfirmUsageEvent(ctx, report.Receipt, UsageEventOutcome{State: state, EvidenceRef: evidence}); err != nil {
		return err
	}
	if state != UsageEventSourceReady {
		return nil
	}
	return ApplyUsageEvent(ctx, report.Receipt)
}

// TaskUsageTransition binds a finalized adjustment to its winning task claim.
// Reason/Clamp are transient reporting values; neither enters statistics rows.
// A pending/unknown preceding operation blocks another monetary attempt.
type TaskUsageTransition struct {
	PreviousQuota int
	TargetQuota   int
	Refund        bool
	Input         UsageEventInput
	Report        *UsageSourceReport
	Reason        *common.Message
	Clamp         *common.QuotaClamp
}

// NewTaskUsageTransition freezes final numeric values before source mutation.
// Persistence allocates the task-local sequence; no monetary action occurs here.
func NewTaskUsageTransition(task *Task, total int, refund bool, input UsageEventInput) (*TaskUsageTransition, error) {
	if task == nil || task.ID <= 0 || task.Quota < 0 || total < 0 || refund && total != 0 {
		return nil, ErrUsageEventInvalid
	}
	uid, err := NewUsageOccurrenceUID()
	if err != nil {
		return nil, err
	}
	input.Identity = UsageEventIdentity{SourceKind: UsageEventSourceTaskAdjustment,
		SourceNamespace: UsageLiveSourceNamespace(),
		OccurrenceUID: uid, TaskPrimaryID: task.ID}
	input.OccurredAt = common.GetTimestamp()
	input.SourceOperationRef = "task-quota/v1/" + strconv.Itoa(task.Quota) + "/" + strconv.Itoa(total)
	input.Kind = UsageEventKindConsume
	if total < task.Quota {
		input.Kind = UsageEventKindRefund
	}
	return &TaskUsageTransition{PreviousQuota: task.Quota, TargetQuota: total, Refund: refund, Input: input}, nil
}

// prepareTaskUsageTransitionTx runs while holding the task row lock, in the
// same primary transaction as the source CAS/claim. It never replays funding.
func prepareTaskUsageTransitionTx(tx *gorm.DB, task *Task, transition *TaskUsageTransition) error {
	if transition == nil || transition.PreviousQuota != task.Quota || transition.TargetQuota < 0 {
		return ErrUsageEventStateConflict
	}
	if transition.TargetQuota == transition.PreviousQuota {
		return nil
	}
	var prior []UsageEvent
	if err := tx.Where("task_primary_id = ? AND source_kind = ?", task.ID, UsageEventSourceTaskAdjustment).
		Order("task_operation_sequence DESC").Limit(1).Find(&prior).Error; err != nil {
		return err
	}
	sequence := int64(1)
	if len(prior) != 0 {
		previous := prior[0]
		if err := validateUsageEventRow(previous); err != nil {
			return err
		}
		if previous.OccurrenceUID == transition.Input.Identity.OccurrenceUID {
			sequence = previous.TaskOperationSequence
		} else {
			if previous.SourceState == UsageEventSourcePending || previous.SourceState == UsageEventSourceUnknown {
				return ErrUsageEventNotReady
			}
			if previous.SourceState == UsageEventSourceReady {
				operation, ok := strings.CutPrefix(previous.SourceOperationRef, "task-quota/v1/")
				_, target, split := strings.Cut(operation, "/")
				if !ok || !split || target != strconv.Itoa(task.Quota) {
					return ErrUsageEventStateConflict
				}
			}
			var err error
			sequence, err = checkedUsageAdd(previous.TaskOperationSequence, 1)
			if err != nil {
				return err
			}
		}
	}
	input := transition.Input
	input.Identity.TaskOperationSequence = sequence
	report, err := NewUsageSourceReport(tx.Statement.Context, UsageEventSourceTaskAdjustment, input.Kind, input)
	if err != nil {
		return err
	}
	receipt, err := PrepareUsageEventTx(tx, report.Input, UsageEventSourcePending)
	if err != nil {
		return err
	}
	report.Receipt = receipt
	transition.Report = report
	return nil
}

// PrepareTaskUsageTransition claims a direct adjustment before the legacy money
// path. An unresolved claim remains blocked after a crash, never auto-redebited.
func PrepareTaskUsageTransition(ctx context.Context, task *Task, transition *TaskUsageTransition) error {
	if ctx == nil || DB == nil || task == nil || transition == nil {
		return ErrUsageEventInvalid
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := reserveUsageEventSQLiteWriter(tx); err != nil {
			return err
		}
		var stored Task
		if err := lockForUpdate(tx).Select("id", "user_id", "channel_id", "quota", "status").First(&stored, task.ID).Error; err != nil {
			return err
		}
		if stored.UserId != task.UserId || stored.ChannelId != task.ChannelId || stored.Quota != task.Quota || stored.Status != task.Status {
			return ErrUsageEventStateConflict
		}
		return prepareTaskUsageTransitionTx(tx, task, transition)
	})
}

// CommitTaskTerminalWithUsage atomically claims the terminal CAS and numeric
// obligation for legacy non-ledger tasks. The winning caller still performs its
// existing funding policy afterward and explicitly confirms that outcome.
func CommitTaskTerminalWithUsage(ctx context.Context, task *Task, previous TaskStatus, transition *TaskUsageTransition) (bool, error) {
	if ctx == nil || DB == nil || task == nil || task.ID <= 0 {
		return false, ErrUsageEventInvalid
	}
	won := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := reserveUsageEventSQLiteWriter(tx); err != nil {
			return err
		}
		var stored Task
		if err := lockForUpdate(tx).Select("id", "quota", "status", "user_id", "channel_id").First(&stored, task.ID).Error; err != nil {
			return err
		}
		if stored.Status != previous {
			return nil
		}
		if stored.UserId != task.UserId || stored.ChannelId != task.ChannelId || transition != nil && stored.Quota != transition.PreviousQuota {
			return ErrUsageEventStateConflict
		}
		if transition != nil {
			if err := prepareTaskUsageTransitionTx(tx, task, transition); err != nil {
				return err
			}
		}
		result := tx.Model(task).Where("status = ?", previous).Select("*").Updates(task)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("task usage transition lost its locked claim")
		}
		won = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return won, nil
}

// bindUsageSourceReport binds only the exact reporting payload. Optional log
// persistence never confirms a source outcome and is not required by statistics.
// Key/digest metadata remains admin-only and contains no body or arbitrary Other.
func bindUsageSourceReport(log *Log, other *LogOther, report *UsageSourceReport) (*LogOther, error) {
	if err := report.validate(); err != nil {
		return other, err
	}
	input := report.Input
	kind := UsageEventKindConsume
	if log.Type == LogTypeRefund {
		kind = UsageEventKindRefund
	} else if log.Type != LogTypeConsume {
		return other, ErrUsageEventInvalid
	}
	if kind != input.Kind || int64(log.UserId) != input.UserID || int64(log.TokenId) != input.TokenID ||
		int64(log.ChannelId) != input.ChannelID || log.ModelName != input.ModelName || log.Group != input.GroupName ||
		int64(log.Quota) != input.QuotaMagnitude || int64(log.PromptTokens) != input.PromptTokens || int64(log.CompletionTokens) != input.CompletionTokens {
		return other, ErrUsageEventPayloadConflict
	}
	if usageKnown(input.KnownMask, UsageEventKnownUsername) {
		log.Username = input.Username
	}
	if usageKnown(input.KnownMask, UsageEventKnownTokenName) {
		log.TokenName = input.TokenName
	}
	if usageKnown(input.KnownMask, UsageEventKnownRequestID) {
		log.RequestId = input.RequestID
	}
	if usageKnown(input.KnownMask, UsageEventKnownUpstreamRequestID) {
		log.UpstreamRequestId = input.UpstreamRequestID
	}
	log.CreatedAt = input.OccurredAt
	if other == nil {
		other = NewLogOther()
	} else {
		copy := *other
		copy.adminInfo = copyLogOtherMap(other.adminInfo)
		other = &copy
	}
	other.SetAdmin("usage_event_version", 1)
	other.SetAdmin("usage_event_key", input.EventKey)
	other.SetAdmin("usage_event_payload_digest", report.digest)
	// Display fields may omit known zeros or absent upstream usage. Preserve
	// the exact numeric codec separately so keyed verification need not infer
	// cache/input semantics from those optional legacy display fields.
	other.SetAdmin("usage_event_facts", struct {
		KnownMask           UsageEventKnownMask `json:"known_mask"`
		CacheReadTokens     int64               `json:"cache_read_tokens"`
		CacheWriteTokens    int64               `json:"cache_write_tokens"`
		ReportedInputTokens int64               `json:"reported_input_tokens"`
		UsageSemantic       UsageSemantic       `json:"usage_semantic"`
		WriterNode          string              `json:"writer_node"`
		ExportNode          string              `json:"export_node"`
	}{input.KnownMask, input.CacheReadTokens, input.CacheWriteTokens, input.ReportedInputTokens,
		input.UsageSemantic, input.WriterNode, input.ExportNode})
	return other, nil
}
