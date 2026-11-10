package check

// secretenv.go is `yolo check`'s prediction of what the two env sets make a launch print
// (docs/design/secret-env-scoping.md §3.5-§3.7, §4.8). The same gate the launch runs answers it
// (overrideScope), so check and the launch cannot disagree:
//
//   - the env-set lines, a private name nothing claims (row 4) and a public name an unselected
//     shipped provider claims (row 1's line), are each a WARN (secret-env-scoping-plan.md SEP-D9):
//     the launch continues and prints the line;
//   - the refusals, a selected provider's key in the public set (row 2) and a name in both sets,
//     are each a FAIL, in the launch's own words (config.SecretEnvRefusals).
//
// Names only, never a value.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// secretEnvGap is one FAIL per refusal the launch would stop on and one WARN per env-set line it
// would print. hydrated is the env the gate read. Each verb names the scope whose config sets
// the name, as the launch's does (secret-env-scoping-plan.md#SEP-D6).
func secretEnvGap(scope *packload.CredentialScope, hydrated *config.HydratedEnv, workspace string) (fails, warns []overrideGapFinding) {
	for _, refusal := range config.SecretEnvRefusals(hydrated, scope.PublicClaims(), config.EnvSiteOf(workspace, false)) {
		msg, note, _ := strings.Cut(refusal, "\n")
		fails = append(fails, overrideGapFinding{msg: msg, note: note})
	}
	for _, line := range scope.EnvSetLines(packload.DisclosureNotes{Scope: config.EnvNameScope(workspace)}) {
		warns = append(warns, overrideGapFinding{msg: strings.TrimSpace(line)})
	}
	return fails, warns
}
