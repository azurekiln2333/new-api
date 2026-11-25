package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func (b *taskSubmissionTestBilling) ReserveBeforeSubmit(quota int) error { return b.Reserve(quota) }

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

// The submit presenter may answer with the vendor's status; a native route that
// already relayed the vendor's event stream gets nothing more.
func TestPresentTaskSubmissionHonorsReplyWrapperAndRelayedStreams(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"accepted-test",name:"Accepted",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {__status:202,__body:{id:task.task_id}};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	present := func(recorder *httptest.ResponseRecorder, written bool) {
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
		c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
		c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})
		if written {
			_, _ = c.Writer.WriteString("data: {}\n\n")
		}
		presentTaskSubmission(c, &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: &model.Task{TaskID: "task_public"}, RelayInfo: &relaycommon.RelayInfo{}})
	}

	accepted := httptest.NewRecorder()
	present(accepted, false)
	assert.Equal(t, http.StatusAccepted, accepted.Code)
	assert.JSONEq(t, `{"id":"task_public"}`, accepted.Body.String())

	streamed := httptest.NewRecorder()
	present(streamed, true)
	assert.Equal(t, "data: {}\n\n", streamed.Body.String())
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{})
			previousDB := model.DB
			model.DB = database
			t.Cleanup(func() { model.DB = previousDB })
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	require.NoError(t, i18n.Init())
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	settings := kitdto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}
	common.SetContextKey(c, constant.ContextKeyChannelSetting, settings)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	_, capacityErr := service.AcquireChannelCapacity(context.Background(), 1, settings)
	require.NotNil(t, capacityErr, "submission holds the channel slot until its handler returns")
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
	replacement, capacityErr := service.AcquireChannelCapacity(context.Background(), 1, settings)
	require.Nil(t, capacityErr, "canceled submission releases its slot before returning")
	replacement.Release()
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	// The runtime checks durable budget activation even when local budgets are
	// off. Give every isolated fixture its own empty options table; keep the
	// deliberately missing task table in insertion-failure tests unchanged.
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
	models = append(models, &model.Option{})
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
	}{
		{"partial", "SUCCESS", 2, 2}, {"zero", "SUCCESS", 0, 0}, {"larger", "SUCCESS", 6, 6},
		{"invalid usage", "SUCCESS", -1, 4}, {"expression failure", "SUCCESS", 7, 4}, {"negative result", "SUCCESS", 8, 4}, {"missing usage", "SUCCESS", nil, 4}, {"failed", "FAILURE", 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			require.NoError(t, info.Billing.Settle(want))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota, "terminal settlement is idempotent")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "response_started", Source: "system"}, decideTaskRetry(c, taskErr, 2), "a rendered error must never be retried")
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(taskSubmissionTestContext(), &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

// A ledger-tracked submission commits its acceptance delta with the task row,
// returns that committed result to the caller, moves it into usage once and is
// never charged again by a later settle of the same submission.
func TestTaskSubmissionCommitsLedgerChargeAtomically(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{},
		&model.ChannelKeyEntry{}, &model.ChannelBudgetAccount{}, &model.ChannelBudgetReservation{},
		&model.ChannelBudgetReservationItem{}, &model.ChannelBudgetOperation{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
	})
	require.NoError(t, model.MigrateChannelBudgets(db))
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())

	user := model.User{Username: "ledger_submit", AffCode: "ledger_submit", Quota: 100000}
	require.NoError(t, db.Create(&user).Error)
	tokenKey := "sk-ledger-submit"
	token := model.Token{UserId: user.Id, Key: tokenKey, Name: "ledger", RemainQuota: 100000, UnlimitedQuota: false}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "ledger submit", Type: constant.ChannelTypeTaskPlugin, Key: "channel-key",
		Status: common.ChannelStatusEnabled, Group: "default", Models: "plugin-model"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, model.EnsureChannelKeyEntries(&channel))
	var entry model.ChannelKeyEntry
	require.NoError(t, db.Where("channel_id = ?", channel.Id).Order("position, id").First(&entry).Error)

	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(nil)
	info.UserId = user.Id
	info.TokenId = token.Id
	info.TokenKey = tokenKey
	info.UserSetting.BillingPreference = "wallet_only"
	info.LockedChannel = &channel
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: constant.ChannelTypeTaskPlugin}
	service.SetSelectedChannelAllocation(c, entry.Id, entry.ConfigRevision)
	require.Nil(t, service.PrepareAttemptChannelBudget(c, info))
	require.True(t, info.ChannelBudget.HasBudgetReservation())

	const quota = 900
	task := model.InitTask(constant.TaskPlatform("plugin"), info)
	task.ChannelId, task.UserId = channel.Id, user.Id
	task.PrivateData.AllocationId = info.ChannelBudget.AllocationID
	task.PrivateData.AllocationRevision = info.ChannelBudget.AllocationRevision
	task.PrivateData.BudgetReservationId = info.ChannelBudget.ReservationID
	task.PrivateData.TokenId = token.Id
	task.Quota = quota
	submit := &model.TaskBudgetSubmit{Total: quota, Delta: quota, TokenID: token.Id, Final: true}
	committed, err := model.InsertTaskWithSubmissionCharge(c.Request.Context(), task, nil, nil, submit)
	require.NoError(t, err)
	require.NotNil(t, committed, "a committed submission must return its ledger identity")
	assert.Equal(t, quota, committed.Delta)
	assert.EqualValues(t, task.PrivateData.BudgetReservationId, committed.ReservationID)
	service.FinalizeTaskBudgetSubmission(c, info, task, *committed, quota)
	// The submission is the only station charge; finalizing again is a no-op.
	service.FinalizeTaskBudgetSubmission(c, info, task, *committed, quota)

	var updated model.User
	var updatedToken model.Token
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.NoError(t, db.First(&updatedToken, token.Id).Error)
	assert.Equal(t, 100000-quota, updated.Quota)
	assert.Equal(t, 100000-quota, updatedToken.RemainQuota)
	for _, scope := range []model.ChannelBudgetScopeType{model.BudgetScopeChannel, model.BudgetScopeKey} {
		id := int64(channel.Id)
		if scope == model.BudgetScopeKey {
			id = entry.Id
		}
		view, readErr := model.ReadChannelBudget(t.Context(), scope, id)
		require.NoError(t, readErr)
		assert.EqualValues(t, quota, view.PeriodUsedQuota, "%s usage comes from the committed submission", scope)
		assert.EqualValues(t, quota, view.LifetimeUsedQuota)
		assert.Zero(t, view.ReservedQuota)
	}
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

