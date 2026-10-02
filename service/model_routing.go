package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelroute"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// Model-local failures are handled by the routing state machine, not a ban of
// every model on the channel. Explicit channel/key errors and invalid credentials
// still follow the existing safety policy.
func ShouldDisableChannelForRequest(c *gin.Context, err *types.NewAPIError) bool {
	if !ShouldDisableChannel(err) {
		return false
	}
	if c == nil || err.StatusCode == 401 || types.IsChannelError(err) {
		return true
	}
	s := ModelRoutingScope(c, common.GetContextKeyString(c, constant.ContextKeyUsingGroup), c.GetString("original_model"))
	return s == nil || !modelroute.Default.Active(*s)
}

// ModelRoutingScope reads only request metadata, never stores prompts or keys.
func ModelRoutingScope(c *gin.Context, group, name string) *modelroute.Scope {
	if c == nil || c.Request == nil || modelroute.Default.Config().Mode == "off" {
		return nil
	}
	path := c.Request.URL.Path
	if path != "/v1/chat/completions" && path != "/v1/responses" && path != "/v1/messages" {
		return nil
	}
	var s modelroute.Scope
	if cached, ok := c.Get("model_routing_scope"); ok {
		s = cached.(modelroute.Scope)
	} else {
		var body struct {
			Stream    bool   `json:"stream"`
			Effort    string `json:"reasoning_effort"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := common.UnmarshalBodyReusable(c, &body); err != nil {
			return nil
		}
		s = modelroute.Scope{Endpoint: path, Stream: body.Stream, Effort: body.Effort}
		if body.Reasoning.Effort != "" {
			s.Effort = body.Reasoning.Effort
		}
		c.Set("model_routing_scope", s)
	}
	s.Group = group
	s.Model = name
	for _, used := range c.GetStringSlice("use_channel") {
		if id, err := strconv.Atoi(used); err == nil {
			s.Excluded = append(s.Excluded, id)
		}
	}
	return &s
}

// BeginModelRoutingAttempt runs after local validation/preparation. The lease
// is for this attempt only; retries never attribute earlier failures to the winner.
func BeginModelRoutingAttempt(c *gin.Context, info *relaycommon.RelayInfo, id int) (modelroute.Lease, time.Time) {
	start := time.Now()
	info.RoutingFirstContentMS = nil
	s := ModelRoutingScope(c, common.GetContextKeyString(c, constant.ContextKeyUsingGroup), info.OriginModelName)
	if s == nil {
		return modelroute.Lease{}, start
	}
	if info.UsingGroup != "" {
		s.Group = info.UsingGroup
	}
	if !modelroute.Default.Enabled(*s) {
		return modelroute.Lease{}, start
	}
	info.RoutingFirstContentMS = new(atomic.Int64)
	channel, err := model.CacheGetChannel(id)
	if err != nil {
		return modelroute.Lease{}, start
	}
	lease, err := modelroute.Default.Begin(c.Request.Context(), *s, model.ModelRoutingTarget(channel, s.Model), false)
	if err != nil {
		common.SysError("model routing observation unavailable: " + err.Error())
	}
	return lease, start
}

// ModelRoutingOutcome shares attribution between real requests and synthetic
// probes. Protocol-level rate limits can arrive inside an HTTP 200 SSE stream.
func ModelRoutingOutcome(ctx context.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) modelroute.Outcome {
	switch perfmetrics.ClassifyRelayOutcome(ctx, info, apiErr) {
	case perfmetrics.OutcomeSuccess:
		return modelroute.Success
	case perfmetrics.OutcomeIgnored:
		return modelroute.Ignored
	}
	isLimit := func(status int, code, kind string) bool {
		if status == 429 {
			return true
		}
		for _, value := range []string{code, kind} {
			switch strings.ToLower(value) {
			case "rate_limit_exceeded", "rate_limit_error":
				return true
			}
		}
		return false
	}
	if info != nil {
		stream := info.StreamStatus.OutcomeSnapshot()
		if isLimit(stream.ErrorStatus, stream.ErrorCode, stream.ErrorType) {
			return modelroute.Throttled
		}
	}
	for apiErr != nil {
		var inner *types.NewAPIError
		if errors.As(apiErr.Unwrap(), &inner) && inner != apiErr {
			apiErr = inner
			continue
		}
		if apiErr.GetErrorType() != types.ErrorTypeNewAPIError && isLimit(apiErr.StatusCode, string(apiErr.GetErrorCode()), apiErr.ToOpenAIError().Type) {
			return modelroute.Throttled
		}
		break
	}
	return modelroute.Failure
}

func FinishModelRoutingAttempt(c *gin.Context, info *relaycommon.RelayInfo, lease modelroute.Lease, start time.Time, apiErr *types.NewAPIError) {
	if lease.ID == "" {
		return
	}
	o := modelroute.Observation{Outcome: modelroute.Ignored, DurationMS: time.Since(start).Milliseconds()}
	o.OutputTokens = info.PerformanceOutputTokens
	first := info.RoutingFirstContentMS.Load()
	if first > 0 {
		o.FirstContentMS = first - start.UnixMilli()
	}
	o.Outcome = ModelRoutingOutcome(c.Request.Context(), info, apiErr)
	// An empty/metadata-only stream is not evidence of recovery or speed.
	if info.IsStream && o.Outcome == modelroute.Success && o.FirstContentMS <= 0 {
		o.Outcome = modelroute.Ignored
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := modelroute.Default.Finish(ctx, lease, o); err != nil {
		common.SysError("model routing result unavailable: " + err.Error())
	}
}
