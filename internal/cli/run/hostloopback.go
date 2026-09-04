package run

// hostloopback.go asks the container runtime to forward the HOST'S LOOPBACK into
// the jail, which is the fix for docs/design/loopback-tls-reachability.md §6.
//
// # The bug, in three sentences
//
// yolo's host daemons bind the host's loopback and advertise
// `host.containers.internal`, on the assumption that the runtime forwards that
// name to the host's loopback. Whether it does is a property of WHICH rootless
// network stack is in use AND of where podman aims that NAME: false for pasta —
// podman's default since 5.0 — which forwards it to the host's GLOBAL address;
// true for slirp4netns only with allow_host_loopback AND a hosts entry pinning
// the name at slirp's gateway, since podman aims it at the host's global address
// there too (measured — see slirp4netnsHostAddr). Out of the box, therefore,
// every loopback-TLS service is unreachable from every jail on both stacks.
// The design doc walks the whole map (§2-§3) and rules out changing what yolo
// binds (§5); the fix has to move to the runtime, which is this file.
//
// # Why this is the one place a bug BRICKS the product
//
// Everything here emits a `--network=` flag onto the launch argv, and a wrong
// `--network=` flag does not degrade a jail — it stops the jail from starting at
// all. Nobody who wrote this file could test a pasta host (this jail's podman
// reports slirp4netns, and podman-in-podman forces --net=host anyway), so the
// rule the whole file is built around is:
//
//	THE WORST CASE MUST BE TODAY'S BEHAVIOUR — services unreachable but jails
//	launch — AND NEVER "jails do not launch".
//
// Four things enforce that, and none of them is optional:
//
//   - **Every fact is POSITIVE.** The backend comes from `podman info`, not from
//     a guess about defaults; the flag is emitted only after the backend's own
//     binary was seen to advertise it. A failed `podman info`, unparseable
//     output, an unrecognised backend, a probe that could not run — every one of
//     them emits NOTHING, which is exactly today's behaviour.
//   - **Rootless is REQUIRED, not assumed.** `--network=pasta` means something
//     different from "the rootless default" on a rootful podman: there it would
//     replace a netavark bridge with a mode that host has no reason to support.
//     `host.security.rootless` gates the whole thing.
//   - **An explicit `network.mode` is never overridden** (OQ-R1). The user owns
//     that setting; they keep the bug and get told so.
//   - **YOLO_NO_HOST_LOOPBACK is the escape hatch**, mirroring
//     YOLO_ALLOW_STALE_IMAGE: if the emitted flag ever does brick a launch, the
//     user needs a way back to today's behaviour that does not involve editing
//     Go. It is loud and it names what it suppresses, and it stays silent when
//     it is suppressing nothing.
//
// # WHY AN OLD PASST DEGRADES RATHER THAN REFUSING — this IS the end state
//
// An earlier reading of OQ-R3 made a passt with `--map-host-loopback` a hard
// requirement and refused the launch without it. That was re-ruled on 2026-08-17,
// and the reason generalizes past this file: **yolo has to work on the host it is
// given.** Refusing converts "some services are down" into "no jail at all", on a
// machine whose owner may not be able to upgrade passt at all — a distro freeze,
// a shared box, a policy. Trading a degraded jail for no jail is not a safety win;
// it is the tool declining to run.
//
// So the rule is: **unsupported is not the same as broken.** An old passt is a
// KNOWN LIMITATION — say so once, clearly, name the version that fixes it and the
// command that checks, and launch. A host where yolo DID emit the forwarding
// option and the service is still unreachable is BROKEN, and that is what the
// in-jail probe is for (OQ-R2). Only the second earns a failure.
//
// Verified support, so the degraded path is rarer than it reads: the flag is
// present in pasta 2026_07_16 (measured 2026-08-17), which is what the
// maintainer's own host runs.
//
// # THE SLIRP4NETNS FALLBACK — degrade the STACK before degrading the JAIL
//
// "Unsupported is not broken" was written when an old passt meant a jail with no
// reachable services. It does not have to. Podman can be asked for the OTHER
// rootless stack, and slirp4netns forwards the host's loopback on request — so a
// host whose passt is too old can be made to WORK rather than merely be told why
// it does not. That is what fallbackSupport and slirpForwardingArgs are.
//
// It is a FALLBACK and never a preference, in both directions:
//
//   - A pasta that advertises --map-host-loopback is taken first, always.
//     slirp4netns is the older stack and the slower one (a userspace TCP stack per
//     container), so a host that does not need it never sees it.
//   - It is taken only when yolo POSITIVELY established that slirp4netns is there
//     — podman's own executable path, and that binary's own --help. Anything less
//     emits nothing and keeps the warn-and-launch path, because a --network= flag
//     naming a stack the host cannot start is a jail that does not start at all,
//     and that is the one outcome this file may never produce.
//
// The measurement that shaped it, and the correction it carries, are at
// slirp4netnsHostAddr: the option alone forwards a loopback nothing in the jail
// dials.
//
// # A PODMAN TOO OLD TO NAME ITS STACK gets the same rescue
//
// `rootlessNetworkCmd` is not an old field: measured against containers/podman's
// libpod/define/info.go on 2026-09-04, it is absent at v4.9.3 and v5.0.0 and
// present at v5.1.0. Every podman below that answers `podman info` perfectly well
// and names no stack, so the backend read "" and landed in the
// unrecognised-backend silence below — Ubuntu 24.04 LTS's stock podman is 4.9.3,
// so an LTS host had every jail-facing service silently down.
//
// That is not an unrecognised backend, it is an UNREAD one, and the difference is
// exactly what backendUnnamed carries. The rescue is the fallback above, reached
// by a second door and held to the same positive bar — podman's own slirp4netns
// executable, advertising host-loopback control — so the worst case is unchanged:
// a host with no usable slirp4netns keeps today's silence.
//
// This is also the shape GitHub's hosted runners took on 2026-08-31, when the
// ubuntu-24.04 images downgraded podman 5.8.4 → 4.9.3
// (actions/runner-images#14642) and `TestInJailServiceReachability` went red on
// both arches with nothing in this repo having changed.
//
// # What this decision TELLS THE JAIL, and why it has to
//
// "Unsupported is not broken" is a distinction only this file can draw. From
// inside a jail the two look identical — a service that does not answer — and the
// in-jail witness (internal/entrypoint/reachability.go) is the thing OQ-R2 turns
// into a launch failure. So the plan carries a `disposition` that rides into the
// container as paths.HostLoopbackEnvVar: `requested` when the option above went
// out on the argv (an unreachable service is then a FAULT), `unsupported` when
// yolo identified the stack and could not get it to forward (a KNOWN LIMITATION,
// never a launch failure), and `unknown` for every path that reached no conclusion
// — a rootful podman, an unrecognised backend, an explicit network.mode, the
// opt-out, a podman that would not answer.
//
// UNKNOWN IS SPELLED RATHER THAN OMITTED (OQ-R6), and the safety argument is
// unchanged by that. It used to be the absence of the variable, which forced
// absence to mean both "yolo could not tell" and "this launcher is older than the
// variable" — and, worse, the same absence covered a jail that SHARES the
// launcher's network namespace, where there is no forwarding hop at all and an
// unreachable service is therefore the least ambiguous case there is. The zero
// value of the plan still lands on `unknown` by construction (jailEnvArgs), so a
// path added here that names no disposition degrades to "no conclusion" rather than
// to a claim it never made.
//
// THE FOURTH STATE, `shared`, IS NOT THIS FILE'S TO EMIT. The shapes that have it
// never reach this decision: assemble.go branches to --net=host above the call, and
// must (podman refuses a container carrying two network selectors). Its emission
// point is there, beside the branch that creates it — see jailLoopbackEnvArgs.

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const (
	// hostLoopbackAddr is the address a jail dials to mean "the host". Podman
	// puts it in the jail's /etc/hosts as host.containers.internal and maps it
	// with pasta's `--map-guest-addr`; §3.1 of the design doc measured it
	// reaching the host (SSH answered on it) and measured that it lands on the
	// host's GLOBAL address rather than its loopback. Telling pasta to
	// `--map-host-loopback` THIS address is what closes that gap.
	//
	// Hardcoding it is safe in the direction that matters: if a future podman
	// picks a different address, the jail dials something we did not map, which
	// is today's behaviour — not a launch failure. Verified 2026-08-17 on pasta
	// 2026_07_16 that passt accepts the same address for --map-host-loopback and
	// --map-guest-addr (podman passes the latter itself), so §6's "use a distinct
	// address if passt rejects it" contingency is not needed.
	hostLoopbackAddr = "169.254.1.2"

	// backendPasta / backendSlirp4netns are podman's `rootlessNetworkCmd`
	// spellings. Anything else — including "" — is an unrecognised backend and
	// emits nothing.
	backendPasta       = "pasta"
	backendSlirp4netns = "slirp4netns"

	// pastaMapHostLoopbackFlag is both the flag yolo emits and the literal the
	// capability probe greps for in `pasta --help`. One constant on purpose: a
	// probe that looks for a different string than the one it gates is how you
	// ship a confident wrong answer.
	pastaMapHostLoopbackFlag = "--map-host-loopback"

	// slirp4netnsHostAddr is the address slirp4netns answers on as "the host": the
	// second address of its default subnet (`--cidr`, default 10.0.2.0/24, per
	// slirp4netns --help). yolo passes no cidr, so the default is what podman gets.
	//
	// It has to be spelled out here because of a fact the design doc's §3
	// slirp4netns row does not carry — that row was measured by dialling the
	// gateway DIRECTLY. Measured 2026-08-17 (podman 5.8.4, slirp4netns 1.3.4),
	// with the jail's inherited /etc/hosts removed from the picture so podman's own
	// computation is what is observed:
	//
	//	--network=slirp4netns:allow_host_loopback=true         host.containers.internal
	//	                                                       = 192.168.1.131  → FAIL
	//	  + --add-host=host.containers.internal:10.0.2.2       = 10.0.2.2       → CONNECT
	//	--add-host … WITHOUT allow_host_loopback               = 10.0.2.2       → FAIL
	//
	// Under slirp4netns podman points host.containers.internal at the host's GLOBAL
	// address, which is the very failure §1 is about. That is not a podman bug:
	// etchosts prefers a mapped address only when PASTA reported one (`PreferIP`,
	// fed from pastaResult.MapGuestAddrIPs and from nothing else) and rootless
	// otherwise falls through to GetLocalIPExcluding — read in containers/common
	// libnetwork/etchosts/ip.go and podman libpod/container_internal_common.go.
	//
	// So the option alone forwards a loopback NOBODY IN THE JAIL DIALS: yolo's
	// daemons advertise a NAME (svcendpoint.DefaultAdvertiseHost), and the name is
	// what podman aims elsewhere. Both flags together, or neither.
	slirp4netnsHostAddr = "10.0.2.2"

	// slirp4netnsHostLoopbackFlag is slirp4netns's side of the same capability.
	// Podman's `allow_host_loopback=true` option works by OMITTING
	// `--disable-host-loopback` from the slirp4netns argv it builds, so a binary
	// that advertises that flag is a binary whose host-loopback behaviour podman
	// can steer. See probeHostLoopbackSupport for what this does and does not
	// prove.
	slirp4netnsHostLoopbackFlag = "--disable-host-loopback"

	// minPasstVersion is the passt release that introduced --map-host-loopback
	// (Fedora's passt-0^20240821.g1d6142f). OQ-R3 requires the message to name a
	// version the user can act on, so this string is user-facing.
	minPasstVersion = "2024_08_21"

	// minPodmanReportingVersion is the podman release that added
	// `rootlessNetworkCmd` to `podman info`. Measured against
	// containers/podman's libpod/define/info.go on 2026-09-04: the field is
	// ABSENT at v4.9.3 and v5.0.0, PRESENT at v5.1.0. Below it yolo cannot READ
	// this host's rootless stack, which is a different fact from the host not
	// having one — see backendUnnamed. User-facing, for the same reason
	// minPasstVersion is.
	minPodmanReportingVersion = "5.1"

	// hostLoopbackOptOutEnv is the escape hatch. Any non-empty value (the
	// YOLO_ALLOW_STALE_IMAGE convention) drops back to today's behaviour.
	hostLoopbackOptOutEnv = "YOLO_NO_HOST_LOOPBACK"

	// podmanInfoTimeout / flagProbeTimeout bound the two subprocesses. Both are
	// on the launch path, so neither may hang a jail: a runtime that cannot
	// answer in this window is treated as an unrecognised backend and emits
	// nothing.
	podmanInfoTimeout = 10 * time.Second
	flagProbeTimeout  = 5 * time.Second
)

