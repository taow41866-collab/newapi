package helper

import (
	"errors"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	// map model name
	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		mapped, err := model.ResolveModelMapping(info.OriginModelName, modelMapping)
		if err != nil {
			return err
		}
		info.IsModelMapped = mapped != info.OriginModelName
		if info.IsModelMapped {
			info.UpstreamModelName = mapped
		}
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return validateSubscriptionV1Route(info)
}

func validateSubscriptionV1Route(info *relaycommon.RelayInfo) error {
	if info != nil && info.SubscriptionV1Billing &&
		(info.ChannelId != model.SubscriptionV1ChannelID || info.OriginModelName != model.SubscriptionV1Model ||
			info.UpstreamModelName != model.SubscriptionV1Model) {
		return errors.New("subscription V1 final service binding mismatch")
	}
	return nil
}
