package entrypoint

// launchwrapper.go is the INSTALLER/WRAPPER SPLIT that OQ-DP7 rules must be paid for
// before the third launch spelling can close (docs/design/declaration-parity.md §5.6.1 (3),
// DP-B44).
//
// THE SPLIT, AND WHY THERE HAD TO BE ONE. Until this file, one generated script did two
// jobs: it INSTALLED a pack's program lazily, and it was the thing PATH resolved when
// somebody typed the program's name. Injecting a pack's launch flags at that script's exec
// is what closes the third spelling — but `launcherShadows` (launchercollision.go) writes
// no such script for a name /bin or a declared mise tool already provides, because a lazy
// INSTALLER must never stand in front of a binary the image ships (defect 11.1). So
// injecting there and stopping would have delivered the permission bypass for most packs
// and silently dropped it for the baked ones: a divergence keyed on something the user
// cannot see, which is the "accepted and not honored" shape the parity catalog exists to
// name. "Write the installer anyway when it carries flags" is not the fix either — it
// installs a second copy of a binary the image already ships.
//
// So the two jobs become two ARTEFACTS. The installer keeps its collision check exactly as
// it was — nothing in this file relaxes it, and in particular nothing here teaches it about
// the install prefixes, which is the one widening AGENTS.md forbids outright (spelled "is
// this name already resolvable on PATH?", the check switches evergreen delivery off after
// its own first success). What this file adds is a WRAPPER: a script that installs nothing,
// resolves the program exactly where PATH would have resolved it without the wrapper
// present, and execs it with the pack's declared flags. It is written for every name a pack
// declares flags for that does not already have a carrier — the shadowed ones, and the
// names no pack installs at all.
//
// THE RESULT IS TOTAL, AND THAT IS THE POINT: after this pass, every binary any pack
// declares `launch` flags for has a script in ~/.yolo/bin/launch that injects them. There
// is no "most packs" left to be partial about.

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// DeliverLaunchFlags guarantees a carrier for every pack-declared launch flag, and states
// on the launch terminal what the carriers do.
//
// IT RUNS LAST of the three launch-dir generators, and the order is load-bearing rather
// than tidy. GenerateAgentLaunchers resets the dir and writes the pack installers;
// GeneratePackageManagerLaunchers adds core's own (pnpm). Both of those already splice the
// flags for the names they write (launchFlagSplices), so what is left for this pass is
// exactly the names neither of them covered. Running it earlier would put a wrapper where
// an installer belongs: GeneratePackageManagerLaunchers skips any name a file already
// occupies ("a pack already claimed this bin name"), so a wrapper written first would take
// pnpm's lazy install away from it.
//
// A BIN WITH A CARRIER IS LEFT ALONE, and the check is the file's existence rather than a
// second derivation of "would the installer pass have written this?". Two derivations of
// one fact is what the alias path was unified to stop doing; the file on disk is the fact.
func DeliverLaunchFlags(e *Env) error {
	packs, err := LoadJailPacks(e)
	if err != nil {
		return err
	}
	launchDir := e.LaunchDir()
	probePath, miseBins := imageProbePath(e), declaredMiseBins(e)

	var delivered []*packload.LaunchInjection
	var wrapped []string
	for _, bin := range launchFlagBins(packs) {
		inj := launchFlagsFor(packs, bin)
		if inj == nil {
			// Unreachable: launchFlagBins lists the bins WITH flags. Kept because the two
			// answers come from one injector and a future skip rule could disagree with
			// the enumeration, and a dropped flag must not be the way we find out.
			continue
		}
		if !packdecl.ValidBinName(bin) {
			// The wrapper is FILED at filepath.Join(launchDir, bin); a traversal bin would
			// write outside the anchor into the jail's persistent home. Same
			// defense-in-depth as the installer loop's copy of this guard.
			e.warn("pack " + inj.Pack + ": declares launch flags for an unusable binary " +
				"name " + shquote.Quote(bin) + " — no wrapper was written and the flags " +
				"reach nothing in this jail")
			continue
		}
		delivered = append(delivered, inj)
		path := filepath.Join(launchDir, bin)
		if pathExists(path) {
			// An installer (or the package-manager generator) already wrote a carrier for
			// this name, and it carries the same flags from the same injector.
			continue
		}
		provider := launcherShadows(bin, probePath, miseBins)
		if provider == "" {
			provider = "no pack installs it"
		}
		body := launchWrapper(bin, launchDir, wrapperFallback(e, bin, probePath, miseBins),
			provider, inj)
		if err := writeExecutable(path, body); err != nil {
			return err
		}
		wrapped = append(wrapped, bin)
	}
	discloseLaunchFlagDelivery(e, delivered, wrapped)
	return nil
}

