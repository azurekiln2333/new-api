package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/lmfp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testChallenges = []lmfp.Challenge{
	{ID: "one", Prompt: "first", ExpectedCount: 100},
	{ID: "two", Prompt: "second", ExpectedCount: 100},
	{ID: "three", Prompt: "third", ExpectedCount: 100},
}

// newTestFingerprintJob builds the job state runFingerprintJob operates on, so
// the budget and cancellation invariants are testable without a channel.
func newTestFingerprintJob(challenges []lmfp.Challenge) *FingerprintJob {
	job := &FingerprintJob{
		ID:            "test-job",
		Model:         "test-model",
		State:         "running",
		Probes:        make([]FingerprintProbe, len(challenges)),
		RequestBudget: len(challenges),
		TokenBudget:   100,
	}
	for i, challenge := range challenges {
		job.Probes[i] = FingerprintProbe{
			ID:            challenge.ID,
			State:         "queued",
			ExpectedCount: challenge.ExpectedCount,
		}
	}
	return job
}

func TestFingerprintProbeLimitNeverExceedsRemainingBudget(t *testing.T) {
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 1000
	limits := []uint{}
	probe := func(_ context.Context, _ lmfp.Challenge, maxOutputTokens uint) (FingerprintProbeResult, error) {
		limits = append(limits, maxOutputTokens)
		return FingerprintProbeResult{PromptTokens: 100, OutputTokens: 100, Text: "1 2 3", ParsedCount: 3}, nil
	}

	runFingerprintJob(context.Background(), job, testChallenges, nil, probe, time.Second)

	// 1000 budget, 512 reserved per request: 488 then 288; after two probes the
	// remaining room is below the minimum useful size, so the third is not sent.
	require.Len(t, limits, 2)
	assert.Equal(t, uint(1000-fingerprintProbeInputReserve), limits[0])
	assert.Equal(t, uint(800-fingerprintProbeInputReserve), limits[1])
	assert.Equal(t, 400, job.TokensUsed)
	assert.Equal(t, "cancelled", job.Probes[2].State)
	assert.Contains(t, job.Error, "token budget")
}

func TestFingerprintRunRefusesBudgetTooSmallForARequest(t *testing.T) {
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 300
	calls := 0
	probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		calls++
		return FingerprintProbeResult{}, nil
	}

	runFingerprintJob(context.Background(), job, testChallenges, nil, probe, time.Second)

	// 300 tokens cannot cover the input reserve plus a useful reply, so nothing
	// is sent and the run explains why.
	assert.Equal(t, 0, calls)
	assert.Equal(t, 0, job.RequestsUsed)
	assert.Equal(t, "cancelled", job.Probes[0].State)
	assert.Contains(t, job.Error, "token budget")
}

func TestFingerprintRunStopsAtTokenBudget(t *testing.T) {
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 1000
	calls := 0
	probe := func(_ context.Context, _ lmfp.Challenge, maxOutputTokens uint) (FingerprintProbeResult, error) {
		calls++
		return FingerprintProbeResult{
			Text:         "1 2 3",
			ParsedCount:  3,
			PromptTokens: 40,
			OutputTokens: 60,
		}, nil
	}

	runFingerprintJob(context.Background(), job, testChallenges, nil, probe, time.Second)

	assert.Equal(t, 3, calls)
	assert.Equal(t, 3, job.RequestsUsed)
	assert.Equal(t, 300, job.TokensUsed)
	for _, probe := range job.Probes {
		assert.Equal(t, "complete", probe.State)
	}
}

func TestFingerprintRunCancelStopsDispatch(t *testing.T) {
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 10_000
	job.cancelled = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		calls++
		return FingerprintProbeResult{}, nil
	}

	runFingerprintJob(ctx, job, testChallenges, nil, probe, time.Second)

	assert.Equal(t, 0, calls)
	assert.Equal(t, 0, job.RequestsUsed)
	for _, probe := range job.Probes {
		assert.Equal(t, "cancelled", probe.State)
	}
	assert.Equal(t, "cancelled", job.State)
}

func TestFingerprintRunRecordsFailureUsage(t *testing.T) {
	job := newTestFingerprintJob(testChallenges[:1])
	job.TokenBudget = 10_000
	probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		// The upstream billed the call before returning the error.
		return FingerprintProbeResult{PromptTokens: 30, OutputTokens: 20}, errors.New("upstream returned 500")
	}

	runFingerprintJob(context.Background(), job, testChallenges[:1], nil, probe, time.Second)

	assert.Equal(t, 50, job.TokensUsed)
	assert.Equal(t, "failed", job.Probes[0].State)
	assert.Equal(t, 30, job.Probes[0].PromptTokens)
	assert.Equal(t, 20, job.Probes[0].OutputTokens)
	assert.Equal(t, "upstream returned 500", job.Probes[0].Error)
	assert.Equal(t, "failed", job.State)
	assert.Equal(t, "upstream returned 500", job.Error)
}

