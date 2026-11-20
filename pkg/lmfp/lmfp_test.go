package lmfp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVendoredBankLoadsOffline(t *testing.T) {
	// A release build must score without any download or operator setup.
	detector, err := Load(context.Background(), Options{})
	require.NoError(t, err)
	require.NotNil(t, detector)
	assert.NotEmpty(t, detector.Version())

	bankSum, detectorSum := detector.Checksums()
	assert.Equal(t, vendoredBankSHA256, bankSum)
	assert.Equal(t, vendoredDetectorSHA256, detectorSum)
}

func TestOperatorBankRequiresBothPins(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, bankFile), []byte(`{"models":[]}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, detectorFile), []byte("detector"), 0o600))
	zero := "0000000000000000000000000000000000000000000000000000000000000000"

	for _, testCase := range []struct {
		name    string
		options Options
	}{
		{name: "no pins", options: Options{Dir: dir}},
		{name: "detector pin only", options: Options{Dir: dir, DetectorSHA256: zero}},
		{name: "bank checksum mismatch", options: Options{Dir: dir, BankSHA256: zero, DetectorSHA256: zero}},
		{name: "missing directory", options: Options{Dir: filepath.Join(dir, "absent"), BankSHA256: zero, DetectorSHA256: zero}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			detector, err := Load(context.Background(), testCase.options)
			require.Error(t, err)
			assert.Nil(t, detector)
		})
	}
}

func TestOperatorBankAcceptsVerifiedFiles(t *testing.T) {
	// The vendored files, supplied through the operator path with their real
	// digests, must load through the same verification.
	dir := t.TempDir()
	bank, err := vendoredBank.ReadFile("data/" + bankFile)
	require.NoError(t, err)
	detector, err := vendoredBank.ReadFile("data/" + detectorFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, bankFile), bank, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, detectorFile), detector, 0o600))

	loaded, err := Load(context.Background(), Options{
		Dir:            dir,
		BankSHA256:     vendoredBankSHA256,
		DetectorSHA256: vendoredDetectorSHA256,
	})
	require.NoError(t, err)
	require.NotNil(t, loaded)
	bankSum, detectorSum := loaded.Checksums()
	assert.Equal(t, vendoredBankSHA256, bankSum)
	assert.Equal(t, vendoredDetectorSHA256, detectorSum)
}

func TestVendoredBankRepairsCorruptedCache(t *testing.T) {
	cacheRoot := t.TempDir()
	// os.UserCacheDir reads LOCALDAPPDATA on Windows and XDG_CACHE_HOME elsewhere.
	t.Setenv("LOCALAPPDATA", cacheRoot)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	first, err := Load(context.Background(), Options{})
	require.NoError(t, err)
	require.NotNil(t, first)

	// The cache copy exists now; corrupt one file so the fast path must reject it.
	cacheDir := filepath.Join(cacheRoot, "new-api-lmfp", vendoredDetectorSHA256[:16])
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, bankFile), []byte(`{"models":[]}`), 0o600))

	second, err := Load(context.Background(), Options{})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.Version(), second.Version())
	bankSum, detectorSum := second.Checksums()
	assert.Equal(t, vendoredBankSHA256, bankSum)
	assert.Equal(t, vendoredDetectorSHA256, detectorSum)
	// The corrupted copy was replaced, not trusted.
	restored, err := os.ReadFile(filepath.Join(cacheDir, bankFile))
	require.NoError(t, err)
	assert.Equal(t, vendoredBankSHA256, sha256Hex(restored))
}

func TestLoadFailsClosedWhenPinnedFilesAreMissing(t *testing.T) {
	detector, err := Load(context.Background(), Options{
		Dir:            t.TempDir(),
		BankSHA256:     vendoredBankSHA256,
		DetectorSHA256: vendoredDetectorSHA256,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDataNotConfigured))
	assert.Nil(t, detector)
}

// knownReplies returns three fixed replies. The numbers alternate between a high
// and a low band, a shape the reference bank scores instead of rejecting as
// program-generated. ExpectedCount is fixed so the sample never depends on the
// challenge generator, and the test asserts the exact decision it produces.
func knownReplies() []Reply {
	const expectedCount = 100
	replies := make([]Reply, 0, 3)
	for round := range 3 {
		nums := make([]string, expectedCount)
		for i := range nums {
			if i%2 == 0 {
				nums[i] = strconv.Itoa(300 - (i+round)%30)
				continue
			}
			nums[i] = strconv.Itoa(5 + (i+round)%20)
		}
		replies = append(replies, Reply{Text: strings.Join(nums, " "), ExpectedCount: expectedCount})
	}
	return replies
}

func TestAnalyzeRanksKnownSample(t *testing.T) {
	detector, err := Load(context.Background(), Options{})
	require.NoError(t, err)

	analysis, err := detector.Analyze(knownReplies())
	require.NoError(t, err)

	// Three full, model-like samples are scored against the bank, so the exact
	// decision and bank are pinned by this fixture.
	assert.Equal(t, "scored", analysis.Decision)
	assert.Equal(t, "qwen3.8-27b", analysis.Prediction)
	assert.NotEmpty(t, analysis.PredictionName)
	assert.NotEmpty(t, analysis.BankVersion)
	assert.Equal(t, 3, analysis.UsedOutputs)
	require.NotEmpty(t, analysis.Results)
	// A ranked result names the exact bank that produced it.
	bankSum, detectorSum := detector.Checksums()
	assert.Equal(t, bankSum, analysis.BankSHA256)
	assert.Equal(t, detectorSum, analysis.DetectorSHA256)
	// The ranking is ordered best first. Confidence is only present when the
	// detector carries a matching calibration file, so it is not required here.
	for i := 1; i < len(analysis.Results); i++ {
		assert.GreaterOrEqual(t, analysis.Results[i-1].Score, analysis.Results[i].Score)
	}
	if analysis.Probability != nil {
		assert.GreaterOrEqual(t, *analysis.Probability, 0.0)
		assert.LessOrEqual(t, *analysis.Probability, 1.0)
	}
}

func TestAnalyzeRejectsProgramLikeReplies(t *testing.T) {
	detector, err := Load(context.Background(), Options{})
	require.NoError(t, err)

	// An ascending run looks program-generated to the bank; the analysis must say
	// so instead of inventing a closest match.
	nums := make([]string, 100)
	for i := range nums {
		nums[i] = strconv.Itoa(i + 1)
	}
	analysis, err := detector.Analyze([]Reply{{Text: strings.Join(nums, " "), ExpectedCount: 100}})
	require.NoError(t, err)

	assert.Equal(t, "not_model", analysis.Decision)
	assert.Empty(t, analysis.Results)
	assert.NotEmpty(t, analysis.Reason)
}
