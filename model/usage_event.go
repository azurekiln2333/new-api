package model

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UsageEventProjectionVersion int64 = 1
	usageEventDigestLength            = 64
	usageEventUIDLength               = 32
	usageEventTextLimit               = 2048
)

// UsageEventSourceKind identifies the durable namespace that produced one
// occurrence. Values are persistent and must not be renumbered.
type UsageEventSourceKind int16

const (
	UsageEventSourceRelay UsageEventSourceKind = iota + 1
	UsageEventSourceTaskSubmission
	UsageEventSourceTaskAdjustment
	UsageEventSourceDiagnostic
	UsageEventSourceViolationFee
	UsageEventSourceMidjourney
	UsageEventSourceLegacyRelational
	UsageEventSourceLegacyClickHouse
)

// UsageEventKind separates gross consume reports from refunds.
type UsageEventKind int16

const (
	UsageEventKindConsume UsageEventKind = iota + 1
	UsageEventKindRefund
)

// UsageEventSourceState is the source owner's durable disposition. Projection
// completion is represented independently by AppliedVersion.
type UsageEventSourceState int16

const (
	UsageEventSourcePending UsageEventSourceState = 2
	UsageEventSourceReady   UsageEventSourceState = 3
	UsageEventNotReported   UsageEventSourceState = 4
	UsageEventSourceUnknown UsageEventSourceState = 5
)

// UsageEventOutcome is an explicit source-owner confirmation. EvidenceRef is
// value-free and is stored only as a digest; it is not a finance receipt.
type UsageEventOutcome struct {
	State       UsageEventSourceState
	EvidenceRef string
}

// UsageEventProjectionEligibility selects independent projection families.
type UsageEventProjectionEligibility int64

const (
	UsageEventProjectHours UsageEventProjectionEligibility = 1 << iota
	UsageEventProjectChannel
	usageEventProjectionAll = UsageEventProjectHours | UsageEventProjectChannel
)

// UsageEventKnownMask distinguishes unknown values from known empty/zero ones.
type UsageEventKnownMask int64

const (
	UsageEventKnownUserID UsageEventKnownMask = 1 << iota
	UsageEventKnownTokenID
	UsageEventKnownChannelID
	UsageEventKnownUsername
	UsageEventKnownTokenName
	UsageEventKnownModelName
	UsageEventKnownGroupName
	UsageEventKnownRequestID
	UsageEventKnownUpstreamRequestID
	UsageEventKnownWriterNode
	UsageEventKnownExportNode
	UsageEventKnownPromptTokens
	UsageEventKnownCompletionTokens
	UsageEventKnownCacheReadTokens
	UsageEventKnownCacheWriteTokens
	UsageEventKnownReportedInputTokens
	UsageEventKnownQuota
	UsageEventKnownUsageSemantic
)

const usageEventKnownAll UsageEventKnownMask = (1 << 18) - 1

// UsageSemantic identifies explicit source token semantics.
type UsageSemantic int16

const (
	UsageSemanticUnknown UsageSemantic = iota
	UsageSemanticOpenAI
	UsageSemanticAnthropic
	UsageSemanticOtherExplicit
)

// UsageEventIdentity is canonical source identity. EventKey is derived from
// the complete identity and a supplied key is accepted only if it matches.
type UsageEventIdentity struct {
	SourceNamespace       string
	SourceKind            UsageEventSourceKind
	OccurrenceUID         string
	SourceRowID           int64
	SourceTupleDigest     string
	SourceOrdinal         int64
	TaskPrimaryID         int64
	TaskOperationSequence int64
}

// UsageEventInput contains finalized numeric facts and historical filter
// dimensions only; bodies, credentials and arbitrary log metadata are excluded.
type UsageEventInput struct {
	EventKey             string
	Identity             UsageEventIdentity
	CollectionEpoch      string
	WriterEpoch          string
	ActivationGeneration int64
	SourceOperationRef   string
	Kind                 UsageEventKind
	Eligibility          UsageEventProjectionEligibility
	KnownMask            UsageEventKnownMask
	OccurredAt           int64
	UserID               int64
	TokenID              int64
	ChannelID            int64
	Username             string
	TokenName            string
	ModelName            string
	GroupName            string
	RequestID            string
	UpstreamRequestID    string
	WriterNode           string
	ExportNode           string
	PromptTokens         int64
	CompletionTokens     int64
	CacheReadTokens      int64
	CacheWriteTokens     int64
	ReportedInputTokens  int64
	QuotaMagnitude       int64
	UsageSemantic        UsageSemantic
}

// UsageEventReceipt binds a retry to a full source identity and immutable
// finalized payload digest.
type UsageEventReceipt struct {
	EventKey      string
	Identity      UsageEventIdentity
	PayloadDigest string
}

var (
	ErrUsageEventInvalid           = errors.New("usage event input is invalid")
	ErrUsageEventIdentityConflict  = errors.New("usage event identity conflicts with its canonical key")
	ErrUsageEventPayloadConflict   = errors.New("usage event key conflicts with a different immutable payload")
	ErrUsageEventStateConflict     = errors.New("usage event source state conflicts with the requested transition")
	ErrUsageEventNotFound          = errors.New("usage event was not found")
	ErrUsageEventNotReady          = errors.New("usage event is not source-ready")
	ErrUsageEventOverflow          = errors.New("usage statistics counter overflow")
	ErrUsageEventDimensionConflict = errors.New("usage statistics dimension key conflicts with different dimensions")
)

