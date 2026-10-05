package run

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
// The second cluster below (appliedNetMode, appliedResourceLimits; the context-mount list
// is decided per entry in ctxmounts.go)
// clears that bar for a reason worth naming: their two call sites are the ARGV and the
// BRIEFING, and a briefing composed from the config instead of from what the launch
// applied is §6's defect — a jail that told the agent something untrue, which is worse
// than a jail missing a capability because an agent plans around it. Both consumers now
// read one answer, so the divergence is unrepresentable rather than fixed case by case.

// acROBindsFloor is the Apple Container version from which `:ro` is HONORED, and it is a
// measurement rather than a changelog reading.
//
// ⚠ THIS BELIEF WAS INVERTED BY THE FIRST CI RUN THAT COULD TEST IT (2026-09-14, macOS
// 26.5 arm64, `container` 1.1.0). The tree said, in this file and three others, that
// Apple Container "accepts `-v src:dest:ro` and IGNORES the suffix" — apple/container#889,
// last observed on 0.12.3. TestAppleContainerIgnoresReadOnlyBinds had never once executed
// (its `imageExists("container")` helper could not speak that CLI, so it skipped every
// run); the moment it did, it reported `MEASURED: Apple Container HONORED a :ro bind.`
//
// A VERSION FLOOR RATHER THAN A FLIP, and the asymmetry is the whole argument. Getting
// this wrong in the "supported" direction hands an agent WRITE access to a host directory
// the user granted read-only — silently, because the mount succeeds either way. Getting it
// wrong in the "unsupported" direction costs a feature and says so. So an unparseable
// version, an absent CLI, or anything below the floor stays refused: this is the same
// stance the repo takes wherever it cannot interrogate a runtime — decline rather than
// assume.
//
// The floor is 1.1.0 because that is what was measured. The release that actually fixed
// #889 is somewhere in (0.12.3, 1.1.0] and nobody here knows which; a lower floor would be
// a guess in the dangerous direction.
const acROBindsFloor = "1.1.0"

// roBindsUnsupported reports why a backend cannot honor a read-only bind mount,
// or "" when it can.
//
// Apple Container BELOW acROBindsFloor accepts `-v src:dest:ro` and ignores the suffix,
// which is the dangerous failure mode rather than the annoying one: the mount succeeds, so
// nothing looks wrong, and the agent holds write access to a host directory the user
// granted as read-only. Both callers therefore refuse the mount rather than downgrade it —
// there is no read-only bind to fall back to, and handing over a writable one on a backend
// the user picked for isolation is not a degradation anyone consented to.
//
// macos-user is deliberately absent: it has no bind mounts at all, so a `:ro` question
// does not arise there. What it delivers by copy instead is write-protected by its Seatbelt
// profile (macosuser.ResolveHomeReadonly).
func (o *Options) roBindsUnsupported(rt string) string {
	if rt != "container" { // parity: NotApplicable — only AC ever ignored :ro; podman honors it and macos-user has no binds
		return ""
	}
	v, ok := o.appleContainerVersion()
	if ok && versionAtLeast(v, acROBindsFloor) {
		return ""
	}
	detail := "this version ignores read-only (:ro), so it would be writable"
	if !ok {
		detail = "yolo could not read `container --version`, so it cannot tell whether " +
			"this version honors read-only (:ro) — declining rather than risking a " +
			"writable mount"
	} else {
		detail = "Apple Container " + v + " ignores read-only (:ro), so it would be " +
			"writable (honored from " + acROBindsFloor + ")"
	}
	return detail + ". Use `YOLO_RUNTIME=podman` for read-only context mounts."
}

// appleContainerVersion returns the `container` CLI's version, memoized for this launch.
//
// MEMOIZED because four call sites ask (the argv twice, the host-mount grants, and the
// BRIEFING), and they must agree — a briefing composed from a different answer than the
// argv applied is backend-parity.md §6's defect, a jail told something untrue. One probe,
// one answer, whatever order they run in.
func (o *Options) appleContainerVersion() (string, bool) {
	if o.acVersion != nil {
		return o.acVersion.v, o.acVersion.ok
	}
	v, ok := "", false
	if bin, found := o.LookPath("container"); found {
		res := o.Exec([]string{bin, "--version"}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			v, ok = parseAppleContainerVersion(res.Stdout), true
			ok = v != ""
		}
	}
	o.acVersion = &acVersionProbe{v: v, ok: ok}
	return v, ok
}

// acVersionProbe caches appleContainerVersion's answer, including a FAILED one — a probe
// that could not answer must not be retried three more times per launch and must not read
// as a different answer the second time.
type acVersionProbe struct {
	v  string
	ok bool
}

