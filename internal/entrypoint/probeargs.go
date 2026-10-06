package entrypoint

import "github.com/mschulkind-oss/yolo-jail/internal/shquote"

// probeargs.go is the launcher half of a program's VERSION PROBE (packdecl.Contribution.ProbeArgs;
// docs/design/pi-extension-store-builds.md XB-D24, which coined the term, and XB-D51, which built
// it): an invocation whose first argument is one the pack declares, answered by the program before
// it does any work of its own.
//
// WHY. Every step a launcher runs before its exec is there for the program's run: the hourly
// update, the MCP servers' refresh, the pre-launch refresh and its lock, the authentication step,
// the model menu, the tree gate. A version probe reads none of their results, and each can hold it:
// `pi --version` ran pi's update, the servers' refresh and `pi update --extensions`, could wait
// 60 s for the refresh's lock and 60 s more for the refresh, and rewrote the extension store (§9 of
// that design, MEASURED). So the launcher reads the first argument once, at the top, and every one
// of those steps asks _YOLO_PROBE first.
//
// WHAT STILL RUNS, AND WHY. A cold install, since without it there is no program to answer; and a
// fork's materialize, since a home holding an older build does not hold this launch's program. The
// launch flags and the agent's own env file are kept too: they change what the program is handed,
// not what it waits for, and a probe should run the program the next launch will.
//
// DECLARED BY THE PACK and never keyed on `--version` in core, for the refresh's reason
// (prelaunchrefresh.go): which words a vendor answers at once is that vendor's fact. pi answers
// `--help` only after resolving its packages and installing a missing one unlocked, so pi's help is
// not a probe, and skipping the locked refresh in front of it would hand that install to pi.

// probeArgsDeclShell is the probe's BAKED declaration and its one reading of the invocation,
// spliced into every template's header beside the refresh's. It is top-level shell, not a
// function, because only there is "$1" the invocation's own first argument. Same splice contract
// as npmLauncherTemplate: every sentinel is a shquote'd literal in a bare position. HAS_PROBE_ARGS
// gates the array for HAS_UPDATE_VERB's reason: bash before 4.4 treats "${arr[@]}" on an EMPTY
// array as unbound under "set -u", and macos-user runs these launchers on a stock bash 3.2.
const probeArgsDeclShell = `# The pack's declared PROBE ARGUMENTS (pi-extension-store-builds.md XB-D24, XB-D51): an
# invocation whose FIRST argument is one is a version probe, which skips every step below that
# its answer never reads. BAKED, like everything above; _YOLO_PROBE is 1 for a probe.
HAS_PROBE_ARGS=__YOLO_HAS_PROBE_ARGS__
PROBE_ARGS=(__YOLO_PROBE_ARGS__)
_YOLO_PROBE=0
if [ "$HAS_PROBE_ARGS" = "1" ] && [ "$#" -gt 0 ]; then
    for _yolo_probe_arg in "${PROBE_ARGS[@]}"; do
        if [ "$1" = "$_yolo_probe_arg" ]; then _YOLO_PROBE=1; fi
    done
fi
`

// probeArgsSplices renders the probe's sentinel pairs for a strings.Replacer, riding on the end
// of each generator's pair list as refreshSplices does. Join, not Quote, for the list: several
// words landing in the bare `PROBE_ARGS=(…)`, each one quoted.
func probeArgsSplices(args []string) []string {
	return []string{
		"__YOLO_HAS_PROBE_ARGS__", shquote.Quote(boolFlag(len(args) > 0)),
		"__YOLO_PROBE_ARGS__", shquote.Join(args),
	}
}
