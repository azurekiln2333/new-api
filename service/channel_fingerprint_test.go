package service

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/lmfp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprintDetectorConfigurationErrorIsSafe(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("FINGERPRINT_DATA_DIR", dataDir)
	t.Setenv("FINGERPRINT_BANK_SHA256", "bank-pin")
	t.Setenv("FINGERPRINT_DETECTOR_SHA256", "detector-pin")

	fingerprintDetectorCache.mu.Lock()
	oldDir, oldBank, oldDetectorPin, oldDetector :=
		fingerprintDetectorCache.dir, fingerprintDetectorCache.bankPin,
		fingerprintDetectorCache.detPin, fingerprintDetectorCache.detector
	fingerprintDetectorCache.dir = ""
	fingerprintDetectorCache.bankPin = ""
	fingerprintDetectorCache.detPin = ""
	fingerprintDetectorCache.detector = nil
	fingerprintDetectorCache.mu.Unlock()
	t.Cleanup(func() {
		fingerprintDetectorCache.mu.Lock()
		fingerprintDetectorCache.dir = oldDir
		fingerprintDetectorCache.bankPin = oldBank
		fingerprintDetectorCache.detPin = oldDetectorPin
		fingerprintDetectorCache.detector = oldDetector
		fingerprintDetectorCache.mu.Unlock()
	})

	calls := 0
	_, err := StartChannelFingerprint(t.Context(), FingerprintRequest{
		ChannelID: 1, Model: "gpt-4.1", Count: 1, TokenBudget: 24000,
	}, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		calls++
		return FingerprintProbeResult{}, nil
	})
	require.ErrorIs(t, err, ErrFingerprintDataNotConfigured)
	assert.Zero(t, calls, "missing reference data must fail before any paid probe")
	assert.NotContains(t, err.Error(), dataDir)
}

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

func TestFingerprintInconclusiveRunPreservesBudgetStop(t *testing.T) {
	detector, err := lmfp.Load(t.Context(), lmfp.Options{})
	require.NoError(t, err)

	// This fixed ascending reply is rejected as program-like by the real
	// detector. Its reported usage leaves too little room for another probe.
	numbers := make([]string, 100)
	for i := range numbers {
		numbers[i] = strconv.Itoa(i + 1)
	}
	output := strings.Join(numbers, " ")
	job := newTestFingerprintJob(testChallenges)
	job.TokenBudget = 1000
	calls := 0
	runFingerprintJob(t.Context(), job, testChallenges, detector, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		calls++
		return FingerprintProbeResult{Text: output, ParsedCount: 100, PromptTokens: 100, OutputTokens: 200}, nil
	}, time.Second)

	final := job.snapshot()
	assert.Equal(t, 1, calls, "the scheduling stop must prevent another paid probe")
	assert.Equal(t, 1, final.RequestsUsed)
	assert.Equal(t, 300, final.TokensUsed)
	assert.Equal(t, "complete", final.Probes[0].State)
	assert.Equal(t, "cancelled", final.Probes[1].State)
	assert.Equal(t, "cancelled", final.Probes[2].State)
	assert.Equal(t, "inconclusive", final.State)
	assert.Equal(t, "Stopped before reaching the 1000 token budget; the remaining budget cannot cover another request.", final.Error)
	require.NotNil(t, final.Analysis)
	assert.Equal(t, "not_model", final.Analysis.Decision)
	assert.Empty(t, final.Analysis.Results)
	assert.NotEmpty(t, final.Analysis.Reason)
	assert.NotEqual(t, final.Error, final.Analysis.Reason, "scoring evidence remains separate from the scheduling stop")
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
	fingerprintPreviews.mu.Lock()
	previousPreviews, previousPreviewNow := fingerprintPreviews.previews, fingerprintPreviews.now
	fingerprintPreviews.previews, fingerprintPreviews.now = map[string]*fingerprintPreviewRecord{}, time.Now
	fingerprintJobs.mu.Lock()
	previousJobs, previousNow := fingerprintJobs.jobs, fingerprintJobs.now
	fingerprintJobs.jobs, fingerprintJobs.now = map[string]*FingerprintJob{}, time.Now
	fingerprintJobs.mu.Unlock()
	fingerprintPreviews.mu.Unlock()
	t.Cleanup(func() {
		fingerprintPreviews.mu.Lock()
		fingerprintJobs.mu.Lock()
		fingerprintJobs.jobs, fingerprintJobs.now = previousJobs, previousNow
		fingerprintJobs.mu.Unlock()
		fingerprintPreviews.previews, fingerprintPreviews.now = previousPreviews, previousPreviewNow
		fingerprintPreviews.mu.Unlock()
	})
}

