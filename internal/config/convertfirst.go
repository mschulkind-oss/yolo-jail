package config

// convertfirst.go is the one refusal a config-editing feature gives when the file it would write
// is still in the deprecated JSONC format and what it writes is TOML vocabulary
// (docs/design/readable-config.md): the owner ruled that "convert your config first" is a
// reasonable stop for such a feature, and the happy-path principle makes it name the conversion
// command and its preview.

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// ConvertFirstMigrateStep is the next step every convert-first refusal names.
const ConvertFirstMigrateStep = "Convert it first: `yolo config migrate` (preview with `yolo config migrate --dry-run`)"

// ConvertFirstRefusal is the refusal a verb prints, before it writes anything, when path is still
// JSONC and what (such as "`yolo allow`") needs the TOML format.
func ConvertFirstRefusal(path, what string) string {
	return fmt.Sprintf("%s is still in the deprecated JSONC format, and %s needs the TOML format. %s, "+
		"then run this command again. Nothing was written.", termsafe.Visible(path), what, ConvertFirstMigrateStep)
}

// UserScopeHoldsBrokered reports whether the user scope still carries the legacy `brokered` key.
// A canonical `allow` table anywhere in the user scope suppresses every legacy `brokered`
// user grant (readableScopeCheckWithSettings applies the legacy allowance only when the user
// scope has no `allow`), so writing `allow` beside `brokered` would silently revoke the
// legacy ceiling. A scope that cannot be read counts as not holding it: the edit's own target
// resolution reports the read failure.
func UserScopeHoldsBrokered() bool {
	user, _, err := UserScopeConfigWithSources(false, func(string) {})
	if err != nil || user == nil {
		return false
	}
	_, has := user.Get(brokeredKey)
	return has
}
