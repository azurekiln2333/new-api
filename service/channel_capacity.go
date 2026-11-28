package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const channelCapacityLeaseTTL = 30 * time.Second
const channelCapacityRedisTimeout = 2 * time.Second

const (
	channelCapacityAdmissionTimeoutEnv     = "CHANNEL_CAPACITY_ADMISSION_TIMEOUT_MS"
	channelCapacityAdmissionTimeoutDefault = 3000
	channelCapacityAdmissionTimeoutMin     = 250
	channelCapacityAdmissionTimeoutMax     = 10000
	channelCapacityAdmissionCleanupTimeout = 250 * time.Millisecond
	channelCapacityRenewalSafetyMargin     = time.Second
	channelCapacityRedisErrorLogInterval   = time.Minute
)

type channelCapacityPhase string

const (
	channelCapacityAdmissionPhase     channelCapacityPhase = "admission"
	channelCapacityRenewalPhase       channelCapacityPhase = "renewal"
	channelCapacityProbeRenewalPhase  channelCapacityPhase = "probe_renewal"
	channelCapacityQueueRegisterPhase channelCapacityPhase = "queue_register"
	channelCapacityQueueAcquirePhase  channelCapacityPhase = "queue_acquire"
	channelCapacityBusyReportPhase    channelCapacityPhase = "busy_report"
)

type channelCapacityRedisFailure struct {
	channelID  int
	phase      channelCapacityPhase
	errorClass string
}

var channelCapacityRedisErrorLog = struct {
	sync.Mutex
	lastByFailure map[channelCapacityRedisFailure]time.Time
}{lastByFailure: make(map[channelCapacityRedisFailure]time.Time)}

func channelCapacityAdmissionTimeout() time.Duration {
	milliseconds := common.GetEnvOrDefault(channelCapacityAdmissionTimeoutEnv, channelCapacityAdmissionTimeoutDefault)
	milliseconds = max(channelCapacityAdmissionTimeoutMin, min(channelCapacityAdmissionTimeoutMax, milliseconds))
	return time.Duration(milliseconds) * time.Millisecond
}

func channelCapacityRedisErrorClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if err.Error() == "redis: connection pool timeout" {
		return "pool_timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "network"
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, redis.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "network"
	}
	var redisError redis.Error
	if errors.As(err, &redisError) {
		return "redis_server"
	}
	return "redis_command"
}

// Keep only the server request correlation, never a Gin context, credentials,
// request payload, caller deadline or cancellation, for asynchronous diagnostics.
func channelCapacityDiagnosticContext(ctx context.Context) context.Context {
	background := context.Background()
	if ctx == nil {
		return background
	}
	requestID, _ := ctx.Value(common.RequestIdKey).(string)
	if requestID == "" || len(requestID) > 128 {
		return background
	}
	for _, char := range requestID {
		if char != '-' && char != '_' && (char < '0' || char > '9') &&
			(char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			return background
		}
	}
	return context.WithValue(background, common.RequestIdKey, requestID)
}

// phase and errorClass are fixed internal categories, never Redis error text.
// Different failure phases remain distinguishable without an unbounded map or
// per-request log flood. Suppression affects diagnostics only, not admission.
func logChannelCapacityRedisError(ctx context.Context, channelID int, phase channelCapacityPhase, errorClass string) {
	if errorClass == "canceled" || (ctx != nil && ctx.Err() != nil) {
		return
	}
	now := time.Now()
	failure := channelCapacityRedisFailure{channelID: channelID, phase: phase, errorClass: errorClass}
	channelCapacityRedisErrorLog.Lock()
	last := channelCapacityRedisErrorLog.lastByFailure[failure]
	if now.Sub(last) < channelCapacityRedisErrorLogInterval {
		channelCapacityRedisErrorLog.Unlock()
		return
	}
	for key, loggedAt := range channelCapacityRedisErrorLog.lastByFailure {
		if now.Sub(loggedAt) >= channelCapacityRedisErrorLogInterval {
			delete(channelCapacityRedisErrorLog.lastByFailure, key)
		}
	}
	if len(channelCapacityRedisErrorLog.lastByFailure) >= 4096 {
		channelCapacityRedisErrorLog.Unlock()
		return
	}
	channelCapacityRedisErrorLog.lastByFailure[failure] = now
	channelCapacityRedisErrorLog.Unlock()
	logger.LogWarn(channelCapacityDiagnosticContext(ctx), common.LogText("channel capacity Redis coordination failed: channel_id=%d phase=%s error_class=%s node=%q", channelID, phase, errorClass, common.NodeName))
}

