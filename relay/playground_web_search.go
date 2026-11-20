package relay

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// validatePlaygroundWebSearch checks the actual wire payload after conversion,
// plugins and overrides. It verifies intent survived, not model capability:
// providers still decide whether their configured model supports hosted search.
func validatePlaygroundWebSearch(c *gin.Context, info *relaycommon.RelayInfo, body []byte) *types.NewAPIError {
	if info == nil || !info.IsPlayground {
		return nil
	}
	inbound, ok := info.Request.(*dto.GeneralOpenAIRequest)
	if !ok || inbound.WebSearchOptions == nil {
		return nil
	}
	var wire struct {
		WebSearchOptions *dto.WebSearchOptions `json:"web_search_options"`
		EnableSearch     bool                  `json:"enable_search"`
		WebSearch        struct {
			Enable bool `json:"enable"`
		} `json:"web_search"`
		SearchParameters struct {
			Mode string `json:"mode"`
		} `json:"search_parameters"`
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
		Tools []struct {
			Type      string `json:"type"`
			WebSearch struct {
				Enable *bool `json:"enable"`
			} `json:"web_search"`
			GoogleSearch          map[string]any `json:"googleSearch"`
			GoogleSearchSnake     map[string]any `json:"google_search"`
			GoogleSearchRetrieval map[string]any `json:"googleSearchRetrieval"`
		} `json:"tools"`
	}
	if err := common.Unmarshal(body, &wire); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if wire.WebSearchOptions != nil || wire.EnableSearch || wire.WebSearch.Enable ||
		wire.SearchParameters.Mode == "auto" || wire.SearchParameters.Mode == "on" {
		return nil
	}
	for _, plugin := range wire.Plugins {
		if plugin.ID == "web" {
			return nil
		}
	}
	for _, tool := range wire.Tools {
		if tool.Type == "web_search" || tool.Type == "web_search_preview" || strings.HasPrefix(tool.Type, "web_search_20") {
			if tool.WebSearch.Enable == nil || *tool.WebSearch.Enable {
				return nil
			}
		}
		if tool.GoogleSearch != nil || tool.GoogleSearchSnake != nil || tool.GoogleSearchRetrieval != nil {
			return nil
		}
	}
	return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgPlaygroundWebSearchUnavailable)), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
