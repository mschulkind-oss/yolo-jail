package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// promotionTargetKey selects where `yolo config promote` writes when --to is omitted.
const promotionTargetKey = "promotion_target"

// PromotionTarget returns the user's default promotion destination. It is deliberately
// read from user scope: a workspace is writable by the jailed agent, so it must not get to
// redirect captured personal settings into a pack of its choosing. An absent or unusable
// value preserves the conventional local-pack default.
func PromotionTarget() string {
	return promotionTargetValue(UserScopeConfigOrEmpty())
}

func promotionTargetValue(cfg *jsonx.OrderedMap) string {
	v, present := cfg.Get(promotionTargetKey)
	if !present || v == nil {
		return "local"
	}
	if problem := promotionTargetProblem(v); problem != "" {
		return "local"
	}
	return v.(string)
}

// promotionTargetProblem validates the target vocabulary shared by the reader and schema.
// `host` is intentionally not a default: it is a different ownership contract and requires
// an explicit --to host on each invocation.
func promotionTargetProblem(v any) string {
	s, ok := v.(string)
	if !ok {
		return "expected \"local\" or \"pack:<name>\" (got " + pyReprValue(v) + ")"
	}
	if s == "local" || (strings.HasPrefix(s, "pack:") && strings.TrimPrefix(s, "pack:") != "") {
		return ""
	}
	return "expected \"local\" or \"pack:<name>\" (got " + pyReprValue(v) + ")"
}