// UsageEvent is one immutable finalized fact plus mutable source/projection
// lifecycle metadata. It belongs only in the primary database.
type UsageEvent struct {
	ID                    int64                           `gorm:"primaryKey;autoIncrement;index:ix_usage_event_drain,priority:4;index:ix_usage_event_time,priority:4;index:ix_usage_event_self,priority:5;index:ix_usage_event_channel,priority:5;index:ix_usage_event_channel_tail,priority:5;index:ix_usage_event_pending_time,priority:5"`
	EventKey              string                          `gorm:"type:char(64);not null;uniqueIndex:ux_usage_event_key"`
	KeyVersion            int16                           `gorm:"type:smallint;not null"`
	PayloadDigest         string                          `gorm:"type:char(64);not null"`
	SourceNamespace       string                          `gorm:"type:char(32);not null"`
	CollectionEpoch       string                          `gorm:"type:char(32);not null"`
	WriterEpoch           string                          `gorm:"type:char(32);not null"`
	ActivationGeneration  int64                           `gorm:"type:bigint;not null"`
	SourceKind            UsageEventSourceKind            `gorm:"type:smallint;not null"`
	OccurrenceUID         string                          `gorm:"type:char(32);not null"`
	SourceOperationRef    string                          `gorm:"type:text;not null"`
	SourceRowID           int64                           `gorm:"type:bigint;not null"`
	SourceTupleDigest     string                          `gorm:"type:char(64);not null"`
	SourceOrdinal         int64                           `gorm:"type:bigint;not null"`
	TaskPrimaryID         int64                           `gorm:"type:bigint;not null;index:ix_usage_event_task,priority:1"`
	TaskOperationSequence int64                           `gorm:"type:bigint;not null;index:ix_usage_event_task,priority:2"`
	SourceState           UsageEventSourceState           `gorm:"type:smallint;not null;index:ix_usage_event_drain,priority:1;index:ix_usage_event_time,priority:1;index:ix_usage_event_self,priority:2;index:ix_usage_event_channel,priority:2;index:ix_usage_event_channel_tail,priority:2;index:ix_usage_event_pending_time,priority:1;index:ix_usage_event_request,priority:2;index:ix_usage_event_upstream_request,priority:2"`
	Kind                  UsageEventKind                  `gorm:"type:smallint;not null;index:ix_usage_event_time,priority:2;index:ix_usage_event_self,priority:3;index:ix_usage_event_channel,priority:3;index:ix_usage_event_channel_tail,priority:4;index:ix_usage_event_pending_time,priority:3;index:ix_usage_event_request,priority:3;index:ix_usage_event_upstream_request,priority:3"`
	Eligibility           UsageEventProjectionEligibility `gorm:"type:bigint;not null"`
	KnownMask             UsageEventKnownMask             `gorm:"type:bigint;not null"`
	OccurredAt            int64                           `gorm:"type:bigint;not null;index:ix_usage_event_time,priority:3;index:ix_usage_event_self,priority:4;index:ix_usage_event_channel,priority:4;index:ix_usage_event_pending_time,priority:4;index:ix_usage_event_request,priority:4;index:ix_usage_event_upstream_request,priority:4"`
	PreparedAt            int64                           `gorm:"type:bigint;not null"`
	UpdatedAt             int64                           `gorm:"type:bigint;not null;autoUpdateTime:false"`
	AppliedAt             int64                           `gorm:"type:bigint;not null"`
	UserID                int64                           `gorm:"type:bigint;not null;index:ix_usage_event_self,priority:1"`
	TokenID               int64                           `gorm:"type:bigint;not null"`
	ChannelID             int64                           `gorm:"type:bigint;not null;index:ix_usage_event_channel,priority:1;index:ix_usage_event_channel_tail,priority:1"`
	Username              string                          `gorm:"type:text;not null"`
	TokenName             string                          `gorm:"type:text;not null"`
	ModelName             string                          `gorm:"type:text;not null"`
	GroupName             string                          `gorm:"type:text;not null"`
	RequestID             string                          `gorm:"type:text;not null"`
	UpstreamRequestID     string                          `gorm:"type:text;not null"`
	WriterNode            string                          `gorm:"type:text;not null"`
	ExportNode            string                          `gorm:"type:text;not null"`
	RequestDigest         string                          `gorm:"type:char(64);not null;index:ix_usage_event_request,priority:1"`
	UpstreamRequestDigest string                          `gorm:"type:char(64);not null;index:ix_usage_event_upstream_request,priority:1"`
	PromptTokens          int64                           `gorm:"type:bigint;not null"`
	CompletionTokens      int64                           `gorm:"type:bigint;not null"`
	CacheReadTokens       int64                           `gorm:"type:bigint;not null"`
	CacheWriteTokens      int64                           `gorm:"type:bigint;not null"`
	ReportedInputTokens   int64                           `gorm:"type:bigint;not null"`
	QuotaMagnitude        int64                           `gorm:"type:bigint;not null"`
	UsageSemantic         UsageSemantic                   `gorm:"type:smallint;not null"`
	AppliedVersion        int64                           `gorm:"type:bigint;not null;index:ix_usage_event_drain,priority:2;index:ix_usage_event_channel_tail,priority:3;index:ix_usage_event_pending_time,priority:2"`
	NextAttemptAt         int64                           `gorm:"type:bigint;not null;index:ix_usage_event_drain,priority:3"`
	Attempts              int64                           `gorm:"type:bigint;not null"`
	ErrorCode             string                          `gorm:"type:varchar(48);not null"`
	OutcomeEvidenceDigest string                          `gorm:"type:char(64);not null"`
}

