package cli

// hostapplygate.go is THE LAUNCH HOOK: at `yolo host -- <bin>`, behave like a jail launch and
// then exec (docs/design/host-apply-staleness.md §4.1, §4.3, §4.4).
//
// # Why the launch is the only moment
//
// `yolo host apply` renders pack surfaces into the real $HOME and nothing ever re-checks them,
// so the rendered and would-be-rendered states drift apart silently. Agents read their config
// at startup and do not reload it, so a render that is stale while nothing reads it is not a
// problem and the same render stale at the instant an agent starts is the whole problem (§1
// P1). Every generated wrapper already execs through here, which makes this the one place the
// question is worth asking — and the reason the design adds no per-command check, no
// fingerprint and no standalone notice.
//
// # What it compares
//
// The RENDER, not the config (OQ-HS9). A host approval snapshot mirroring the jail's
// `approvals/<name>.json` would be cheaper and needs no predicate, and it is structurally blind
// to a hand-edited `~/.claude/settings.json`: the config never moved, so nothing would prompt.
// The comparison runs through applyHostSurveyed — the apply itself, in observe posture, with
// its output captured — so the gate cannot describe a render the apply would not perform.
//
// # What it never does
//
//   - It does not add a second confirmation. `confirmHostLosses` and the skills/briefing
//     adoption gates still own the one-way doors, reached through the ordinary applyHost when
//     this gate applies (§7).
//   - It does not run in a jail, at all. `render.Host` targets the invoking user's real home
//     and paths.Home() in a jail is /home/agent, so there is no host home in here to be stale.
//   - It does not touch `yolo run` or `yolo host apply`. Both take
//     `--accept-config-changes`; honoring the environment variable there would buy nothing and
//     would let one shell-rc line pre-approve every jail launch on the machine (§1 P4).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// acceptConfigChangesEnv grants the config-change approval for ONE wrapped host launch.
//
// AN ENVIRONMENT VARIABLE, on this path and this path only, and it is not a contradiction of
// `config.AcceptConfigChangesFlag`'s ruling that a per-launch approval must be a flag — it is
// the answer to a different question (OQ-HS10). For `yolo run` the choice is flag-vs-env-var
// and there the variable is pure cost. Here the wrapper body is fixed —
// `exec yolo host -- claude "$@"` (internal/hostwrap.Body) — and hostMain hands everything
// after `--` to the program, so a user typing `claude --print foo` has NO SLOT for a
// yolo-level flag. The choice is env-var-vs-nothing, and "nothing" means a scripted agent
// launch can never proceed.
//
// The cost is real and accepted knowingly (maintainer ruling 2026-09-03): exported in a shell
// profile, this becomes de facto standing consent for every wrapped launch in that shell. Two
// containments make that tolerable rather than a hole, and both are load-bearing:
//
//  1. IT IS HONORED HERE AND NOWHERE ELSE. Not by `yolo run`, not by `yolo host apply` — see
//     the file header.
//  2. THE WRAPPER MUST NOT BAKE IT IN. A generator that wrote the grant into the wrapper body
//     when a config key said so is the obvious next step and is REFUSED: it converts a
//     per-shell act into a permanent one, which is the standing consent §1's retraction
//     forbids. The variable is tolerable *because* someone has to type it.
//
// Named to match the flag it stands in for, so the two read as one grant in two spellings and
// a refusal can offer whichever channel its reader can reach. The constant lives beside the
// refusal that names it, following snapshot.go's rule for exactly this: the spelling a user is
// told to set and the spelling the code reads cannot drift apart.
const acceptConfigChangesEnv = "YOLO_ACCEPT_CONFIG_CHANGES"

// hostApplyGateBudget bounds the observe pass (§4.4).
//
// A STUCK-DETECTOR, NOT A TUNING KNOB, which is why there is no config key and no environment
// variable for it: the observe pass measured 11.4 ms warm (§5), so a second is three orders of
// magnitude of headroom and anything past it means a cold or network-mounted $HOME rather than
// a value someone should be adjusting. On expiry the gate reports cannot-determine and execs —
// a launch must never hang on a check it can decline to make.
//
// A package var only so a test can shrink it, following flockSyscall's convention. Nothing but
// a test reassigns it.
var hostApplyGateBudget = time.Second

// hostGateCanPrompt reports whether this process can put a question to a human.
//
// STDIN, not stdout, and the difference is a real case: `claude --print foo > out.txt` has a
// redirected stdout and a perfectly good terminal on stdin, and refusing that launch as
// "nobody to ask" would be false. It is the same probe the jail's own approval gate uses
// (run.Options.IsTTYStdin, read by config.CheckConfigChanges), so the two notches agree about
// what "no TTY" means.
//
// A package var for hostApplyGateBudget's reason.
var hostGateCanPrompt = func() bool { return isTTY(os.Stdin) }

