package entrypoint

// launchflags.go is the THIRD spelling of a pack's declared launch flags
// (docs/design/declaration-parity.md §5.6, DP-B44, OQ-DP7): the one a non-interactive
// in-jail shell takes.
//
// THREE SPELLINGS, ONE INJECTOR AND ONE RECORD. A pack's `launch` flags reach a program
// three ways, and they are three MECHANISMS because the entry points are disjoint
// (§5.6.1) — not because anyone wanted three:
//
//   - `yolo -- claude` on the HOST, rewritten above the backend dispatch before the
//     container exists (run.injectLaunchFlagsDisclosed);
//   - `claude` typed at the jail's interactive prompt, through a `.bashrc` alias
//     (packAliases);
//   - `claude` in ANY OTHER in-jail shell — an agent's own `bash -c claude`, a build
//     script, a Makefile — which expands no alias and passes through no host argv. That
//     one carried NO flags at all until this file: DP-B44.
//
// What is unified is the INJECTOR and the RECORD, which is all that can be: every one of
// the three folds the table by calling packload.InjectLaunchFlags and renders the
// packload.LaunchInjection it returns. Nothing here re-assembles an argv from
// LaunchFlagsFor beside it — that second fold is what the 2026-09-13 unification deleted
// from the alias path, and re-introducing it here would put it back one file over.
//
// THE CARRIER IS ~/.yolo/bin/launch, which is second on PATH on every backend (BootPath,
// the .bashrc export, and macosuser.SandboxPath — the three copies AGENTS.md names). A
// script in that dir therefore mediates a bare-name call from ANY shell, which is exactly
// the reach the alias does not have. Two consequences the ruling accepts rather than
// dissolves: the flags reach commands nobody typed at yolo (a build script calling
// `claude` gets the permission bypass, which is consistent with autonomy being ON at the
// `jail` notch), and there is exactly one production fold of launch flags and it hardcodes
// the autonomous posture (DP-B23, sharpened rather than fixed).

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// NoLaunchFlagsEnv runs ONE invocation without the flags a pack declared for it.
//
// It exists because this change took the old escape away. While the alias was the only
// in-jail carrier, `\claude` ran the program bare — bash's own alias bypass, documented in
// the alias disclosure. A launcher is on PATH, so `\claude` now reaches it too and the
// flags come back; without a switch the only remaining bypass would be knowing the install
// prefix by heart. That is a user's own command line, not a yolo defect, which is the line
// AGENTS.md draws for an escape hatch — and the sibling generated dir already ships the
// same shape for the same reason (YOLO_BYPASS_SHIMS, for the blockers).
//
// It is read by the generated script at RUN time, not baked: the question it answers is
// about one invocation, and the boot that wrote the launcher has no opinion on it.
const NoLaunchFlagsEnv = "YOLO_NO_LAUNCH_FLAGS"

// launchFlagsFor returns the record of the rewrite a bare `<bin>` gets in this jail, or nil
// when no pack declares a flag for that name.
//
// THE ONE CALL that folds the table, for every generated carrier and for the alias. It
// takes the BARE argv `<bin>` because that is the only argv a generator has: the user's
// real arguments do not exist until the script runs, and the skip rule they need is
// re-stated in the shell by launchFlagsShellFn (pinned against this function by
// TestTheGeneratedLauncherInjectsWhatTheInjectorWould).
//
// The posture is the RENDER TARGET's (launchAutonomy), the same bit the surface render hands
// packoverlay.Collect, so the flags a carrier bakes and the config a surface renders always
// come from one posture: autonomous in a jail, guarded for a host-targeted Env.
func launchFlagsFor(e *Env, packs []*packload.Pack, bin string) *packload.LaunchInjection {
	_, inj := packload.InjectLaunchFlags(packs, launchAutonomy(e), []string{bin})
	return inj
}

// launchAutonomy is the posture bit every launch-flag fold in this package reads: the render
// target's, never a literal (docs/plans/notch-convergence.md item 20).
func launchAutonomy(e *Env) bool { return e.renderTarget().Profile().AgentAutonomy }

