package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUsageStatisticsNotReady = errors.New("usage statistics coverage is not verified")

type UsageStatisticsFamily int16

const (
	UsageStatisticsFacts UsageStatisticsFamily = iota + 1
	UsageStatisticsChannels
	UsageStatisticsDashboard
)

type UsageCoverageStatus string

const (
	UsageCoverageUnknown          UsageCoverageStatus = "unknown"
	UsageCoveragePartial          UsageCoverageStatus = "partial"
	UsageCoverageRetainedVerified UsageCoverageStatus = "retained_verified"
	UsageCoverageComplete         UsageCoverageStatus = "complete"
)

// UsageStatisticsCoverageView describes the requested subtotal's evidence, not
// its value. A partial/unknown empty subtotal is never verified zero history.
// Zero bounds mean lifetime/unbounded and cannot be certified by finite evidence.
type UsageStatisticsCoverageView struct {
	Complete               bool                `json:"complete"`
	Coverage               UsageCoverageStatus `json:"coverage"`
	HistoricalCoverage     UsageCoverageStatus `json:"historical_coverage"`
	LiveCoverage           UsageCoverageStatus `json:"live_coverage"`
	LegacyBaselineIncluded bool                `json:"legacy_baseline_included"`
	StartTimestamp         int64               `json:"start_timestamp"`
	EndTimestamp           int64               `json:"end_timestamp"`
}

// UsageReadLineage is an explicitly attested membership boundary, not a clock
// cutoff. The legacy baseline includes only IDs <= LegacyQuotaDataMaxID; the
// frozen manifest attests its contents. Live hours must match BOTH the returned
// namespace (in their source facts) and collection epoch, and ProjectHours.
// Historical imports never receive ProjectHours. A lineage alone proves neither
// complete source capture nor permission to delete anything.
type UsageReadLineage struct {
	IncludeLegacyQuotaData bool
	LegacyQuotaDataMaxID   int64
	LegacyBaselineDigest   string
	FenceDigest            string
	// Legacy source identity names the fenced retained log collection; it is
	// independent of the live event codec identity below.
	LegacySourceNamespace string
	LegacyCollectionEpoch string
	LiveSourceNamespace   string
	LiveCollectionEpoch   string
	DashboardEligibility  UsageEventProjectionEligibility
	HistoricalCoverage    UsageCoverageStatus
	WholeHistoryCoverage  UsageCoverageStatus
	LineageDigest         string
}

// UsageStatisticsLineage is immutable once installed. Changing a deployment's
// source membership needs a separately reviewed lineage transition, not an
// overwrite that makes old events eligible for a second dashboard projection.
type UsageStatisticsLineage struct {
	ID                    int64  `gorm:"primaryKey;autoIncrement:false"`
	LineageDigest         string `gorm:"type:char(64);not null"`
	LegacyQuotaDataMaxID  int64  `gorm:"type:bigint;not null"`
	LegacyBaselineDigest  string `gorm:"type:char(64);not null"`
	FenceDigest           string `gorm:"type:char(64);not null"`
	LegacySourceNamespace string `gorm:"type:char(32);not null;default:''"`
	LegacyCollectionEpoch string `gorm:"type:char(32);not null;default:''"`
	LiveSourceNamespace   string `gorm:"type:char(32);not null"`
	LiveCollectionEpoch   string `gorm:"type:char(32);not null"`
	InstalledAt           int64  `gorm:"type:bigint;not null"`
}

func (UsageStatisticsLineage) TableName() string { return "usage_statistics_lineages" }

// UsageStatisticsTransition is a permanent serialization row. Every upgraded
// legacy aggregate write and lineage installation locks it before inspecting
// membership. It has no lease and conveys no completeness evidence.
type UsageStatisticsTransition struct {
	ID int64 `gorm:"primaryKey;autoIncrement:false"`
}

func (UsageStatisticsTransition) TableName() string { return "usage_statistics_transitions" }