// hostLoopbackSupport is the capability verdict for one backend. "absent" and
// "unknown" both emit nothing but say different things to the user — "your
// passt is too old, upgrade to X" is actionable, "yolo could not find a pasta to
// ask" is a different problem — and collapsing them would send a reader looking
// in the wrong place.
type hostLoopbackSupport int

const (
	// supportUnknown: the probe could not run (no binary, no output, timeout).
	supportUnknown hostLoopbackSupport = iota
	// supportConfirmed: the backend's own --help advertises the flag. THIS is
	// the only value that emits an argv.
	supportConfirmed
	// supportAbsent: the probe ran and the flag is not there.
	supportAbsent
)

// hostLoopbackFacts is everything the decision needs, and nothing that requires
// a subprocess to obtain. Splitting it out is what makes the decision a pure
// function that a table test can drive through every combination — the half of
// this feature that CAN be tested from in here.
type hostLoopbackFacts struct {
	// netMode is the effective network mode. "bridge" is the default path — the
	// only one yolo may take ownership of (OQ-R1).
	netMode string
	// backend is `podman info` host.rootlessNetworkCmd, verbatim. "" when it
	// could not be read, which is treated exactly like an unrecognised backend.
	backend string
	// backendUnnamed is podman ANSWERING and naming no rootless stack: `podman
	// info` parsed, host.security.rootless was true, and rootlessNetworkCmd was
	// empty. That field only exists from podman minPodmanReportingVersion, so on
	// every older podman — Ubuntu 24.04 LTS's stock podman is 4.9.3 — backend
	// reads "" and the decision would fall through to the
	// unrecognised-backend silence, leaving every jail-facing service down on a
	// host that can forward perfectly well. This is the fact that separates those
	// two, and it is POSITIVE rather than the absence of one: a podman that did
	// not run, would not answer, answered non-JSON, or is ROOTFUL never sets it,
	// so every one of those keeps today's silence.
	backendUnnamed bool
	// rootless is host.security.rootless. False disables everything here.
	rootless bool
	// support is the capability verdict for backend.
	support hostLoopbackSupport
	// fallbackSupport is the capability verdict for slirp4netns AS A FALLBACK —
	// the availability half of the decision. It is gathered only on the one path
	// where it can change the answer (a pasta host whose pasta cannot forward
	// loopback), so it is supportUnknown everywhere else and reads as "no fallback"
	// there, which is exactly today's behaviour.
	//
	// supportConfirmed means two positive facts at once: PODMAN knows where a
	// slirp4netns is, and that binary's own --help advertises host-loopback
	// control. See hostLoopbackFactsFor for why podman's lookup is the only one
	// that may count.
	fallbackSupport hostLoopbackSupport
	// probeCmd is the exact command the capability probe ran, echoed in the
	// warning so the user can re-run it. "" when no probe ran.
	probeCmd string
	// version is the backend version `podman info` reported, first line only.
	// Diagnostic sugar: it is what turns "upgrade passt" into "you have X".
	version string
	// podmanVersion is `podman info` version.Version. Diagnostic sugar too — NO
	// BRANCH READS IT — so a podman that stops reporting it costs a nicer
	// sentence and never a decision. Deciding on a parsed version number is the
	// guess-the-default this whole file refuses to make.
	podmanVersion string
	// optOut is hostLoopbackOptOutEnv being set.
	optOut bool
}

