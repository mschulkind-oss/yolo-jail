package run

// packrefusal.go turns a REFUSED CONTRIBUTION into a REFUSED LAUNCH
// (docs/design/trust-paths.md §3.1, OQ-TP6).
//
// # What this replaces, and why it is a deletion rather than a fix
//
// The host computed a refusal, printed `Warning: refused installer …`, and then staged the
// UNMODIFIED pack.json anyway. The jail re-derived the same verdict from
// entrypoint/packsurfaces.go, which passed a hardcoded `mayAccessHost = true` — so the refusal
// branch was unreachable in the jail and the curl-to-bash launcher was written for a fetched,
// unapproved pack. The warning was true about the DECISION and false about the OUTCOME.
//
// The obvious fix is to carry the decision across the boundary (a marker file, an env var, a
// rewritten manifest). The ruling does something better: it removes the thing being carried.
// If the host refuses, NO JAIL STARTS, so there is no decision in flight and nothing for the
// two sides to disagree about. That is why this file is short and why nothing was added to the
// staging format.
//
// # No partial packs
//
// A pack that half-loads is a pack whose behaviour nobody can predict from reading it: the
// manifest says one thing, the running system does another, and the difference is a warning
// that scrolled past ten minutes ago. The three choices the ruling names — FIX the pack,
// REMOVE the pack, APPROVE it — are exhaustive precisely because they are the only three that
// end with the manifest and the runtime agreeing.
//
// **There is deliberately no escape hatch.** Every other fatal in this system that has one
// (YOLO_ALLOW_UNREACHABLE_SERVICES, YOLO_ALLOW_STALE_IMAGE) offers it because the user might be
// unable to repair the cause from where they are standing — a host daemon they can only reach
// from outside the jail, a nix build on a disk-starved machine. That does not apply here: the
// approve path is one command the user can run right now, and a fourth "run it anyway" choice
// would resurrect the partial pack the ruling exists to retire.
//
// # Two things that look like this and MUST NOT be fatal
//
//   - A declared bind mount whose HOST PATH IS ABSENT is skipped with a warning
//     (internal/loopholes/runtime.go). That is adaptation inside a capability the user already
//     consented to — nothing was refused, the thing simply is not there.
//   - A contribution whose KIND this build does not recognise is skipped, not fatal, because
//     the host CLI and the baked entrypoint legitimately differ in age (packload's TolerateSkew:
//     "the wrong one is the boot path, where the cost is a jail that will not start"). That is
//     SKEW TOLERANCE, and it surfaces as p.SkewNotes, which this file deliberately does not read.
//
// The line that separates them from the ruling: this is about a claim yolo UNDERSTOOD and
// DECLINED. Something absent, or something from the future, is neither.

import (
	"errors"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packRefusals is every claim this pack made that yolo understood and declined, in the order
// the Honored* family is declared.
//
// EVERY producer, for the same reason packMayAccessHost reaches for one merged claim helper
// rather than a hand-built subset: the set that refuses the launch and the set `pack install`
// prompts for must be the same set, or a user approves everything they were shown and the
// launch still refuses over a producer nobody mentioned.
//
// HonoredPlugins is in the list even though the launch path never consulted it before — that is
// the point rather than an oversight. A wrapped plugin's hooks travel INSIDE the pack's skills
// tree, which the launch stages into the jail whole, so a fetched pack's unapproved hook bodies
// reached the agent's lifecycle events with the refusal computed nowhere on this path at all.
// It is row 21 of the trust inventory, and this is where it closes.
func packRefusals(p *packload.Pack) []string {
	var refused []string
	if _, r := p.HonoredHostFiles(); len(r) > 0 {
		refused = append(refused, r...)
	}
	if _, r := p.HonoredMounts(); len(r) > 0 {
		refused = append(refused, r...)
	}
	// Per contribution: a pack mixing an npm install with a curl-to-shell installer is refused
	// for the installer alone, so the message names the one thing that needs fixing.
	if _, r := p.HonoredInstalls(); len(r) > 0 {
		refused = append(refused, r...)
	}
	if _, r := p.HonoredLoopholes(); len(r) > 0 {
		refused = append(refused, r...)
	}
	if _, r := p.HonoredPlugins(); len(r) > 0 {
		refused = append(refused, r...)
	}
	refused = append(refused, p.RefusedBriefingOverlays()...)
	return refused
}

// refusedLaunchError is the whole user experience of this failure, which is the reason it is
// this long.
//
// Before the ruling a user with a selected-but-unapproved fetched pack got a warning and a
// working jail; now they get no jail. A fatal the reader cannot act on would be strictly worse
// than the warning it replaces, so the message owes them three things and states all three:
// WHICH PACK, WHICH SPECIFIC CLAIM (the packload refusals carry the URL, the path, the loophole
// name — never "a claim"), and the THREE WAYS OUT, with the actual command for the approve path.
//
// It reads in the register internal/entrypoint's reachability refusal set: a lead sentence that
// says what happened, the per-item detail, then an indented block that says why this is a
// refusal rather than a warning and what to do about it.
func refusedLaunchError(refused []string) error {
	var b strings.Builder
	b.WriteString("packs: REFUSING TO LAUNCH — a pack asks for something you have not approved.\n")
	for _, msg := range refused {
		b.WriteString("\n  " + msg + "\n")
	}
	b.WriteString("\n" +
		"  yolo does not run a pack with some of its contributions switched off: a pack that\n" +
		"  half-loads is one whose behaviour nobody can predict from reading it\n" +
		"  (docs/design/trust-paths.md, OQ-TP6). There are exactly three ways forward, and\n" +
		"  they are the only three that end with the manifest and the jail agreeing:\n" +
		"      FIX     — edit the pack so it stops asking for what was refused\n" +
		"      REMOVE  — delete it from `packs` in " + paths.UserConfigPath() + "\n" +
		"      APPROVE — run `yolo pack install`, which shows every claim the pack makes\n" +
		"                and records your yes in the lockfile")
	return errors.New(b.String())
}
