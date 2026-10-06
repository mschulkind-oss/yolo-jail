package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
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
// or not. The REMAPS this launch relays are the other way round, conditional on both ends,
// and from one plan (planMacosUserPortRelays): the notice names them to the human, and the
// relay sentence below to the agent.

// backendLimits returns the standing constraints of `rt` for the agent's briefing,
// or nil when the backend imposes none (every container backend today). relays are the port
// remaps this launch relays (macosUserPortPlan), named so the agent can use them.
func backendLimits(rt string, packs []*packload.Pack, cfg *jsonx.OrderedMap, relays []macosUserPortRelay) []string {
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
	// "state", not "history", since 2026-09-21: the machine tier stopped being credential
	// and history directories alone when packs/pi declared `.pi-shared-npm`, its extension
	// package store. An agent told to expect "history that is not yours" in a node_modules
	// tree has been handed a sentence that does not describe what it will find — and the
	// thing it does need to know about a shared store is the opposite of history's: another
	// workspace's session can change the packages under it WHILE this one runs.
	if dirs := packload.SharedDirs(packs); len(dirs) > 0 {
		out = append(out, "Your home is SHARED by every workspace on this machine, not "+
			"scoped to this project — "+strings.Join(dirs, ", ")+" are the same directories "+
			"another workspace's session reads and writes. Treat anything you put there as "+
			"visible outside this project, and expect to find state that is not yours: "+
			"another workspace's login, its history, or a package store it installed into.")
	}

	// ⚠ A PARAGRAPH WAS DELETED HERE ON 2026-09-13, and deleting it is the point. It told
	// the agent "your agent config files were rendered from DEFAULTS, not from the human's
	// own", naming every pack `reads-host` grant, and instructed it not to reason from the
	// settings it found. That was true while the bytes crossed on a /ctx mount this
	// backend does not have; since DP-L1 they cross by COPY into a root-owned tree and the
	// surface composes the human's real file (internal/cli/run/macosctxtree.go).
	//
	// Leaving it would be strictly worse than never having written it. The human-facing
	// warnings it paired with were retired on the same evidence (in the since-deleted
	// noteMacosUserHostByteGaps), and an agent told its config is not the human's would
	// discount preferences that ARE theirs — a standing constraint that is false is acted on
	// for the whole session, with no moment of use to correct it at. The remaining asymmetry
	// is the one every backend has: a grant whose host file does not exist composes from
	// defaults, which needs no line here because it is not this backend's fact.

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
	// two clients belong to loopholes that declare `platforms: ["linux"]`, and their
	// binaries are not staged into the sandbox at all — the guest set is yolo-jaild and the
	// clients of the loopholes that run on a Mac (macosuser.GuestBinaries). So the agent
	// cannot run them whatever is running on the host. The guest's own clients (`yolo-serial`,
	// `yolo-ps`: macosuser.GuestClients) are kept OUT of that sentence and said to work, in one
	// of their own: the guest stages them whenever their loophole's endpoint is published, so
	// calling them unavailable would be false of the launch that has them, and an agent on a
	// Mac told only what is missing would assume the rest of the Linux set is missing too.
	switch {
	case rt == "macos-user": // parity: Warned — the two clients belong to Linux-only loopholes and the guest stages only yolo, yolo-jaild and the Mac-capable loopholes' clients (macosuser.GuestBinaries), so the sandbox cannot run them whatever the host started
		out = append(out, "The in-jail loophole clients `yolo-journalctl` and `yolo-cglimit` "+
			"are not available here: they belong to Linux-only loopholes and are not staged "+
			"into this sandbox. "+guestClientsSentence()+" Host services that DO run on this "+
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
	//
	// A `network.ports` RELAY makes two of its words false, so the sentence names the exception
	// rather than contradicting the relay sentence after it: the relay publishes a port, and one
	// listening on a real interface exposes the port it dials however the agent binds it, so
	// "bind to 127.0.0.1" no longer keeps that port private. A false standing constraint is the
	// worst kind (above): it is acted on for the whole session.
	publishes := false
	for _, r := range relays {
		publishes = publishes || r.key == keyNetworkPorts
	}
	exposes := relaysExpose(relays)
	network := "There is no network namespace here: every port you bind is bound on the human's " +
		"REAL machine, on its real interfaces, listed in `network.ports` or not. "
	switch {
	case exposes:
		network += "Nothing confines a port, and nothing publishes one but the relays below — bind " +
			"to `127.0.0.1` when you do not mean to expose a service to their network, except on " +
			"a port a relay below publishes on a real interface, which is exposed however you bind it."
	case publishes:
		network += "Nothing confines a port, and nothing publishes one but the relays below — bind " +
			"to `127.0.0.1` when you do not mean to expose a service to their network."
	default:
		network += "Nothing publishes a port and nothing confines one — bind to `127.0.0.1` when " +
			"you do not mean to expose a service to their network."
	}
	out = append(out, network)
	// AND THE REMAPS THE LAUNCH RELAYS, which the agent has no other way to learn: the briefing's
	// port sections are the container mechanism's and stay out (appliedNetMode is "host" here),
	// and the relays' own lines are on the human's terminal. Only when there is one.
	if line := relaySentenceForAgent(relays); line != "" {
		out = append(out, line)
	}

	// THE USERLAND IS THE MAC'S, and an agent trained mostly on Linux shells reaches for GNU
	// flags without thinking. UNCONDITIONAL, like the network sentence: whether a GNU build is
	// ahead on PATH is a fact of `packages:` and mise that this composition does not resolve,
	// and the sentence is true either way because it names the condition. No launch line pairs
	// with it — the human chose a Mac and knows its tools; the agent is the one that forgets.
	// `ls --color` is left out of the failing list on purpose: whether a current macOS `ls`
	// accepts it has not been checked, and a wrong "this fails" is acted on all session.
	out = append(out, "`sed`, `find`, `grep`, `tar` and `ls` here are the Mac's own BSD tools, "+
		"not GNU's, unless `packages:` or a mise tool puts a GNU build ahead of them on PATH: "+
		"`sed -i` needs a suffix argument (`sed -i ''`), and `find -printf`, `grep -P` and "+
		"`tar --wildcards` fail. Write portable invocations — a script you write here may also "+
		"run on a Linux CI machine.")

	// THE TWO REFUSALS THE PROFILE MAKES BY DEFAULT, each with the setting that lifts it. Both
	// are stops the agent meets with nothing it can see naming the cause — Seatbelt's refusal is
	// a bare "Operation not permitted" — and no launch line pairs with them, so this is the one
	// surface where the next step can be said ("Every stop names the next step", AGENTS.md).
	//
	// The log: `macos_log` "off", the default, denies the unified log's stores and its stream
	// service (macosuser.macosLogDenies), so `/usr/bin/log` reads nothing either, and the only
	// text naming the remedy used to sit inside the `yolo-log` stub, which nothing tells the agent
	// exists. Conditional on the profile's own reading of the key (macosuser.MacosLogOff), so the
	// sentence and the deny cannot disagree; under "user" and "full" there is nothing to say.
	if macosuser.MacosLogOff(cfg) {
		out = append(out, "The macOS unified log is unreadable here (`macos_log` is off, as it is "+
			"by default): `/usr/bin/log` and the `yolo-log` helper cannot read it. If you need it, "+
			"ask the human to set `\"macos_log\": \"user\"` in yolo-jail.jsonc and relaunch.")
	}
	// Devices: the profile's `(deny file-ioctl)` is unconditional, re-allowed for terminals and
	// for each raw-path `devices` entry (macosuser.DeviceIoctlPaths), so the sentence is
	// unconditional too. Raw disks and packet capture are refused whatever the config lists, so
	// the remedy says so rather than send the human to add an entry the launch will skip.
	out = append(out, "Device control calls (`ioctl`) on /dev nodes are refused here, except on "+
		"the terminal and pseudo-terminals (`/dev/tty`, `/dev/ptmx`, `/dev/ttys*`, `/dev/pty*`) "+
		"and on each node listed in `devices`, so configuring a serial adapter, for one, fails "+
		"until its node is listed. If you need one, ask the human to add its path (a serial "+
		"adapter's `/dev/cu.*` node, say) to `devices` in yolo-jail.jsonc and relaunch; raw disks "+
		"and packet capture stay refused whatever is listed.")
	return out
}

// guestClientsSentence says which in-jail loophole clients DO run in a macos-user sandbox, and
// when: each macosuser.GuestClients binary, read from that list so a client added to the guest
// set is named here without anyone remembering this file.
func guestClientsSentence() string {
	names := make([]string, 0, len(macosuser.GuestClients))
	for _, c := range macosuser.GuestClients {
		names = append(names, "`"+c.Binary+"`")
	}
	list := strings.Join(names, " and ")
	if len(names) > 2 {
		list = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return list + " do run here, staged into this sandbox whenever the human has switched " +
		"their loophole on."
}