func TestFingerprintRunChargesOutputGrantForUnknownUsage(t *testing.T) {
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 20000
	calls := 0
	probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		calls++
		// No usage at all, from a failed request whose spend is unknown.
		return FingerprintProbeResult{}, errors.New("upstream closed the connection")
	}

	runFingerprintJob(context.Background(), job, testChallenges, nil, probe, time.Second)

	// Every request that reported nothing is charged the input reserve plus the
	// output limit it was granted, so three unknown failures cost three grants.
	grant := fingerprintProbeInputReserve + fingerprintProbeMaxTokens
	assert.Equal(t, 3, calls)
	assert.Equal(t, 3*grant, job.TokensUsed)
}

func TestFingerprintRunStopsAfterUnknownUsageExhaustsBudget(t *testing.T) {
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 2000
	calls := 0
	probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		calls++
		return FingerprintProbeResult{}, errors.New("upstream closed the connection")
	}

	runFingerprintJob(context.Background(), job, testChallenges, nil, probe, time.Second)

	// One unknown-usage request consumes more than half the budget, so the next
	// dispatch cannot be afforded and the run stops instead of guessing.
	assert.Equal(t, 1, calls)
	assert.Greater(t, job.TokensUsed, job.TokenBudget/2)
	assert.Equal(t, "cancelled", job.Probes[1].State)
	assert.Contains(t, job.Error, "token budget")
}

func TestFingerprintRunRejectsInvalidUsageAndSaturatesTotals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reports []FingerprintProbeResult
		spent   int
	}{
		{name: "negative input", reports: []FingerprintProbeResult{{PromptTokens: -1, OutputTokens: 100}}, spent: 2000},
		{name: "negative output", reports: []FingerprintProbeResult{{PromptTokens: 100, OutputTokens: -1}}, spent: 2000},
		{name: "one report overflows", reports: []FingerprintProbeResult{{PromptTokens: math.MaxInt, OutputTokens: math.MaxInt}}, spent: math.MaxInt},
		{name: "cumulative total overflows", reports: []FingerprintProbeResult{{PromptTokens: 100, OutputTokens: 100}, {OutputTokens: math.MaxInt}}, spent: math.MaxInt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := newTestFingerprintJob(testChallenges)
			job.TokenBudget = 2000
			calls := 0
			probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
				require.Less(t, calls, len(tc.reports), "invalid usage must not authorize another request")
				result := tc.reports[calls]
				calls++
				return result, errors.New("upstream returned invalid usage")
			}

			runFingerprintJob(context.Background(), job, testChallenges, nil, probe, time.Second)

			assert.Equal(t, len(tc.reports), calls)
			assert.Equal(t, tc.spent, job.TokensUsed)
			assert.Equal(t, "cancelled", job.Probes[calls].State)
			assert.Contains(t, job.Error, "token budget")
			for _, result := range job.Probes {
				assert.GreaterOrEqual(t, result.PromptTokens, 0)
				assert.GreaterOrEqual(t, result.OutputTokens, 0)
			}
		})
	}
}

func TestFingerprintRunBoundsEachProbeWithDeadline(t *testing.T) {
	job := newTestFingerprintJob(testChallenges[:1])
	job.TokenBudget = 10_000
	var deadlineSet bool
	probe := func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		_, deadlineSet = ctx.Deadline()
		return FingerprintProbeResult{Text: "1 2 3", ParsedCount: 3}, nil
	}

	runFingerprintJob(context.Background(), job, testChallenges[:1], nil, probe, 30*time.Second)

	assert.True(t, deadlineSet, "every probe must run under its own deadline")
}

func TestFingerprintRegistryRejectsWhenActiveCapReached(t *testing.T) {
	// The registry is local: this test never touches the process-wide one, so it
	// needs no isolation and must not copy a value that embeds a mutex.
	registry := fingerprintRegistry{jobs: map[string]*FingerprintJob{}, now: time.Now}
	active := &FingerprintJob{ID: "active", State: "running", Probes: []FingerprintProbe{}}
	require.NoError(t, registry.add(active))
	for i := 1; i < fingerprintMaxActiveJobs; i++ {
		require.NoError(t, registry.add(&FingerprintJob{
			ID:     "running-" + string(rune('a'+i)),
			State:  "running",
			Probes: []FingerprintProbe{},
		}))
	}

	// The cap counts running jobs only; a finished run does not free a slot for
	// another run while the cap is reached.
	err := registry.add(&FingerprintJob{ID: "next", State: "running", Probes: []FingerprintProbe{}})
	require.ErrorIs(t, err, ErrFingerprintTooManyJobs)
	assert.Len(t, registry.jobs, fingerprintMaxActiveJobs)
}

