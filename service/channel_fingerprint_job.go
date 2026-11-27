package service

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lmfp"
)

// The detector holds a multi-megabyte reference bank, so it is loaded once and
// reused. A changed directory or pins reloads it.
var fingerprintDetectorCache struct {
	mu       sync.Mutex
	dir      string
	bankPin  string
	detPin   string
	detector *lmfp.Detector
}

func loadFingerprintDetector(ctx context.Context) (*lmfp.Detector, error) {
	options := lmfp.Options{
		Dir:            common.GetEnvOrDefaultString("FINGERPRINT_DATA_DIR", ""),
		BankSHA256:     common.GetEnvOrDefaultString("FINGERPRINT_BANK_SHA256", ""),
		DetectorSHA256: common.GetEnvOrDefaultString("FINGERPRINT_DETECTOR_SHA256", ""),
	}
	cache := &fingerprintDetectorCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.detector != nil &&
		cache.dir == options.Dir &&
		cache.bankPin == options.BankSHA256 &&
		cache.detPin == options.DetectorSHA256 {
		return cache.detector, nil
	}
	detector, err := lmfp.Load(ctx, options)
	if err != nil {
		return nil, err
	}
	cache.dir, cache.bankPin, cache.detPin, cache.detector = options.Dir, options.BankSHA256, options.DetectorSHA256, detector
	return detector, nil
}

// add admits a run or rejects it. Running jobs are never evicted: they hold the
// cancel handle and would keep spending upstream quota with no way to stop them.
func (registry *fingerprintRegistry) add(job *FingerprintJob) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.expireLocked()
	active := 0
	for _, existing := range registry.jobs {
		if !existing.isFinished() {
			active++
		}
	}
	if active >= fingerprintMaxActiveJobs {
		return ErrFingerprintTooManyJobs
	}
	registry.jobs[job.ID] = job
	registry.trimLocked()
	return nil
}

func (registry *fingerprintRegistry) get(id string) *FingerprintJob {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.expireLocked()
	return registry.jobs[id]
}

// expireLocked drops finished runs past their TTL. Only terminal runs expire,
// so a slow check keeps its handle until it finishes or is cancelled.
func (registry *fingerprintRegistry) expireLocked() {
	cutoff := registry.now().Add(-fingerprintJobTTL).UnixMilli()
	for id, job := range registry.jobs {
		job.mu.Lock()
		finished := job.FinishedAt
		job.mu.Unlock()
		if finished != 0 && finished < cutoff {
			delete(registry.jobs, id)
		}
	}
}

// trimLocked forgets the oldest terminal runs beyond the retention cap; active
// runs always stay.
func (registry *fingerprintRegistry) trimLocked() {
	for len(registry.jobs) > fingerprintMaxRetained {
		oldestID := ""
		var oldest int64
		for id, job := range registry.jobs {
			job.mu.Lock()
			finished, started := job.FinishedAt, job.StartedAt
			job.mu.Unlock()
			if finished == 0 {
				continue
			}
			if oldestID == "" || started < oldest {
				oldestID, oldest = id, started
			}
		}
		if oldestID == "" {
			return
		}
		delete(registry.jobs, oldestID)
	}
}

func (job *FingerprintJob) isFinished() bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.FinishedAt != 0
}

func (job *FingerprintJob) tokensUsed() int {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.TokensUsed
}

func (job *FingerprintJob) addTokens(count int) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.TokensUsed += min(max(0, count), math.MaxInt-job.TokensUsed)
}

func (job *FingerprintJob) setProbeState(index int, state, message string, maxTokens uint) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if index < 0 || index >= len(job.Probes) {
		return
	}
	job.Probes[index].State = state
	job.Probes[index].Error = message
	if state == "cancelled" {
		job.Probes[index].FinishedAt = time.Now().UnixMilli()
	}
	if maxTokens > 0 {
		job.Probes[index].MaxTokens = maxTokens
	}
}

// cancelQueuedProbes marks the probes from index onward cancelled, so a stopped
// run never leaves entries that still look pending.
func (job *FingerprintJob) cancelQueuedProbes(index int, message string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	for i := index; i < len(job.Probes); i++ {
		if job.Probes[i].State == "queued" {
			job.Probes[i].State = "cancelled"
			job.Probes[i].Error = message
			job.Probes[i].FinishedAt = time.Now().UnixMilli()
		}
	}
}

