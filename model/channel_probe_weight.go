package model

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const channelProbeWeightStateKey = "probe_weight_state"

type channelProbeWeightState struct {
	OriginalWeight       uint `json:"original_weight"`
	ManagedWeight        uint `json:"managed_weight"`
	ConsecutiveFailures  int  `json:"consecutive_failures"`
	ConsecutiveSuccesses int  `json:"consecutive_successes"`
}

// UpdateChannelProbeWeight applies a shared, durable probe streak to channel routing weight.
// Two consecutive failures reduce the weight to zero; two consecutive successes restore
// the weight that was active before probe management began. A manual weight edit while
// degraded becomes the new restoration target.
func UpdateChannelProbeWeight(channelID int, success bool, failureThreshold, successThreshold int, enabled, requireSuccessThreshold bool) (bool, error) {
	if channelID < 1 {
		return false, errors.New("channel ID must be positive")
	}
	if failureThreshold < 1 || successThreshold < 1 {
		return false, errors.New("probe thresholds must be positive")
	}

	ready := false
	weightChanged := false
	var updatedWeight uint
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).Select("id", "weight", "other_info").First(&channel, channelID).Error; err != nil {
			return err
		}

		otherInfo := make(map[string]json.RawMessage)
		if channel.OtherInfo != "" {
			if err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo); err != nil {
				return fmt.Errorf("decode channel probe state: %w", err)
			}
		}
		if otherInfo == nil {
			otherInfo = make(map[string]json.RawMessage)
		}

		var state channelProbeWeightState
		stateRaw, hasState := otherInfo[channelProbeWeightStateKey]
		if hasState {
			if err := common.Unmarshal(stateRaw, &state); err != nil {
				return fmt.Errorf("decode channel probe weight state: %w", err)
			}
		}

		currentWeight := uint(0)
		if channel.Weight != nil {
			currentWeight = *channel.Weight
		}
		if !enabled {
			if !hasState {
				ready = true
				return nil
			}
			if currentWeight == state.ManagedWeight {
				currentWeight = state.OriginalWeight
			}
			delete(otherInfo, channelProbeWeightStateKey)
			ready = true
		} else {
			if success && !hasState && !requireSuccessThreshold {
				ready = true
				return nil
			}
			if !hasState {
				state.OriginalWeight = currentWeight
				state.ManagedWeight = currentWeight
			} else if currentWeight != state.ManagedWeight {
				state.OriginalWeight = currentWeight
				state.ManagedWeight = currentWeight
				state.ConsecutiveFailures = 0
				state.ConsecutiveSuccesses = 0
			}

			if success {
				state.ConsecutiveFailures = 0
				state.ConsecutiveSuccesses = min(state.ConsecutiveSuccesses+1, successThreshold)
				if state.ConsecutiveSuccesses >= successThreshold {
					currentWeight = state.OriginalWeight
					delete(otherInfo, channelProbeWeightStateKey)
					ready = true
				} else {
					stateRaw, err := common.Marshal(state)
					if err != nil {
						return err
					}
					otherInfo[channelProbeWeightStateKey] = stateRaw
				}
			} else {
				state.ConsecutiveSuccesses = 0
				state.ConsecutiveFailures = min(state.ConsecutiveFailures+1, failureThreshold)
				if state.ConsecutiveFailures >= failureThreshold {
					currentWeight = 0
					state.ManagedWeight = 0
					ready = true
				}
				stateRaw, err := common.Marshal(state)
				if err != nil {
					return err
				}
				otherInfo[channelProbeWeightStateKey] = stateRaw
			}
		}

		otherInfoJSON, err := common.Marshal(otherInfo)
		if err != nil {
			return err
		}
		weightChanged = channel.Weight == nil && currentWeight != 0
		if channel.Weight != nil {
			weightChanged = currentWeight != *channel.Weight
		}
		updatedWeight = currentWeight
		updates := map[string]any{"other_info": string(otherInfoJSON)}
		if weightChanged {
			updates["weight"] = currentWeight
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
			return err
		}
		if weightChanged {
			if err := tx.Model(&Ability{}).Where("channel_id = ?", channelID).Update("weight", currentWeight).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if weightChanged {
		CacheUpdateChannelWeight(channelID, updatedWeight)
	}
	return ready, nil
}