func channelCapacityRenewalTimeout(deadline time.Time, timeout time.Duration) time.Duration {
	remaining := time.Until(deadline)
	if remaining <= channelCapacityRenewalSafetyMargin {
		return 0
	}
	return min(timeout, remaining-channelCapacityRenewalSafetyMargin)
}

// Unlimited counting is telemetry only; it must not add the full coordinator
// timeout to a request that would otherwise leave immediately.
const channelCapacityTelemetryTimeout = 500 * time.Millisecond

// A degraded owner's marker outlives several retries so a single failed retry
// does not make an incomplete cluster sample look complete.
const channelCapacityTelemetryRetry = 2 * time.Second

// The marker must outlive more than one retry interval, otherwise a reader
// landing between a marker's expiry and the next publish would see a complete
// sample while a live owner is still missing from the count.
const channelCapacityMarkerTTL = 3 * channelCapacityTelemetryRetry

// Publishing a marker after a failed registration needs its own short context:
// the registration context is usually already expired at that point. Admission
// latency stays bounded because the two attempts run sequentially with these
// fixed timeouts.
const channelCapacityMarkerTimeout = channelCapacityTelemetryTimeout / 2

func channelCapacityKey(channelID int) string {
	return fmt.Sprintf("new-api:channel-capacity:%d", channelID)
}

// The marker key is shared: any gateway that lost a live owner's counter entry
// publishes a marker there, so every instance reports an incomplete sample
// instead of a silently low cluster count.
func channelCapacityMarkerKey(channelID int) string {
	return fmt.Sprintf("new-api:channel-capacity-incomplete:%d", channelID)
}

// Server time and owner tokens prevent a delayed gateway from renewing an
// expired lease or releasing the slot of a later owner. Expired owners are
// removed atomically before checking capacity; the key expires when idle.
var channelCapacityAcquireScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1] * 1000 + math.floor(clock[2] / 1000)
-- A lost reply can replay this same admission after another request queued.
-- Confirm only an existing live owner, without renewing its score or key TTL.
local expiry = redis.call('ZSCORE', KEYS[1], ARGV[2])
if expiry and tonumber(expiry)>now then return 1 end
if #KEYS >= 3 then
  local head = redis.call('ZRANGE',KEYS[2],0,0)[1]
  while head do
    local expiry = redis.call('ZSCORE',KEYS[3],head)
    if expiry and tonumber(expiry)>now then return 0 end
    redis.call('ZREM',KEYS[2],head)
    head = redis.call('ZRANGE',KEYS[2],0,0)[1]
  end
end
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then return 0 end
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[3]), ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

