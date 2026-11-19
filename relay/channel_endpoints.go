package relay

import (
	"errors"
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

// resolveTextUpstreamEndpoint runs once per attempt, after channel selection.
// An empty declaration retains the old policy and passthrough behavior.
func resolveTextUpstreamEndpoint(c *gin.Context, info *relaycommon.RelayInfo) (types.RelayFormat, *types.NewAPIError) {
	settings := &info.ChannelSetting
	globalSettings := model_setting.GetGlobalSettings()
	passthrough := globalSettings.PassThroughRequestEnabled || settings.PassThroughBodyEnabled
	canUpgrade := info.RelayFormat == types.RelayFormatClaude ||
		(info.RelayFormat == types.RelayFormatOpenAI && info.RelayMode == relayconstant.RelayModeChatCompletions)
	preferResponses := canUpgrade && service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName)
	if len(settings.SupportedEndpoints) == 0 {
		if preferResponses && !passthrough {
			return types.RelayFormatOpenAIResponses, nil
		}
		return info.RelayFormat, nil
	}
	if err := common.ValidateChannelSupportedEndpoints(info.ChannelType, settings); err != nil {
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, "relay.channel_endpoint_invalid")), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	chatSupported := slices.Contains(settings.SupportedEndpoints, dto.ChannelEndpointChat)
	responsesSupported := slices.Contains(settings.SupportedEndpoints, dto.ChannelEndpointResponses)
	target := info.RelayFormat
	switch info.RelayFormat {
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		if !responsesSupported {
			target = ""
		}
	case types.RelayFormatOpenAI, types.RelayFormatClaude:
		if !canUpgrade {
			target = ""
		} else if responsesSupported && (!chatSupported || preferResponses && !passthrough) {
			target = types.RelayFormatOpenAIResponses
		} else {
			target = types.RelayFormatOpenAI
		}
	case types.RelayFormatGemini:
		// The ordinary OpenAI adaptor already supports Gemini -> Chat. A
		// Responses-only declaration must not fall through to that Chat path.
		if chatSupported {
			target = types.RelayFormatOpenAI
		} else {
			target = ""
		}
	default:
		target = ""
	}
	if target == "" {
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, "relay.channel_endpoint_unsupported")), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if passthrough && target != info.RelayFormat {
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, "relay.channel_endpoint_passthrough_conflict")), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return target, nil
}
