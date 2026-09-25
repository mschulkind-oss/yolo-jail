package run

// loopholeinert.go is the INERT REPORT: one line when a selected pack's loophole will do
// nothing this launch, whether the reason is the BACKEND or the PLATFORM
// (docs/reference/loophole-system.md#where-a-loophole-does-nothing).
//
// # This is the B-0 rule applied to a new kind
//
// run.go records B-0 as "a backend that looked provisioned and configured nothing", and the
// whole run pipeline was restructured to end it. ONE shipped backend makes the loophole kind
// a silent no-op, and that skip is WIDER than draft 1 of the design claimed: on Apple
// Container, startLoopholes admits ONE name — openai-auth-broker — and skips every other
// pack-shipped host daemon, intercepting or not. A different skip from the container-ARGS one
// draft 1 cited (loopholes/runtime.go's `intercepts` check, which only drops --add-host).
//
// ⚠ THIS SAID startLoopholes "returns nil for rt == \"container\" BEFORE any external service
// starts", which is FALSE of the code it describes and is retracted rather than reworded: the
// allow-list in loopholesruntime.go has admitted the credential broker since long before this
// report existed, so a reader who believed this went looking for a return that is not there.
// The REPORT is unaffected — backendInertReason keys on the backend, not on the allow-list, so
// the admitted loophole gets an inert line too, which is correct because what fails there is
// the DIAL and not the spawn (36c47baa deleted the exemption that used to hide it).
//
// So a pack whose whole purpose is a loophole could be installed, selected, and completely
// inert, with the jail reporting a successful launch.
//
// ⚠ THERE WERE TWO SUCH BACKENDS UNTIL THE LIFECYCLE WAS GENERALISED, and macos-user's entry
// is gone because the gap it named is CLOSED rather than because it became inconvenient. It
// read "the branch returns from Run() long before startLoopholes is reached, so the kind is
// inert on that backend ENTIRELY" — true of an arm that started one credential service by
// hand, and false of one that goes through startLoopholesDisclosed like every container
// launch (run.go's macos-user arm). What the report says there now is the PLATFORM axis
// alone, which is the axis that still has something to say on a Mac: `audio`, `journal`,
// `host-processes` and `cgroup-delegate` all declare `platforms: ["linux"]`. Keeping the
// backend line would be the failure loopholeinert.go's own rule names one function down — a
// warning that describes a closed gap teaches the reader to distrust the ones still true.
//
// # ONE MECHANISM, TWO AXES
//
// §3.1 is explicit that the platform declaration and the inert-backend report share one
// mechanism, because platform (darwin vs linux) and backend (container today; macos-user
// until its lifecycle was generalised) are two axes with ONE answer shape: "this loophole
// does nothing here, and here is why." Two half-messages for one user-visible situation is
// how B-0 happened in the first place.
//
// The platform half is the PRODUCER's whole answer — loopholes.PlatformInertNotes, selection
// included, not just its rendering. That is a correction: for a batch this file did its own
// selection over its own manifest read while the producer had no callers, and the two
// disagreed about a duplicate (2 lines vs 1) and about a disabled loophole (1 line vs 0). One
// mechanism has to mean one selection, or "shares one mechanism" is a statement about the
// sentence shape rather than about the answer.