// ledgerSubmissionFixture is one accepted-submission scenario: a real user,
// token, channel, allocation and live budget reservation, all on the isolated
// task database so the whole executeTaskSubmission path runs against a real
// ledger.
type ledgerSubmissionFixture struct {
	db      *gorm.DB
	user    model.User
	token   model.Token
	channel model.Channel
	entry   model.ChannelKeyEntry
	c       *gin.Context
	info    *relaycommon.RelayInfo
}

func newLedgerSubmissionFixture(t *testing.T, withReservation bool, extraModels ...any) *ledgerSubmissionFixture {
	t.Helper()
	tables := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{},
		&model.ChannelKeyEntry{}, &model.ChannelBudgetAccount{}, &model.ChannelBudgetReservation{},
		&model.ChannelBudgetReservationItem{}, &model.ChannelBudgetOperation{}}
	db, dialect := openTaskDialectDatabase(t, append(tables, extraModels...)...)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	t.Cleanup(func() {
		// Persisted refund balances do not mean the nested quota-cache task
		// has finished. Drain the default pool before restoring globals or
		// releasing this fixture's database. Workers may enqueue more work;
		// all of those workers finish before WorkerCount reaches zero.
		require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond,
			"ledger submission refund/cache workers must finish before fixture teardown")
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
	})
	require.NoError(t, model.MigrateChannelBudgets(db))
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())

	fixture := &ledgerSubmissionFixture{db: db}
	fixture.user = model.User{Username: "ledger_flow", AffCode: "ledger_flow", Quota: 100000}
	require.NoError(t, db.Create(&fixture.user).Error)
	fixture.token = model.Token{UserId: fixture.user.Id, Key: "sk-ledger-flow", Name: "ledger", RemainQuota: 100000}
	require.NoError(t, db.Create(&fixture.token).Error)
	fixture.channel = model.Channel{Name: "ledger flow", Type: constant.ChannelTypeTaskPlugin, Key: "channel-key",
		Status: common.ChannelStatusEnabled, Group: "default", Models: "plugin-model"}
	require.NoError(t, db.Create(&fixture.channel).Error)
	require.NoError(t, model.EnsureChannelKeyEntries(&fixture.channel))
	require.NoError(t, db.Where("channel_id = ?", fixture.channel.Id).Order("position, id").First(&fixture.entry).Error)

	fixture.c = taskSubmissionTestContext()
	fixture.info = taskSubmissionRelayInfo(nil)
	fixture.info.UserId = fixture.user.Id
	fixture.info.TokenId = fixture.token.Id
	fixture.info.TokenKey = fixture.token.Key
	fixture.info.UserSetting.BillingPreference = "wallet_only"
	fixture.info.LockedChannel = &fixture.channel
	fixture.info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: fixture.channel.Id, ChannelType: constant.ChannelTypeTaskPlugin}
	if withReservation {
		service.SetSelectedChannelAllocation(fixture.c, fixture.entry.Id, fixture.entry.ConfigRevision)
		// Reserve exactly what the session pre-consumed, so this fixture covers
		// the ledger path rather than admission.
		err := reserveLedgerAttemptForTest(fixture)
		require.NoError(t, err)
		require.True(t, fixture.info.ChannelBudget.HasBudgetReservation())
	}
	return fixture
}

