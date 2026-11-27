package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lmfp"
)

const (
	fingerprintPreviewTTL         = 5 * time.Minute
	fingerprintPreviewMaxRetained = 128
	fingerprintPreviewMaxPerOwner = 4
	fingerprintPreviewRunLifetime = fingerprintMaxProbeTimeout * fingerprintMaxChallenges
)

var (
	ErrFingerprintPreviewUnavailable = errors.New("fingerprint preview is unavailable or expired")
	ErrFingerprintPreviewLimit       = errors.New("too many fingerprint previews; wait for one to expire")
)

// FingerprintKeySelection names an allocation, never a credential. HasIndex
// adds the console's position fence without making index the allocation ID.
type FingerprintKeySelection struct {
	EntryID  int64
	Revision int64
	Index    int
	HasIndex bool
}

type FingerprintPreviewProbe struct {
	ID            string `json:"id"`
	Prompt        string `json:"prompt"`
	ExpectedCount int    `json:"expected_count"`
}

type FingerprintPreview struct {
	PreviewID   string                   `json:"preview_id"`
	ExpiresAt   int64                    `json:"expires_at"`
	ChannelID   int                      `json:"channel_id"`
	Model       string                   `json:"model"`
	Endpoint    string                   `json:"endpoint_type"`
	Count       int                      `json:"count"`
	KeyEntryID  *string                  `json:"key_entry_id,omitempty"`
	ExpectedRev *int64                   `json:"expected_rev,omitempty"`
	KeyIndex    *int                     `json:"key_index,omitempty"`
	Probes      []FingerprintPreviewProbe `json:"probes"`
}

type fingerprintPreviewRecord struct {
	ownerID    int
	request    FingerprintRequest
	selection  FingerprintKeySelection
	challenges []lmfp.Challenge
	expiresAt  time.Time
	jobID      string
}

type fingerprintPreviewRegistry struct {
	mu       sync.Mutex
	previews map[string]*fingerprintPreviewRecord
	now      func() time.Time
}

var fingerprintPreviews = fingerprintPreviewRegistry{
	previews: make(map[string]*fingerprintPreviewRecord),
	now:      time.Now,
}