// launchFlagBins lists every binary some pack declares launch flags for, in the order the
// caller should walk them — sorted, so two boots of one jail write the same files and
// disclose the same lines.
//
// It is the SUPERSET of the bins that get a launcher: a pack may declare flags for a name
// it does not install (the image's, the workspace's, another pack's), and the host argv
// rewrite has always honoured that. A carrier must exist for every one of them or the
// in-jail spellings diverge from the host one on a fact the user cannot see.
func launchFlagBins(e *Env, packs []*packload.Pack) []string {
	var out []string
	for bin := range packload.LaunchFlagsFor(packs, launchAutonomy(e)) {
		out = append(out, bin)
	}
	sort.Strings(out)
	return out
}

// launchFlagSplices returns the two template sentinel replacements carrying inj's flags.
//
// Both obey the splice contract on npmLauncherTemplate: a shquote'd literal landing in a
// bare position — the "1"/"0" on an assignment's right-hand side, the flags inside
// `LAUNCH_FLAGS=(…)`, which is the one place in these templates where the word splitting
// Join leaves behind is intended rather than tolerated.
//
// HAS_LAUNCH_FLAGS is a separate switch rather than a `${#LAUNCH_FLAGS[@]}` test for
// HAS_UPDATE_VERB's reason, stated in both templates: bash before 4.4 treats an empty
// array's expansion as unbound under `set -u`, and macos-user runs these launchers against
// a stock /bin/bash 3.2.
func launchFlagSplices(inj *packload.LaunchInjection) []string {
	var flags []string
	if inj != nil {
		flags = inj.Flags
	}
	return []string{
		"__YOLO_HAS_LAUNCH_FLAGS__", shquote.Quote(boolFlag(len(flags) > 0)),
		"__YOLO_LAUNCH_FLAGS__", shquote.Join(flags),
	}
}

// launchFlagsShellFn is the shell half of packload.InjectLaunchFlags, embedded by every
// generated carrier: the npm and native launchers, the package-manager launcher, and the
// wrapper.
//
// IT IS A SECOND IMPLEMENTATION OF ONE RULE, and that is a cost this file pays with its
// eyes open. The flags themselves are folded ONCE, in Go, and baked; what the shell has to
// decide is the one thing generation time cannot know — whether the argv the user actually
// typed already carries a flag yolo is about to add. Without that test every `yolo --
// claude` would arrive here with the host's rewrite already applied and get the flag a
// second time, on the most common path there is. The rule is therefore kept literal and
// tiny (equal, or `--flag=` prefixed — packload.hasFlag), and pinned against the Go
// injector by a differential test rather than by two comments agreeing.
//
// YOLO_ARGV is a global the caller expands at its exec site. A function cannot return an
// argv in bash, and the alternative — a command substitution — cannot carry an argument
// with a newline in it.
const launchFlagsShellFn = `
# --- pack-declared launch flags (declaration-parity.md §5.6, DP-B44) -----------------
# The flags are BAKED (folded once, in Go, by packload.InjectLaunchFlags). What is decided
# here is only what generation time cannot know: whether this invocation's own argv already
# carries one — which is how a "yolo -- <bin>" launch, whose flags the HOST already
# injected, arrives here and does not get them twice.
_yolo_launch_argv() {
    YOLO_ARGV=("$@")
    if [ "$HAS_LAUNCH_FLAGS" != "1" ] || [ "${` + NoLaunchFlagsEnv + `:-}" = "1" ]; then
        return 0
    fi
    local i f a present
    # Reverse, prepending each: the declared ORDER is preserved, and each flag is compared
    # against the argv as it stands, so a flag declared twice is added once. Same walk as
    # the Go injector's.
    for (( i=${#LAUNCH_FLAGS[@]} - 1; i >= 0; i-- )); do
        f=${LAUNCH_FLAGS[$i]}
        present=0
        for a in ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"}; do
            # The quoted halves are LITERAL even when a flag carries a glob character; the
            # bare * is the pattern. "--flag=value" counts as the flag being present, which
            # is packload.hasFlag's rule.
            case "$a" in
                "$f"|"$f="*) present=1; break ;;
            esac
        done
        if [ "$present" = "0" ]; then
            YOLO_ARGV=("$f" ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"})
        fi
    done
}
`