func TestFingerprintPreviewExactChallengesAndRetryBinding(t *testing.T) {
	isolateFingerprintRegistry(t)
	request := FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "openai", Count: 3}
	selection := FingerprintKeySelection{EntryID: 41, Revision: 7, Index: 1, HasIndex: true}
	preview, err := CreateChannelFingerprintPreview(t.Context(), 9, request, selection)
	require.NoError(t, err)
	require.Len(t, preview.Probes, 3)
	assert.Empty(t, fingerprintJobs.jobs, "generating actual SDK prompts never starts upstream work")
	reshuffled, err := CreateChannelFingerprintPreview(t.Context(), 9, request, selection)
	require.NoError(t, err)
	assert.NotEqual(t, preview.PreviewID, reshuffled.PreviewID)
	assert.NotEqual(t, preview.Probes[0].ID, reshuffled.Probes[0].ID, "reshuffling creates a separate SDK challenge set")
	assert.Empty(t, fingerprintJobs.jobs)
	assert.Equal(t, "41", *preview.KeyEntryID)
	assert.EqualValues(t, 7, *preview.ExpectedRev)
	assert.Equal(t, 1, *preview.KeyIndex)
	for _, sample := range preview.Probes {
		assert.NotEmpty(t, sample.ID)
		assert.NotEmpty(t, sample.Prompt)
		assert.GreaterOrEqual(t, sample.ExpectedCount, 292)
		assert.LessOrEqual(t, sample.ExpectedCount, 332)
	}

	// The response is a copy: client-side edits cannot change retained prompts.
	expected := append([]FingerprintPreviewProbe(nil), preview.Probes...)
	preview.Probes[0].Prompt = "client replacement must never be dispatched"
	var calls atomic.Int32
	seen := make(chan lmfp.Challenge, 3)
	release := make(chan struct{})
	probe := func(ctx context.Context, challenge lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		calls.Add(1)
		seen <- challenge
		select {
		case <-release:
		case <-ctx.Done():
		}
		return FingerprintProbeResult{PromptTokens: 1, OutputTokens: 1}, errors.New("no scoreable reply")
	}
	var starts sync.WaitGroup
	type result struct {
		job *FingerprintJob
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		starts.Go(func() {
			started, startErr := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, selection, probe)
			results <- result{job: started, err: startErr}
		})
	}
	starts.Wait()
	accepted := <-results
	require.NoError(t, accepted.err)
	job := accepted.job
	live := fingerprintJobs.get(job.ID)
	require.NotNil(t, live)
	t.Cleanup(func() {
		live.cancel()
		<-live.done
	})
	concurrent := <-results
	require.NoError(t, concurrent.err)
	assert.Equal(t, job.ID, concurrent.job.ID, "concurrent first Starts admit exactly one job")
	assert.Equal(t, 24000, job.TokenBudget)
	replayed, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, selection, probe)
	require.NoError(t, err)
	assert.Equal(t, job.ID, replayed.ID, "response-loss retries bind to the accepted job")
	select {
	case first := <-seen:
		assert.Equal(t, expected[0].Prompt, first.Prompt)
		assert.Equal(t, expected[0].ID, first.ID)
		assert.Equal(t, expected[0].ExpectedCount, first.ExpectedCount)
	case <-time.After(5 * time.Second):
		t.Fatal("accepted preview never reached its prober")
	}
	assert.EqualValues(t, 1, calls.Load(), "a retry must not launch an additional paid worker")
	close(release)
	select {
	case <-live.done:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted preview did not finish")
	}
	for _, sample := range expected[1:] {
		challenge := <-seen
		assert.Equal(t, sample.Prompt, challenge.Prompt)
		assert.Equal(t, sample.ID, challenge.ID)
		assert.Equal(t, sample.ExpectedCount, challenge.ExpectedCount)
	}
	final, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, selection, probe)
	require.NoError(t, err)
	assert.Equal(t, job.ID, final.ID, "completed/cancelled runs cannot be restarted with the same preview")
	assert.EqualValues(t, 3, calls.Load())
}

