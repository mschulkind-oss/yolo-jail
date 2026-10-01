package entrypoint

// launchercollision.go is the generation-time half of B2
// (docs/design/program-delivery.md §3.5, OQ-PD12a).
//
// THE TWO HALVES ARE ONE DECISION. Moving the launch dir ahead of the install prefixes is
// what makes a launcher reachable past its own first install — that is the whole of the
// evergreen mechanism, and without it the hourly update has been running in a house nobody
// visits twice (measured: claude.stamp last touched 2026-08-25, nine days before the plan
// was written). Refusing to WRITE a launcher for a name the image already provides is what
// keeps that position safe.
//
// IT CONVERTS A STRUCTURAL IMPOSSIBILITY INTO A HANDLED CASE, and that is the honest cost.
// With the launch dir after /bin, a pack declaring `program fzf` simply could not shadow
// the image's /bin/fzf: AGENTS.md called the failure "unrepresentable rather than handled".
// Under B2 the protection moves from POSITION to this CHECK, so a bug in it is now
// expressible where before it was not — which is why the test that matters is the one that
// fails when the check is deleted, not one that shows the check works.
//
// A SECOND AXIS LIVES HERE, and it is the same QUESTION rather than the same reason: "why is
// there no launcher for this declared program?" The shadow axis answers "because something
// else already provides the name"; the PLATFORM axis answers "because the vendor publishes no
// build for this machine" (a pack `program`'s `platforms` list, packdecl.Install). Both end in
// one warned line and no launcher, which is why they are one file — the loophole side of the
// tree makes the same call for the same reason (internal/cli/run/loopholeinert.go: "ONE
// MECHANISM, TWO AXES … splitting them would produce two half-messages for one user-visible
// situation").

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// imageProbeBase is the image's own bin dirs. A var only so a test that runs the boot path
// on a HOST can point it away from the host's /bin (OverrideImageProbeBase): a host with
// codex, pi or pnpm installed system-wide otherwise suppresses exactly the launchers that test
// asserts are written.
var imageProbeBase = "/bin:/usr/bin"

// OverrideImageProbeBase points the image dirs the collision check searches at dirs, a PATH
// value, and returns the function that restores the previous value. A TEST SEAM, for a test
// in another package that drives the launcher generators on a host (internal/cli/run's
// credential-gate jail); this package's own tests start from an empty stand-in that TestMain
// installs. Production code never calls it: a boot searches the image's /bin and /usr/bin.
func OverrideImageProbeBase(dirs string) (restore func()) {
	prev := imageProbeBase
	imageProbeBase = dirs
	return func() { imageProbeBase = prev }
}

// imageProbePath is the PATH the collision check searches: what the IMAGE provides, with
// every per-home install prefix removed.
//
// THE SCOPE IS THE WHOLE FEATURE, and the natural wider reading is a silent kill switch.
// Spelled as "is this name already resolvable on PATH?", the check destroys what it
// protects: after one successful install ~/.local/bin/claude exists, so the next boot
// writes no launcher, so PATH resolves the installed binary directly, and evergreen works
// exactly once. Green, silent, and identical to the freeze this design exists to end. So
// the install prefixes — $NPM_CONFIG_PREFIX/bin, $HOME/.local/bin, $GOBIN — are excluded,
// and they are excluded by the property that DEFINES them rather than by a list: every one
// of them lives under the jail home, and nothing the image ships does.
//
// On macos-user there is no image, and $YOLO_DARWIN_LOGIN_PATH is the honest stand-in: the
// native nix store prefix `packages:` materializes into, plus the system dirs. Filtering it
// by the same home rule leaves exactly that and drops the sandbox's own prefixes — the same
// reasoning agentPath uses to decide which PATH a `requires` probe counts.
func imageProbePath(e *Env) string {
	base := imageProbeBase
	if p := e.Vars[DarwinLoginPathEnv]; p != "" {
		base = p
	}
	var out []string
	// STORE-DELIVERED PACKAGES COUNT AS "WHAT THE LAUNCH PROVIDES", for the same reason
	// /bin does and NOT for the reason the install prefixes are excluded. Under C4/C5 an
	// opt-in launch takes `packages:` (and the image's bulk extras) out of the image and
	// delivers them from the mounted nix store instead
	// (docs/reference/image-staging-vs-baking.md, "Store-delivered packages"). A name that was
	// in /bin is then here,
	// so omitting this dir would let a pack-declared launcher shadow a tool the workspace
	// asked for BY NAME — defect 11.1, arriving through the door C4 opens.
	//
	// This is not the widening the header warns about. The kill switch there is spelling
	// the check as "is this name already resolvable on PATH?", which folds in the dirs a
	// launcher INSTALLS INTO and so stops writing the launcher after its own first
	// success. Nothing installs here: the farm is written at boot from the config's own
	// declarations and is empty again on the next boot. Gated on the DECLARATION
	// (StoreProfiles) rather than on the directory's existence, so a launch that bakes is
	// unaffected even if a stale dir survives somehow.
	if len(StoreProfiles(e)) > 0 {
		out = append(out, StorePackagesBin())
	}
	for _, dir := range strings.Split(base, ":") {
		if dir == "" || underJailHome(e, dir) {
			continue
		}
		out = append(out, dir)
	}
	return strings.Join(out, ":")
}