// UsageLegacyAcknowledgement proves this exact occurrence was durably added to
// quota_data. It commits with that addition and its covered-hour projection.
// Enqueuing a memory buffer or publishing a diagnostic log is not such proof.
type UsageLegacyAcknowledgement struct {
	EventKey      string `gorm:"type:char(64);primaryKey"`
	PayloadDigest string `gorm:"type:char(64);not null"`
	QuotaDataID   int64  `gorm:"type:bigint;not null"`
}

func (UsageLegacyAcknowledgement) TableName() string { return "usage_legacy_acknowledgements" }

// UsageLegacyHour retains the exact part of a live hour already owned by the
// immutable legacy baseline. Readers remove only these proven contributions,
// never an estimated residual. Its dimension codec is the UsageHour codec.
type UsageLegacyHour struct {
	DimensionKey         string `gorm:"type:char(64);primaryKey"`
	ProjectionVersion    int64  `gorm:"type:bigint;not null"`
	SourceNamespace      string `gorm:"type:char(32);not null"`
	CollectionEpoch      string `gorm:"type:char(32);not null"`
	HourStart            int64  `gorm:"type:bigint;not null"`
	UserID               int64  `gorm:"type:bigint;not null"`
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

func (UsageLegacyHour) TableName() string { return "usage_legacy_hours" }

func lockUsageStatisticsTransition(tx *gorm.DB) error {
	if err := reserveUsageEventSQLiteWriter(tx); err != nil {
		return err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&UsageStatisticsTransition{ID: 1}).Error; err != nil {
		return err
	}
	var anchor UsageStatisticsTransition
	return lockForUpdate(tx).Where("id = ?", 1).Take(&anchor).Error
}

// UsageStatisticsCoverage retains the exact closed interval and evidence that
// an external source owner has verified. Backfill only verifies retained rows;
// it cannot write this all-source attestation or resolve pre-Prepare losses.
type UsageStatisticsCoverage struct {
	CoverageKey    string                `gorm:"type:char(64);primaryKey"`
	Family         UsageStatisticsFamily `gorm:"type:smallint;not null;index:ix_usage_coverage_range,priority:1"`
	StartAt        int64                 `gorm:"type:bigint;not null;index:ix_usage_coverage_range,priority:2"`
	EndAt          int64                 `gorm:"type:bigint;not null"`
	LineageDigest  string                `gorm:"type:char(64);not null"`
	ManifestDigest string                `gorm:"type:char(64);not null"`
	SourceSeal     string                `gorm:"type:char(64);not null"`
	VerifiedAt     int64                 `gorm:"type:bigint;not null"`
}

// UsageLegacyQuotaDataBaseline is a bounded inspection of the retained
// legacy dashboard aggregate. The digest covers every row in id order and is
// evidence for an operator supplied fence; it is not a source completeness
// attestation by itself.
type UsageLegacyQuotaDataBaseline struct {
	Rows   int64  `json:"rows"`
	MaxID  int64  `json:"max_id"`
	Digest string `json:"digest"`
}

func quotaDataBaselineRowDigest(previous string, row QuotaData) (string, error) {
	encoded, err := common.Marshal(row)
	if err != nil {
		return "", err
	}
	var chain bytes.Buffer
	appendCanonicalString(&chain, 0, previous, true)
	appendCanonicalField(&chain, 1, 'b', true, encoded)
	return digestCanonical(chain.Bytes()), nil
}

// InspectUsageLegacyQuotaData reads quota_data in bounded pages and computes a
// deterministic digest without retaining the whole table in memory.
func InspectUsageLegacyQuotaData(ctx context.Context) (UsageLegacyQuotaDataBaseline, error) {
	baseline := UsageLegacyQuotaDataBaseline{Digest: digestCanonical([]byte("usage-legacy-quota-data/v1"))}
	if ctx == nil || DB == nil {
		return baseline, ErrUsageStatisticsNotReady
	}
	return inspectUsageLegacyQuotaData(DB.WithContext(ctx))
}

func inspectUsageLegacyQuotaData(db *gorm.DB) (UsageLegacyQuotaDataBaseline, error) {
	baseline := UsageLegacyQuotaDataBaseline{Digest: digestCanonical([]byte("usage-legacy-quota-data/v1"))}
	const pageSize = 1000
	lastID := 0
	for {
		var rows []QuotaData
		if err := db.Where("id > ?", lastID).Order("id").Limit(pageSize).Find(&rows).Error; err != nil {
			return UsageLegacyQuotaDataBaseline{}, err
		}
		if len(rows) == 0 {
			return baseline, nil
		}
		for _, row := range rows {
			if row.Id <= lastID {
				return UsageLegacyQuotaDataBaseline{}, fmt.Errorf("quota_data ids are not strictly increasing")
			}
			digest, err := quotaDataBaselineRowDigest(baseline.Digest, row)
			if err != nil {
				return UsageLegacyQuotaDataBaseline{}, err
			}
			baseline.Digest = digest
			baseline.Rows++
			baseline.MaxID = int64(row.Id)
			lastID = row.Id
		}
		if len(rows) < pageSize {
			return baseline, nil
		}
	}
}

// UsageStatisticsLineageProof is the required retained-set evidence for the
// public cutover entry point. ExpectedBaseline must be the exact bounded
// quota_data inspection supplied by the operator. The operator must separately
// stop/drain old writers and protect the source/TTL range through verification
// and commit; this value is evidence, not a runtime fence or an assertion that
// the application stopped old binaries or native TTL.
type UsageStatisticsLineageProof struct {
	ExpectedBaseline UsageLegacyQuotaDataBaseline
}

// ActivateUsageStatisticsLineage re-inspects quota_data and the complete
// retained numeric log set immediately before installing an immutable global
// lineage. Its caller must already hold the externally managed old-writer,
// source and TTL fence for the entire call. This function neither creates nor
// pretends to enforce that distributed prerequisite.
func ActivateUsageStatisticsLineage(ctx context.Context, lineage UsageReadLineage, expected UsageLegacyQuotaDataBaseline) error {
	return activateUsageStatisticsLineageProof(ctx, lineage, expected)
}

// activateUsageStatisticsLineageProof performs the complete retained-set proof
// before the marker commit. The name records the operator-held precondition;
// there is deliberately no callback/token API that could falsely claim to
// enforce an external writer or native TTL fence.
func activateUsageStatisticsLineageProof(ctx context.Context, lineage UsageReadLineage, expected UsageLegacyQuotaDataBaseline) error {
	if ctx == nil || DB == nil || !lineage.IncludeLegacyQuotaData || expected.Rows < 0 || expected.MaxID < 0 ||
		!isLowerHex(expected.Digest, usageEventDigestLength) ||
		!isLowerHex(lineage.LegacyBaselineDigest, usageEventDigestLength) ||
		!isLowerHex(lineage.FenceDigest, usageEventDigestLength) ||
		!isLowerHex(lineage.LegacySourceNamespace, usageEventUIDLength) ||
		!isLowerHex(lineage.LegacyCollectionEpoch, usageEventUIDLength) ||
		expected.Digest != lineage.LegacyBaselineDigest {
		return ErrUsageEventInvalid
	}
	if err := ValidateUsageLiveReadLineage(lineage); err != nil {
		return err
	}
	// This process can prove only its own drain. The external fence attests
	// every other old binary/buffer and the retained source/TTL boundary.
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	if len(CacheQuotaData) != 0 || len(pendingUsageLegacyReports) != 0 {
		return ErrUsageStatisticsNotReady
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUsageStatisticsTransition(tx); err != nil {
			return err
		}
		observed, err := inspectUsageLegacyQuotaData(tx)
		if err != nil {
			return err
		}
		if observed != expected || observed.MaxID != lineage.LegacyQuotaDataMaxID {
			return ErrUsageStatisticsNotReady
		}
		installed, err := getUsageReadLineage(tx)
		if err != nil {
			return err
		}
		// Facts written by an older dual-write codec have no exact baseline
		// ownership. Refuse activation instead of guessing whether to omit them.
		var ambiguous int64
		if err := tx.Model(&UsageEvent{}).Where("source_namespace = ? AND collection_epoch = ? AND (eligibility & ?) <> 0 AND activation_generation <> ?",
			lineage.LiveSourceNamespace, lineage.LiveCollectionEpoch, UsageEventProjectHours, usageDashboardOwnershipVersion).Count(&ambiguous).Error; err != nil {
			return err
		}
		if ambiguous != 0 {
			return ErrUsageStatisticsNotReady
		}
		if installed.IncludeLegacyQuotaData {
			// The marker is immutable and cleanup may already have removed its
			// verified source rows. Replay compares the original coordinates;
			// it must not require those disposable rows to reappear.
			return installUsageReadLineageTx(tx, lineage)
		}
		// A lineage switches fact/channel readers too. Verify the frozen retained
		// source was imported before it can replace their surviving log history.
		var checkpoint UsageEventBackfill
		if err := lockForUpdate(tx).Where("source_namespace = ?", lineage.LegacySourceNamespace).Take(&checkpoint).Error; err != nil {
			return errors.Join(ErrUsageStatisticsNotReady, err)
		}
		if normalizeFixedText(checkpoint.CollectionEpoch) != lineage.LegacyCollectionEpoch ||
			normalizeFixedText(checkpoint.FenceDigest) != lineage.FenceDigest || checkpoint.VerifiedAt <= 0 ||
			checkpoint.ImportedRows != checkpoint.ExpectedRows || checkpoint.ExpectedRows < 0 ||
			normalizeFixedText(checkpoint.RollingDigest) != normalizeFixedText(checkpoint.ManifestDigest) ||
			!isLowerHex(normalizeFixedText(checkpoint.ManifestDigest), usageEventDigestLength) {
			return ErrUsageStatisticsNotReady
		}
		source := LOG_DB
		if source == nil {
			return ErrUsageStatisticsNotReady
		}
		if source == DB {
			source = tx
		}
		if err := verifyUsageRetainedLogCoverage(tx, source, checkpoint); err != nil {
			return err
		}
		return installUsageReadLineageTx(tx, lineage)
	})
}

