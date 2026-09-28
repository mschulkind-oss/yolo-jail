package packload

// profileserved.go answers two questions about a jail daemon that serves a PROFILE, both from
// the selected packs' `env` declarations and the same per-agent gate the credential gate asks
// (gateFiresFor; docs/design/provider-credential-scope.md OQ-CN7, ruled 2026-09-28).
//
// A PROFILE-SERVED DAEMON (coined here) is a jail daemon that some selected pack's `env`
// contribution names `served_by`, where EVERY contribution naming it carries a `profile` gate:
// its only clients are the agents that selected that profile. aws-auth's credential adapter is
// the one shipped today — its pointer is gated on `bedrock` — and codex's OpenAI refresh
// adapter is not, because codex's pointer to it is ungated. Such a daemon starts only when one
// of its gates is delivered to some agent of the launch (CN7 (b)).
//
// A SCOPED CALLER TOKEN (coined here) is the caller token of a daemon some selected pack names
// through loopholedecl.TokenCallerToken. It is delivered only in the agent files that pointer
// reaches, never exported through the shared per-entry channel (CN7 (c)).

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// ProfileServedDaemon is one profile-served daemon and the profiles whose gate would start it.
type ProfileServedDaemon struct {
	Name     string
	Profiles []string
}

// profileServedDaemons maps each profile-served daemon the selected packs name to the packs
// and profiles of the gated contributions naming it.
func profileServedDaemons(packs []*Pack) map[string][]gatedServer {
	gated := map[string][]gatedServer{}
	ungated := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, daemon := range p.Decl.EnvServedBy() {
			ungated[daemon] = true
		}
		for _, c := range p.Decl.ProfiledEnvContributions() {
			if c.ServedBy != "" {
				gated[c.ServedBy] = append(gated[c.ServedBy], gatedServer{pack: p, profile: c.Profile})
			}
		}
	}
	for daemon := range ungated {
		delete(gated, daemon)
	}
	return gated
}

type gatedServer struct {
	pack    *Pack
	profile string
}

// UnselectedProfileServedDaemons is every profile-served daemon no gate of this launch's
// selection delivers to any agent, sorted by name, each with the profiles that would start it:
// the daemons a launch leaves out of its jail-daemon payload (OQ-CN7 (b)). profiles is the
// CLI-keyed effective selection (ProfileTable). nil when every one is selected.
func UnselectedProfileServedDaemons(packs []*Pack, profiles map[string]string) []ProfileServedDaemon {
	var out []ProfileServedDaemon
	for daemon, servers := range profileServedDaemons(packs) {
		delivered := false
		seen := map[string]bool{}
		var names []string
		for _, s := range servers {
			if gateDelivered(packs, s.pack, s.profile, profiles) {
				delivered = true
				break
			}
			if !seen[s.profile] {
				seen[s.profile] = true
				names = append(names, s.profile)
			}
		}
		if delivered {
			continue
		}
		sort.Strings(names)
		out = append(out, ProfileServedDaemon{Name: daemon, Profiles: names})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ScopedCallerTokenDaemons is the set of daemons whose caller token some selected pack names
// through loopholedecl.TokenCallerToken (a scoped caller token, above). nil for none.
func ScopedCallerTokenDaemons(packs []*Pack) map[string]bool {
	var out map[string]bool
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.ProfiledEnvContributions() {
			if c.ServedBy == "" {
				continue
			}
			for _, v := range c.Vars {
				if strings.Contains(v, loopholedecl.TokenCallerToken) {
					if out == nil {
						out = map[string]bool{}
					}
					out[c.ServedBy] = true
				}
			}
		}
	}
	return out
}

// ProfileServedDaemonNames is every profile-served daemon the selected packs name, sorted.
func ProfileServedDaemonNames(packs []*Pack) []string {
	var out []string
	for daemon := range profileServedDaemons(packs) {
		out = append(out, daemon)
	}
	sort.Strings(out)
	return out
}
