package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// openUsageEventTestDatabase owns only the four foundation tables. It uses a
// file-backed SQLite database so separate connections exercise real locking.
func openUsageEventTestDatabase(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	previousPath := common.SQLitePath
	var dsn string
	switch dialect {
	case "sqlite":
		common.SQLitePath = filepath.Join(t.TempDir(), "usage-events.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)"
		dsn = "local"
	case "mysql":
		dsn = os.Getenv("TEST_MYSQL_DSN")
	case "postgres":
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	default:
		t.Fatalf("unsupported usage-event test dialect %q", dialect)
	}
	if dsn == "" {
		t.Skip("test database DSN is not configured")
	}
	t.Setenv("USAGE_EVENT_TEST_DSN", dsn)
	db, databaseType, err := chooseDB("USAGE_EVENT_TEST_DSN", false)
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(4)
	previousDB, previousType := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(databaseType)
	initCol()
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.SQLitePath = previousPath
		initCol()
		require.NoError(t, connection.Close())
	})
	tables := []any{&UsageEvent{}, &UsageHour{}, &UsageChannelModel{}, &UsageChannel{}}
	require.NoError(t, db.Migrator().DropTable(tables...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
	require.NoError(t, MigrateUsageStatistics(db))
	var version string
	query := "SELECT version()"
	if dialect == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("engine=%s version=%s", dialect, version)
	return db
}

func clearUsageEventTestRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []any{&UsageEvent{}, &UsageHour{}, &UsageChannelModel{}, &UsageChannel{}} {
		require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
	}
}

func usageEventTestInput(sequence int64) UsageEventInput {
	return UsageEventInput{
		Identity: UsageEventIdentity{
			SourceNamespace: strings.Repeat("a", 32), SourceKind: UsageEventSourceRelay,
			OccurrenceUID: fmt.Sprintf("%032x", sequence),
		},
		CollectionEpoch: strings.Repeat("b", 32), Kind: UsageEventKindConsume,
		Eligibility: UsageEventProjectHours | UsageEventProjectChannel,
		KnownMask: usageEventKnownAll,
		OccurredAt: 1700000123, UserID: 91230, TokenID: 91231, ChannelID: 91232,
		Username: "usage-owner", TokenName: "historical-token", ModelName: "model-α", GroupName: "group-甲",
		RequestID: "shared-request", UpstreamRequestID: "shared-upstream", WriterNode: "writer", ExportNode: "ingress",
		PromptTokens: 100, CompletionTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 5,
		ReportedInputTokens: 999, QuotaMagnitude: 7, UsageSemantic: UsageSemanticOpenAI,
	}
}

type usageEventProjectionSnapshot struct {
	Channels []UsageChannel
	Models   []UsageChannelModel
	Hours    []UsageHour
}

func readUsageEventProjections(t *testing.T, db *gorm.DB) usageEventProjectionSnapshot {
	t.Helper()
	var snapshot usageEventProjectionSnapshot
	require.NoError(t, db.Order("channel_id").Find(&snapshot.Channels).Error)
	require.NoError(t, db.Order("id").Find(&snapshot.Models).Error)
	require.NoError(t, db.Order("id").Find(&snapshot.Hours).Error)
	return snapshot
}

func TestUsageEventFoundationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openUsageEventTestDatabase(t, dialect)

			t.Run("canonical_claim_replay_and_full_payload_conflict", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				input := usageEventTestInput(1)
				key, err := CanonicalUsageEventKey(input.Identity)
				require.NoError(t, err)
				input.EventKey = strings.Repeat("c", 64)
				_, err = PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
				require.ErrorIs(t, err, ErrUsageEventIdentityConflict)
				input.EventKey = key
				receipt, err := PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
				require.NoError(t, err)
				var before UsageEvent
				require.NoError(t, db.Where("event_key = ?", key).Take(&before).Error)
				replayed, err := PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
				require.NoError(t, err)
				assert.Equal(t, receipt, replayed)
				var after UsageEvent
				require.NoError(t, db.Where("event_key = ?", key).Take(&after).Error)
				assert.Equal(t, before, after, "duplicate claim cannot alter timestamps or immutable fields")
				require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				snapshot := readUsageEventProjections(t, db)
				require.Len(t, snapshot.Channels, 1)
				require.Len(t, snapshot.Models, 1)
				require.Len(t, snapshot.Hours, 1)
				assert.EqualValues(t, 1, snapshot.Channels[0].ConsumeCount)
				assert.EqualValues(t, 7, snapshot.Channels[0].GrossQuota)
				assert.EqualValues(t, 100, snapshot.Channels[0].PromptTokens)
				assert.EqualValues(t, 20, snapshot.Channels[0].CompletionTokens)
				assert.EqualValues(t, input.OccurredAt, snapshot.Models[0].LastUsedAt)
				assert.EqualValues(t, 120, snapshot.Hours[0].RankingTokens)
				assert.EqualValues(t, 100, snapshot.Hours[0].InputDenominator, "explicit input total is not the established cache denominator")

				changed := input
				changed.ModelName = "MODEL-α"
				_, err = PrepareUsageEvent(t.Context(), changed, UsageEventSourceReady)
				require.ErrorIs(t, err, ErrUsageEventPayloadConflict)
				changed = input
				changed.KnownMask &^= UsageEventKnownReportedInputTokens
				changed.ReportedInputTokens = 0
				_, err = PrepareUsageEvent(t.Context(), changed, UsageEventSourceReady)
				require.ErrorIs(t, err, ErrUsageEventPayloadConflict)
				assert.Equal(t, snapshot, readUsageEventProjections(t, db))

				// Simulate a colliding digest supplied by storage: full typed
				// payload comparison still rejects a different immutable fact.
				changed = input
				changed.Username = "different-owner"
				forgedDigest := usageEventPayloadDigest(changed)
				require.NoError(t, db.Model(&UsageEvent{}).Where("event_key = ?", key).Update("payload_digest", forgedDigest).Error)
				_, err = PrepareUsageEvent(t.Context(), changed, UsageEventSourceReady)
				require.ErrorIs(t, err, ErrUsageEventPayloadConflict)
				require.NoError(t, db.Model(&UsageEvent{}).Where("event_key = ?", key).Update("payload_digest", receipt.PayloadDigest).Error)
				assert.Equal(t, snapshot, readUsageEventProjections(t, db))
			})

			t.Run("hour_dimensions_isolate_namespace_and_epoch_without_replaying_facts", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				first := usageEventTestInput(30)
				second := usageEventTestInput(31)
				second.Identity.SourceNamespace = strings.Repeat("c", 32)
				third := usageEventTestInput(32)
				third.CollectionEpoch = strings.Repeat("d", 32)
				for _, input := range []UsageEventInput{first, second, third} {
					receipt, err := PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
					require.NoError(t, err)
					require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
					require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				}
				snapshot := readUsageEventProjections(t, db)
				require.Len(t, snapshot.Hours, 3, "equal hour/filter dimensions cannot merge distinct source memberships")
				require.Len(t, snapshot.Channels, 1)
				assert.EqualValues(t, 3, snapshot.Channels[0].ConsumeCount)
				for _, input := range []UsageEventInput{first, second, third} {
					var hour UsageHour
					require.NoError(t, db.Where("source_namespace = ? AND collection_epoch = ?", input.Identity.SourceNamespace, input.CollectionEpoch).Take(&hour).Error)
					assert.EqualValues(t, 1, hour.ConsumeCount)
					assert.Equal(t, input.QuotaMagnitude, hour.GrossQuota)
					assert.Equal(t, input.PromptTokens+input.CompletionTokens, hour.RankingTokens)
				}
				var event UsageEvent
				require.NoError(t, db.Where("occurrence_uid = ?", first.Identity.OccurrenceUID).Take(&event).Error)
				var hour UsageHour
				require.NoError(t, db.Where("source_namespace = ? AND collection_epoch = ?", first.Identity.SourceNamespace, first.CollectionEpoch).Take(&hour).Error)
				require.NoError(t, db.Model(&UsageHour{}).Where("id = ?", hour.ID).Update("source_namespace", second.Identity.SourceNamespace).Error)
				before := readUsageEventProjections(t, db)
				// A full-tuple collision fails atomically, not by trusting the
				// digest or by attaching a foreign namespace to old counters.
				replay := usageEventTestInput(33)
				receipt, err := PrepareUsageEvent(t.Context(), replay, UsageEventSourceReady)
				require.NoError(t, err)
				require.ErrorIs(t, ApplyUsageEvent(t.Context(), receipt), ErrUsageEventDimensionConflict)
				assert.Equal(t, before, readUsageEventProjections(t, db))
				var untouched UsageEvent
				require.NoError(t, db.Where("event_key = ?", receipt.EventKey).Take(&untouched).Error)
				assert.Zero(t, untouched.AppliedVersion)
				assert.Equal(t, first.Identity.SourceNamespace, normalizeFixedText(event.SourceNamespace))
			})

			t.Run("source_confirmation_never_promotes_on_prepare_or_recovery", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				input := usageEventTestInput(2)
				receipt, err := PrepareUsageEvent(t.Context(), input, UsageEventSourcePending)
				require.NoError(t, err)
				replayed, err := PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
				require.NoError(t, err)
				assert.Equal(t, receipt, replayed)
				require.ErrorIs(t, ApplyUsageEvent(t.Context(), receipt), ErrUsageEventNotReady)
				count, err := RecoverUsageEventProjections(t.Context(), 10)
				require.NoError(t, err)
				assert.Zero(t, count)
				assert.Empty(t, readUsageEventProjections(t, db).Channels)
				require.NoError(t, ConfirmUsageEvent(t.Context(), receipt, UsageEventOutcome{State: UsageEventSourceUnknown}))
				require.ErrorIs(t, ConfirmUsageEvent(t.Context(), receipt, UsageEventOutcome{State: UsageEventSourceReady}), ErrUsageEventStateConflict)
				outcome := UsageEventOutcome{State: UsageEventSourceReady, EvidenceRef: "same-occurrence-evidence"}
				require.NoError(t, ConfirmUsageEvent(t.Context(), receipt, outcome))
				require.NoError(t, ConfirmUsageEvent(t.Context(), receipt, outcome))
				require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				require.ErrorIs(t, ConfirmUsageEvent(t.Context(), receipt, UsageEventOutcome{State: UsageEventNotReported}), ErrUsageEventStateConflict)

				unreported, err := PrepareUsageEvent(t.Context(), usageEventTestInput(3), UsageEventSourcePending)
				require.NoError(t, err)
				require.NoError(t, ConfirmUsageEvent(t.Context(), unreported, UsageEventOutcome{State: UsageEventNotReported}))
				require.ErrorIs(t, ApplyUsageEvent(t.Context(), unreported), ErrUsageEventNotReady)
				count, err = RecoverUsageEventProjections(t.Context(), 10)
				require.NoError(t, err)
				assert.Zero(t, count)
				assert.EqualValues(t, 1, readUsageEventProjections(t, db).Channels[0].ConsumeCount)
			})

			t.Run("atomic_projection_rollback_and_delayed_recovery", func(t *testing.T) {
				for _, failedTable := range []string{"usage_channel_models", "usage_hours", "usage_events"} {
					t.Run(failedTable, func(t *testing.T) {
						clearUsageEventTestRows(t, db)
						receipt, err := PrepareUsageEvent(t.Context(), usageEventTestInput(4), UsageEventSourceReady)
						require.NoError(t, err)
						const callback = "test:usage_event_projection_failure"
						injected := errors.New("injected projection storage failure")
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
							if tx.Statement.Table == failedTable {
								tx.AddError(injected)
							}
						}))
						applyErr := ApplyUsageEvent(t.Context(), receipt)
						require.NoError(t, db.Callback().Update().Remove(callback))
						require.ErrorIs(t, applyErr, injected)
						snapshot := readUsageEventProjections(t, db)
						assert.Empty(t, snapshot.Channels)
						assert.Empty(t, snapshot.Models)
						assert.Empty(t, snapshot.Hours)
						var stored UsageEvent
						require.NoError(t, db.Where("event_key = ?", receipt.EventKey).Take(&stored).Error)
						assert.EqualValues(t, 0, stored.AppliedVersion)
						assert.Equal(t, receipt.PayloadDigest, normalizeFixedText(stored.PayloadDigest))

						// Recovery persists a due-time delay but never consumes or
						// discards the ready fact when projection fails again.
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
							if tx.Statement.Table == "usage_hours" {
								tx.AddError(injected)
							}
						}))
						count, recoverErr := RecoverUsageEventProjections(t.Context(), 1)
						require.NoError(t, db.Callback().Update().Remove(callback))
						require.ErrorIs(t, recoverErr, injected)
						assert.Zero(t, count)
						require.NoError(t, db.Where("event_key = ?", receipt.EventKey).Take(&stored).Error)
						assert.EqualValues(t, 1, stored.Attempts)
						assert.Greater(t, stored.NextAttemptAt, common.GetTimestamp())
						assert.Equal(t, "statistics_apply_failed", stored.ErrorCode)
						count, err = RecoverUsageEventProjections(t.Context(), 1)
						require.NoError(t, err)
						assert.Zero(t, count, "future retry is excluded from the due page")
						require.NoError(t, db.Model(&UsageEvent{}).Where("event_key = ?", receipt.EventKey).Update("next_attempt_at", int64(0)).Error)
						count, err = RecoverUsageEventProjections(t.Context(), 1)
						require.NoError(t, err)
						assert.Equal(t, 1, count)
						count, err = RecoverUsageEventProjections(t.Context(), 1)
						require.NoError(t, err)
						assert.Zero(t, count)
						snapshot = readUsageEventProjections(t, db)
						require.Len(t, snapshot.Channels, 1)
						assert.EqualValues(t, 1, snapshot.Channels[0].ConsumeCount)
						require.Len(t, snapshot.Models, 1)
						require.Len(t, snapshot.Hours, 1)
					})
				}
			})

			t.Run("overflow_does_not_commit_any_target_or_marker", func(t *testing.T) {
				for _, table := range []string{"usage_channels", "usage_channel_models", "usage_hours"} {
					t.Run(table, func(t *testing.T) {
						clearUsageEventTestRows(t, db)
						first, err := PrepareUsageEvent(t.Context(), usageEventTestInput(5), UsageEventSourceReady)
						require.NoError(t, err)
						require.NoError(t, ApplyUsageEvent(t.Context(), first))
						require.NoError(t, db.Table(table).Where("1 = 1").Update("consume_count", int64(math.MaxInt64)).Error)
						before := readUsageEventProjections(t, db)
						second, err := PrepareUsageEvent(t.Context(), usageEventTestInput(6), UsageEventSourceReady)
						require.NoError(t, err)
						require.ErrorIs(t, ApplyUsageEvent(t.Context(), second), ErrUsageEventOverflow)
						assert.Equal(t, before, readUsageEventProjections(t, db))
						var stored UsageEvent
						require.NoError(t, db.Where("event_key = ?", second.EventKey).Take(&stored).Error)
						assert.Zero(t, stored.AppliedVersion)
					})
				}
				clearUsageEventTestRows(t, db)
				input := usageEventTestInput(7)
				input.PromptTokens, input.CompletionTokens = math.MaxInt64, 1
				receipt, err := PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
				require.NoError(t, err)
				require.ErrorIs(t, ApplyUsageEvent(t.Context(), receipt), ErrUsageEventOverflow)
				assert.Empty(t, readUsageEventProjections(t, db).Channels)
			})

			t.Run("explicit_semantics_unknownness_refunds_and_timestamp_max", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				first := usageEventTestInput(8)
				first.UsageSemantic = UsageSemanticAnthropic
				receipt, err := PrepareUsageEvent(t.Context(), first, UsageEventSourceReady)
				require.NoError(t, err)
				require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				second := usageEventTestInput(9)
				second.OccurredAt -= 10
				second.UsageSemantic = UsageSemanticUnknown
				second.KnownMask &^= UsageEventKnownUsageSemantic | UsageEventKnownCacheReadTokens | UsageEventKnownCacheWriteTokens
				second.CacheReadTokens, second.CacheWriteTokens = 0, 0
				receipt, err = PrepareUsageEvent(t.Context(), second, UsageEventSourceReady)
				require.NoError(t, err)
				require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				snapshot := readUsageEventProjections(t, db)
				require.Len(t, snapshot.Channels, 1)
				require.Len(t, snapshot.Models, 1)
				require.Len(t, snapshot.Hours, 1)
				assert.EqualValues(t, first.OccurredAt, snapshot.Channels[0].LastUsedAt)
				assert.EqualValues(t, first.OccurredAt, snapshot.Models[0].LastUsedAt)
				assert.EqualValues(t, 2, snapshot.Channels[0].ConsumeCount)
				assert.EqualValues(t, 275, snapshot.Hours[0].RankingTokens)
				assert.EqualValues(t, 235, snapshot.Hours[0].InputDenominator)
				assert.EqualValues(t, 1, snapshot.Hours[0].CacheUnknownCount)
				assert.EqualValues(t, 1, snapshot.Hours[0].SemanticUnknownCount)
				refund := usageEventTestInput(10)
				refund.Kind, refund.Eligibility = UsageEventKindRefund, 0
				receipt, err = PrepareUsageEvent(t.Context(), refund, UsageEventSourceReady)
				require.NoError(t, err)
				require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
				assert.Equal(t, snapshot, readUsageEventProjections(t, db), "refund is a positive fact, not a subtraction from gross consumption")
			})

			t.Run("two_connection_first_claim_and_apply_once", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				input := usageEventTestInput(11)
				connectionsReady := make(chan struct{}, 2)
				start := make(chan struct{})
				results := make(chan error, 2)
				var workers sync.WaitGroup
				for range 2 {
					workers.Go(func() {
						err := db.WithContext(t.Context()).Connection(func(connection *gorm.DB) error {
							connectionsReady <- struct{}{}
							<-start
							return connection.Transaction(func(tx *gorm.DB) error {
								receipt, err := PrepareUsageEventTx(tx, input, UsageEventSourceReady)
								if err != nil {
									return err
								}
								return ApplyUsageEventTx(tx, receipt)
							})
						})
						results <- err
					})
				}
				<-connectionsReady
				<-connectionsReady
				close(start)
				workers.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				var facts int64
				require.NoError(t, db.Model(&UsageEvent{}).Count(&facts).Error)
				assert.EqualValues(t, 1, facts)
				snapshot := readUsageEventProjections(t, db)
				require.Len(t, snapshot.Channels, 1)
				assert.EqualValues(t, 1, snapshot.Channels[0].ConsumeCount)
				require.Len(t, snapshot.Models, 1)
				assert.EqualValues(t, 1, snapshot.Models[0].ConsumeCount)
				require.Len(t, snapshot.Hours, 1)
				assert.EqualValues(t, 1, snapshot.Hours[0].ConsumeCount)
			})

			t.Run("source_report_freezes_identity_and_optional_log_link", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				input := usageEventTestInput(20)
				input.Identity = UsageEventIdentity{}
				input.CollectionEpoch = ""
				report, err := NewUsageSourceReport(t.Context(), UsageEventSourceRelay, UsageEventKindConsume, input)
				require.NoError(t, err)
				otherReport, err := NewUsageSourceReport(t.Context(), UsageEventSourceRelay, UsageEventKindConsume, input)
				require.NoError(t, err)
				assert.NotEqual(t, report.Input.EventKey, otherReport.Input.EventKey, "same request can contain distinct reporting occurrences")
				var count int64
				require.NoError(t, db.Model(&UsageEvent{}).Count(&count).Error)
				assert.Zero(t, count, "freezing performs no SQL write")
				require.NoError(t, PrepareUsageSourceReport(t.Context(), report))
				before := readUsageEventProjections(t, db)
				assert.Empty(t, before.Channels)
				require.NoError(t, ConfirmUsageSourceReport(t.Context(), report, UsageEventSourceUnknown, "source_outcome_unknown"))
				_, err = RecoverUsageEventProjections(t.Context(), 100)
				require.NoError(t, err)
				assert.Empty(t, readUsageEventProjections(t, db).Channels)
				require.NoError(t, ConfirmUsageSourceReport(t.Context(), report, UsageEventSourceReady, "source_report_observed"))
				require.NoError(t, PrepareUsageSourceReport(t.Context(), report))
				require.NoError(t, ConfirmUsageSourceReport(t.Context(), report, UsageEventSourceReady, "source_report_observed"))
				after := readUsageEventProjections(t, db)
				require.Len(t, after.Channels, 1)
				assert.EqualValues(t, 1, after.Channels[0].ConsumeCount)
				frozen := report.Input
				report.Input.QuotaMagnitude++
				require.ErrorIs(t, PrepareUsageSourceReport(t.Context(), report), ErrUsageEventPayloadConflict)
				report.Input = frozen
				log := &Log{Type: LogTypeConsume, UserId: int(frozen.UserID), TokenId: int(frozen.TokenID), ChannelId: int(frozen.ChannelID),
					ModelName: frozen.ModelName, Group: frozen.GroupName, Quota: int(frozen.QuotaMagnitude),
					PromptTokens: int(frozen.PromptTokens), CompletionTokens: int(frozen.CompletionTokens)}
				other := NewLogOther()
				other.SetPublic("safe", "display")
				bound, err := bindUsageSourceReport(log, other, report)
				require.NoError(t, err)
				assert.Equal(t, frozen.OccurredAt, log.CreatedAt)
				assert.Equal(t, frozen.Username, log.Username)
				assert.Equal(t, frozen.RequestID, log.RequestId)
				assert.Contains(t, bound.JSONString(), frozen.EventKey)
				assert.NotContains(t, other.JSONString(), frozen.EventKey, "binding does not mutate caller metadata")
				assert.NotContains(t, formatLogOtherJSON(bound.JSONString(), logOtherVisibilityUser), frozen.EventKey)
				log.Quota++
				_, err = bindUsageSourceReport(log, other, report)
				require.ErrorIs(t, err, ErrUsageEventPayloadConflict)
			})

			t.Run("installed_lineage_guard_is_membership_not_coverage_or_activation", func(t *testing.T) {
				require.NoError(t, MigrateUsageEventBackfill(db))
				require.NoError(t, db.Where("id = ?", 1).Delete(&UsageStatisticsLineage{}).Error)
				t.Cleanup(func() { require.NoError(t, db.Where("id = ?", 1).Delete(&UsageStatisticsLineage{}).Error) })
				require.NoError(t, ValidateInstalledUsageLiveLineage(t.Context()), "missing historical manifest must not prevent live capture")
				var count int64
				require.NoError(t, db.Model(&UsageStatisticsLineage{}).Count(&count).Error)
				assert.Zero(t, count, "startup cannot manufacture an all-writer fence or baseline")
				lineage := UsageReadLineage{
					IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: 0,
					LegacyBaselineDigest: strings.Repeat("c", 64), FenceDigest: strings.Repeat("d", 64),
					LegacySourceNamespace: strings.Repeat("1", 32), LegacyCollectionEpoch: strings.Repeat("2", 32),
					LiveSourceNamespace: UsageLiveSourceNamespace(), LiveCollectionEpoch: UsageLiveCollectionEpoch(),
					DashboardEligibility: UsageEventProjectHours,
				}
				require.NoError(t, ValidateUsageLiveReadLineage(lineage))
				require.NoError(t, installUsageReadLineageTestOnly(t.Context(), lineage))
				require.NoError(t, ValidateInstalledUsageLiveLineage(t.Context()))
				for _, column := range []string{"live_source_namespace", "live_collection_epoch"} {
					t.Run(column, func(t *testing.T) {
						conflicting := lineage
						value := strings.Repeat("e", 32)
						if column == "live_source_namespace" {
							conflicting.LiveSourceNamespace = value
						} else {
							conflicting.LiveCollectionEpoch = value
						}
						require.NoError(t, db.Model(&UsageStatisticsLineage{}).Where("id = ?", 1).Updates(map[string]any{
							column: value, "lineage_digest": usageReadLineageDigest(conflicting),
						}).Error)
						require.ErrorIs(t, ValidateInstalledUsageLiveLineage(t.Context()), ErrUsageEventStateConflict)
						require.NoError(t, db.Model(&UsageStatisticsLineage{}).Where("id = ?", 1).Updates(map[string]any{
							"live_source_namespace": lineage.LiveSourceNamespace, "live_collection_epoch": lineage.LiveCollectionEpoch,
							"lineage_digest": usageReadLineageDigest(lineage),
						}).Error)
					})
				}
				conflictingLegacy := lineage
				conflictingLegacy.LegacySourceNamespace = strings.Repeat("e", 32)
				assert.ErrorIs(t, installUsageReadLineageTestOnly(t.Context(), conflictingLegacy), ErrUsageEventPayloadConflict, "installed legacy identity cannot be replaced")
				storedLineage, err := GetUsageReadLineage(t.Context())
				require.NoError(t, err)
				assert.Equal(t, lineage.LegacySourceNamespace, storedLineage.LegacySourceNamespace)
				assert.Equal(t, lineage.LegacyCollectionEpoch, storedLineage.LegacyCollectionEpoch)
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				require.Error(t, ValidateInstalledUsageLiveLineage(cancelled), "lookup errors are not absent manifests")
				require.Error(t, ValidateInstalledUsageLiveLineage(nil))
			})

			t.Run("transaction_variants_use_only_the_caller_connection", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				rollback := errors.New("rollback source fixture")
				DB = nil
				err := db.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
					receipt, err := PrepareUsageEventTx(tx, usageEventTestInput(12), UsageEventSourcePending)
					if err != nil {
						return err
					}
					if err := ConfirmUsageEventTx(tx, receipt, UsageEventOutcome{State: UsageEventSourceReady}); err != nil {
						return err
					}
					if err := ApplyUsageEventTx(tx, receipt); err != nil {
						return err
					}
					return rollback
				})
				DB = db
				require.ErrorIs(t, err, rollback)
				var facts int64
				require.NoError(t, db.Model(&UsageEvent{}).Count(&facts).Error)
				assert.Zero(t, facts)
				assert.Empty(t, readUsageEventProjections(t, db).Channels)
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				_, err = PrepareUsageEvent(cancelled, usageEventTestInput(13), UsageEventSourceReady)
				require.ErrorIs(t, err, context.Canceled)
			})
		})
	}
}

func TestUsageEventInputBoundsAndIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*UsageEventInput)
	}{
		{"oversize_model", func(input *UsageEventInput) { input.ModelName = strings.Repeat("界", 5462) }},
		{"invalid_utf8", func(input *UsageEventInput) { input.Username = string([]byte{0xff}) }},
		{"nul", func(input *UsageEventInput) { input.TokenName = "bad\x00name" }},
		{"negative_quantity", func(input *UsageEventInput) { input.PromptTokens = -1 }},
		{"unknown_nonzero_cache", func(input *UsageEventInput) { input.KnownMask &^= UsageEventKnownCacheReadTokens }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := usageEventTestInput(1)
			test.modify(&input)
			_, _, _, err := validateUsageEventInput(input, UsageEventSourceReady)
			require.ErrorIs(t, err, ErrUsageEventInvalid)
		})
	}
	identity := UsageEventIdentity{SourceNamespace: strings.Repeat("a", 32), SourceKind: UsageEventSourceLegacyRelational, SourceRowID: 42}
	key, err := CanonicalUsageEventKey(identity)
	require.NoError(t, err)
	identity.SourceRowID++
	otherKey, err := CanonicalUsageEventKey(identity)
	require.NoError(t, err)
	assert.NotEqual(t, key, otherKey)
	identity.SourceKind, identity.SourceRowID = UsageEventSourceLegacyClickHouse, 0
	identity.SourceTupleDigest, identity.SourceOrdinal = strings.Repeat("b", 64), 1
	key, err = CanonicalUsageEventKey(identity)
	require.NoError(t, err)
	identity.SourceOrdinal++
	otherKey, err = CanonicalUsageEventKey(identity)
	require.NoError(t, err)
	assert.NotEqual(t, key, otherKey, "identical physical tuples retain multiplicity")
}