// verifyUsageRetainedLogCoverage binds a checkpoint to the actual retained store,
// not merely a successfully imported subrange. The global marker serves lifetime
// readers, so every retained consume/refund row must fit its fenced envelope and
// match a ready, applied primary fact. Verification is paged and never imports,
// deletes, repairs or claims complete history. Independent stores rely on the
// caller's source fence remaining in force until the marker commits.
func verifyUsageRetainedLogCoverage(primary, source *gorm.DB, checkpoint UsageEventBackfill) error {
	plan := UsageEventBackfillPlan{
		SourceNamespace: normalizeFixedText(checkpoint.SourceNamespace), SourceKind: checkpoint.SourceKind,
		CollectionEpoch: normalizeFixedText(checkpoint.CollectionEpoch), StartAt: checkpoint.StartAt, EndAt: checkpoint.EndAt,
		HighWatermark: checkpoint.HighWatermark, ExpectedRows: checkpoint.ExpectedRows,
		ManifestDigest: normalizeFixedText(checkpoint.ManifestDigest), FenceDigest: normalizeFixedText(checkpoint.FenceDigest),
	}
	if validateUsageBackfillPlan(plan, true) != nil ||
		(plan.SourceKind == UsageEventSourceLegacyClickHouse) != (source.Dialector.Name() == "clickhouse") {
		return ErrUsageStatisticsNotReady
	}
	outside := source.WithContext(primary.Statement.Context).Table("logs").
		Where("type IN ?", []int{LogTypeConsume, LogTypeRefund})
	if plan.SourceKind == UsageEventSourceLegacyRelational {
		outside = outside.Where("created_at IS NULL OR created_at < ? OR created_at > ? OR id IS NULL OR id <= 0 OR id > ?", plan.StartAt, plan.EndAt, plan.HighWatermark)
	} else {
		outside = outside.Where("created_at IS NULL OR created_at < ? OR created_at > ?", plan.StartAt, plan.EndAt)
	}
	var uncovered int64
	if err := outside.Count(&uncovered).Error; err != nil {
		return errors.Join(ErrUsageStatisticsNotReady, err)
	}
	if uncovered != 0 {
		return ErrUsageStatisticsNotReady
	}
	cursor := UsageEventBackfillCursor{}
	for {
		page, err := InspectUsageEventBackfillBatch(primary.Statement.Context, source, plan, cursor, usageEventBackfillBatchLimit)
		if err != nil {
			return errors.Join(ErrUsageStatisticsNotReady, err)
		}
		for _, occurrence := range page.rows {
			linked, err := usageBackfillVerifyLinked(primary, occurrence, false)
			if err != nil {
				return errors.Join(ErrUsageStatisticsNotReady, err)
			}
			if linked {
				continue
			}
			var stored UsageEvent
			if err := primary.Where("event_key = ?", occurrence.input.EventKey).Take(&stored).Error; err != nil {
				return errors.Join(ErrUsageStatisticsNotReady, err)
			}
			if validateUsageEventRow(stored) != nil || stored.SourceState != UsageEventSourceReady ||
				stored.AppliedVersion != UsageEventProjectionVersion || usageEventInputFromRow(stored) != occurrence.input {
				return ErrUsageStatisticsNotReady
			}
		}
		cursor = page.Next
		if cursor.Rows > plan.ExpectedRows {
			return ErrUsageStatisticsNotReady
		}
		if page.Exhausted {
			if cursor.Rows != plan.ExpectedRows || cursor.Digest != plan.ManifestDigest {
				return ErrUsageStatisticsNotReady
			}
			return nil
		}
	}
}

