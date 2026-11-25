package system_setting

import (
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// UpdateSourceDefaultRepository is the upstream release channel. It stays the
// default so an unconfigured deployment keeps following the project releases;
// the override exists for forks that publish their own tags.
const UpdateSourceDefaultRepository = "QuantumNous/new-api"

// UpdateSourceRepositoryOption is the stored override key ("owner/repo").
const UpdateSourceRepositoryOption = "system_update_setting.repository"

// UpdateSourceRepositoryEnv names a startup default for deployments that never
// open the console.
const UpdateSourceRepositoryEnv = "NEW_API_UPDATE_SOURCE_REPOSITORY"

// ErrUpdateSourceRepositoryInvalid rejects anything that is not a bare
// "owner/repo" pair on github.com.
var ErrUpdateSourceRepositoryInvalid = errors.New("Enter the repository as owner/repo, for example owner/new-api.")

var updateSourceRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type UpdateSourceSetting struct {
	// Repository is "owner/repo" on github.com. An empty value follows the
	// upstream release channel.
	Repository string `json:"repository"`
}

var updateSourceSetting = UpdateSourceSetting{
	Repository: "",
}

func init() {
	config.GlobalConfig.Register("system_update_setting", &updateSourceSetting)
}

// ValidateUpdateSourceRepository accepts only a bare "owner/repo" pair. The
// frontend builds a fixed api.github.com URL from it, so schemes, paths,
// credentials or query strings must never be accepted.
func ValidateUpdateSourceRepository(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > 140 || !updateSourceRepositoryPattern.MatchString(trimmed) {
		return ErrUpdateSourceRepositoryInvalid
	}
	for _, part := range strings.SplitN(trimmed, "/", 2) {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return ErrUpdateSourceRepositoryInvalid
		}
	}
	return nil
}

// UpdateSourceRepository returns the effective release repository. The stored
// setting wins; the environment variable provides a startup default; the
// upstream repository remains the fallback so existing deployments are
// unchanged until an administrator opts in.
func UpdateSourceRepository() string {
	if repository := strings.TrimSpace(updateSourceSetting.Repository); repository != "" {
		return repository
	}
	if repository := strings.TrimSpace(os.Getenv(UpdateSourceRepositoryEnv)); repository != "" {
		if ValidateUpdateSourceRepository(repository) == nil {
			return repository
		}
	}
	return UpdateSourceDefaultRepository
}