func TestInstallUsageReadLineageRequiresRetainedSetProof(t *testing.T) {
	db := openUsageEventTestDatabase(t, "sqlite")
	previousLogDB, previousType := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousType)
		initCol()
	})
	require.NoError(t, db.AutoMigrate(&QuotaData{}, &Log{}))
	require.NoError(t, MigrateUsageEventBackfill(db))
	baseline, err := InspectUsageLegacyQuotaData(t.Context())
	require.NoError(t, err)
	lineage := UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: baseline.MaxID,
		LegacyBaselineDigest: baseline.Digest, FenceDigest: strings.Repeat("c", 64),
		LegacySourceNamespace: strings.Repeat("a", 32), LegacyCollectionEpoch: strings.Repeat("b", 32),
		LiveSourceNamespace: UsageLiveSourceNamespace(), LiveCollectionEpoch: UsageLiveCollectionEpoch(),
		DashboardEligibility: UsageEventProjectHours,
	}
	proof := UsageStatisticsLineageProof{ExpectedBaseline: baseline}

	// A baseline digest alone cannot install the global marker without the
	// retained-source checkpoint and exact linked readiness proof.
	require.ErrorIs(t, InstallUsageReadLineage(t.Context(), lineage, proof), ErrUsageStatisticsNotReady)
	var installed int64
	require.NoError(t, db.Model(&UsageStatisticsLineage{}).Count(&installed).Error)
	assert.Zero(t, installed)

	plan := UsageEventBackfillPlan{
		SourceNamespace: lineage.LegacySourceNamespace, SourceKind: UsageEventSourceLegacyRelational,
		CollectionEpoch: lineage.LegacyCollectionEpoch, StartAt: 1000, EndAt: 2000,
		HighWatermark: 0, FenceDigest: lineage.FenceDigest,
	}
	page, err := InspectUsageEventBackfillBatch(t.Context(), db, plan, UsageEventBackfillCursor{}, 100)
	require.NoError(t, err)
	require.True(t, page.Exhausted)
	plan.ExpectedRows, plan.ManifestDigest = page.Next.Rows, page.Next.Digest
	progress, err := RunUsageEventBackfillBatch(t.Context(), db, plan, 100)
	require.NoError(t, err)
	require.True(t, progress.RetainedVerified)

	// The checkpoint covers an empty [1000,2000] set. A later retained consume
	// row outside that extent must keep public activation fail-closed.
	require.NoError(t, db.Create(&Log{UserId: 7, CreatedAt: 2001, Type: LogTypeConsume, Quota: 3}).Error)
	require.ErrorIs(t, InstallUsageReadLineage(t.Context(), lineage, proof), ErrUsageStatisticsNotReady)
	require.NoError(t, db.Model(&UsageStatisticsLineage{}).Count(&installed).Error)
	assert.Zero(t, installed)
}