// hostLoopbackPlan is the decision's whole output: an argv fragment to append
// (usually empty), a warning to print (usually empty), and what the JAIL is told
// about the decision. args+warning both empty is the healthy, silent,
// overwhelmingly common case.
type hostLoopbackPlan struct {
	args    []string
	warning string
	// disposition is the one fact this decision has that nothing inside the jail
	// can recover for itself: whether the forwarding option went out on the argv.
	// The in-jail reachability witness needs it to tell a KNOWN LIMITATION (yolo
	// could not ask this host) from a FAULT (yolo asked and the service is still
	// unreachable) — only the second may ever fail a launch, which is OQ-R2 as
	// scoped by OQ-R3. See paths.HostLoopbackEnvVar.
	//
	// "" means NO CONCLUSION — an unrecognised backend, a rootful podman, an
	// explicit network.mode, the opt-out, a podman that would not answer — and it is
	// the ZERO VALUE on purpose: every such path returns a bare hostLoopbackPlan{}
	// and lands on the witness's safe default without having to be enumerated here.
	// It reaches the jail as paths.HostLoopbackUnknown rather than as an absent
	// variable; jailEnvArgs is where that translation lives and why.
	disposition string
}

// jailEnvArgs renders this plan's disposition as the `-e` pair the container
// carries. Always exactly one pair — see jailLoopbackEnvArgs.
func (p hostLoopbackPlan) jailEnvArgs() []string {
	return jailLoopbackEnvArgs(p.disposition)
}

