package controller

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelroute"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Read-only, behind the existing administrator + channel.read route guards.
func GetModelRouting(c *gin.Context) {
	stream, err := strconv.ParseBool(c.DefaultQuery("stream", "true"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid stream"})
		return
	}
	s := modelroute.Scope{Group: c.Query("group"), Model: c.Query("model"), Endpoint: c.DefaultQuery("endpoint", "/v1/responses"), Stream: stream, Effort: c.Query("effort")}
	if !modelroute.Default.Enabled(s) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"config": modelroute.Default.Config(), "scope_enabled": false}})
		return
	}
	channels, err := model.ModelRoutingCandidates(s.Group, s.Model, []dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: s.Endpoint}})
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "candidate lookup unavailable"})
		return
	}
	targets := make([]modelroute.Target, len(channels))
	for i, ch := range channels {
		targets[i] = model.ModelRoutingTarget(ch, s.Model)
	}
	_, states, err := modelroute.Default.Select(c.Request.Context(), s, targets, 0.5)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "shared routing state unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"config": modelroute.Default.Config(), "scope_enabled": true, "candidates": states}})
}

// The existing 15-minute scheduler and worker limit remain the owner. At most
// four scopes per channel per run (2 degraded / 1 recent / 1 rotating).
// Managed channels never pass through legacy whole-channel weight mutation.
func testModelRoutingHealth(ctx context.Context, ch *model.Channel, userID int) (channelTestSummary, bool) {
	var summary channelTestSummary
	if modelroute.Default.Config().Mode != "active" || ch.Status != common.ChannelStatusEnabled {
		return summary, false
	}
	groups := strings.Split(ch.Group, ",")
	models := ch.GetModels()
	scopes, err := modelroute.Default.KnownScopes(ctx, ch.Id)
	if err != nil {
		common.SysError("model routing probes unavailable: " + err.Error())
		return summary, modelRoutingChannelManaged(ch)
	}
	// Cold models get the channel's configured test protocol; real request
	// scopes subsequently provide exact endpoint/effort coverage.
	endpoint := "/v1/chat/completions"
	if probeEndpointForChannel(ch) == string(constant.EndpointTypeOpenAIResponse) {
		endpoint = "/v1/responses"
	}
	for _, group := range groups {
		for _, name := range models {
			s := modelroute.Scope{Group: group, Model: name, Endpoint: endpoint, Stream: true}
			if !modelroute.Default.Enabled(s) {
				continue
			}
			found := false
			for _, known := range scopes {
				if known.Group == group && known.Model == name {
					found = true
					break
				}
			}
			if !found {
				scopes = append(scopes, s)
			}
		}
	}
	scopes = slices.DeleteFunc(scopes, func(s modelroute.Scope) bool {
		return !slices.Contains(groups, s.Group) || !slices.Contains(models, s.Model) || !modelroute.Default.Enabled(s)
	})
	if len(scopes) == 0 {
		return summary, false
	}
	candidates := make([]modelroute.ProbeCandidate, 0, len(scopes))
	for _, scope := range scopes {
		candidates = append(candidates, modelroute.ProbeCandidate{Scope: scope, Target: model.ModelRoutingTarget(ch, scope.Model)})
	}
	selected, err := modelroute.Default.PlanProbes(ctx, candidates)
	if err != nil {
		common.SysError("model probe planning unavailable: " + err.Error())
		return summary, true
	}
	for _, candidate := range selected {
		if ctx.Err() != nil {
			break
		}
		s := candidate.Scope
		lease, err := modelroute.Default.Begin(ctx, s, candidate.Target, true)
		if err != nil {
			continue
		}
		endpointType := string(constant.EndpointTypeOpenAI)
		if s.Endpoint == "/v1/responses" {
			endpointType = string(constant.EndpointTypeOpenAIResponse)
		} else if s.Endpoint == "/v1/messages" {
			endpointType = string(constant.EndpointTypeAnthropic)
		}
		result := testChannel(ctx, ch, userID, s.Model, endpointType, s.Stream, s)
		o := modelroute.Observation{Outcome: modelroute.Ignored}
		if result.routingInfo != nil {
			o.Outcome = service.ModelRoutingOutcome(ctx, result.routingInfo, result.newAPIError)
			if o.Outcome == modelroute.Success && result.localErr != nil {
				o.Outcome = modelroute.Ignored
			}
			if s.Stream && result.routingInfo.RoutingFirstContentMS.Load() == 0 && o.Outcome == modelroute.Success {
				o.Outcome = modelroute.Ignored
			}
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err = modelroute.Default.Finish(finishCtx, lease, o)
		cancel()
		if err != nil {
			common.SysError("model probe result unavailable: " + err.Error())
		}
		summary.Tested++
		if o.Outcome == modelroute.Success {
			summary.Succeeded++
		} else {
			summary.Failed++
		}
	}
	return summary, true
}

func modelRoutingChannelManaged(ch *model.Channel) bool {
	for _, g := range strings.Split(ch.Group, ",") {
		for _, m := range ch.GetModels() {
			if modelroute.Default.Active(modelroute.Scope{Group: g, Model: m, Endpoint: "/v1/responses"}) {
				return true
			}
		}
	}
	return false
}