// reserveLedgerAttemptForTest creates the ledger hold this fixture submits
// against, with the same identity the submission path validates. It uses the
// public reserve entry point rather than any test-only seam.
func reserveLedgerAttemptForTest(f *ledgerSubmissionFixture) error {
	r, err := model.ReserveChannelBudgets(context.Background(), model.BudgetReserveRequest{
		AttemptKey: "ledger-flow-" + f.token.Key,
		Selection: model.BudgetSelection{
			ChannelID: f.channel.Id, AllocationID: f.entry.Id, AllocationRevision: f.entry.ConfigRevision,
			UserID: f.user.Id, Group: "default", ModelName: "plugin-model",
		},
		ReserveQuota: 0,
		OwnerNode:    common.NodeName,
	})
	if err != nil {
		return err
	}
	f.info.ChannelBudget = relaycommon.BudgetAttempt{
		ReservationID: r.Id, AttemptKey: r.AttemptKey, Revision: r.SettlementRevision,
		AllocationID: r.AllocationId, AllocationRevision: r.AllocationRevision,
	}
	return nil
}

// submitAccepted runs the real submission path with an immediate terminal
// upstream result of the given cost.
func (f *ledgerSubmissionFixture) submitAccepted(t *testing.T, quota int) (*taskSubmissionOutcome, *dto.TaskError) {
	t.Helper()
	f.info.PriceData.Quota = quota
	return executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_job",
			Platform:       constant.TaskPlatform("plugin"),
			Quota:          quota,
			Immediate:      &relaycommon.TaskInfo{Status: model.TaskStatusSuccess},
		}, nil
	})
}

func (f *ledgerSubmissionFixture) reload(t *testing.T) (model.User, model.Token) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, f.db.First(&user, f.user.Id).Error)
	require.NoError(t, f.db.First(&token, f.token.Id).Error)
	return user, token
}

