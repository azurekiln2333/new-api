package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const channelCapacityLeaseTTL = 30 * time.Second
const channelCapacityRedisTimeout = 2 * time.Second

// Server time and owner tokens prevent a delayed gateway from renewing an
// expired lease or releasing the slot of a later owner. Expired owners are
// removed atomically before checking capacity; the key expires when idle.
var channelCapacityAcquireScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1] * 1000 + math.floor(clock[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then return 0 end
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[3]), ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

var channelCapacityRenewScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1] * 1000 + math.floor(clock[2] / 1000)
local expiry = redis.call('ZSCORE', KEYS[1], ARGV[1])
if not expiry or tonumber(expiry) <= now then return 0 end
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

var localChannelCapacity = struct {
	sync.Mutex
	active map[int]int
}{active: make(map[int]int)}

// ChannelCapacityLease owns one admitted upstream attempt. The caller must
// release it after the handler/connection has closed, even on panic. Renewal
// continues until cleanup: native submission streams deliberately outlive the
// client. Unlimited channels need no counter or background worker.
type ChannelCapacityLease struct {
	ctx       context.Context
	control   context.Context
	cancel    context.CancelCauseFunc
	once      sync.Once
	channelID int
	client    *redis.Client
	key       string
	owner     string
	done      chan struct{}
}

type channelCapacityContextKey struct{}

func newChannelCapacityLease(parent context.Context, channelID int) *ChannelCapacityLease {
	control, cancelControl := context.WithCancelCause(context.Background())
	ctx, cancelRequest := context.WithCancelCause(parent)
	lease := &ChannelCapacityLease{control: control, channelID: channelID, cancel: func(cause error) {
		cancelControl(cause)
		cancelRequest(cause)
	}}
	lease.ctx = context.WithValue(ctx, channelCapacityContextKey{}, lease)
	return lease
}

// WithChannelCapacityCancellation restores lease-loss cancellation after a
// deliberate context.WithoutCancel. Client cancellation remains detached; the
// admitted provider work must still stop if its distributed slot is lost.
func WithChannelCapacityCancellation(ctx context.Context) (context.Context, context.CancelFunc) {
	lease, _ := ctx.Value(channelCapacityContextKey{}).(*ChannelCapacityLease)
	if lease == nil {
		return ctx, func() {}
	}
	bound, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(lease.control, func() { cancel(context.Cause(lease.control)) })
	if cause := context.Cause(lease.control); cause != nil {
		cancel(cause)
	}
	return bound, func() { stop(); cancel(context.Canceled) }
}

func (l *ChannelCapacityLease) Context() context.Context { return l.ctx }

// Err reports coordination loss separately from client cancellation and from
// the transport error resulting from canceling an admitted attempt.
func (l *ChannelCapacityLease) Err() *types.NewAPIError {
	var apiErr *types.NewAPIError
	if l.control != nil && errors.As(context.Cause(l.control), &apiErr) && IsChannelCapacityError(apiErr) {
		return apiErr
	}
	return nil
}

func (l *ChannelCapacityLease) Release() {
	if l == nil || l.cancel == nil {
		return
	}
	l.once.Do(func() {
		l.cancel(context.Canceled)
		if l.client != nil {
			<-l.done
			ctx, cancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
			defer cancel()
			// Failure leaves only this owner's expiring lease, never a local
			// fallback counter or a deletion of a successor's reservation.
			_ = l.client.ZRem(ctx, l.key, l.owner).Err()
			return
		}
		localChannelCapacity.Lock()
		defer localChannelCapacity.Unlock()
		localChannelCapacity.active[l.channelID]--
		if localChannelCapacity.active[l.channelID] == 0 {
			delete(localChannelCapacity.active, l.channelID)
		}
	})
}

func channelCapacityError(code types.ErrorCode) *types.NewAPIError {
	status, message := http.StatusTooManyRequests, i18n.MsgChannelCapacitySaturated
	if code == types.ErrorCodeChannelCapacityUnavailable {
		status, message = http.StatusServiceUnavailable, i18n.MsgChannelCapacityUnavailable
	}
	return types.NewErrorWithStatusCode(errors.New(i18n.Translate("", message)), code, status, types.ErrOptionWithSkipRetry())
}

func IsChannelCapacityError(err *types.NewAPIError) bool {
	return err != nil && (err.GetErrorCode() == types.ErrorCodeChannelCapacitySaturated || err.GetErrorCode() == types.ErrorCodeChannelCapacityUnavailable)
}

func AcquireChannelCapacity(ctx context.Context, channelID int, settings dto.ChannelSettings) (*ChannelCapacityLease, *types.NewAPIError) {
	if err := ctx.Err(); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusRequestTimeout, types.ErrOptionWithSkipRetry())
	}
	if settings.MaxConcurrentRequests == nil || *settings.MaxConcurrentRequests <= 0 {
		// Native submission retries can carry a detached previous attempt's
		// context values. Unlimited admission must not rebind that old lease.
		return &ChannelCapacityLease{ctx: context.WithValue(ctx, channelCapacityContextKey{}, (*ChannelCapacityLease)(nil))}, nil
	}
	limit := *settings.MaxConcurrentRequests
	if !common.RedisEnabled {
		localChannelCapacity.Lock()
		defer localChannelCapacity.Unlock()
		if localChannelCapacity.active[channelID] >= limit {
			return nil, channelCapacityError(types.ErrorCodeChannelCapacitySaturated)
		}
		localChannelCapacity.active[channelID]++
		return newChannelCapacityLease(ctx, channelID), nil
	}
	// A configured shared coordinator must never degrade into independent
	// per-process admission, including during startup/reconnection failures.
	if common.RDB == nil {
		return nil, channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
	}
	return acquireRedisChannelCapacity(ctx, common.RDB, channelID, limit)
}