// Unlimited channels are counted without a cap: the same lease lifecycle owns
// the owner token, while zero/unset still means "never reject".
var channelCapacityCountAdmitScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1] * 1000 + math.floor(clock[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

// Owner markers expire on their own; the write-time reap only bounds the set
// and never touches the capacity key.
var channelCapacityMarkerScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1] * 1000 + math.floor(clock[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

// Non-mutating read. Every invocation reads its own Redis server TIME, so the
// returned time belongs to that channel's count; a pipelined batch is not one
// atomic instant. Expired owners and markers are excluded, never reaped here.
var channelCapacityCountScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1] * 1000 + math.floor(clock[2] / 1000)
local current = redis.call('ZCOUNT', KEYS[1], '(' .. now, '+inf')
local incomplete = redis.call('ZCOUNT', KEYS[2], '(' .. now, '+inf')
return {now, current, incomplete}
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

// channelCapacityLostOwners counts live unlimited attempts on this gateway that
// are outside the shared count. It makes the local sampler report unavailable
// even when the shared marker could not be published, which is the case during
// a coordinator outage or a one-sided partition. It cannot discover another
// gateway's partition; the cluster sample is therefore best effort and may
// briefly undercount, never a claimed exact value.
var channelCapacityLostOwners = struct {
	sync.Mutex
	counts map[int]int
}{counts: make(map[int]int)}

func addLostChannelCapacityOwner(channelID int) {
	channelCapacityLostOwners.Lock()
	channelCapacityLostOwners.counts[channelID]++
	channelCapacityLostOwners.Unlock()
}

func removeLostChannelCapacityOwner(channelID int) {
	channelCapacityLostOwners.Lock()
	if remaining := channelCapacityLostOwners.counts[channelID] - 1; remaining > 0 {
		channelCapacityLostOwners.counts[channelID] = remaining
	} else {
		delete(channelCapacityLostOwners.counts, channelID)
	}
	channelCapacityLostOwners.Unlock()
}

func hasLostChannelCapacityOwner(channelID int) bool {
	channelCapacityLostOwners.Lock()
	defer channelCapacityLostOwners.Unlock()
	return channelCapacityLostOwners.counts[channelID] > 0
}

// ChannelCapacityLease owns one admitted upstream attempt. The caller must
// release it after the handler/connection has closed, even on panic. Renewal
// continues until cleanup: native submission streams deliberately outlive the
// client. Unlimited channels are counted without a cap: they own a local count
// plus a best-effort shared owner entry, and their telemetry loss is recorded
// instead of canceling the traffic.
type ChannelCapacityLease struct {
	ctx          context.Context
	control      context.Context
	cancel       context.CancelCauseFunc
	once         sync.Once
	channelID    int
	client       *redis.Client
	key          string
	owner        string
	done         chan struct{}
	leaseExpiry  atomic.Int64
	probeExpiry  atomic.Int64
	renewalRetry atomic.Bool
	// unlimited leases count without rejecting. A telemetry failure may never
	// cancel their traffic, so their context carries a typed-nil lease and a
	// separate retention handle.
	unlimited bool
	// handleMu guards the retention handle count. The owning attempt holds one;
	// each detached continuation that asks for cancellation support holds one
	// more, so draining the attempt cannot drop work that is still running.
	handleMu sync.Mutex
	handles  int
	drained  bool
	// telemetryMu guards the owner-entry state shared by maintenance and
	// release. Release stops maintenance before reading it, so the state is
	// stable when release acts on it.
	telemetryMu         sync.Mutex
	telemetryRegistered bool
	telemetryMarker     bool
	telemetryLost       bool
	markerKey           string
	waitCoordinator     *channelWaitCoordinator
	waitProbeOwner      string
	waitProbeAvailable  atomic.Bool
}

type channelCapacityContextKey struct{}

// channelCapacityUnlimitedKey carries the unlimited retention handle across a
// deliberate context.WithoutCancel. It deliberately does not carry a
// cancellable lease: unlimited admission has no coordination loss to propagate,
// and a stale detached context must never rebind a new attempt's lease.
type channelCapacityUnlimitedKey struct{}

func newChannelCapacityLease(parent context.Context, channelID int) *ChannelCapacityLease {
	control, cancelControl := context.WithCancelCause(channelCapacityDiagnosticContext(parent))
	ctx, cancelRequest := context.WithCancelCause(parent)
	lease := &ChannelCapacityLease{control: control, channelID: channelID, cancel: func(cause error) {
		cancelControl(cause)
		cancelRequest(cause)
	}}
	// Mask an inherited unlimited counting handle: a detached previous attempt's
	// context must not make this limited attempt retain that old owner.
	ctx = context.WithValue(ctx, channelCapacityUnlimitedKey{}, (*ChannelCapacityLease)(nil))
	lease.ctx = context.WithValue(ctx, channelCapacityContextKey{}, lease)
	return lease
}

// WithChannelCapacityCancellation restores lease-loss cancellation after a
// deliberate context.WithoutCancel. Client cancellation remains detached; the
// admitted provider work must still stop if its distributed slot is lost.
// An unlimited attempt has no slot to lose, but its detached continuation keeps
// the attempt's counting handle until both the attempt and that continuation
// finish, so a long detached stream is not reported as gone.
func WithChannelCapacityCancellation(ctx context.Context) (context.Context, context.CancelFunc) {
	lease, _ := ctx.Value(channelCapacityContextKey{}).(*ChannelCapacityLease)
	if lease == nil {
		if unlimited, _ := ctx.Value(channelCapacityUnlimitedKey{}).(*ChannelCapacityLease); unlimited != nil {
			return ctx, unlimited.retainCountingHandle()
		}
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

// retainCountingHandle holds the unlimited attempt's counting owner across a
// detached continuation. A drained or released owner is never revived, so a
// stale detached context from a previous attempt cannot resurrect it.
func (l *ChannelCapacityLease) retainCountingHandle() context.CancelFunc {
	l.handleMu.Lock()
	if l.drained || !l.unlimited {
		l.handleMu.Unlock()
		return func() {}
	}
	l.handles++
	l.handleMu.Unlock()
	var once sync.Once
	return func() { once.Do(l.releaseCountingHandle) }
}

// releaseCountingHandle drops one holder. The last holder performs cleanup, so
// the attempt's own Release after a detached continuation still counts it once.
func (l *ChannelCapacityLease) releaseCountingHandle() {
	l.handleMu.Lock()
	if l.drained {
		l.handleMu.Unlock()
		return
	}
	l.handles--
	last := l.handles <= 0
	if last {
		l.drained = true
	}
	l.handleMu.Unlock()
	if last {
		l.releaseUnlimited()
	}
}

func (l *ChannelCapacityLease) Release() {
	if l == nil || l.cancel == nil {
		return
	}
	if l.unlimited {
		// The owning attempt contributes exactly one holder, so repeated Release
		// calls cannot drain a count a detached continuation still holds.
		l.once.Do(l.releaseCountingHandle)
		return
	}
	l.once.Do(func() {
		defer l.releaseWaitProbe()
		l.cancel(context.Canceled)
		if l.client != nil {
			<-l.done
			ctx, cancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
			defer cancel()
			// Failure leaves only this owner's expiring lease, never a local
			// fallback counter or a deletion of a successor's reservation.
			_ = l.client.ZRem(ctx, l.key, l.owner).Err()
			_ = l.client.Publish(ctx, channelWaitNotifications, "release").Err()
			return
		}
		localChannelCapacity.Lock()
		localChannelCapacity.active[l.channelID]--
		if localChannelCapacity.active[l.channelID] == 0 {
			delete(localChannelCapacity.active, l.channelID)
		}
		localChannelCapacity.Unlock()
		localChannelWait.notify()
	})
}

func channelCapacityError(code types.ErrorCode) *types.NewAPIError {
	status, message := http.StatusTooManyRequests, i18n.MsgChannelCapacitySaturated
	if code == types.ErrorCodeChannelCapacityUnavailable {
		status, message = http.StatusServiceUnavailable, i18n.MsgChannelCapacityUnavailable
	} else if code == types.ErrorCodeChannelCapacityQueueFull {
		message = i18n.MsgChannelCapacityQueueFull
	} else if code == types.ErrorCodeChannelCapacityWaitTimeout {
		message = i18n.MsgChannelCapacityWaitTimeout
	}
	return types.NewErrorWithStatusCode(errors.New(i18n.Translate("", message)), code, status, types.ErrOptionWithSkipRetry())
}

func IsChannelCapacityError(err *types.NewAPIError) bool {
	return err != nil && (err.GetErrorCode() == types.ErrorCodeChannelCapacitySaturated || err.GetErrorCode() == types.ErrorCodeChannelCapacityUnavailable || err.GetErrorCode() == types.ErrorCodeChannelCapacityQueueFull || err.GetErrorCode() == types.ErrorCodeChannelCapacityWaitTimeout)
}

func AcquireChannelCapacity(ctx context.Context, channelID int, settings dto.ChannelSettings) (*ChannelCapacityLease, *types.NewAPIError) {
	if err := ctx.Err(); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusRequestTimeout, types.ErrOptionWithSkipRetry())
	}
	limit := 0
	if settings.MaxConcurrentRequests != nil && *settings.MaxConcurrentRequests > 0 {
		limit = *settings.MaxConcurrentRequests
	}
	if limit == 0 {
		return acquireUnlimitedChannelCapacity(ctx, channelID), nil
	}
	if !common.RedisEnabled {
		localChannelWait.mu.Lock()
		defer localChannelWait.mu.Unlock()
		if localChannelHasWaiters(channelID) {
			return nil, channelCapacityError(types.ErrorCodeChannelCapacitySaturated)
		}
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
		logChannelCapacityRedisError(ctx, channelID, channelCapacityAdmissionPhase, "coordinator_missing")
		return nil, channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
	}
	return acquireRedisChannelCapacity(ctx, common.RDB, channelID, limit)
}

// acquireUnlimitedChannelCapacity counts an attempt without ever rejecting it.
// Native submission retries can carry a detached previous attempt's context
// values, so the lease context keeps a typed-nil lease and cannot rebind that
// old attempt; counting never becomes an admission or cancellation signal.
func acquireUnlimitedChannelCapacity(ctx context.Context, channelID int) *ChannelCapacityLease {
	control, cancelControl := context.WithCancelCause(context.Background())
	lease := &ChannelCapacityLease{
		ctx:       context.WithValue(ctx, channelCapacityContextKey{}, (*ChannelCapacityLease)(nil)),
		control:   control,
		cancel:    cancelControl,
		channelID: channelID,
		unlimited: true,
		handles:   1,
		markerKey: channelCapacityMarkerKey(channelID),
	}
	lease.ctx = context.WithValue(lease.ctx, channelCapacityUnlimitedKey{}, lease)
	// The local owner lifecycle exists in both modes: it is the instance's
	// sample in memory mode and the last-resort signal when the shared count is
	// known to be missing this live attempt.
	localChannelCapacity.Lock()
	localChannelCapacity.active[channelID]++
	localChannelCapacity.Unlock()
	if !common.RedisEnabled {
		return lease
	}
	key := channelCapacityKey(channelID)
	owner := uuid.NewString()
	if common.RDB == nil {
		// A coordinator may appear while this attempt is still running. Until it
		// does, this live attempt is outside the shared count, so keep the local
		// lost-owner record until release: a later sample must report unavailable
		// rather than a false zero. The traffic itself is never rejected.
		lease.telemetryMu.Lock()
		lease.telemetryLost = true
		lease.telemetryMu.Unlock()
		addLostChannelCapacityOwner(channelID)
		return lease
	}
	commandCtx, cancel := context.WithTimeout(ctx, channelCapacityTelemetryTimeout)
	counted, err := channelCapacityCountAdmitScript.Run(commandCtx, common.RDB, []string{key}, owner, channelCapacityLeaseTTL.Milliseconds()).Int()
	cancel()
	if err == nil && counted == 1 {
		lease.client, lease.key, lease.owner = common.RDB, key, owner
		lease.done = make(chan struct{})
		lease.telemetryRegistered = true
		go lease.maintainUnlimited()
		return lease
	}
	if err == nil {
		// The write outcome is ambiguous; expiration bounds this token.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), channelCapacityTelemetryTimeout)
		_ = common.RDB.ZRem(cleanupCtx, key, owner).Err()
		cleanupCancel()
	}
	// A failed registration leaves a live owner outside the shared count. Publish
	// the shared marker with its own bounded context (the registration context
	// has usually expired by now) and record the owner locally as a last-resort
	// signal. The maintenance loop then retries registration immediately.
	lease.client, lease.key, lease.owner = common.RDB, key, owner
	lease.done = make(chan struct{})
	lease.telemetryLost = true
	addLostChannelCapacityOwner(channelID)
	if lease.publishTelemetryMarker() == nil {
		lease.telemetryMarker = true
	}
	go lease.maintainUnlimited()
	return lease
}

// publishTelemetryMarker records this owner in the shared incomplete set with
// an independent short context, so a registration timeout cannot also doom the
// marker write.
func (l *ChannelCapacityLease) publishTelemetryMarker() error {
	markerCtx, markerCancel := context.WithTimeout(context.Background(), channelCapacityMarkerTimeout)
	defer markerCancel()
	_, err := channelCapacityMarkerScript.Run(markerCtx, l.client, []string{l.markerKey}, l.owner, channelCapacityMarkerTTL.Milliseconds()).Int()
	return err
}

// releaseUnlimited stops the maintenance loop first, then reads the owner state
// it stabilized, then drains the local lifecycle and this owner's exact
// coordinator entries. The owner entry is removed unconditionally: an ambiguous
// write may have registered it even though this gateway never saw a success.
// Losing telemetry never resurrects a drained owner.
func (l *ChannelCapacityLease) releaseUnlimited() {
	l.cancel(context.Canceled)
	if l.done != nil {
		<-l.done
	}
	l.telemetryMu.Lock()
	lost := l.telemetryLost
	l.telemetryRegistered, l.telemetryMarker, l.telemetryLost = false, false, false
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), channelCapacityTelemetryTimeout)
	if l.client != nil {
		pipe := l.client.Pipeline()
		pipe.ZRem(releaseCtx, l.key, l.owner)
		pipe.ZRem(releaseCtx, l.markerKey, l.owner)
		_, _ = pipe.Exec(releaseCtx)
	}
	releaseCancel()
	l.telemetryMu.Unlock()
	if lost {
		removeLostChannelCapacityOwner(l.channelID)
	}
	localChannelCapacity.Lock()
	if remaining := localChannelCapacity.active[l.channelID] - 1; remaining > 0 {
		localChannelCapacity.active[l.channelID] = remaining
	} else {
		delete(localChannelCapacity.active, l.channelID)
	}
	localChannelCapacity.Unlock()
}