func (UsageStatisticsCoverage) TableName() string { return "usage_statistics_coverages" }

// MigrateUsageEventBackfill is additive and primary-only. Root owns startup
// wiring. Calling it does not install a lineage, attest coverage or run import.
func MigrateUsageEventBackfill(db *gorm.DB) error {
	if db == nil {
		return ErrUsageEventInvalid
	}
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
		return db.AutoMigrate(&UsageStatisticsLineage{}, &UsageStatisticsCoverage{}, &UsageEventBackfill{}, &UsageEventBackfillTuple{},
			&UsageStatisticsTransition{}, &UsageLegacyAcknowledgement{}, &UsageLegacyHour{})
	default:
		return ErrUsageEventInvalid
	}
}

func usageReadLineageDigest(lineage UsageReadLineage) string {
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-read-lineage/v2", true)
	appendCanonicalInt64(&encoded, 1, lineage.LegacyQuotaDataMaxID, true)
	appendCanonicalString(&encoded, 2, lineage.LegacyBaselineDigest, true)
	appendCanonicalString(&encoded, 3, lineage.FenceDigest, true)
	appendCanonicalString(&encoded, 4, lineage.LiveSourceNamespace, true)
	appendCanonicalString(&encoded, 5, lineage.LiveCollectionEpoch, true)
	appendCanonicalString(&encoded, 6, lineage.LegacySourceNamespace, true)
	appendCanonicalString(&encoded, 7, lineage.LegacyCollectionEpoch, true)
	return digestCanonical(encoded.Bytes())
}