func acquireRedisChannelCapacity(ctx context.Context, client *redis.Client, channelID, limit int) (*ChannelCapacityLease, *types.NewAPIError) {
	key := fmt.Sprintf("new-api:channel-capacity:%d", channelID)
	owner := uuid.NewString()
	commandCtx, cancel := context.WithTimeout(ctx, channelCapacityRedisTimeout)
	admitted, err := channelCapacityAcquireScript.Run(commandCtx, client, []string{key}, limit, owner, channelCapacityLeaseTTL.Milliseconds()).Int()
	cancel()
	if err != nil {
		// The result of a timed-out write is ambiguous. Remove this token
		// only; expiration also bounds cleanup if Redis is still unavailable.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
		_ = client.ZRem(cleanupCtx, key, owner).Err()
		cleanupCancel()
		return nil, channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
	}
	if admitted == 0 {
		return nil, channelCapacityError(types.ErrorCodeChannelCapacitySaturated)
	}
	lease := newChannelCapacityLease(ctx, channelID)
	lease.client, lease.key, lease.owner, lease.done = client, key, owner, make(chan struct{})
	go lease.maintain()
	return lease, nil
}

func (l *ChannelCapacityLease) renew() bool {
	ctx, cancel := context.WithTimeout(l.control, channelCapacityRedisTimeout)
	defer cancel()
	renewed, err := channelCapacityRenewScript.Run(ctx, l.client, []string{l.key}, l.owner, channelCapacityLeaseTTL.Milliseconds()).Int()
	if err != nil || renewed != 1 {
		l.cancel(channelCapacityError(types.ErrorCodeChannelCapacityUnavailable))
		return false
	}
	return true
}

func (l *ChannelCapacityLease) maintain() {
	defer close(l.done)
	ticker := time.NewTicker(channelCapacityLeaseTTL / 3)
	defer ticker.Stop()
	for {
		select {
		case <-l.control.Done():
			return
		case <-ticker.C:
			if !l.renew() {
				return
			}
		}
	}
}

func AcquireRequestChannelCapacity(c *gin.Context, channelID int) (*ChannelCapacityLease, *types.NewAPIError) {
	settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	lease, err := AcquireChannelCapacity(c.Request.Context(), channelID, settings)
	if IsChannelCapacityError(err) {
		key := i18n.MsgChannelCapacitySaturated
		if err.GetErrorCode() == types.ErrorCodeChannelCapacityUnavailable {
			key = i18n.MsgChannelCapacityUnavailable
		}
		err.SetMessage(i18n.T(c, key))
	}
	return lease, err
}

// RetryChannelAdmission excludes a busy candidate without recording an
// upstream failure or consuming the upstream retry budget. Pins and strict
// sessions keep their routing boundary even when no upstream call was made.
func RetryChannelAdmission(c *gin.Context, channelID int, err *types.NewAPIError, retry *RetryParam) bool {
	if !IsChannelCapacityError(err) {
		return false
	}
	state := RequestPolicy(c)
	decision := PolicyDecision{Action: "stop", Reason: string(err.GetErrorCode()), Source: "capacity"}
	_, pinned, _ := GetChannelConstraints(c).ResolvedPin()
	if err.GetErrorCode() == types.ErrorCodeChannelCapacitySaturated && c.Request.Context().Err() == nil &&
		!pinned && !ShouldSkipRetryAfterChannelAffinityFailure(c) && state.SessionMode != "strict" && !c.Writer.Written() {
		if !slices.Contains(state.SaturatedChannelIDs, channelID) {
			state.SaturatedChannelIDs = append(state.SaturatedChannelIDs, channelID)
			// Auto selection may have prepared the NEXT retry's group before
			// admission (including the initial middleware selection). A busy
			// candidate did not use that retry: exhaust its actual group first.
			if snapshot := state.selectedAutoGroupRetry; snapshot != nil && snapshot.channelID == channelID && retry != nil && retry.TokenGroup == "auto" {
				common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, snapshot.groupIndex)
				retry.SetRetry(snapshot.retry)
				retry.resetNextTry = snapshot.resetNextTry
				c.Set(ginKeyChannelAffinityRetryOffset, snapshot.affinityRetryOffset)
			}
			decision.Action = "select"
		}
	}
	state.AddEvent(PolicyEvent{ChannelID: channelID, Status: err.StatusCode, ErrorCode: string(err.GetErrorCode()), ErrorSource: "local", Decision: decision, Health: "unchanged"})
	return decision.Action == "select"
}
