package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lmfp"
)

// Fingerprint checks are admin-triggered, bounded and short lived, so the job
// state stays in memory: no database schema is added for a diagnostic run.
const (
	fingerprintJobTTL        = 30 * time.Minute
	fingerprintMaxActiveJobs = 4
	fingerprintMaxRetained   = 32
	fingerprintMaxChallenges = 3
	// A probe needs at least this much output room to return a usable run of
	// integers; below it the run stops instead of spending on a truncated reply.
	fingerprintMinProbeTokens = 256
	// Input reserve covers the challenge prompt and request framing, which the
	// upstream bills before any output token.
	fingerprintProbeInputReserve = 512
	// The most output one probe may ask for, before the run budget narrows it.
	fingerprintProbeMaxTokens = 4096
	// The job keeps its probes in memory for its whole TTL, so the actual prompt
	// and reply are capped per probe. A diagnostic reply never needs more than a
	// few thousand characters, and the cap keeps a pathological upstream that
	// streams an unbounded reply from growing the retained snapshot.
	fingerprintProbeTextMaxRunes = 4096
	// Budget and timeout bounds. A run is a diagnostic, not a batch job.
	fingerprintDefaultTokenBudget         = 24000
	fingerprintMaxTokenBudget             = 200000
	fingerprintDefaultProbeTimeoutSeconds = 120
	fingerprintMaxProbeTimeoutSeconds     = 600
	fingerprintMaxProbeTimeout            = 10 * time.Minute
)

// Fingerprint errors surfaced to the dashboard.
var (
	ErrFingerprintDataNotConfigured = errors.New("fingerprint reference data is not available")
	ErrFingerprintJobNotFound       = errors.New("fingerprint check was not found or has expired")
	ErrFingerprintJobFinished       = errors.New("fingerprint check already finished")
	ErrFingerprintTooManyJobs       = errors.New("too many fingerprint checks are running; wait for one to finish")
)

// FingerprintProbe is one collected answer. Prompt and Output carry the actual
// challenge text and the model's actual reply so the console can show what was
// sent and received; both come from already-collected bounded values and never
// trigger another request. The truncated flags report when the retained text was
// cut at the memory cap so the console can say so instead of showing a silently
// shortened reply as if it were complete.
type FingerprintProbe struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	ExpectedCount   int    `json:"expected_count"`
	ParsedCount     int    `json:"parsed_count"`
	PromptTokens    int    `json:"prompt_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	MaxTokens       uint   `json:"max_tokens"`
	Prompt          string `json:"prompt,omitempty"`
	PromptTruncated bool   `json:"prompt_truncated,omitempty"`
	Output          string `json:"output,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	Error           string `json:"error,omitempty"`
	Attempt         int    `json:"attempt"`
	StartedAt       int64  `json:"started_at,omitempty"`
	FinishedAt      int64  `json:"finished_at,omitempty"`
	TimeoutSeconds  int64  `json:"timeout_seconds"`
	RetryAllowed    bool   `json:"retry_allowed"`
}

// FingerprintJob is one run and its bounded budget accounting.
type FingerprintJob struct {
	ID             string             `json:"id"`
	Revision       int                `json:"revision"`
	ChannelID      int                `json:"channel_id"`
	ChannelName    string             `json:"channel_name"`
	Model          string             `json:"model"`
	Endpoint       string             `json:"endpoint"`
	State          string             `json:"state"`
	Probes         []FingerprintProbe `json:"probes"`
	Analysis       *lmfp.Analysis     `json:"analysis,omitempty"`
	Error          string             `json:"error,omitempty"`
	RequestBudget  int                `json:"request_budget"`
	RequestsUsed   int                `json:"requests_used"`
	TokenBudget    int                `json:"token_budget"`
	TokensUsed     int                `json:"tokens_used"`
	DetectorVer    string             `json:"detector_version"`
	BankSHA256     string             `json:"bank_sha256"`
	DetectorSHA256 string             `json:"detector_sha256"`
	Attribution    string             `json:"attribution"`
	Limitation     string             `json:"limitation"`
	StartedAt      int64              `json:"started_at"`
	FinishedAt     int64              `json:"finished_at,omitempty"`

	mu        sync.Mutex
	cancelled bool
	cancel    context.CancelFunc
	done      chan struct{}
	// Replies stay server-side for rescoring without repeating successful work.
	replies       []lmfp.Reply
	probeTimeout  time.Duration
	retryAccepted map[string]bool
	detector      *lmfp.Detector
}

// FingerprintProbeResult is what one probe reports back. Usage is kept even
// when the probe fails, because the upstream may already have billed the call.
type FingerprintProbeResult struct {
	Text         string
	ParsedCount  int
	PromptTokens int
	OutputTokens int
}

