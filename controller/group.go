package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func UpdateGroupSettings(c *gin.Context) {
	var request struct {
		Values map[string]string `json:"values"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || len(request.Values) == 0 {
		common.ApiErrorMsg(c, "group settings must contain a non-empty values object")
		return
	}
	for key := range request.Values {
		switch key {
		case "GroupRatio", "UserUsableGroups", "TopupGroupRatio", "GroupDisplayNames":
		default:
			common.ApiErrorMsg(c, "unsupported group setting: "+key)
			return
		}
	}
	if err := model.UpdateOptionsBulk(request.Values); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func GetGroupDisplayNames(c *gin.Context) {
	names := setting.GetGroupDisplayNamesCopy()
	if c.GetInt("role") < common.RoleAdminUser {
		userGroup, err := model.GetUserGroup(c.GetInt("id"), true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to load user group"})
			return
		}
		usable := service.GetUserUsableGroups(userGroup)
		for group := range names {
			if _, allowed := usable[group]; !allowed || !ratio_setting.ContainsGroupRatio(group) {
				delete(names, group)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": names})
}

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]any)
	userGroup := ""
	userId := c.GetInt("id")
	userGroup, _ = model.GetUserGroup(userId, false)
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]any{
				"ratio": service.GetUserGroupRatio(userGroup, groupName),
				"desc":  desc,
			}
		}
	}
	if _, ok := userUsableGroups["auto"]; ok {
		usableGroups["auto"] = map[string]any{
			"ratio": "自动",
			"desc":  setting.GetUsableGroupDescription("auto"),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
