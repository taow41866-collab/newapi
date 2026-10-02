package authz

import (
	"slices"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func seedBuiltInRoles(db *gorm.DB) error {
	for _, spec := range builtInRoles {
		role := model.AuthzRole{
			Key:         spec.Key,
			Name:        spec.Name,
			Description: spec.Description,
			BuiltIn:     spec.BuiltIn,
			Enabled:     true,
			Sort:        spec.Sort,
		}
		if err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name",
				"description",
				"built_in",
				"enabled",
				"sort",
			}),
		}).Create(&role).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetBuiltInRolePolicies(db *gorm.DB) error {
	subjects := make([]string, 0, len(builtInRoles))
	for _, spec := range builtInRoles {
		subjects = append(subjects, RoleSubject(spec.Key))
	}
	// SQL equality can be case-insensitive and ignore trailing spaces on MySQL.
	// Match the adapter's exact policy semantics before deleting by primary key;
	// a scoped legacy deny must survive baseline reseeding.
	var candidates []model.CasbinRule
	if err := db.Select("id", "ptype", "v0", "v4", "v5").
		Where("ptype = ? AND v0 IN ?", "p", subjects).Find(&candidates).Error; err != nil {
		return err
	}
	ids := make([]uint, 0, len(candidates))
	for _, rule := range candidates {
		if rule.Ptype == "p" && slices.Contains(subjects, rule.V0) && rule.V4 == "" && rule.V5 == "" {
			ids = append(ids, rule.Id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return db.Where("id IN ?", ids).Delete(&model.CasbinRule{}).Error
}

func seedDefaultPolicies(db *gorm.DB) error {
	type policyKey struct{ subject, resource, action string }
	subjects := make([]string, 0, len(builtInRoles))
	for _, spec := range builtInRoles {
		if !spec.Superuser {
			subjects = append(subjects, RoleSubject(spec.Key))
		}
	}
	if len(subjects) == 0 {
		return nil
	}
	var legacyAll []model.CasbinRule
	if err := db.Where("ptype = ? AND v0 IN ? AND v4 = ? AND (v5 = ? OR v5 IS NULL) AND (v3 = ? OR v3 = ? OR v3 IS NULL)",
		"p", subjects, "all", "", "", EffectAllow).Find(&legacyAll).Error; err != nil {
		return err
	}
	covered := make(map[policyKey]bool, len(legacyAll))
	for _, rule := range legacyAll {
		// MySQL collations may match ALL or padded scopes here, while the
		// adapter treats only exact all/empty as a usable global grant.
		if rule.Ptype != "p" || rule.V4 != "all" || rule.V5 != "" || (rule.V3 != "" && rule.V3 != EffectAllow) {
			continue
		}
		covered[policyKey{rule.V0, rule.V1, rule.V2}] = true
	}

	rules := make([]model.CasbinRule, 0)
	for _, spec := range builtInRoles {
		if spec.Superuser {
			continue
		}
		for _, permission := range PermissionsForRole(spec.Key) {
			subject := RoleSubject(spec.Key)
			if !covered[policyKey{subject, permission.Resource, permission.Action}] {
				rules = append(rules, newRule("p", []string{subject, permission.Resource, permission.Action, EffectAllow}))
			}
		}
	}
	if len(rules) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rules).Error
}