func (UsageEvent) TableName() string { return "usage_events" }

// UsageHour is one dashboard-compatible hourly dimension bucket.
type UsageHour struct {
	ID                int64  `gorm:"primaryKey;autoIncrement;index:ix_usage_hour_time,priority:3;index:ix_usage_hour_self,priority:4;index:ix_usage_hour_lineage,priority:5;index:ix_usage_hour_lineage_self,priority:6"`
	ProjectionVersion int64  `gorm:"type:bigint;not null;uniqueIndex:ux_usage_hour_key,priority:1;index:ix_usage_hour_time,priority:1;index:ix_usage_hour_self,priority:2;index:ix_usage_hour_lineage,priority:3;index:ix_usage_hour_lineage_self,priority:4"`
	DimensionKey      string `gorm:"type:char(64);not null;uniqueIndex:ux_usage_hour_key,priority:2"`
	// Empty identifies an older, unattributed projection. Migration must not
	// invent a namespace for already aggregated contributions or replay facts.
	SourceNamespace      string `gorm:"type:char(32);not null;default:'';index:ix_usage_hour_lineage,priority:1;index:ix_usage_hour_lineage_self,priority:1"`
	CollectionEpoch      string `gorm:"type:char(32);not null;index:ix_usage_hour_lineage,priority:2;index:ix_usage_hour_lineage_self,priority:2"`
	HourStart            int64  `gorm:"type:bigint;not null;index:ix_usage_hour_time,priority:2;index:ix_usage_hour_self,priority:3;index:ix_usage_hour_lineage,priority:4;index:ix_usage_hour_lineage_self,priority:5"`
	UserID               int64  `gorm:"type:bigint;not null;index:ix_usage_hour_self,priority:1;index:ix_usage_hour_lineage_self,priority:3"`
	Username             string `gorm:"type:text;not null"`
	ModelName            string `gorm:"type:text;not null"`
	GroupName            string `gorm:"type:text;not null"`
	TokenID              int64  `gorm:"type:bigint;not null"`
	ChannelID            int64  `gorm:"type:bigint;not null"`
	ExportNode           string `gorm:"type:text;not null"`
	DimensionKnownMask   int64  `gorm:"type:bigint;not null"`
	ConsumeCount         int64  `gorm:"type:bigint;not null"`
	GrossQuota           int64  `gorm:"type:bigint;not null"`
	PromptTokens         int64  `gorm:"type:bigint;not null"`
	CompletionTokens     int64  `gorm:"type:bigint;not null"`
	RankingTokens        int64  `gorm:"type:bigint;not null"`
	CacheReadTokens      int64  `gorm:"type:bigint;not null"`
	CacheWriteTokens     int64  `gorm:"type:bigint;not null"`
	InputDenominator     int64  `gorm:"type:bigint;not null"`
	CacheUnknownCount    int64  `gorm:"type:bigint;not null"`
	SemanticUnknownCount int64  `gorm:"type:bigint;not null"`
}

func (UsageHour) TableName() string { return "usage_hours" }

// UsageChannel is one lifetime channel consume projection.
type UsageChannel struct {
	ProjectionVersion int64 `gorm:"type:bigint;primaryKey;autoIncrement:false"`
	ChannelID         int64 `gorm:"type:bigint;primaryKey;autoIncrement:false"`
	ConsumeCount      int64 `gorm:"type:bigint;not null"`
	GrossQuota        int64 `gorm:"type:bigint;not null"`
	PromptTokens      int64 `gorm:"type:bigint;not null"`
	CompletionTokens  int64 `gorm:"type:bigint;not null"`
	LastUsedAt        int64 `gorm:"type:bigint;not null"`
}

func (UsageChannel) TableName() string { return "usage_channels" }

// UsageChannelModel stores historical model dimensions for one channel.
type UsageChannelModel struct {
	ID                int64  `gorm:"primaryKey;autoIncrement;index:ix_usage_channel_models,priority:3"`
	ProjectionVersion int64  `gorm:"type:bigint;not null;uniqueIndex:ux_usage_channel_model,priority:1;index:ix_usage_channel_models,priority:1"`
	ChannelID         int64  `gorm:"type:bigint;not null;uniqueIndex:ux_usage_channel_model,priority:2;index:ix_usage_channel_models,priority:2"`
	ModelKey          string `gorm:"type:char(64);not null;uniqueIndex:ux_usage_channel_model,priority:3"`
	ModelName         string `gorm:"type:text;not null"`
	ModelKnown        bool   `gorm:"not null"`
	ConsumeCount      int64  `gorm:"type:bigint;not null"`
	GrossQuota        int64  `gorm:"type:bigint;not null"`
	PromptTokens      int64  `gorm:"type:bigint;not null"`
	CompletionTokens  int64  `gorm:"type:bigint;not null"`
	LastUsedAt        int64  `gorm:"type:bigint;not null"`
}

