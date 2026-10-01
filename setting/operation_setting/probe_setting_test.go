package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProbeSettingOptions(t *testing.T) {
	valid := map[string]string{
		"probe_setting.platform_models":               `{"openai":"gpt-6.1-sol","deepseek":"deepseek-v4.1-flash"}`,
		"probe_setting.openai_reasoning_effort":       "high",
		"probe_setting.scheduling_failure_threshold":  "2",
		"probe_setting.scheduling_success_threshold":  "2",
		"probe_setting.first_token_threshold_seconds": "45",
		"probe_setting.first_token_minimum_samples":   "5",
	}
	require.NoError(t, ValidateProbeSettingOptions(valid))

	tests := []map[string]string{
		{"probe_setting.platform_models": `{"not-a-platform":"model"}`},
		{"probe_setting.openai_reasoning_effort": "extreme"},
		{"probe_setting.scheduling_failure_threshold": "21"},
		{"probe_setting.first_token_threshold_seconds": "4"},
		{"probe_setting.upstream_retry_status_codes": "401,403,429"},
		{"probe_setting.scheduling_failure_threshold": "2junk"},
		{"probe_setting.unknown": "value"},
	}
	for _, values := range tests {
		assert.Error(t, ValidateProbeSettingOptions(values))
	}
}

func TestGetProbeSettingNormalizesValues(t *testing.T) {
	original := probeSetting
	t.Cleanup(func() { probeSetting = original })
	probeSetting = ProbeSetting{
		PlatformModels:             map[string]string{"openai": "  gpt-6.1-sol ", "antigravity": "claude-opus", "empty": ""},
		OpenAIReasoningEffort:      "invalid",
		SchedulingFailureThreshold: 0,
		SchedulingSuccessThreshold: 99,
		FirstTokenThresholdSeconds: 1,
		FirstTokenMinimumSamples:   99,
	}
	setting := GetProbeSetting()
	assert.Equal(t, map[string]string{"openai": "gpt-6.1-sol"}, setting.PlatformModels)
	assert.Equal(t, "high", setting.OpenAIReasoningEffort)
	assert.Equal(t, DefaultProbeFailureThreshold, setting.SchedulingFailureThreshold)
	assert.Equal(t, DefaultProbeSuccessThreshold, setting.SchedulingSuccessThreshold)
	assert.Equal(t, DefaultFirstTokenThresholdSeconds, setting.FirstTokenThresholdSeconds)
	assert.Equal(t, DefaultFirstTokenMinimumSamples, setting.FirstTokenMinimumSamples)
}