func validUsageReadLineage(lineage UsageReadLineage) bool {
	return lineage.IncludeLegacyQuotaData && lineage.LegacyQuotaDataMaxID >= 0 &&
		isLowerHex(lineage.LegacyBaselineDigest, usageEventDigestLength) &&
		isLowerHex(lineage.FenceDigest, usageEventDigestLength) &&
		isLowerHex(lineage.LegacySourceNamespace, usageEventUIDLength) &&
		isLowerHex(lineage.LegacyCollectionEpoch, usageEventUIDLength) &&
		isLowerHex(lineage.LiveSourceNamespace, usageEventUIDLength) &&
		isLowerHex(lineage.LiveCollectionEpoch, usageEventUIDLength) &&
		lineage.DashboardEligibility == UsageEventProjectHours
}

// InstallUsageReadLineage is the proof-bearing public cutover entry point.
// Marker-only installation is impossible: the exact retained baseline proof is
// required and the same retained-set/checkpoint verification as activation is
// always performed. External writer/source/TTL fencing remains an explicit
// operator prerequisite documented by the migration procedure; this function
// does not claim to enforce it. A previously installed marker is not a public
// replay shortcut; replay after source cleanup uses the reader's internal
// idempotent activation path instead.
func InstallUsageReadLineage(ctx context.Context, lineage UsageReadLineage, proof UsageStatisticsLineageProof) error {
	if ctx == nil || DB == nil || !validUsageReadLineage(lineage) {
		return ErrUsageEventInvalid
	}
	installed, err := GetUsageReadLineage(ctx)
	if err != nil {
		return err
	}
	if installed.IncludeLegacyQuotaData {
		return ErrUsageStatisticsNotReady
	}
	return ActivateUsageStatisticsLineage(ctx, lineage, proof.ExpectedBaseline)
}

