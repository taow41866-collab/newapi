package helper

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelMappedHelperRejectsSubscriptionV1RouteDrift(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		channelID  int
		modelMap   string
	}{
		{name: "mapped to a different upstream model", channelID: 24, modelMap: `{"deepseek-v4.1-flash":"other-model"}`},
		{name: "mapped through a different channel", channelID: 25},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("model_mapping", test.modelMap)
			info := &relaycommon.RelayInfo{
				SubscriptionV1Billing: true,
				OriginModelName:       "deepseek-v4.1-flash",
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelId:         test.channelID,
					UpstreamModelName: "deepseek-v4.1-flash",
				},
			}
			err := ModelMappedHelper(ctx, info, &dto.GeneralOpenAIRequest{Model: "deepseek-v4.1-flash"})
			require.Error(t, err)
		})
	}
}
