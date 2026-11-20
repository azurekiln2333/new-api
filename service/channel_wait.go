package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	channelWaitGlobalKey        = "new-api:channel-wait:active"
	channelWaitSequenceKey      = "new-api:channel-wait:sequence"
	channelWaitNotifications    = "new-api:channel-wait:wake"
	channelWaitRecoveryInterval = time.Second
)

// ChannelWaitCandidate contains a policy-approved candidate snapshot. A grant is
// an admission reservation, not authorization: the caller must revalidate its
// current group/key/model/pin policy before using the returned lease.
type ChannelWaitCandidate struct {
	ChannelID int
	Settings  dto.ChannelSettings
}

type ChannelWaitGrant struct {
	ChannelID int
	Lease     *ChannelCapacityLease
}

// ChannelWaiter belongs to one relay request. Repeated upstream busy responses
// reuse it, so they cannot reset the original waiting deadline. It never reads
// or writes billing state, response bytes, or channel selection policy.
type ChannelWaiter struct {
	ctx         context.Context
	cancel      context.CancelFunc
	userID      int
	mu          sync.Mutex
	deadline    time.Time
	duration    time.Duration
	maxTotal    int
	maxUser     int
	coordinator *channelWaitCoordinator
}

type channelWaitResult struct {
	grant *ChannelWaitGrant
	err   *types.NewAPIError
}

type channelWaitEntry struct {
	id         string
	userID     int
	ctx        context.Context
	leaseCtx   context.Context
	deadline   time.Time
	candidates []ChannelWaitCandidate
	result     chan channelWaitResult
}

type channelUpstreamGate struct {
	until    time.Time
	expires  time.Time
	attempts int
	probe    string
}

// A coordinator has one notification listener and recovery clock, irrespective
// of queue length. Waiters park on their own result channel, never a Redis
// blocking command or a periodic Redis query in each request goroutine.
type channelWaitCoordinator struct {
	mu      sync.Mutex
	client  *redis.Client
	entries []*channelWaitEntry
	busy    map[int]*channelUpstreamGate
	wake    chan struct{}
	running bool
}

func newChannelWaitCoordinator(client *redis.Client) *channelWaitCoordinator {
	return &channelWaitCoordinator{client: client, busy: make(map[int]*channelUpstreamGate), wake: make(chan struct{}, 1)}
}

var localChannelWait = newChannelWaitCoordinator(nil)
var redisChannelWait = struct {
	sync.Mutex
	coordinator *channelWaitCoordinator
}{}

func currentChannelWaitCoordinator() *channelWaitCoordinator {
	if !common.RedisEnabled {
		return localChannelWait
	}
	redisChannelWait.Lock()
	defer redisChannelWait.Unlock()
	if redisChannelWait.coordinator == nil || redisChannelWait.coordinator.client != common.RDB {
		redisChannelWait.coordinator = newChannelWaitCoordinator(common.RDB)
	}
	return redisChannelWait.coordinator
}

func channelWaitQueueKey(channelID int) string {
	return fmt.Sprintf("new-api:channel-wait:channel:%d", channelID)
}
func channelWaitBusyKey(channelID int) string {
	return fmt.Sprintf("new-api:channel-wait:busy:%d", channelID)
}
func channelWaitUserKey(userID int) string {
	return fmt.Sprintf("new-api:channel-wait:user:%d", userID)
}

func NewChannelWaiter(ctx context.Context, userID int) *ChannelWaiter {
	ctx, cancel := context.WithCancel(ctx)
	return &ChannelWaiter{
		ctx: ctx, cancel: cancel, userID: userID,
		duration:    time.Duration(max(1, min(3600, common.GetEnvOrDefault("CHANNEL_WAIT_TIMEOUT_SECONDS", 300)))) * time.Second,
		maxTotal:    max(1, min(10000, common.GetEnvOrDefault("CHANNEL_WAIT_MAX_REQUESTS", 1000))),
		maxUser:     max(1, min(1000, common.GetEnvOrDefault("CHANNEL_WAIT_MAX_PER_USER", 20))),
		coordinator: currentChannelWaitCoordinator(),
	}
}

func (w *ChannelWaiter) Close() {
	if w != nil {
		w.cancel()
	}
}

