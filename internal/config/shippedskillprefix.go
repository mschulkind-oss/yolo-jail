package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// shippedskillprefix.go is `shipped_skill_prefix` (docs/reference/pack-system.md#shipped-skill-prefix, PN-D13): the
// word every skill yolo ships is delivered under, `<prefix>-<skill>`, built-in skills and shipped
// packs' skills alike. Unset is no prefix, the default: shipped skills carry no marker.
//
// USER SCOPE ONLY. A workspace is writable by the jailed agent, and the names an agent's skills
// carry, at the host notch included, are the user's choice; a workspace value is refused by
// validation and never read.

// shippedSkillPrefixKey is the top-level key.
const shippedSkillPrefixKey = "shipped_skill_prefix"

// ShippedSkillPrefix is the user's shipped_skill_prefix, or "" when it is unset or unusable (the
// validator reports a bad value; a reader that guessed would be a second place the rule lives).
func ShippedSkillPrefix() string {
	return ShippedSkillPrefixIn(UserScopeConfigOrEmpty())
}

// ShippedSkillPrefixIn is ShippedSkillPrefix read from cfg, a user-scope config the caller holds.
func ShippedSkillPrefixIn(cfg *jsonx.OrderedMap) string {
	if cfg == nil {
		return ""
	}
	v, present := cfg.Get(shippedSkillPrefixKey)
	if !present || v == nil {
		return ""
	}
	if shippedSkillPrefixProblem(v) != "" {
		return ""
	}
	return v.(string)
}

// shippedSkillPrefixProblem is the value rule: a namespace word (packname.Word), the spelling
// every delivered name is in.
func shippedSkillPrefixProblem(v any) string {
	s, ok := v.(string)
	if !ok {
		return "expected a string of lowercase letters, digits and single hyphens (got " + pyReprValue(v) + ")"
	}
	if err := packname.Word(shippedSkillPrefixKey, s); err != nil {
		return err.Error()
	}
	return ""
}

// validateShippedSkillPrefix checks the value and refuses a workspace one.
func validateShippedSkillPrefix(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(shippedSkillPrefixKey)
	if !present {
		return
	}
	if v != nil {
		if problem := shippedSkillPrefixProblem(v); problem != "" {
			add(errs, "config."+shippedSkillPrefixKey+": "+problem)
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(shippedSkillPrefixKey); atWorkspace && wsValue != nil {
		add(errs, "config."+shippedSkillPrefixKey+": user-scope only — it names every skill yolo ships, "+
			"so it is read from "+paths.UserConfigDisplayPath()+" and a workspace value refuses the launch. "+
			"Remove it here, and set it on the host with `yolo config set "+shippedSkillPrefixKey+" '\"<word>\"' --user`.")
	}
}
