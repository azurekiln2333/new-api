package system_setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateSourceRepositoryDefaultsToUpstream(t *testing.T) {
	original := updateSourceSetting.Repository
	t.Cleanup(func() { updateSourceSetting.Repository = original })
	t.Setenv(UpdateSourceRepositoryEnv, "")
	updateSourceSetting.Repository = ""

	assert.Equal(t, UpdateSourceDefaultRepository, UpdateSourceRepository())
	assert.Equal(t, "QuantumNous/new-api", UpdateSourceDefaultRepository)
}

func TestUpdateSourceRepositoryFollowsStoredFork(t *testing.T) {
	original := updateSourceSetting.Repository
	t.Cleanup(func() { updateSourceSetting.Repository = original })
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
// upstream release channel when no environment override is configured.
func TestUpdateSourceOptionClearedFallsBackToUpstream(t *testing.T) {
	original := updateSourceSetting.Repository
	t.Cleanup(func() { updateSourceSetting.Repository = original })
	t.Setenv(UpdateSourceRepositoryEnv, "")
	updateSourceSetting.Repository = "azurekiln2333/new-api"

	require.NoError(t, ValidateUpdateSourceRepository(""))
	updateSourceSetting.Repository = ""
	assert.Equal(t, UpdateSourceDefaultRepository, UpdateSourceRepository())
}

func TestUpdateSourceRepositoryEnvironmentFallback(t *testing.T) {
	original := updateSourceSetting.Repository
	t.Cleanup(func() { updateSourceSetting.Repository = original })
	updateSourceSetting.Repository = ""

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
