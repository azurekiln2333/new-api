package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// ResolveRequestModelIdentity freezes one caller-visible identity before token
// authorization. Registry recognition is not a catalog or authorization grant.
// Every attempt still checks the channel's current effective policy.
func ResolveRequestModelIdentity(c *gin.Context, requested string) (string, error) {
	if canonical := common.GetContextKeyString(c, constant.ContextKeyCanonicalModel); canonical != "" {
		raw := common.GetContextKeyString(c, constant.ContextKeyRequestedModel)
		if requested != raw && requested != canonical {
			return "", errors.New("model identity changed during the request")
		}
		return canonical, nil
	}
	common.SetContextKey(c, constant.ContextKeyRequestedModel, requested)
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	groups := []string{group}
	if group == "auto" {
		groups = GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	canonical, catalogModel, err := model.ResolveModelIDWithCatalog(requested, c.GetInt("id"), groups)
	if err != nil {
		return "", err
	}
	if requested != canonical && hasDistinctModelPricingIdentity(requested) {
		return "", errors.New("model ID has separate pricing; use an exact catalog spelling")
	}
	common.SetContextKey(c, constant.ContextKeyCanonicalModel, canonical)
	common.SetContextKey(c, constant.ContextKeyCatalogModel, catalogModel)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, canonical)
	GetChannelConstraints(c).AddFilter(dto.ChannelFilter{
		Kind: dto.FilterModelIdentity, RequestedModel: requested, CanonicalModel: canonical, CatalogModel: catalogModel,
		ModelGroups: append([]string(nil), groups...), ModelUserID: c.GetInt("id"),
	})
	return canonical, nil
}

// A separately named price is not permission to substitute canonical pricing,
// even when its numeric value happens to equal the canonical model's price.
func hasDistinctModelPricingIdentity(name string) bool {
	for _, prices := range []map[string]float64{
		ratio_setting.GetModelRatioCopy(), ratio_setting.GetModelPriceCopy(),
		ratio_setting.GetCompletionRatioCopy(), ratio_setting.GetImageRatioCopy(),
		ratio_setting.GetAudioRatioCopy(), ratio_setting.GetAudioCompletionRatioCopy(),
		ratio_setting.GetCacheRatioCopy(), ratio_setting.GetCreateCacheRatioCopy(),
	} {
		if _, exists := prices[name]; exists {
			return true
		}
	}
	if _, exists := billing_setting.GetBillingModeCopy()[name]; exists {
		return true
	}
	if _, exists := billing_setting.GetBillingExprCopy()[name]; exists {
		return true
	}
	for key := range billing_setting.GetPluginBillingExprCopy() {
		_, modelName, exists := strings.Cut(key, "::")
		if exists && modelName == name {
			return true
		}
	}
	return false
}