// jailLoopbackEnvArgs renders ONE disposition as the container's `-e` pair, and it
// is a free function rather than only the method above because two of the four
// states are not produced by a plan at all: the shared-namespace shapes never reach
// decideHostLoopback (assemble.go branches to --net=host first), so the assembler
// renders those directly. One renderer for all four is what makes "every launch
// carries exactly one disposition" a property of the code instead of an agreement
// between call sites.
//
// The empty disposition renders as `unknown` — the OQ-R6 inversion. Omitting the
// variable was the old spelling of "no conclusion", and it collided with the two
// things absence must be free to mean: a launcher older than the variable, and (until
// this change) a jail sharing the launcher's namespace. Rendering it keeps the zero
// value on the safe never-escalate answer while freeing absence to mean only version
// skew.
func jailLoopbackEnvArgs(disposition string) []string {
	if disposition == "" {
		disposition = paths.HostLoopbackUnknown
	}
	return []string{"-e", paths.HostLoopbackEnvVar + "=" + disposition}
}

// decideHostLoopback maps facts onto the plan. Pure — no exec, no filesystem, no
// clock.
func decideHostLoopback(f hostLoopbackFacts) hostLoopbackPlan {
	plan := hostLoopbackPlanFor(f)
	if !f.optOut {
		return plan
	}
	// The escape hatch speaks only when it is actually suppressing something.
	// A hatch that announces itself on every launch of every unaffected host
	// trains the reader to skip the line it exists to be read on.
	if len(plan.args) == 0 {
		return hostLoopbackPlan{}
	}
	// The suppressed plan carries NO POSITIVE disposition, so it reaches the jail as
	// `unknown` — and the promise that matters survives intact: a user who
	// deliberately turned the fix off must never have their own choice reported back
	// to them as a broken jail, and `unknown` never escalates.
	//
	// What the hatch promises about the argv is the NETWORK argv — no forwarding
	// option, no selector, nothing that can make podman refuse the container, which
	// is the failure it exists for. It stopped being able to promise "not one byte"
	// when the disposition became unconditional (OQ-R6), and that trade is the right
	// way round: `unknown` is the TRUE statement about a launch where yolo was told
	// not to look, and the alternative — an absent variable — is now the one
	// spelling reserved for a launcher too old to have an opinion at all.
	return hostLoopbackPlan{warning: "[yellow]Warning: " + hostLoopbackOptOutEnv + " is set — NOT requesting " +
		"host-loopback forwarding (" + strings.Join(plan.args, " ") + ").\n" +
		"  Jail-facing services (Claude OAuth broker, yolo-ps, yolo-journalctl) will be\n" +
		"  unreachable from inside this jail. Unset it to restore the fix.\n" +
		"  docs/design/loopback-tls-reachability.md[/yellow]"}
}