// maintainUnlimited keeps a live owner's shared entries fresh. A failed initial
// registration retries immediately; an already registered owner waits for its
// renewal deadline instead of writing twice at admission. A coordinator failure
// never cancels the traffic.
func (l *ChannelCapacityLease) maintainUnlimited() {
	defer close(l.done)
	l.telemetryMu.Lock()
	delay := time.Duration(0)
	if l.telemetryRegistered {
		delay = channelCapacityLeaseTTL / 3
	}
	l.telemetryMu.Unlock()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-l.control.Done():
			return
		case <-timer.C:
			healthy := l.refreshUnlimited()
			delay = channelCapacityTelemetryRetry
			if healthy {
				delay = channelCapacityLeaseTTL / 3
			}
			timer.Reset(delay)
		}
	}
}

// refreshUnlimited renews the owner entry, re-registers a live owner after a
// coordinator interruption, and keeps the incomplete marker aligned with it.
// Coordinator errors only publish the marker; they never cancel the traffic.
func (l *ChannelCapacityLease) refreshUnlimited() bool {
	ctx, cancel := context.WithTimeout(l.control, channelCapacityTelemetryTimeout)
	defer cancel()
	l.telemetryMu.Lock()
	registered, marked := l.telemetryRegistered, l.telemetryMarker
	l.telemetryMu.Unlock()

	if registered {
		renewed, err := channelCapacityRenewScript.Run(ctx, l.client, []string{l.key}, l.owner, channelCapacityLeaseTTL.Milliseconds()).Int()
		if err == nil && renewed == 1 {
			return l.reconcileTelemetry(marked)
		}
		// The entry is gone or unreachable: this owner is no longer counted.
		l.telemetryMu.Lock()
		l.telemetryRegistered = false
		l.telemetryMu.Unlock()
	}
	// Re-register the still-running owner. A released lease has already
	// stopped this loop, so a drained owner is never resurrected.
	counted, err := channelCapacityCountAdmitScript.Run(ctx, l.client, []string{l.key}, l.owner, channelCapacityLeaseTTL.Milliseconds()).Int()
	if err == nil && counted == 1 {
		l.telemetryMu.Lock()
		l.telemetryRegistered = true
		l.telemetryMu.Unlock()
		return l.reconcileTelemetry(marked)
	}
	// Both the renewal and the re-registration failed, so this live attempt is
	// outside the shared count. Record it locally as well as in the shared
	// marker: the local record still makes this gateway's sample unavailable
	// when the marker itself cannot be written.
	l.markTelemetryLost()
	if err := l.publishTelemetryMarker(); err != nil {
		return false
	}
	l.telemetryMu.Lock()
	l.telemetryMarker = true
	l.telemetryMu.Unlock()
	return false
}

