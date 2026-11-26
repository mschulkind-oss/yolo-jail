package packdecl

// updateprobe.go is `update_probes`: per program, the address a launcher asks, after the program's own
// update verb FAILS, whether its upstream answered (program-delivery.md PD-NF3). It is the host the verb
// downloads the release from, which need not be the installer's (`url`): claude's `claude install` downloads
// from downloads.claude.ai, and a probe of claude.ai's installer that answered while the release host was
// down refused a launch that should have run the installed version (2026-11-26). Absent, the probe asks
// `url`.
//
// PACK-WIDE AND KEYED BY BIN, for host_keychain_env's reason: a new field on a `program` contribution makes
// an older yolo's tolerant reader skip the whole program, while it ignores an unknown top-level key.

import (
	"fmt"
	"sort"
	"strings"
)

// validateUpdateProbes reports every problem with `update_probes`: each key names a non-fork installer
// program of this pack that declares an update verb, the one launcher that probes a URL after the verb,
// and each value is an https:// URL.
func (m *Manifest) validateUpdateProbes() []string {
	var problems []string
	bins := make([]string, 0, len(m.UpdateProbeByBin))
	for bin := range m.UpdateProbeByBin {
		bins = append(bins, bin)
	}
	sort.Strings(bins)
	for _, bin := range bins {
		label := fmt.Sprintf("update_probes[%q]", bin)
		var owner *Contribution
		for _, c := range m.Contributions() {
			if bin != "" && c.Provides() && c.Bin == bin && !c.IsFork() {
				c := c
				owner = &c
				break
			}
		}
		switch {
		case owner == nil:
			problems = append(problems, label+": must name a non-fork program declared by this pack; move the probe to the owning pack")
		case owner.Via != "installer":
			problems = append(problems, label+`: is read only for a program with via "installer" — only its launcher probes a URL after a failed update; remove it`)
		case len(owner.Update) == 0:
			problems = append(problems, label+`: needs the program's "update" verb — it is asked only after that verb fails; declare the verb or remove the probe`)
		}
		if u := m.UpdateProbeByBin[bin]; !strings.HasPrefix(u, "https://") {
			problems = append(problems, fmt.Sprintf("%s: %q is not an https:// URL — name the host the update verb downloads from", label, u))
		}
	}
	return problems
}