// hostLoopbackPlanFor is decideHostLoopback minus the opt-out, split out so the
// opt-out can report what it suppressed rather than guessing.
func hostLoopbackPlanFor(f hostLoopbackFacts) hostLoopbackPlan {
	// Rootless is the gate on everything: `rootlessNetworkCmd` is a
	// containers.conf value that a rootful podman still reports and never uses,
	// and emitting `--network=pasta` there would swap out a working bridge for a
	// mode that host has no reason to support.
	if !f.rootless {
		return hostLoopbackPlan{}
	}

	// An explicit network.mode belongs to the user (OQ-R1): they keep control and
	// they keep the bug. Warn only when the bug is real for them — `host` shares
	// the host's stack, so its loopback IS the jail's and there is nothing to
	// forward — and only when the backend is one this would otherwise have fixed,
	// so the line names a consequence instead of musing about networking.
	//
	// NO DISPOSITION on either branch. "Unsupported" would be a lie (this host may
	// well support the forwarding — yolo simply did not ask), and "requested" is
	// plainly false, so the witness is told nothing and treats an unreachable
	// service as unattributable. The specific, more useful sentence — that an
	// explicit mode is why — is the warning printed right here, at launch.
	if f.netMode != "bridge" {
		if f.netMode == "host" || !mappableBackend(f.backend) {
			return hostLoopbackPlan{}
		}
		return hostLoopbackPlan{warning: "[yellow]Warning: network.mode is set to '" + f.netMode +
			"', so yolo is not requesting host-loopback forwarding for it.\n" +
			"  On the default 'bridge' mode yolo asks " + f.backend + " to forward the host's\n" +
			"  loopback into the jail; with an explicit mode that is your call to make, and\n" +
			"  without it jail-facing services (Claude OAuth broker, yolo-ps,\n" +
			"  yolo-journalctl) may be unreachable from inside the jail.\n" +
			"  docs/design/loopback-tls-reachability.md[/yellow]"}
	}

	switch f.backend {
	case backendPasta:
		if f.support == supportConfirmed {
			// Podman splits the option string on commas into pasta's argv, so this
			// is `pasta --map-host-loopback 169.254.1.2`.
			return hostLoopbackPlan{
				args: []string{
					"--network=pasta:" + pastaMapHostLoopbackFlag + "," + hostLoopbackAddr,
				},
				disposition: paths.HostLoopbackRequested,
			}
		}
		// Only now — this pasta cannot forward loopback. Switching the jail to the
		// older stack beats leaving every jail-facing service down, and is still
		// strictly better than the alternative that OQ-R3 rejected (refusing to
		// launch). It happens ONLY on a positively established slirp4netns, so the
		// worst case of an unavailable one stays the warn-and-launch below.
		if f.fallbackSupport == supportConfirmed {
			return hostLoopbackPlan{
				args:        slirpForwardingArgs(),
				warning:     slirpFallbackNotice(f),
				disposition: paths.HostLoopbackRequested,
			}
		}
		return hostLoopbackPlan{
			warning:     pastaUnsupportedWarning(f),
			disposition: paths.HostLoopbackUnsupported,
		}
	case backendSlirp4netns:
		if f.support == supportConfirmed {
			return hostLoopbackPlan{
				args:        slirpForwardingArgs(),
				disposition: paths.HostLoopbackRequested,
			}
		}
		return hostLoopbackPlan{
			warning:     slirpUnsupportedWarning(f),
			disposition: paths.HostLoopbackUnsupported,
		}
	default:
		// A podman too old to NAME its rootless stack is not a host without one.
		// It reaches here as backend "" — indistinguishable, at this layer, from a
		// podman that never answered — which is why backendUnnamed exists and why
		// the emptiness is re-checked here rather than trusted from the flag alone.
		//
		// The rescue is the mechanism the old-pasta fallback already uses, on the
		// same positive bar: PODMAN'S OWN slirp4netns executable, advertising
		// host-loopback control. Nothing weaker may emit, because a --network=
		// naming a stack the host cannot start is a jail that does not start.
		//
		// What yolo does NOT know here is whether this switched stacks: below
		// podman 5.0 slirp4netns is the rootless default and only the forwarding
		// is new, at 5.0.x pasta is and this moves off it. The note says so rather
		// than picking one — see unnamedBackendNotice.
		if f.backend == "" && f.backendUnnamed && f.fallbackSupport == supportConfirmed {
			return hostLoopbackPlan{
				args:        slirpForwardingArgs(),
				warning:     unnamedBackendNotice(f),
				disposition: paths.HostLoopbackRequested,
			}
		}
		// Unrecognised backend — including "" from a podman that would not answer.
		// Silence is the correct output: this is today's behaviour exactly, and
		// warning about a stack we failed to identify would fire on every macOS
		// host, every rootful host, and every future backend.
		return hostLoopbackPlan{}
	}
}

// slirpForwardingArgs is the slirp4netns side of the fix, and it is TWO flags for
// the reason measured at slirp4netnsHostAddr: the option makes the stack forward
// the host's loopback, and the hosts entry aims the name the jail actually dials
// at the address that forwards it. Either flag on its own is a no-op that the
// launcher would then report to the jail as `requested` — a dead service filed as
// a fault, which is worse than the honest warning it replaced.
//
// Both arms of the decision emit this, the slirp4netns host and the old-passt
// fallback alike: the mechanism does not know why it was chosen, and a host that
// gets the option without the entry is broken the same way in both.
//
// It pins ONE name — the one yolo's daemons advertise. Podman writes its own
// entry for every name it still owns (host.docker.internal, measured 2026-08-17
// as surviving alongside this one), and a user entry only displaces the name it
// spells, so nothing else in the jail's /etc/hosts moves.
//
// The name comes from svcendpoint rather than a literal so the pin and the thing
// pinned are one fact. Its one blind spot, named rather than papered over: a user
// who sets svcendpoint.AdvertiseHostEnv on the host makes the daemons publish a
// DIFFERENT name, which this does not pin — the jail then dials something yolo did
// not aim, and the outcome is today's unreachable services. Reading the override
// here would buy that back at the cost of another input to the decision, and the
// override is a host-side expert knob that no shipped path sets.
func slirpForwardingArgs() []string {
	return []string{
		"--network=slirp4netns:allow_host_loopback=true",
		"--add-host=" + svcendpoint.DefaultAdvertiseHost + ":" + slirp4netnsHostAddr,
	}
}

