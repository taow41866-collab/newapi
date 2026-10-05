package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func customerDiscountUserID(c *gin.Context) (int, error) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		return 0, errors.New("invalid user id")
	}
	return userID, nil
}

func GetCustomerChannelDiscounts(c *gin.Context) {
	userID, err := customerDiscountUserID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	history, err := model.GetCustomerChannelDiscounts(c.Request.Context(), userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, history)
}

func UpdateCustomerChannelDiscounts(c *gin.Context) {
	userID, err := customerDiscountUserID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	var request struct {
		ExpectedVersion int64                           `json:"expected_version"`
		Rules           []model.CustomerChannelDiscount `json:"rules"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid customer discount rules"})
		return
	}
	history, err := model.AppendCustomerChannelDiscounts(c.Request.Context(), userID, c.GetInt("id"), request.ExpectedVersion, request.Rules)
	if errors.Is(err, model.ErrCustomerDiscountConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, history)
}
