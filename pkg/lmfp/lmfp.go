package lmfp

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

// Model fingerprint scoring reuses the reviewed lm-detector reference bank and
// detector instead of reimplementing the statistics. The bank ships inside the
// binary, so a release build works without any download and without operator
// setup; an operator may still point at a newer published bank.
const (
	// bankFile and detectorFile are the two files the SDK reads from a data
	// directory.
	bankFile     = "unified_bank.json"
	detectorFile = "shared_detector.bin"

	// The vendored bank is pinned by digest. A replacement bank, whether
	// configured by an operator or shipped in a future release, must update both
	// constants, so a swapped file can never silently change results.
	vendoredBankSHA256     = "85c7887bff9c1fd066666c23733ec259facc8f62d3f86a89d1795d088b6eccd6"
	vendoredDetectorSHA256 = "a55d4bb275b09b630d9deeeac5a119077869a40dc65132ffff0ab87e58b7c9e9"

	// AttributionNotice must stay with every displayed result: the scoring data
	// and algorithm are MIT licensed by their author.
	AttributionNotice = "Fingerprint scoring uses the MIT-licensed lm-detector reference bank and detector " +
		"(Copyright (c) 2026 xqy2006, github.com/Ikaleio/lm-detector)."

	// LimitationNotice must stay with every displayed result: the ranking is a
	// statistical comparison inside the reference bank only.
	LimitationNotice = "A match is a ranking relative to the reference bank. It does not prove model identity, " +
		"quality, or authenticity, and a model outside the bank still receives a closest candidate."
)

//go:embed data/unified_bank.json data/shared_detector.bin
var vendoredBank embed.FS

// ErrDataNotConfigured reports reference data that is missing or not the pinned
// bank.
var ErrDataNotConfigured = errors.New("fingerprint reference data is not available")

// Detector is a loaded reference bank plus the verified identity of the files
// that produced it.
type Detector struct {
	detector       *lmfpd.Detector
	bankSHA256     string
	detectorSHA256 string
}

// Options selects the reference data. The zero value uses the vendored bank.
type Options struct {
	// Dir is an operator-supplied directory holding the two bank files. Empty
	// selects the vendored bank.
	Dir string
	// BankSHA256 and DetectorSHA256 pin the files in Dir. They are required with
	// Dir and ignored for the vendored bank, whose digests are compiled in.
	BankSHA256     string
	DetectorSHA256 string
}

// Load returns the reference bank. It never downloads data.
func Load(ctx context.Context, options Options) (*Detector, error) {
	dir := strings.TrimSpace(options.Dir)
	if dir == "" {
		return loadVendored(ctx)
	}
	bankPin := strings.ToLower(strings.TrimSpace(options.BankSHA256))
	detectorPin := strings.ToLower(strings.TrimSpace(options.DetectorSHA256))
	if bankPin == "" || detectorPin == "" {
		return nil, fmt.Errorf("%w: a data directory requires both file checksums", ErrDataNotConfigured)
	}
	bankSum, err := fileSHA256(filepath.Join(dir, bankFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDataNotConfigured, filepath.Join(dir, bankFile))
	}
	detectorSum, err := fileSHA256(filepath.Join(dir, detectorFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDataNotConfigured, filepath.Join(dir, detectorFile))
	}
	if bankSum != bankPin {
		return nil, fmt.Errorf("fingerprint bank checksum %s does not match the pinned %s", bankSum, bankPin)
	}
	if detectorSum != detectorPin {
		return nil, fmt.Errorf("fingerprint detector checksum %s does not match the pinned %s", detectorSum, detectorPin)
	}
	return open(ctx, dir, bankSum, detectorSum)
}

// loadVendored verifies the embedded bank and reads it through the SDK. The SDK
// reads a directory, so the verified bytes are materialized once into a
// content-addressed cache directory.
func loadVendored(ctx context.Context) (*Detector, error) {
	bank, err := vendoredBank.ReadFile("data/" + bankFile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDataNotConfigured, err)
	}
	detector, err := vendoredBank.ReadFile("data/" + detectorFile)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDataNotConfigured, err)
	}
	bankSum := sha256Hex(bank)
	detectorSum := sha256Hex(detector)
	if bankSum != vendoredBankSHA256 || detectorSum != vendoredDetectorSHA256 {
		return nil, fmt.Errorf(
			"embedded fingerprint data does not match its pinned checksums (bank %s, detector %s)",
			bankSum,
			detectorSum,
		)
	}
	dir, err := materialize(bank, detector, detectorSum)
	if err != nil {
		return nil, err
	}
	return open(ctx, dir, bankSum, detectorSum)
}

var materializeMu sync.Mutex

// materialize writes the embedded bank into a cache directory named by its
// digest, so concurrent processes and repeated starts reuse one verified copy.
// Both files are re-verified on the fast path: a cache copy with one corrupted
// file must be replaced, not trusted.
func materialize(bank, detector []byte, digest string) (string, error) {
	materializeMu.Lock()
	defer materializeMu.Unlock()
	root, err := os.UserCacheDir()
	if err != nil {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "new-api-lmfp", digest[:16])
	if cachedMatches(dir, bank, detector) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot prepare the fingerprint cache directory: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, bankFile), bank); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, detectorFile), detector); err != nil {
		return "", err
	}
	if !cachedMatches(dir, bank, detector) {
		return "", fmt.Errorf("%w: the fingerprint cache directory could not be written", ErrDataNotConfigured)
	}
	return dir, nil
}