// reachabilityOptOutArgs forwards the IN-JAIL witness's escape hatch
// (paths.AllowUnreachableServicesEnv) from the host environment into the
// container, and only when the user actually set it.
//
// The hatch is not this file's to honour — the witness that reads it runs in
// internal/entrypoint — but the forwarding has to happen here, and that is the
// whole point. The user types it on the HOST, in front of `yolo`, exactly as they
// type YOLO_ALLOW_STALE_IMAGE; a container inherits nothing from the launcher's
// environment, so a hatch that is only ever read in-jail is a hatch nobody can
// reach. This one exists for the user whose jail will not start, who by
// definition has no in-jail shell to set it from.
//
// It is emitted for every runtime and every network mode, not just the branch
// above: the witness runs on all of them, so the way out has to as well.
func (o *Options) reachabilityOptOutArgs() []string {
	val := o.Getenv(paths.AllowUnreachableServicesEnv)
	if val == "" {
		return nil
	}
	return []string{"-e", paths.AllowUnreachableServicesEnv + "=" + val}
}

// mappableBackend reports whether backend is one whose host-loopback forwarding
// yolo knows how to ask for.
func mappableBackend(backend string) bool {
	return backend == backendPasta || backend == backendSlirp4netns
}

// pastaUnsupportedWarning reports a KNOWN LIMITATION, not a failure — see the
// degrade-never-refuse note at the top of this file. It has to carry three things
// or it is not actionable: what breaks, the version that fixes it, and the command
// that checks. It must never read as an error, because launching is the correct
// outcome and the user has not done anything wrong.
func pastaUnsupportedWarning(f hostLoopbackFacts) string {
	return "[yellow]Warning: this host's rootless network stack is pasta, which forwards\n" +
		"  host.containers.internal to the host's GLOBAL address rather than its loopback —\n" +
		"  so jail-facing services (Claude OAuth broker, yolo-ps, yolo-journalctl) will be\n" +
		"  unreachable from inside this jail.\n" +
		"  The fix is pasta's " + pastaMapHostLoopbackFlag + ", and " +
		pastaSupportPhrase(f) + "\n" +
		"  " + slirpFallbackPhrase(f) + "\n" +
		"  Upgrade passt to " + minPasstVersion + " or newer (the release that added it) and\n" +
		"  check with: pasta --version\n" +
		"  Launching without it — nothing else changes.\n" +
		"  docs/design/loopback-tls-reachability.md[/yellow]"
}

// slirpFallbackPhrase says why the OTHER stack was not used, which is the
// question this warning now invites: yolo has a fallback, so a user reading "your
// pasta is too old" is owed the reason it was not taken here. Both not-confirmed
// verdicts appear, because "podman knows no slirp4netns" and "the slirp4netns
// podman knows cannot do it either" send a reader to different places.
func slirpFallbackPhrase(f hostLoopbackFacts) string {
	if f.fallbackSupport == supportAbsent {
		return "The slirp4netns fallback was not taken either: that binary does not " +
			"advertise\n  host-loopback control."
	}
	return "The slirp4netns fallback was not taken either: podman reports no usable\n" +
		"  slirp4netns on this host."
}

// slirpFallbackNotice is the one message in this file about a host that WORKS, so
// it is a note and not a warning: nothing is broken, nothing is owed, and a
// yellow "Warning:" on a healthy launch is how a reader learns to skip the line.
//
// It still has to be said out loud. yolo silently moved this jail onto a
// different, slower network stack than the one the host's podman defaults to —
// that shows up as throughput, and a user debugging it should not have to read
// this file to find out why. So the note carries what changed, what it costs, how
// to get back onto pasta, and the hatch that turns the whole thing off.
func slirpFallbackNotice(f hostLoopbackFacts) string {
	return "[cyan]Note: " + pastaFallbackReason(f) + "\n" +
		"  yolo launched this jail on slirp4netns instead, which does forward it, so\n" +
		"  jail-facing services (Claude OAuth broker, yolo-ps, yolo-journalctl) work.\n" +
		"  The cost is that slirp4netns is the older and slower rootless stack.\n" +
		"  Upgrade passt to " + minPasstVersion + " or newer and yolo goes back to pasta by itself;\n" +
		"  " + hostLoopbackOptOutEnv + "=1 turns off both.\n" +
		"  docs/design/loopback-tls-reachability.md[/cyan]"
}

// unnamedBackendNotice is slirpFallbackNotice's twin for a podman too old to say
// what its rootless stack is, and a note rather than a warning for the same
// reason: nothing is broken, the jail works, and a yellow "Warning:" on a healthy
// launch teaches the reader to skip the line.
//
// It may not borrow the other one's sentence. That one KNOWS the host's stack is
// pasta and says yolo moved off it; here yolo does not know what it moved off, or
// whether it moved at all — below podman 5.0 slirp4netns IS the rootless default
// and only the forwarding option is new, at 5.0.x pasta is and this does switch
// stacks. Claiming either would be inventing the fact the rest of this file
// refuses to invent, so the note states both readings and names the upgrade that
// lets yolo read the stack instead of asking for one.
func unnamedBackendNotice(f hostLoopbackFacts) string {
	return "[cyan]Note: this host's podman" + podmanVersionSuffix(f) + " does not report which rootless\n" +
		"  network stack it uses (`podman info` gained rootlessNetworkCmd in podman " +
		minPodmanReportingVersion + "),\n" +
		"  so yolo asked for slirp4netns by name — it does forward the host's loopback, so\n" +
		"  jail-facing services (Claude OAuth broker, yolo-ps, yolo-journalctl) work.\n" +
		"  On podman 4.x that is the stack this jail would have had anyway; on 5.0.x it is\n" +
		"  the older and slower one.\n" +
		"  Upgrade podman to " + minPodmanReportingVersion + " or newer and yolo reads the stack instead of asking;\n" +
		"  " + hostLoopbackOptOutEnv + "=1 turns this off.\n" +
		"  docs/design/loopback-tls-reachability.md[/cyan]"
}

