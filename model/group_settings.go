package model

import (
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

var groupSettingsOptionMutex sync.Mutex

func isGroupIdentityOption(key string) bool {
	return key == "GroupRatio" || key == "group_ratio_setting.group_ratio" || key == "GroupDisplayNames" || key == "UserUsableGroups" || key == "TopupGroupRatio"
}

// Both option spellings update the same runtime map. Persist them together so
// a later configuration reload cannot resurrect a stale alias.
func normalizeGroupIdentityOptions(values map[string]string) (map[string]string, error) {
	values = maps.Clone(values)
	ratio, canonical := values["GroupRatio"]
	alias, aliased := values["group_ratio_setting.group_ratio"]
	if canonical && aliased && ratio != alias {
		return nil, fmt.Errorf("conflicting group ratio options")
	}
	if aliased {
		ratio = alias
	}
	if canonical || aliased {
		values["GroupRatio"] = ratio
		values["group_ratio_setting.group_ratio"] = ratio
	}
	return values, nil
}

func validateGroupIdentityOptions(tx *gorm.DB, values map[string]string) error {
	if raw, changed := values["UserUsableGroups"]; changed {
		var usable map[string]string
		if err := common.UnmarshalJsonStr(raw, &usable); err != nil || usable == nil {
			return fmt.Errorf("usable groups must be a JSON object of descriptions")
		}
	}
	if raw, changed := values["TopupGroupRatio"]; changed {
		var topup map[string]float64
		if err := common.UnmarshalJsonStr(raw, &topup); err != nil || topup == nil {
			return fmt.Errorf("topup group ratios must be a JSON object")
		}
		for _, ratio := range topup {
			if ratio < 0 {
				return fmt.Errorf("topup group ratios must not be negative")
			}
		}
	}
	groups := ratio_setting.GetGroupRatioCopy()
	labels := setting.GroupDisplayNames2JSONString()
	var stored []Option
	if err := lockForUpdate(tx).Where(commonKeyCol+" IN ?", []string{
		"GroupRatio", "group_ratio_setting.group_ratio", "GroupDisplayNames",
	}).Order(commonKeyCol).Find(&stored).Error; err != nil {
		return err
	}
	// Prefer the canonical spelling when upgrading an older installation which
	// persisted both aliases independently. Parse only the winning value: a
	// corrupt obsolete alias must not block an otherwise valid configuration.
	var storedRatio string
	var hasStoredRatio bool
	for _, key := range []string{"group_ratio_setting.group_ratio", "GroupRatio", "GroupDisplayNames"} {
		for _, option := range stored {
			if option.Key != key {
				continue
			}
			if key == "GroupDisplayNames" {
				labels = option.Value
			} else {
				storedRatio, hasStoredRatio = option.Value, true
			}
		}
	}
	if hasStoredRatio {
		groups = nil
		if err := common.UnmarshalJsonStr(storedRatio, &groups); err != nil || groups == nil {
			return fmt.Errorf("invalid stored group ratios")
		}
	}
	if raw, changed := values["GroupRatio"]; changed {
		var next map[string]float64
		if err := common.UnmarshalJsonStr(raw, &next); err != nil || next == nil {
			return fmt.Errorf("group ratios must be a JSON object")
		}
		if err := ratio_setting.CheckGroupRatio(raw); err != nil {
			return err
		}
		for group := range groups {
			if _, exists := next[group]; exists {
				continue
			}
			if err := checkGroupIdentifierUnused(tx, group); err != nil {
				return err
			}
		}
		groups = next
	}
	if raw, changed := values["GroupDisplayNames"]; changed {
		return setting.ValidateGroupDisplayNamesForGroups(raw, groups)
	}
	// Deleted groups may retain labels for historical display, but they do not
	// reserve the name of a new active group.
	var activeLabels map[string]string
	if err := common.UnmarshalJsonStr(labels, &activeLabels); err != nil {
		return err
	}
	for group := range activeLabels {
		if _, exists := groups[group]; !exists {
			delete(activeLabels, group)
		}
	}
	encoded, err := common.Marshal(activeLabels)
	if err != nil {
		return err
	}
	return setting.ValidateGroupDisplayNamesForGroups(string(encoded), groups)
}

// A group key is an authorization/routing identity. Names can be edited via
// GroupDisplayNames; removing a referenced identity is never a rename.
func checkGroupIdentifierUnused(tx *gorm.DB, group string) error {
	for _, reference := range []struct {
		model any
		where string
		args  []any
	}{
		{&User{}, commonGroupCol + " = ?", []any{group}},
		{&Token{}, commonGroupCol + " = ?", []any{group}},
		{&Ability{}, commonGroupCol + " = ?", []any{group}},
		{&SubscriptionPlan{}, "upgrade_group = ? OR downgrade_group = ?", []any{group, group}},
		{&UserSubscription{}, "upgrade_group = ? OR downgrade_group = ? OR prev_user_group = ?", []any{group, group, group}},
	} {
		var count int64
		if err := tx.Model(reference.model).Where(reference.where, reference.args...).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("分组 %s 仍被使用；改名请编辑显示名称，不能删除或替换分组标识", group)
		}
	}
	var channels []Channel
	if err := tx.Select("id", commonGroupCol).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		for name := range strings.SplitSeq(channel.Group, ",") {
			if strings.TrimSpace(name) == group {
				return fmt.Errorf("分组 %s 仍被渠道使用；改名请编辑显示名称", group)
			}
		}
	}
	var tokens []Token
	return tx.Select("id", "auto_groups").Where("auto_groups <> ''").FindInBatches(&tokens, 500, func(_ *gorm.DB, _ int) error {
		for _, token := range tokens {
			groups, err := token.GetAutoGroups()
			if err != nil {
				return fmt.Errorf("invalid token auto groups: %w", err)
			}
			for _, name := range groups {
				if name == group {
					return fmt.Errorf("分组 %s 仍被令牌自动分组使用；改名请编辑显示名称", group)
				}
			}
		}
		return nil
	}).Error
}