func TestDeleteOldLogBatchRejectsLegacyNodeMutation(t *testing.T) {
	db := openUsageEventTestDatabase(t, "sqlite")
	previousLogDB, previousType := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousType)
		initCol()
	})
	require.NoError(t, MigrateUsageEventBackfill(db))
	require.NoError(t, db.AutoMigrate(&Log{}))

	namespace := strings.Repeat("a", usageEventUIDLength)
	epoch := strings.Repeat("b", usageEventUIDLength)
	manifest := strings.Repeat("c", usageEventDigestLength)
	fence := strings.Repeat("d", usageEventDigestLength)
		require.NoError(t, installUsageReadLineageTestOnly(t.Context(), UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: 0, LegacyBaselineDigest: manifest, FenceDigest: fence,
		LegacySourceNamespace: namespace, LegacyCollectionEpoch: epoch, LiveSourceNamespace: UsageLiveSourceNamespace(),
		LiveCollectionEpoch: UsageLiveCollectionEpoch(), DashboardEligibility: UsageEventProjectHours,
	}))

	legacyOther := func(writerNode, exportNode string) string {
		encoded, err := common.Marshal(map[string]any{
			"admin_info": map[string]any{
				"node_name": writerNode, "submit_node_name": exportNode,
			},
		})
		require.NoError(t, err)
		return string(encoded)
	}
	log := &Log{
		UserId: 7, Username: "legacy-user", CreatedAt: 1000, Type: LogTypeConsume, TokenName: "legacy-token",
		ModelName: "legacy-model", PromptTokens: 10, CompletionTokens: 20, Quota: 30, ChannelId: 4, TokenId: 5,
		Group: "legacy-group", RequestId: "legacy-request", UpstreamRequestId: "legacy-upstream",
		Other: legacyOther("writer-a", "export-a"),
	}
	require.NoError(t, db.Create(log).Error)
	plan := UsageEventBackfillPlan{
		SourceNamespace: namespace, SourceKind: UsageEventSourceLegacyRelational, CollectionEpoch: epoch,
		StartAt: log.CreatedAt, EndAt: log.CreatedAt, HighWatermark: int64(log.Id), ExpectedRows: 1,
		ManifestDigest: manifest, FenceDigest: fence,
	}
	row := usageBackfillRow{
		ID: int64(log.Id), UserID: int64(log.UserId), OccurredAt: log.CreatedAt, Type: log.Type, Username: log.Username,
		TokenName: log.TokenName, ModelName: log.ModelName, Quota: int64(log.Quota), PromptTokens: int64(log.PromptTokens),
		CompletionTokens: int64(log.CompletionTokens), ChannelID: int64(log.ChannelId), TokenID: int64(log.TokenId),
		GroupName: log.Group, RequestID: log.RequestId, UpstreamRequestID: log.UpstreamRequestId,
		WriterNode: `"writer-a"`, ExportNode: `"export-a"`, Multiplicity: 1,
	}
	input, err := usageBackfillInput(plan, row, "", 1)
	require.NoError(t, err)
	receipt, err := PrepareUsageEvent(t.Context(), input, UsageEventSourceReady)
	require.NoError(t, err)
	require.NoError(t, ApplyUsageEvent(t.Context(), receipt))
	require.NoError(t, db.Create(&UsageEventBackfill{
		SourceNamespace: namespace, SourceKind: UsageEventSourceLegacyRelational, CollectionEpoch: epoch,
		StartAt: log.CreatedAt, EndAt: log.CreatedAt, HighWatermark: int64(log.Id), ExpectedRows: 1,
		ManifestDigest: manifest, FenceDigest: fence, Revision: 1, LastRowID: int64(log.Id),
		ImportedRows: 1, RollingDigest: manifest, VerifiedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp(),
	}).Error)

	require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Update("other", legacyOther("writer-b", "export-a")).Error)
	_, err = DeleteOldLogBatch(t.Context(), log.CreatedAt+1, 10)
	require.ErrorIs(t, err, ErrUsageStatisticsNotReady)
	var count int64
	require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count, "legacy rows with changed writer provenance remain retained")

	require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Update("other", legacyOther("writer-a", "export-b")).Error)
	_, err = DeleteOldLogBatch(t.Context(), log.CreatedAt+1, 10)
	require.ErrorIs(t, err, ErrUsageStatisticsNotReady)
	require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count, "legacy rows with changed export provenance remain retained")

	require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Update("other", legacyOther("writer-a", "export-a")).Error)
	deleted, err := DeleteOldLogBatch(t.Context(), log.CreatedAt+1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
}

