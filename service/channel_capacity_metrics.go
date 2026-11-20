package service

import (
	"context"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// Channel cards and details read live concurrency through these samples. Redis
// mode reports every gateway's owners in the shared coordinator; memory mode
// reports this process only, which the scope field makes explicit.
const (
	ChannelConcurrencyScopeCluster  = "cluster"
	ChannelConcurrencyScopeInstance = "instance"
)

// ChannelConcurrencySample is one channel's live attempt count. A nil Current
// means the sample is unavailable, never zero: an idle channel reports 0.
type ChannelConcurrencySample struct {
	Current    *int
	Scope      string
	SampleTime time.Time
}

// ReadChannelConcurrency returns one sample per requested channel ID. It never
// writes: Redis mode counts unexpired lease scores at Redis server time, and
// memory mode takes one map snapshot. A coordinator error leaves those channels
// unavailable instead of reporting a false zero.
//
// Redis-mode samples are a best-effort cluster view. A live owner is missing
// from the count if its gateway cannot reach the coordinator at all; when that
// gateway can still publish the shared incomplete marker, every sampler reports
// nil instead. A gateway cannot discover another gateway's one-sided partition,
// so the count may briefly undercount and is never an exact capacity statement.
func ReadChannelConcurrency(ctx context.Context, channelIDs []int) map[int]ChannelConcurrencySample {
	samples := make(map[int]ChannelConcurrencySample, len(channelIDs))
	if !common.RedisEnabled {
		now := time.Now()
		localChannelCapacity.Lock()
		for _, channelID := range channelIDs {
			current := localChannelCapacity.active[channelID]
			samples[channelID] = ChannelConcurrencySample{Current: &current, Scope: ChannelConcurrencyScopeInstance, SampleTime: now}
		}
		localChannelCapacity.Unlock()
		return samples
	}
	unavailable := ChannelConcurrencySample{Scope: ChannelConcurrencyScopeCluster, SampleTime: time.Now()}
	for _, channelID := range channelIDs {
		samples[channelID] = unavailable
	}
	client := common.RDB
	if client == nil || len(channelIDs) == 0 {
		return samples
	}
	// This gateway has a live unlimited attempt whose owner entry is missing and
	// whose shared marker may also be unwritable; its count omits that attempt,
	// so the sample is incomplete rather than merely stale. Those channels are
	// reported as unavailable even when the shared count itself succeeds.
	localIncomplete := make(map[int]struct{}, len(samples))
	for channelID := range samples {
		if hasLostChannelCapacityOwner(channelID) {
			localIncomplete[channelID] = struct{}{}
		}
	}
	commandCtx, cancel := context.WithTimeout(ctx, channelCapacityRedisTimeout)
	defer cancel()
	return readRedisChannelConcurrency(commandCtx, client, channelIDs, samples, localIncomplete)
}

// readRedisChannelConcurrency pipelines one non-mutating count script per key.
// A live owner missing from the shared count publishes an incomplete marker
// there, so a sampler on any gateway sees the marker and reports unavailable
// rather than a plausible-looking low cluster count.
func readRedisChannelConcurrency(ctx context.Context, client *redis.Client, channelIDs []int, samples map[int]ChannelConcurrencySample, localIncomplete map[int]struct{}) map[int]ChannelConcurrencySample {
	for attempt := range 2 {
		pipe := client.Pipeline()
		commands := make(map[int]*redis.Cmd, len(channelIDs))
		for _, channelID := range channelIDs {
			commands[channelID] = channelCapacityCountScript.Run(ctx, pipe, []string{channelCapacityKey(channelID), channelCapacityMarkerKey(channelID)})
		}
		_, _ = pipe.Exec(ctx)
		missingScript := false
		for channelID, command := range commands {
			values, err := command.Slice()
			if err != nil {
				if strings.HasPrefix(err.Error(), "NOSCRIPT ") {
					missingScript = true
				}
				continue
			}
			if len(values) != 3 {
				continue
			}
			serverMillis, serverOK := values[0].(int64)
			current, currentOK := values[1].(int64)
			incomplete, incompleteOK := values[2].(int64)
			if !serverOK || !currentOK || !incompleteOK {
				continue
			}
			// Each script reads its own server TIME, so a pipelined batch is not
			// one atomic instant; report the time this channel's count belongs to.
			sampleTime := time.UnixMilli(serverMillis)
			_, locallyLost := localIncomplete[channelID]
			if serverMillis <= 0 || incomplete > 0 || locallyLost {
				samples[channelID] = ChannelConcurrencySample{Scope: ChannelConcurrencyScopeCluster, SampleTime: sampleTime}
				continue
			}
			count := int(current)
			samples[channelID] = ChannelConcurrencySample{Current: &count, Scope: ChannelConcurrencyScopeCluster, SampleTime: sampleTime}
		}
		if !missingScript || attempt == 1 {
			return samples
		}
		// Refill the coordinator cache and retry the whole batch once.
		loadCtx, cancel := context.WithTimeout(ctx, channelCapacityTelemetryTimeout)
		_ = channelCapacityCountScript.Load(loadCtx, client).Err()
		cancel()
	}
	return samples
}
