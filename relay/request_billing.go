package relay

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// PrepareRequestBilling estimates and reserves one request's charge. Transports
// provide the current request body through BodyStorage or BillingRequestInput;
// channel retries retain the resulting billing session and pricing snapshot.
func PrepareRequestBilling(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if info.OriginModelName == model.SubscriptionV1Model {
		hasSubscription, err := model.HasActiveSubscriptionV1(info.UserId)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeQueryDataError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		}
		if hasSubscription {
			info.SubscriptionV1Billing = true
			info.BillingSource = service.BillingSourceSubscription
			info.SubscriptionV1SelectedChannelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
			info.SubscriptionV1SelectedModel = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
			if info.Request == nil {
				return types.NewErrorWithStatusCode(errors.New("subscription V1 requires a token-countable request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
	}
	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	meta := &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
	if info.Request != nil && (needSensitiveCheck || constant.CountToken || info.SubscriptionV1Billing) {
		meta = info.Request.GetTokenCountMeta()
	} else {
		// Avoid building CombineText when only the pricing quantities are needed.
		switch request := info.Request.(type) {
		case *dto.GeneralOpenAIRequest:
			meta.MaxTokens = int(max(lo.FromPtr(request.MaxTokens), lo.FromPtr(request.MaxCompletionTokens)))
		case *dto.OpenAIResponsesRequest:
			meta.MaxTokens = int(lo.FromPtr(request.MaxOutputTokens))
		case *dto.ClaudeRequest:
			meta.MaxTokens = int(lo.FromPtr(request.MaxTokens))
		case *dto.ImageRequest:
			meta = request.GetTokenCountMeta()
		}
	}

	if needSensitiveCheck && meta != nil {
		if contains, words := service.CheckSensitiveText(meta.CombineText); contains {
			service.RequestPolicy(c).AddEvent(service.PolicyEvent{ErrorCode: string(types.ErrorCodeSensitiveWordsDetected), ErrorSource: "local", Decision: service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "global"}, Health: "unchanged"})
			message := fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", "))
			logger.LogWarn(c, message)
			return types.NewError(errors.New(message), types.ErrorCodeSensitiveWordsDetected)
		}
	}

	var tokens int
	var err error
	if info.SubscriptionV1Billing {
		tokens, err = service.CountRequestToken(c, meta, info)
	} else {
		tokens, err = service.EstimateRequestToken(c, meta, info)
	}
	if err != nil {
		return types.NewError(err, types.ErrorCodeCountTokenFailed)
	}
	info.SetEstimatePromptTokens(tokens)

	priceData, err := helper.ModelPriceHelper(c, info, tokens, meta)
	if err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
	}
	if info.OriginModelName == model.SubscriptionV1Model {
		if info.SubscriptionV1Billing {
			if info.GetSubscriptionV1ChannelID() != model.SubscriptionV1ChannelID || info.GetSubscriptionV1UpstreamModel() != model.SubscriptionV1Model {
				return types.NewErrorWithStatusCode(errors.New("subscription service binding mismatch"), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
			}
			outputTokens, reserveErr := subscriptionV1OutputReservation(info.UserId, meta)
			if reserveErr != nil {
				return types.NewErrorWithStatusCode(reserveErr, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			reserved, reserveErr := service.ReserveSubscriptionV1Billing(info, int64(tokens), outputTokens)
			if reserveErr != nil {
				status, code := http.StatusInternalServerError, types.ErrorCodeUpdateDataError
				if errors.Is(reserveErr, model.ErrSubscriptionV1Quota) {
					status, code = http.StatusForbidden, types.ErrorCodeInsufficientUserQuota
				} else if errors.Is(reserveErr, service.ErrSubscriptionV1Replay) || errors.Is(reserveErr, model.ErrSubscriptionV1Conflict) {
					status, code = http.StatusConflict, types.ErrorCodeInvalidRequest
				}
				return types.NewErrorWithStatusCode(reserveErr, code, status, types.ErrOptionWithSkipRetry())
			}
			info.SubscriptionId = reserved.Usage.UserSubscriptionID
			info.SubscriptionV1Billing = true
			return nil
		}
	}
	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", info.OriginModelName))
		return nil
	}
	return service.PreConsumeBilling(c, priceData.QuotaToPreConsume, info)
}

func subscriptionV1OutputReservation(userID int, meta *types.TokenCountMeta) (int64, error) {
	if meta != nil && meta.MaxTokens < 0 {
		return 0, errors.New("max output tokens cannot be negative")
	}
	if meta != nil && meta.MaxTokens > 0 {
		return int64(meta.MaxTokens), nil
	}
	subscriptions, err := model.GetActiveSubscriptionV1Candidates(userID)
	if err != nil {
		return 0, err
	}
	var available int64
	for _, sub := range subscriptions {
		remaining := sub.DailyOutputTokenLimit - sub.DailyOutputTokensUsed
		if remaining > available {
			available = remaining
		}
	}
	if available <= 0 {
		return 0, model.ErrSubscriptionV1Quota
	}
	return available, nil
}

// RefundFailedRequestBilling applies the common final-failure policy after all
// eligible attempts have ended. A settled BillingSession never refunds again.
func RefundFailedRequestBilling(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	apiErr = service.NormalizeViolationFeeError(apiErr)
	if info.SubscriptionV1Billing {
		if !info.SubscriptionV1RequestStarted && info.SubscriptionId > 0 {
			if _, err := service.RefundSubscriptionV1Billing(info); err != nil {
				logger.LogError(c, "error refunding subscription V1 reservation: "+err.Error())
			}
		}
		return apiErr
	} else if info.Billing != nil {
		info.Billing.Refund(c)
	}
	service.ChargeViolationFeeIfNeeded(c, info, apiErr)
	return apiErr
}
