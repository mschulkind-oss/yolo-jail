package cli

// unknownflags.go closes requirement 3's unmet half
// (docs/reference/self-documenting-cli.md): "misuse exits non-zero with usage on stderr".
//
// The command GROUPS (`config`, `pack`, `apply`, `describe`, `check-deps`, `host`, `programs`)
// already exit 2 on an unknown flag, and the reference calls that "the shape the rest should take".
// The flag-parsing commands did not: their scans are `switch` statements with no default arm, so a
// typo'd flag was dropped and the command returned its normal — usually 0 — code. `checkOptions`
// said so in its own comment.
//
// # Why silence is worse here than a wrong answer
//
// A dropped flag is not a smaller version of a refused one, it is a different failure: the operator
// believes they asked for something, the machine believes they asked for the default, and the exit
// code agrees with the machine. `prune` is the sharpest case and already had a bespoke guard for
// exactly it — someone passing `--keep-images 8` got a pass that removed every image no workspace
// pointed at, with no indication the number did nothing. This generalizes that guard.

import (
	"fmt"
	"io"
	"strings"
)

// refuseUnknownFlags reports whether argv carries a flag this command does not define, printing the
// misuse to errw. A true return means the caller must exit 2.
//
// # What counts as a flag, and where scanning stops
//
// A token is a candidate only if it starts with "-" and is longer than one character, so a bare "-"
// (stdin, by convention) is a positional. `--flag=value` is split on the first "=" so the NAME is
// what gets matched.
//
// ⚠ Scanning STOPS at a bare "--", and that is load-bearing rather than tidy: `yolo -- claude
// --dangerously-skip-permissions` passes everything after the separator to the container, and those
// are the wrapped program's flags, not yolo's. Treating them as yolo's would refuse the launch that
// is this tool's entire purpose.
//
// ⚠ THE SEPARATOR IS NOT THE ONLY BOUNDARY, and this function only knows about that one. `run`
// also has an IMPLICIT command start — `yolo run claude --resume` carries no `--`, and the first
// bare token begins the inner command — so its caller passes a PRE-TRUNCATED slice, the boundary
// coming from parseRunArgs, which is the switch that defines it. A caller with a positional command
// form must do the same; handing this function the whole argv refuses the wrapped program's flags.
//
// A value that itself looks like a flag (`--format --json`) is therefore reported rather than
// consumed. That is deliberate: the reference requires a refused `--format` to leave stdout empty,
// so a nonsense value being loud is the desired end, not a false positive to engineer around.
func refuseUnknownFlags(cmd string, args, known []string, errw io.Writer) bool {
	defined := make(map[string]bool, len(known))
	for _, k := range known {
		defined[k] = true
	}
	var unknown []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if len(a) < 2 || !strings.HasPrefix(a, "-") {
			continue
		}
		name := a
		if i := strings.Index(name, "="); i >= 0 {
			name = name[:i]
		}
		if !defined[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return false
	}
	// One line per unknown flag, then the pointer to the authority — help is the list, and
	// reprinting it here would be a second copy to keep true.
	for _, u := range unknown {
		fmt.Fprintf(errw, "yolo %s: unknown flag %q\n", cmd, u)
	}
	fmt.Fprintf(errw, "Run `yolo %s --help` for the flags this command defines.\n", cmd)
	return true
}