func TestFingerprintRegistryKeepsRunningJobsPastRetention(t *testing.T) {
	registry := fingerprintRegistry{jobs: map[string]*FingerprintJob{}, now: time.Now}
	running := &FingerprintJob{ID: "running", State: "running", StartedAt: 1, Probes: []FingerprintProbe{}}
	require.NoError(t, registry.add(running))
	for i := 0; i < fingerprintMaxRetained+5; i++ {
		finished := &FingerprintJob{
			ID:         "finished",
			State:      "completed",
			StartedAt:  int64(1000 + i),
			FinishedAt: int64(1000 + i),
			Probes:     []FingerprintProbe{},
		}
		registry.jobs[finished.ID+string(rune('a'+i))] = finished
	}
	registry.mu.Lock()
	registry.trimLocked()
	registry.mu.Unlock()

	assert.NotNil(t, registry.get("running"), "a running job must never be evicted")
	assert.LessOrEqual(t, len(registry.jobs), fingerprintMaxRetained)
}

// isolateFingerprintRegistry swaps the global registry so an end-to-end test
// starts from a known state and leaves no jobs behind.
func isolateFingerprintRegistry(t *testing.T) {
	t.Helper()
	fingerprintJobs.mu.Lock()
	previousJobs, previousNow := fingerprintJobs.jobs, fingerprintJobs.now
	fingerprintJobs.jobs, fingerprintJobs.now = map[string]*FingerprintJob{}, time.Now
	fingerprintJobs.mu.Unlock()
	t.Cleanup(func() {
		fingerprintJobs.mu.Lock()
		fingerprintJobs.jobs, fingerprintJobs.now = previousJobs, previousNow
		fingerprintJobs.mu.Unlock()
	})
}

func TestStartChannelFingerprintCancelStopsProbeAndReleasesRun(t *testing.T) {
	isolateFingerprintRegistry(t)
	probeRunning := make(chan struct{}, 1)
	probeReturned := make(chan struct{}, 1)
	job, err := StartChannelFingerprint(
		context.Background(),
		FingerprintRequest{ChannelID: 1, ChannelName: "c1", Model: "m1", Count: 1, TokenBudget: 4000},
		func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
			probeRunning <- struct{}{}
			<-ctx.Done()
			probeReturned <- struct{}{}
			return FingerprintProbeResult{}, ctx.Err()
		},
	)
	require.NoError(t, err)
	<-probeRunning

	if _, err := CancelChannelFingerprint(job.ID); err != nil {
		require.NoError(t, err)
	}

	// Cancelling must reach the in-flight request, end the run goroutine, and
	// leave the job finished so no timer or cancel handle stays alive.
	select {
	case <-probeReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe was not cancelled")
	}
	live := fingerprintJobs.get(job.ID)
	require.NotNil(t, live)
	select {
	case <-live.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run goroutine did not finish")
	}
	final, err := GetChannelFingerprint(job.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", final.State)
	assert.Equal(t, "cancelled", final.Probes[0].State)
	// A finished run cannot be cancelled again; its handle was released.
	_, err = CancelChannelFingerprint(job.ID)
	assert.ErrorIs(t, err, ErrFingerprintJobFinished)
}

func TestStartChannelFingerprintAdmitsOnlyActiveCap(t *testing.T) {
	isolateFingerprintRegistry(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	blocking := func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return FingerprintProbeResult{}, ctx.Err()
	}

	started := make([]*FingerprintJob, 0, fingerprintMaxActiveJobs)
	for i := range fingerprintMaxActiveJobs {
		job, err := StartChannelFingerprint(
			context.Background(),
			FingerprintRequest{ChannelID: i + 1, ChannelName: "c", Model: "m", Count: 1, TokenBudget: 4000},
			blocking,
		)
		require.NoError(t, err)
		started = append(started, job)
	}

	_, err := StartChannelFingerprint(
		context.Background(),
		FingerprintRequest{ChannelID: 99, ChannelName: "extra", Model: "m", Count: 1, TokenBudget: 4000},
		blocking,
	)
	require.ErrorIs(t, err, ErrFingerprintTooManyJobs)

	// Running jobs are never evicted, so every admitted run still has a handle.
	for _, job := range started {
		_, err := CancelChannelFingerprint(job.ID)
		require.NoError(t, err)
	}
}
