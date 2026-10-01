package operation_setting

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// ProbeSetting controls the optional quality checks that run as part of the
// existing channel health task. Empty platform model values deliberately fall
// back to each channel's configured test model.
type ProbeSetting struct {
	PlatformModels              map[string]string `json:"platform_models"`
	OpenAIReliableEnabled       bool              `json:"openai_reliable_enabled"`
	OpenAIReasoningEffort       string            `json:"openai_reasoning_effort"`
	SchedulingProtectionEnabled bool              `json:"scheduling_protection_enabled"`
	SchedulingFailureThreshold  int               `json:"scheduling_failure_threshold"`
	SchedulingSuccessThreshold  int               `json:"scheduling_success_threshold"`
	AppendProbeErrorCodes       bool              `json:"append_probe_error_codes"`
	FirstTokenProtectionEnabled bool              `json:"first_token_protection_enabled"`
	FirstTokenThresholdSeconds  int               `json:"first_token_threshold_seconds"`
	FirstTokenMinimumSamples    int               `json:"first_token_minimum_samples"`
}

const (
	ProbeSettingOptionPrefix          = "probe_setting."
	DefaultProbeFailureThreshold      = 2
	DefaultProbeSuccessThreshold      = 2
	DefaultFirstTokenThresholdSeconds = 45
	DefaultFirstTokenMinimumSamples   = 5
)

var probeSetting = ProbeSetting{
	PlatformModels:             map[string]string{},
	OpenAIReasoningEffort:      "high",
	SchedulingFailureThreshold: DefaultProbeFailureThreshold,
	SchedulingSuccessThreshold: DefaultProbeSuccessThreshold,
	FirstTokenThresholdSeconds: DefaultFirstTokenThresholdSeconds,
	FirstTokenMinimumSamples:   DefaultFirstTokenMinimumSamples,
}

func init() {
	config.GlobalConfig.Register("probe_setting", &probeSetting)
}

func GetProbeSetting() *ProbeSetting {
	probeSetting.PlatformModels = normalizePlatformModels(probeSetting.PlatformModels)
	switch probeSetting.OpenAIReasoningEffort {
	case "low", "medium", "high", "xhigh":
	default:
		probeSetting.OpenAIReasoningEffort = "high"
	}
	if probeSetting.SchedulingFailureThreshold < 1 || probeSetting.SchedulingFailureThreshold > 20 {
		probeSetting.SchedulingFailureThreshold = DefaultProbeFailureThreshold
	}
	if probeSetting.SchedulingSuccessThreshold < 1 || probeSetting.SchedulingSuccessThreshold > 20 {
		probeSetting.SchedulingSuccessThreshold = DefaultProbeSuccessThreshold
	}
	if probeSetting.FirstTokenThresholdSeconds < 5 || probeSetting.FirstTokenThresholdSeconds > 300 {
		probeSetting.FirstTokenThresholdSeconds = DefaultFirstTokenThresholdSeconds
	}
	if probeSetting.FirstTokenMinimumSamples < 2 || probeSetting.FirstTokenMinimumSamples > 20 {
		probeSetting.FirstTokenMinimumSamples = DefaultFirstTokenMinimumSamples
	}
	return &probeSetting
}

func ValidateProbeSettingOptions(options map[string]string) error {
	for key := range options {
		if !strings.HasPrefix(key, ProbeSettingOptionPrefix) {
			continue
		}
		if _, ok := probeSettingOptionNames[strings.TrimPrefix(key, ProbeSettingOptionPrefix)]; !ok {
			return fmt.Errorf("unknown probe setting: %s", key)
		}
	}
	if value := strings.TrimSpace(options[ProbeSettingOptionPrefix+"platform_models"]); value != "" {
		var models map[string]string
		if err := common.Unmarshal([]byte(value), &models); err != nil {
			return fmt.Errorf("invalid probe platform models: %w", err)
		}
		for platform, model := range models {
			if !validProbePlatforms[platform] {
				return fmt.Errorf("unknown probe platform: %s", platform)
			}
			if len(strings.TrimSpace(model)) > 200 {
				return fmt.Errorf("probe model is too long for platform %s", platform)
			}
		}
	}
	if effort := strings.TrimSpace(options[ProbeSettingOptionPrefix+"openai_reasoning_effort"]); effort != "" {
		switch effort {
		case "low", "medium", "high", "xhigh":
		default:
			return fmt.Errorf("invalid OpenAI reasoning effort: %s", effort)
		}
	}
	for _, key := range []string{
		"openai_reliable_enabled",
		"scheduling_protection_enabled",
		"append_probe_error_codes",
		"first_token_protection_enabled",
	} {
		if value := strings.TrimSpace(options[ProbeSettingOptionPrefix+key]); value != "" {
			if _, err := strconv.ParseBool(value); err != nil {
				return fmt.Errorf("%s must be a boolean", key)
			}
		}
	}
	for key, bounds := range map[string][2]int{
		"scheduling_failure_threshold":  {1, 20},
		"scheduling_success_threshold":  {1, 20},
		"first_token_threshold_seconds": {5, 300},
		"first_token_minimum_samples":   {2, 20},
	} {
		value := strings.TrimSpace(options[ProbeSettingOptionPrefix+key])
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < bounds[0] || parsed > bounds[1] {
			return fmt.Errorf("%s must be between %d and %d", key, bounds[0], bounds[1])
		}
	}
	return nil
}

var probeSettingOptionNames = map[string]struct{}{
	"platform_models": {}, "openai_reliable_enabled": {}, "openai_reasoning_effort": {},
	"scheduling_protection_enabled": {}, "scheduling_failure_threshold": {},
	"scheduling_success_threshold": {}, "append_probe_error_codes": {},
	"first_token_protection_enabled": {}, "first_token_threshold_seconds": {},
	"first_token_minimum_samples": {},
}

var validProbePlatforms = map[string]bool{
	"openai": true, "anthropic": true, "gemini": true,
	"grok": true, "kimi": true, "zhipu": true, "deepseek": true,
	"minimax": true,
}

func normalizePlatformModels(models map[string]string) map[string]string {
	result := make(map[string]string, len(models))
	for key, value := range models {
		if validProbePlatforms[key] && strings.TrimSpace(value) != "" {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}
