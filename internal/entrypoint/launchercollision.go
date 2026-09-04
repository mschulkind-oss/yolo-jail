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

import (
	"path/filepath"
	"strings"
)

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
	base := "/bin:/usr/bin"
	if p := e.Vars["YOLO_DARWIN_LOGIN_PATH"]; p != "" {
		base = p
	}
	var out []string
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
// GenerateAgentLaunchers runs at boot.go:439 and ConfigureMisePrism at :491, so the mise
// shim directory is EMPTY when this is asked on a cold boot. A check that read the
// directory would find nothing, write the launcher, and let an agent-class mechanism
// shadow a project dependency on exactly the boots where the project is new — which is
// what P6 forbids (§3.5's `pnpm` note is the live case).
//
// A mise tool key may carry a backend prefix (`npm:pnpm`, `cargo:ripgrep`); the bin is the
// last segment. That is a heuristic and it is the RIGHT direction to be wrong in: over-
// naming a bin costs one launcher that is not written, while under-naming it costs a
// shadowed project dependency.
func declaredMiseBins(e *Env) map[string]struct{} {
	out := map[string]struct{}{}
	tools := loadInjectedTools(e)
	for _, key := range tools.Keys() {
		name := key
		if i := strings.LastIndex(name, ":"); i >= 0 {
			name = name[i+1:]
		}
		name = filepath.Base(name)
		if name != "" && name != "." && name != "/" {
			out[name] = struct{}{}
		}
	}
	return out
}

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
