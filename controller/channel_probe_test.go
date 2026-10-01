package controller

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeOutcomeUsesConsecutiveThresholds(t *testing.T) {
	probeStateByChannel.Lock()
	probeStateByChannel.values = make(map[int]*probeState)
	probeStateByChannel.Unlock()
	settings := &operation_setting.ProbeSetting{
		SchedulingProtectionEnabled: true,
		SchedulingFailureThreshold:  2,
		SchedulingSuccessThreshold:  2,
	}
	assert.False(t, recordProbeOutcome(1, false, settings))
	assert.True(t, recordProbeOutcome(1, false, settings))
	assert.False(t, recordProbeOutcome(1, true, settings))
	assert.True(t, recordProbeOutcome(1, true, settings))
}

func TestProbeLatencyUsesLatestSamples(t *testing.T) {
	probeStateByChannel.Lock()
	probeStateByChannel.values = make(map[int]*probeState)
	probeStateByChannel.Unlock()
	settings := &operation_setting.ProbeSetting{
		FirstTokenProtectionEnabled: true,
		FirstTokenThresholdSeconds:  45,
		FirstTokenMinimumSamples:    2,
	}

	assert.False(t, recordProbeLatency(2, 100_000, settings))
	assert.True(t, recordProbeLatency(2, 100_000, settings))
	assert.True(t, recordProbeLatency(2, 5_000, settings))
	assert.False(t, recordProbeLatency(2, 5_000, settings))
}

func TestProbeModelAndOpenAIReliableEndpoint(t *testing.T) {
	original := *operation_setting.GetProbeSetting()
	t.Cleanup(func() { *operation_setting.GetProbeSetting() = original })
	setting := operation_setting.GetProbeSetting()
	setting.PlatformModels = map[string]string{"openai": "gpt-probe"}
	setting.OpenAIReliableEnabled = true
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	assert.Equal(t, "gpt-probe", probeModelForChannel(channel))
	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), probeEndpointForChannel(channel))

	request, ok := buildTestRequest("gpt-probe", string(constant.EndpointTypeOpenAIResponse), channel, false).(*dto.OpenAIResponsesRequest)
	if assert.True(t, ok) {
		assert.Equal(t, setting.OpenAIReasoningEffort, request.Reasoning.Effort)
	}
}

func TestProbeDoesNotInferProvidersFromGenericChannelTypes(t *testing.T) {
	assert.Empty(t, probePlatformForChannel(constant.ChannelTypeAdvancedCustom))
	assert.Empty(t, probePlatformForChannel(constant.ChannelTypeNewAPI))
}

func TestProbeResponseBodyRecordsFirstBodyByte(t *testing.T) {
	body := &probeResponseBody{
		ReadCloser: io.NopCloser(strings.NewReader("first event")),
		startedAt:  time.Now().Add(-time.Second),
	}

	content, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "first event", string(content))
	assert.GreaterOrEqual(t, body.firstByteMilliseconds, int64(900))
}