// Wait registers before rechecking capacity, closing the release-before-park
// window. Calls on the same waiter must be sequential. The lease is already
// counted and must be released even if policy revalidation rejects the grant.
func (w *ChannelWaiter) Wait(ctx context.Context, candidates []ChannelWaitCandidate) (*ChannelWaitGrant, *types.NewAPIError) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.userID <= 0 {
		return nil, types.NewErrorWithStatusCode(errors.New("authenticated user required for channel waiting"), types.ErrorCodeInvalidRequest, http.StatusUnauthorized, types.ErrOptionWithSkipRetry())
	}
	if w.deadline.IsZero() {
		w.deadline = time.Now().Add(w.duration)
	}
	waitCtx, cancel := context.WithDeadline(ctx, w.deadline)
	stop := context.AfterFunc(w.ctx, cancel)
	defer func() { stop(); cancel() }()
	if w.ctx.Err() != nil {
		cancel()
	}
	if waitCtx.Err() != nil {
		return nil, channelWaitContextError(waitCtx)
	}
	if len(candidates) == 0 || len(candidates) > 256 {
		return nil, types.NewErrorWithStatusCode(errors.New("no eligible channel to wait for"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	seen := make(map[int]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate.ChannelID <= 0 || seen[candidate.ChannelID] {
			return nil, types.NewErrorWithStatusCode(errors.New("invalid channel wait candidates"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		seen[candidate.ChannelID] = true
	}
	// The request may end earlier than the queue's maximum duration. Redis
	// expiry must use that effective deadline so a crashed gateway cannot leave
	// its already-expired request blocking peers until the longer queue limit.
	deadline, _ := waitCtx.Deadline()
	entry := &channelWaitEntry{id: uuid.NewString(), userID: w.userID, ctx: waitCtx, leaseCtx: ctx, deadline: deadline, candidates: slices.Clone(candidates), result: make(chan channelWaitResult, 1)}
	if apiErr := w.coordinator.register(entry, w.maxTotal, w.maxUser); apiErr != nil {
		return nil, apiErr
	}
	defer w.coordinator.remove(entry)
	select {
	case result := <-entry.result:
		if waitCtx.Err() != nil {
			if result.grant != nil {
				result.grant.Lease.Release()
			}
			return nil, channelWaitContextError(waitCtx)
		}
		return result.grant, result.err
	case <-waitCtx.Done():
		// Removal synchronizes with dispatch. A grant that raced cancellation
		// is drained and returned; no slot or upstream probe is leaked.
		w.coordinator.remove(entry)
		select {
		case result := <-entry.result:
			if result.grant != nil {
				result.grant.Lease.Release()
			}
		default:
		}
		return nil, channelWaitContextError(waitCtx)
	}
}

func channelWaitContextError(ctx context.Context) *types.NewAPIError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return channelCapacityError(types.ErrorCodeChannelCapacityWaitTimeout)
	}
	return types.NewErrorWithStatusCode(ctx.Err(), types.ErrorCodeDoRequestFailed, http.StatusRequestTimeout, types.ErrOptionWithSkipRetry())
}

func (c *channelWaitCoordinator) notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *channelWaitCoordinator) register(entry *channelWaitEntry, maxTotal, maxUser int) *types.NewAPIError {
	if entry.ctx.Err() != nil {
		return channelWaitContextError(entry.ctx)
	}
	if common.RedisEnabled && c.client == nil {
		return channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
	}
	if c.client != nil {
		keys := []string{channelWaitGlobalKey, channelWaitUserKey(entry.userID), channelWaitSequenceKey}
		for _, candidate := range entry.candidates {
			keys = append(keys, channelWaitQueueKey(candidate.ChannelID))
		}
		ctx, cancel := context.WithTimeout(entry.ctx, channelCapacityRedisTimeout)
		registered, err := channelWaitRegisterScript.Run(ctx, c.client, keys, entry.id, time.Until(entry.deadline).Milliseconds(), maxTotal, maxUser).Int()
		cancel()
		if err != nil {
			c.removeRedis(entry)
			return channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
		}
		if registered == 0 {
			return channelCapacityError(types.ErrorCodeChannelCapacityQueueFull)
		}
	}
	c.mu.Lock()
	if entry.ctx.Err() != nil {
		c.mu.Unlock()
		if c.client != nil {
			c.removeRedis(entry)
		}
		return channelWaitContextError(entry.ctx)
	}
	defer c.mu.Unlock()
	if c.client == nil {
		userCount := 0
		for _, pending := range c.entries {
			if pending.userID == entry.userID {
				userCount++
			}
		}
		if len(c.entries) >= maxTotal || userCount >= maxUser {
			return channelCapacityError(types.ErrorCodeChannelCapacityQueueFull)
		}
	}
	c.entries = append(c.entries, entry)
	if !c.running {
		c.running = true
		go c.run()
	}
	c.notify()
	return nil
}

func (c *channelWaitCoordinator) remove(entry *channelWaitEntry) {
	c.mu.Lock()
	index := slices.Index(c.entries, entry)
	if index == -1 {
		c.mu.Unlock()
		return
	}
	c.entries = slices.Delete(c.entries, index, index+1)
	c.mu.Unlock()
	if c.client != nil {
		c.removeRedis(entry)
	}
	c.notify()
}

func (c *channelWaitCoordinator) removeRedis(entry *channelWaitEntry) {
	keys := []string{channelWaitGlobalKey, channelWaitUserKey(entry.userID)}
	for _, candidate := range entry.candidates {
		keys = append(keys, channelWaitQueueKey(candidate.ChannelID))
	}
	ctx, cancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
	defer cancel()
	// On coordinator failure the server-side expiry remains authoritative.
	_, _ = channelWaitRemoveScript.Run(ctx, c.client, keys, entry.id, channelWaitNotifications).Result()
}

func (c *channelWaitCoordinator) run() {
	var notifications <-chan *redis.Message
	if c.client != nil {
		subscription := c.client.Subscribe(context.Background(), channelWaitNotifications)
		defer subscription.Close()
		notifications = subscription.Channel()
	}
	ticker := time.NewTicker(channelWaitRecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.wake:
		case <-ticker.C:
		case <-notifications:
		}
		c.mu.Lock()
		c.dispatchLocked(time.Now())
		if len(c.entries) == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}

func (c *channelWaitCoordinator) dispatchLocked(now time.Time) {
	if c.client != nil {
		c.dispatchRedisLocked()
		return
	}
	blocked := make(map[int]bool)
	for i := 0; i < len(c.entries); {
		entry := c.entries[i]
		var grant *ChannelWaitGrant
		var apiErr *types.NewAPIError
		if entry.ctx.Err() != nil {
			apiErr = channelWaitContextError(entry.ctx)
		} else {
			for _, candidate := range entry.candidates {
				if blocked[candidate.ChannelID] {
					continue
				}
				grant = c.tryLocal(entry, candidate, now)
				if grant != nil {
					break
				}
			}
		}
		if grant == nil && apiErr == nil {
			for _, candidate := range entry.candidates {
				blocked[candidate.ChannelID] = true
			}
			i++
			continue
		}
		c.entries = slices.Delete(c.entries, i, i+1)
		entry.result <- channelWaitResult{grant: grant, err: apiErr}
	}
}

func (c *channelWaitCoordinator) tryLocal(entry *channelWaitEntry, candidate ChannelWaitCandidate, now time.Time) *ChannelWaitGrant {
	gate := c.busy[candidate.ChannelID]
	if gate != nil && gate.probe == "" && !now.Before(gate.expires) {
		delete(c.busy, candidate.ChannelID)
		gate = nil
	}
	if gate != nil && (gate.probe != "" || now.Before(gate.until)) {
		return nil
	}
	limit := 0
	if candidate.Settings.MaxConcurrentRequests != nil {
		limit = *candidate.Settings.MaxConcurrentRequests
	}
	localChannelCapacity.Lock()
	if limit > 0 && localChannelCapacity.active[candidate.ChannelID] >= limit {
		localChannelCapacity.Unlock()
		return nil
	}
	localChannelCapacity.active[candidate.ChannelID]++
	localChannelCapacity.Unlock()
	lease := newChannelCapacityLease(entry.leaseCtx, candidate.ChannelID)
	lease.waitCoordinator = c
	if gate != nil {
		gate.probe = entry.id
		lease.waitProbeOwner = entry.id
	}
	return &ChannelWaitGrant{ChannelID: candidate.ChannelID, Lease: lease}
}

// ReportChannelUpstreamBusy must only follow a positively classified upstream
// concurrency rejection. Generic HTTP 429, quota, transport errors and arbitrary
// response prose are not queue triggers. Retry-After is bounded to one minute.
func ReportChannelUpstreamBusy(ctx context.Context, channelID int, retryAfter time.Duration) *types.NewAPIError {
	c := currentChannelWaitCoordinator()
	if common.RedisEnabled && c.client == nil {
		return channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
	}
	retryAfter = max(0, min(time.Minute, retryAfter))
	jitter := time.Duration(rand.Int64N(250)) * time.Millisecond
	if c.client != nil {
		commandCtx, cancel := context.WithTimeout(ctx, channelCapacityRedisTimeout)
		defer cancel()
		_, err := channelWaitBusyScript.Run(commandCtx, c.client, []string{channelWaitBusyKey(channelID)}, retryAfter.Milliseconds(), jitter.Milliseconds()).Result()
		if err != nil {
			return channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
		}
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	gate := c.busy[channelID]
	if gate == nil || !now.Before(gate.expires) {
		gate = &channelUpstreamGate{}
		c.busy[channelID] = gate
	}
	gate.attempts = min(6, gate.attempts+1)
	delay := min(30*time.Second, time.Second<<(gate.attempts-1)) + jitter
	gate.until = now.Add(max(delay, retryAfter))
	gate.expires = now.Add(10 * time.Minute)
	return nil
}

// MarkUpstreamAvailable clears backoff only for this queue-owned probe. An old
// successful request cannot clear a newer request's busy/probe ownership.
func (l *ChannelCapacityLease) MarkUpstreamAvailable() {
	if l == nil || l.waitCoordinator == nil || l.waitProbeOwner == "" {
		return
	}
	l.waitProbeAvailable.Store(true)
	c := l.waitCoordinator
	if c.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
		defer cancel()
		_, _ = channelWaitProbeScript.Run(ctx, c.client, []string{channelWaitBusyKey(l.channelID)}, l.waitProbeOwner, "success", channelWaitNotifications).Result()
		return
	}
	c.mu.Lock()
	if gate := c.busy[l.channelID]; gate != nil && gate.probe == l.waitProbeOwner {
		delete(c.busy, l.channelID)
	}
	c.mu.Unlock()
}

func (l *ChannelCapacityLease) releaseWaitProbe() {
	if l.waitCoordinator == nil || l.waitProbeOwner == "" {
		return
	}
	c := l.waitCoordinator
	if c.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
		defer cancel()
		_, _ = channelWaitProbeScript.Run(ctx, c.client, []string{channelWaitBusyKey(l.channelID)}, l.waitProbeOwner, "release", channelWaitNotifications).Result()
	} else {
		c.mu.Lock()
		if gate := c.busy[l.channelID]; gate != nil && gate.probe == l.waitProbeOwner {
			gate.probe = ""
			// Cancellation or an unclassified failure is not evidence of
			// recovery. Avoid an immediate unbounded chain of probes.
			if floor := time.Now().Add(time.Second); gate.until.Before(floor) {
				gate.until = floor
			}
		}
		c.mu.Unlock()
		c.notify()
	}
}

func (c *channelWaitCoordinator) dispatchRedisLocked() {
	// One pipeline per coordinator sweep, not one round trip per waiter. The
	// individual Lua commands remain atomic across independent gateways.
	ctx, cancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
	defer cancel()
	pipe := c.client.Pipeline()
	commands := make([]*redis.Cmd, len(c.entries))
	owners := make([]string, len(c.entries))
	for i, entry := range c.entries {
		if entry.ctx.Err() != nil {
			continue
		}
		keys := []string{channelWaitGlobalKey, channelWaitUserKey(entry.userID)}
		owners[i] = uuid.NewString()
		args := []any{entry.id, owners[i], channelCapacityLeaseTTL.Milliseconds(), channelWaitNotifications}
		for _, candidate := range entry.candidates {
			keys = append(keys, channelWaitQueueKey(candidate.ChannelID), channelCapacityKey(candidate.ChannelID), channelWaitBusyKey(candidate.ChannelID))
			limit := 0
			if candidate.Settings.MaxConcurrentRequests != nil {
				limit = *candidate.Settings.MaxConcurrentRequests
			}
			args = append(args, limit)
		}
		// EVAL avoids a NOSCRIPT cache miss after a Redis restart leaving a
		// whole pipeline unexecuted; no per-waiter recovery round trips.
		commands[i] = pipe.Eval(ctx, channelWaitAcquireLua, keys, args...)
	}
	_, _ = pipe.Exec(ctx)
	remaining := make([]*channelWaitEntry, 0, len(c.entries))
	cleanup := c.client.Pipeline()
	for i, entry := range c.entries {
		var grant *ChannelWaitGrant
		var apiErr *types.NewAPIError
		var result []int64
		var err error
		if commands[i] != nil {
			result, err = commands[i].Int64Slice()
		}
		switch {
		case commands[i] == nil:
			apiErr = channelWaitContextError(entry.ctx)
		case err != nil || len(result) != 2:
			apiErr = channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
		case result[0] == -1:
			apiErr = channelCapacityError(types.ErrorCodeChannelCapacityWaitTimeout)
		case result[0] == 0 && entry.ctx.Err() == nil:
			remaining = append(remaining, entry)
			continue
		case result[0] > 0 && result[0] <= int64(len(entry.candidates)):
			channelID := entry.candidates[int(result[0])-1].ChannelID
			lease := newChannelCapacityLease(entry.leaseCtx, channelID)
			lease.client, lease.key, lease.owner, lease.done = c.client, channelCapacityKey(channelID), owners[i], make(chan struct{})
			lease.waitCoordinator = c
			if result[1] == 1 {
				lease.waitProbeOwner = lease.owner
			}
			go lease.maintain()
			grant = &ChannelWaitGrant{ChannelID: channelID, Lease: lease}
		default:
			if entry.ctx.Err() != nil {
				apiErr = channelWaitContextError(entry.ctx)
			} else {
				apiErr = channelCapacityError(types.ErrorCodeChannelCapacityUnavailable)
			}
		}
		if grant == nil {
			keys := []string{channelWaitGlobalKey, channelWaitUserKey(entry.userID)}
			for _, candidate := range entry.candidates {
				keys = append(keys, channelWaitQueueKey(candidate.ChannelID))
				if owners[i] != "" {
					// Ambiguous pipeline writes may have granted a lease. Remove
					// only this generated owner, including its probe handle.
					cleanup.ZRem(context.Background(), channelCapacityKey(candidate.ChannelID), owners[i])
					cleanup.Eval(context.Background(), channelWaitProbeLua, []string{channelWaitBusyKey(candidate.ChannelID)}, owners[i], "release", channelWaitNotifications)
				}
			}
			cleanup.Eval(context.Background(), channelWaitRemoveLua, keys, entry.id, channelWaitNotifications)
		}
		entry.result <- channelWaitResult{grant: grant, err: apiErr}
	}
	c.entries = remaining
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), channelCapacityRedisTimeout)
	defer cleanupCancel()
	_, _ = cleanup.Exec(cleanupCtx)
}