// markTelemetryLost records once that a live owner is missing from the shared
// count, so the local sampler stops reporting a complete-looking number.
func (l *ChannelCapacityLease) markTelemetryLost() {
	l.telemetryMu.Lock()
	first := !l.telemetryLost
	l.telemetryLost = true
	l.telemetryMu.Unlock()
	if first {
		addLostChannelCapacityOwner(l.channelID)
	}
}

// reconcileTelemetry runs after the owner entry is live again: the shared
// marker and the local lost-owner record are cleared with independent bounded
// contexts, so an expired probe context cannot leave a false incomplete state.
func (l *ChannelCapacityLease) reconcileTelemetry(marked bool) bool {
	if marked {
		clearCtx, clearCancel := context.WithTimeout(context.Background(), channelCapacityMarkerTimeout)
		err := l.client.ZRem(clearCtx, l.markerKey, l.owner).Err()
		clearCancel()
		if err != nil {
			return false
		}
		l.telemetryMu.Lock()
		l.telemetryMarker = false
		l.telemetryMu.Unlock()
	}
	l.telemetryMu.Lock()
	lost := l.telemetryLost
	l.telemetryLost = false
	l.telemetryMu.Unlock()
	if lost {
		removeLostChannelCapacityOwner(l.channelID)
	}
	return true
}