func TestFingerprintPreviewRejectsContextMismatchAndLostState(t *testing.T) {
	isolateFingerprintRegistry(t)
	request := FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "", Count: 3}
	selection := FingerprintKeySelection{EntryID: 41, Revision: 7, Index: 1, HasIndex: true}
	preview, err := CreateChannelFingerprintPreview(t.Context(), 9, request, selection)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		owner     int
		id        string
		request   FingerprintRequest
		selection FingerprintKeySelection
	}{
		{name: "wrong owner", owner: 10, id: preview.PreviewID, request: request, selection: selection},
		{name: "missing owner", id: preview.PreviewID, request: request, selection: selection},
		{name: "other process or restart", owner: 9, id: "unknown", request: request, selection: selection},
		{name: "missing preview", owner: 9, request: request, selection: selection},
		{name: "channel", owner: 9, id: preview.PreviewID, request: FingerprintRequest{ChannelID: 13, Model: "m", Count: 3}, selection: selection},
		{name: "model", owner: 9, id: preview.PreviewID, request: FingerprintRequest{ChannelID: 12, Model: "n", Count: 3}, selection: selection},
		{name: "raw auto differs from chat intent", owner: 9, id: preview.PreviewID, request: FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "openai", Count: 3}, selection: selection},
		{name: "count", owner: 9, id: preview.PreviewID, request: FingerprintRequest{ChannelID: 12, Model: "m", Count: 2}, selection: selection},
		{name: "automatic cannot replace selected", owner: 9, id: preview.PreviewID, request: request},
		{name: "key identity", owner: 9, id: preview.PreviewID, request: request, selection: FingerprintKeySelection{EntryID: 42, Revision: 7, Index: 1, HasIndex: true}},
		{name: "key revision", owner: 9, id: preview.PreviewID, request: request, selection: FingerprintKeySelection{EntryID: 41, Revision: 8, Index: 1, HasIndex: true}},
		{name: "key index fence", owner: 9, id: preview.PreviewID, request: request, selection: FingerprintKeySelection{EntryID: 41, Revision: 7, Index: 0, HasIndex: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := StartChannelFingerprintPreview(t.Context(), tc.owner, tc.id, tc.request, tc.selection, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
				t.Error("invalid preview must never dispatch")
				return FingerprintProbeResult{}, nil
			})
			require.ErrorIs(t, err, ErrFingerprintPreviewUnavailable)
			assert.Empty(t, fingerprintJobs.jobs)
		})
	}

	accepted, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, selection, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		return FingerprintProbeResult{PromptTokens: 1, OutputTokens: 1}, errors.New("no scoreable reply")
	})
	require.NoError(t, err)
	live := fingerprintJobs.get(accepted.ID)
	require.NotNil(t, live)
	<-live.done
	fingerprintJobs.mu.Lock()
	delete(fingerprintJobs.jobs, accepted.ID) // Simulate completed-job eviction or lost process state.
	fingerprintJobs.mu.Unlock()
	_, err = StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, selection, nil)
	require.ErrorIs(t, err, ErrFingerprintPreviewUnavailable)
	assert.Empty(t, fingerprintJobs.jobs, "a lost job handle must not recreate paid work")
}

