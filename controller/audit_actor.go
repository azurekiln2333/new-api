package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// auditActorSnapshot is the plain value captured at handler entry. It is the only
// audit input that may cross a transaction boundary: no *gin.Context is retained
// by a transaction, an outbox row, or a delivery worker.
type auditActorSnapshot struct {
	UserId     int
	Username   string
	Role       int
	AuthMethod string
	TokenRef   string
	Ip         string
	UserAgent  string
	Method     string
	Route      string
	RequestId  string
}

// snapshotAuditActor copies the authenticated identity out of the request. The
// username is snapshotted so deferred delivery never repeats the user lookup that
// blocks the request critical path.
func snapshotAuditActor(c *gin.Context) auditActorSnapshot {
	snapshot := auditActorSnapshot{
		UserId:     c.GetInt("id"),
		Username:   c.GetString("username"),
		Role:       c.GetInt("role"),
		AuthMethod: auditAuthMethod(c),
		Ip:         c.ClientIP(),
	}
	if c.Request != nil {
		snapshot.UserAgent = c.Request.UserAgent()
		snapshot.Method = c.Request.Method
	}
	snapshot.Route = c.FullPath()
	snapshot.TokenRef = c.GetString("access_token_ref")
	// The request id is only stored under common.RequestIdKey by the middleware;
	// a bare "request_id" lookup would silently lose the correlation.
	snapshot.RequestId = c.GetString(common.RequestIdKey)
	return snapshot
}

// buildAuditDraft renders a secret-free outbox draft. Callers pass the same
// params they previously handed to recordManageAudit.
func buildAuditDraft(snapshot auditActorSnapshot, action string, params map[string]any) model.AuditEventDraft {
	encodedParams := ""
	if len(params) > 0 {
		if encoded, err := common.Marshal(params); err == nil {
			encodedParams = string(encoded)
		}
	}
	return model.AuditEventDraft{
		EventId:       common.NewRequestId(),
		Category:      model.AuditCategoryOperation,
		Action:        action,
		ActorUserId:   snapshot.UserId,
		ActorUsername: snapshot.Username,
		ActorRole:     snapshot.Role,
		AuthMethod:    snapshot.AuthMethod,
		TokenRef:      snapshot.TokenRef,
		Ip:            snapshot.Ip,
		UserAgent:     snapshot.UserAgent,
		Method:        snapshot.Method,
		Route:         snapshot.Route,
		Status:        200,
		Success:       true,
		RequestId:     snapshot.RequestId,
		Content:       auditContentEN(action, params),
		ParamsJson:    encodedParams,
	}
}
