package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

type selectedPackCollisionFinding struct {
	message string
	note    string
}

// selectedPackCollisionFindings is the shared launch-refusal half of sectionPacks. ConfigCheck
// runs it over the declarations it could read; any unresolved packs remain explicit SKIPs and
// do not erase collisions among the readable selection.
func selectedPackCollisionFindings(packs []*packload.Pack) []selectedPackCollisionFinding {
	var findings []selectedPackCollisionFinding
	for _, c := range packload.ConfigSurfaceCollisions(packs) {
		findings = append(findings, selectedPackCollisionFinding{
			message: "config surface " + c.Target + " has more than one owner",
			note:    "packs " + strings.Join(c.Packs, ", ") + " — " + c.Reason + "\n" + keepOneNote(),
		})
	}
	for _, c := range packload.ProgramProviderCollisions(packs) {
		findings = append(findings, selectedPackCollisionFinding{
			message: "program " + c.Target + " has more than one provider",
			note:    "packs " + strings.Join(c.Packs, ", ") + " — " + c.Reason,
		})
	}
	for _, c := range packload.AgentNameCollisions(packs) {
		findings = append(findings, selectedPackCollisionFinding{
			message: "agent name " + c.Target + " has more than one owning pack",
			note:    "packs " + strings.Join(c.Packs, ", ") + " — " + c.Reason + "\n" + keepOneNote(),
		})
	}
	for _, c := range packload.NameCollisions(packs) {
		findings = append(findings, selectedPackCollisionFinding{message: "pack name collision", note: c})
	}
	return findings
}