func TestFingerprintPreviewExpiryCapacityAndActiveRetention(t *testing.T) {
	isolateFingerprintRegistry(t)
	now := time.Now()
	fingerprintPreviews.now = func() time.Time { return now }
	request := FingerprintRequest{ChannelID: 12, Model: "m", Count: 1}
	var first *FingerprintPreview
	for range fingerprintPreviewMaxPerOwner {
		preview, err := CreateChannelFingerprintPreview(t.Context(), 9, request, FingerprintKeySelection{})
		require.NoError(t, err)
		if first == nil {
			first = preview
		}
	}
	_, err := CreateChannelFingerprintPreview(t.Context(), 9, request, FingerprintKeySelection{})
	require.ErrorIs(t, err, ErrFingerprintPreviewLimit)
	now = now.Add(fingerprintPreviewTTL)
	_, err = StartChannelFingerprintPreview(t.Context(), 9, first.PreviewID, request, FingerprintKeySelection{}, nil)
	require.ErrorIs(t, err, ErrFingerprintPreviewUnavailable, "expiry boundary is closed")
	assert.Empty(t, fingerprintJobs.jobs)

	preview, err := CreateChannelFingerprintPreview(t.Context(), 9, request, FingerprintKeySelection{})
	require.NoError(t, err)
	job, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, FingerprintKeySelection{}, func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		<-ctx.Done()
		return FingerprintProbeResult{}, ctx.Err()
	})
	require.NoError(t, err)
	live := fingerprintJobs.get(job.ID)
	require.NotNil(t, live)
	t.Cleanup(func() { live.cancel(); <-live.done })
	now = now.Add(fingerprintPreviewRunLifetime + fingerprintJobTTL + time.Hour)
	replayed, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, FingerprintKeySelection{}, nil)
	require.NoError(t, err)
	assert.Equal(t, job.ID, replayed.ID, "active bindings are never expired or evicted")

	// Fill the actual bounded registry without launching paid runs. Admission
	// must reject rather than evict the active preview's cancellation/replay handle.
	for owner := 100; owner < 100+fingerprintPreviewMaxRetained-1; owner++ {
		_, err := CreateChannelFingerprintPreview(t.Context(), owner, request, FingerprintKeySelection{})
		require.NoError(t, err)
	}
	_, err = CreateChannelFingerprintPreview(t.Context(), 999, request, FingerprintKeySelection{})
	require.ErrorIs(t, err, ErrFingerprintPreviewLimit)
	replayed, err = StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, FingerprintKeySelection{}, nil)
	require.NoError(t, err)
	assert.Equal(t, job.ID, replayed.ID)
	live.cancel()
	<-live.done
}

// A completed preview fixture exercises the same retained prompts and budget as
// the HTTP contract. Every worker is joined before the global registry restores.
func finishedFingerprintPreview(t *testing.T, request FingerprintRequest, selection FingerprintKeySelection, probe FingerprintProber) (*FingerprintPreview, *FingerprintJob) {
	t.Helper()
	preview, err := CreateChannelFingerprintPreview(t.Context(), 9, request, selection)
	require.NoError(t, err)
	started, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, selection, probe)
	require.NoError(t, err)
	live := fingerprintJobs.get(started.ID)
	require.NotNil(t, live)
	t.Cleanup(func() {
		live.mu.Lock()
		cancel, done := live.cancel, live.done
		live.mu.Unlock()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("fingerprint worker did not stop before registry teardown")
		}
	})
	select {
	case <-live.done:
	case <-time.After(5 * time.Second):
		t.Fatal("initial fingerprint run did not finish")
	}
	return preview, live
}

