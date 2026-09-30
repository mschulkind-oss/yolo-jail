package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// brokerfence.go is the mount fence (docs/design/boundary-broker.md BB-D26). `mounts` is a
// workspace key and accepts any host path, so without a fence an agent that can edit
// yolo-jail.jsonc could mount yolo's broker directory — the store, every launch's scope file
// and an audit log carrying every workspace's argv — or the host credential a broker exists
// to keep out of the jail, into the next launch, once a human approved the diff.
//
// ARMED BY SELECTION: with a selected pack shipping a loophole that declares `brokered`, a
// `mounts` entry whose host source is, contains or lies inside yolo's broker directory or
// one of that block's `credential_paths` is REFUSED from a workspace config, naming the
// entry, and DISCLOSED from the user config, where it is the user's own authority (§9.6).
// The approvals directory is not fenced (BB-D34): no mount can write an approval — a read-only
// one cannot write anything, and a read-write one is refused for any source inside yolo's
// state directory (context-mounts.md §2.3 clause 1, config.rwMountRefusal).

func validateBrokerMountFence(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver,
	errs, warns *[]string) {
	// The fence is the host launcher's: in a jail the paths it would compare are the jail's,
	// not the machine's, and nothing in a jail mounts anything.
	if resolver == nil || inJail() {
		return
	}
	// NO MOUNT, NO QUESTION: the loophole set is asked for only when there is a mount to fence,
	// as validateLoopholes asks only when there is a `loopholes` block. The real resolver's first
	// answer resolves the configured packs and is memoized for the process, so asking on every
	// ValidateConfig fixed that answer under whichever home validated first — in internal/cli's
	// suite, a later test's `yolo loopholes status` then ran pack doctors it had never configured.
	mounts := mountHostSources(config)
	if len(mounts) == 0 {
		return
	}
	known, _ := resolver.Known()
	var names []string
	for name, info := range known {
		if info.Brokered != nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	home := os.Getenv("HOME")
	type fence struct{ path, owner, what string }
	var fences []fence
	fences = append(fences, fence{paths.BrokerDir(), strings.Join(names, ", "),
		"yolo's broker directory: the brokers' store, each launch's repository scope and the " +
			"audit log of every workspace's brokered calls"})
	for _, name := range names {
		for _, p := range brokerscope.FencedPaths(known[name].Brokered.CredentialPaths, home, os.Getenv)[1:] {
			fences = append(fences, fence{p, name, "the host credential the " + name +
				" loophole keeps out of the jail"})
		}
	}
	fencedPaths := make([]string, len(fences))
	for i, f := range fences {
		fencedPaths[i] = f.path
	}

	wsMounts := map[string]bool{}
	if wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {}); err == nil && wsCfg != nil {
		for spec := range mountHostSources(wsCfg) {
			wsMounts[spec] = true
		}
	}
	specs := make([]string, 0, len(mounts))
	for spec := range mounts {
		specs = append(specs, spec)
	}
	sort.Strings(specs)
	for _, spec := range specs {
		reached := brokerscope.Reaches(mounts[spec].source, fencedPaths)
		if reached == "" {
			continue
		}
		var f fence
		for _, cand := range fences {
			if cand.path == reached {
				f = cand
				break
			}
		}
		if wsMounts[spec] {
			add(errs, fmt.Sprintf("config.mounts: %q reaches %s, %s (%s). A workspace config may not "+
				"mount it: the agent can edit that file, and the mount would carry it into the next "+
				"launch. Remove the entry; the same entry in %s is allowed and disclosed",
				spec, f.path, f.what, f.owner, paths.UserConfigPath()))
			continue
		}
		access := "read it"
		if mounts[spec].rw {
			access = "read AND write it"
		}
		add(warns, fmt.Sprintf("config.mounts: %q (user config) reaches %s, %s (%s); the jail can "+
			"%s", spec, f.path, f.what, f.owner, access))
	}
}

// mountHostSources maps each `mounts` element, keyed by how it was written, to its resolved
// host source and whether it is read-write. Through ParseMounts, so an object element is
// fenced exactly as a string one is (context-mounts.md CX-D1: a string-only reader here would
// skip every object element in silence).
func mountHostSources(config *jsonx.OrderedMap) map[string]fencedMount {
	out := map[string]fencedMount{}
	for _, m := range ParseMounts(config) {
		out[m.Spec] = fencedMount{source: expandAndResolve(m.Host), rw: m.RW}
	}
	return out
}

// fencedMount is one `mounts` element as the fence judges it.
type fencedMount struct {
	source string
	rw     bool
}
