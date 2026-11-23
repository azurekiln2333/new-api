package common

import (
	"fmt"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ValidateChannelSupportedEndpoints is shared by save-time validation and
// runtime routing, including configurations saved before validation existed.
func ValidateChannelSupportedEndpoints(channelType int, settings *dto.ChannelSettings) error {
	if err := settings.ValidateSupportedEndpoints(); err != nil {
		return err
	}
	if settings == nil || len(settings.SupportedEndpoints) == 0 {
		return nil
	}
	// These channels use the ordinary OpenAI adaptor's Chat/Responses paths.
	// Fixed custom URLs and native multi-protocol gateways have different contracts.
	switch channelType {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeAzure,
		constant.ChannelTypeOpenAIMax, constant.ChannelTypeOpenRouter,
		constant.ChannelTypeXinference:
	default:
		return fmt.Errorf("supported_endpoints is not supported for this channel type")
	}
	// A Messages upstream is an Anthropic-shaped endpoint behind an OpenAI
	// channel. Only the plain OpenAI channel is verified for it.
	for _, endpoint := range settings.SupportedEndpoints {
		if endpoint == dto.ChannelEndpointMessages && channelType != constant.ChannelTypeOpenAI {
			return fmt.Errorf("supported_endpoints messages is only supported for the OpenAI channel type")
		}
	}
	return nil
}