// hostApplyGate implements §4.3's table. It returns true to proceed with the exec and false to
// abort the launch; every false path has already explained itself and named a remedy (§1 P5).
//
// errw, not out: everything printed here lands on stderr, including the prompt. A wrapped
// launch is one exec away from being the agent, and an agent's stdout is routinely parsed
// (`claude --print`), so a gate that wrote to stdout would corrupt the very launches it is
// least entitled to disturb.
func hostApplyGate(errw io.Writer, stdin io.Reader, bin string) bool {
	// IN-JAIL IS A HARD NO-OP, checked first and before the key: the config an in-jail process
	// reads is the generated snapshot, and the inherit census strips this key from it — but a
	// hand-written config or an older snapshot could still carry it, and there is no host home
	// in here for it to be about. The discriminator is config.InJail(), the same one every
	// other in-jail behaviour reads.
	if config.InJail() {
		return true
	}
	// NOT OPTED IN IS TOTAL SILENCE. The default, and what makes "no launch and no command
	// mentions any of this" true for everyone who never asked (§11).
	if !config.HostApplyOnLaunchEnabled() {
		return true
	}

	survey, why := surveyHostApplyWithinBudget()
	if survey == nil {
		// CANNOT DETERMINE (§4.4): a malformed pack manifest, an unreadable home, an
		// unresolvable file:// pack, a budget overrun. The predicate has no answer, so there is
		// no change to refuse over — exec, with at most one line. Per internal/version's
		// srcskew house rule, a gate that cannot prove its condition does not fire.
		fmt.Fprintf(errw, "yolo host: could not check whether your host render is up to date "+
			"(%s) — launching %s anyway.\n", why, bin)
		return true
	}
	if !survey.Changes() {
		// The common case, and the one R3 is about: silence. A freshly-applied home must
		// prompt not at all, ever, until something actually changes.
		return true
	}

	if hostGateCanPrompt() {
		reportHostApplyGateChanges(errw, bin, survey)
		if !promptYesNo(errw, stdin, fmt.Sprintf("  Apply these and launch %s? [y/N] ", bin)) {
			// DECLINE ABORTS, as it does in the jail (OQ-HS5). Launching anyway would make the
			// question a formality, and applying anyway would make "no" mean nothing.
			fmt.Fprintf(errw, "yolo host: not applied — %s was not launched.\n"+
				"  Launch it unchanged by turning off `host_apply_on_launch` in %s, or by "+
				"running the real binary directly.\n", bin, paths.UserConfigPath())
			return false
		}
		return hostApplyGateApply(errw, stdin, bin)
	}

	// NO TTY. The approval is the only thing that can stand in for the human, and it may only
	// arrive through the environment because no flag can reach this process (see
	// acceptConfigChangesEnv). PRESENCE, NOT TRUTH-PARSING: any non-empty value grants,
	// matching YOLO_ALLOW_STALE_IMAGE's consent probe — consent is about intent, not about the
	// token. A variable set to `0` by someone expecting "off" is the one plausible objection,
	// and the house precedent goes the other way.
	if os.Getenv(acceptConfigChangesEnv) != "" {
		return hostApplyGateApply(errw, stdin, bin)
	}
	refuseHostApplyGate(errw, bin, survey)
	return false
}

// hostApplyGateApply runs the ordinary writing apply and reports whether the launch may
// proceed.
//
// IT IS THE ORDINARY applyHost, deliberately: every one-way-door confirmation the explicit
// command has — confirmHostLosses, the skills and briefing adoption gates — is reached through
// it, unchanged, on exactly the conditions it already fires on. This design adds a new MOMENT
// for those prompts, never a second mechanism (§7).
//
// A FAILED APPLY ABORTS THE LAUNCH, which the design's table does not cover and which follows
// from what the user just asked for: they said "apply these and launch", so exec'ing an agent
// against a home the apply did not finish is the "it looked like it worked" outcome the whole
// gate exists to remove. The render is idempotent, so the next launch converges (§4.4).
func hostApplyGateApply(errw io.Writer, stdin io.Reader, bin string) bool {
	if rc := applyHost(errw, errw, false, true, stdin); rc != 0 {
		fmt.Fprintf(errw, "yolo host: the host apply did not complete (rc=%d, see above) — %s "+
			"was not launched.\n"+
			"  Fix what it reported and run `yolo host apply --assert`, then launch again.\n",
			rc, bin)
		return false
	}
	return true
}

// reportHostApplyGateChanges names what a re-apply would change, for the reader who is about
// to be asked about it.
//
// THE CHANGE LIST, NOT A UNIFIED DIFF, and that is a deliberate boundary rather than a
// shortfall. `yolo host apply --dry-run` already renders the per-key detail — which managed key
// would be overwritten, which entry would be replaced, which comment would not survive — and
// naming the command is how the reader gets it. Re-rendering that detail here would be a second
// reporter for the same facts at a surface where it has to stay short: this text interrupts
// somebody starting an agent, and a fifty-line report at that moment is a report nobody reads.
func reportHostApplyGateChanges(errw io.Writer, bin string, survey *hostApplySurvey) {
	fmt.Fprintf(errw, "yolo host: %d host destination(s) are out of date, and %s reads its "+
		"config at startup:\n", len(survey.Changed), bin)
	for _, c := range survey.Changed {
		fmt.Fprintf(errw, "  %-14s %-24s %s\n", c.Kind, c.Surface, c.Path)
	}
	fmt.Fprintf(errw, "  (`yolo host apply --dry-run` shows exactly what changes in each.)\n")
}

