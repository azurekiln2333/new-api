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
// It records the declared choice on info.UpstreamTextEndpoint; an empty
// declaration leaves that empty so adaptors keep their historical behavior.
func resolveTextUpstreamEndpoint(c *gin.Context, info *relaycommon.RelayInfo) (types.RelayFormat, *types.NewAPIError) {
	settings := &info.ChannelSetting
	globalSettings := model_setting.GetGlobalSettings()
	passthrough := globalSettings.PassThroughRequestEnabled || settings.PassThroughBodyEnabled
	canUpgrade := info.RelayFormat == types.RelayFormatClaude ||
		(info.RelayFormat == types.RelayFormatOpenAI && info.RelayMode == relayconstant.RelayModeChatCompletions)
	preferResponses := canUpgrade && service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName)
	if len(settings.SupportedEndpoints) == 0 {
		info.UpstreamTextEndpoint = ""
		if preferResponses && !passthrough {
			return types.RelayFormatOpenAIResponses, nil
		}
		return info.RelayFormat, nil
	}
	if err := common.ValidateChannelSupportedEndpoints(info.ChannelType, settings); err != nil {
		info.UpstreamTextEndpoint = ""
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayChannelEndpointInvalid)), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	chatSupported := slices.Contains(settings.SupportedEndpoints, dto.ChannelEndpointChat)
	responsesSupported := slices.Contains(settings.SupportedEndpoints, dto.ChannelEndpointResponses)
	messagesSupported := slices.Contains(settings.SupportedEndpoints, dto.ChannelEndpointMessages)
	// messages already passed ValidateChannelSupportedEndpoints, which restricts
	// the declaration to the OpenAI channel type, so no second reachability test
	// is needed here.

	target := info.RelayFormat
	switch info.RelayFormat {
	case types.RelayFormatOpenAIResponses:
		if !responsesSupported && !messagesSupported {
			target = ""
		} else if !responsesSupported {
			target = types.RelayFormatClaude
		}
	case types.RelayFormatOpenAIResponsesCompaction:
		// Compaction is a Responses-only operation. A Messages upstream has no
		// equivalent, so only a declared Responses endpoint can serve it.
		if !responsesSupported {
			target = ""
		}
	case types.RelayFormatOpenAI, types.RelayFormatClaude:
		if info.RelayFormat == types.RelayFormatClaude && messagesSupported {
			// A Claude client speaks the upstream protocol natively; prefer the
			// declared Messages endpoint over any conversion.
			target = types.RelayFormatClaude
			break
		}
		if !canUpgrade {
			target = ""
		} else if messagesSupported && !chatSupported && !responsesSupported {
			// Messages-only: convert the OpenAI chat request into Messages.
			target = types.RelayFormatClaude
		} else if responsesSupported && (!chatSupported || preferResponses && !passthrough) {
			target = types.RelayFormatOpenAIResponses
		} else {
			target = types.RelayFormatOpenAI
		}
	case types.RelayFormatGemini:
		// The ordinary OpenAI adaptor already supports Gemini -> Chat. A
		// Responses-only or Messages-only declaration must not fall through to
		// that Chat path.
		if chatSupported {
			target = types.RelayFormatOpenAI
		} else {
			target = ""
		}
	default:
		target = ""
	}
	if target == "" {
		info.UpstreamTextEndpoint = ""
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayChannelEndpointUnsupported)), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if passthrough && target != info.RelayFormat {
		info.UpstreamTextEndpoint = ""
		return "", types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayChannelEndpointPassthroughConflict)), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if target == types.RelayFormatClaude {
		info.UpstreamTextEndpoint = types.RelayFormatClaude
	} else {
		info.UpstreamTextEndpoint = ""
	}
	return target, nil
}
