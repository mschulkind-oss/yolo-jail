package run

// profiledaemons.go is the attach half of OQ-CN7 (b) (docs/design/provider-credential-scope.md,
// ruled 2026-09-28): a PROFILE-SERVED jail daemon (coined in internal/packload's
// profileserved.go — one whose only clients are the agents a profile-gated pointer reaches)
// starts only when a fresh launch's selection delivers that pointer to some agent
// (withoutUnselectedProfileDaemons). An attach starts no daemon, so an attach whose selection
// needs one the running jail's launch did not start could only hand an agent a pointer to
// nothing. That is the attach-skew disposition's case: the jail cannot take what this entry
// delivers.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

// missingProfileServedDaemons is every profile-served daemon in specs — this entry's payload,
// composed over the running jail's packs — that the running jail's frozen YOLO_JAIL_DAEMONS
// does not name, sorted. nil when the container's environment could not be read (envLines nil:
// nothing is known, so nothing is claimed missing), and nil when none is missing.
func missingProfileServedDaemons(envLines []string, specs []loopholes.JailDaemonSpec,
	packs []*packload.Pack) []string {
	if envLines == nil {
		return nil
	}
	gated := map[string]bool{}
	for _, name := range packload.ProfileServedDaemonNames(packs) {
		gated[name] = true
	}
	running := map[string]bool{}
	for _, s := range supervisor.ParseEnv(envLineValue(envLines, "YOLO_JAIL_DAEMONS")) {
		running[s.Name] = true
	}
	var out []string
	for _, s := range specs {
		if gated[s.Name] && !running[s.Name] {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// profileDaemonSkew words missing profile-served daemons for the attach-skew disposition.
func profileDaemonSkew(missing []string) attachSkew {
	quoted := make([]string, len(missing))
	for i, m := range missing {
		quoted[i] = strconv.Quote(m)
	}
	why := "Its launch selected no profile that daemon serves, so it was not started " +
		"(provider-credential-scope.md OQ-CN7), and an attach starts no daemon: the pointer this " +
		"entry would hand an agent would point at nothing."
	return attachSkew{
		jail: "was started without the " + strings.Join(quoted, ", ") + " jail daemon that this " +
			"entry's profile selection needs",
		lines:       []string{"  • " + why},
		differences: []string{why},
	}
}

// envLinesMap is a container-inspect env listing as a map. nil for nil.
func envLinesMap(envLines []string) map[string]string {
	if envLines == nil {
		return nil
	}
	out := make(map[string]string, len(envLines))
	for _, l := range envLines {
		if k, v, ok := strings.Cut(l, "="); ok {
			out[k] = v
		}
	}
	return out
}
