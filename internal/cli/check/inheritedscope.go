package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// reportInheritedScope renders the jail's inherited user scope through the launch's own
// computation (run.PreviewInheritedScope, LC-D4 in docs/reference/config/rulings.md) and
// discloses any unclassified keys, JSONC fallbacks, or legacy omissions once.
//
// Inside a jail, the user scope is already the host-rendered snapshot rather than a host
// config being crossed into a jail, so nothing is re-crossed here.
func (o *Options) reportInheritedScope(r *reporter, workspace, rt string) bool {
	if o.inJail() {
		return false
	}
	preview, err := run.PreviewInheritedScope(workspace, rt)
	if err != nil {
		r.fail(termsafe.VisibleLines(err.Error()), configNote(err.Error(), workspace, configRecheck(r.mode)))
		return true
	}
	if len(preview.Unknown) > 0 {
		r.warn("Config keys with no inherit classification were dropped from the jail's user scope: "+
			strings.Join(preview.Unknown, ", "),
			"Classify them in internal/config/inherit.go, then "+configRecheck(r.mode))
	}
	for _, fb := range preview.Fallbacks {
		r.warn(termsafe.VisibleLines(fb),
			"Convert legacy user-scope values with `yolo config migrate`, then "+configRecheck(r.mode))
	}
	for _, line := range preview.Omissions {
		r.warn(termsafe.VisibleLines(line),
			"Convert or remove the legacy setting in the host file it names, or launch as-is (the host's own launch reads it as before); "+configRecheck(r.mode))
	}
	return false
}