func acquireRedisChannelCapacity(ctx context.Context, client *redis.Client, channelID, limit int) (*ChannelCapacityLease, *types.NewAPIError) {
	key := channelCapacityKey(channelID)
	owner := uuid.NewString()
	acquireStarted := time.Now()
	commandCtx, cancel := context.WithTimeout(ctx, channelCapacityAdmissionTimeout())
	admitted, err := channelCapacityAcquireScript.Run(commandCtx, client, []string{key, channelWaitQueueKey(channelID), channelWaitGlobalKey}, limit, owner, channelCapacityLeaseTTL.Milliseconds()).Int()
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			logChannelCapacityRedisError(ctx, channelID, channelCapacityAdmissionPhase, channelCapacityRedisErrorClass(err))
		}
		// The result of a timed-out write is ambiguous. Remove this token
		// only; expiration also bounds cleanup if Redis is still unavailable.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), channelCapacityAdmissionCleanupTimeout)
		_ = client.ZRem(cleanupCtx, key, owner).Err()
		cleanupCancel()
		return nil, channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
	}
	if admitted == 0 {
		return nil, channelCapacityError(types.ErrorCodeChannelCapacitySaturated)
	}
	lease := newChannelCapacityLease(ctx, channelID)
	lease.client, lease.key, lease.owner, lease.done = client, key, owner, make(chan struct{})
	lease.leaseExpiry.Store(acquireStarted.Add(channelCapacityLeaseTTL).UnixNano())
	go lease.maintain()
	return lease, nil
}