import (
	"strings"

	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// backendInertReason says why a backend runs NO loophole host service, or "" when it does.
//
// ONE backend answers non-empty, and its answer is wider than draft 1 of the design claimed:
// on container (Apple Container), startLoopholes admits ONE name — openai-auth-broker — and
// skips every other pack-shipped host daemon, intercepting or not. That is a different skip
// from the container-ARGS one (loopholes/runtime.go's `intercepts` skip), which only drops
// --add-host.
//
// ⚠ THE ADMITTED ONE IS REPORTED INERT TOO, and that is deliberate rather than an
// over-report: its daemon really does start and really does write its endpoint file across a
// bind this backend mounts fine, and the jail still cannot DIAL it. See the header for the
// "returns nil" retraction this paragraph used to carry.
//
// ⚠ macos-user ANSWERED TOO UNTIL THE LIFECYCLE WAS GENERALISED, on the grounds that its arm
// "returns from Run() long before startLoopholes is reached". That arm now goes through
// startLoopholesDisclosed, so the reason expired with the structure it described and the case
// is deleted rather than reworded: a backend that starts every host service has no
// backend-shaped reason to give, and leaving one would report a pack inert in the same launch
// whose exec disclosure names its daemon. The file header carries the longer note.
//
// This is the B-0 rule applied to a new kind — run.go records B-0 as "a backend that looked
// provisioned and configured nothing", and the pipeline was restructured to end it. A pack
// whose whole purpose is a loophole must not look installed on a backend that ignores it.
func backendInertReason(rt string) string {
	switch rt {
	case "container":
		// ⚠ THE REASON CHANGED ON 2026-09-15 AND THE OLD ONE WAS WRONG. This said "no socket
		// bind-mount there", which was true of the unix-socket era and of nothing shipped:
		// MOST shipped loopholes declare `transport: loopback-tls` and reach the host over the
		// NETWORK, learning their endpoint from a file in a bind-mounted DIRECTORY, which this
		// backend mounts fine. OQ-BP-4 ruled the reason stale and asked for a measurement
		// before lifting the skip.
		//
		// ⚠ THAT USED TO READ "four of six shipped loopholes" AND THE COUNT IS DELETED RATHER
		// THAN INCREMENTED. It was true when written; `hello-daemon` (2026-09-17) and
		// `aws-auth` (2026-09-18) landed within the week and made it six of NINE, and a count
		// in a comment is one more thing to keep true every time a pack ships a loophole —
		// AGENTS.md's rule for the cmd/ table, applied to the same failure one file over.
		// `rg -l '"transport": "loopback-tls"' packs/*/loopholes/*/manifest.jsonc` is the list.
		// OQ-BP-4's ledger entry in docs/design/backend-parity.md keeps the original four by
		// name, which is correct there: it records what was measured on 2026-09-14.
		//
		// The measurement CONFIRMED the skip and replaced its reason. On `container` 1.1.0
		// nothing crosses container→host at ANY bind address, and the two failures are NOT the
		// same one twice:
		//
		//   - a listener bound to the Mac's 127.0.0.1 — what svcendpoint.Listen actually binds
		//     — is never reached at all: every candidate got ECONNREFUSED, and the host side
		//     accepted no peer. Nothing forwards this host's loopback, the way rootless
		//     podman's pasta does with --map-host-loopback.
		//   - a WIDER bind (0.0.0.0, or the vmnet bridge address) does complete a handshake and
		//     then carries nothing, by two alternating mechanisms (ENOTCONN on a just-accepted
		//     socket; a NAT-answered CONNECT to a port nothing accepts).
		//
		// So no bind address helps, but do not compress that into "bridge and wildcard die
		// like 127.0.0.1" — this comment did, and it hid the one result the follow-up pricing
		// turns on: the cheap remedy the test's own `hits` branch prices (bind the bridge
		// address only) is refuted by the TEARDOWN, not by the refusal. `host.containers
		// .internal` does not resolve there either. integration/applecontainer_test.go's
		// TestAppleContainerReachesHostLoopback is the witness, and it is bounded:
		// container→internet works, Mac→container works.
		//
		// So this is narrower than it was and it will EXPIRE with an upstream release rather
		// than standing forever. Re-run that test before believing it still holds.
		return "inert on this backend — Apple Container carries no container→host connection " +
			"(measured on 1.1.0: a loopback-bound listener is never reached, and a wider bind " +
			"completes a handshake that carries nothing), so a loopback-tls loophole could " +
			"not be reached from the jail this launch"
	}
	return ""
}

// notePackLoopholesInert prints ONE line per pack-shipped loophole that will do nothing this
// launch, naming the axis that made it inert.
//
// ONE MECHANISM, TWO AXES (§3.1, §8). Platform (`darwin` vs `linux`) and backend
// (`container` — the one backend that still starts nothing) both answer "this loophole does
// nothing here, and here is why", and the design is explicit that splitting them would
// produce two half-messages for one user-visible situation.
//
// BACKEND BEATS PLATFORM when both apply, and that is not arbitrary: an inert backend
// starts no host service whatever the platform says, so the platform answer would be a
// second reason for one outcome. The line the user needs is the one they can act on — and
// on an inert backend that is "switch backends", not "get a different machine".
//
// The platform half is read through internal/loopholedecl (the schema's own
// PlatformsUnsupportedReason), never re-implemented here: two matchers over one declaration
// is how a report and a gate come to disagree. A manifest that will not parse prints
// nothing — the discovery layer's contract is warn-and-continue and it already warns, and a
// second complaint from the launch path about the same file would read as a second bug.
//
// ONE MECHANISM MEANS ONE SELECTION, NOT JUST ONE RENDERING. The rendering converged on
// InertNote.Line() a batch before the selection did, and the gap was measurable: this
// function walked pack loopholes itself and called a private platform reader, while
// loopholes.PlatformInertNotes — whose doc comment states the dedup and the disabled-skip as
// REQUIREMENTS — had zero production callers. Given one loophole declared twice the producer
// emitted ONE note and this report emitted TWO identical lines; given `enabled: false` plus a
// wrong platform the producer emitted ZERO and this report emitted one. Same shape, different
// answers, which is what "one mechanism" is supposed to make impossible.
//
// So the PLATFORM axis is now the producer's answer, whole: this function resolves each pack
// module to a loopholes.Loophole and hands the slice to PlatformInertNotes. The dedup and the
// disabled-skip are the same CODE, not the same intent.
//
// THE BACKEND AXIS KEEPS ITS OWN SELECTION, and the asymmetry is the design's: an inert
// backend is a statement about the LAUNCH ("nothing any of this runs here"), so it applies to
// a disabled loophole too — a pack whose whole purpose is a loophole must not look installed
// on a backend that ignores it (B-0). Only the platform axis is a per-loophole property the
// user's own switch can preempt.
func (o *Options) notePackLoopholesInert(rt string, packs []*packload.Pack, cfg *jsonx.OrderedMap) {
	if backend := backendInertReason(rt); backend != "" {
		// CONFIG-DECLARED loopholes are inert on these backends too, and this report
		// walked packs only — so a user whose `loopholes.<name>.command` names a daemon
		// got no line at all. The AC skip drops both sources (SourcePack and
		// SourceConfig); reporting one of them made the silence look deliberate.
		o.printInertLines(append(backendInertLines(packs, backend), configInertLines(cfg, backend)...))
		return
	}
	o.printInertLines(platformInertLines(packs, cfg))
}

// configInertLines is one line per `loopholes.<name>` entry in the user's own config on a
// backend that starts none of them — the SourceConfig half of the same report.
//
// Keyed on presence rather than on Active(): resolving whether a config-declared daemon
// would have run requires probing its `requires`, and on a backend that starts nothing the
// answer cannot change the outcome. Same reasoning backendInertLines gives for not reading
// pack manifests.
func configInertLines(cfg *jsonx.OrderedMap, reason string) []string {
	section := cfgMap(cfg, "loopholes")
	if section == nil {
		return nil
	}
	var lines []string
	for _, name := range section.Keys() {
		lines = append(lines, inertLineFor("your config", loopholes.InertNote{
			Name: name, Axis: loopholes.AxisBackend, Reason: reason,
		}))
	}
	return lines
}

// backendInertLines is one line per pack-shipped loophole on an inert backend.
//
// Deduped by (pack, loophole) for the same reason the platform producer dedups by name — one
// mistake, one line — but keyed on the pair, because this report's line is prefixed with the
// pack and two packs shipping one name is a distinct (and separately refused) situation.
//
// It does NOT read the manifests. A backend that starts no host services says nothing about
// what any manifest declares, so resolving them would make the answer depend on a file whose
// contents cannot change it — and an unreadable manifest would then silence a line that is
// true regardless.
func backendInertLines(packs []*packload.Pack, reason string) []string {
	var lines []string
	seen := map[string]bool{}
	for _, p := range packs {
		for _, lp := range packLoopholes(p) {
			key := lp.Pack + "\x00" + lp.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			lines = append(lines, inertLineFor(lp.Pack,
				loopholes.InertNote{Name: lp.Name, Axis: loopholes.AxisBackend, Reason: reason}))
		}
	}
	return lines
}

// platformInertLines is the PRODUCER's answer, prefixed with the pack that shipped each
// loophole.
//
// The pack name is prefixed here rather than folded into the note because it is a fact about
// THIS report's context (which selected pack shipped it) and not about the loophole —
// `yolo loopholes list` renders the same note for a hand-placed loophole that has no pack at
// all. The note→pack mapping is built from the resolved records, so a note the producer DROPS
// (a duplicate, a disabled loophole) contributes no line by construction rather than by this
// function also remembering to drop it.
//
// THE USER'S SWITCH, NOT THE AUTHOR'S: resolveInertLoophole reads the manifest alone, so its
// Enabled is `default_enabled`. ApplyConfigEnabled lays the merged config's
// `loopholes.<name>.enabled` over it before the producer's disabled-skip reads it — without
// that, a Linux-only loophole the user switched ON on a Mac drew no line (G15).
func platformInertLines(packs []*packload.Pack, cfg *jsonx.OrderedMap) []string {
	var resolved []*loopholes.Loophole
	packOf := map[string]string{}
	for _, p := range packs {
		for _, decl := range packLoopholes(p) {
			lp := resolveInertLoophole(decl.Dir)
			if lp == nil {
				continue
			}
			if _, seen := packOf[lp.Name]; !seen {
				packOf[lp.Name] = decl.Pack
			}
			resolved = append(resolved, lp)
		}
	}
	var lines []string
	loopholes.ApplyConfigEnabled(resolved, cfgMap(cfg, "loopholes"))
	for _, note := range loopholes.PlatformInertNotes(resolved) {
		lines = append(lines, inertLineFor(packOf[note.Name], note))
	}
	return lines
}

// printInertLines emits the report, sorted, to stderr. Sorted because the lines are assembled
// from a map-free walk of two nested slices whose order is the config's, and a launch notice
// that reorders between launches for one unchanged config reads as churn.
func (o *Options) printInertLines(lines []string) {
	if len(lines) == 0 {
		return
	}
	sort.Strings(lines)
	out := o.pr(o.Stderr)
	for _, l := range lines {
		out.print("[yellow]" + l + "[/yellow]")
	}
}

// resolveInertLoophole loads one module dir as a resolved record for the platform producer,
// returning nil when it cannot be read.
//
// TOLERANT (loopholes.LoadLoophole → LoadDirTolerant), matching every other cross-version
// manifest read: a manifest carrying a key only a newer build knows must not make this report
// claim the loophole is fine, nor make it shout. An unreadable manifest yields nothing here —
// the discovery layer's contract is warn-and-continue and it already warns about the same
// file; a second complaint from the launch path would read as a second, unrelated bug.
//
// It goes through the loopholes package rather than decoding the manifest here, because the
// producer takes RECORDS and the record is where `enabled` and the evaluated `platforms`
// declaration both live. Reading the manifest directly is what let this report skip the
// disabled check: a *loopholedecl.Manifest answers "which platforms" and a *Loophole answers
// "is this loophole inert here", which is the question being asked.
func resolveInertLoophole(dir string) *loopholes.Loophole {
	lp, err := loopholes.LoadLoophole(dir)
	if err != nil {
		return nil
	}
	return lp
}

// inertLineFor renders what THIS report prints for one (pack, loophole, reason), so a test can
// match a whole line rather than guess at a substring.
//
// A function rather than a marker constant, because after the convergence onto
// loopholes.InertNote there is no single interesting substring left to key on: the two axes
// share only Line's fixed "loophole <name> is " prefix, and a marker taken from either axis's
// reason clause would silently stop matching the other — the drift the single rendering exists
// to prevent. Producing the exact line from the same inputs the report uses cannot drift at
// all.
func inertLineFor(pack string, note loopholes.InertNote) string {
	return pack + ": " + note.Line()
}

// noteMachineWideWorkspaceState is GONE, and its absence is the record that the defect it
// described was fixed rather than forgotten.
//
// It named every pack `state` dir at scope:workspace and said they were shared by every
// workspace on the machine, because SandboxHome() is a constant. They are not, since the
// home-tier layout: each one is a symlink into <workspace>/.yolo/home, the same sidecar the
// container backends bind from (entrypoint.DeriveDarwinHomeLayout,
// docs/design/macos-user-home-tiers.md). A warning that describes a closed gap is worse than
// no warning — it teaches the reader to distrust the ones that are still true — which is the
// rule noteMacosUserContentGaps below was already rewritten under.

// noteMacosUserContentGaps names the two content pipelines that never reach this
// backend. Both are host-side steps inside runContainer, which the macos-user arm
// returns before — the same B-0 shape as pack staging and launch flags, but with a
// fix that is a delivery mechanism rather than a moved call, so it warns for now.
//
// SKILLS AND BRIEFINGS ARE NOW DELIVERED (2026-09-03), by composing the same trees the
// container path composes and copying them over the sandbox home instead of mounting
// them (macoshomeoverlay.go). What survives is a DIFFERENT and smaller statement, and
// this function now makes it: the copy is writable where a bind is `:ro`.
//
// It shrank AGAIN with the home-tier layout. The second half — "a concurrent second
// workspace replaces what this one delivered" — was true of one machine-wide home and is
// not true of a destination that is a symlink into <workspace>/.yolo/home. What is left is
// the one difference the layout cannot close, because it is about the enforcement primitive
// rather than the location: a copy is writable and a bind is not.
//
// The text this replaced said the agent "starts with no AGENTS.md/CLAUDE.md and no
// skills". Leaving it would be the worse failure of the two available: a warning that
// describes a gap yolo has closed teaches the reader to distrust the warnings that are
// still true.
func (o *Options) noteMacosUserContentGaps(packs []*packload.Pack, cfg *jsonx.OrderedMap) {
	if len(packs) > 0 {
		o.pr(o.Stderr).print("[yellow]Note: briefings and skills are delivered by COPY on macos-user[/yellow] — " +
			"every other backend mounts them read-only, so here the agent can edit its own " +
			"skills and briefing, and the next launch overwrites them again.")
	}
	// ⚠ TWO WARNINGS WERE RETIRED HERE ON 2026-09-12, and what retired them is that the
	// gap they named is closed rather than that they became inconvenient.
	//
	// One said `mise_tools` are NOT installed on macos-user, on three stated grounds:
	// nothing provides a `mise` binary the sandbox can reach, nothing runs
	// `mise install`, and the sandbox home has no mise data dir. All three are now
	// false. The floor puts mise on the sandbox's PATH
	// (docs/design/macos-user-provisioning.md §9), MISE_DATA_DIR names a real
	// machine-wide store (macosuser.SandboxMiseData), and the confined provisioning
	// stage runs `mise install` before the agent starts (macosuser.ProvisionSetup).
	//
	// The other said `lsp_servers` CONFIG renders but the binaries never install,
	// because the installer is a generated bootstrap script "the container path runs and
	// this backend deliberately does not". That script is now generated here too
	// (entrypoint.GenerateDarwinBootstrapScript) and the stage execs it — and since the LSP
	// recipe table's deletion (docs/reference/mcp-configuration.md#oq-lsp1) no backend
	// installs a language server at all, so the property is shared rather than a gap.
	//
	// Leaving either would be the failure the note above this function names: a warning
	// that describes a gap yolo has closed teaches the reader to distrust the warnings
	// that are still true — and the ones below are still true.
	//
	// What is NOT retired is `mcp_presets`, which really is still undelivered here: the
	// preset wrappers are Linux-absolute, so the bootstrap skips them and warns from
	// inside itself (entrypoint.RunDarwinBootstrap), and the stage installs none of the
	// npm packages behind them either (Env.SkipMCPPresets).
	//
	// ⚠ THE HOST-BYTE WARNINGS USED TO BE CALLED FROM HERE and are now a separate call
	// on the arm, below the composition they describe. That is not tidying: what they
	// have left to say depends on what the launch actually staged (DP-L1), and this
	// function runs long before the context tree is composed. A printer that ran first
	// would be back to describing the config rather than the delivery, which is the
	// whole failure mode both of them were written under.
}

// noteMacosUserHostByteGaps names what carries HOST BYTES into a config surface and did
// NOT cross on this launch. Since DP-L1 that is one shape only, and the shrinking is the
// story of this function rather than a detail of it.
//
// ⚠ TWO WARNINGS WERE RETIRED HERE ON 2026-09-13, and what retired them is that the gap
// they named is CLOSED rather than that they became inconvenient. One said pack
// `reads-host` grants do not cross, so each surface renders from its DEFAULTS layer and
// "the agent gets a working config file that is not yours". The other said source-bearing
// `host_files` entries are dropped from the wire entirely. Both were true because the
// bytes crossed on a /ctx mount and this backend has none; both are now false, because
// the bytes cross by COPY into a root-owned tree under /var/yolo-jail
// (internal/cli/run/macosctxtree.go, macosuser.StageCtxCommands). Leaving either would be
// the failure this file's own rule names: a warning that describes a gap yolo has closed
// teaches the reader to distrust the warnings that are still true.
//
// ⚠ AND THE CARVE-OUT THEY PROPPED UP IS GONE WITH THEM. The old text said this warning
// was "half of why the jail does not refuse here": the jail's host-layer read fails closed
// (OQ-CO10), this backend reported `unsupported`, and that was defensible only while the
// deficiency was SAID. The report now says `supported` whenever a tree was staged
// (macosuser.hostLayerWire), so a delivered file that the jail cannot read REFUSES the
// launch here exactly as it does everywhere else. Nothing is being excused any more, so
// nothing has to be said to excuse it.
//
// WHAT SURVIVES is the directory-shaped `host_files` entry, which is DP-D15 rather than
// DP-L1: it names an arbitrary user tree, a copy does not scale to one, and the ruling
// there is that a delivery yolo cannot make is stated rather than half-performed. ONE
// LINE, only when the user declared one — a launch that declared none says nothing, which
// is what keeps this from being the warning OQ-BP-3 says people learn to skip.
func (o *Options) noteMacosUserHostByteGaps(delivery macosCtxDelivery) {
	if len(delivery.undeliveredDirs) == 0 {
		return
	}
	named := make([]string, 0, len(delivery.undeliveredDirs))
	for _, p := range delivery.undeliveredDirs {
		named = append(named, "~/"+p)
	}
	o.pr(o.Stderr).print("[yellow]Warning: a host_files entry whose `source` is a DIRECTORY " +
		"does not cross on macos-user[/yellow] — " + strings.Join(named, ", ") + ". This " +
		"backend has no bind mounts, so host bytes arrive by COPY, and a copy does not " +
		"scale to an arbitrary tree. Single FILE entries are delivered normally; split the " +
		"directory into the files you need, or use the Apple Container runtime " +
		"(runtime: \"container\"), which binds it read-only from Apple Container " +
		acROBindsFloor + " (older versions skip it with a warning).")
}

// noteMacosUserPlatformGaps names the three PLATFORM keys this backend reads nowhere:
// `devices`, `gpu` and `kvm` (docs/design/declaration-parity.md DP-B4, fixed by DP-L10).
//
// WHY IT IS NOT THE CONTAINER PATH'S SENTENCE, REUSED. run.deviceArgs, run.kvmArgs and
// assembleRunCmd's GPU line all warn on macOS already — and every one of them is reached
// only from run.assembleRunCmd, below the `rt == "macos-user"` return in run.Run, so on
// this backend all three keys were SILENT. Not merely unhonored: silent, while
// docs/guides/macos.md told the user each was "skipped with a warning" (DP-B36).
//
// The reason those strings could not simply be moved here is that they state a DIFFERENT
// FACT. "not supported on macOS" is a claim about the platform, and it is the container
// backends' honest answer: podman and Apple Container on macOS run a Linux VM that cannot
// see a host USB device. Here there is no container and no VM at all, so the key is not
// refused by a platform limit — it is READ BY NOTHING. Copying the container sentence
// would assert the wrong reason on the one backend whose reason is structural, which is
// the doc-drift shape §5.5 is a list of.
//
// ⚠ THE CALL SITE IS THE BACKEND GATE. This is called from the macos-user arm of run.Run
// and nowhere else, deliberately: hoisting it above the dispatch would double-warn on a
// macOS podman or Apple Container launch, where assembleRunCmd's own three warnings still
// fire. One backend, one printer.
//
// ONE LINE PER DECLARED KEY, and none for a key the config never mentions — so a user who
// declares nothing sees nothing, which is what keeps this from being the warning
// OQ-BP-3 says people learn to skip.
func (o *Options) noteMacosUserPlatformGaps(cfg *jsonx.OrderedMap) {
	out := o.pr(o.Stderr)

	if devs := cfgList(cfg, "devices"); len(devs) > 0 {
		out.print("[yellow]Warning: `devices` is not read on macos-user[/yellow] — " +
			strings.Join(deviceLabels(devs), ", ") + ". Device passthrough attaches a host " +
			"device to a CONTAINER, and this backend starts none; the sandboxed process " +
			"reaches devices under ordinary macOS permissions instead, so yolo neither " +
			"attaches nor restricts anything here.")
	}

	if gpuSec := cfgMap(cfg, "gpu"); gpuSec != nil && mapBoolOr(gpuSec, "enabled", false) {
		out.print("[yellow]Warning: `gpu.enabled` is not read on macos-user[/yellow] — " +
			"GPU passthrough is a CDI device plus NVIDIA/ROCm environment on a container, " +
			"and this backend starts none. yolo passes nothing through and gates nothing; " +
			"whatever the sandboxed process can reach through macOS, it reaches.")
	}

	if cfgTrue(cfg, "kvm") {
		out.print("[yellow]Warning: `kvm` is not read on macos-user[/yellow] — it asks for " +
			"/dev/kvm inside a container, and there is neither a container nor a /dev/kvm " +
			"on macOS.")
	}
}

// deviceLabels renders `devices` entries for a report the way run.deviceArgs renders them
// for a warning: the raw path, the USB description (or its id), or the cgroup rule.
//
// A shared LABELLING, not a shared sentence — see noteMacosUserPlatformGaps for why the
// sentences must differ. What a reader needs from both surfaces is the same: which entry
// of theirs is being talked about.
func deviceLabels(entries []any) []string {
	var out []string
	for _, devAny := range entries {
		switch dev := devAny.(type) {
		case string:
			out = append(out, dev)
		case *jsonx.OrderedMap:
			if usbV, ok := dev.Get("usb"); ok {
				label := pyStrCoerce(usbV)
				if d := mapStr(dev, "description"); d != "" {
					label = d
				}
				out = append(out, "usb "+label)
			} else if rule := mapStr(dev, "cgroup_rule"); rule != "" {
				out = append(out, "cgroup rule "+rule)
			}
		}
	}
	return out
}

// noteMacosUserPortKeys is the human half of DP-L2 (docs/design/declaration-parity.md
// §5.1.1 (2)): one stderr line per non-empty `network.ports` / `network.forward_host_ports`.
//
// WHAT IT PAIRS WITH. The AGENT already learns this — sharesLauncherNetns answers true for
// this backend, so appliedNetMode is "host", both port sections fall out of the briefing and
// backendLimits states the network fact. The human learned nothing at all, which
// backendlimits.go's header records as the one entry breaking its "one source, two
// renderings" rule. This is that rendering.
//
// REFUSED AS A KEY, NEVER AS A LAUNCH — run.roBindsUnsupported's shape (refuse the
// declaration, print the reason, continue). Its force does not carry, and the difference is
// worth knowing: refusing an Apple Container `:ro` mount REMOVES an exposure, whereas
// nothing here removes anything, because the sandboxed process binds host ports regardless.
// The message is the whole deliverable.
//
// ⚠ ONLY WHEN NON-EMPTY, and that is what makes this safe where a `network.mode` refusal
// would not be. Neither key has a default (run.NewDefaultOptions sets Network: "bridge",
// which is why `mode` is APPLIED as host rather than refused), so this cannot fire on a
// launch that never mentioned networking.
func (o *Options) noteMacosUserPortKeys(cfg *jsonx.OrderedMap) {
	netSec := cfgMap(cfg, "network")
	if netSec == nil {
		return
	}
	out := o.pr(o.Stderr)

	if ports := asAnyList(mapGet(netSec, "ports")); len(ports) > 0 {
		msg := "[yellow]Warning: `network.ports` is not honored on macos-user[/yellow] — " +
			strings.Join(portLabels(ports), ", ") + ". The sandbox runs on the launcher's " +
			"own network stack, so a port it binds IS published on this machine's real " +
			"interfaces — listed here or not. Nothing is mapped and nothing is confined " +
			"to a bind address."
		if remapped := remappedPorts(ports); len(remapped) > 0 {
			msg += " " + strings.Join(remapped, ", ") + " asks for a port REMAP, which " +
				"needs a second stack to land on and cannot be delivered at all: the " +
				"process is reachable on the port it binds."
		}
		out.print(msg)
	}

	if fwd := asAnyList(mapGet(netSec, "forward_host_ports")); len(fwd) > 0 {
		msg := "[yellow]Warning: `network.forward_host_ports` is not honored on " +
			"macos-user[/yellow] — " + strings.Join(portLabels(fwd), ", ") + ". There is " +
			"no hop to make: the sandbox is already on this machine's stack, so " +
			"`localhost:<port>` inside it is this machine's port."
		if remapped := remappedPorts(fwd); len(remapped) > 0 {
			msg += " " + strings.Join(remapped, ", ") + " asks for a port REMAP, which " +
				"needs a second loopback to land on and is not delivered."
		}
		out.print(msg)
	}
}

// portLabels renders port entries as the user wrote them.
func portLabels(entries []any) []string {
	var out []string
	for _, e := range entries {
		out = append(out, pyStrCoerce(e))
	}
	return out
}

// remappedPorts names the entries whose two port numbers DIFFER — the only entries that are
// not vacuously satisfied by a shared stack (§5.1.1's entry-form table).
//
// ONE CLASSIFIER FOR BOTH KEYS, and it is correct for both despite their opposite orders:
// `ports` is [IP:]HOST:JAIL and `forward_host_ports` is JAIL:HOST, but this asks only
// whether the two numbers differ, which is order-free. An entry with one number, or with a
// non-numeric field, is not a remap and is not named.
func remappedPorts(entries []any) []string {
	var out []string
	for _, e := range entries {
		s := pyStrCoerce(e)
		fields := strings.Split(s, ":")
		if len(fields) < 2 {
			continue
		}
		a, b := fields[len(fields)-2], fields[len(fields)-1]
		if a != b && isAllDigits(a) && isAllDigits(b) {
			out = append(out, s)
		}
	}
	return out
}

// isAllDigits reports whether s is a non-empty run of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
