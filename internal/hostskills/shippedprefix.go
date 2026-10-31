package hostskills

import "github.com/mschulkind-oss/yolo-jail/internal/config"

// shippedSkillPrefix is the user's `shipped_skill_prefix` (config.ShippedSkillPrefix), the
// namespace PackLayer gives a shipped pack's skills (docs/design/pack-naming.md PN-D13). A
// variable so a test can say what the user configured without a config file.
var shippedSkillPrefix = config.ShippedSkillPrefix