func TestDeleteOldLogBatchRejectsLinkedNodeMutation(t *testing.T) {
	db := openUsageEventTestDatabase(t, "sqlite")
	previousLogDB, previousType := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousType)
		initCol()
	})
	require.NoError(t, db.AutoMigrate(&Log{}, &QuotaData{}))
	prepareUsageBackfillFixture(t, db)

	input := usageEventTestInput(78)
	input.Identity, input.CollectionEpoch = UsageEventIdentity{}, ""
	report, err := NewUsageSourceReport(t.Context(), UsageEventSourceRelay, UsageEventKindConsume, input)
	require.NoError(t, err)
	input = report.Input
	require.NoError(t, PrepareUsageSourceReport(t.Context(), report))
	require.NoError(t, ConfirmUsageSourceReport(t.Context(), report, UsageEventSourceReady, ""))
	baseline, err := InspectUsageLegacyQuotaData(t.Context())
	require.NoError(t, err)
	lineage := transitionTestLineage(baseline)
	importTransitionTestLogs(t, db, lineage, input.OccurredAt, input.OccurredAt)
	require.NoError(t, ActivateUsageStatisticsLineage(t.Context(), lineage, baseline))
	for _, tc := range []struct {
		name   string
		writer any
		export any
		omit   bool
		retain bool
	}{
		{"writer_conflict", "writer-mutated", input.ExportNode, false, true},
		{"export_conflict", input.WriterNode, "export-mutated", false, true},
		{"matching_nodes", input.WriterNode, input.ExportNode, false, false},
		{"optional_nodes_absent", nil, nil, true, false},
		{"optional_nodes_null", nil, nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &Log{
				UserId: int(input.UserID), Username: input.Username, CreatedAt: input.OccurredAt, Type: LogTypeConsume,
				TokenName: input.TokenName, ModelName: input.ModelName, PromptTokens: int(input.PromptTokens),
				CompletionTokens: int(input.CompletionTokens), Quota: int(input.QuotaMagnitude), ChannelId: int(input.ChannelID),
				TokenId: int(input.TokenID), Group: input.GroupName, RequestId: input.RequestID, UpstreamRequestId: input.UpstreamRequestID,
			}
			other := NewLogOther()
			other.SetPublic("cache_tokens", input.CacheReadTokens)
			other.SetPublic("cache_write_tokens", input.CacheWriteTokens)
			other.SetPublic("usage_semantic", "openai")
			if !tc.omit {
				other.SetAdmin("node_name", tc.writer)
				other.SetAdmin("submit_node_name", tc.export)
			}
			bound, err := bindUsageSourceReport(log, other, report)
			require.NoError(t, err)
			log.Other = bound.JSONString()
			require.NoError(t, db.Create(log).Error)
			deleted, err := DeleteOldLogBatch(t.Context(), input.OccurredAt+1, 10)
			if tc.retain {
				require.ErrorIs(t, err, ErrUsageStatisticsNotReady)
				assert.Zero(t, deleted)
				var count int64
				require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Count(&count).Error)
				assert.EqualValues(t, 1, count, "linked rows with changed node provenance remain retained")
				other.SetAdmin("node_name", input.WriterNode)
				other.SetAdmin("submit_node_name", input.ExportNode)
				bound, err = bindUsageSourceReport(log, other, report)
				require.NoError(t, err)
				require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Update("other", bound.JSONString()).Error)
				deleted, err = DeleteOldLogBatch(t.Context(), input.OccurredAt+1, 10)
			}
			require.NoError(t, err)
			assert.EqualValues(t, 1, deleted)
		})
	}
}