// podmanVersionSuffix renders " (4.9.3)" when podman reported a version, and
// nothing when it did not. Same rule as versionSuffix: a diagnostic never
// fabricates the thing it is diagnosing.
func podmanVersionSuffix(f hostLoopbackFacts) string {
	if f.podmanVersion == "" {
		return ""
	}
	return " (" + f.podmanVersion + ")"
}

// pastaFallbackReason keeps the note honest about WHY the fallback was taken. An
// unprobeable pasta is not a proven-old one, and saying so would be the kind of
// invented fact the rest of this file refuses. Both readings still lead here: with
// no forwarding option on the argv, pasta does not forward the host's loopback at
// any version, so "could not confirm" and "does not have it" cost a jail the same
// services.
func pastaFallbackReason(f hostLoopbackFacts) string {
	if f.support == supportAbsent {
		return "this host's pasta" + versionSuffix(f) + " has no " + pastaMapHostLoopbackFlag +
			",\n  so it cannot forward the host's loopback into a jail."
	}
	return "yolo could not confirm this host's pasta supports " + pastaMapHostLoopbackFlag +
		",\n  without which it does not forward the host's loopback into a jail."
}

// pastaSupportPhrase completes the sentence above differently for "asked and it
// is not there" than for "could not ask", per hostLoopbackSupport's rationale.
func pastaSupportPhrase(f hostLoopbackFacts) string {
	if f.support == supportAbsent {
		return "this pasta does not have it" + versionSuffix(f) + "."
	}
	if f.probeCmd != "" {
		return "yolo could not confirm it (`" + f.probeCmd + "` gave no usable answer)."
	}
	return "yolo could not find a pasta binary to ask."
}

// slirpUnsupportedWarning is the slirp4netns twin. Shorter on purpose: this path
// is the OLD rootless default, so a host on it is already on a podman old enough
// that "upgrade" is not the useful advice — naming the option the user can set
// by hand is.
func slirpUnsupportedWarning(f hostLoopbackFacts) string {
	return "[yellow]Warning: this host's rootless network stack is slirp4netns and yolo could\n" +
		"  not confirm it supports host-loopback forwarding" + versionSuffix(f) + ", so jail-facing\n" +
		"  services (Claude OAuth broker, yolo-ps, yolo-journalctl) may be unreachable from\n" +
		"  inside this jail. Check with: slirp4netns --help | grep host-loopback\n" +
		"  docs/design/loopback-tls-reachability.md[/yellow]"
}

// versionSuffix renders " (reported: X)" when a version is known.
func versionSuffix(f hostLoopbackFacts) string {
	if f.version == "" {
		return ""
	}
	return " (reported: " + f.version + ")"
}

// --- fact gathering (the impure half) ---

// podmanInfo is the sliver of `podman info --format json` this needs. Everything
// else podman reports is deliberately not modelled: an unknown field is not an
// error for encoding/json, so this struct keeps working across podman versions,
// and a missing field decodes to the zero value — which every branch above reads
// as "do nothing".
type podmanInfo struct {
	Host struct {
		RootlessNetworkCmd string `json:"rootlessNetworkCmd"`
		Security           struct {
			Rootless bool `json:"rootless"`
		} `json:"security"`
		Pasta       podmanHelperInfo `json:"pasta"`
		Slirp4netns podmanHelperInfo `json:"slirp4netns"`
	} `json:"host"`
	// Version is podman's own version block. The inner key really is capitalised
	// inside a lower-cased outer one — verified against a live `podman info
	// --format json` (podman 5.8.4, 2026-09-04) — and getting that wrong would
	// silently cost the diagnostic rather than the decision, which is why nothing
	// branches on it.
	Version struct {
		Version string `json:"Version"`
	} `json:"version"`
}

// podmanHelperInfo is podman's per-helper block. `executable` is why this is
// read at all: it is the path podman itself will run, which beats a PATH lookup
// that can find a different pasta than the one that will do the forwarding.
type podmanHelperInfo struct {
	Executable string `json:"executable"`
	Version    string `json:"version"`
}

// parsePodmanInfo decodes `podman info --format json`. Returns ok=false for
// anything it cannot read, which the caller turns into "emit nothing".
func parsePodmanInfo(stdout string) (podmanInfo, bool) {
	var info podmanInfo
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		return podmanInfo{}, false
	}
	return info, true
}

