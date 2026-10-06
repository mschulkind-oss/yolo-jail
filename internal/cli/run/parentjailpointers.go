package run

// parentjailpointers.go is the launcher half of a loophole's `inherit_from_parent_jail` block
// (loopholedecl/parentjail.go; docs/design/sso-backed-bedrock.md SSO-D2 to SSO-D5): a podman jail
// launched from INSIDE a jail takes a declaring loophole's credential pointer from the launching
// jail's own environment, and starts neither of the loophole's daemons.
//
// # Why only there
//
// The pointer is an address on the launching jail's loopback. A podman launched inside a
// container is forced onto that container's network namespace (`--net=host`: netavark cannot
// create one without NET_ADMIN, appliedNetMode), so the nested jail's loopback IS the launching
// jail's and the address answers there. That is the one setup where the reach is provable from
// the launcher, so it is the only one that inherits: every other launch — from the host, on
// another backend — runs the loophole as it always has, refusal included.
//
// # What it decides, and who reads it
//
// parentJailPointers is the one decision, a function of the runtime, the loophole records and
// the environment alone, so the launch and its keeper (a separate process that inherits this
// environment and rebuilds its Options from the plan) answer it alike:
//
//   - loopholeAllow excludes the loophole, so no host daemon, front or settings file is started
//     or written for it, by the launch or by its keeper (plannedLoopholeNames agrees);
//   - hostServicesMountArgs emits no endpoint variable for it, so the in-jail reachability
//     witness waits on no endpoint nothing will publish;
//   - jailDaemonsFor drops its jail daemon from the nested payload and records the pointer, which
//     servedDaemons hands the credential gate (packload.ServedDaemons.WithInherited), so the
//     agents the pointer's gate reaches get the launching jail's values in its place;
//   - composeFetchedLists answers its model-list fetch as a failed one (SSO-D5).
//
// The values are the launching jail's credential pointer and are never printed.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// parentJailPointers is every enabled loophole in set that declares `inherit_from_parent_jail`
// and whose pointer this launch takes from the launching jail, each mapped to the launching
// environment's value of every variable the block names. nil unless this process runs inside a
// container and rt is podman (the file comment's reason), and a loophole is left out unless every
// one of its variables is set and non-empty here.
func (o *Options) parentJailPointers(rt string, set loopholes.Set) map[string]map[string]string {
	if rt != "podman" { // parity: NotApplicable — the question arises only where a nested launch is forced onto the launching jail's loopback, which is podman-in-podman's --net=host (appliedNetMode); every other backend runs the loophole as before
		return nil
	}
	if o.PathExists == nil || o.Getenv == nil || !o.inContainer() {
		return nil
	}
	var out map[string]map[string]string
	for _, lp := range set.Enabled() {
		inh := lp.InheritFromParentJail
		if inh == nil || len(inh.Vars) == 0 {
			continue
		}
		vals := make(map[string]string, len(inh.Vars))
		for _, v := range inh.Vars {
			val := o.Getenv(v)
			if val == "" {
				vals = nil
				break
			}
			vals[v] = val
		}
		if vals == nil {
			continue
		}
		if out == nil {
			out = map[string]map[string]string{}
		}
		out[lp.Name] = vals
	}
	return out
}

// inheritedLoopholes is parentJailPointers over cfg's converged loophole set, for the readers that
// hold a config rather than a set.
func (o *Options) inheritedLoopholes(rt string, cfg *jsonx.OrderedMap) map[string]map[string]string {
	return o.parentJailPointers(rt, loopholes.NewHostSet(cfgMap(cfg, "loopholes")))
}

// withoutInheritedDaemons drops from specs the jail daemon of every loophole whose pointer this
// launch inherits (parentJailPointers) and records those pointers for servedDaemons and the
// disclosure. Only a loophole whose daemon reached specs is recorded: one the payload already
// left out (no agent's selection serves it) delivers no pointer, so there is nothing to say.
func (o *Options) withoutInheritedDaemons(rt string, set loopholes.Set,
	specs []loopholes.JailDaemonSpec) []loopholes.JailDaemonSpec {
	o.parentPointers = nil
	inherited := o.parentJailPointers(rt, set)
	if len(inherited) == 0 {
		return specs
	}
	out := specs[:0:0]
	for _, s := range specs {
		if vals, ok := inherited[s.Name]; ok {
			if o.parentPointers == nil {
				o.parentPointers = map[string]map[string]string{}
			}
			o.parentPointers[s.Name] = vals
			continue
		}
		out = append(out, s)
	}
	return out
}

// inheritedDisclosures maps each loophole whose channel composition inherits a pointer for
// (packChannel.served) to its block's `disclose` sentence.
func (o *Options) inheritedDisclosures(set loopholes.Set, served packload.ServedDaemons) map[string]string {
	out := map[string]string{}
	for _, lp := range set.Enabled() {
		if served.Inherits(lp.Name) && lp.InheritFromParentJail != nil {
			out[lp.Name] = lp.InheritFromParentJail.Disclose
		}
	}
	return out
}

// noteParentJailPointers is the disclosure: one line per loophole whose pointer the effective
// channel inherits, `<loophole>: <disclose>`, in name order. A disclosure, so no quiet switch hides
// it (docs/reference/report-tiers.md, OQ-RO3). Silent when nothing is inherited.
func (o *Options) noteParentJailPointers(cfg *jsonx.OrderedMap, channel *packChannel) {
	if channel == nil {
		return
	}
	lines := o.inheritedDisclosures(loopholes.NewHostSet(cfgMap(cfg, "loopholes")), channel.served)
	names := make([]string, 0, len(lines))
	for name := range lines {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		o.pr(o.Stderr).print(richtext.Escape(name + ": " + lines[name]))
	}
}

// inheritedModelListAnswer is the model-list answer for a service whose pointer this launch
// inherits: a failed fetch, worded as one (SSO-D5). The inherited credential was narrowed by the
// host for the launching jail's agents and carries no permission to list models, and the service
// that could list them with the host's own credential is not this launch's to ask.
func inheritedModelListAnswer(service string) awsauthdaemon.ModelListAnswer {
	return awsauthdaemon.ModelListAnswer{Note: "the fetch failed: this nested launch takes its " + service +
		" credentials from the launching jail, narrowed by the host, which carry no permission to list " +
		"models, and it starts no " + service + " service of its own to ask"}
}