// Called with localChannelWait.mu held, before the ordinary local admission
// counter lock. A newcomer cannot consume a slot promised to existing waiters.
func localChannelHasWaiters(channelID int) bool {
	for _, entry := range localChannelWait.entries {
		if entry.ctx.Err() == nil && slices.ContainsFunc(entry.candidates, func(candidate ChannelWaitCandidate) bool { return candidate.ChannelID == channelID }) {
			return true
		}
	}
	return false
}

var channelWaitRegisterScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1]*1000 + math.floor(clock[2]/1000)
local ttl = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
if ttl <= 0 or redis.call('ZCARD',KEYS[1]) >= tonumber(ARGV[3]) or redis.call('ZCARD',KEYS[2]) >= tonumber(ARGV[4]) then return 0 end
local seq = redis.call('INCR', KEYS[3])
redis.call('ZADD', KEYS[1], now+ttl, ARGV[1])
redis.call('ZADD', KEYS[2], now+ttl, ARGV[1])
local function retain(key)
  if redis.call('PTTL',key)<ttl+60000 then redis.call('PEXPIRE',key,ttl+60000) end
end
for i=4,#KEYS do redis.call('ZADD',KEYS[i],seq,ARGV[1]); retain(KEYS[i]) end
retain(KEYS[1]); retain(KEYS[2])
return 1
`)

const channelWaitRemoveLua = `
for i=1,#KEYS do redis.call('ZREM',KEYS[i],ARGV[1]) end
redis.call('PUBLISH',ARGV[2],'release')
return 1
`

var channelWaitRemoveScript = redis.NewScript(channelWaitRemoveLua)

const channelWaitAcquireLua = `
local clock = redis.call('TIME')
local now = clock[1]*1000 + math.floor(clock[2]/1000)
local expiry = redis.call('ZSCORE',KEYS[1],ARGV[1])
if not expiry or tonumber(expiry)<=now then return {-1,0} end
for i=3,#KEYS,3 do
  local head = redis.call('ZRANGE',KEYS[i],0,0)[1]
  while head do
    local alive = redis.call('ZSCORE',KEYS[1],head)
    if alive and tonumber(alive)>now then break end
    redis.call('ZREM',KEYS[i],head)
    head = redis.call('ZRANGE',KEYS[i],0,0)[1]
  end
  if head == ARGV[1] then
    local gate = redis.call('HMGET',KEYS[i+2],'until','probe_until')
    local busyUntil = tonumber(gate[1]) or 0
    local probeUntil = tonumber(gate[2]) or 0
    redis.call('ZREMRANGEBYSCORE',KEYS[i+1],'-inf',now)
    local index = (i-3)/3+1
    local limit = tonumber(ARGV[index+4])
    if now>=busyUntil and now>=probeUntil and (limit<=0 or redis.call('ZCARD',KEYS[i+1])<limit) then
      redis.call('ZADD',KEYS[i+1],now+tonumber(ARGV[3]),ARGV[2])
      redis.call('PEXPIRE',KEYS[i+1],ARGV[3])
      local probe = 0
      if gate[1] then
        redis.call('HSET',KEYS[i+2],'probe',ARGV[2],'probe_until',now+tonumber(ARGV[3]))
        redis.call('PEXPIRE',KEYS[i+2],600000)
        probe=1
      end
      redis.call('ZREM',KEYS[1],ARGV[1]); redis.call('ZREM',KEYS[2],ARGV[1])
      for j=3,#KEYS,3 do redis.call('ZREM',KEYS[j],ARGV[1]) end
      redis.call('PUBLISH',ARGV[4],'grant')
      return {index,probe}
    end
  end