// boundedProbeText keeps retained probe text inside the memory cap and reports
// whether it cut the value. The cut is rune-aware so a multi-byte character is
// never split.
func boundedProbeText(text string) (string, bool) {
	runes := []rune(text)
	if len(runes) <= fingerprintProbeTextMaxRunes {
		return text, false
	}
	return string(runes[:fingerprintProbeTextMaxRunes]), true
}

// FingerprintProber runs one probe against the channel with the given output
// limit. The caller supplies it so relay plumbing (key selection, endpoint
// conversion, capacity, timeouts, billing) stays in the controller, while this
// package owns orchestration, budgets and cancel.
type FingerprintProber func(
	ctx context.Context,
	challenge lmfp.Challenge,
	maxOutputTokens uint,
) (FingerprintProbeResult, error)

// FingerprintRequest starts one run.
type FingerprintRequest struct {
	ChannelID   int
	ChannelName string
	Model       string
	Endpoint    string
	Count       int
	TokenBudget int
	// ProbeTimeout bounds a single upstream request.
	ProbeTimeout time.Duration
}

type fingerprintRegistry struct {
	mu   sync.Mutex
	jobs map[string]*FingerprintJob
	now  func() time.Time
}

var fingerprintJobs = fingerprintRegistry{jobs: map[string]*FingerprintJob{}, now: time.Now}

// StartChannelFingerprint is the internal direct-start primitive used by
// bounded service tests and trusted in-process callers. Console HTTP starts
// must use StartChannelFingerprintPreview so retries retain one accepted job.
func StartChannelFingerprint(
	parent context.Context,
	request FingerprintRequest,
	probe FingerprintProber,
) (*FingerprintJob, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		return nil, errors.New("a model is required")
	}
	if request.Count == 0 {
		request.Count = fingerprintMaxChallenges
	}
	if request.Count < 1 || request.Count > fingerprintMaxChallenges {
		return nil, fmt.Errorf("the challenge count must be between 1 and %d", fingerprintMaxChallenges)
	}
	challenges, err := lmfp.Challenges(request.Count)
	if err != nil {
		return nil, err
	}
	return startChannelFingerprintWithChallenges(parent, request, challenges, probe, nil)
}

func startChannelFingerprintWithChallenges(
	parent context.Context,
	request FingerprintRequest,
	challenges []lmfp.Challenge,
	probe FingerprintProber,
	onAccepted func(*FingerprintJob),
) (*FingerprintJob, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		return nil, errors.New("a model is required")
	}
	if request.Count == 0 {
		request.Count = len(challenges)
	}
	if request.Count < 1 || request.Count > fingerprintMaxChallenges || request.Count != len(challenges) {
		return nil, fmt.Errorf("the challenge count must be between 1 and %d", fingerprintMaxChallenges)
	}
	detector, err := loadFingerprintDetector(parent)
	if err != nil {
		// Detector loading may include operator paths or checksum values in its
		// cause. Keep cancellation observable to the worker, but expose only the
		// stable public diagnostic error through the console API.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, ErrFingerprintDataNotConfigured
	}
	// 0 means "use the configured default"; a negative or oversized budget is
	// rejected instead of silently clamped, so the operator sees what will run.
	tokenBudget := request.TokenBudget
	if tokenBudget < 0 {
		return nil, fmt.Errorf("the token budget cannot be negative")
	}
	if tokenBudget == 0 {
		tokenBudget = common.GetEnvOrDefault("FINGERPRINT_TOKEN_BUDGET", fingerprintDefaultTokenBudget)
	}
	if tokenBudget > fingerprintMaxTokenBudget {
		return nil, fmt.Errorf("the token budget cannot exceed %d", fingerprintMaxTokenBudget)
	}
	if tokenBudget-fingerprintProbeInputReserve < fingerprintMinProbeTokens {
		return nil, fmt.Errorf(
			"the token budget must be at least %d to accommodate one request",
			fingerprintProbeInputReserve+fingerprintMinProbeTokens,
		)
	}
	probeTimeout := request.ProbeTimeout
	if probeTimeout < 0 {
		return nil, fmt.Errorf("the probe timeout cannot be negative")
	}
	if probeTimeout == 0 {
		probeTimeout = time.Duration(
			min(
				max(common.GetEnvOrDefault("FINGERPRINT_PROBE_TIMEOUT_SECONDS", fingerprintDefaultProbeTimeoutSeconds), 1),
				fingerprintMaxProbeTimeoutSeconds,
			),
		) * time.Second
	}
	if probeTimeout > fingerprintMaxProbeTimeout {
		return nil, fmt.Errorf("the probe timeout cannot exceed %s", fingerprintMaxProbeTimeout)
	}
	if parent == nil {
		parent = context.Background()
	}
	if probe == nil {
		return nil, ErrFingerprintPreviewUnavailable
	}
	// The run outlives the HTTP request that started it: WithoutCancel keeps its
	// values while dropping the handler's cancellation, and the timeout bounds
	// the whole run even when no relay timeout is configured.
	runCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parent),
		probeTimeout*time.Duration(len(challenges)),
	)
	job := &FingerprintJob{
		ID:             common.GetUUID(),
		Revision:       1,
		ChannelID:      request.ChannelID,
		ChannelName:    request.ChannelName,
		Model:          model,
		Endpoint:       request.Endpoint,
		State:          "running",
		Probes:         make([]FingerprintProbe, len(challenges)),
		RequestBudget:  len(challenges),
		TokenBudget:    tokenBudget,
		DetectorVer:    detector.Version(),
		BankSHA256:     firstChecksum(detector),
		DetectorSHA256: secondChecksum(detector),
		Attribution:    lmfp.AttributionNotice,
		Limitation:     lmfp.LimitationNotice,
		StartedAt:      fingerprintJobs.now().UnixMilli(),
		cancel:         cancel,
		done:           make(chan struct{}),
		replies:        make([]lmfp.Reply, len(challenges)),
		probeTimeout:   probeTimeout,
		detector:       detector,
	}
	for i, challenge := range challenges {
		prompt, promptTruncated := boundedProbeText(challenge.Prompt)
		job.Probes[i] = FingerprintProbe{
			ID:              challenge.ID,
			State:           "queued",
			ExpectedCount:   challenge.ExpectedCount,
			Prompt:          prompt,
			PromptTruncated: promptTruncated,
		}
	}
	if err := fingerprintJobs.add(job); err != nil {
		cancel()
		return nil, err
	}
	// Preview acceptance records this exact job ID while its registry lock is
	// still held and before the worker goroutine can dispatch a paid probe.
	if onAccepted != nil {
		onAccepted(job)
	}
	go runFingerprintJob(runCtx, job, challenges, detector, probe, probeTimeout)
	return job.snapshot(), nil
}

