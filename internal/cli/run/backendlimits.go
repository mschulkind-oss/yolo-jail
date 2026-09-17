package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// backendlimits.go answers, for the AGENT, the question the launch warnings answer
// for the human: what does this backend not do for me?
//
// WHY THE AGENT NEEDS ITS OWN COPY. Every fact below is printed at launch — to
// stderr, where the human reads it and the agent never does. The agent then reasons
// from a jail it believes is like every other one: that its config file reflects the
// user's, that its home is its own, that a skill it edits stays edited. Each of those
// is false here, silently, and none has a moment of use to attach the correction to
// (docs/reference/information-at-the-point-of-need.md: no moment → the briefing).
//
// NOT EVERYTHING THE HUMAN IS TOLD BELONGS HERE, and the filter is that same
// principle. `resources` and `cache_relocations` are read-and-ignored on this backend
// and warned about, and they condition nothing an agent does — it never asked for a
// memory cap. `mcp_presets` are absent, and the absence shows up as a server that is
// simply not in its config. Those stay human-only.
//
// ONE SOURCE, TWO RENDERINGS. The facts come from the same predicates the note*
// printers use, so a limit cannot be reported to one audience and not the other. The
// PROSE differs on purpose — the human gets an explanation at launch, the agent gets
// a standing constraint — but the conditions do not.
//
// ⚠ ONE ENTRY IS HALF-PAIRED, and this passage used to say it was not paired at all: that
// the network sentence at the bottom "has no note* counterpart, because nothing on the
// launch path says anything about `network.ports` or `forward_host_ports` on this backend at
// all", with DP-L2's stderr half (docs/design/declaration-parity.md §5.1.1 (2)) unbuilt. It
// is built — noteMacosUserPortKeys prints one line per non-empty key, called from the
// macos-user arm of run.Run.
//
// What survives is a difference in CONDITIONALITY rather than in source, and it is deliberate
// on both ends: the human hears it only when they DECLARED a port key, because a warning
// about a key nobody wrote is the one OQ-BP-3 says readers learn to skip, while the sentence
// below is unconditional — the agent binds ports whether the config ever mentioned them
// or not.

// backendLimits returns the standing constraints of `rt` for the agent's briefing,
// or nil when the backend imposes none (every container backend today).
func backendLimits(rt string, packs []*packload.Pack, cfg *jsonx.OrderedMap) []string {
	if rt != "macos-user" {
		return nil
	}
	var out []string

	// The home is machine-wide. This is the one that conditions the most: an agent
	// that believes its home is its own will write state there expecting it to be
	// private to this project, and it is visible to every workspace on the machine.
	//
	// SharedDirs, NOT WritableDirs, and the narrowing is a correction rather than a
	// tightening (DP-B11). WritableDirs is the `scope: workspace` tier, and since
	// entrypoint.InstallDarwinHomeLayout every one of those directories is symlinked into
	// <workspace>/.yolo/home — so the line named exactly the directories that are NOT
	// shared and stayed silent about the ones that are. SharedDirs is `scope: machine`,
	// which the layout deliberately leaves real in the account home and mirrors into the
	// sidecar, and it is the tier this sentence has always been describing.
	if dirs := packload.SharedDirs(packs); len(dirs) > 0 {
		out = append(out, "Your home is SHARED by every workspace on this machine, not "+
			"scoped to this project — "+strings.Join(dirs, ", ")+" are the same directories "+
			"another workspace's session reads and writes. Treat anything you put there as "+
			"visible outside this project, and expect to see history that is not yours.")
	}

	// ⚠ A PARAGRAPH WAS DELETED HERE ON 2026-09-13, and deleting it is the point. It told
	// the agent "your agent config files were rendered from DEFAULTS, not from the human's
	// own", naming every pack `reads-host` grant, and instructed it not to reason from the
	// settings it found. That was true while the bytes crossed on a /ctx mount this
	// backend does not have; since DP-L1 they cross by COPY into a root-owned tree and the
	// surface composes the human's real file (internal/cli/run/macosctxtree.go).
	//
	// Leaving it would be strictly worse than never having written it. The human-facing
	// warnings it paired with are retired on the same evidence (noteMacosUserHostByteGaps),
	// and an agent told its config is not the human's would discount preferences that ARE
	// theirs — a standing constraint that is false is acted on for the whole session, with
	// no moment of use to correct it at. The remaining asymmetry is the one every backend
	// has: a grant whose host file does not exist composes from defaults, which needs no
	// line here because it is not this backend's fact.

	// Content is a writable copy rather than a read-only mount.
	if len(packs) > 0 {
		out = append(out, "Your skills and this briefing are a writable COPY, not the "+
			"read-only mount every other backend gives. You can edit them; the next launch "+
			"overwrites them without warning. Edit the pack they came from instead.")
	}

	// The jail-side loophole clients are unusable here — and the REASON changed on
	// 2026-09-17 without the sentence needing to go. macos-user used to be an inert
	// BACKEND (it started no host service at all), so `backendInertReason` was the right
	// predicate. It now starts every admitted loophole, and gating on that predicate would
	// have silently deleted this line from the one backend it is most true of.
	//
	// What is still true there is narrower and has nothing to do with inertness: these
	// three clients belong to loopholes that declare `platforms: ["linux"]`, and their
	// binaries are not staged into the sandbox at all — `StageBinaryCommands` stages `yolo`
	// and nothing else. So the agent cannot run them whatever is running on the host.
	switch {
	case rt == "macos-user": // parity: Warned — the three clients belong to Linux-only loopholes and StageBinaryCommands stages only `yolo`, so the sandbox cannot run them whatever the host started
		out = append(out, "The in-jail loophole clients (`yolo-ps`, `yolo-journalctl`, "+
			"`yolo-cglimit`) are not available here: they belong to Linux-only loopholes "+
			"and are not staged into this sandbox. Host services that DO run on this "+
			"backend are reachable normally.")
	case backendInertReason(rt) != "":
		out = append(out, "No loophole host services are running, so their in-jail clients "+
			"(`yolo-ps`, `yolo-journalctl`, `yolo-cglimit`) have nothing to talk to here.")
	}

	// THE ONE NETWORK SENTENCE NEITHER PARAGRAPH SAYS, and this is the only surface with
	// a place to put it (docs/design/declaration-parity.md §5.1.1 (3)). The briefing's
	// host-networking paragraph is true here and incomplete: it says `localhost` reaches
	// the host and that no port mapping is needed, which leaves an agent to assume the
	// usual container reading — that a listener is confined until something publishes it.
	// There is no namespace on this backend, so binding IS publishing, and `network.ports`
	// pins nothing: every port this agent opens is open on the machine's real interfaces,
	// whether or not the config ever mentioned it.
	out = append(out, "There is no network namespace here: every port you bind is bound on "+
		"the human's REAL machine, on its real interfaces, listed in `network.ports` or not. "+
		"Nothing publishes a port and nothing confines one — bind to `127.0.0.1` when you do "+
		"not mean to expose a service to their network.")
	return out
}