// cachedMatches reports whether the directory already holds exactly these files.
func cachedMatches(dir string, bank, detector []byte) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	cachedBank, err := os.ReadFile(filepath.Join(dir, bankFile))
	if err != nil || sha256Hex(cachedBank) != sha256Hex(bank) {
		return false
	}
	cachedDetector, err := os.ReadFile(filepath.Join(dir, detectorFile))
	if err != nil || sha256Hex(cachedDetector) != sha256Hex(detector) {
		return false
	}
	return true
}

// writeAtomic publishes a complete file or nothing, so a concurrent reader never
// sees a half-written reference bank.
func writeAtomic(path string, content []byte) error {
	staging, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("cannot stage %s: %w", path, err)
	}
	stagingPath := staging.Name()
	if _, err := staging.Write(content); err != nil {
		staging.Close()
		os.Remove(stagingPath)
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := staging.Close(); err != nil {
		os.Remove(stagingPath)
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := os.Chmod(stagingPath, 0o644); err != nil {
		os.Remove(stagingPath)
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := os.Rename(stagingPath, path); err != nil {
		os.Remove(stagingPath)
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

func open(ctx context.Context, dir, bankSum, detectorSum string) (*Detector, error) {
	loaded, err := lmfpd.Load(ctx, lmfpd.LoadOptions{Sources: []string{dir}})
	if err != nil {
		return nil, err
	}
	return &Detector{detector: loaded, bankSHA256: bankSum, detectorSHA256: detectorSum}, nil
}

// Version reports the reference bank build the detector was trained against.
func (d *Detector) Version() string {
	if d == nil || d.detector == nil {
		return ""
	}
	return d.detector.BuiltAt()
}

// Checksums reports the verified file digests, so a result names the exact
// reference bank that produced it.
func (d *Detector) Checksums() (bank, detector string) {
	if d == nil {
		return "", ""
	}
	return d.bankSHA256, d.detectorSHA256
}

// Challenge is one prompt asking the model for a run of random integers.
type Challenge struct {
	ID            string
	Prompt        string
	ExpectedCount int
}

// Challenges returns the prompts for one run, 1 to 3.
func Challenges(count int) ([]Challenge, error) {
	generated, err := lmfpd.GenerateChallenges(count)
	if err != nil {
		return nil, err
	}
	challenges := make([]Challenge, len(generated))
	for i, challenge := range generated {
		challenges[i] = Challenge{
			ID:            challenge.ID,
			Prompt:        challenge.Prompt,
			ExpectedCount: challenge.ExpectedCount,
		}
	}
	return challenges, nil
}

// Reply is one collected answer and the number count its challenge asked for.
type Reply struct {
	Text          string
	ExpectedCount int
}

// Analysis is the scoring outcome.
type Analysis struct {
	Decision       string
	Prediction     string
	PredictionName string
	Family         string
	FamilyName     string
	Results        []Candidate
	Probability    *float64
	UsedOutputs    int
	Reason         string
	BankVersion    string
	BankSHA256     string
	DetectorSHA256 string
}

// Candidate is one ranked model from the reference bank.
type Candidate struct {
	Model       string
	DisplayName string
	Family      string
	FamilyName  string
	Score       float64
	Probability *float64
}

// Analyze scores the collected replies. An insufficient or implausible reply
// set yields an analysis with no ranking and a reason instead of an error.
func (d *Detector) Analyze(replies []Reply) (*Analysis, error) {
	if d == nil || d.detector == nil {
		return nil, ErrDataNotConfigured
	}
	outputs := make([]lmfpd.Output, len(replies))
	for i, reply := range replies {
		outputs[i] = lmfpd.Output{Text: reply.Text, ExpectedCount: reply.ExpectedCount}
	}
	analysis, err := d.detector.Analyze(outputs, false)
	if err != nil {
		return nil, err
	}
	bankSum, detectorSum := d.Checksums()
	result := &Analysis{
		Decision:       analysis.Decision,
		Prediction:     analysis.Prediction,
		PredictionName: analysis.PredictionName,
		Family:         analysis.FamilyPrediction,
		FamilyName:     analysis.FamilyPredictionName,
		Probability:    analysis.Probability,
		UsedOutputs:    analysis.UsedOutputs,
		Reason:         analysis.Evidence.Reason,
		BankVersion:    d.Version(),
		BankSHA256:     bankSum,
		DetectorSHA256: detectorSum,
	}
	for _, candidate := range analysis.Results {
		result.Results = append(result.Results, Candidate{
			Model:       candidate.Model,
			DisplayName: candidate.DisplayName,
			Family:      candidate.Family,
			FamilyName:  candidate.FamilyName,
			Score:       candidate.Score,
			Probability: candidate.Probability,
		})
	}
	return result, nil
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func fileSHA256(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(content), nil
}