// A trusted user pre-consumes nothing, so the accepted cost is charged in full
// and the ledger moves the same amount into both scopes.
func TestLedgerTaskSubmissionChargesUpwardActual(t *testing.T) {
	fixture := newLedgerSubmissionFixture(t, true)
	const quota = 900
	outcome, taskErr := fixture.submitAccepted(t, quota)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.Equal(t, quota, outcome.Task.Quota)

	user, token := fixture.reload(t)
	assert.Equal(t, 100000-quota, user.Quota)
	assert.Equal(t, 100000-quota, token.RemainQuota)

	var stored model.Task
	require.NoError(t, fixture.db.Where("task_id = ?", outcome.Task.TaskID).First(&stored).Error)
	assert.True(t, stored.PrivateData.BudgetPending, "the pending flag must be part of the persisted task row")
	assert.EqualValues(t, outcome.Task.PrivateData.BudgetReservationId, stored.PrivateData.BudgetReservationId)

	var reservation model.ChannelBudgetReservation
	require.NoError(t, fixture.db.First(&reservation, stored.PrivateData.BudgetReservationId).Error)
	assert.Equal(t, stored.TaskID, reservation.TaskId)
	assert.False(t, reservation.ReconcilePending, "the submission charge is applied once")
	assert.EqualValues(t, quota, reservation.SettledQuota)

	for _, scope := range []model.ChannelBudgetScopeType{model.BudgetScopeChannel, model.BudgetScopeKey} {
		id := int64(fixture.channel.Id)
		if scope == model.BudgetScopeKey {
			id = fixture.entry.Id
		}
		view, err := model.ReadChannelBudget(t.Context(), scope, id)
		require.NoError(t, err)
		assert.EqualValues(t, quota, view.PeriodUsedQuota, "%s usage", scope)
		assert.EqualValues(t, quota, view.LifetimeUsedQuota)
		assert.Zero(t, view.ReservedQuota)
	}

	// Replaying the finalization never charges again.
	service.FinalizeTaskBudgetSubmission(fixture.c, fixture.info, outcome.Task, model.TaskBudgetInsertResult{
		ReservationID: stored.PrivateData.BudgetReservationId, DesiredRevision: reservation.DesiredRevision,
		Delta: quota, TokenID: fixture.token.Id, TokenKey: fixture.token.Key,
	}, quota)
	user, token = fixture.reload(t)
	assert.Equal(t, 100000-quota, user.Quota)
	assert.Equal(t, 100000-quota, token.RemainQuota)
	var replayed model.ChannelBudgetReservation
	require.NoError(t, fixture.db.First(&replayed, reservation.Id).Error)
	assert.EqualValues(t, quota, replayed.SettledQuota, "the ledger move is guarded by the desired revision")
}

func TestLedgerTaskSubmissionRecordsSubscriptionOverage(t *testing.T) {
	fixture := newLedgerSubmissionFixture(t, true, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{})
	plan := model.SubscriptionPlan{Id: 700101, Title: "submission overage", TotalAmount: 500, QuotaResetPeriod: "never"}
	require.NoError(t, fixture.db.Create(&plan).Error)
	sub := model.UserSubscription{UserId: fixture.user.Id, PlanId: plan.Id, AmountTotal: 500,
		Status: "active", StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, fixture.db.Create(&sub).Error)
	fixture.info.UserSetting.BillingPreference = "subscription_only"
	fixture.info.RequestId = common.GetUUID()
	fixture.info.ForcePreConsume = true
	require.Nil(t, service.PreConsumeBilling(fixture.c, 300, fixture.info))
	require.Equal(t, sub.Id, fixture.info.SubscriptionId)
	require.NoError(t, model.IncreaseChannelBudgetReservation(t.Context(), fixture.info.ChannelBudget.ReservationID, 300))
	outcome, taskErr := fixture.submitAccepted(t, 900)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NoError(t, fixture.db.First(&sub, sub.Id).Error)
	assert.EqualValues(t, 900, sub.AmountUsed, "accepted upstream work is recorded even beyond the plan total")
	assert.EqualValues(t, 600, fixture.info.SubscriptionPostDelta)
	user, token := fixture.reload(t)
	assert.Equal(t, 100000, user.Quota)
	assert.Equal(t, 99100, token.RemainQuota)
	assert.False(t, fixture.info.Billing.NeedsRefund())
	fixture.info.Billing.Refund(fixture.c)
	require.NoError(t, fixture.info.Billing.Settle(900))
	require.NoError(t, fixture.db.First(&sub, sub.Id).Error)
	assert.EqualValues(t, 900, sub.AmountUsed)
	view, err := model.ReadChannelBudget(t.Context(), model.BudgetScopeChannel, int64(fixture.channel.Id))
	require.NoError(t, err)
	assert.EqualValues(t, 900, view.PeriodUsedQuota)
	assert.Zero(t, view.ReservedQuota)
}