// refuseHostApplyGate is the non-TTY refusal (OQ-HS6).
//
// ITS READER TYPED `claude`, NOT `yolo` (§1 P5, risk R2), so an unexplained failure here reads
// as "claude is broken". Three things therefore have to be in the message: what stopped, that
// it was yolo and which key of theirs asked for it, and the remedy IN A SPELLING THIS READER
// CAN USE. Both remedies are given because they suit different readers — the two-step apply
// leaves nothing behind in the environment and is what an interactive reader should reach for,
// while a scripted caller that cannot pass a flag needs the variable.
//
// # The two-step remedy is `yolo host apply --assert`, with no flag after it
//
// The design writes it as `yolo host apply --assert --accept-config-changes` (§4.3), and that
// command does not exist: hostApply's parser accepts --assert, --dry-run and --shell-init and
// exits 2 on anything else. Teaching it the flag was considered and is REFUSED by the design's
// own §7 — *"It does not change the explicit `apply` path. `yolo host apply` keeps
// observe-by-default and keeps its fail-closed confirmations."* The flag would have to stand in
// for those confirmations to mean anything there, which is exactly the fail-closed gate
// TestApplyHostFirstApplyFailsClosedWithoutStdin exists to hold.
//
// Nor is it needed: `--accept-config-changes` grants the JAIL's config-approval, and the host
// apply has no such approval to grant. Its gates are stdin-driven one-way doors over a
// first-ever apply, a different mechanism with a different answer. So step 1 is the bare
// `--assert`, which is all the remedy needs — and if the apply does have a one-way door to ask
// about, it says so itself, on its own terms, which is the only place that question belongs.
func refuseHostApplyGate(errw io.Writer, bin string, survey *hostApplySurvey) {
	fmt.Fprintf(errw, "yolo host: refusing to launch %s — %d host destination(s) are out of "+
		"date and this launch has no terminal to approve the update on.\n",
		bin, len(survey.Changed))
	for _, c := range survey.Changed {
		fmt.Fprintf(errw, "  %-14s %-24s %s\n", c.Kind, c.Surface, c.Path)
	}
	fmt.Fprintf(errw, "\nA change to your real home is never applied without someone saying so, "+
		"and a scripted launch is exactly where nobody is watching. This check is "+
		"`host_apply_on_launch` in %s.\n\n"+
		"Apply it first, which leaves nothing behind in your environment:\n"+
		"  yolo host apply --assert\n"+
		"  %s ...\n\n"+
		"Or approve THIS launch only, on the one channel a wrapper leaves open:\n"+
		"  %s=1 %s ...\n",
		paths.UserConfigPath(), bin, acceptConfigChangesEnv, bin)
}

// surveyHostApplyWithinBudget runs the observe pass under §4.4's budget and returns the
// roll-up, or nil plus the one-phrase reason it has no answer.
//
// IT RUNS THE APPLY, in observe posture, with the report captured and thrown away. Growing a
// separate traversal of the four written kinds here would be a second model of what an apply
// does, free to drift out of step with the apply it describes — the shape AGENTS.md records as
// having shipped five times.
//
// A NON-ZERO rc IS CANNOT-DETERMINE, not a change: an observe pass exits non-zero for an inert
// pack, an unresolvable config-overlay, a doubly-owned config surface. Every one of those is a
// pack-authoring problem `yolo check` owns, and none of them is something a launch may stop
// over.
//
// The goroutine is abandoned on expiry rather than cancelled, because the pass is synchronous
// filesystem work with no cancellation point. It writes only to its own buffer and only in
// observe posture, so an abandoned one cannot touch the home; the exec that follows ends it.
func surveyHostApplyWithinBudget() (*hostApplySurvey, string) {
	type outcome struct {
		survey *hostApplySurvey
		rc     int
	}
	done := make(chan outcome, 1)
	go func() {
		var sink bytes.Buffer
		survey := &hostApplySurvey{}
		rc := applyHostSurveyed(&sink, &sink, false, false, nil, survey)
		done <- outcome{survey, rc}
	}()
	select {
	case got := <-done:
		if got.rc != 0 {
			return nil, fmt.Sprintf("`yolo host apply` reports a problem of its own (rc=%d); "+
				"run `yolo check`", got.rc)
		}
		return got.survey, ""
	case <-time.After(hostApplyGateBudget):
		return nil, fmt.Sprintf("the check did not finish within %s", hostApplyGateBudget)
	}
}
