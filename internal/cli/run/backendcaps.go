package run

import (
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// backendcaps.go holds the predicates that answer "can this backend do X?" for the
// run pipeline — the thing docs/design/backend-parity.md is about.
//
// It exists because of a defect shape rather than a tidiness urge. The `:ro` rule
// below spent its whole life as `ctxMountsUnsafe := rt == "container"`, a local
// variable inside assemble.go's config-`mounts` loop. That is a perfectly good place
// to put a rule you believe has one call site, and a bad place to put one that turns
// out to have two: when the pack `mount` kind landed — emitting the identical
// `-v host:dest:ro` argv from packhostgrants.go — there was no shared thing for it to
// consult, so it silently didn't. A rule reachable only from the function that
// discovered it will be re-discovered, or not.
//
// The bar for adding a predicate here: a capability at least two call sites must agree
// on, stated once, with its evidence in the comment. A single-site `rt ==` check is
// still fine at its site.
//
// The second cluster below (appliedNetMode, appliedCtxMounts, appliedResourceLimits)
// clears that bar for a reason worth naming: their two call sites are the ARGV and the
// BRIEFING, and a briefing composed from the config instead of from what the launch
// applied is §6's defect — a jail that told the agent something untrue, which is worse
// than a jail missing a capability because an agent plans around it. Both consumers now
// read one answer, so the divergence is unrepresentable rather than fixed case by case.

// roBindsUnsupported reports why a backend cannot honor a read-only bind mount,
// or "" when it can.
//
// Apple Container accepts `-v src:dest:ro` and IGNORES the suffix, which is the
// dangerous failure mode rather than the annoying one: the mount succeeds, so nothing
// looks wrong, and the agent holds write access to a host directory the user granted
// as read-only. Both callers therefore refuse the mount rather than downgrade it —
// there is no read-only bind to fall back to, and handing over a writable one on a
// backend the user picked for isolation is not a degradation anyone consented to.
//
// macos-user is deliberately absent: it has no bind mounts at all, so a `:ro` question
// does not arise there. Its gaps are reported by noteMacosUserContentGaps.
func roBindsUnsupported(rt string) string {
	if rt == "container" {
		return "Apple Container ignores read-only (:ro), so it would be writable. " +
			"Use `YOLO_RUNTIME=podman` for read-only context mounts."
	}
	return ""
}

// appliedNetMode reports the network mode this launch actually RUNS under, which is not
// always the one the config asked for. Its two callers are assembleRunCmd, which emits
// the `--net=` selector, and refreshJailBriefings, which tells the agent what its own
// `localhost` means.
//
// They disagreed in both directions, and that is backend-parity.md §6's first live case:
//
//   - podman-in-podman is FORCED to host networking whatever `network.mode` says
//     (netavark cannot create a netns without NET_ADMIN), so a nested jail — the repo's
//     own dev loop — was told it was bridged while sharing the launcher's stack;
//   - Apple Container emits no network selector at all, so a jail configured
//     `network.mode: "host"` there was told "localhost resolves directly to the host"
//     one line after the launch warned that the key is not honored.
//
// This is sharesLauncherNetns read as a MODE rather than a second spelling of it: one
// namespace with the launcher IS host networking, and Apple Container is excluded here
// for the same reason it is excluded there — it does its own per-container networking
// and takes no selector from the assembler, so its jail is never host-networked however
// the key is set. "bridge" is what that backend has always rendered and what its warning
// tells the user to expect; nothing here escalates it (§7).
//
// macos-user is deliberately absent: Run() returns before runContainer, so neither
// caller ever sees that runtime.
func appliedNetMode(rt, netMode string, inContainer bool) string {
	if rt == "container" {
		return "bridge"
	}
	if sharesLauncherNetns(rt, netMode, inContainer) {
		return "host"
	}
	return netMode
}

// appliedCtxMounts filters config `mounts` descriptions down to the ones the backend
// will actually bind.
//
// §6 names only network and resources, but a briefing section headed "Additional Context
// Mounts (read-only)" listing mounts the backend REFUSED is the same defect with a
// different key: the assembler drops every one of them on Apple Container (roBindsUnsupported,
// and it prints why), and the agent was then handed a list of /ctx paths that do not exist.
// The rule is not restated here — this is roBindsUnsupported's briefing-side projection,
// which is the whole point of that predicate having a home.
func appliedCtxMounts(rt string, descriptions []string) []string {
	if roBindsUnsupported(rt) != "" {
		return nil
	}
	return descriptions
}

// limitSource says where an applied resource limit's value came from. It exists so the
// briefing can state what the backend IMPOSES without gaining a standing line: yolo's own
// uniform fallback (podman's `--pids-limit 32768`, applied to every jail that has never
// set the key) is a constant no reader learns anything from, while Apple Container's
// defaults are derived from THIS machine and are the difference between an agent
// believing it is uncapped and knowing it is not.
type limitSource int

const (
	// limitConfigured: the value is the user's own resources.<key>.
	limitConfigured limitSource = iota
	// limitBackendDefault: the config left the key unset and this BACKEND caps anyway.
	limitBackendDefault
	// limitPipelineDefault: the config left the key unset and yolo passes its own
	// uniform fallback, identical in every jail on the backend.
	limitPipelineDefault
)

// resourceLimit is one resource flag a backend actually passes: the flag, the config key
// it answers to (which is what a briefing names), the value, and where the value came from.
type resourceLimit struct {
	flag   string
	key    string
	value  string
	source limitSource
}

// appliedResourceLimits reports the resource flags this backend will actually pass, in
// argv order. resourceArgs renders it straight into the argv and the briefing renders the
// same list into prose, so "described as kernel-enforced" and "on the command line" are
// one list rather than two beliefs about one config block.
//
// The ruling for the two readings of "applied" (backend-parity.md §6) is REPORT WHAT IS
// EMITTED: an agent believing it is uncapped while capped is the worse lie, so Apple
// Container's defaults-when-unconfigured are limits like any other — and `pids_limit`,
// which that backend never passes, is absent from the list rather than described.
//
// acDefaultMemory is called ONLY on the path that needs it (Apple Container with no
// configured memory) because that value is a host-memory read: appleContainerDefaultMemory
// is the argv caller's answer, and the briefing caller passes a description instead,
// keeping its own path free of host probes (see refreshJailBriefings).
func appliedResourceLimits(rt string, resCfg *jsonx.OrderedMap, acDefaultMemory func() string) []resourceLimit {
	memory, memorySrc := "", limitConfigured
	cpus, cpusSrc := "", limitConfigured
	haveCPUs := false
	if resCfg != nil {
		if v := mapGet(resCfg, "memory"); v != nil {
			memory = pyStrCoerce(v)
		}
		if v := mapGet(resCfg, "cpus"); v != nil {
			cpus = pyStrCoerce(v)
			haveCPUs = true
		}
	}

	if rt == "container" {
		if !haveCPUs {
			hostCPUs := numCPU()
			half := hostCPUs / 2
			if half < 2 {
				half = 2
			}
			cpus = strconv.Itoa(half)
			cpusSrc = limitBackendDefault
			haveCPUs = true
		}
		if memory == "" {
			memory = acDefaultMemory()
			memorySrc = limitBackendDefault
		}
	}

	var out []resourceLimit
	if memory != "" {
		out = append(out, resourceLimit{flag: "--memory", key: "memory", value: memory, source: memorySrc})
	}
	if haveCPUs {
		out = append(out, resourceLimit{flag: "--cpus", key: "cpus", value: cpus, source: cpusSrc})
	}
	if rt != "container" {
		pids, pidsSrc := "32768", limitPipelineDefault
		if resCfg != nil {
			if v := mapGet(resCfg, "pids_limit"); v != nil {
				pids, pidsSrc = pyStrCoerce(v), limitConfigured
			}
		}
		out = append(out, resourceLimit{flag: "--pids-limit", key: "pids_limit", value: pids, source: pidsSrc})
	}
	return out
}

// appleContainerDefaultMemoryDesc is how the briefing names the memory cap Apple Container
// receives when `resources.memory` is unset. A DESCRIPTION rather than the number because
// the number is a host-memory read and the briefing path takes no host probes;
// appleContainerDefaultMemory (helpers.go) is the authority for the formula this repeats.
// It carries no comma: the briefing joins the limits with ", ".
const appleContainerDefaultMemoryDesc = "half of host RAM (min 4g)"

// briefedResourceLimits is the applied limits as the briefing states them, keyed by config
// key. Nil when there is nothing to say, which is what keeps the resources line conditional.
//
// limitPipelineDefault is dropped, and that is a deliberate omission rather than an
// oversight: podman passes `--pids-limit 32768` to every jail ever launched, so including
// it would add a standing line to every existing briefing to report a constant. What the
// line must never do is the opposite — claim a limit the backend never passed.
func briefedResourceLimits(rt string, resCfg *jsonx.OrderedMap) map[string]any {
	out := map[string]any{}
	for _, lim := range appliedResourceLimits(rt, resCfg, func() string { return appleContainerDefaultMemoryDesc }) {
		if lim.source == limitPipelineDefault {
			continue
		}
		out[lim.key] = lim.value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