// Inject at the SQL boundary after a Task INSERT: Commit really completes
// before the caller receives the error. Earlier admission transactions pass.
type submissionCommitPool struct {
	*sql.DB
	armed    atomic.Bool
	finished atomic.Bool
	rollback bool
}

type submissionCommitTx struct {
	*sql.Tx
	pool *submissionCommitPool
}

func (p *submissionCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &submissionCommitTx{Tx: tx, pool: p}, nil
}

func (tx *submissionCommitTx) Commit() error {
	if !tx.pool.armed.CompareAndSwap(true, false) {
		return tx.Tx.Commit()
	}
	var err error
	if tx.pool.rollback {
		err = tx.Tx.Rollback()
	} else {
		err = tx.Tx.Commit()
	}
	if err != nil {
		return err
	}
	tx.pool.finished.Store(true)
	return errors.New("fixture: commit acknowledgement lost")
}

func TestLedgerTaskSubmissionCommitOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		rollback, unreadable bool
	}{
		{name: "committed with lost acknowledgement"},
		{name: "committed with unreadable outcome", unreadable: true},
		{name: "known rollback refunds and releases", rollback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLedgerSubmissionFixture(t, true)
			fixture.info.ForcePreConsume = true
			require.Nil(t, service.PreConsumeBilling(fixture.c, 300, fixture.info))
			require.Equal(t, 300, fixture.info.Billing.GetPreConsumedQuota())
			require.NoError(t, model.IncreaseChannelBudgetReservation(t.Context(), fixture.info.ChannelBudget.ReservationID, 300))
			sqlDB, err := fixture.db.DB()
			require.NoError(t, err)
			pool := &submissionCommitPool{DB: sqlDB, rollback: tc.rollback}
			wrapped := fixture.db.Session(&gorm.Session{NewDB: true, Initialized: true})
			wrapped.ConnPool, wrapped.Statement.ConnPool = pool, pool
			wrapped = wrapped.Session(&gorm.Session{NewDB: true})
			originalDB := model.DB
			model.DB = wrapped
			t.Cleanup(func() { model.DB = originalDB })
			const arm = "test:submission_commit_arm"
			require.NoError(t, fixture.db.Callback().Create().After("gorm:create").Register(arm, func(tx *gorm.DB) {
				if tx.Error == nil && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
					pool.armed.Store(true)
				}
			}))
			t.Cleanup(func() { require.NoError(t, fixture.db.Callback().Create().Remove(arm)) })
			const readFailure = "test:submission_outcome_unreadable"
			if tc.unreadable {
				require.NoError(t, fixture.db.Callback().Query().Before("gorm:query").Register(readFailure, func(tx *gorm.DB) {
					if pool.finished.Load() && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ChannelBudgetReservation" {
						tx.AddError(errors.New("fixture: outcome lookup unavailable"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, fixture.db.Callback().Query().Remove(readFailure)) })
			}
			outcome, taskErr := fixture.submitAccepted(t, 900)
			require.True(t, pool.finished.Load(), "the real SQL commit/rollback boundary was exercised")
			if tc.rollback || tc.unreadable {
				require.Nil(t, outcome)
				require.NotNil(t, taskErr)
				assert.Equal(t, "task_insert_failed", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
				require.NotNil(t, outcome)
				assert.False(t, fixture.info.Billing.NeedsRefund())
			}
			if tc.unreadable {
				require.NoError(t, fixture.db.Callback().Query().Remove(readFailure))
			}
			model.DB = originalDB
			var tasks []model.Task
			require.NoError(t, fixture.db.Where("user_id = ?", fixture.user.Id).Find(&tasks).Error)
			var reservation model.ChannelBudgetReservation
			require.NoError(t, fixture.db.First(&reservation, fixture.info.ChannelBudget.ReservationID).Error)
			if tc.rollback {
				assert.Empty(t, tasks)
				assert.Equal(t, model.BudgetReservationPhaseReleased, reservation.Phase)
				view, viewErr := model.ReadChannelBudget(t.Context(), model.BudgetScopeChannel, int64(fixture.channel.Id))
				require.NoError(t, viewErr)
				assert.Zero(t, view.ReservedQuota)
				require.Eventually(t, func() bool {
					user, token := fixture.reload(t)
					return user.Quota == 100000 && token.RemainQuota == 100000
				}, 3*time.Second, 10*time.Millisecond)
				return
			}
			require.Len(t, tasks, 1)
			assert.Equal(t, tasks[0].TaskID, reservation.TaskId)
			assert.Equal(t, tc.unreadable, reservation.ReconcilePending)
			assert.EqualValues(t, 900, reservation.DesiredQuota)
			assert.EqualValues(t, 300, reservation.ReservedQuota, "deferred cleanup preserves the hold")
			user, token := fixture.reload(t)
			assert.Equal(t, 99100, user.Quota, "deferred cleanup preserves the committed charge")
			assert.Equal(t, 99100, token.RemainQuota)
			count, err := model.ReconcileChannelBudgets(t.Context(), 10)
			require.NoError(t, err)
			if tc.unreadable {
				assert.Equal(t, 1, count)
			} else {
				assert.Zero(t, count, "verified commit finished through the ordinary success path")
			}
			count, err = model.ReconcileChannelBudgets(t.Context(), 10)
			require.NoError(t, err)
			assert.Zero(t, count)
			require.NoError(t, fixture.db.First(&reservation, reservation.Id).Error)
			assert.EqualValues(t, 900, reservation.SettledQuota)
			view, viewErr := model.ReadChannelBudget(t.Context(), model.BudgetScopeChannel, int64(fixture.channel.Id))
			require.NoError(t, viewErr)
			assert.Zero(t, view.ReservedQuota)
			user, token = fixture.reload(t)
			assert.Equal(t, 99100, user.Quota)
			assert.Equal(t, 99100, token.RemainQuota)
		})
	}
}

