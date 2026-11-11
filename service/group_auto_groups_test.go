package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureRequestAutoGroupsTest(t *testing.T) {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","svip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","svip":"SVIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"svip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func newRequestAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestGetRequestAutoGroupsInheritedListIsNotLimited(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default", "svip"}, groups)
}

func TestGetRequestAutoGroupsFiltersBeforeApplyingCurrentLimit(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"revoked", "vip", "default", "svip"})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default"}, groups)
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	assert.Equal(t, []string{"vip"}, GetRequestAutoGroups(ctx, "default"))
}

func TestGetRequestAutoGroupsDoesNotFallBackAfterPermissionChange(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Empty(t, groups)
}

func TestGroupLevelsUnionAndAuthorization(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	levels := setting.GroupLevels2JSONString()
	t.Cleanup(func() { require.NoError(t, setting.UpdateGroupLevelsByJSONString(levels)) })
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"svip":1,"ultra":1,"ultramax":1,"unranked":1}`))
	require.NoError(t, setting.UpdateGroupLevelsByJSONString(`{"default":0,"vip":1,"svip":2,"ultra":3,"ultramax":4}`))
	ordered := []string{"default", "vip", "svip", "ultra", "ultramax"}
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip","svip","ultra","ultramax"]`))
	for i, group := range ordered {
		visible := GetUserUsableGroups(group)
		assert.Len(t, visible, i+1)
		for j, candidate := range ordered {
			assert.Equal(t, j <= i, IsUserSelectableGroup(group, candidate), "%s -> %s", group, candidate)
		}
		assert.Equal(t, ordered[:i+1], GetUserAutoGroup(group))
	}
	assert.Equal(t, map[string]string{"unranked": "User group"}, GetUserUsableGroups("unranked"))
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"ultramax", "vip", "default"})
	assert.Equal(t, []string{"default"}, GetRequestAutoGroups(ctx, "default"), "forged or stale token groups cannot grant higher-level access")
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"ultramax":"Manual grant"}`))
	assert.True(t, IsUserSelectableGroup("default", "ultramax"), "explicit grants remain a union")
	for _, invalid := range []string{`null`, `{"vip":null}`, `{"vip":1.5}`, `{"auto":1}`, `{"vip":2147483648}`} {
		assert.Error(t, setting.UpdateGroupLevelsByJSONString(invalid))
	}
	assert.Equal(t, 1, setting.GetGroupLevelsCopy()["vip"], "invalid settings leave permissions intact")
}
