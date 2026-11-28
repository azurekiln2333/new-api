package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSystemTaskRegistry swaps the package registry for the given handlers for
// the duration of a test and restores the original registry afterward.
func withSystemTaskRegistry(t *testing.T, handlers ...SystemTaskHandler) {
	t.Helper()
	systemTaskHandlersMu.Lock()
	saved := systemTaskHandlers
	systemTaskHandlers = map[string]SystemTaskHandler{}
	for _, h := range handlers {
		systemTaskHandlers[h.Type()] = h
	}
	systemTaskHandlersMu.Unlock()
	t.Cleanup(func() {
		systemTaskHandlersMu.Lock()
		systemTaskHandlers = saved
		systemTaskHandlersMu.Unlock()
	})
}

type stubScheduledHandler struct {
	taskType string
	enabled  bool
	interval time.Duration
	onRun    func(ctx context.Context, task *model.SystemTask, runnerID string)
}

type stubSystemTaskRunResult struct {
	taskID   string
	taskType string
	err      error
}

func (h *stubScheduledHandler) Type() string { return h.taskType }

func (h *stubScheduledHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if h.onRun != nil {
		h.onRun(ctx, task, runnerID)
	}
}

func (h *stubScheduledHandler) Enabled() bool           { return h.enabled }
func (h *stubScheduledHandler) Interval() time.Duration { return h.interval }
func (h *stubScheduledHandler) NewPayload() any         { return nil }

func countSystemTasks(t *testing.T, taskType string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", taskType).Count(&count).Error)
	return count
}

func TestSystemTaskSchedulerCreatesWhenDueAndDedups(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_scheduled", enabled: true, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// An active (pending) row already exists, so a second pass must not create
	// another row.
	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Finish the run; with a fresh updated_at the next run is not due yet.
	latest, err := model.GetLatestSystemTask(handler.taskType)
	require.NoError(t, err)
	require.NotNil(t, latest)
	_, claimed, err := model.ClaimSystemTask(latest.ID, handler.taskType, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(latest.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Backdate the finished row beyond the interval -> the job becomes due again.
	require.NoError(t, model.DB.Model(&model.SystemTask{}).
		Where("task_id = ?", latest.TaskID).
		Update("updated_at", common.GetTimestamp()-120).Error)

	runSystemTaskScheduler()
	require.Equal(t, int64(2), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskLogRetention(t *testing.T) {
	truncate(t)
	settings := operation_setting.GetLogSetting()
	common.OptionMapRWMutex.Lock()
	previousDays, previousMaster := settings.RetentionDays, common.IsMasterNode
	settings.RetentionDays = 0
	common.IsMasterNode = true
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		settings.RetentionDays, common.IsMasterNode = previousDays, previousMaster
		common.OptionMapRWMutex.Unlock()
	})
	handler := logCleanupHandler{}
	withSystemTaskRegistry(t, handler)
	runSystemTaskScheduler()
	assert.Zero(t, countSystemTasks(t, handler.Type()), "upgrade defaults never delete history")
	common.OptionMapRWMutex.Lock()
	settings.RetentionDays = 7
	common.IsMasterNode = false
	common.OptionMapRWMutex.Unlock()
	runSystemTaskScheduler()
	assert.Zero(t, countSystemTasks(t, handler.Type()), "followers cannot schedule retention")
	common.IsMasterNode = true
	runSystemTaskScheduler()
	runSystemTaskScheduler()
	assert.EqualValues(t, 1, countSystemTasks(t, handler.Type()), "manual and scheduled cleanup share active-task deduplication")
	task, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, task)
	var payload LogCleanupPayload
	require.NoError(t, task.DecodePayload(&payload))
	assert.True(t, payload.Scheduled)
	assert.Equal(t, logCleanupBatchSize, payload.BatchSize)
	assert.InDelta(t, common.GetTimestamp()-7*86400, payload.TargetTimestamp, 2)
	_, claimed, err := model.ClaimSystemTask(task.ID, handler.Type(), "retention-runner", systemTaskLockUntil())
	require.NoError(t, err)
	require.True(t, claimed)
	common.OptionMapRWMutex.Lock()
	settings.RetentionDays = 0
	common.OptionMapRWMutex.Unlock()
	runLogCleanupTask(t.Context(), task, "retention-runner")
	latest, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, latest.Status, "disabling retention stops the next batch without deleting logs")
	common.OptionMapRWMutex.Lock()
	settings.RetentionDays = operation_setting.MaxLogRetentionDays
	common.OptionMapRWMutex.Unlock()
	maxRetentionPayload := handler.NewPayload().(LogCleanupPayload)
	assert.Greater(t, maxRetentionPayload.TargetTimestamp, int64(0), "the maximum accepted retention must not create an invalid task cutoff")
}