func TestLedgerTaskSubmissionOutcomeIdentity(t *testing.T) {
	fixture := newLedgerSubmissionFixture(t, true)
	outcome, taskErr := fixture.submitAccepted(t, 900)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	// Historical public task IDs are not globally unique. Another owner's row
	// must neither prove this charge nor make the matching row ambiguous.
	for _, otherUser := range []bool{true, false} {
		collision := *outcome.Task
		collision.ID = 0
		if otherUser {
			collision.UserId++
		} else {
			collision.ChannelId++
		}
		require.NoError(t, fixture.db.Create(&collision).Error)
	}
	committed, err := service.ResolveLedgerSubmissionOutcome(fixture.c, fixture.info, outcome.Task)
	require.NoError(t, err)
	assert.True(t, committed)

	wrongRevision := *outcome.Task
	wrongRevision.PrivateData.AllocationRevision++
	committed, err = service.ResolveLedgerSubmissionOutcome(fixture.c, fixture.info, &wrongRevision)
	require.ErrorIs(t, err, model.ErrBudgetAttemptConflict)
	assert.False(t, committed)

	// A duplicate within the expected owner/channel is genuinely ambiguous and
	// must preserve the charge rather than deciding to refund it.
	ambiguous := *outcome.Task
	ambiguous.ID = 0
	require.NoError(t, fixture.db.Create(&ambiguous).Error)
	committed, err = service.ResolveLedgerSubmissionOutcome(fixture.c, fixture.info, outcome.Task)
	require.ErrorIs(t, err, model.ErrBudgetAttemptConflict)
	assert.False(t, committed)
	user, token := fixture.reload(t)
	assert.Equal(t, 99100, user.Quota)
	assert.Equal(t, 99100, token.RemainQuota)
}

func TestLedgerTaskSubmissionUsesCurrentTokenCredential(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted=%t", deleted), func(t *testing.T) {
			fixture := newLedgerSubmissionFixture(t, true)
			if deleted {
				require.NoError(t, fixture.db.Delete(&fixture.token).Error)
			} else {
				require.NoError(t, fixture.db.Model(&fixture.token).UpdateColumn("key", "rotated-token").Error)
			}
			committed, _ := fixture.insertWithSnapshot(t, "nil")
			if deleted {
				assert.Empty(t, committed.TokenKey, "a deleted token cannot receive a stale cache delta")
			} else {
				assert.Equal(t, "rotated-token", committed.TokenKey)
				var token model.Token
				require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
				assert.Equal(t, 99100, token.RemainQuota)
				assert.Equal(t, 900, token.UsedQuota)
			}
			var user model.User
			require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
			assert.Equal(t, 99100, user.Quota)
		})
	}
}

