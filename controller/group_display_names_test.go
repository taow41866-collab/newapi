package controller

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGroupDisplayNames(t *testing.T) {
	t.Helper()
	groups := ratio_setting.GroupRatio2JSONString()
	names := setting.GroupDisplayNames2JSONString()
	usable := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
		require.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(names))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usable))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"pro-x20":20,"private":4}`))
	require.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(`{}`))
}

func TestGroupDisplayNamesValidationPreservesRoutingIdentity(t *testing.T) {
	setupGroupDisplayNames(t)
	for _, value := range []string{
		`{"default":"默认","pro-x20":"高级组"}`,
		`{"pro-x20":"pro-x20"}`,
		`{"pro-x20":"  高级组  "}`,
		`{"pro-x20":"` + strings.Repeat("组", 64) + `"}`,
		`{}`,
	} {
		require.NoError(t, setting.ValidateGroupDisplayNames(value), value)
	}
	for _, value := range []string{
		`null`, `[]`, `{"pro-x20":1}`, `{"missing":"新组"}`,
		`{"pro-x20":""}`, `{"pro-x20":"  "}`,
		`{"pro-x20":"高级\n组"}`, `{"pro-x20":"高级\u007f组"}`,
		`{"pro-x20":"` + strings.Repeat("组", 65) + `"}`,
		`{"default":"高级组","pro-x20":"高级组"}`,
		`{"default":"高级组 ","pro-x20":" 高级组"}`,
		`{"pro-x20":"private"}`,
	} {
		assert.Error(t, setting.ValidateGroupDisplayNames(value), value)
	}
	require.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(`{"pro-x20":"  高级组  "}`))
	assert.Equal(t, "高级组", setting.GetGroupDisplayNamesCopy()["pro-x20"])
	before := ratio_setting.GetGroupRatioCopy()
	require.Error(t, setting.UpdateGroupDisplayNamesByJSONString(`{"pro-x20":`))
	assert.Equal(t, map[string]string{"pro-x20": "高级组"}, setting.GetGroupDisplayNamesCopy())
	assert.Equal(t, before, ratio_setting.GetGroupRatioCopy())
	assert.Equal(t, float64(20), service.GetUserGroupRatio("default", "pro-x20"))

	copyNames := setting.GetGroupDisplayNamesCopy()
	copyNames["pro-x20"] = "外部修改"
	assert.Equal(t, "高级组", setting.GetGroupDisplayNamesCopy()["pro-x20"])
	assert.JSONEq(t, `{"pro-x20":"高级组"}`, setting.GroupDisplayNames2JSONString())
	candidate := ratio_setting.GetGroupRatioCopy()
	candidate["高级组"] = 1
	assert.Error(t, setting.ValidateGroupDisplayNamesForGroups(setting.GroupDisplayNames2JSONString(), candidate))
	require.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(`{}`))
	assert.JSONEq(t, `{}`, setting.GroupDisplayNames2JSONString())
}

func TestGetGroupDisplayNamesFiltersCurrentUserPermissions(t *testing.T) {
	setupGroupDisplayNames(t)
	initModelListColumnNames(t)
	admin, _ := setupAccessTokenAudit(t)
	user := model.User{Username: "group-display-user", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1, AffCode: "group-display"}
	require.NoError(t, model.DB.Create(&user).Error)
	_, userToken := createAccessTokenTestSession(t, user.Id, "group-display-user-session")
	_, adminToken := createAccessTokenTestSession(t, admin.Id, "group-display-admin-session")
	profileToken, _ := createScopedAccessToken(t, user.Id, 0, "profile:read")
	usageToken, _ := createScopedAccessToken(t, user.Id, 0, "usage:read")
	engine := gin.New()
	engine.GET("/api/group/display-names", middleware.UserAuth(), GetGroupDisplayNames)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"pro-x20":"可用","removed":"旧组"}`))
	require.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(`{"default":"默认","pro-x20":"高级","private":"隐藏"}`))
	// Removed groups keep their display names for historical views, while new
	// relay requests must continue to use the current valid group IDs.
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"pro-x20":20}`))
	for _, tc := range []struct {
		token string
		want  map[string]string
	}{
		{userToken, map[string]string{"default": "默认", "pro-x20": "高级"}},
		{profileToken, map[string]string{"default": "默认", "pro-x20": "高级"}},
		{adminToken, map[string]string{"default": "默认", "pro-x20": "高级", "private": "隐藏"}},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/group/display-names", nil)
		request.Header.Set("Authorization", "Bearer "+tc.token)
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Success bool              `json:"success"`
			Data    map[string]string `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Success)
		assert.Equal(t, tc.want, response.Data)
	}
	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodGet, "/api/group/display-names", nil)
	deniedRequest.Header.Set("Authorization", "Bearer "+usageToken)
	engine.ServeHTTP(denied, deniedRequest)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.NotContains(t, denied.Body.String(), "高级")
	for _, token := range []string{"", "invalid-session-token"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/group/display-names", nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		assert.NotContains(t, recorder.Body.String(), "高级")
		assert.NotContains(t, recorder.Body.String(), "隐藏")
	}
	// A failed permission lookup must not return the unfiltered name map.
	require.NoError(t, model.DB.Migrator().DropTable(&model.User{}))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", user.Id)
	ctx.Set("role", common.RoleCommonUser)
	GetGroupDisplayNames(ctx)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "隐藏")
}

