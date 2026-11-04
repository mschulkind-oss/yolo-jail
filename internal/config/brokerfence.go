package config

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// brokerfence.go is the mount fence (docs/design/boundary-broker.md BB-D26). `mounts` is a
// workspace key and accepts any host path, so without a fence an agent that can edit
// yolo-jail.jsonc could mount yolo's broker directory — the store, every launch's scope file
// and an audit log carrying every workspace's argv — or the host credential a broker exists
// to keep out of the jail, into the next launch, once a human approved the diff.
//
// ARMED BY SELECTION: when a selected pack ships a host-daemon loophole, every `mounts` entry
// whose host source is, contains or lies inside that loophole's state directory is REFUSED from
// workspace config, naming the entry, and DISCLOSED from the user config, where it is the user's
// own authority. A brokered loophole additionally fences yolo's broker directory and its declared
// `credential_paths`. The approvals directory is not fenced (BB-D34): no mount can write an
// approval — a read-only one cannot write anything, and a read-write one is refused for any source
// inside yolo's state directory (context-mounts.md#the-refusal-set clause 1, config.rwMountRefusal).
//
// The fenced paths are brokerMountFences' (mountsrequestbind.go), which the config-change gate
// also runs on a writable mount request it read itself (CheckWorkspaceMounts).

func validateBrokerMountFence(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver,
	errs, warns *[]string, edit ...*editValidationContext) {
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
	fences := brokerMountFences(resolver)
	if len(fences) == 0 {
		return
	}

	wsMounts := map[string]bool{}
	if wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...); err == nil && wsCfg != nil {
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
		f, reached := fences.reach(mounts[spec].source)
		if !reached {
			continue
		}
		if wsMounts[spec] {
			add(errs, workspaceFenceRefusal(spec, f))
			continue
		}
		access := "read it"
		if mounts[spec].rw {
			access = "read AND write it"
		}
		add(warns, fmt.Sprintf("config.mounts: %q (user config) reaches %s, %s (%s); the jail can %s. "+
			"Remove the entry if the jail should not have this access.", spec, f.path, f.what, f.owner, access))
	}
}

// mountHostSources maps each `mounts` element, keyed by how it was written, to its resolved
// host source and whether it is read-write. Through ParseMounts, so an object element is
// fenced exactly as a string one is (context-mounts.md#CX-D1: a string-only reader here would
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

func sortStrings(s []string) { sort.Strings(s) }