// The ledger move is replayable: finalizing the same submission again writes no
// further usage, while the cache delta was published once from the committed
// charge at the call site.
func TestLedgerTaskSubmissionFinalizeIsReplaySafe(t *testing.T) {
	fixture := newLedgerSubmissionFixture(t, true)
	writes := 0
	callback := "test:ledger-submit-publish"
	require.NoError(t, fixture.db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_budget_reservations" {
			writes++
		}
	}))
	t.Cleanup(func() { require.NoError(t, fixture.db.Callback().Update().Remove(callback)) })

	outcome, taskErr := fixture.submitAccepted(t, 900)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	before := writes
	committed := model.TaskBudgetInsertResult{
		ReservationID: outcome.Task.PrivateData.BudgetReservationId,
		Delta:         900, TokenID: fixture.token.Id, TokenKey: fixture.token.Key,
	}
	service.FinalizeTaskBudgetSubmission(fixture.c, fixture.info, outcome.Task, committed, 900)
	service.FinalizeTaskBudgetSubmission(fixture.c, fixture.info, outcome.Task, committed, 900)
	assert.Equal(t, before, writes, "a replay cannot move the ledger again")

	var reservation model.ChannelBudgetReservation
	require.NoError(t, fixture.db.First(&reservation, outcome.Task.PrivateData.BudgetReservationId).Error)
	assert.EqualValues(t, 900, reservation.SettledQuota)
	user, token := fixture.reload(t)
	assert.Equal(t, 100000-900, user.Quota)
	assert.Equal(t, 100000-900, token.RemainQuota)
}

// A legacy task without a ledger reservation keeps the pre-existing path: it
// reserves through the billing session and settles through it afterwards.
func TestLegacyTaskSubmissionKeepsSessionPath(t *testing.T) {
	fixture := newLedgerSubmissionFixture(t, false)
	events := []string{}
	billing := &taskSubmissionTestBilling{events: &events}
	fixture.info.Billing = billing
	outcome, taskErr := fixture.submitAccepted(t, 900)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, []string{"reserve", "settle"}, events)
	var stored model.Task
	require.NoError(t, fixture.db.Where("task_id = ?", outcome.Task.TaskID).First(&stored).Error)
	assert.False(t, stored.PrivateData.BudgetPending)
}

// Snapshot handling keeps the diagnostics contract of the existing insert path
// while still committing the ledger charge exactly once: nil and disabled send
// nothing to the store, a recorded payload is retained, and an unavailable
// detail store degrades to storage_failed without losing the task or charge.
func (f *ledgerSubmissionFixture) insertWithSnapshot(t *testing.T, state string) (*model.TaskBudgetInsertResult, *model.Task) {
	t.Helper()
	var snapshot *common.RequestContentSnapshot
	switch state {
	case "nil":
	case "disabled":
		snapshot = &common.RequestContentSnapshot{RequestID: "req-disabled", UserID: f.user.Id,
			Status: common.RequestContentStatusDisabled, SubmitNodeName: "node-test"}
	case "recorded", "storage-failed":
		snapshot = &common.RequestContentSnapshot{RequestID: "req-" + state, UserID: f.user.Id,
			Ref: "a1b2c3d4e5f60718293a4b5c6d7e8f90", Payload: []byte(`{"prompt":"hello"}`),
			Status: common.RequestContentStatusRecorded, SubmitNodeName: "node-test", CapturedBytes: 18}
	}
	if state == "storage-failed" {
		// Keep coordination available, but make the payload write fail on the
		// actual SQL store. The task and its charge must still commit once.
		require.NoError(t, f.db.Migrator().DropTable(&model.LogRequestDetail{}))
	}

	task := model.InitTask(constant.TaskPlatform("plugin"), f.info)
	task.ChannelId, task.UserId = f.channel.Id, f.user.Id
	task.PrivateData.AllocationId = f.info.ChannelBudget.AllocationID
	task.PrivateData.AllocationRevision = f.info.ChannelBudget.AllocationRevision
	task.PrivateData.BudgetReservationId = f.info.ChannelBudget.ReservationID
	task.PrivateData.BudgetSettlementRevision = f.info.ChannelBudget.Revision
	task.PrivateData.TokenId = f.token.Id
	task.Quota = 900
	committed, err := model.InsertTaskWithSubmissionCharge(context.Background(), task, snapshot, nil,
		&model.TaskBudgetSubmit{Total: 900, Delta: 900, TokenID: f.token.Id, Final: true})
	require.NoError(t, err)
	require.NotNil(t, committed)
	return committed, task
}