func installUsageReadLineageTx(tx *gorm.DB, lineage UsageReadLineage) error {
	candidate := UsageStatisticsLineage{
		ID: 1, LineageDigest: usageReadLineageDigest(lineage), LegacyQuotaDataMaxID: lineage.LegacyQuotaDataMaxID,
		LegacyBaselineDigest: lineage.LegacyBaselineDigest, FenceDigest: lineage.FenceDigest,
		LegacySourceNamespace: lineage.LegacySourceNamespace, LegacyCollectionEpoch: lineage.LegacyCollectionEpoch,
		LiveSourceNamespace: lineage.LiveSourceNamespace, LiveCollectionEpoch: lineage.LiveCollectionEpoch,
		InstalledAt: common.GetTimestamp(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
		return err
	}
	var stored UsageStatisticsLineage
	if err := lockForUpdate(tx).Where("id = ?", 1).Take(&stored).Error; err != nil {
		return err
	}
	stored.InstalledAt, candidate.InstalledAt = 0, 0
	if stored != candidate {
		return ErrUsageEventPayloadConflict
	}
	return nil
}

// GetUsageReadLineage returns an installed disjoint baseline, or the canonical
// live-only membership when none was installed. The default is not an
// attestation: it never includes legacy quota_data or claims complete history.
// Only a missing row chooses it; database failures remain failures.
func GetUsageReadLineage(ctx context.Context) (UsageReadLineage, error) {
	if ctx == nil || DB == nil {
		return UsageReadLineage{}, ErrUsageStatisticsNotReady
	}
	return getUsageReadLineage(DB.WithContext(ctx))
}

// UsageReadLineageInstalled reports whether the operator-installed lineage
// fence is present. A missing lineage intentionally returns the canonical
// live codec from GetUsageReadLineage so writers can keep collecting durable
// facts during a rolling upgrade; readers must continue serving their legacy
// source until this explicit cutover marker exists.
func UsageReadLineageInstalled(ctx context.Context) (bool, error) {
	lineage, err := GetUsageReadLineage(ctx)
	if err != nil {
		return false, err
	}
	return lineage.IncludeLegacyQuotaData, nil
}

// LegacyQuotaDataWriterAllowed keeps the old hourly writer active until the
// explicit lineage fence is installed. Source capture may therefore roll out
// ahead of reader cutover without making the pre-cutover dashboard lose new
// events. Any inability to inspect the fence fails closed.
func LegacyQuotaDataWriterAllowed(ctx context.Context) (bool, error) {
	installed, err := UsageReadLineageInstalled(ctx)
	if err != nil {
		return false, err
	}
	return !installed, nil
}

func getUsageReadLineage(db *gorm.DB) (UsageReadLineage, error) {
	if db == nil {
		return UsageReadLineage{}, ErrUsageStatisticsNotReady
	}
	var stored UsageStatisticsLineage
	err := db.Where("id = ?", 1).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		lineage := UsageReadLineage{
			LiveSourceNamespace: UsageLiveSourceNamespace(), LiveCollectionEpoch: UsageLiveCollectionEpoch(),
			DashboardEligibility: UsageEventProjectHours, HistoricalCoverage: UsageCoverageUnknown,
			WholeHistoryCoverage: UsageCoveragePartial,
		}
		lineage.LineageDigest = usageReadLineageDigest(lineage)
		return lineage, nil
	}
	if err != nil {
		return UsageReadLineage{}, err
	}
	lineage := UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: stored.LegacyQuotaDataMaxID,
		LegacyBaselineDigest: normalizeFixedText(stored.LegacyBaselineDigest), FenceDigest: normalizeFixedText(stored.FenceDigest),
		LiveSourceNamespace: normalizeFixedText(stored.LiveSourceNamespace), LiveCollectionEpoch: normalizeFixedText(stored.LiveCollectionEpoch),
		LegacySourceNamespace: normalizeFixedText(stored.LegacySourceNamespace), LegacyCollectionEpoch: normalizeFixedText(stored.LegacyCollectionEpoch),
		DashboardEligibility: UsageEventProjectHours, HistoricalCoverage: UsageCoveragePartial,
		WholeHistoryCoverage: UsageCoveragePartial, LineageDigest: normalizeFixedText(stored.LineageDigest),
	}
	if stored.ID != 1 || stored.InstalledAt <= 0 || !validUsageReadLineage(lineage) || lineage.LineageDigest != usageReadLineageDigest(lineage) {
		return UsageReadLineage{}, ErrUsageStatisticsNotReady
	}
	return lineage, nil
}

