package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/modelroute"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// ResolveModelMapping is shared by routing measurements and the actual relay.
func ResolveModelMapping(requested, raw string) (string, error) {
	if raw == "" || raw == "{}" {
		return requested, nil
	}
	var mapping map[string]string
	if err := common.UnmarshalJsonStr(raw, &mapping); err != nil {
		return requested, errors.New("unmarshal_model_mapping_failed")
	}
	current := requested
	visited := map[string]bool{current: true}
	for {
		next := mapping[current]
		if next == "" {
			next = mapping[hostreasoning.BaseModelName(current)]
		}
		if next == "" || next == current {
			return current, nil
		}
		if visited[next] {
			return requested, errors.New("model_mapping_contains_cycle")
		}
		visited[next] = true
		current = next
	}
}

func ModelRoutingTarget(channel *Channel, requested string) modelroute.Target {
	upstream, _ := ResolveModelMapping(requested, channel.GetModelMapping())
	// Configuration changes invalidate old measurements without storing credentials.
	h := sha256.Sum256([]byte(channel.GetBaseURL() + "\x00" + channel.GetModelMapping() + "\x00" + strconv.Itoa(channel.Type)))
	return modelroute.Target{ID: channel.Id, Upstream: upstream, Version: hex.EncodeToString(h[:]), Weight: float64(channel.GetWeight()), Priority: channel.GetPriority()}
}

// ModelRoutingCandidates preserves existing group/model/path constraints and
// releases the channel cache lock before any shared-state I/O.
func ModelRoutingCandidates(group, name string, filters []dto.ChannelFilter) ([]*Channel, error) {
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		defer channelSyncLock.RUnlock()
		ids, _ := filterCandidateIDs(group2model2channels[group][name], name, filters)
		if len(ids) == 0 {
			ids, _ = filterCandidateIDs(group2model2channels[group][ratio_setting.RoutingMatchModelName(name)], name, filters)
		}
		out := make([]*Channel, 0, len(ids))
		for _, id := range ids {
			if c := channelsIDM[id]; c != nil && c.Status == common.ChannelStatusEnabled {
				copy := *c
				out = append(out, &copy)
			}
		}
		return out, nil
	}
	var abilities []Ability
	if err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, name, true).Find(&abilities).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(abilities))
	for _, a := range abilities {
		ids = append(ids, a.ChannelId)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var channels []*Channel
	if err := DB.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	out := make([]*Channel, 0, len(channels))
	for _, c := range channels {
		if ok, _ := ChannelSatisfiesFilters(c, name, filters); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func selectModelRoute(group, name string, filters []dto.ChannelFilter, scopes []*modelroute.Scope) (*Channel, bool) {
	if len(scopes) == 0 || scopes[0] == nil {
		return nil, false
	}
	s := *scopes[0]
	s.Group = group
	s.Model = name
	if !modelroute.Default.Enabled(s) {
		return nil, false
	}
	channels, err := ModelRoutingCandidates(group, name, filters)
	if err != nil {
		return nil, false
	}
	targets := make([]modelroute.Target, len(channels))
	for i, c := range channels {
		targets[i] = ModelRoutingTarget(c, name)
	}
	id, _, err := modelroute.Default.Select(context.Background(), s, targets, rand.Float64())
	if err != nil {
		common.SysError("model routing unavailable; retaining legacy selection: " + err.Error())
		return nil, false
	}
	if !modelroute.Default.Active(s) {
		return nil, false
	}
	for _, c := range channels {
		if c.Id == id {
			return c, true
		}
	}
	return nil, true
}