func (l *ChannelCapacityLease) renewalDeadline() time.Time {
	deadline := l.leaseExpiry.Load()
	if l.waitProbeOwner != "" && !l.waitProbeAvailable.Load() {
		deadline = min(deadline, l.probeExpiry.Load())
	}
	return time.Unix(0, deadline)
}

// Cancel upstream ownership before logging: observability must not delay the
// fail-closed signal when an owner expires or the coordinator is unavailable.
func (l *ChannelCapacityLease) failCoordination(phase channelCapacityPhase, errorClass string) bool {
	if l.control.Err() != nil {
		return false
	}
	diagnosticCtx := channelCapacityDiagnosticContext(l.control)
	l.cancel(channelCapacityError(types.ErrorCodeChannelCapacityUnavailable))
	logChannelCapacityRedisError(diagnosticCtx, l.channelID, phase, errorClass)
	return false
}

// An ambiguous renewal does not extend the confirmed ownership deadline. A
// transient coordinator failure can be retried only inside that deadline.
func (l *ChannelCapacityLease) retryRenewal(phase channelCapacityPhase, err error) bool {
	if l.control.Err() != nil {
		return false
	}
	errorClass := channelCapacityRedisErrorClass(err)
	if errors.Is(err, redis.ErrClosed) || channelCapacityRenewalTimeout(l.renewalDeadline(), channelCapacityAdmissionTimeout()) <= 0 {
		return l.failCoordination(phase, errorClass)
	}
	logChannelCapacityRedisError(l.control, l.channelID, phase, errorClass)
	if channelCapacityRenewalTimeout(l.renewalDeadline(), channelCapacityAdmissionTimeout()) <= 0 {
		return l.failCoordination(phase, "ownership_deadline")
	}
	l.renewalRetry.Store(true)
	return true
}

