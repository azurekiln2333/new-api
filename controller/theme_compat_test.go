package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionRejectsRetiredFrontendTheme(t *testing.T) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/",
		strings.NewReader(`{"key":"theme.frontend","value":"classic"}`),
	)

	UpdateOption(context)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"success":false,"message":"The Classic frontend has been removed. The theme can only be set to default","message_key":"The Classic frontend has been removed. The theme can only be set to default"}`, response.Body.String())
}

func TestGetStatusAdvertisesDefaultDashboard(t *testing.T) {
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousMap })
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

	GetStatus(context)

	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, "default", payload.Data["theme"])
}

func TestGetStatusSidebarPersonalSettingsVisibility(t *testing.T) {
	settings := console_setting.GetConsoleSetting()
	previousSettings := *settings
	previousMap := common.OptionMap
	t.Cleanup(func() {
		*settings = previousSettings
		common.OptionMap = previousMap
	})
	common.OptionMap = map[string]string{}

	tests := []struct {
		name    string
		options map[string]string
		want    bool
	}{
		{
			name: "missing option preserves visible default",
			want: true,
		},
		{
			name: "explicit false hides ordinary user card",
			options: map[string]string{
				"console_setting.sidebar_personal_settings_enabled": "false",
			},
			want: false,
		},
		{
			name: "explicit true restores visible card",
			options: map[string]string{
				"console_setting.sidebar_personal_settings_enabled": "true",
			},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*settings = console_setting.ConsoleSetting{
				SidebarPersonalSettingsEnabled: true,
			}
			require.NoError(t, config.GlobalConfig.LoadFromDB(test.options))
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

			GetStatus(context)

			var payload struct {
				Success bool           `json:"success"`
				Data    map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assert.Equal(t, test.want, payload.Data["sidebar_personal_settings_enabled"])
			assert.NotContains(t, payload.Data, "sidebar_modules")
		})
	}
}
