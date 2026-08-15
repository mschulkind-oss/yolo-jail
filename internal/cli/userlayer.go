package cli

// userlayer.go is the front-door half of `--user-layer <file>` (OQ-LP9 R4): layer a config
// file in at user-level precedence, explicitly, for this one invocation.
//
// IT IS A GLOBAL FLAG, stripped before subcommand resolution, and that placement is the
// design rather than a convenience. The flag has to reach `run` (so a nested launch composes
// from the layer), `check` (so an agent can verify what it just wrote), `loopholes` (so a
// freshly-declared loophole is listed) and `pack` (so a pack named only in the layer
// resolves). Adding it to four flag parsers would guarantee the fifth command forgets it —
// and a `--user-layer` that changed a launch but not the command you verify with would send
// an agent hunting for a bug in the feature instead of reading the flag it forgot to pass.
//
// NO APPROVAL GATE, ON THE HOST OR IN A JAIL. That is a ruling, not an omission:
// docs/design/gate-placement-principle.md Test 1 — passing an argv to `yolo` requires the
// ability to run commands, which already exceeds anything this argument grants. Anyone who
// can pass it can equally edit the user config file. A prompt here would refuse an actor who
// has already cleared a stronger bar.
//
// It REPLACES a `config.local.jsonc` proposal, withdrawn with cause: a conventionally-named
// auto-merged file activates because a file EXISTS, invisibly at the call site. This is
// visible in the command line, testable, and inert unless passed.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// userLayerFlag is the flag's name in both spellings.
const userLayerFlag = "--user-layer"

// stripUserLayer removes `--user-layer <file>` / `--user-layer=<file>` from args and returns
// the remaining args plus the path ("" when not passed).
//
// It stops at `--`, so a `--user-layer` meant for the COMMAND the jail runs
// (`yolo -- mytool --user-layer x`) is left alone. Getting that wrong would let yolo eat an
// inner command's flag, which is the classic argv-rewriting bug and is why the boundary is
// explicit here rather than implied by the loop.
//
// A trailing `--user-layer` with no value yields ok=false so the caller can refuse loudly:
// silently treating it as unset would ignore an argument the user typed.
func stripUserLayer(args []string) (rest []string, path string, ok bool) {
	ok = true
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			return rest, path, ok
		}
		switch {
		case a == userLayerFlag:
			if i+1 >= len(args) || args[i+1] == "--" {
				ok = false
				continue
			}
			i++
			path = args[i]
		case strings.HasPrefix(a, userLayerFlag+"="):
			path = strings.TrimPrefix(a, userLayerFlag+"=")
			if path == "" {
				ok = false
			}
		default:
			rest = append(rest, a)
		}
	}
	return rest, path, ok
}

// applyUserLayerFlag strips the flag, validates the file, and publishes it to the config
// loader for the rest of the process. Returns the remaining args and ok=false when the
// invocation must be refused (the message is already printed).
//
// Validation happens HERE, once, at the front door — before any config is loaded — because
// an explicitly-named layer that cannot be read must fail the command rather than be
// skipped. That is the whole difference between an argument and a convention.
func applyUserLayerFlag(args []string, errw io.Writer) ([]string, bool) {
	rest, path, ok := stripUserLayer(args)
	if !ok {
		fmt.Fprintf(errw, "yolo: %s needs a file argument (a JSONC config to layer in at "+
			"user-level precedence for this invocation)\n", userLayerFlag)
		return rest, false
	}
	if path == "" {
		return rest, true
	}
	if msg := config.ValidateUserLayer(path); msg != "" {
		fmt.Fprintln(errw, "yolo: "+msg)
		return rest, false
	}
	// Published through the environment so it reaches EVERY user-scope reader in the
	// process, including the three that deliberately bypass the merged config and read the
	// user file directly (LoadPacks, LoadHostFiles, LoadCacheRelocations). See
	// config.UserLayerEnv for why a threaded parameter would have been the weaker choice.
	// It also survives into any child `yolo` this process execs, which is what makes the
	// layer part of the effective config a nested launch composes from.
	if err := os.Setenv(config.UserLayerEnv, path); err != nil {
		fmt.Fprintf(errw, "yolo: could not publish %s: %v\n", userLayerFlag, err)
		return rest, false
	}
	return rest, true
}
