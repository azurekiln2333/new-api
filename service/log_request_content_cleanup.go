package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	logRequestContentCleanupInterval = 10 * time.Minute
	logRequestContentCleanupTimeout  = 5 * time.Minute
	logRequestContentCleanupBatch    = 100
)

// StartLogRequestContentCleanup reclaims original request bodies whose usage
// logs and active asynchronous tasks are all gone. It also covers bodies
// orphaned by a failed cross-store write and the native ClickHouse log TTL,
// which cannot delete the payload table on its own. Only the master instance
// sweeps; every other node still triggers a bounded sweep when it deletes logs.
func StartLogRequestContentCleanup() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		cleanupLogRequestContent()
		ticker := time.NewTicker(logRequestContentCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleanupLogRequestContent()
		}
	}()
}

func cleanupLogRequestContent() {
	ctx, cancel := context.WithTimeout(context.Background(), logRequestContentCleanupTimeout)
	defer cancel()
	// Resolve abandoned cross-store operations before reclaiming anything; an
	// uncertain operation stays protected instead of being guessed complete.
	if err := model.RecoverLogRequestContentOperations(ctx, logRequestContentCleanupBatch); err != nil {
		common.SysError(common.LogText("failed to recover original request content operations: %s", err.Error()))
	}
	if err := model.CleanupLogRequestDetails(ctx, common.GetTimestamp(), logRequestContentCleanupBatch); err != nil {
		common.SysError(common.LogText("failed to reclaim original request content: %s", err.Error()))
	}
}