// underJailHome reports whether dir is the jail home or lives inside it.
func underJailHome(e *Env, dir string) bool {
	home := strings.TrimSuffix(e.Home, "/")
	if home == "" {
		return false
	}
	return dir == home || strings.HasPrefix(dir, home+"/")
}

// declaredMiseBins is the set of binary names the DECLARED mise tools provide.
//
// DECLARED, NOT INSTALLED, and the ordering makes that mandatory rather than tidy:
// boot.go's genStep list runs GenerateAgentLaunchers BEFORE ConfigureMisePrism, so the mise
// shim directory is EMPTY when this is asked on a cold boot. (Named by symbol rather than by
// line: the ORDER is the fact, and the numbers move whenever a genStep is added.) A check
// that read the directory would find nothing, write the launcher, and let an agent-class
// mechanism shadow a project dependency on exactly the boots where the project is new —
// which is what P6 forbids (§3.5's `pnpm` note is the live case).
//
// A mise tool key may carry a backend prefix (`npm:pnpm`, `cargo:ripgrep`); the bin is the
// last segment (config.MiseToolBin, which says why that heuristic errs the right way).
//
// THE HOST ASKS THE SAME QUESTION OF THE SAME TABLE. config.MergeMiseDisabledTools drops
// pnpm from MISE_DISABLE_TOOLS for exactly the declarations that withhold the pnpm launcher
// here, so mise delivers the declared tool. Before it did, a declared `pnpm` key lost both
// doors: no launcher, and mise told to hide it.
func declaredMiseBins(e *Env) map[string]struct{} {
	out := map[string]struct{}{}
	tools := loadInjectedTools(e)
	for _, key := range tools.Keys() {
		if name := config.MiseToolBin(key); name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

// A LAUNCHER FOR A PROGRAM ITS VENDOR DOES NOT PUBLISH HERE is declined too, by the one
// installable-program predicate, packdecl.Install.UnpublishedReason (docs/plans/
// notch-convergence.md item 7), which the host's dep probe asks as well. It lived here as
// launcherUnpublished while only a jail asked it.
//
// NO LAUNCHER AND A LINE — not a launch refusal, and not silence. Three precedents already
// in the tree decide this, and none of them is a refusal: launcherShadows declines one
// launcher and warns; `packages[].platforms` FILTERS the nix package list
// (config.filterPackagesForPlatform); and a loophole whose `platforms` exclude this machine
// goes inert with one disclosed line (run.notePackLoopholesInert). A refusal would also be
// wrong on its own terms — a pack is more than its program (omp also contributes skills, a
// briefing, a config surface and state), so refusing the launch turns a degraded pack into
// an unusable one, and the platform is the one fault class no user can act on.
//
// It is deliberately NOT folded into launcherShadows: the shadow question is asked of a
// PATH, this one of a DECLARATION, and the call site asks the shadow question FIRST. That
// order is loopholeinert's ("BACKEND BEATS PLATFORM … the line the user needs is the one
// they can act on"): if the image already provides the binary, "the image provides /bin/x"
// is both true and useful, while "your vendor has no build" would alarm about a tool the
// jail has. The host keeps the same order: a binary already on its PATH is reported present.

// launcherShadows reports WHY a launcher for bin must not be written, or "" when it is
// safe to write one. The string is a reason, phrased to be dropped into a warning.
func launcherShadows(bin, probePath string, mise map[string]struct{}) string {
	if _, declared := mise[bin]; declared {
		return "the workspace declares it as a mise tool"
	}
	if p := lookPathIn(probePath, bin); p != "" {
		return "the image provides " + p
	}
	return ""
}