func TestSystemTaskLogRetentionStopsWhenUsageStatisticsAreNotReady(t *testing.T) {
	truncate(t)
	previousLogDB := model.LOG_DB
	previousType := common.LogDatabaseType()
	model.LOG_DB = model.DB
	common.SetLogDatabaseType(common.MainDatabaseType())
	t.Cleanup(func() { model.LOG_DB = previousLogDB; common.SetLogDatabaseType(previousType) })
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}, &model.UsageEvent{}, &model.UsageHour{}, &model.UsageChannel{}, &model.UsageChannelModel{}))
	require.NoError(t, model.MigrateUsageEventBackfill(model.DB))
	require.NoError(t, model.DB.Create(&model.Log{UserId: 801, CreatedAt: 100, Type: model.LogTypeConsume, ModelName: "m", Quota: 1}).Error)
	_, err := model.DeleteOldLogBatch(t.Context(), 200, 10)
	require.ErrorIs(t, err, model.ErrUsageStatisticsNotReady)
	var count int64
	require.NoError(t, model.DB.Model(&model.Log{}).Where("user_id = ?", 801).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestSystemTaskLogRetentionDeletesOnlyAfterLegacyImportAndPreservesUsageTotals(t *testing.T) {
	truncate(t)
	previousLogDB := model.LOG_DB
	previousType := common.LogDatabaseType()
	model.LOG_DB = model.DB
	common.SetLogDatabaseType(common.MainDatabaseType())
	t.Cleanup(func() { model.LOG_DB = previousLogDB; common.SetLogDatabaseType(previousType) })
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}, &model.QuotaData{}))
	require.NoError(t, model.MigrateUsageStatistics(model.DB))
	require.NoError(t, model.MigrateUsageEventBackfill(model.DB))
	const namespace = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, model.DB.Where("id = ?", 1).Delete(&model.UsageStatisticsLineage{}).Error)
	require.NoError(t, model.DB.Where("source_namespace = ?", namespace).Delete(&model.UsageEventBackfill{}).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("id = ?", 1).Delete(&model.UsageStatisticsLineage{}).Error)
		require.NoError(t, model.DB.Where("source_namespace = ?", namespace).Delete(&model.UsageEventBackfill{}).Error)
	})
	log := model.Log{UserId: 802, CreatedAt: 100, Type: model.LogTypeConsume, Username: "legacy-user", ModelName: "legacy-model", Group: "default", RequestId: "legacy-request", ChannelId: 802, Quota: 17, PromptTokens: 3, CompletionTokens: 4, Other: `{"cache_tokens":0,"cache_write_tokens":0,"usage_semantic":"openai"}`}
	require.NoError(t, model.DB.Create(&log).Error)
	plan := model.UsageEventBackfillPlan{
		SourceNamespace: namespace, SourceKind: model.UsageEventSourceLegacyRelational,
		CollectionEpoch: strings.Repeat("b", 32), StartAt: 100, EndAt: 100, HighWatermark: int64(log.Id),
		ExpectedRows: -1, FenceDigest: strings.Repeat("c", 64),
	}
	page, err := model.InspectUsageEventBackfillBatch(t.Context(), model.DB, plan, model.UsageEventBackfillCursor{}, 100)
	require.NoError(t, err)
	require.True(t, page.Exhausted)
	plan.ExpectedRows, plan.ManifestDigest = page.Next.Rows, page.Next.Digest
	progress, err := model.RunUsageEventBackfillBatch(t.Context(), model.DB, plan, 100)
	require.NoError(t, err)
	require.True(t, progress.RetainedVerified)
	baseline, err := model.InspectUsageLegacyQuotaData(t.Context())
	require.NoError(t, err)
	require.NoError(t, model.ActivateUsageStatisticsLineage(t.Context(), model.UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: baseline.MaxID, LegacyBaselineDigest: baseline.Digest,
		FenceDigest: plan.FenceDigest, LegacySourceNamespace: plan.SourceNamespace, LegacyCollectionEpoch: plan.CollectionEpoch,
		LiveSourceNamespace: model.UsageLiveSourceNamespace(), LiveCollectionEpoch: model.UsageLiveCollectionEpoch(),
		DashboardEligibility: model.UsageEventProjectHours,
	}, baseline))
	before, err := model.SumUsedQuota(model.LogFilter{UserId: 802, StartTimestamp: 100, EndTimestamp: 100})
	require.NoError(t, err)
	channelsBefore, err := model.GetChannelUsages([]int{802})
	require.NoError(t, err)
	deleted, err := model.DeleteOldLogBatch(t.Context(), 200, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	var logs, events int64
	require.NoError(t, model.DB.Model(&model.Log{}).Where("user_id = ?", 802).Count(&logs).Error)
	require.NoError(t, model.DB.Model(&model.UsageEvent{}).Count(&events).Error)
	assert.Zero(t, logs)
	assert.EqualValues(t, 1, events)
	after, err := model.SumUsedQuota(model.LogFilter{UserId: 802, StartTimestamp: 100, EndTimestamp: 100})
	require.NoError(t, err)
	assert.Equal(t, before, after)
	channelsAfter, err := model.GetChannelUsages([]int{802})
	require.NoError(t, err)
	assert.Equal(t, channelsBefore, channelsAfter)
}