func TestFingerprintRetryDispatchesOnlyFailedSampleAndReplaysAcceptedAttempt(t *testing.T) {
	isolateFingerprintRegistry(t)
	request := FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "openai", Count: 3, ProbeTimeout: 30 * time.Second}
	selection := FingerprintKeySelection{EntryID: 41, Revision: 7, Index: 1, HasIndex: true}
	initialCalls := 0
	preview, live := finishedFingerprintPreview(t, request, selection, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		initialCalls++
		result := FingerprintProbeResult{Text: "12 34 56", ParsedCount: 3, PromptTokens: 100, OutputTokens: 100}
		if initialCalls == 1 {
			return result, nil
		}
		return result, errors.New("fingerprint request failed (HTTP 503)")
	})
	before := live.snapshot()
	require.Equal(t, 3, initialCalls)
	require.True(t, before.Probes[1].RetryAllowed)
	assert.False(t, before.Probes[0].RetryAllowed)
	// Mutating the returned preview cannot replace the server-held challenge.
	expectedPrompt := preview.Probes[1].Prompt
	preview.Probes[1].Prompt = "client replacement"
	var calls atomic.Int32
	seen := make(chan lmfp.Challenge, 1)
	release := make(chan struct{})
	probe := func(ctx context.Context, challenge lmfp.Challenge, limit uint) (FingerprintProbeResult, error) {
		calls.Add(1)
		seen <- challenge
		assert.LessOrEqual(t, limit, uint(4096))
		select {
		case <-release:
		case <-ctx.Done():
			return FingerprintProbeResult{}, ctx.Err()
		}
		return FingerprintProbeResult{Text: "78 90 12", ParsedCount: 3, PromptTokens: 100, OutputTokens: 100}, nil
	}
	type result struct {
		job *FingerprintJob
		err error
	}
	results := make(chan result, 2)
	var concurrent sync.WaitGroup
	for range 2 {
		concurrent.Go(func() {
			job, err := RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[1].ID, 1, request, selection, probe)
			results <- result{job: job, err: err}
		})
	}
	concurrent.Wait()
	for range 2 {
		accepted := <-results
		require.NoError(t, accepted.err)
		assert.Equal(t, live.ID, accepted.job.ID)
		assert.Equal(t, before.Revision+1, accepted.job.Revision)
	}
	select {
	case dispatched := <-seen:
		assert.Equal(t, preview.Probes[1].ID, dispatched.ID)
		assert.Equal(t, expectedPrompt, dispatched.Prompt)
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not dispatch the selected failed sample")
	}
	progress, err := GetChannelFingerprint(live.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, calls.Load(), "concurrent replay must admit only one worker")
	assert.Equal(t, before.Probes[0], progress.Probes[0], "successful paid output is preserved")
	assert.Equal(t, before.Probes[2].Output, progress.Probes[2].Output)
	assert.Equal(t, "running", progress.Probes[1].State)
	assert.Equal(t, 2, progress.Probes[1].Attempt)
	assert.Positive(t, progress.Probes[1].StartedAt)
	assert.Zero(t, progress.Probes[1].FinishedAt)
	assert.EqualValues(t, 30, progress.Probes[1].TimeoutSeconds)
	assert.Equal(t, 24000, progress.TokenBudget)
	assert.Equal(t, before.TokensUsed, progress.TokensUsed)
	_, err = RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[2].ID, 1, request, selection, probe)
	require.Error(t, err, "a different sample cannot start a concurrent retry on this job")
	assert.EqualValues(t, 1, calls.Load())
	close(release)
	<-live.done
	final := live.snapshot()
	assert.Equal(t, before.Probes[0], final.Probes[0])
	assert.Equal(t, "complete", final.Probes[1].State)
	assert.False(t, final.Probes[1].RetryAllowed)
	assert.GreaterOrEqual(t, final.Probes[1].FinishedAt, final.Probes[1].StartedAt)
	assert.Equal(t, 4, final.RequestsUsed)
	assert.Equal(t, 4, final.RequestBudget)
	assert.Equal(t, before.TokensUsed+200, final.TokensUsed)
	assert.Equal(t, 24000, final.TokenBudget)
	replayed, err := RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[1].ID, 1, request, selection, probe)
	require.NoError(t, err)
	assert.Equal(t, final.Revision, replayed.Revision, "response-loss replay returns the already accepted result")
	for _, attempt := range []int{0, 2, 3} {
		_, err := RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[1].ID, attempt, request, selection, probe)
		require.Error(t, err, "there is at most one additional attempt per sample")
	}
	_, err = RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[0].ID, 1, request, selection, probe)
	require.Error(t, err, "a successful sample is never replayed")
	assert.EqualValues(t, 1, calls.Load())
}

