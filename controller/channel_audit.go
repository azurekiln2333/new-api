package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// channelAuditActor is the immutable actor and request context of one channel
// mutation, captured at handler entry. The transaction, the outbox row and the
// delivery worker never retain *gin.Context, and the username is snapshotted so
// delivery never performs a user lookup on a request path.
type channelAuditActor struct {
	UserId    int
	Username  string
	Role      int
	AuthMethod string
	TokenRef  string
	Ip        string
	UserAgent string
	Method    string
	Route     string
	RequestId string
}

// snapshotChannelAuditActor reads the actor and request context once, before the
// mutation opens its transaction.
func snapshotChannelAuditActor(c *gin.Context) channelAuditActor {
	actor := channelAuditActor{
		UserId:     c.GetInt("id"),
		Username:   c.GetString("username"),
		Role:       c.GetInt("role"),
		AuthMethod: auditAuthMethod(c),
		Ip:         c.ClientIP(),
	}
	if c.Request != nil {
		actor.UserAgent = c.Request.UserAgent()
		actor.Method = c.Request.Method
	}
	actor.RequestId = common.GetContextKeyString(c, common.RequestIdKey)
	actor.Route = c.FullPath()
	if actor.AuthMethod == "access_token" {
		actor.TokenRef = c.GetString("access_token_ref")
	}
	return actor
}

// draft builds the secret-free audit obligation for one action. It is a pure
// value computation over the committed parameters, so it is safe to call inside
// a transaction and produces identical output in tests.
func (actor channelAuditActor) draft(action string, params map[string]any, success bool) model.AuditEventDraft {
	if params == nil {
		params = map[string]any{}
	}
	paramsJson, err := common.Marshal(params)
	if err != nil {
		paramsJson = []byte("{}")
	}
	status := 200
	if !success {
		status = 500
	}
	return model.AuditEventDraft{
		EventId:       common.GetUUID(),
		Category:      "operation",
		Action:        action,
		ActorUserId:   actor.UserId,
		ActorUsername: actor.Username,
		ActorRole:     actor.Role,
		AuthMethod:    actor.AuthMethod,
		TokenRef:      actor.TokenRef,
		Ip:            actor.Ip,
		UserAgent:     actor.UserAgent,
		Method:        actor.Method,
		Route:         actor.Route,
		Status:        status,
		Success:       success,
		RequestId:     actor.RequestId,
		Content:       auditContentEN(action, params),
		ParamsJson:    string(paramsJson),
	}
}
