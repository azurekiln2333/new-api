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

// FingerprintProbe is one collected answer.
type FingerprintProbe struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	ExpectedCount int    `json:"expected_count"`
	ParsedCount   int    `json:"parsed_count"`
	PromptTokens  int    `json:"prompt_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	MaxTokens     uint   `json:"max_tokens"`
	Error         string `json:"error,omitempty"`
}

// FingerprintJob is one run and its bounded budget accounting.
type FingerprintJob struct {
	ID             string             `json:"id"`
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
}

// FingerprintProbeResult is what one probe reports back. Usage is kept even
// when the probe fails, because the upstream may already have billed the call.
type FingerprintProbeResult struct {
	Text         string
	ParsedCount  int
	PromptTokens int
	OutputTokens int
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

// StartChannelFingerprint validates the run, records its budget and starts the
// probes. It returns as soon as the run is admitted; the run continues on its
// own bounded context, so the HTTP request finishing does not stop it.
func StartChannelFingerprint(
	parent context.Context,
	request FingerprintRequest,
	probe FingerprintProber,
) (*FingerprintJob, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		return nil, errors.New("a model is required")
	}
	if request.Count < 1 || request.Count > fingerprintMaxChallenges {
		return nil, fmt.Errorf("the challenge count must be between 1 and %d", fingerprintMaxChallenges)
	}
	detector, err := loadFingerprintDetector(parent)
	if err != nil {
		return nil, err
	}
	challenges, err := lmfp.Challenges(request.Count)
	if err != nil {
		return nil, err
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
	// The run outlives the HTTP request that started it: WithoutCancel keeps its
	// values while dropping the handler's cancellation, and the timeout bounds
	// the whole run even when no relay timeout is configured.
	runCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parent),
		probeTimeout*time.Duration(len(challenges)),
	)
	job := &FingerprintJob{
		ID:             common.GetUUID(),
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
	}
	for i, challenge := range challenges {
		job.Probes[i] = FingerprintProbe{
			ID:            challenge.ID,
			State:         "queued",
			ExpectedCount: challenge.ExpectedCount,
		}
	}
	if err := fingerprintJobs.add(job); err != nil {
		cancel()
		return nil, err
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

// CancelChannelFingerprint stops the remaining probes of a run.
func CancelChannelFingerprint(id string) (*FingerprintJob, error) {
	job := fingerprintJobs.get(id)
	if job == nil {
		return nil, ErrFingerprintJobNotFound
	}
	if job.isFinished() {
		return job.snapshot(), ErrFingerprintJobFinished
	}
	job.mu.Lock()
	job.cancelled = true
	job.mu.Unlock()
	job.cancel()
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
	replies := make([]lmfp.Reply, 0, len(challenges))
	defer func() {
		job.finish(ctx, replies, detector)
		// Releasing the run context frees its timer; the job is terminal here.
		if job.cancel != nil {
			job.cancel()
		}
		if job.done != nil {
			close(job.done)
		}
	}()
	for i, challenge := range challenges {
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
		// The request limit never exceeds the remaining budget, so the budget is
		// a real cap on what one probe can ask for rather than a post-hoc check.
		maxOutputTokens := uint(min(remaining-fingerprintProbeInputReserve, fingerprintProbeMaxTokens))
		job.setProbeState(i, "running", "", maxOutputTokens)
		job.addRequestsUsed(1)
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		result, err := probe(probeCtx, challenge, maxOutputTokens)
		cancel()
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
			if ctx.Err() != nil {
				job.setProbeState(i, "cancelled", "Cancelled during the request.", maxOutputTokens)
				job.cancelQueuedProbes(i+1, "Cancelled before dispatch.")
				return
			}
			job.failProbe(i, err.Error(), result)
			continue
		}
		job.completeProbe(i, result, maxOutputTokens)
		replies = append(replies, lmfp.Reply{Text: result.Text, ExpectedCount: challenge.ExpectedCount})
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
		reason := analysis.Reason
		if reason == "" {
			reason = "The replies could not be scored."
		}
		job.setTerminal("inconclusive", reason, analysis)
		return
	}
	job.setTerminal("completed", job.errorText(), analysis)
}
