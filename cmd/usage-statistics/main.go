// Command usage-statistics provides bounded operator actions for the durable
// usage transition. It never creates a source fence or installs lineage from
// an unverified moving source; the operator must supply the immutable plan and
// an external old-writer/buffer/TTL fence digest.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type command struct {
	operation string
	timeout   time.Duration
	baseline  model.UsageLegacyQuotaDataBaseline
	lineage   model.UsageReadLineage
	plan      model.UsageEventBackfillPlan
	cursor    model.UsageEventBackfillCursor
	limit     int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}

	// Parse and reject invalid invocations before initializing database
	// connections, which may perform startup work.
	common.InitEnv()
	if err := model.InitDB(); err != nil {
		return err
	}
	defer model.CloseDB()
	if cmd.operation == "activate" || cmd.operation == "inspect-backfill" || cmd.operation == "import" || cmd.operation == "resume" {
		if err := model.InitLogDB(); err != nil {
			return err
		}
	}

	base, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(base, cmd.timeout)
	defer cancel()
	switch cmd.operation {
	case "inspect-baseline":
		baseline, err := model.InspectUsageLegacyQuotaData(ctx)
		if err != nil {
			return err
		}
		return printJSON(baseline)
	case "activate":
		if err := model.ActivateUsageStatisticsLineage(ctx, cmd.lineage, cmd.baseline); err != nil {
			return err
		}
		installed, err := model.GetUsageReadLineage(ctx)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{
			"activated": true, "legacy_source_namespace": installed.LegacySourceNamespace,
			"legacy_collection_epoch": installed.LegacyCollectionEpoch, "lineage": installed,
		})
	case "inspect-backfill":
		page, err := model.InspectUsageEventBackfillBatch(ctx, model.LOG_DB, cmd.plan, cmd.cursor, cmd.limit)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"cursor": page.Next, "exhausted": page.Exhausted})
	case "import", "resume":
		progress, err := model.RunUsageEventBackfillBatch(ctx, model.LOG_DB, cmd.plan, cmd.limit)
		if err != nil {
			return err
		}
		return printJSON(progress)
	default:
		return fmt.Errorf("unknown operation %q", cmd.operation)
	}
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New("operation is required: inspect-baseline, activate, inspect-backfill, import, or resume")
	}
	cmd := command{operation: args[0], limit: 100, timeout: 2 * time.Minute}
	switch cmd.operation {
	case "inspect-baseline":
		if len(args) != 1 {
			return command{}, errors.New("inspect-baseline takes no arguments")
		}
	case "activate":
		fs := flag.NewFlagSet("activate", flag.ContinueOnError)
		namespace := fs.String("namespace", "", "stable legacy log source namespace (32 lowercase hex)")
		epoch := fs.String("epoch", "", "legacy collection epoch (32 lowercase hex)")
		fence := fs.String("fence", "", "external old-writer/buffer/TTL fence digest (64 lowercase hex)")
		baselineDigest := fs.String("baseline-digest", "", "quota_data baseline digest (64 lowercase hex)")
		maxID := fs.Int64("max-id", -1, "quota_data maximum ID included in the baseline")
		rows := fs.Int64("rows", -1, "quota_data rows included in the baseline")
		timeout := fs.Duration("timeout", cmd.timeout, "operation timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return command{}, err
		}
		if fs.NArg() != 0 || !validHex(*namespace, 32) || !validHex(*epoch, 32) || !validHex(*fence, 64) ||
			!validHex(*baselineDigest, 64) || *maxID < 0 || *rows < 0 || *timeout <= 0 || *timeout > time.Hour {
			return command{}, errors.New("invalid activation arguments")
		}
		cmd.timeout = *timeout
		cmd.baseline = model.UsageLegacyQuotaDataBaseline{Rows: *rows, MaxID: *maxID, Digest: *baselineDigest}
		cmd.lineage = model.UsageReadLineage{
			IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: *maxID, LegacyBaselineDigest: *baselineDigest,
			FenceDigest: *fence, LegacySourceNamespace: *namespace, LegacyCollectionEpoch: *epoch,
			LiveSourceNamespace: model.UsageLiveSourceNamespace(), LiveCollectionEpoch: model.UsageLiveCollectionEpoch(),
			DashboardEligibility: model.UsageEventProjectHours, HistoricalCoverage: model.UsageCoveragePartial,
			WholeHistoryCoverage: model.UsageCoveragePartial,
		}
	case "inspect-backfill", "import", "resume":
		fs := flag.NewFlagSet(cmd.operation, flag.ContinueOnError)
		namespace := fs.String("namespace", "", "stable source namespace")
		kind := fs.String("kind", "relational", "relational or clickhouse")
		epoch := fs.String("epoch", "", "frozen source collection epoch")
		start := fs.Int64("start", 0, "inclusive source timestamp")
		end := fs.Int64("end", 0, "inclusive source timestamp")
		high := fs.Int64("high-watermark", 0, "relational source ID watermark; zero for ClickHouse")
		expected := fs.Int64("expected-rows", -1, "manifest row count (required for import/resume)")
		manifest := fs.String("manifest-digest", "", "manifest digest")
		fence := fs.String("fence", "", "source fence digest")
		limit := fs.Int("limit", 100, "rows per bounded operation, maximum 100")
		cursorJSON := fs.String("cursor-json", "", "cursor object emitted by the previous inspect-backfill page")
		timeout := fs.Duration("timeout", cmd.timeout, "operation timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return command{}, err
		}
		if fs.NArg() != 0 || !validHex(*namespace, 32) || !validHex(*epoch, 32) || !validHex(*fence, 64) ||
			*start <= 0 || *end < *start || *high < 0 || *limit <= 0 || *limit > 100 || *timeout <= 0 || *timeout > time.Hour {
			return command{}, errors.New("invalid backfill arguments")
		}
		sourceKind := model.UsageEventSourceLegacyRelational
		if strings.EqualFold(*kind, "clickhouse") {
			sourceKind = model.UsageEventSourceLegacyClickHouse
		} else if !strings.EqualFold(*kind, "relational") {
			return command{}, errors.New("kind must be relational or clickhouse")
		}
		if sourceKind == model.UsageEventSourceLegacyClickHouse && *high != 0 {
			return command{}, errors.New("ClickHouse high-watermark must be zero")
		}
		cmd.timeout, cmd.limit = *timeout, *limit
		cmd.plan = model.UsageEventBackfillPlan{
			SourceNamespace: *namespace, SourceKind: sourceKind, CollectionEpoch: *epoch, StartAt: *start, EndAt: *end,
			HighWatermark: *high, ExpectedRows: *expected, ManifestDigest: *manifest, FenceDigest: *fence,
		}
		if cmd.operation == "inspect-backfill" {
			if *expected != -1 || *manifest != "" {
				return command{}, errors.New("inspect-backfill does not accept expected-rows or manifest-digest")
			}
			if *cursorJSON != "" {
				if err := common.UnmarshalJsonStr(*cursorJSON, &cmd.cursor); err != nil {
					return command{}, errors.New("invalid cursor-json")
				}
			}
		} else {
			if *expected < 0 || !validHex(*manifest, 64) {
				return command{}, errors.New("import/resume requires frozen expected-rows and manifest-digest")
			}
			if *cursorJSON != "" {
				return command{}, errors.New("cursor-json is only used by inspect-backfill")
			}
		}
	default:
		return command{}, fmt.Errorf("unknown operation %q", cmd.operation)
	}
	return cmd, nil
}

func printJSON(value any) error {
	data, err := common.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}