func TestFingerprintRetrySharesOriginalSchedulingBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int
		wantLimit uint
	}{
		{name: "exhausted", remaining: 0},
		{name: "below useful request", remaining: 767},
		{name: "minimum useful request", remaining: 768, wantLimit: 256},
		{name: "remaining budget narrows output", remaining: 4000, wantLimit: 3488},
		{name: "output cap remains enforced", remaining: 10000, wantLimit: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateFingerprintRegistry(t)
			request := FingerprintRequest{ChannelID: 12, Model: "m", Count: 1}
			preview, live := finishedFingerprintPreview(t, request, FingerprintKeySelection{}, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
				return FingerprintProbeResult{PromptTokens: 24000 - tc.remaining}, errors.New("initial failed request")
			})
			assert.Equal(t, tc.wantLimit != 0, live.snapshot().Probes[0].RetryAllowed)
			var calls atomic.Int32
			limits := make(chan uint, 1)
			retry := func(_ context.Context, _ lmfp.Challenge, uintLimit uint) (FingerprintProbeResult, error) {
				calls.Add(1)
				limits <- uintLimit
				return FingerprintProbeResult{}, errors.New("usage unavailable")
			}
			_, err := RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[0].ID, 1, request, FingerprintKeySelection{}, retry)
			if tc.wantLimit == 0 {
				require.ErrorContains(t, err, "budget is exhausted")
				assert.Zero(t, calls.Load())
				assert.Equal(t, 1, live.snapshot().RequestBudget)
				return
			}
			require.NoError(t, err)
			<-live.done
			assert.Equal(t, tc.wantLimit, <-limits)
			final := live.snapshot()
			assert.Equal(t, 24000-tc.remaining+512+int(tc.wantLimit), final.TokensUsed, "unknown retry usage consumes its full grant, not a reset budget")
			assert.Equal(t, 24000, final.TokenBudget)
			assert.Equal(t, 2, final.RequestsUsed)
			assert.False(t, final.Probes[0].RetryAllowed)
			_, err = RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[0].ID, 2, request, FingerprintKeySelection{}, retry)
			require.Error(t, err)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestFingerprintRetryRejectsStaleBindingCapacityAndExpiry(t *testing.T) {
	isolateFingerprintRegistry(t)
	request := FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "openai", Count: 1}
	selection := FingerprintKeySelection{EntryID: 41, Revision: 7, Index: 1, HasIndex: true}
	preview, live := finishedFingerprintPreview(t, request, selection, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		return FingerprintProbeResult{PromptTokens: 1}, errors.New("initial failure")
	})
	probe := func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		t.Error("a rejected retry must not dispatch")
		return FingerprintProbeResult{}, nil
	}
	for _, tc := range []struct {
		name      string
		owner     int
		previewID string
		jobID     string
		probeID   string
		request   FingerprintRequest
		selection FingerprintKeySelection
	}{
		{name: "owner", owner: 10, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: request, selection: selection},
		{name: "lost preview", owner: 9, previewID: "missing", jobID: live.ID, probeID: preview.Probes[0].ID, request: request, selection: selection},
		{name: "job", owner: 9, previewID: preview.PreviewID, jobID: "other-job", probeID: preview.Probes[0].ID, request: request, selection: selection},
		{name: "sample", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: "other-sample", request: request, selection: selection},
		{name: "channel", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: FingerprintRequest{ChannelID: 13, Model: "m", Endpoint: "openai", Count: 1}, selection: selection},
		{name: "model", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: FingerprintRequest{ChannelID: 12, Model: "n", Endpoint: "openai", Count: 1}, selection: selection},
		{name: "endpoint", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "anthropic", Count: 1}, selection: selection},
		{name: "count", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: FingerprintRequest{ChannelID: 12, Model: "m", Endpoint: "openai", Count: 2}, selection: selection},
		{name: "automatic fallback", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: request},
		{name: "key", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: request, selection: FingerprintKeySelection{EntryID: 42, Revision: 7, Index: 1, HasIndex: true}},
		{name: "revision", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: request, selection: FingerprintKeySelection{EntryID: 41, Revision: 8, Index: 1, HasIndex: true}},
		{name: "position", owner: 9, previewID: preview.PreviewID, jobID: live.ID, probeID: preview.Probes[0].ID, request: request, selection: FingerprintKeySelection{EntryID: 41, Revision: 7, Index: 0, HasIndex: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RetryChannelFingerprintProbe(t.Context(), tc.owner, tc.previewID, tc.jobID, tc.probeID, 1, tc.request, tc.selection, probe)
			require.Error(t, err)
			assert.Equal(t, 1, live.snapshot().RequestsUsed)
		})
	}
	for i := range fingerprintMaxActiveJobs {
		require.NoError(t, fingerprintJobs.add(&FingerprintJob{ID: "occupied-" + string(rune('a'+i))}))
	}
	_, err := RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[0].ID, 1, request, selection, probe)
	require.ErrorIs(t, err, ErrFingerprintTooManyJobs)
	assert.Equal(t, 1, live.snapshot().Revision)
	future := time.Now().Add(fingerprintJobTTL + time.Minute)
	fingerprintPreviews.now = func() time.Time { return future }
	fingerprintJobs.now = func() time.Time { return future }
	_, err = RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[0].ID, 1, request, selection, probe)
	require.Error(t, err, "expiry never recreates a paid run")
}