func (l *ChannelCapacityLease) renew() bool {
	timeout := channelCapacityAdmissionTimeout()
	commandTimeout := channelCapacityRenewalTimeout(l.renewalDeadline(), timeout)
	if commandTimeout <= 0 {
		phase := channelCapacityRenewalPhase
		if l.waitProbeOwner != "" && !l.waitProbeAvailable.Load() && l.probeExpiry.Load() <= l.leaseExpiry.Load() {
			phase = channelCapacityProbeRenewalPhase
		}
		return l.failCoordination(phase, "ownership_deadline")
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(l.control, commandTimeout)
	renewed, err := channelCapacityRenewScript.Run(ctx, l.client, []string{l.key}, l.owner, channelCapacityLeaseTTL.Milliseconds()).Int()
	cancel()
	if err != nil {
		return l.retryRenewal(channelCapacityRenewalPhase, err)
	}
	if renewed != 1 {
		return l.failCoordination(channelCapacityRenewalPhase, "owner_missing_or_expired")
	}
	if channelCapacityRenewalTimeout(l.renewalDeadline(), timeout) <= 0 {
		return l.failCoordination(channelCapacityRenewalPhase, "ownership_deadline")
	}
	l.leaseExpiry.Store(started.Add(channelCapacityLeaseTTL).UnixNano())
	if l.waitProbeOwner != "" && !l.waitProbeAvailable.Load() {
		probeTimeout := channelCapacityRenewalTimeout(l.renewalDeadline(), timeout)
		if probeTimeout <= 0 {
			return l.failCoordination(channelCapacityProbeRenewalPhase, "ownership_deadline")
		}
		probeStarted := time.Now()
		probeCtx, probeCancel := context.WithTimeout(l.control, probeTimeout)
		probeRenewed, probeErr := channelWaitProbeRenewScript.Run(probeCtx, l.client, []string{channelWaitBusyKey(l.channelID)}, l.waitProbeOwner, channelCapacityLeaseTTL.Milliseconds()).Int()
		probeCancel()
		if probeErr != nil && !l.waitProbeAvailable.Load() {
			return l.retryRenewal(channelCapacityProbeRenewalPhase, probeErr)
		}
		if !l.waitProbeAvailable.Load() {
			if probeRenewed != 1 {
				return l.failCoordination(channelCapacityProbeRenewalPhase, "owner_missing_or_expired")
			}
			if channelCapacityRenewalTimeout(l.renewalDeadline(), timeout) <= 0 {
				return l.failCoordination(channelCapacityProbeRenewalPhase, "ownership_deadline")
			}
			l.probeExpiry.Store(probeStarted.Add(channelCapacityLeaseTTL).UnixNano())
		}
	}
	l.renewalRetry.Store(false)
	return true
}

func (l *ChannelCapacityLease) maintain() {
	defer close(l.done)
	delay := channelCapacityRenewalTimeout(l.renewalDeadline(), channelCapacityLeaseTTL/3)
	timer := time.NewTimer(max(0, delay))
	defer timer.Stop()
	for {
		select {
		case <-l.control.Done():
			return
		case <-timer.C:
			if !l.renew() {
				return
			}
			delay = channelCapacityLeaseTTL / 3
			if l.renewalRetry.Swap(false) {
				delay = time.Second
			}
			// Never sleep beyond the last confirmed lease/probe deadline. The
			// next renewal fails closed before another gateway can reuse it.
			delay = channelCapacityRenewalTimeout(l.renewalDeadline(), delay)
			timer.Reset(max(0, delay))
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
		} else if err.GetErrorCode() == types.ErrorCodeChannelCapacityQueueFull {
			key = i18n.MsgChannelCapacityQueueFull
		} else if err.GetErrorCode() == types.ErrorCodeChannelCapacityWaitTimeout {
			key = i18n.MsgChannelCapacityWaitTimeout
		}
		err.SetMessage(i18n.T(c, key))
	}
	return lease, err
}

// RetryChannelAdmission excludes a capacity-rejected candidate without recording an
// upstream failure or consuming the upstream retry budget. Pins and strict
// sessions keep their routing boundary even when no upstream call was made.
func RetryChannelAdmission(c *gin.Context, channelID int, err *types.NewAPIError, retry *RetryParam) bool {
	if !IsChannelCapacityError(err) {
		return false
	}
	state := RequestPolicy(c)
	decision := PolicyDecision{Action: "stop", Reason: string(err.GetErrorCode()), Source: "capacity"}
	_, pinned, _ := GetChannelConstraints(c).ResolvedPin()
	if channelID > 0 && c.Request != nil && c.Request.Context().Err() == nil && !pinned && !ShouldSkipRetryAfterChannelAffinityFailure(c) && state.SessionMode != "strict" && !c.Writer.Written() {
		rejected := state.CapacityRejectedChannelIDs
		if !slices.Contains(rejected, channelID) {
			state.CapacityRejectedChannelIDs = append(rejected, channelID)
			if err.GetErrorCode() == types.ErrorCodeChannelCapacitySaturated && !slices.Contains(state.SaturatedChannelIDs, channelID) {
				state.SaturatedChannelIDs = append(state.SaturatedChannelIDs, channelID)
			}
			// Auto selection may have prepared the NEXT retry's group before
			// admission (including the initial middleware selection). A rejected
			// candidate did not use that retry: exhaust its actual group first.
			retry.restoreUnusedAutoGroupAttempt(c, channelID, state.selectedAutoGroupRetry)
			decision.Action = "select"
		}
	}
	state.AddEvent(PolicyEvent{ChannelID: channelID, Status: err.StatusCode, ErrorCode: string(err.GetErrorCode()), ErrorSource: "local", Decision: decision, Health: "unchanged"})
	return decision.Action == "select"
}
