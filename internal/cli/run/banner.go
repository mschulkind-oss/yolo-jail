package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// LaunchBanner formats the launch line once the selected setup and applied network are known.
// The setup name and fields lead; runtime identity, stale image version and resource limits follow.
func LaunchBanner(s setup.Setup, network string, source setup.Source, runtimeName, cname, hostVersion, jailVersion string, resParts []string) string {
	line := setup.LaunchDisclosure(s, network, source)
	line += " | Jail: " + cname + " | Runtime: " + runtimeName
	if jailVersion != "" && jailVersion != hostVersion {
		line += " | built at " + jailVersion
	}
	if len(resParts) > 0 {
		line += "\nResource limits: " + strings.Join(resParts, ", ")
	}
	return line
}