// wrapperFallback is the absolute path the wrapper falls back to when the PATH it runs
// under does not name the program — a caller that scrubbed the environment, or macos-user's
// `env -i`.
//
// IT IS A FALLBACK, NEVER THE PRIMARY, and the asymmetry is the wrapper's whole contract:
// the program a bare name means is whatever PATH resolves it to AT RUN TIME, and a
// generation-time answer is a snapshot that a later install, a mise activation or a store
// farm can make wrong. Baking it anyway costs one string and turns "nothing happened" into
// "the program ran" on the environments that have no useful PATH at all.
//
// "" when nothing can be named, which is the common case: a name no pack installs and the
// image does not provide has no honest fallback, and the wrapper says so at run time rather
// than exec'ing a guess.
func wrapperFallback(e *Env, bin, probePath string, mise map[string]struct{}) string {
	if p := lookPathIn(probePath, bin); p != "" {
		return p
	}
	if _, declared := mise[bin]; declared {
		// DECLARED, not installed: on a cold boot the shim does not exist yet, which is
		// exactly why declaredMiseBins reads the declaration rather than the directory.
		// The wrapper tests -x before using it, so naming a path that may not be there yet
		// costs nothing and covers the boot after the tool is installed.
		return filepath.Join(e.MiseShims(), bin)
	}
	return ""
}

// launchWrapper renders the wrapper for one binary. Same splice contract as the three
// launcher templates (see npmLauncherTemplate): every sentinel is a shquote'd literal
// landing in a bare position.
func launchWrapper(bin, launchDir, fallback, provider string,
	inj *packload.LaunchInjection) string {
	r := strings.NewReplacer(append([]string{
		"__YOLO_BIN__", shquote.Quote(bin),
		"__YOLO_LAUNCH_DIR__", shquote.Quote(launchDir),
		"__YOLO_FALLBACK_BIN__", shquote.Quote(fallback),
		"__YOLO_PROVIDER__", shquote.Quote(provider),
		"__YOLO_PACK__", shquote.Quote(inj.Pack),
	}, launchFlagSplices(inj)...)...)
	return r.Replace(launchWrapperTemplate)
}

