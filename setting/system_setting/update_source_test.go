package system_setting

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func isolateUpdateSourceRepository(t *testing.T) {
	t.Helper()
	originalSetting := updateSourceSetting
	originalBuildDefault := UpdateSourceBuildDefaultRepository
	t.Cleanup(func() {
		updateSourceSetting = originalSetting
		UpdateSourceBuildDefaultRepository = originalBuildDefault
	})
	updateSourceSetting.Repository = ""
	UpdateSourceBuildDefaultRepository = ""
	t.Setenv(UpdateSourceRepositoryEnv, "")
}

func TestUpdateSourceRepositoryDefaultsToUpstream(t *testing.T) {
	isolateUpdateSourceRepository(t)

	assert.Equal(t, UpdateSourceDefaultRepository, UpdateSourceRepository())
	assert.Equal(t, "QuantumNous/new-api", UpdateSourceDefaultRepository)
}

func TestUpdateSourceRepositoryFollowsStoredFork(t *testing.T) {
	isolateUpdateSourceRepository(t)
	t.Setenv(UpdateSourceRepositoryEnv, "other-owner/other-repo")
	updateSourceSetting.Repository = "azurekiln2333/new-api"

	assert.Equal(t, "azurekiln2333/new-api", UpdateSourceRepository())
}

func TestValidateUpdateSourceRepository(t *testing.T) {
	for _, valid := range []string{
		"",
		"   ",
		"QuantumNous/new-api",
		"azurekiln2333/new-api",
		"owner_name/repo.name",
		" owner/repo ",
	} {
		assert.NoError(t, ValidateUpdateSourceRepository(valid), valid)
	}

	for _, invalid := range []string{
		"https://github.com/QuantumNous/new-api",
		"QuantumNous",
		"QuantumNous/new-api/extra",
		"QuantumNous new-api",
		"user:pass@owner/repo",
		"owner/repo?x=1",
		"owner/..",
		"./owner/repo",
		"./repo",
		"../repo",
		"owner/.",
		"owner./repo",
		"owner/repo.",
		"owner/" + strings.Repeat("x", 135),
	} {
		assert.ErrorIs(t, ValidateUpdateSourceRepository(invalid), ErrUpdateSourceRepositoryInvalid, invalid)
	}
}

// Clearing the stored override follows the startup default, which remains the
// upstream release channel when no environment or build default is configured.
func TestUpdateSourceOptionClearedFallsBackToUpstream(t *testing.T) {
	isolateUpdateSourceRepository(t)
	updateSourceSetting.Repository = "azurekiln2333/new-api"

	require.NoError(t, ValidateUpdateSourceRepository(""))
	updateSourceSetting.Repository = ""
	assert.Equal(t, UpdateSourceDefaultRepository, UpdateSourceRepository())
}

func TestUpdateSourceRepositoryEnvironmentFallback(t *testing.T) {
	isolateUpdateSourceRepository(t)

	for _, tc := range []struct {
		name       string
		repository string
		want       string
	}{
		{name: "configured fork", repository: " azurekiln2333/new-api ", want: "azurekiln2333/new-api"},
		{name: "blank", repository: " ", want: UpdateSourceDefaultRepository},
		{name: "invalid URL", repository: "https://github.com/owner/repo", want: UpdateSourceDefaultRepository},
		{name: "dot segment", repository: "owner/..", want: UpdateSourceDefaultRepository},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(UpdateSourceRepositoryEnv, tc.repository)
			assert.Equal(t, tc.want, UpdateSourceRepository())
		})
	}
}