func TestFingerprintProbeProgressReportsRunningDeadlineAndTimeoutFailure(t *testing.T) {
	isolateFingerprintRegistry(t)
	dispatched := make(chan context.Context, 1)
	preview, err := CreateChannelFingerprintPreview(t.Context(), 9, FingerprintRequest{ChannelID: 12, Model: "m", Count: 1}, FingerprintKeySelection{})
	require.NoError(t, err)
	request := FingerprintRequest{ChannelID: 12, Model: "m", Count: 1}
	started, err := StartChannelFingerprintPreview(t.Context(), 9, preview.PreviewID, request, FingerprintKeySelection{}, func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		dispatched <- ctx
		<-ctx.Done()
		return FingerprintProbeResult{PromptTokens: 10}, ctx.Err()
	})
	require.NoError(t, err)
	live := fingerprintJobs.get(started.ID)
	require.NotNil(t, live)
	t.Cleanup(func() { live.cancel(); <-live.done })
	select {
	case ctx := <-dispatched:
		_, hasDeadline := ctx.Deadline()
		assert.True(t, hasDeadline)
	case <-time.After(5 * time.Second):
		t.Fatal("sample did not start")
	}
	running, err := GetChannelFingerprint(started.ID)
	require.NoError(t, err)
	require.Len(t, running.Probes, 1)
	assert.Equal(t, "running", running.Probes[0].State)
	assert.Positive(t, running.Probes[0].StartedAt)
	assert.Positive(t, running.Probes[0].TimeoutSeconds)
	assert.Zero(t, running.Probes[0].FinishedAt)
	assert.Empty(t, running.Probes[0].Output, "status polling does not fabricate streamed output")
	_, err = CancelChannelFingerprint(started.ID, running.Revision)
	require.NoError(t, err)
	<-live.done

	// Await the probe's own deadline, without a fixed sleep. Even a prober
	// incorrectly returning nil after expiry must leave a failed attempt.
	job := newTestFingerprintJob(testChallenges[:1])
	job.TokenBudget = 1000
	runFingerprintJob(t.Context(), job, testChallenges[:1], nil, func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		<-ctx.Done()
		return FingerprintProbeResult{PromptTokens: 10}, nil
	}, time.Nanosecond)
	assert.Equal(t, "failed", job.Probes[0].State)
	assert.Equal(t, context.DeadlineExceeded.Error(), job.Probes[0].Error)
	assert.Positive(t, job.Probes[0].FinishedAt)
	assert.Equal(t, 1, job.RequestsUsed)
}

func TestFingerprintRetainedRescoringRepliesStayBounded(t *testing.T) {
	job := newTestFingerprintJob(testChallenges[:1])
	job.TokenBudget = 1000
	output := strings.Repeat("界", 4097)
	runFingerprintJob(t.Context(), job, testChallenges[:1], nil, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		return FingerprintProbeResult{Text: output, PromptTokens: 10}, nil
	}, time.Second)
	assert.True(t, job.Probes[0].OutputTruncated)
	assert.Equal(t, strings.Repeat("界", 4096), job.Probes[0].Output)
	require.Len(t, job.replies, 1)
	assert.Equal(t, job.Probes[0].Output, job.replies[0].Text, "retained evidence cannot bypass the public reply memory bound")
	assert.Equal(t, "failed", job.State)
	assert.Equal(t, ErrFingerprintDataNotConfigured.Error(), job.Error, "missing scoring data has a fixed safe fallback when no earlier stop reason exists")
}

