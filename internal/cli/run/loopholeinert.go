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
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
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

// platformInertLines is the PRODUCERS' answer — the platform axis, then the binary axis —
// prefixed with the pack that shipped each loophole.
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
	// A declared build not fetched yet (loopholes.BinaryInertNotes, the binary axis): the
	// launch never fetches (docs/design/broker-as-a-pack.md BP-D1), so without this line a
	// loophole the user switched on would simply not run, with nothing said. The producer
	// skips what the platform producer above already reported.
	for _, note := range loopholes.BinaryInertNotes(resolved) {
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
// rule noteMacosUserContentGaps was rewritten under before it, too, was retired (below).

// noteMacosUserContentGaps is GONE (G14, 2026-09-27), and its absence is the record that the
// gap it named was CLOSED rather than that the line became inconvenient.
//
// Its last text was "briefings and skills are delivered by COPY on macos-user — every other
// backend mounts them read-only, so here the agent can edit its own skills and briefing". They
// are still delivered by copy (macoshomeoverlay.go), and the copy is now write-protected by the
// session's own Seatbelt profile: every write, rename and unlink of a delivered skills dir or
// briefing is denied at the physical path the kernel sees, and the chain above each one may not
// be moved aside or replaced (macosuser.ResolveHomeReadonly, homeReadonlyDenies). That is the
// container's `:ro` done by the kernel's policy instead of by a mount, which the gap tracker
// records as HonoredBy — a disposition that owes the launch no line. Leaving the sentence would
// be the failure this file's own rule names: a warning that describes a gap yolo has closed
// teaches the reader to distrust the warnings that are still true.
//
// ⚠ WHAT LINUX CANNOT SAY is whether the kernel honors the rule. The policy suite
// (integration/macosuserseatbelt_test.go) and a real launch (macosusercontent_test.go) ask it on
// a Mac; until one of them has run green, the profile's text is what is pinned.
//
// The function had shrunk twice before. The concurrent-workspace half of its warning went with
// the home-tier layout, which made each destination a symlink into <workspace>/.yolo/home. And
// on 2026-09-12 two sibling warnings were retired from here for gaps that closed: `mise_tools`
// (the floor puts mise on the sandbox PATH and the confined provisioning stage runs
// `mise install`) and `lsp_servers` (no backend installs a language server since
// docs/reference/mcp-configuration.md#oq-lsp1). What still warns is `mcp_presets`, from inside
// the bootstrap (entrypoint.RunDarwinBootstrap), because the preset wrappers are Linux-absolute.

// noteMacosUserPlatformGaps names what this backend does with the PLATFORM keys — `devices`,
// `gpu`, `kvm` and `ephemeral_storage` (docs/design/declaration-parity.md DP-B4, fixed by DP-L10;
// DP-B5) — and discloses the one host editor config a container launch delivers and this one
// does not.
//
// `devices` IS HALF READ since 2026-10-04: a raw-path entry under /dev gets its control calls
// back in the Seatbelt profile (macosuser.DeviceIoctlPaths, the ONE classifier the profile reads
// too), and that is DISCLOSED, unsuppressibly, because it widens the sandbox. A raw-path entry the
// classifier refuses is warned with its next step, and the USB and cgroup forms, which name no
// node, are still read by nothing.
//
// WHY IT IS NOT THE CONTAINER PATH'S SENTENCE, REUSED. run.deviceArgs, run.kvmArgs and
// assembleRunCmd's GPU line all warn on macOS already — and every one of them is reached
// only from run.assembleRunCmd, below the `rt == "macos-user"` return in run.Run, so on
// this backend all three keys were SILENT. Not merely unhonored: silent, while
// userguide/guides/macos.md told the user each was "skipped with a warning" (DP-B36).
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
// ONE BLOCK PER DECLARED KEY, and none for a key the config never mentions — so a user who
// declares none of these keys hears nothing about them, which is what keeps this from being
// the warning OQ-BP-3 says people learn to skip. `gpu`, `kvm` and `ephemeral_storage` print
// one warning each, under the condition beside it. `devices` prints up to three kinds of line:
// one disclosure naming every raw path the profile carves out, one warning per entry the
// classifier refuses, and one warning for the USB and cgroup forms together.
//
// The ONE line not keyed on the config is the host nvim disclosure, which depends on the HOST:
// it prints whenever ~/.config/nvim exists there, declared or not, because nothing declares it.
func (o *Options) noteMacosUserPlatformGaps(cfg *jsonx.OrderedMap) {
	out := o.pr(o.Stderr)

	if devs := cfgList(cfg, "devices"); len(devs) > 0 {
		var raw []string
		var other []any
		for _, d := range devs {
			if s, ok := d.(string); ok {
				raw = append(raw, s)
			} else {
				other = append(other, d)
			}
		}
		allowed, refused := macosuser.DeviceIoctlPaths(raw)
		if len(allowed) > 0 {
			out.print("[dim]devices: the sandbox allows device control (ioctl) on " +
				strings.Join(allowed, ", ") + ". The node opens under ordinary macOS " +
				"permissions; nothing is attached.[/dim]")
		}
		for _, r := range refused {
			out.print("[yellow]Warning: `devices` entry " + r.Entry + " is skipped on " +
				"macos-user[/yellow] — " + r.Reason + "; " + r.Next + ".")
		}
		if labels := deviceLabels(other); len(labels) > 0 {
			// Each form's own container mechanism, because they differ: a USB entry attaches a
			// device (`--device` on the node lsusb resolves), and a cgroup rule attaches nothing
			// — it becomes `--device-cgroup-rule`, which only lets the container's device cgroup
			// open matching device numbers (deviceArgs).
			//
			// The words are the setup census's notice for `devices` on this backend
			// (internal/setupcensus, OQ-BP-1: "the macos-user notice block reads it").
			out.print(setupcensus.Warning(setupcensus.MacosUser, "devices").Line(strings.Join(labels, ", ")))
		}
	}

	if gpuSec := cfgMap(cfg, "gpu"); gpuSec != nil && mapBoolOr(gpuSec, "enabled", false) {
		out.print(setupcensus.Warning(setupcensus.MacosUser, "gpu").Line(""))
	}

	if cfgTrue(cfg, "kvm") {
		out.print(setupcensus.Warning(setupcensus.MacosUser, "kvm").Line(""))
	}

	// `ephemeral_storage: "tmpfs"` asks for RAM-backed scratch, which is a tmpfs mount in a
	// container. "volume" (the default) and an absent key ask for disk-backed scratch, which is
	// what this backend's /tmp and /var/folders already are, so they say nothing (DP-B5).
	if cfgStr(cfg, "ephemeral_storage") == "tmpfs" {
		out.print("[yellow]Warning: `ephemeral_storage: \"tmpfs\"` is not read on macos-user" +
			"[/yellow] — RAM-backed scratch is a tmpfs mount inside a CONTAINER, and this " +
			"backend starts none, so the sandbox writes this machine's own /tmp and " +
			"/var/folders, on disk. Remove the key, or use Apple Container " +
			"(runtime: \"container\"), whose scratch is always RAM-backed" + o.containerStepClause() + ".")
	}

	// THE HOST NVIM CONFIG, a disclosure and not a refusal: nothing declares it (the container
	// arm binds ~/.config/nvim whenever it exists, assemble.go), so refusing it would refuse every
	// launch on a Mac that has one (CX-D10), and silence would leave nvim coming up unconfigured
	// with nothing said. Apple Container's "Skipping host nvim config" line is the precedent.
	// Delivery waits on docs/design/baked-editor-preference.md OQ-ED2, which decides where this
	// machinery lives at all.
	if isDir(filepath.Join(homeDir(), ".config", "nvim")) {
		out.print("[dim]Host nvim config (~/.config/nvim) is not delivered on macos-user: the " +
			"sandbox's nvim starts with its own. Where host editor config goes is an open " +
			"design question (OQ-ED2), so there is nothing to change until it is ruled.[/dim]")
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
// WHAT IT PAIRS WITH. The AGENT learns the same facts from its briefing: sharesLauncherNetns
// answers true for this backend, so appliedNetMode is "host", both port sections fall out of the
// briefing, and backendLimits states the network fact and names every remap this launch relays.
//
// WHAT EACH LINE SAYS, from the plan the launch acts on (planMacosUserPortRelays), so a line can
// never call a remap relayed that is not, or the reverse:
//
//   - `ports`: that listing a port confines nothing — the sandbox is on the launcher's own stack,
//     so every port it binds is on this machine's real interfaces, listed here or not. A WARNING
//     for that reason alone, whatever else is delivered, because on a container `ports` is the
//     whole exposure surface and here it is none of it. It ends with the step that keeps a
//     service private: bind it to 127.0.0.1 (on a port no relay publishes on a real interface,
//     when one does), or use a container runtime.
//   - `forward_host_ports`: that a same-port entry needs no hop. A disclosure when every remap in
//     it is relayed, since nothing is then left undone; a warning when one is not.
//   - Both: the remaps this launch relays (each relay names itself as it opens, or warns that it
//     could not), and each one it does not, with why and the step that would have it relayed.
//
// ⚠ ONLY WHEN NON-EMPTY. Neither key has a default (resolveNetMode answers "bridge" for a launch
// that names no mode, which is why `mode` is APPLIED as host rather than refused), so this cannot
// fire on a launch that never mentioned networking.
func (o *Options) noteMacosUserPortKeys(cfg *jsonx.OrderedMap, plan macosUserPortPlan) {
	netSec := cfgMap(cfg, "network")
	if netSec == nil {
		return
	}
	out := o.pr(o.Stderr)

	// The headline and body are the setup census's notices (noteMacosUserPlatformGaps says why);
	// the remap sentence after each is this function's, since it reads the entries themselves.
	if ports := asAnyList(mapGet(netSec, "ports")); len(ports) > 0 {
		// THE NEXT STEP, after whatever the plan relays: what keeps a service private here, and
		// the backend where listing a port is the whole exposure. Binding loopback is not enough
		// for a port a relay publishes on a real interface, so then the step says so.
		step := " To keep a service on this Mac alone, bind it to `127.0.0.1` in the sandbox"
		if relaysExpose(plan.relays) {
			step += ", on a port no relay named here publishes on a real interface"
		}
		step += ", or use a container runtime (`runtime: \"podman\"`), whose published ports are " +
			"the only way in."
		out.print("[yellow]Warning: `network.ports` confines nothing on macos-user[/yellow] — " +
			strings.Join(portLabels(ports), ", ") + ". The sandbox runs on the launcher's own " +
			"network stack, so a port it binds IS published on this machine's real interfaces — " +
			"listed here or not — and nothing pins one to a bind address." +
			plan.remapSentences(keyNetworkPorts) + step)
	}

	if fwd := asAnyList(mapGet(netSec, "forward_host_ports")); len(fwd) > 0 {
		body := strings.Join(portLabels(fwd), ", ") + ". A same-port entry needs no hop: the " +
			"sandbox is already on this machine's stack, so `localhost:<port>` inside it is this " +
			"machine's port." + plan.remapSentences(keyForwardHostPorts)
		if plan.hasUnrelayed(keyForwardHostPorts) {
			out.print("[yellow]Warning: `network.forward_host_ports` is not fully delivered on " +
				"macos-user[/yellow] — " + body)
		} else {
			out.print("[dim]`network.forward_host_ports` on macos-user — " + body + "[/dim]")
		}
	}
}

// remapSentences says what the plan does with key's remaps: the ones it relays, then each reason
// some are not, with the entries it covers. Empty when key has no remap.
func (p macosUserPortPlan) remapSentences(key string) string {
	var relayed []string
	for _, r := range p.relays {
		if r.key == key {
			relayed = append(relayed, r.entry)
		}
	}
	var whys []string
	byWhy := map[string][]string{}
	for _, u := range p.unrelayed {
		if u.key != key {
			continue
		}
		if _, seen := byWhy[u.why]; !seen {
			whys = append(whys, u.why)
		}
		byWhy[u.why] = append(byWhy[u.why], u.entry)
	}
	var s string
	if len(relayed) > 0 {
		s += " " + strings.Join(relayed, ", ") + pluralIs(relayed, " is a port REMAP", " are port REMAPs") +
			", which this launch relays from outside the sandbox (TCP)."
	}
	for _, why := range whys {
		entries := byWhy[why]
		s += " " + strings.Join(entries, ", ") + pluralIs(entries, " is a port REMAP", " are port REMAPs") +
			" this launch does not relay: " + why + "."
	}
	return s
}

// hasUnrelayed reports whether any remap of key goes undelivered.
func (p macosUserPortPlan) hasUnrelayed(key string) bool {
	for _, u := range p.unrelayed {
		if u.key == key {
			return true
		}
	}
	return false
}

// pluralIs picks one or many by the length of items.
func pluralIs(items []string, one, many string) string {
	if len(items) == 1 {
		return one
	}
	return many
}

// portLabels renders port entries as the user wrote them.
func portLabels(entries []any) []string {
	var out []string
	for _, e := range entries {
		out = append(out, pyStrCoerce(e))
	}
	return out
}