func TestUpdateSourceRepositoryBuildDefaultFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stored       string
		environment  string
		buildDefault string
		want         string
	}{
		{name: "empty build default", want: UpdateSourceDefaultRepository},
		{name: "blank build default", buildDefault: " \t ", want: UpdateSourceDefaultRepository},
		{name: "fork build default", buildDefault: "azurekiln2333/new-api", want: "azurekiln2333/new-api"},
		{name: "trimmed build default", buildDefault: " azurekiln2333/new-api ", want: "azurekiln2333/new-api"},
		{name: "invalid build URL", buildDefault: "https://github.com/owner/repo", want: UpdateSourceDefaultRepository},
		{name: "invalid build credentials", buildDefault: "user:pass@owner/repo", want: UpdateSourceDefaultRepository},
		{name: "invalid build dot segment", buildDefault: "owner/..", want: UpdateSourceDefaultRepository},
		{name: "invalid build trailing dot", buildDefault: "owner/repo.", want: UpdateSourceDefaultRepository},
		{name: "invalid build length", buildDefault: "owner/" + strings.Repeat("x", 135), want: UpdateSourceDefaultRepository},
		{name: "stored wins", stored: " operator/stored ", environment: "operator/environment", buildDefault: "azurekiln2333/new-api", want: "operator/stored"},
		{name: "environment wins", environment: " operator/environment ", buildDefault: "azurekiln2333/new-api", want: "operator/environment"},
		{name: "explicit upstream stored wins", stored: UpdateSourceDefaultRepository, environment: "operator/environment", buildDefault: "azurekiln2333/new-api", want: UpdateSourceDefaultRepository},
		{name: "explicit upstream environment wins", environment: UpdateSourceDefaultRepository, buildDefault: "azurekiln2333/new-api", want: UpdateSourceDefaultRepository},
		{name: "blank stored and environment use build", stored: " \t ", environment: " \t ", buildDefault: "azurekiln2333/new-api", want: "azurekiln2333/new-api"},
		{name: "invalid environment uses build", environment: "https://github.com/owner/repo", buildDefault: "azurekiln2333/new-api", want: "azurekiln2333/new-api"},
		{name: "invalid defaults use upstream", environment: "owner/..", buildDefault: "owner/repo.", want: UpdateSourceDefaultRepository},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateUpdateSourceRepository(t)
			updateSourceSetting.Repository = tc.stored
			UpdateSourceBuildDefaultRepository = tc.buildDefault
			t.Setenv(UpdateSourceRepositoryEnv, tc.environment)

			assert.Equal(t, tc.want, UpdateSourceRepository())
			assert.Equal(t, tc.stored, updateSourceSetting.Repository, "resolving a default must not overwrite the stored option")
			assert.Equal(t, tc.buildDefault, UpdateSourceBuildDefaultRepository, "resolving must not rewrite the build default")
		})
	}
}

func TestUpdateSourceRepositoryLinkedBuildDefault(t *testing.T) {
	linkedRepository := UpdateSourceBuildDefaultRepository
	expectedRepository, configured := os.LookupEnv("TEST_UPDATE_SOURCE_BUILD_REPOSITORY")
	if !configured {
		t.Skip("TEST_UPDATE_SOURCE_BUILD_REPOSITORY is not set")
	}
	require.NotEmpty(t, strings.TrimSpace(expectedRepository))
	require.NoError(t, ValidateUpdateSourceRepository(expectedRepository))
	require.Equal(t, expectedRepository, linkedRepository, "the build default must come from the linker's -X flag")

	// Unlike the assignment fixtures, this test must keep the linked value.
	originalSetting := updateSourceSetting
	t.Cleanup(func() { updateSourceSetting = originalSetting })
	t.Setenv(UpdateSourceRepositoryEnv, "")
	updateSourceSetting.Repository = ""

	assert.Equal(t, expectedRepository, UpdateSourceRepository())
	assert.Empty(t, updateSourceSetting.Repository, "resolving the build default must not save an override")
	assert.Equal(t, linkedRepository, UpdateSourceBuildDefaultRepository)
}

func TestUpdateSourceOptionClearedFallsBackToBuildDefault(t *testing.T) {
	isolateUpdateSourceRepository(t)
	UpdateSourceBuildDefaultRepository = "azurekiln2333/new-api"
	updateSourceSetting.Repository = "operator/stored"
	require.Equal(t, "operator/stored", UpdateSourceRepository())
	updateSourceSetting.Repository = ""

	assert.Equal(t, "azurekiln2333/new-api", UpdateSourceRepository())
	assert.Empty(t, updateSourceSetting.Repository)
}