func TestLedgerTaskSubmissionSnapshotStates(t *testing.T) {
	for _, state := range []string{"nil", "disabled", "recorded", "storage-failed"} {
		t.Run(state, func(t *testing.T) {
			fixture := newLedgerSubmissionFixture(t, true, &model.LogRequestContentProtection{}, &model.LogRequestDetail{})
			_, task := fixture.insertWithSnapshot(t, state)

			var stored model.Task
			require.NoError(t, fixture.db.Where("task_id = ?", task.TaskID).First(&stored).Error)
			if state == "nil" {
				assert.Nil(t, stored.PrivateData.RequestContent)
			} else {
				require.NotNil(t, stored.PrivateData.RequestContent)
				assert.Equal(t, "node-test", stored.PrivateData.RequestContent.SubmitNodeName)
				switch state {
				case "disabled":
					assert.Equal(t, common.RequestContentStatusDisabled, stored.PrivateData.RequestContent.Status)
					assert.Empty(t, stored.PrivateData.RequestContent.Ref)
				case "storage-failed":
					assert.Equal(t, common.RequestContentStatusStorageFail, stored.PrivateData.RequestContent.Status)
					assert.Empty(t, stored.PrivateData.RequestContent.Ref)
				case "recorded":
					assert.Equal(t, common.RequestContentStatusRecorded, stored.PrivateData.RequestContent.Status)
					require.True(t, model.ValidLogRequestContentRef(stored.PrivateData.RequestContent.Ref))
					var detail model.LogRequestDetail
					require.NoError(t, fixture.db.Where("ref = ?", stored.PrivateData.RequestContent.Ref).Take(&detail).Error)
					assert.JSONEq(t, `{"prompt":"hello"}`, string(detail.Payload))
					assert.Equal(t, "req-recorded", detail.RequestID)
					assert.Equal(t, fixture.user.Id, detail.UserID)
					assert.Equal(t, common.RequestContentStatusRecorded, detail.Status)
					assert.Equal(t, 18, detail.CapturedBytes)
					var protection model.LogRequestContentProtection
					require.NoError(t, fixture.db.Where("ref = ?", detail.Ref).Take(&protection).Error)
					assert.Equal(t, stored.TaskID, protection.TaskID)
					assert.Equal(t, "plugin", protection.TaskPlatform)
				}
			}
			if state != "recorded" {
				var count int64
				require.NoError(t, fixture.db.Model(&model.LogRequestContentProtection{}).Count(&count).Error)
				assert.Zero(t, count)
				if state != "storage-failed" {
					require.NoError(t, fixture.db.Model(&model.LogRequestDetail{}).Count(&count).Error)
					assert.Zero(t, count)
				}
			}
			var reservation model.ChannelBudgetReservation
			require.NoError(t, fixture.db.First(&reservation, fixture.info.ChannelBudget.ReservationID).Error)
			assert.Equal(t, stored.TaskID, reservation.TaskId)
			assert.EqualValues(t, 900, reservation.DesiredQuota)
			assert.True(t, reservation.ReconcilePending, "the transaction persists the replayable ledger target")
			user, token := fixture.reload(t)
			assert.Equal(t, 100000-900, user.Quota, fmt.Sprintf("%s: exactly one charge", state))
			assert.Equal(t, 100000-900, token.RemainQuota)
		})
	}
}