// CreateChannelFingerprintPreview creates server-held SDK challenges. It never
// contacts the upstream channel. The opaque handle is process-local and bound
// to its authenticated owner and immutable request context.
func CreateChannelFingerprintPreview(
	ctx context.Context,
	ownerID int,
	request FingerprintRequest,
	selection FingerprintKeySelection,
) (*FingerprintPreview, error) {
	if ownerID <= 0 || request.ChannelID <= 0 || strings.TrimSpace(request.Model) == "" {
		return nil, ErrFingerprintPreviewUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request.Count == 0 {
		request.Count = fingerprintMaxChallenges
	}
	if request.Count < 1 || request.Count > fingerprintMaxChallenges {
		return nil, errors.New("the challenge count must be between 1 and 3")
	}
	if err := validateFingerprintSelection(selection); err != nil {
		return nil, err
	}
	challenges, err := lmfp.Challenges(request.Count)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, challenge := range challenges {
		if _, truncated := boundedProbeText(challenge.Prompt); truncated {
			return nil, ErrFingerprintPreviewUnavailable
		}
	}

	registry := &fingerprintPreviews
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.expireLocked()
	ownerCount := 0
	for _, preview := range registry.previews {
		if preview.ownerID == ownerID && preview.jobID == "" {
			ownerCount++
		}
	}
	if len(registry.previews) >= fingerprintPreviewMaxRetained || ownerCount >= fingerprintPreviewMaxPerOwner {
		return nil, ErrFingerprintPreviewLimit
	}

	id := common.GetUUID()
	now := registry.now()
	request.Model = strings.TrimSpace(request.Model)
	request.Endpoint = strings.TrimSpace(request.Endpoint)
	request.Count = len(challenges)
	request.TokenBudget = fingerprintDefaultTokenBudget
	preview := &fingerprintPreviewRecord{
		ownerID: ownerID, request: request, selection: selection,
		challenges: challenges, expiresAt: now.Add(fingerprintPreviewTTL),
	}
	registry.previews[id] = preview
	return preview.snapshot(id), nil
}

// StartChannelFingerprintPreview atomically binds one accepted preview to its
// one job before the worker can dispatch. An uncertain HTTP response is retried
// by ID and resolves to that same job; lost local state never authorizes a new
// paid run.
func StartChannelFingerprintPreview(
	parent context.Context,
	ownerID int,
	previewID string,
	request FingerprintRequest,
	selection FingerprintKeySelection,
	probe FingerprintProber,
) (*FingerprintJob, error) {
	registry := &fingerprintPreviews
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.expireLocked()
	preview := registry.previews[previewID]
	if preview == nil || ownerID <= 0 || preview.ownerID != ownerID ||
		!sameFingerprintPreviewContext(preview.request, preview.selection, request, selection) {
		return nil, ErrFingerprintPreviewUnavailable
	}
	if preview.jobID != "" {
		job, err := GetChannelFingerprint(preview.jobID)
		if err != nil {
			return nil, ErrFingerprintPreviewUnavailable
		}
		return job, nil
	}
	if !registry.now().Before(preview.expiresAt) {
		delete(registry.previews, previewID)
		return nil, ErrFingerprintPreviewUnavailable
	}

	if probe == nil {
		return nil, ErrFingerprintPreviewUnavailable
	}
	request = preview.request
	request.TokenBudget = fingerprintDefaultTokenBudget
	job, err := startChannelFingerprintWithChallenges(parent, request, preview.challenges, probe, func(accepted *FingerprintJob) {
		preview.jobID = accepted.ID
		preview.expiresAt = registry.now().Add(fingerprintPreviewRunLifetime + fingerprintJobTTL)
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

// RetryChannelFingerprintProbe admits one explicit retry of one failed sample.
// The original preview binds owner, prompts and selection; expectedAttempt is
// also the idempotency key, so response loss cannot authorize a second retry.
func RetryChannelFingerprintProbe(parent context.Context, ownerID int, previewID, jobID, probeID string, expectedAttempt int, request FingerprintRequest, selection FingerprintKeySelection, probe FingerprintProber) (*FingerprintJob, error) {
	registry := &fingerprintPreviews
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.expireLocked()
	preview := registry.previews[previewID]
	if preview == nil || ownerID <= 0 || preview.ownerID != ownerID || preview.jobID != jobID ||
		!sameFingerprintPreviewContext(preview.request, preview.selection, request, selection) {
		return nil, ErrFingerprintPreviewUnavailable
	}
	jobs := &fingerprintJobs
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	jobs.expireLocked()
	job := jobs.jobs[jobID]
	if job == nil {
		return nil, ErrFingerprintJobNotFound
	}
	job.mu.Lock()
	index := -1
	for i := range preview.challenges {
		if preview.challenges[i].ID == probeID {
			index = i
			break
		}
	}
	key := probeID + ":" + strconv.Itoa(expectedAttempt)
	if index < 0 || index >= len(job.Probes) || expectedAttempt != 1 {
		job.mu.Unlock()
		return nil, errors.New("fingerprint sample cannot be retried")
	}
	if job.retryAccepted[key] {
		job.mu.Unlock()
		return job.snapshot(), nil
	}
	if job.FinishedAt == 0 || job.cancelled || job.State == "cancelled" || probe == nil ||
		job.Probes[index].State != "failed" || job.Probes[index].Attempt != expectedAttempt {
		job.mu.Unlock()
		return nil, errors.New("fingerprint sample cannot be retried")
	}
	// Terminal publication closes done under the same lock. Do not replace
	// cancellation/done handles while the previous worker is still scoring.
	select {
	case <-job.done:
	default:
		job.mu.Unlock()
		return nil, errors.New("fingerprint sample cannot be retried")
	}
	if job.TokenBudget-job.TokensUsed < fingerprintProbeInputReserve+fingerprintMinProbeTokens {
		job.mu.Unlock()
		return nil, errors.New("fingerprint retry budget is exhausted")
	}
	job.mu.Unlock()
	active := 0
	for _, existing := range jobs.jobs {
		if !existing.isFinished() {
			active++
		}
	}
	if active >= fingerprintMaxActiveJobs {
		return nil, ErrFingerprintTooManyJobs
	}
	if parent == nil {
		parent = context.Background()
	}
	job.mu.Lock()
	// Recheck under the job lock after counting active runs. Cancellation uses
	// this same lock, so no accepted cancellation can be overwritten here.
	if job.cancelled || job.FinishedAt == 0 {
		job.mu.Unlock()
		return nil, errors.New("fingerprint sample cannot be retried")
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), job.probeTimeout)
	job.cancel, job.done = cancel, make(chan struct{})
	if job.retryAccepted == nil {
		job.retryAccepted = make(map[string]bool)
	}
	job.retryAccepted[key] = true
	job.Revision++
	job.RequestBudget++
	job.State, job.Error, job.Analysis, job.FinishedAt = "running", "", nil, 0
	// Retain successful samples and all prior spend. Only this slot is reset.
	old := job.Probes[index]
	job.Probes[index] = FingerprintProbe{
		ID: old.ID, Prompt: old.Prompt, PromptTruncated: old.PromptTruncated,
		ExpectedCount: old.ExpectedCount, Attempt: old.Attempt, State: "queued",
	}
	detector, timeout := job.detector, job.probeTimeout
	job.mu.Unlock()
	go runFingerprintProbes(runCtx, job, preview.challenges, []int{index}, detector, probe, timeout)
	return job.snapshot(), nil
}

func validateFingerprintSelection(selection FingerprintKeySelection) error {
	if selection.EntryID == 0 && selection.Revision == 0 && !selection.HasIndex {
		return nil
	}
	if selection.EntryID <= 0 || selection.Revision <= 0 || selection.Index < 0 && selection.HasIndex {
		return ErrFingerprintPreviewUnavailable
	}
	return nil
}

func sameFingerprintPreviewContext(
	preview FingerprintRequest,
	previewSelection FingerprintKeySelection,
	request FingerprintRequest,
	selection FingerprintKeySelection,
) bool {
	count := request.Count
	if count == 0 {
		count = fingerprintMaxChallenges
	}
	return preview.ChannelID == request.ChannelID &&
		preview.Model == strings.TrimSpace(request.Model) &&
		preview.Endpoint == strings.TrimSpace(request.Endpoint) &&
		preview.Count == count && previewSelection == selection
}

func (preview *fingerprintPreviewRecord) snapshot(id string) *FingerprintPreview {
	response := &FingerprintPreview{
		PreviewID: id,
		ExpiresAt: preview.expiresAt.UnixMilli(),
		ChannelID: preview.request.ChannelID,
		Model:     preview.request.Model,
		Endpoint:  preview.request.Endpoint,
		Count:     len(preview.challenges),
		Probes:    make([]FingerprintPreviewProbe, len(preview.challenges)),
	}
	if preview.selection.EntryID > 0 {
		entryID := strconv.FormatInt(preview.selection.EntryID, 10)
		revision := preview.selection.Revision
		response.KeyEntryID = &entryID
		response.ExpectedRev = &revision
		if preview.selection.HasIndex {
			index := preview.selection.Index
			response.KeyIndex = &index
		}
	}
	for i, challenge := range preview.challenges {
		response.Probes[i] = FingerprintPreviewProbe{
			ID: challenge.ID, Prompt: challenge.Prompt, ExpectedCount: challenge.ExpectedCount,
		}
	}
	return response
}

func (registry *fingerprintPreviewRegistry) expireLocked() {
	now := registry.now()
	for id, preview := range registry.previews {
		if preview.jobID == "" {
			if !now.Before(preview.expiresAt) {
				delete(registry.previews, id)
			}
			continue
		}
		job := fingerprintJobs.get(preview.jobID)
		if job == nil {
			delete(registry.previews, id)
			continue
		}
		job.mu.Lock()
		finishedAt := job.FinishedAt
		job.mu.Unlock()
		if finishedAt == 0 {
			continue // Active runs and their idempotency handles are never evicted.
		}
		preview.expiresAt = time.UnixMilli(finishedAt).Add(fingerprintJobTTL)
		if !now.Before(preview.expiresAt) {
			delete(registry.previews, id)
		}
	}
}
