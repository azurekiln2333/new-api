package relay

import (
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// RelayTextDiagnostic applies the selected channel's text endpoint policy. When
// handled is false, the caller continues its normal adaptor request. This entry
// deliberately does not settle quota: the channel test records usage once.
func RelayTextDiagnostic(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request dto.Request) (usage *dto.Usage, handled bool, apiErr *types.NewAPIError) {
	target, apiErr := resolveTextUpstreamEndpoint(c, info)
	if apiErr != nil {
		return nil, true, apiErr
	}
	if target != types.RelayFormatOpenAIResponses || info.RelayFormat == types.RelayFormatOpenAIResponses {
		return nil, false, nil
	}
	usage, apiErr = textRequestViaResponses(c, info, adaptor, request)
	return usage, true, apiErr
}