func TestUsageEventBackfillBatchResumesAfterTransactionalFailure(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{
		{"sqlite", ""},
		{"mysql", "TEST_MYSQL_DSN"},
		{"postgres", "TEST_POSTGRES_DSN"},
	} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			primary := openUsageEventTestDatabase(t, dialect.kind)
			source := withOpenTestLogDatabase(t, dialect.kind, dsn)
			prepareUsageBackfillFixture(t, primary)

			logs := []Log{
				{UserId: 1, Username: "alice", CreatedAt: 1000, Type: LogTypeConsume, TokenName: "token-a", ModelName: "model-a", PromptTokens: 10, CompletionTokens: 2, Quota: 3, ChannelId: 9420, TokenId: 11, Group: "default", RequestId: "request-a"},
				{UserId: 1, Username: "alice", CreatedAt: 1001, Type: LogTypeConsume, TokenName: "token-a", ModelName: "model-a", PromptTokens: 20, CompletionTokens: 4, Quota: 5, ChannelId: 9420, TokenId: 11, Group: "default", RequestId: "request-b"},
				{UserId: 2, Username: "bob", CreatedAt: 1002, Type: LogTypeConsume, TokenName: "token-b", ModelName: "model-b", PromptTokens: 30, CompletionTokens: 6, Quota: 7, ChannelId: 9421, TokenId: 22, Group: "vip", RequestId: "request-c"},
			}
			require.NoError(t, source.Create(&logs).Error)
			plan := inspectUsageBackfillFixture(t, source, usageBackfillFixturePlan(t, source), 2)
			assert.EqualValues(t, 3, plan.ExpectedRows)

			injected := errors.New("backfill projection failure")
			const callback = "usage_backfill_projection_failure"
			require.NoError(t, primary.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "usage_channel_models" {
					tx.AddError(injected)
				}
			}))
			t.Cleanup(func() { _ = primary.Callback().Create().Remove(callback) })
			_, err := RunUsageEventBackfillBatch(t.Context(), source, plan, 2)
			require.NoError(t, primary.Callback().Create().Remove(callback))
			require.ErrorIs(t, err, injected)

			var checkpoint UsageEventBackfill
			require.NoError(t, primary.Where("source_namespace = ?", plan.SourceNamespace).Take(&checkpoint).Error)
			assert.Zero(t, checkpoint.ImportedRows, "failed import must roll back the checkpoint")
			assert.Zero(t, checkpoint.LastRowID)
			var events, channels, models int64
			require.NoError(t, primary.Model(&UsageEvent{}).Count(&events).Error)
			require.NoError(t, primary.Model(&UsageChannel{}).Count(&channels).Error)
			require.NoError(t, primary.Model(&UsageChannelModel{}).Count(&models).Error)
			assert.Zero(t, events, "failed import must roll back facts")
			assert.Zero(t, channels, "failed import must roll back projections")
			assert.Zero(t, models, "failed import must roll back projections")

			first, err := RunUsageEventBackfillBatch(t.Context(), source, plan, 2)
			require.NoError(t, err)
			assert.EqualValues(t, 2, first.BatchRows)
			assert.False(t, first.RetainedVerified)
			second, err := RunUsageEventBackfillBatch(t.Context(), source, plan, 2)
			require.NoError(t, err)
			assert.EqualValues(t, 3, second.Cursor.Rows)
			assert.EqualValues(t, 1, second.BatchRows)
			assert.True(t, second.RetainedVerified)

			var beforeEvents, beforeChannels, beforeModels, beforeQuota int64
			require.NoError(t, primary.Model(&UsageEvent{}).Count(&beforeEvents).Error)
			require.NoError(t, primary.Model(&UsageChannel{}).Count(&beforeChannels).Error)
			require.NoError(t, primary.Model(&UsageChannelModel{}).Count(&beforeModels).Error)
			require.NoError(t, primary.Model(&UsageChannel{}).Select("COALESCE(SUM(gross_quota), 0)").Scan(&beforeQuota).Error)
			replay, err := RunUsageEventBackfillBatch(t.Context(), source, plan, 2)
			require.NoError(t, err)
			assert.True(t, replay.RetainedVerified)
			assert.Zero(t, replay.BatchRows, "replaying a verified import must be a no-op")
			var afterEvents, afterChannels, afterModels, afterQuota int64
			require.NoError(t, primary.Model(&UsageEvent{}).Count(&afterEvents).Error)
			require.NoError(t, primary.Model(&UsageChannel{}).Count(&afterChannels).Error)
			require.NoError(t, primary.Model(&UsageChannelModel{}).Count(&afterModels).Error)
			require.NoError(t, primary.Model(&UsageChannel{}).Select("COALESCE(SUM(gross_quota), 0)").Scan(&afterQuota).Error)
			assert.Equal(t, beforeEvents, afterEvents)
			assert.Equal(t, beforeChannels, afterChannels)
			assert.Equal(t, beforeModels, afterModels)
			assert.Equal(t, beforeQuota, afterQuota)
		})
	}
}
