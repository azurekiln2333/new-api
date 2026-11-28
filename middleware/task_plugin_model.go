package middleware

import (
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// resolveTaskPluginModelIdentity applies the same catalog and exact token scope
// as the distributor before any registry-driven spelling rewrite.
func resolveTaskPluginModelIdentity(c *gin.Context, requested string) (string, error) {
	if raw := common.GetContextKeyString(c, constant.ContextKeyRequestedModel); raw != "" {
		requested = raw
	}
	canonical, err := service.ResolveRequestModelIdentity(c, requested)
	if err != nil {
		return "", err
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		limit, _ := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
		if !TokenModelLimitAllows(limit, canonical, common.GetContextKeyString(c, constant.ContextKeyCatalogModel)) {
			return "", errors.New("token is not allowed to use this model")
		}
	}
	return canonical, nil
}

// rewriteTaskPluginJSONModel patches the top-level JSON "model" field and
// replaces BodyStorage. Non-JSON bodies are left untouched.
func rewriteTaskPluginJSONModel(c *gin.Context, spelling string) error {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return nil
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return err
	}
	patched, err := ojson.SetBytes(raw, "model", spelling)
	if err != nil {
		return err
	}
	newStorage, err := common.CreateBodyStorage(patched)
	if err != nil {
		return err
	}
	_ = storage.Close()
	c.Set(common.KeyBodyStorage, newStorage)
	c.Set(common.KeyRequestBody, nil)
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(patched))
	return nil
}