func (job *FingerprintJob) completeProbe(index int, result FingerprintProbeResult, maxTokens uint) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if index < 0 || index >= len(job.Probes) {
		return
	}
	job.Probes[index].State = "complete"
	job.Probes[index].FinishedAt = time.Now().UnixMilli()
	job.Probes[index].ParsedCount = result.ParsedCount
	job.Probes[index].PromptTokens = result.PromptTokens
	job.Probes[index].OutputTokens = result.OutputTokens
	job.Probes[index].MaxTokens = maxTokens
	output, outputTruncated := boundedProbeText(result.Text)
	job.Probes[index].Output = output
	job.Probes[index].OutputTruncated = outputTruncated
	job.Probes[index].Error = ""
}

// failProbe keeps the usage a failed request already caused, so the run's token
// accounting matches what the upstream billed.
func (job *FingerprintJob) failProbe(index int, message string, result FingerprintProbeResult) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if index < 0 || index >= len(job.Probes) {
		return
	}
	job.Probes[index].State = "failed"
	job.Probes[index].FinishedAt = time.Now().UnixMilli()
	job.Probes[index].PromptTokens = result.PromptTokens
	job.Probes[index].OutputTokens = result.OutputTokens
	output, outputTruncated := boundedProbeText(result.Text)
	job.Probes[index].Output = output
	job.Probes[index].OutputTruncated = outputTruncated
	job.Probes[index].Error = message
}

// firstProbeError reports the first probe failure, so a run that produced no
// reply still explains itself.
func (job *FingerprintJob) firstProbeError() string {
	job.mu.Lock()
	defer job.mu.Unlock()
	for _, probe := range job.Probes {
		if probe.State == "failed" && probe.Error != "" {
			return probe.Error
		}
	}
	return ""
}

func (job *FingerprintJob) errorText() string {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.Error
}

// setJobError records why the run stopped early without ending it.
func (job *FingerprintJob) setJobError(message string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.Error = message
}

func (job *FingerprintJob) setTerminal(state, message string, analysis *lmfp.Analysis) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.cancelled {
		state, analysis = "cancelled", nil
	}
	job.State = state
	if message != "" {
		job.Error = message
	}
	job.Analysis = analysis
	job.FinishedAt = time.Now().UnixMilli()
	// Publish terminal state and retry readiness together. The worker only
	// releases its captured context after this point, never these new handles.
	if job.done != nil {
		close(job.done)
	}
}

// snapshot returns a copy safe to serialize while the run continues.
func (job *FingerprintJob) snapshot() *FingerprintJob {
	job.mu.Lock()
	defer job.mu.Unlock()
	copied := &FingerprintJob{
		ID:             job.ID,
		Revision:       job.Revision,
		ChannelID:      job.ChannelID,
		ChannelName:    job.ChannelName,
		Model:          job.Model,
		Endpoint:       job.Endpoint,
		State:          job.State,
		Error:          job.Error,
		RequestBudget:  job.RequestBudget,
		RequestsUsed:   job.RequestsUsed,
		TokenBudget:    job.TokenBudget,
		TokensUsed:     job.TokensUsed,
		DetectorVer:    job.DetectorVer,
		BankSHA256:     job.BankSHA256,
		DetectorSHA256: job.DetectorSHA256,
		Attribution:    job.Attribution,
		Limitation:     job.Limitation,
		StartedAt:      job.StartedAt,
		FinishedAt:     job.FinishedAt,
		Probes:         make([]FingerprintProbe, len(job.Probes)),
	}
	copy(copied.Probes, job.Probes)
	for i := range copied.Probes {
		copied.Probes[i].RetryAllowed = job.FinishedAt != 0 && !job.cancelled &&
			job.State != "cancelled" && copied.Probes[i].State == "failed" && copied.Probes[i].Attempt == 1 &&
			!job.retryAccepted[copied.Probes[i].ID+":1"] &&
			job.TokenBudget-job.TokensUsed >= fingerprintProbeInputReserve+fingerprintMinProbeTokens
	}
	if job.Analysis != nil {
		analysis := *job.Analysis
		analysis.Results = append([]lmfp.Candidate(nil), job.Analysis.Results...)
		copied.Analysis = &analysis
	}
	return copied
}