// firstLine trims a multi-line version blob down to something printable —
// podman reports pasta's version as its whole copyright banner.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// hostLoopbackFactsFor gathers the facts for this launch. It runs at most two
// short subprocesses (`podman info`, then `<backend> --help`) and every failure
// path returns facts that decide to nothing.
//
// rt/netMode come from the assembler. The caller has already handled the
// podman-in-podman case (forced --net=host), so this is never reached there.
func (o *Options) hostLoopbackFactsFor(rt, netMode string) hostLoopbackFacts {
	f := hostLoopbackFacts{
		netMode: netMode,
		optOut:  o.Getenv(hostLoopbackOptOutEnv) != "",
	}
	// Not podman, or macOS: out of scope. Apple Container does its own
	// networking and podman-machine puts a VM between the jail and the host's
	// loopback, which is a different problem with a different fix (design doc §8
	// puts macOS out of scope explicitly).
	if rt != "podman" || o.IsMacOS {
		return f
	}
	podman, ok := o.LookPath("podman")
	if !ok {
		return f
	}
	res := o.Exec([]string{podman, "info", "--format", "json"}, "", nil, podmanInfoTimeout)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return f
	}
	info, ok := parsePodmanInfo(res.Stdout)
	if !ok {
		return f
	}
	f.backend = info.Host.RootlessNetworkCmd
	f.rootless = info.Host.Security.Rootless
	f.podmanVersion = firstLine(info.Version.Version)
	if !f.rootless {
		return f
	}
	// Recorded HERE, where "podman answered and is rootless" is still in hand.
	// One layer down only backend "" survives, and "" is also what a podman that
	// never ran leaves behind — so this is the last point at which the two can be
	// told apart at all.
	f.backendUnnamed = f.backend == ""
	if !f.backendUnnamed && !mappableBackend(f.backend) {
		return f
	}
	// The capability probe only ever gates the default path. On an explicit
	// network.mode the decision is already made (the user's), so spending a
	// second subprocess to learn a fact nothing will read is pure launch latency.
	if netMode != "bridge" {
		return f
	}

	// A podman that named no stack has no backend helper to interrogate, so the
	// only question left is the fallback one, asked exactly as the pasta arm asks
	// it below: podman's own executable path, no PATH lookup. There is no
	// f.version to set either — naming a helper version for a stack yolo did not
	// establish is the invented fact this file exists to avoid.
	if f.backendUnnamed {
		f.fallbackSupport, _ = o.probeHostLoopbackSupport(
			info.Host.Slirp4netns.Executable, nil, slirp4netnsHostLoopbackFlag)
		return f
	}

	helper := info.Host.Pasta
	flag := pastaMapHostLoopbackFlag
	fallbacks := []string{"pasta", "passt"}
	if f.backend == backendSlirp4netns {
		helper = info.Host.Slirp4netns
		flag = slirp4netnsHostLoopbackFlag
		fallbacks = []string{"slirp4netns"}
	}
	f.version = firstLine(helper.Version)
	f.support, f.probeCmd = o.probeHostLoopbackSupport(helper.Executable, fallbacks, flag)

	// The slirp4netns FALLBACK, asked about only where the answer can change
	// anything: a pasta host whose pasta cannot forward loopback. A third
	// subprocess on the healthy path would be launch latency spent on a fact
	// nothing reads.
	//
	// PODMAN'S OWN executable path is the only one accepted here — note the empty
	// fallback list, deliberately unlike the active-backend probe above. podman is
	// the process that will exec slirp4netns, so podman's lookup is the only one
	// whose success predicts the launch; a slirp4netns that yolo can find on PATH
	// and podman cannot is a container that fails to START, which is the single
	// outcome this file may not produce. Measured 2026-08-17: podman reports
	// host.slirp4netns.executable as "" when the binary is off its PATH, so "" is
	// podman itself saying it has none — a positive fact, not a missing one.
	if f.backend == backendPasta && f.support != supportConfirmed {
		f.fallbackSupport, _ = o.probeHostLoopbackSupport(
			info.Host.Slirp4netns.Executable, nil, slirp4netnsHostLoopbackFlag)
	}
	return f
}

// probeHostLoopbackSupport asks the backend's own binary whether it advertises
// flag, by scraping `<exe> --help`. Returns the verdict and the command it ran.
//
// **What a confirmed verdict proves, exactly.** For pasta it is airtight: the
// flag yolo emits IS this flag, passed straight through by podman, so a binary
// that lists it is a binary that will accept it. For slirp4netns it is one step
// removed — yolo emits podman's `allow_host_loopback=true`, which podman
// implements by dropping `--disable-host-loopback` from the argv it builds — so
// this confirms the mechanism exists without proving podman parses the option
// key. The gap is covered from the other side by podman's own age: podman added
// allow_host_loopback in 1.6 (2019), so every podman modern enough to be run at
// all knows the key. That used to be argued from `rootlessNetworkCmd` being
// present, which was wrong in the direction that matters — the field only exists
// from minPodmanReportingVersion, and the backendUnnamed path is precisely the
// one where it is missing. If the key ever turns out to be unparsed the failure
// is a refused launch, which is what hostLoopbackOptOutEnv exists for.
//
// The exit status is deliberately ignored: some builds print usage to stderr and
// exit non-zero, and the OUTPUT is the evidence either way. What is not ignored
// is empty output — that is "could not ask", not "does not have it".
func (o *Options) probeHostLoopbackSupport(exe string, fallbacks []string, flag string) (hostLoopbackSupport, string) {
	if exe == "" {
		for _, name := range fallbacks {
			if p, ok := o.LookPath(name); ok {
				exe = p
				break
			}
		}
	}
	if exe == "" {
		return supportUnknown, ""
	}
	cmd := exe + " --help"
	res := o.Exec([]string{exe, "--help"}, "", nil, flagProbeTimeout)
	if !res.Ran || res.Timeout {
		return supportUnknown, cmd
	}
	help := res.Stdout + res.Stderr
	if strings.TrimSpace(help) == "" {
		return supportUnknown, cmd
	}
	if strings.Contains(help, flag) {
		return supportConfirmed, cmd
	}
	return supportAbsent, cmd
}