func TestGroupDisplayNamesConcurrentReadAndUpdate(t *testing.T) {
	setupGroupDisplayNames(t)
	var workers sync.WaitGroup
	workers.Go(func() {
		assert.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(`{"pro-x20":"高级"}`))
	})
	workers.Go(func() {
		_ = setting.GetGroupDisplayNamesCopy()
		_ = setting.GroupDisplayNames2JSONString()
	})
	workers.Wait()
	assert.Equal(t, map[string]string{"pro-x20": "高级"}, setting.GetGroupDisplayNamesCopy())
}

func TestUpdateGroupSettingsAtomicityAndRootPermission(t *testing.T) {
	common.OptionMapRWMutex.RLock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	previousTopup := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(previousTopup))
	})
	setupGroupDisplayNames(t)
	initModelListColumnNames(t)
	admin, _ := setupAccessTokenAudit(t)
	root := model.User{Username: "group-settings-root", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleRootUser, AuthVersion: 1, AffCode: "group-settings-root"}
	user := model.User{Username: "group-settings-user", Group: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1, AffCode: "group-settings-user"}
	require.NoError(t, model.DB.Create(&root).Error)
	require.NoError(t, model.DB.Create(&user).Error)
	_, rootToken := createAccessTokenTestSession(t, root.Id, "group-settings-root-session")
	_, adminToken := createAccessTokenTestSession(t, admin.Id, "group-settings-admin-session")
	_, userToken := createAccessTokenTestSession(t, user.Id, "group-settings-user-session")
	rootWriteToken, _ := createScopedAccessToken(t, root.Id, 0, "option:write")
	rootReadToken, _ := createScopedAccessToken(t, root.Id, 0, "option:read")
	engine := gin.New()
	engine.PUT("/api/group/settings", middleware.RootAuth(), UpdateGroupSettings)
	request := func(token, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/group/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	valid := map[string]string{
		"GroupRatio":        `{"default":1,"pro-x20":20,"private":4}`,
		"UserUsableGroups":  `{"default":"默认","pro-x20":"高级"}`,
		"TopupGroupRatio":   `{"default":1,"pro-x20":2}`,
		"GroupDisplayNames": `{"pro-x20":"高级组"}`,
	}
	encoded, err := common.Marshal(gin.H{"values": valid})
	require.NoError(t, err)
	for _, tc := range []struct {
		token string
		code  int
	}{
		{"", http.StatusUnauthorized},
		{"invalid-session-token", http.StatusUnauthorized},
		{userToken, http.StatusForbidden},
		{adminToken, http.StatusForbidden},
		{rootReadToken, http.StatusForbidden},
	} {
		assert.Equal(t, tc.code, request(tc.token, string(encoded)).Code)
	}
	response := request(rootToken, string(encoded))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":true`)
	response = request(rootWriteToken, string(encoded))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":true`)
	for key, value := range valid {
		var stored model.Option
		require.NoError(t, model.DB.Where(&model.Option{Key: key}).First(&stored).Error)
		assert.JSONEq(t, value, stored.Value)
	}
	assert.Equal(t, "高级组", setting.GetGroupDisplayNamesCopy()["pro-x20"])
	beforeGroups := ratio_setting.GetGroupRatioCopy()
	beforeNames := setting.GetGroupDisplayNamesCopy()
	beforeUsable := setting.UserUsableGroups2JSONString()
	beforeTopup := common.TopupGroupRatio2JSONString()
	for _, body := range []string{
		`{}`, `{"values":{}}`, `{"values":null}`, `{"values":{"GroupRatio":1}}`,
		`{"values":{"GroupRatio":"{\"default\":9}","Notice":"not allowed"}}`,
		`{"values":{"group_ratio_setting.group_ratio":"{}"}}`,
		`{"values":{"GroupRatio":"{\"default\":1,\"pro-x20\":99,\"private\":4}","UserUsableGroups":"{}","TopupGroupRatio":"{}","GroupDisplayNames":"{\"pro-x20\":\"\"}"}}`,
		`{"values":{"GroupRatio":"{\"default\":1,\"pro-x20\":99,\"private\":4}","UserUsableGroups":"null"}}`,
		`{"values":{"GroupRatio":"{\"default\":1,\"pro-x20\":99,\"private\":4}","TopupGroupRatio":"{\"pro-x20\":-1}"}}`,
	} {
		response := request(rootToken, body)
		assert.Contains(t, response.Body.String(), `"success":false`, body)
		assert.Equal(t, beforeGroups, ratio_setting.GetGroupRatioCopy(), body)
		assert.Equal(t, beforeNames, setting.GetGroupDisplayNamesCopy(), body)
		assert.Equal(t, beforeUsable, setting.UserUsableGroups2JSONString(), body)
		assert.Equal(t, beforeTopup, common.TopupGroupRatio2JSONString(), body)
		for key, value := range valid {
			var stored model.Option
			require.NoError(t, model.DB.Where(&model.Option{Key: key}).First(&stored).Error)
			assert.JSONEq(t, value, stored.Value, body)
		}
	}
}