// launchWrapperTemplate is the body of a launch-flag wrapper: no install, no update, no
// stamp, no lock, no receipt. Everything the launcher templates carry exists to manage an
// install this script deliberately does not do.
//
// IT MUST BE TRANSPARENT EXCEPT FOR THE FLAGS. The program it runs is the one the bare name
// would have resolved to with this file absent — the next PATH entry that is not the launch
// dir — so a wrapper for a name the image bakes runs the image's binary, and a wrapper for a
// declared mise tool runs the shim, each with its declared flags and nothing else changed.
// Resolving by PATH rather than by a baked path is what keeps that true after an install
// moves the answer.
const launchWrapperTemplate = `#!/bin/bash
# Launch-flag WRAPPER (declaration-parity.md §5.6, DP-B44). yolo installs nothing for this
# name — PROVIDER below says what does. All this script adds is the flags the pack declared,
# to every invocation, in any shell. Delete-proof by construction: it is regenerated at each
# boot from the pack manifest.
set -euo pipefail
BIN=__YOLO_BIN__
LAUNCH_DIR=__YOLO_LAUNCH_DIR__
FALLBACK_BIN=__YOLO_FALLBACK_BIN__
PROVIDER=__YOLO_PROVIDER__
PACK=__YOLO_PACK__
HAS_LAUNCH_FLAGS=__YOLO_HAS_LAUNCH_FLAGS__
LAUNCH_FLAGS=(__YOLO_LAUNCH_FLAGS__)
` + launchFlagsShellFn + `
# NOTHING TO INSTALL AND NOTHING TO REFRESH, and both callers reach this script BY NAME.
# "yolo pack update" runs <launch dir>/<bin> with YOLO_PACK_UPDATE=1 for every program a
# pack declares, and "yolo capture" runs ` + "`env YOLO_INSTALL_ONLY=1 <bin>`" + `. Falling
# through to the exec below would RUN the agent in both cases — which is the trap
# refreshPrograms' own comment names for the package-manager launchers, arriving here by a
# different door. Exit 0: a name yolo does not install has genuinely nothing to refresh.
if [ "${YOLO_PACK_UPDATE:-}" = "1" ] || [ "${YOLO_INSTALL_ONLY:-}" = "1" ]; then
    echo "  $BIN: yolo installs nothing for this name — $PROVIDER. Nothing to refresh." >&2
    exit 0
fi

# The physical spelling of our own dir, resolved ONCE. A second spelling of it on PATH (a
# symlinked home is the live way to get one) would resolve this script back to itself, and
# an exec of yourself is a fork bomb rather than a wrong answer.
LAUNCH_DIR_PHYS=$(cd "$LAUNCH_DIR" 2>/dev/null && pwd -P) || LAUNCH_DIR_PHYS="$LAUNCH_DIR"

# _resolve answers "what would this name have meant without this file?" — the next PATH
# entry that is not our own dir. It is deliberately not "$FALLBACK_BIN" first: the baked
# path is a boot-time snapshot, and an install, a mise activation or a package farm can all
# make it the wrong answer while PATH stays right.
_resolve() {
    local dir p phys
    local IFS=:
    for dir in ${PATH:-}; do
        if [ -z "$dir" ]; then dir=.; fi
        if [ "$dir" = "$LAUNCH_DIR" ]; then continue; fi
        p="$dir/$BIN"
        if [ -x "$p" ] && [ ! -d "$p" ]; then
            phys=$(cd "$dir" 2>/dev/null && pwd -P) || phys="$dir"
            if [ "$phys" = "$LAUNCH_DIR_PHYS" ]; then continue; fi
            printf '%s\n' "$p"
            return 0
        fi
    done
    if [ -n "$FALLBACK_BIN" ] && [ -x "$FALLBACK_BIN" ]; then
        printf '%s\n' "$FALLBACK_BIN"
        return 0
    fi
    return 1
}

REAL_BIN=$(_resolve) || REAL_BIN=""
if [ -z "$REAL_BIN" ]; then
    # 127 is the shell's own "command not found", which is what this is: the wrapper adds
    # flags to a program it does not provide, and pack $PACK declared those flags for a name
    # nothing in this jail supplies.
    echo "  ⚠ $BIN: not found on PATH, and yolo installs nothing for it — $PROVIDER." >&2
    echo "    (pack $PACK declares launch flags for this name; it did not declare the program.)" >&2
    exit 127
fi

` + agentEnvShellFn + `
_yolo_launch_argv "$@"
exec "$REAL_BIN" ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"}
`

// discloseLaunchFlagDelivery states, once per boot, that a pack's declared flags now reach
// EVERY invocation of these names in this jail — not only the prompt.
//
// IT IS ONE LINE AND IT DOES NOT RENDER THE REWRITES, which is the whole of its design.
// The per-binary before/after belongs to the writer that produces it: the host's block for
// an argv it rewrote (run.noteLaunchFlagInjection), the alias disclosure for the line it
// wrote into the .bashrc (discloseShellAliases, DP-B42). Rendering the same rewrite a third
// time, forty lines earlier in the same boot, is how a disclosure surface becomes the
// wallpaper OQ-BP-3 names. What this line adds is the fact neither of those can state: the
// REACH. A user who read the alias disclosure and concluded "so a script gets the plain
// binary" would be wrong, and that conclusion was CORRECT until this pass existed.
//
// SILENT WHEN NO PACK DECLARED A FLAG, which is most jails — the same exactness the other
// two disclosures keep, and for the same reason.
//
// NOT SUPPRESSIBLE: a launch has no quiet mode (OQ-RO3), and this is a disclosure rather
// than progress. NoLaunchFlagsEnv turns the injection off for one command; nothing turns
// the sentence off.
func discloseLaunchFlagDelivery(e *Env, delivered []*packload.LaunchInjection, wrapped []string) {
	if len(delivered) == 0 {
		return
	}
	var bins []string
	for _, inj := range delivered {
		bins = append(bins, inj.Before[0])
	}
	e.warn("yolo adds pack-declared launch flags to EVERY invocation of " +
		strings.Join(bins, ", ") + " in this jail — a script's and an agent's, not just " +
		"the ones you type (" + e.LaunchDir() + "/<name> is the script; " +
		NoLaunchFlagsEnv + "=1 runs one without them).")
	if len(wrapped) > 0 {
		// The wrapper is the half a reader cannot infer from the pack manifest: for these
		// names yolo installs NOTHING, and the program comes from wherever it already came
		// from. Saying so is what keeps the installer warning above it ("no launcher for X
		// — the image provides Y") from reading as a dropped declaration.
		e.warn("  " + strings.Join(wrapped, ", ") + ": yolo installs nothing for these " +
			"names — the wrapper adds the flags and runs whatever PATH already provides.")
	}
}
