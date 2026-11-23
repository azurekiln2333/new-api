package middleware

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// RequestLogDiagnostics runs after relay authentication and before any body
// preparation. Rejected unread bodies stay unread; only their capture status is
// logged. It freezes the original prompt fields before plugins or parameter
// overrides rewrite the request, and guarantees one generic error log for a
// final authenticated rejection that no other relay path recorded.
func RequestLogDiagnostics() gin.HandlerFunc {
	return func(c *gin.Context) {
		if (!common.LogConsumeEnabled && !constant.ErrorLogEnabled) || !common.BeginRequestContentCapture(c) {
			c.Next()
			return
		}
		defer model.TrackRequestContentWrites(c)()
		c.Next()
		if c.Writer.Status() < http.StatusBadRequest || !constant.ErrorLogEnabled {
			return
		}
		// An upstream attempt error or a final rejection already wrote its log;
		// the attempt/error switches also decide whether anything is recorded.
		if c.GetBool("request_error_log_written") || c.GetBool("request_error_log_excluded") {
			return
		}
		status := c.Writer.Status()
		err := types.NewErrorWithStatusCode(errors.New(http.StatusText(status)), types.ErrorCodeInvalidRequest, status)
		service.RecordRelayErrorLog(c, c.GetInt("channel_id"), err, nil)
	}
}