end
return {0,0}
`

var channelWaitBusyScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = clock[1]*1000 + math.floor(clock[2]/1000)
local attempts = math.min(6,redis.call('HINCRBY',KEYS[1],'attempts',1))
local delay = math.max(tonumber(ARGV[1]), math.min(30000,1000*2^(attempts-1))+tonumber(ARGV[2]))
redis.call('HSET',KEYS[1],'until',now+delay)
redis.call('PEXPIRE',KEYS[1],600000)
return 1
`)

const channelWaitProbeLua = `
if redis.call('HGET',KEYS[1],'probe') ~= ARGV[1] then return 0 end
if ARGV[2]=='success' then redis.call('DEL',KEYS[1]) else
  local clock = redis.call('TIME')
  local now = clock[1]*1000 + math.floor(clock[2]/1000)
  local untilAt = tonumber(redis.call('HGET',KEYS[1],'until')) or 0
  redis.call('HSET',KEYS[1],'until',math.max(untilAt,now+1000))
  redis.call('HDEL',KEYS[1],'probe','probe_until')
end
redis.call('PUBLISH',ARGV[3],'probe')
return 1
`

var channelWaitProbeScript = redis.NewScript(channelWaitProbeLua)

var channelWaitProbeRenewScript = redis.NewScript(`
if redis.call('HGET',KEYS[1],'probe') ~= ARGV[1] then return 0 end
local clock = redis.call('TIME')
local now = clock[1]*1000 + math.floor(clock[2]/1000)
redis.call('HSET',KEYS[1],'probe_until',now+tonumber(ARGV[2]))
redis.call('PEXPIRE',KEYS[1],600000)
return 1
`)

// ParseChannelRetryAfter accepts the HTTP delta-seconds and date forms. It is a
// scheduling hint only; even a huge provider value cannot extend the request's
// fixed waiting deadline or delay one probe by more than one minute.
func ParseChannelRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Duration(max(0, min(60, seconds))) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, min(time.Minute, at.Sub(now)))
	}
	return 0
}