func TestFingerprintRetryCancellationFencesEarlierRevision(t *testing.T) {
	isolateFingerprintRegistry(t)
	request := FingerprintRequest{ChannelID: 12, Model: "m", Count: 2}
	preview, live := finishedFingerprintPreview(t, request, FingerprintKeySelection{}, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		return FingerprintProbeResult{PromptTokens: 10}, errors.New("initial failure")
	})
	before := live.snapshot()
	dispatched := make(chan context.Context, 1)
	_, err := RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[0].ID, 1, request, FingerprintKeySelection{}, func(ctx context.Context, _ lmfp.Challenge, _ uint) (FingerprintProbeResult, error) {
		dispatched <- ctx
		<-ctx.Done()
		return FingerprintProbeResult{PromptTokens: 7, OutputTokens: 3}, ctx.Err()
	})
	require.NoError(t, err)
	ctx := <-dispatched
	_, err = CancelChannelFingerprint(live.ID, before.Revision)
	require.ErrorIs(t, err, ErrFingerprintJobFinished, "a delayed cancellation of the old run cannot cancel its retry")
	require.NoError(t, ctx.Err())
	current := live.snapshot()
	_, err = CancelChannelFingerprint(live.ID, current.Revision)
	require.NoError(t, err)
	<-live.done
	final := live.snapshot()
	assert.Equal(t, "cancelled", final.State)
	assert.Equal(t, "cancelled", final.Probes[0].State)
	assert.Equal(t, 2, final.Probes[0].Attempt)
	assert.Equal(t, 7, final.Probes[0].PromptTokens)
	assert.Equal(t, 3, final.Probes[0].OutputTokens)
	assert.Equal(t, before.Probes[1].Error, final.Probes[1].Error)
	assert.Equal(t, before.TokensUsed+10, final.TokensUsed)
	for _, sample := range final.Probes {
		assert.False(t, sample.RetryAllowed)
	}
	_, err = RetryChannelFingerprintProbe(t.Context(), 9, preview.PreviewID, live.ID, preview.Probes[1].ID, 1, request, FingerprintKeySelection{}, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
		t.Error("cancellation must prevent another failed sample from dispatching")
		return FingerprintProbeResult{}, nil
	})
	require.Error(t, err, "cancellation forbids all later retries")
}

func TestStartChannelFingerprintChallengeCountDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		want  int
	}{
		{name: "omitted defaults to three", want: 3},
		{name: "explicit one", count: 1, want: 1},
		{name: "explicit two", count: 2, want: 2},
		{name: "explicit three", count: 3, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateFingerprintRegistry(t)
			calls := 0
			job, err := StartChannelFingerprint(t.Context(), FingerprintRequest{
				ChannelID: 1, Model: "m", Count: tc.count, TokenBudget: 24000,
			}, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
				calls++
				return FingerprintProbeResult{PromptTokens: 1, OutputTokens: 1}, errors.New("no scoreable reply")
			})
			require.NoError(t, err)
			live := fingerprintJobs.get(job.ID)
			require.NotNil(t, live)
			select {
			case <-live.done:
			case <-time.After(5 * time.Second):
				t.Fatal("fingerprint samples did not finish")
			}
			final, err := GetChannelFingerprint(job.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.want, final.RequestBudget)
			assert.Len(t, final.Probes, tc.want)
			assert.Equal(t, tc.want, calls)
			assert.Equal(t, tc.want, final.RequestsUsed)
		})
	}
}

func TestStartChannelFingerprintRejectsOutOfRangeChallengeCount(t *testing.T) {
	for _, count := range []int{-1, 4} {
		_, err := StartChannelFingerprint(t.Context(), FingerprintRequest{
			Model: "m", Count: count,
		}, func(context.Context, lmfp.Challenge, uint) (FingerprintProbeResult, error) {
			t.Fatal("invalid count must not dispatch a sample")
			return FingerprintProbeResult{}, nil
		})
		require.ErrorContains(t, err, "challenge count must be between 1 and 3")
	}
}

func TestStartChannelFingerprintCancelStopsProbeAndReleasesRun(t *testing.T) {
	isolateFingerprintRegistry(t)
	probeRunning := make(chan struct{}, 1)
	probeReturned := make(chan struct{}, 1)
	job, err := StartChannelFingerprint(
		context.Background(),
		FingerprintRequest{ChannelID: 1, ChannelName: "c1", Model: "m1", Count: 3, TokenBudget: 24000},
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
	assert.Equal(t, 1, final.RequestsUsed, "samples stay serial and cancellation prevents later dispatch")
	require.Len(t, final.Probes, 3)
	for _, probe := range final.Probes {
		assert.Equal(t, "cancelled", probe.State)
	}
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
	for _, job := range started {
		live := fingerprintJobs.get(job.ID)
		require.NotNil(t, live)
		select {
		case <-live.done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled job did not stop before registry teardown")
		}
	}
}