// AttestUsageStatisticsCoverage is a source-owner control-plane operation, not
// recovery. SourceSeal must identify an actual all-writer closed-world outcome
// verification, including losses and old buffers; a backfill checkpoint, elapsed
// time, empty apply queue or heartbeat is NOT such evidence. No current worker
// calls this automatically. An absent attestation keeps readers not-ready.
func AttestUsageStatisticsCoverage(ctx context.Context, coverage UsageStatisticsCoverage) error {
	if ctx == nil || DB == nil || coverage.Family < UsageStatisticsFacts || coverage.Family > UsageStatisticsDashboard ||
		coverage.StartAt <= 0 || coverage.EndAt < coverage.StartAt ||
		!isLowerHex(coverage.ManifestDigest, usageEventDigestLength) || !isLowerHex(coverage.SourceSeal, usageEventDigestLength) {
		return ErrUsageEventInvalid
	}
	lineage, err := GetUsageReadLineage(ctx)
	if err != nil {
		return err
	}
	if coverage.LineageDigest != lineage.LineageDigest {
		return ErrUsageStatisticsNotReady
	}
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-coverage/v1", true)
	appendCanonicalInt16(&encoded, 1, int16(coverage.Family), true)
	appendCanonicalInt64(&encoded, 2, coverage.StartAt, true)
	appendCanonicalInt64(&encoded, 3, coverage.EndAt, true)
	appendCanonicalString(&encoded, 4, coverage.LineageDigest, true)
	key := digestCanonical(encoded.Bytes())
	if coverage.CoverageKey != "" && coverage.CoverageKey != key {
		return ErrUsageEventIdentityConflict
	}
	coverage.CoverageKey, coverage.VerifiedAt = key, common.GetTimestamp()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := reserveUsageEventSQLiteWriter(tx); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&coverage).Error; err != nil {
			return err
		}
		var stored UsageStatisticsCoverage
		if err := lockForUpdate(tx).Where("coverage_key = ?", key).Take(&stored).Error; err != nil {
			return err
		}
		stored.VerifiedAt, coverage.VerifiedAt = 0, 0
		if stored != coverage {
			return ErrUsageEventPayloadConflict
		}
		return nil
	})
}