func (UsageChannelModel) TableName() string { return "usage_channel_models" }

// MigrateUsageStatistics installs the additive primary-database foundation.
func MigrateUsageStatistics(db *gorm.DB) error {
	if db == nil {
		return errors.New("primary database is unavailable")
	}
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
		return db.AutoMigrate(&UsageEvent{}, &UsageHour{}, &UsageChannel{}, &UsageChannelModel{})
	default:
		return ErrUsageEventInvalid
	}
}

// NewUsageOccurrenceUID returns an opaque identifier that callers must retain
// for every retry of the same reporting occurrence.
func NewUsageOccurrenceUID() (string, error) {
	var raw [16]byte
	if _, err := crand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// CanonicalUsageEventKey derives the only accepted key for an event identity.
func CanonicalUsageEventKey(identity UsageEventIdentity) (string, error) {
	if err := validateUsageEventIdentity(identity); err != nil {
		return "", err
	}
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-event-key/v1", true)
	appendCanonicalInt16(&encoded, 1, int16(identity.SourceKind), true)
	appendCanonicalString(&encoded, 2, identity.SourceNamespace, true)
	appendCanonicalString(&encoded, 3, identity.OccurrenceUID, identity.OccurrenceUID != "")
	appendCanonicalInt64(&encoded, 4, identity.SourceRowID, identity.SourceRowID != 0)
	appendCanonicalString(&encoded, 5, identity.SourceTupleDigest, identity.SourceTupleDigest != "")
	appendCanonicalInt64(&encoded, 6, identity.SourceOrdinal, identity.SourceOrdinal != 0)
	appendCanonicalInt64(&encoded, 7, identity.TaskPrimaryID, identity.TaskPrimaryID != 0)
	appendCanonicalInt64(&encoded, 8, identity.TaskOperationSequence, identity.TaskOperationSequence != 0)
	return digestCanonical(encoded.Bytes()), nil
}

func validateUsageEventIdentity(identity UsageEventIdentity) error {
	if !isLowerHex(identity.SourceNamespace, usageEventUIDLength) {
		return ErrUsageEventInvalid
	}
	if identity.SourceKind < UsageEventSourceRelay || identity.SourceKind > UsageEventSourceLegacyClickHouse {
		return ErrUsageEventInvalid
	}
	if identity.SourceRowID < 0 || identity.SourceOrdinal < 0 || identity.TaskPrimaryID < 0 || identity.TaskOperationSequence < 0 {
		return ErrUsageEventInvalid
	}
	if identity.TaskOperationSequence > 0 && identity.TaskPrimaryID == 0 {
		return ErrUsageEventInvalid
	}
	if identity.SourceKind == UsageEventSourceTaskAdjustment && (identity.TaskPrimaryID == 0 || identity.TaskOperationSequence == 0) {
		return ErrUsageEventInvalid
	}
	if identity.SourceKind != UsageEventSourceTaskSubmission && identity.SourceKind != UsageEventSourceTaskAdjustment && (identity.TaskPrimaryID != 0 || identity.TaskOperationSequence != 0) {
		return ErrUsageEventInvalid
	}
	if identity.SourceTupleDigest != "" && !isLowerHex(identity.SourceTupleDigest, usageEventDigestLength) {
		return ErrUsageEventInvalid
	}
	switch identity.SourceKind {
	case UsageEventSourceLegacyRelational:
		if identity.OccurrenceUID != "" || identity.SourceRowID <= 0 || identity.SourceTupleDigest != "" || identity.SourceOrdinal != 0 {
			return ErrUsageEventInvalid
		}
	case UsageEventSourceLegacyClickHouse:
		if identity.OccurrenceUID != "" || identity.SourceRowID != 0 || identity.SourceTupleDigest == "" || identity.SourceOrdinal <= 0 {
			return ErrUsageEventInvalid
		}
	default:
		if !isLowerHex(identity.OccurrenceUID, usageEventUIDLength) || identity.SourceRowID != 0 || identity.SourceTupleDigest != "" || identity.SourceOrdinal != 0 {
			return ErrUsageEventInvalid
		}
	}
	return nil
}

func validateUsageText(value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes || strings.IndexByte(value, 0) >= 0 {
		return ErrUsageEventInvalid
	}
	return nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func validateUsageEventInput(input UsageEventInput, initialState UsageEventSourceState) (UsageEventInput, string, string, error) {
	key, err := CanonicalUsageEventKey(input.Identity)
	if err != nil {
		return UsageEventInput{}, "", "", err
	}
	if input.EventKey != "" && input.EventKey != key {
		return UsageEventInput{}, "", "", ErrUsageEventIdentityConflict
	}
	if !isLowerHex(input.CollectionEpoch, usageEventUIDLength) || (input.WriterEpoch != "" && !isLowerHex(input.WriterEpoch, usageEventUIDLength)) {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.ActivationGeneration < 0 || input.OccurredAt <= 0 || (input.SourceOperationRef != "" && validateUsageText(input.SourceOperationRef, usageEventTextLimit) != nil) {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.Kind != UsageEventKindConsume && input.Kind != UsageEventKindRefund {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if initialState != UsageEventSourcePending && initialState != UsageEventSourceReady {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.Eligibility&^usageEventProjectionAll != 0 || input.KnownMask&^usageEventKnownAll != 0 {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.Kind == UsageEventKindRefund && input.Eligibility != 0 {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.QuotaMagnitude < 0 || input.PromptTokens < 0 || input.CompletionTokens < 0 || input.CacheReadTokens < 0 || input.CacheWriteTokens < 0 || input.ReportedInputTokens < 0 {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	knownValues := []struct {
		known UsageEventKnownMask
		value int64
	}{
		{UsageEventKnownUserID, input.UserID},
		{UsageEventKnownTokenID, input.TokenID},
		{UsageEventKnownChannelID, input.ChannelID},
		{UsageEventKnownPromptTokens, input.PromptTokens},
		{UsageEventKnownCompletionTokens, input.CompletionTokens},
		{UsageEventKnownCacheReadTokens, input.CacheReadTokens},
		{UsageEventKnownCacheWriteTokens, input.CacheWriteTokens},
		{UsageEventKnownReportedInputTokens, input.ReportedInputTokens},
		{UsageEventKnownQuota, input.QuotaMagnitude},
	}
	for _, field := range knownValues {
		if field.value < 0 || !usageKnown(input.KnownMask, field.known) && field.value != 0 {
			return UsageEventInput{}, "", "", ErrUsageEventInvalid
		}
	}
	if input.UsageSemantic < UsageSemanticUnknown || input.UsageSemantic > UsageSemanticOtherExplicit {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if usageKnown(input.KnownMask, UsageEventKnownUsageSemantic) == (input.UsageSemantic == UsageSemanticUnknown) {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.Eligibility != 0 && (!usageKnown(input.KnownMask, UsageEventKnownQuota) || !usageKnown(input.KnownMask, UsageEventKnownPromptTokens) || !usageKnown(input.KnownMask, UsageEventKnownCompletionTokens)) {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	if input.Eligibility&UsageEventProjectChannel != 0 && (!usageKnown(input.KnownMask, UsageEventKnownChannelID) || input.ChannelID <= 0) {
		return UsageEventInput{}, "", "", ErrUsageEventInvalid
	}
	texts := []struct {
		value string
		limit int
		known UsageEventKnownMask
	}{
		{input.Username, 1024, UsageEventKnownUsername},
		{input.TokenName, 1024, UsageEventKnownTokenName},
		{input.ModelName, 16384, UsageEventKnownModelName},
		{input.GroupName, 4096, UsageEventKnownGroupName},
		{input.RequestID, 256, UsageEventKnownRequestID},
		{input.UpstreamRequestID, 512, UsageEventKnownUpstreamRequestID},
		{input.WriterNode, 1024, UsageEventKnownWriterNode},
		{input.ExportNode, 1024, UsageEventKnownExportNode},
	}
	for _, text := range texts {
		if err := validateUsageText(text.value, text.limit); err != nil {
			return UsageEventInput{}, "", "", err
		}
		if !usageKnown(input.KnownMask, text.known) && text.value != "" {
			return UsageEventInput{}, "", "", ErrUsageEventInvalid
		}
	}
	input.EventKey = key
	payloadDigest := usageEventPayloadDigest(input)
	return input, key, payloadDigest, nil
}

func usageKnown(mask UsageEventKnownMask, flag UsageEventKnownMask) bool {
	return mask&flag != 0
}

func appendCanonicalField(dst *bytes.Buffer, fieldID uint16, valueType byte, known bool, value []byte) {
	var field [2]byte
	binary.BigEndian.PutUint16(field[:], fieldID)
	dst.Write(field[:])
	if known {
		dst.WriteByte(1)
	} else {
		dst.WriteByte(0)
	}
	dst.WriteByte(valueType)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	dst.Write(size[:])
	dst.Write(value)
}

func appendCanonicalString(dst *bytes.Buffer, fieldID uint16, value string, known bool) {
	appendCanonicalField(dst, fieldID, 's', known, []byte(value))
}

func appendCanonicalInt64(dst *bytes.Buffer, fieldID uint16, value int64, known bool) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	appendCanonicalField(dst, fieldID, 'i', known, encoded[:])
}

func appendCanonicalInt16(dst *bytes.Buffer, fieldID uint16, value int16, known bool) {
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], uint16(value))
	appendCanonicalField(dst, fieldID, 'h', known, encoded[:])
}

func digestCanonical(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func usageEventPayloadDigest(input UsageEventInput) string {
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-payload/v1", true)
	appendCanonicalString(&encoded, 1, input.EventKey, true)
	appendCanonicalInt16(&encoded, 2, int16(input.Identity.SourceKind), true)
	appendCanonicalString(&encoded, 3, input.Identity.SourceNamespace, true)
	appendCanonicalString(&encoded, 4, input.Identity.OccurrenceUID, input.Identity.OccurrenceUID != "")
	appendCanonicalInt64(&encoded, 5, input.Identity.SourceRowID, input.Identity.SourceRowID != 0)
	appendCanonicalString(&encoded, 6, input.Identity.SourceTupleDigest, input.Identity.SourceTupleDigest != "")
	appendCanonicalInt64(&encoded, 7, input.Identity.SourceOrdinal, input.Identity.SourceOrdinal != 0)
	appendCanonicalInt64(&encoded, 8, input.Identity.TaskPrimaryID, input.Identity.TaskPrimaryID != 0)
	appendCanonicalInt64(&encoded, 9, input.Identity.TaskOperationSequence, input.Identity.TaskOperationSequence != 0)
	appendCanonicalString(&encoded, 10, input.CollectionEpoch, true)
	appendCanonicalString(&encoded, 11, input.WriterEpoch, input.WriterEpoch != "")
	appendCanonicalInt64(&encoded, 12, input.ActivationGeneration, true)
	appendCanonicalString(&encoded, 13, input.SourceOperationRef, true)
	appendCanonicalInt16(&encoded, 14, int16(input.Kind), true)
	appendCanonicalInt64(&encoded, 15, int64(input.Eligibility), true)
	appendCanonicalInt64(&encoded, 16, int64(input.KnownMask), true)
	appendCanonicalInt64(&encoded, 17, input.OccurredAt, true)
	appendCanonicalInt64(&encoded, 18, input.UserID, usageKnown(input.KnownMask, UsageEventKnownUserID))
	appendCanonicalInt64(&encoded, 19, input.TokenID, usageKnown(input.KnownMask, UsageEventKnownTokenID))
	appendCanonicalInt64(&encoded, 20, input.ChannelID, usageKnown(input.KnownMask, UsageEventKnownChannelID))
	appendCanonicalString(&encoded, 21, input.Username, usageKnown(input.KnownMask, UsageEventKnownUsername))
	appendCanonicalString(&encoded, 22, input.TokenName, usageKnown(input.KnownMask, UsageEventKnownTokenName))
	appendCanonicalString(&encoded, 23, input.ModelName, usageKnown(input.KnownMask, UsageEventKnownModelName))
	appendCanonicalString(&encoded, 24, input.GroupName, usageKnown(input.KnownMask, UsageEventKnownGroupName))
	appendCanonicalString(&encoded, 25, input.RequestID, usageKnown(input.KnownMask, UsageEventKnownRequestID))
	appendCanonicalString(&encoded, 26, input.UpstreamRequestID, usageKnown(input.KnownMask, UsageEventKnownUpstreamRequestID))
	appendCanonicalString(&encoded, 27, input.WriterNode, usageKnown(input.KnownMask, UsageEventKnownWriterNode))
	appendCanonicalString(&encoded, 28, input.ExportNode, usageKnown(input.KnownMask, UsageEventKnownExportNode))
	appendCanonicalInt64(&encoded, 29, input.PromptTokens, usageKnown(input.KnownMask, UsageEventKnownPromptTokens))
	appendCanonicalInt64(&encoded, 30, input.CompletionTokens, usageKnown(input.KnownMask, UsageEventKnownCompletionTokens))
	appendCanonicalInt64(&encoded, 31, input.CacheReadTokens, usageKnown(input.KnownMask, UsageEventKnownCacheReadTokens))
	appendCanonicalInt64(&encoded, 32, input.CacheWriteTokens, usageKnown(input.KnownMask, UsageEventKnownCacheWriteTokens))
	appendCanonicalInt64(&encoded, 33, input.ReportedInputTokens, usageKnown(input.KnownMask, UsageEventKnownReportedInputTokens))
	appendCanonicalInt64(&encoded, 34, input.QuotaMagnitude, usageKnown(input.KnownMask, UsageEventKnownQuota))
	appendCanonicalInt16(&encoded, 35, int16(input.UsageSemantic), usageKnown(input.KnownMask, UsageEventKnownUsageSemantic))
	return digestCanonical(encoded.Bytes())
}

func normalizeFixedText(value string) string {
	return strings.TrimRight(value, " ")
}

func usageEventInputFromRow(row UsageEvent) UsageEventInput {
	return UsageEventInput{
		EventKey: normalizeFixedText(row.EventKey),
		Identity: UsageEventIdentity{
			SourceNamespace: normalizeFixedText(row.SourceNamespace), SourceKind: row.SourceKind, OccurrenceUID: normalizeFixedText(row.OccurrenceUID),
			SourceRowID: row.SourceRowID, SourceTupleDigest: normalizeFixedText(row.SourceTupleDigest), SourceOrdinal: row.SourceOrdinal,
			TaskPrimaryID: row.TaskPrimaryID, TaskOperationSequence: row.TaskOperationSequence,
		},
		CollectionEpoch: normalizeFixedText(row.CollectionEpoch), WriterEpoch: normalizeFixedText(row.WriterEpoch), ActivationGeneration: row.ActivationGeneration,
		SourceOperationRef: row.SourceOperationRef, Kind: row.Kind, Eligibility: row.Eligibility, KnownMask: row.KnownMask,
		OccurredAt: row.OccurredAt, UserID: row.UserID, TokenID: row.TokenID, ChannelID: row.ChannelID,
		Username: row.Username, TokenName: row.TokenName, ModelName: row.ModelName, GroupName: row.GroupName,
		RequestID: row.RequestID, UpstreamRequestID: row.UpstreamRequestID, WriterNode: row.WriterNode, ExportNode: row.ExportNode,
		PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, CacheReadTokens: row.CacheReadTokens,
		CacheWriteTokens: row.CacheWriteTokens, ReportedInputTokens: row.ReportedInputTokens,
		QuotaMagnitude: row.QuotaMagnitude, UsageSemantic: row.UsageSemantic,
	}
}

func usageEventRow(input UsageEventInput, state UsageEventSourceState, payloadDigest string, now int64) UsageEvent {
	outcomeDigest := ""
	if state == UsageEventSourceReady {
		outcomeDigest = digestUsageOutcome(UsageEventOutcome{State: state})
	}
	return UsageEvent{
		EventKey: input.EventKey, KeyVersion: 1, PayloadDigest: payloadDigest, OutcomeEvidenceDigest: outcomeDigest,
		SourceNamespace: input.Identity.SourceNamespace, CollectionEpoch: input.CollectionEpoch, WriterEpoch: input.WriterEpoch,
		ActivationGeneration: input.ActivationGeneration, SourceKind: input.Identity.SourceKind, OccurrenceUID: input.Identity.OccurrenceUID,
		SourceOperationRef: input.SourceOperationRef, SourceRowID: input.Identity.SourceRowID, SourceTupleDigest: input.Identity.SourceTupleDigest,
		SourceOrdinal: input.Identity.SourceOrdinal, TaskPrimaryID: input.Identity.TaskPrimaryID, TaskOperationSequence: input.Identity.TaskOperationSequence,
		SourceState: state, Kind: input.Kind, Eligibility: input.Eligibility, KnownMask: input.KnownMask,
		OccurredAt: input.OccurredAt, PreparedAt: now, UpdatedAt: now, UserID: input.UserID, TokenID: input.TokenID, ChannelID: input.ChannelID,
		Username: input.Username, TokenName: input.TokenName, ModelName: input.ModelName, GroupName: input.GroupName,
		RequestID: input.RequestID, UpstreamRequestID: input.UpstreamRequestID, WriterNode: input.WriterNode, ExportNode: input.ExportNode,
		RequestDigest:         digestKnownUsageText(input.RequestID, usageKnown(input.KnownMask, UsageEventKnownRequestID)),
		UpstreamRequestDigest: digestKnownUsageText(input.UpstreamRequestID, usageKnown(input.KnownMask, UsageEventKnownUpstreamRequestID)),
		PromptTokens:          input.PromptTokens, CompletionTokens: input.CompletionTokens, CacheReadTokens: input.CacheReadTokens,
		CacheWriteTokens: input.CacheWriteTokens, ReportedInputTokens: input.ReportedInputTokens,
		QuotaMagnitude: input.QuotaMagnitude, UsageSemantic: input.UsageSemantic,
	}
}

func digestKnownUsageText(value string, known bool) string {
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-text/v1", true)
	appendCanonicalString(&encoded, 1, value, known)
	return digestCanonical(encoded.Bytes())
}

func usageEventReceipt(row UsageEvent) UsageEventReceipt {
	input := usageEventInputFromRow(row)
	return UsageEventReceipt{EventKey: input.EventKey, Identity: input.Identity, PayloadDigest: normalizeFixedText(row.PayloadDigest)}
}

func validateUsageEventReceipt(receipt UsageEventReceipt) error {
	key, err := CanonicalUsageEventKey(receipt.Identity)
	if err != nil {
		return err
	}
	if key != receipt.EventKey || !isLowerHex(receipt.PayloadDigest, usageEventDigestLength) {
		return ErrUsageEventIdentityConflict
	}
	return nil
}

func usageEventReceiptMatches(receipt UsageEventReceipt, row UsageEvent) bool {
	if receipt.EventKey != normalizeFixedText(row.EventKey) || receipt.Identity != usageEventInputFromRow(row).Identity || receipt.PayloadDigest != normalizeFixedText(row.PayloadDigest) {
		return false
	}
	return usageEventPayloadDigest(usageEventInputFromRow(row)) == normalizeFixedText(row.PayloadDigest)
}

func reserveUsageEventSQLiteWriter(tx *gorm.DB) error {
	if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return nil
	}
	return tx.Exec("UPDATE usage_events SET id = id WHERE 1 = 0").Error
}

// PrepareUsageEvent commits a finalized fact on the primary DB. Replaying the
// same identity and payload returns the existing receipt without changing its
// source state.
func PrepareUsageEvent(ctx context.Context, input UsageEventInput, initialState UsageEventSourceState) (UsageEventReceipt, error) {
	if ctx == nil || DB == nil {
		return UsageEventReceipt{}, errors.New("primary database and context are required")
	}
	if _, _, _, err := validateUsageEventInput(input, initialState); err != nil {
		return UsageEventReceipt{}, err
	}
	var receipt UsageEventReceipt
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = PrepareUsageEventTx(tx, input, initialState)
		return err
	})
	return receipt, err
}

// PrepareUsageEventTx uses only the caller's transaction. On SQLite it obtains
// the writer reservation before reading this event; callers must not have
// established a transaction snapshot beforehand.
func PrepareUsageEventTx(tx *gorm.DB, input UsageEventInput, initialState UsageEventSourceState) (UsageEventReceipt, error) {
	if tx == nil {
		return UsageEventReceipt{}, errors.New("primary transaction is unavailable")
	}
	input, key, payloadDigest, err := validateUsageEventInput(input, initialState)
	if err != nil {
		return UsageEventReceipt{}, err
	}
	if err := reserveUsageEventSQLiteWriter(tx); err != nil {
		return UsageEventReceipt{}, err
	}
	candidate := usageEventRow(input, initialState, payloadDigest, common.GetTimestamp())
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&candidate).Error; err != nil {
		return UsageEventReceipt{}, err
	}
	var stored UsageEvent
	if err := lockForUpdate(tx).Where("event_key = ?", key).Take(&stored).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return UsageEventReceipt{}, ErrUsageEventNotFound
		}
		return UsageEventReceipt{}, err
	}
	storedInput := usageEventInputFromRow(stored)
	if stored.KeyVersion != 1 || normalizeFixedText(stored.EventKey) != key || normalizeFixedText(stored.PayloadDigest) != payloadDigest || storedInput != input || usageEventPayloadDigest(storedInput) != payloadDigest {
		return UsageEventReceipt{}, ErrUsageEventPayloadConflict
	}
	if err := validateUsageEventRow(stored); err != nil {
		return UsageEventReceipt{}, err
	}
	return usageEventReceipt(stored), nil
}

// ConfirmUsageEvent records an explicit source-owner disposition.
func ConfirmUsageEvent(ctx context.Context, receipt UsageEventReceipt, outcome UsageEventOutcome) error {
	if ctx == nil || DB == nil {
		return errors.New("primary database and context are required")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return ConfirmUsageEventTx(tx, receipt, outcome)
	})
}

// ConfirmUsageEventTx applies only an explicit source-owner transition. It
// never performs or retries a financial operation.
func ConfirmUsageEventTx(tx *gorm.DB, receipt UsageEventReceipt, outcome UsageEventOutcome) error {
	if tx == nil {
		return errors.New("primary transaction is unavailable")
	}
	if err := validateUsageEventReceipt(receipt); err != nil {
		return err
	}
	if outcome.State != UsageEventSourceReady && outcome.State != UsageEventNotReported && outcome.State != UsageEventSourceUnknown {
		return ErrUsageEventInvalid
	}
	if err := validateUsageText(outcome.EvidenceRef, usageEventTextLimit); err != nil {
		return err
	}
	if err := reserveUsageEventSQLiteWriter(tx); err != nil {
		return err
	}
	var stored UsageEvent
	if err := lockForUpdate(tx).Where("event_key = ?", receipt.EventKey).Take(&stored).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUsageEventNotFound
		}
		return err
	}
	if err := validateUsageEventRow(stored); err != nil {
		return err
	}
	if !usageEventReceiptMatches(receipt, stored) {
		return ErrUsageEventPayloadConflict
	}
	evidenceDigest := digestUsageOutcome(outcome)
	if stored.SourceState == outcome.State {
		if normalizeFixedText(stored.OutcomeEvidenceDigest) == evidenceDigest {
			return nil
		}
		return ErrUsageEventStateConflict
	}
	if stored.SourceState == UsageEventSourceReady || stored.SourceState == UsageEventNotReported {
		return ErrUsageEventStateConflict
	}
	if stored.SourceState != UsageEventSourcePending && stored.SourceState != UsageEventSourceUnknown {
		return ErrUsageEventStateConflict
	}
	if stored.SourceState == UsageEventSourceUnknown && outcome.State != UsageEventSourceUnknown && outcome.EvidenceRef == "" {
		return ErrUsageEventStateConflict
	}
	return tx.Model(&UsageEvent{}).Where("id = ?", stored.ID).Updates(map[string]any{
		"source_state":            outcome.State,
		"outcome_evidence_digest": evidenceDigest,
		"updated_at":              common.GetTimestamp(),
		"error_code":              "",
	}).Error
}

func digestUsageOutcome(outcome UsageEventOutcome) string {
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-outcome/v1", true)
	appendCanonicalInt16(&encoded, 1, int16(outcome.State), true)
	appendCanonicalString(&encoded, 2, outcome.EvidenceRef, outcome.EvidenceRef != "")
	return digestCanonical(encoded.Bytes())
}

func validUsageEventState(state UsageEventSourceState) bool {
	return state == UsageEventSourcePending || state == UsageEventSourceReady || state == UsageEventNotReported || state == UsageEventSourceUnknown
}

func checkedUsageAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, ErrUsageEventOverflow
	}
	return a + b, nil
}

func validateUsageEventRow(row UsageEvent) error {
	input := usageEventInputFromRow(row)
	_, key, digest, err := validateUsageEventInput(input, UsageEventSourcePending)
	if err != nil {
		return err
	}
	if key != normalizeFixedText(row.EventKey) || digest != normalizeFixedText(row.PayloadDigest) || row.KeyVersion != 1 || row.ID <= 0 || row.AppliedVersion < 0 || row.AppliedVersion > UsageEventProjectionVersion || row.Attempts < 0 || !validUsageEventState(row.SourceState) {
		return ErrUsageEventPayloadConflict
	}
	if row.AppliedVersion != 0 && row.SourceState != UsageEventSourceReady {
		return ErrUsageEventStateConflict
	}
	if normalizeFixedText(row.RequestDigest) != digestKnownUsageText(row.RequestID, usageKnown(row.KnownMask, UsageEventKnownRequestID)) || normalizeFixedText(row.UpstreamRequestDigest) != digestKnownUsageText(row.UpstreamRequestID, usageKnown(row.KnownMask, UsageEventKnownUpstreamRequestID)) {
		return ErrUsageEventPayloadConflict
	}
	return nil
}