func TestSystemTaskSchedulerSkipsDisabled(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_disabled", enabled: false, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	assert.Equal(t, int64(0), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskClaimPassDispatchesByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 1)
	handler := &stubScheduledHandler{
		taskType: "test_dispatch",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskType: task.Type,
				err:      model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handler)

	_, err := model.CreateSystemTask(handler.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	select {
	case got := <-ran:
		require.NoError(t, got.err)
		assert.Equal(t, handler.taskType, got.taskType)
	case <-time.After(2 * time.Second):
		t.Fatal("claimed task was not dispatched to its handler")
	}

	require.Eventually(t, func() bool {
		latest, err := model.GetLatestSystemTask(handler.taskType)
		return err == nil && latest != nil && latest.Status == model.SystemTaskStatusSucceeded
	}, 2*time.Second, 20*time.Millisecond)
}

func TestSystemTaskClaimPassDispatchesEarliestPendingByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 2)
	handlerA := &stubScheduledHandler{
		taskType: "test_dispatch_a",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	handlerB := &stubScheduledHandler{
		taskType: "test_dispatch_b",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handlerA, handlerB)

	firstA, err := model.CreateSystemTask(handlerA.taskType, nil, nil)
	require.NoError(t, err)
	secondTaskID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	secondA := &model.SystemTask{
		TaskID: secondTaskID,
		Type:   handlerA.taskType,
		Status: model.SystemTaskStatusPending,
	}
	require.NoError(t, model.DB.Create(secondA).Error)
	firstB, err := model.CreateSystemTask(handlerB.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	got := map[string]bool{}
	for range 2 {
		select {
		case result := <-ran:
			require.NoError(t, result.err)
			got[result.taskID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("claimed tasks were not dispatched to their handlers")
		}
	}

	assert.True(t, got[firstA.TaskID])
	assert.True(t, got[firstB.TaskID])
	assert.False(t, got[secondA.TaskID])

	require.Eventually(t, func() bool {
		reloaded, err := model.GetSystemTaskByTaskID(secondA.TaskID)
		return err == nil && reloaded != nil && reloaded.Status == model.SystemTaskStatusPending
	}, 2*time.Second, 20*time.Millisecond)
}

func TestEnqueueSystemTaskReportsCreatedAndExistingActive(t *testing.T) {
	truncate(t)

	first, created, err := EnqueueSystemTask("test_enqueue", map[string]bool{"manual": true})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	existing, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, existing)
	assert.Equal(t, first.TaskID, existing.TaskID)

	_, claimed, err := model.ClaimSystemTask(first.ID, first.Type, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(first.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	second, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, second)
	assert.NotEqual(t, first.TaskID, second.TaskID)
}