// GetChannelFingerprint returns the current state of one run.
func GetChannelFingerprint(id string) (*FingerprintJob, error) {
	job := fingerprintJobs.get(id)
	if job == nil {
		return nil, ErrFingerprintJobNotFound
	}
	return job.snapshot(), nil
}

// CancelChannelFingerprint stops the remaining probes of a run. HTTP callers
// supply the revision they observed; a delayed cancel cannot stop a newer retry.
// Trusted in-process cleanup callers may omit the fence.
func CancelChannelFingerprint(id string, expectedRevision ...int) (*FingerprintJob, error) {
	job := fingerprintJobs.get(id)
	if job == nil {
		return nil, ErrFingerprintJobNotFound
	}
	job.mu.Lock()
	if job.FinishedAt != 0 || len(expectedRevision) > 0 && expectedRevision[0] != job.Revision {
		job.mu.Unlock()
		return job.snapshot(), ErrFingerprintJobFinished
	}
	job.cancelled = true
	cancel := job.cancel
	job.mu.Unlock()
	cancel()
	return job.snapshot(), nil
}

func firstChecksum(detector *lmfp.Detector) string {
	bank, _ := detector.Checksums()
	return bank
}

func secondChecksum(detector *lmfp.Detector) string {
	_, detectorSum := detector.Checksums()
	return detectorSum
}

func runFingerprintJob(
	ctx context.Context,
	job *FingerprintJob,
	challenges []lmfp.Challenge,
	detector *lmfp.Detector,
	probe FingerprintProber,
	probeTimeout time.Duration,
) {
	indices := make([]int, len(challenges))
	for i := range indices {
		indices[i] = i
	}
	runFingerprintProbes(ctx, job, challenges, indices, detector, probe, probeTimeout)
}

