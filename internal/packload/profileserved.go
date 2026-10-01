package packload

// profileserved.go answers two questions about a jail daemon that serves a SELECTION, both from
// the selected packs' `env` declarations and the same per-agent gate the credential gate asks
// (gateFiresFor; docs/reference/providers.md OQ-CN7, ruled 2026-09-28).
//
// A PROFILE-SERVED DAEMON (coined here) is a jail daemon that some selected pack's `env`
// contribution names `served_by`, where EVERY contribution naming it carries a gate — a
// `profile` name or, since OQ-BR8 (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29), a provider `platform`: its only clients are the agents whose selection satisfies
// one of those gates. aws-auth's credential adapter is the one shipped today — its pointer is
// gated on the platform "aws-bedrock", so it starts for `-p bedrock`, for a user's own
// profile over that provider and for a user's own Bedrock provider alike — and codex's OpenAI
// refresh adapter is not, because codex's pointer to it is ungated. Such a daemon starts only
// when one of its gates is delivered to some agent of the launch (CN7 (b)). The term keeps its
// name: a selection is still made by selecting a profile.
//
// A SCOPED CALLER TOKEN (coined here) is the caller token of a daemon some selected pack names
// through loopholedecl.TokenCallerToken. It is delivered only in the agent files that pointer
// reaches, never exported through the shared per-entry channel (CN7 (c)).

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// ProfileServedDaemon is one profile-served daemon and the gates that would start it: the
// profile names of its name-gated contributions and the platforms of its platform-gated ones.
type ProfileServedDaemon struct {
	Name      string
	Profiles  []string
	Platforms []string
}

// profileServedDaemons maps each profile-served daemon the selected packs name to the packs
// and gates of the gated contributions naming it.
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
		for _, c := range p.Decl.GatedEnvContributions() {
			if c.ServedBy != "" {
				gated[c.ServedBy] = append(gated[c.ServedBy],
					gatedServer{pack: p, profile: c.Profile, platform: c.Platform})
			}
		}
	}
	for daemon := range ungated {
		delete(gated, daemon)
	}
	return gated
}

type gatedServer struct {
	pack     *Pack
	profile  string
	platform string
}

// UnselectedProfileServedDaemons is every profile-served daemon no gate of this launch's
// selection delivers to any agent, sorted by name, each with the gates that would start it:
// the daemons a launch leaves out of its jail-daemon payload (OQ-CN7 (b)). sel is the gate's
// view of the effective selection (SelectionOf). nil when every one is selected.
func UnselectedProfileServedDaemons(packs []*Pack, sel GateSelection) []ProfileServedDaemon {
	var out []ProfileServedDaemon
	for daemon, servers := range profileServedDaemons(packs) {
		delivered := false
		var profiles, platforms []string
		for _, s := range servers {
			if gateDelivered(packs, s.pack, s.profile, s.platform, sel) {
				delivered = true
				break
			}
			switch {
			case s.platform != "":
				platforms = appendUnique(platforms, s.platform)
			case s.profile != "":
				profiles = appendUnique(profiles, s.profile)
			}
		}
		if delivered {
			continue
		}
		sort.Strings(profiles)
		sort.Strings(platforms)
		out = append(out, ProfileServedDaemon{Name: daemon, Profiles: profiles, Platforms: platforms})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ProfilesOnPlatform is every resolved profile whose provider's composed entry declares
// platform, sorted: the `-p` names a line naming a platform gate can offer as the way to
// satisfy it. nil when none does.
func ProfilesOnPlatform(resolved map[string]ResolvedProfile, providers *jsonx.OrderedMap,
	platform string) []string {
	var out []string
	for name, r := range resolved {
		if platform != "" && entryString(providerEntry(providers, r.Provider), "platform") == platform {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func appendUnique(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}

// ScopedCallerTokenDaemons is the set of daemons whose caller token some selected pack names
// through loopholedecl.TokenCallerToken (a scoped caller token, above). nil for none.
func ScopedCallerTokenDaemons(packs []*Pack) map[string]bool {
	var out map[string]bool
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.GatedEnvContributions() {
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
