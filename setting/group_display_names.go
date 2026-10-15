package setting

import (
	"fmt"
	"maps"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var groupDisplayNames = map[string]string{}
var groupDisplayNamesMutex sync.RWMutex

func parseGroupDisplayNames(value string) (map[string]string, error) {
	var names map[string]string
	if err := common.UnmarshalJsonStr(value, &names); err != nil {
		return nil, err
	}
	if names == nil {
		return nil, fmt.Errorf("group display names must be a JSON object")
	}
	seen := make(map[string]string, len(names))
	for group, name := range names {
		for _, char := range name {
			if unicode.IsControl(char) {
				return nil, fmt.Errorf("group display name must not contain control characters: %s", group)
			}
		}
		name = strings.TrimSpace(name)
		if name == "" || utf8.RuneCountInString(name) > 64 {
			return nil, fmt.Errorf("group display name must contain 1 to 64 characters: %s", group)
		}
		if other, exists := seen[name]; exists && other != group {
			return nil, fmt.Errorf("group display names must be unique: %s", name)
		}
		seen[name] = group
		names[group] = name
	}
	return names, nil
}

func ValidateGroupDisplayNamesForGroups(value string, groups map[string]float64) error {
	names, err := parseGroupDisplayNames(value)
	if err != nil {
		return err
	}
	for group, name := range names {
		if _, exists := groups[group]; !exists {
			return fmt.Errorf("group does not exist: %s", group)
		}
		if _, exists := groups[name]; exists && name != group {
			return fmt.Errorf("group display name conflicts with another group ID: %s", name)
		}
	}
	return nil
}

func ValidateGroupDisplayNames(value string) error {
	return ValidateGroupDisplayNamesForGroups(value, ratio_setting.GetGroupRatioCopy())
}

// Loading stored names only validates their shape: a removed group's name is
// retained for history, and group options may load in either order at startup.
// Management writes must call ValidateGroupDisplayNames before persisting.
func UpdateGroupDisplayNamesByJSONString(value string) error {
	names, err := parseGroupDisplayNames(value)
	if err != nil {
		return err
	}
	groupDisplayNamesMutex.Lock()
	defer groupDisplayNamesMutex.Unlock()
	groupDisplayNames = names
	return nil
}

func GetGroupDisplayNamesCopy() map[string]string {
	groupDisplayNamesMutex.RLock()
	defer groupDisplayNamesMutex.RUnlock()
	return maps.Clone(groupDisplayNames)
}

func GroupDisplayNames2JSONString() string {
	value, err := common.Marshal(GetGroupDisplayNamesCopy())
	if err != nil {
		return "{}"
	}
	return string(value)
}
