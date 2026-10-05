package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetRevenueReport(c *gin.Context) {
	start, e1 := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, e2 := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if e1 != nil || e2 != nil || start < 0 || end < start || end-start > 31*86400 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Select a valid time range of up to 31 days"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	report, err := model.GetRevenue(ctx, start, end, c.Query("username"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}
func GetPurchasePrices(c *gin.Context) {
	rules, err := model.GetPurchasePriceRules(c.Request.Context())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rules)
}
func UpdatePurchasePrices(c *gin.Context) {
	var request struct {
		Rules []model.PurchasePrice `json:"rules"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid purchase price rules"})
		return
	}
	if err := model.ValidatePurchasePrices(request.Rules); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if _, err := model.AppendPurchasePriceRules(c.Request.Context(), request.Rules); err != nil {
		if errors.Is(err, model.ErrPurchasePriceHistoryImmutable) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func AppendPurchasePrice(c *gin.Context) {
	var rule model.PurchasePrice
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	if err := common.DecodeJson(c.Request.Body, &rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid purchase price rule"})
		return
	}
	if err := model.ValidatePurchasePrices([]model.PurchasePrice{rule}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	rules, err := model.AppendPurchasePriceRule(c.Request.Context(), rule)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rules[len(rules)-1])
}
