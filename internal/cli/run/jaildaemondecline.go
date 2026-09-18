package run

// jaildaemondecline.go is the macos-user arm's TRUTHFUL DECLINE for the jail-daemon half of
// the loophole lifecycle: one line per daemon this launch declared and will not run, naming
// it (docs/design/jail-daemon-on-macos-user-plan.md step 2; shape (a) of
// docs/design/declaration-parity.md's OQ-DP5 — a coded decline, NOT a warning).
//
// # What was actually broken, and why it was not a warning's job
//
// The HOST half of this backend's loophole lifecycle was generalised on 2026-09-17: it starts
// every host daemon, discloses the execution, publishes each endpoint and ACL-grants it to the
// sandbox account. The JAIL half was not, and the reason is structural rather than an
// oversight — the payload naming the daemons was composed inside the container-argv builder
// and emitted only as `-e YOLO_JAIL_DAEMONS=`, so `rg 'JailDaemon' internal/macosuser/` was
// empty. The result was an endpoint delivered and granted with nothing listening.
//
// THIS IS THE DEFAULT CONFIGURATION, not an edge case. `packs/claude` `needs` both
// `openai-auth` and `wire-bridge` unconditionally, so a bare `"packs": ["claude"]` selects two
// jail daemons on this backend and starts neither: `openai-auth-broker` is
// `default_enabled: true` with a fail-closed HOST daemon on this arm, `packs/codex` points
// `CODEX_REFRESH_TOKEN_URL_OVERRIDE` at the port its missing adapter would bind, and
// `wire-bridge`'s endpoint is published and granted with nothing behind it. Every one of those
// is a working launch that fails later, which is exactly the B-0 shape ("a backend that looked
// provisioned and configured nothing") the run pipeline was restructured to end.
//
// # Why there is no per-entry classifier
//
// The plan asks the native arm to classify each entry runnable or not. Today the answer does
// not vary: this backend has no in-jail supervisor to run one under, and `yolo-jaild` is not
// built for darwin at all (`flake.nix`'s shippedBinaries produces `bin/linux-<arch>` only, and
// `macosuser.StageBinaryCommands` stages `yolo` alone) — so a classifier would be a function
// with one branch, and its second branch would be written from guesses. Resolving argv[0] here
// and deciding whether such a child runs under the Seatbelt profile are two unfiled rulings
// (that plan's §Blockers), and they are what steps 3 and 4 are blocked on.
//
// So the REASON is stated once for the set and the NAMES are stated one per daemon, which is
// report-tiers.md P1 and P6 applied to a decline. One thing this deliberately does NOT do is
// distinguish the daemon that is declined FOREVER (`oauth-terminator`: its default port is 443
// and its interception needs an `--add-host` this backend cannot emit, so that plan's §Don't
// rules it declined and left declined) from the two that are declined UNTIL steps 3–4 land.
// That difference is about yolo's roadmap, not about this launch — both are equally absent
// tonight — and a launch line that predicted which one will start later would be a claim about
// unbuilt work.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// jailDaemonDeclineLines renders the decline, header first, one indented line per daemon.
//
// It takes the COMPOSED PAYLOAD (run.Run's hoisted jailDaemonsFor value), never the packs or
// the config, and that is the whole point of the hoist: the report names exactly the entries
// the container argv would have carried — same composer, same origin gate, same Apple
// Container skip — so a decline can neither miss a daemon the launch declared nor invent one
// it did not. A printer that walked the packs itself would be a second selection, which is the
// drift loopholeinert.go's "one mechanism means one selection, not just one rendering" is a
// long note about.
//
// The argv is printed because the name alone does not tell a user what is missing: the payload
// that named `yolo-jaild openai-auth-adapter --listen 127.0.0.1:1460` is the only place the
// dead port a Codex refresh will dial is written down.
func jailDaemonDeclineLines(specs []loopholes.JailDaemonSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	lines := make([]string, 0, len(specs))
	for _, s := range specs {
		lines = append(lines, s.Name+": "+strings.Join(s.Cmd, " "))
	}
	return lines
}

// noteMacosUserJailDaemonDeclines prints the decline for a macos-user launch.
//
// ⚠ THE CALL SITE IS THE BACKEND GATE, the rule noteMacosUserPlatformGaps states for the same
// reason: this is called from the macos-user arm of run.Run and nowhere else, so it needs no
// branch on the runtime's identity (and adds no row to backendparity_test.go's census).
// Hoisting it above the dispatch would decline daemons that a container launch really starts.
//
// BOTH the live path and `--dry-run`, from one call site below the lifecycle block, because a
// decline is not a spawn: it says what the launch will NOT do, which is as true of a plan
// render as of a launch. That is the opposite of the exec disclosure's rule, and deliberately
// so — an exec line on a dry run would name daemons that invocation never starts.
//
// NOT SUPPRESSIBLE, and nothing may be added that could suppress it (report-tiers.md P4;
// AGENTS.md's "a launch has no quiet mode"). The one-line-per-daemon compression is the
// density control, and a launch that declared no jail daemon prints nothing at all — which is
// what keeps this from being the notice OQ-BP-3 says people learn to skip.
func (o *Options) noteMacosUserJailDaemonDeclines(specs []loopholes.JailDaemonSpec) {
	lines := jailDaemonDeclineLines(specs)
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[yellow]Declined: no jail-side daemon runs on macos-user[/yellow] — this " +
		"backend has no in-jail supervisor, so the daemons a selected pack declared are " +
		"never started and nothing reads YOLO_JAIL_DAEMONS here:")
	for _, l := range lines {
		out.print("[yellow]  " + l + "[/yellow]")
	}
	// WHAT IS NOT AFFECTED, once, because the decline is easy to read as "this loophole is
	// off" when the host half is untouched. The two kinds differ here and the line says so:
	// a LOOPHOLE keeps its host daemon and its published endpoint (this arm starts every one
	// and refuses the launch if the credential service did not), while a pack SERVICE'S jail
	// daemon is the whole of its implementation — packservices.go composes no host half for a
	// service at all, so for `wire-bridge` there is nothing left running anywhere.
	out.print("[dim]A loophole's own host daemon still starts and still publishes its " +
		"endpoint; what is missing is the jail-side process that would dial it. For a pack " +
		"service the jail daemon IS the implementation. A container runtime (podman, or " +
		"runtime: \"container\") runs them.[/dim]")
}
