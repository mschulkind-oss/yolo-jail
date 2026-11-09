package check

// secretenv.go is `yolo check`'s prediction of the env-set lines a launch prints
// (docs/design/secret-env-scoping.md §3.6, §3.7): a private name nothing in the launch claims
// (row 4), and a public name an unselected shipped provider claims (row 1's line). The same gate
// the launch runs answers it (overrideScope), so check and the launch cannot disagree. Each is a
// WARN (secret-env-scoping-plan.md SEP-D9): the launch continues and prints the line. Names only,
// never a value.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// secretEnvGap is one warning per env-set line scope would print at launch.
// Each line's verb names the scope whose config sets the name, as the launch's does
// (secret-env-scoping-plan.md#SEP-D6).
func secretEnvGap(scope *packload.CredentialScope, workspace string) []overrideGapFinding {
	var warns []overrideGapFinding
	for _, line := range scope.EnvSetLines(packload.DisclosureNotes{Scope: config.EnvNameScope(workspace)}) {
		warns = append(warns, overrideGapFinding{msg: strings.TrimSpace(line)})
	}
	return warns
}
