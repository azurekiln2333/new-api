package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsInvalidMaxTokenAutoGroups(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("MaxTokenAutoGroups", value))
		})
	}
	require.NoError(t, validateOptionValue("MaxTokenAutoGroups", "999999"))
}

func TestValidateOptionValueRequiresJSONObjectThemeSettings(t *testing.T) {
	// Empty means "no global theme", so it must stay accepted.
	require.NoError(t, validateOptionValue("ThemeSettings", ""))
	require.NoError(t, validateOptionValue("ThemeSettings", "   "))

	for _, value := range []string{
		`{"mode":"dark","preset":"anthropic"}`,
		`{}`,
		`{"radius":"lg"}`,
	} {
		t.Run("accepts "+value, func(t *testing.T) {
			require.NoError(t, validateOptionValue("ThemeSettings", value))
		})
	}

	// Anything that is not a JSON object would reach every user's page load.
	for _, value := range []string{"dark", "[]", `["dark"]`, "null", "42", "{"} {
		t.Run("rejects "+value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("ThemeSettings", value))
		})
	}
}

func TestValidateOptionValueRejectsSystemTaskRetentionBelowOneDay(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("system_task_setting.retention_days", value))
		})
	}
	require.NoError(t, validateOptionValue("system_task_setting.retention_days", "1"))
	require.NoError(t, validateOptionValue("system_task_setting.retention_days", "30"))
}
