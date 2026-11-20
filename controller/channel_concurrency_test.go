package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type channelConcurrencyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			Id         int    `json:"id"`
			Current    *int   `json:"current"`
			Limit      *int   `json:"limit"`
			Scope      string `json:"scope"`
			SampleTime int64  `json:"sample_time"`
		} `json:"items"`
	} `json:"data"`
}

func TestGetChannelConcurrencySamplesVisibleChannels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupChannelCreateTest(t)
	db := model.DB
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })

	limitedSetting := fmt.Sprintf(`{"max_concurrent_requests":%d}`, 2)
	unlimitedSetting := `{"max_concurrent_requests":0}`
	channels := []*model.Channel{
		{Id: 82001, Name: "limited", Key: "k", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default", Setting: &limitedSetting},
		{Id: 82002, Name: "unlimited", Key: "k", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default"},
		{Id: 82003, Name: "idle", Key: "k", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default", Setting: &unlimitedSetting},
	}
	require.NoError(t, db.Create(&channels).Error)
	t.Cleanup(func() { require.NoError(t, db.Delete(&channels).Error) })

	first, apiErr := service.AcquireChannelCapacity(context.Background(), 82001, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(2)})
	require.Nil(t, apiErr)
	defer first.Release()
	second, apiErr := service.AcquireChannelCapacity(context.Background(), 82001, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(2)})
	require.Nil(t, apiErr)
	defer second.Release()
	unlimited, apiErr := service.AcquireChannelCapacity(context.Background(), 82002, dto.ChannelSettings{})
	require.Nil(t, apiErr)
	defer unlimited.Release()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/concurrency?ids=82001,82002,82003,999999,82001,invalid,-1", nil)
	GetChannelConcurrency(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response channelConcurrencyResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data.Items, 3, "a missing channel is omitted, an idle one is still sampled")

	items := make(map[int]struct {
		current    *int
		limit      *int
		scope      string
		sampleTime int64
	}, len(response.Data.Items))
	for _, item := range response.Data.Items {
		items[item.Id] = struct {
			current    *int
			limit      *int
			scope      string
			sampleTime int64
		}{item.Current, item.Limit, item.Scope, item.SampleTime}
	}

	limited := items[82001]
	require.NotNil(t, limited.current)
	assert.Equal(t, 2, *limited.current)
	require.NotNil(t, limited.limit)
	assert.Equal(t, 2, *limited.limit)
	assert.Equal(t, service.ChannelConcurrencyScopeInstance, limited.scope)
	assert.NotZero(t, limited.sampleTime)

	unlimitedSample := items[82002]
	require.NotNil(t, unlimitedSample.current, "an unlimited channel is counted, not reported as unknown")
	assert.Equal(t, 1, *unlimitedSample.current)
	assert.Nil(t, unlimitedSample.limit, "null limit means unlimited")

	idle := items[82003]
	require.NotNil(t, idle.current, "an idle channel is zero, not unavailable")
	assert.Equal(t, 0, *idle.current)
	assert.Nil(t, idle.limit)
}

func TestGetChannelConcurrencyReportsUnavailableSampling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupChannelCreateTest(t)
	db := model.DB
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	// A configured-but-unreachable coordinator must not turn unlimited traffic
	// away, and its missing sample must not look like zero concurrency.
	common.RedisEnabled, common.RDB = true, nil
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })

	channel := &model.Channel{Id: 82010, Name: "unlimited", Key: "k", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default"}
	require.NoError(t, db.Create(channel).Error)
	t.Cleanup(func() { require.NoError(t, db.Delete(channel).Error) })
	lease, apiErr := service.AcquireChannelCapacity(context.Background(), 82010, dto.ChannelSettings{})
	require.Nil(t, apiErr, "telemetry failure may not reject unlimited traffic")
	defer lease.Release()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/concurrency?ids=82010", nil)
	GetChannelConcurrency(c)

	var response channelConcurrencyResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data.Items, 1)
	if assert.Nil(t, response.Data.Items[0].Current) {
		assert.Equal(t, service.ChannelConcurrencyScopeCluster, response.Data.Items[0].Scope)
	}
	assert.Nil(t, response.Data.Items[0].Limit)
}

func TestGetChannelConcurrencyRejectsOversizedBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	ids := ""
	for id := 1; id <= maxChannelConcurrencyIds+1; id++ {
		if id > 1 {
			ids += ","
		}
		ids += fmt.Sprintf("%d", id)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/concurrency?ids="+ids, nil)
	GetChannelConcurrency(c)

	var response channelConcurrencyResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success, "an oversized batch is an explicit error, never a silent truncation")
	assert.Contains(t, response.Message, fmt.Sprintf("maximum is %d", maxChannelConcurrencyIds))
}

func TestGetChannelConcurrencyDoesNotMutateInvalidSettings(t *testing.T) {
	setupChannelCreateTest(t)
	db := model.DB
	invalidSetting := `{"max_concurrent_requests":`
	channel := &model.Channel{Name: "invalid-concurrency-setting", Key: "k", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default", Setting: &invalidSetting}
	require.NoError(t, db.Create(channel).Error)
	t.Cleanup(func() { require.NoError(t, db.Delete(channel).Error) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channel/concurrency?ids=%d", channel.Id), nil)
	GetChannelConcurrency(c)

	var response channelConcurrencyResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success, "an invalid limit cannot be reported as unlimited")
	var persisted model.Channel
	require.NoError(t, db.First(&persisted, channel.Id).Error)
	require.NotNil(t, persisted.Setting)
	assert.Equal(t, invalidSetting, *persisted.Setting, "sampling must not repair or erase stored settings")
}