// acVersionRe pulls the dotted version out of `container CLI version 1.1.0 (build: …)`.
var acVersionRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+){1,2})`)

// parseAppleContainerVersion extracts the version, or "" when the line does not carry one.
func parseAppleContainerVersion(out string) string {
	return acVersionRe.FindString(strings.TrimSpace(out))
}

// versionAtLeast compares dotted numeric versions, shorter treated as zero-padded.
// Non-numeric components make it answer false, which is the fail-closed direction.
func versionAtLeast(have, floor string) bool {
	hp, fp := strings.Split(have, "."), strings.Split(floor, ".")
	for i := 0; i < len(hp) || i < len(fp); i++ {
		h, f := 0, 0
		var err error
		if i < len(hp) {
			if h, err = strconv.Atoi(hp[i]); err != nil {
				return false
			}
		}
		if i < len(fp) {
			if f, err = strconv.Atoi(fp[i]); err != nil {
				return false
			}
		}
		if h != f {
			return h > f
		}
	}
	return true
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
// macos-user is NOT absent, and the comment that used to stand here saying it was
// ("Run() returns before runContainer, so neither caller ever sees that runtime") named
// a call site that does exist: refreshJailBriefings runs on the macos-user arm of Run,
// above the dispatch, which is what made DP-B3 a false sentence rather than an absence.
// That backend shares the launcher's stack by construction (sharesLauncherNetns), so this
// answers "host" for it and both port sections fall away with the bridge paragraph.
func appliedNetMode(rt, netMode string, inContainer bool) string {
	if rt == "container" { // parity: Warned — AC takes no --net selector; an explicit `network.mode: host` warns
		return "bridge"
	}
	if sharesLauncherNetns(rt, netMode, inContainer) {
		return "host"
	}
	return netMode
}

// THE CONTEXT-MOUNT LIST IS NO LONGER FILTERED HERE. appliedCtxMounts used to drop the
// briefing's context mounts on a backend that did not bind them: every read-only one on an
// Apple Container below the `:ro` floor, and every one on macos-user, which bound none
// (DP-B1 / DP-L7). Both halves left: the `:ro` floor is applied PER ENTRY by the deciders the
// argv uses (configCtxMounts, packCtxMounts; CX-D13), and macos-user delivers its context
// mounts by link and briefs exactly what its own decider delivers (briefedCtxMounts →
// macosCtxLinks; docs/design/context-mounts.md §4 step 4). With nothing left to filter, the
// function went with them.

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

	if rt == "container" { // parity: HonoredBy — AC caps by its own host-derived defaults, so "unconfigured" is not "uncapped"
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
	if rt != "container" { // parity: Dropped — AC passes no pids limit; backend-parity.md §5.1 rules the sub-key warning out as noise
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

// appliedIOPriority is the disk I/O priority this launch applies, which is not always the one
// the config declares: podman on a Linux host passes it to the entrypoint, nested jails
// included, and the macos-user launcher sets it on itself as a process disk policy
// (docs/design/io-priority.md §5.2, IO-D2; §5.5, IO-D7). It feeds the podman argv
// (ioPriorityEnvArgs) and the fresh launch's briefing, so the agent is told a priority exactly
// where one is applied — the same argv/briefing pairing as appliedResourceLimits, and for the
// same reason. An attach briefs launchedIOPriority instead: its config is the current one.
//
// Apple Container and podman on a macOS host apply nothing: the jail runs in a VM and its
// workspace reaches the Mac over VirtioFS, whose protocol has no priority field, so a class
// set in the VM never reaches the Mac's disk for build output. noteIOPriority says so at
// launch (Warned). macos-user has no VM in the way: the agent's I/O is the Mac's own, and the
// policy the orchestrator sets before the bootstrap is inherited by every process of the
// session; its failure line is the orchestrator's.
func appliedIOPriority(rt string, isMacOS bool, resCfg *jsonx.OrderedMap) ioprio.Priority {
	if rt == "macos-user" { // parity: HonoredBy — setiopolicy_np on the macos-user launcher, inherited by every process it starts, where podman on Linux uses the entrypoint's ioprio_set
		return ioprio.FromResources(resCfg)
	}
	if rt != "podman" || isMacOS { // parity: Warned — AC and podman on macOS cross VirtioFS, which carries no priority; noteIOPriority says so
		return ioprio.Normal
	}
	return ioprio.FromResources(resCfg)
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
	// NO KERNEL CAP EXISTS ON macos-user, so this line states none (DP-B6 / DP-L8). That
	// backend passes no limit flag — there is no container to cap — and this line calls its
	// numbers "kernel-enforced" and points at `yolo-cglimit`, which has no delegate to talk
	// to there: the agent was once told that while the launch told the HUMAN the opposite.
	// What each key does on macos-user instead (`io` a disk policy, `memory` a sampled guard,
	// `cpus` parallelism defaults, `pids_limit` nothing) the agent reads in the briefing's
	// packages section and Disk I/O line, and the human in the launch's own lines. The
	// argv-side appliedResourceLimits is deliberately left alone — it is never reached on
	// this backend, and a briefing-only defect is fixed in the briefing's own projection.
	if inStrSlice(paths.NativeRuntimes, rt) { // parity: Warned — macos-user caps nothing in the kernel; the briefing's packages section and the launch's lines say what each key does instead
		return nil
	}
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