func runFingerprintProbes(ctx context.Context, job *FingerprintJob, challenges []lmfp.Challenge, indices []int, detector *lmfp.Detector, probe FingerprintProber, probeTimeout time.Duration) {
	job.mu.Lock()
	cancelRun := job.cancel
	job.mu.Unlock()
	defer func() {
		job.mu.Lock()
		replies := make([]lmfp.Reply, 0, len(job.replies))
		for _, reply := range job.replies {
			if reply.Text != "" {
				replies = append(replies, reply)
			}
		}
		job.mu.Unlock()
		job.finish(ctx, replies, detector)
		// Releasing the run context frees its timer; the job is terminal here.
		if cancelRun != nil {
			cancelRun()
		}
	}()
	for _, i := range indices {
		challenge := challenges[i]
		if ctx.Err() != nil {
			job.cancelQueuedProbes(i, "Cancelled before dispatch.")
			return
		}
		remaining := job.TokenBudget - job.tokensUsed()
		if remaining < fingerprintProbeInputReserve+fingerprintMinProbeTokens {
			job.setJobError(fmt.Sprintf(
				"Stopped before reaching the %d token budget; the remaining budget cannot cover another request.",
				job.TokenBudget,
			))
			job.cancelQueuedProbes(i, "")
			return
		}
		// Bound the requested output by the remaining scheduling budget. Input
		// tokens remain an estimate, so this is not an absolute billing cap.
		maxOutputTokens := uint(min(remaining-fingerprintProbeInputReserve, fingerprintProbeMaxTokens))
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		started := time.Now()
		effectiveTimeout := probeTimeout
		if deadline, ok := probeCtx.Deadline(); ok {
			effectiveTimeout = max(0, deadline.Sub(started))
		}
		job.mu.Lock()
		if len(job.replies) != len(job.Probes) {
			job.replies = make([]lmfp.Reply, len(job.Probes))
		}
		job.Probes[i].Attempt++
		job.Probes[i].StartedAt = started.UnixMilli()
		job.Probes[i].FinishedAt = 0
		job.Probes[i].TimeoutSeconds = int64((effectiveTimeout + time.Second - 1) / time.Second)
		job.Probes[i].State, job.Probes[i].Error, job.Probes[i].MaxTokens = "running", "", maxOutputTokens
		job.RequestsUsed++
		job.mu.Unlock()
		result, err := probe(probeCtx, challenge, maxOutputTokens)
		probeErr := probeCtx.Err()
		cancel()
		if probeErr != nil {
			err = probeErr
		}
		// Usage is recorded before the error is examined: a failed request may
		// already have been billed upstream. A request that reported nothing may
		// have spent its whole output grant, so it is charged the input reserve
		// plus the limit it was given; otherwise unknown failures could quietly
		// run past the budget.
		spent := 0
		if result.PromptTokens >= 0 && result.OutputTokens >= 0 {
			spent = result.PromptTokens + min(result.OutputTokens, math.MaxInt-result.PromptTokens)
		}
		if spent == 0 {
			spent = fingerprintProbeInputReserve + int(maxOutputTokens)
		}
		result.PromptTokens = max(0, result.PromptTokens)
		result.OutputTokens = max(0, result.OutputTokens)
		job.addTokens(spent)
		if err != nil {
			if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				job.failProbe(i, "Cancelled during the request.", result)
				job.setProbeState(i, "cancelled", "Cancelled during the request.", maxOutputTokens)
				job.cancelQueuedProbes(i+1, "Cancelled before dispatch.")
				return
			}
			job.failProbe(i, err.Error(), result)
			if ctx.Err() != nil {
				job.cancelQueuedProbes(i+1, "Cancelled before dispatch.")
				return
			}
			continue
		}
		job.completeProbe(i, result, maxOutputTokens)
		job.mu.Lock()
		// Keep rescoring evidence within the same retained-text bound as the
		// visible reply; a provider ignoring its output cap cannot grow the job.
		job.replies[i] = lmfp.Reply{Text: job.Probes[i].Output, ExpectedCount: challenge.ExpectedCount}
		job.mu.Unlock()
	}
}

// finish records the terminal state: a ranking, an inconclusive result, or the
// reason the run stopped early.
func (job *FingerprintJob) finish(ctx context.Context, replies []lmfp.Reply, detector *lmfp.Detector) {
	job.mu.Lock()
	cancelled := job.cancelled
	job.mu.Unlock()
	if cancelled {
		job.setTerminal("cancelled", "", nil)
		return
	}
	if len(replies) == 0 {
		reason := job.errorText()
		if reason == "" {
			reason = job.firstProbeError()
		}
		if reason == "" {
			reason = "No challenge produced a reply."
		}
		if ctx.Err() != nil {
			reason = "The check ran out of time before any challenge produced a reply."
		}
		job.setTerminal("failed", reason, nil)
		return
	}
	if detector == nil {
		reason := job.errorText()
		if reason == "" {
			reason = ErrFingerprintDataNotConfigured.Error()
		}
		job.setTerminal("failed", reason, nil)
		return
	}
	analysis, err := detector.Analyze(replies)
	if err != nil {
		// A run that stopped early already recorded why; that reason is more
		// useful than the scoring failure it caused.
		reason := job.errorText()
		if reason == "" {
			reason = err.Error()
		}
		job.setTerminal("failed", reason, nil)
		return
	}
	if len(analysis.Results) == 0 {
		reason := job.errorText()
		if reason == "" {
			reason = analysis.Reason
		}
		if reason == "" {
			reason = "The replies could not be scored."
		}
		job.setTerminal("inconclusive", reason, analysis)
		return
	}
	job.setTerminal("completed", job.errorText(), analysis)
}