// GetUsageStatisticsCoverage permits proven ready subtotals without requiring
// a complete-history certificate. Only explicit finite all-source attestations
// can mark Complete. MIN/MAX timestamps, an empty backfill/apply queue and a
// retained-row manifest cannot supply that evidence.
func GetUsageStatisticsCoverage(ctx context.Context, family UsageStatisticsFamily, start, end int64) (UsageStatisticsCoverageView, error) {
	view := UsageStatisticsCoverageView{
		Coverage: UsageCoveragePartial, HistoricalCoverage: UsageCoverageUnknown, LiveCoverage: UsageCoveragePartial,
		StartTimestamp: start, EndTimestamp: end,
	}
	if ctx == nil || DB == nil || family < UsageStatisticsFacts || family > UsageStatisticsDashboard ||
		start < 0 || end < 0 || (start > 0 && end > 0 && end < start) {
		return view, ErrUsageEventInvalid
	}
	lineage, err := GetUsageReadLineage(ctx)
	if err != nil {
		return view, err
	}
	if family == UsageStatisticsDashboard {
		view.LegacyBaselineIncluded = lineage.IncludeLegacyQuotaData
		view.HistoricalCoverage = lineage.HistoricalCoverage
	}
	query := DB.WithContext(ctx).Where("family = ? AND lineage_digest = ?", family, lineage.LineageDigest)
	if end > 0 {
		query = query.Where("start_at <= ?", end)
	}
	if start > 0 {
		query = query.Where("end_at >= ?", start)
	}
	var intervals []UsageStatisticsCoverage
	if err := query.Order("start_at, end_at").Limit(101).Find(&intervals).Error; err != nil {
		return view, err
	}
	if start == 0 || end == 0 || len(intervals) == 0 || len(intervals) > 100 {
		return view, nil
	}
	covered := start
	for _, interval := range intervals {
		if interval.VerifiedAt <= 0 || interval.StartAt <= 0 || interval.EndAt < interval.StartAt ||
			!isLowerHex(normalizeFixedText(interval.ManifestDigest), usageEventDigestLength) ||
			!isLowerHex(normalizeFixedText(interval.SourceSeal), usageEventDigestLength) {
			return view, ErrUsageStatisticsNotReady
		}
		if interval.StartAt > covered {
			return view, nil
		}
		if interval.EndAt >= end {
			view.Complete = true
			view.Coverage, view.HistoricalCoverage, view.LiveCoverage = UsageCoverageComplete, UsageCoverageComplete, UsageCoverageComplete
			return view, nil
		}
		// EndAt < end keeps this addition below MaxInt64.
		covered = max(covered, interval.EndAt+1)
	}
	return view, nil
}

// RequireUsageStatisticsCoverage is the explicit complete-evidence API. It is
// deliberately stricter than serving a labelled partial subtotal.
func RequireUsageStatisticsCoverage(ctx context.Context, family UsageStatisticsFamily, start, end int64) error {
	view, err := GetUsageStatisticsCoverage(ctx, family, start, end)
	if err != nil {
		return err
	}
	if !view.Complete {
		return ErrUsageStatisticsNotReady
	}
	return nil
}
