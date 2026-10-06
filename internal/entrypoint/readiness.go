package entrypoint

// readiness.go is the jail notch's READINESS ACT (docs/design/jail-notch-readiness.md, OQ-JR1):
// every program a selected pack declares is installed in the provisioning stage, before the
// command runs, and a program the stage cannot install STOPS the launch.
//
// # Where it runs, and what it runs
//
// In the bootstrap script (shell.go), after the Node floors and before the stage's verdict, so
// it runs after the CA bundle and after `mise install`, in the one place a launch already
// installs over the network (§3 of the design names why it cannot go in launcher generation:
// `yolo check` runs the generators, and an observe verb must install nothing).
//
// What it runs is each program's OWN LAUNCHER, in install-only mode (InstallOnlyEnv): the npm,
// installer and source launchers each install when the program is absent and stop without
// running it, and do nothing at all when it is present. So there is one implementation of each
// install, the launcher's, and the readiness act is a caller of it, as `yolo pack update` and
// `yolo capture` are. Install-only never refreshes a program that is present: currency stays at
// invocation (OQ-PD12a), and readiness is about presence alone (§1 of the design).
//
// # Which programs
//
// Every program a SELECTED pack declares that got a launcher this boot (OQ-JR2, answered by
// HP-DIR2: no narrowing to what the launch might run). The set is decided by the same
// predicates GenerateAgentLaunchers applies, in the same order, so a program it declined is not
// one readiness asks for: a name the image or a declared mise tool already provides is ready
// already, and a program whose vendor publishes no build for this platform has no launcher and
// keeps the generator's own line, as launchercollision.go rules for that fault class. A test
// holds the two lists together (TestReadinessAsksForExactlyTheLaunchersTheBootWrote).
//
// # What a failure does
//
// It refuses the launch, naming the pack, the program and the install's error, offline
// included, and a degraded launch is opt-in, never the default (the maintainer's ruling on
// OQ-JR1, 2026-10-05: "we can have a bypass var or whatever, but by default, no"). The refusal
// is provision.RefusedStatus, the one status the stage passes through without asking.
// paths.AllowMissingProgramsEnv is the hatch the refusal offers: the jail starts and lists each
// program it could not install, and each installs on first use, as before.
//
// # macos-user
//
// macos-user renders this same act into its bootstrap. Its host admits the confined stage
// when it cannot prove every selected program is already present; on a hit, no stage or install
// is needed.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// readyProgram is one program the readiness act installs: its bin, which names its launcher in
// the launch dir, and "program <bin> (pack <name>)", the words the refusal says. inst is the
// declaration, for the macos-user report's presence check.
type readyProgram struct {
	Bin  string
	Who  string
	inst packdecl.Install
}

// declaredReadyPrograms is the set the readiness act installs, in pack-load order.
//
// A boot that cannot load its packs contributes nothing here: it has a louder problem, reported
// on its own path, and a pack that cannot say what it installs cannot be shown to need it.
func declaredReadyPrograms(e *Env) []readyProgram {
	packs, err := LoadJailPacks(e)
	if err != nil {
		return nil
	}
	return readyProgramsOf(e, packs)
}

// readyProgramsOf is declaredReadyPrograms over packs already loaded. Its skips are
// GenerateAgentLaunchers', in that function's order: an unusable bin name, a kind with no
// launcher, a name the image or a mise tool provides (launcherShadows), a platform the vendor
// does not publish for (UnpublishedReason), and a bin an earlier pack already took.
func readyProgramsOf(e *Env, packs []*packload.Pack) []readyProgram {
	probePath, miseBins := imageProbePath(e), declaredMiseBins(e)
	seen := map[string]bool{}
	var out []readyProgram
	for _, p := range packs {
		if p == nil {
			continue
		}
		installs, _ := p.HonoredInstalls()
		for _, inst := range installs {
			if !packdecl.ValidBinName(inst.Bin) {
				continue
			}
			switch inst.Kind {
			case "npm", "native", packdecl.InstallKindSource:
			default:
				continue
			}
			if launcherShadows(inst.Bin, probePath, miseBins) != "" {
				continue
			}
			if inst.UnpublishedReason(runtime.GOOS, runtime.GOARCH) != "" {
				continue
			}
			if seen[inst.Bin] {
				continue
			}
			seen[inst.Bin] = true
			out = append(out, readyProgram{
				Bin:  inst.Bin,
				Who:  "program " + inst.Bin + " (pack " + p.Name + ")",
				inst: inst,
			})
		}
	}
	return out
}

// readinessChecks renders the readiness act's calls for the bootstrap: one `_yolo_ready <bin>
// <who>` line per program, or "" when no selected pack declares one, so a jail with none runs
// and prints exactly what it did before (the design's §7 item 3).
//
// Every value is shquote'd into a bare word: the bin is validated, the pack name is a
// pack-supplied string, and this is shell source.
//
// The capture/build environment renders a notice rather than calls, and installs nothing.
func readinessChecks(e *Env) string {
	progs := declaredReadyPrograms(e)
	if len(progs) == 0 {
		return ""
	}
	if v := e.Getenv(paths.NoProgramReadinessEnv); v == paths.NoProgramReadinessCaptureJail {
		// The launcher's value for a capture or build jail, not the user's: say the jail's
		// reason, not a variable nobody typed.
		return "echo " + shquote.Quote("  ↳ a capture or build jail: its command is the install, "+
			"so nothing is installed ahead of it") + " >&2"
	} else if v != "" {
		var who []string
		for _, p := range progs {
			who = append(who, p.Who)
		}
		return "echo " + shquote.Quote("  ↳ "+paths.NoProgramReadinessEnv+
			" is set, so nothing is installed ahead of the command; each of these installs the "+
			"first time it is run: "+strings.Join(who, ", ")) + " >&2"
	}
	lines := make([]string, 0, len(progs))
	for _, p := range progs {
		lines = append(lines, "_yolo_ready "+shquote.Quote(p.Bin)+" "+shquote.Quote(p.Who))
	}
	return strings.Join(lines, "\n")
}

// allowMissingPrograms is the hatch's value as the bootstrap bakes it: "1" or "0". Baked, like
// every other value in that script, so the stage reads the launch's decision and not whatever its
// own environment happens to hold.
func allowMissingPrograms(e *Env) string {
	return boolFlag(e.Getenv(paths.AllowMissingProgramsEnv) != "")
}

// MissingProgramReadiness reports the launcher-backed selected programs the host cannot prove
// present for a macos-user launch. vars must carry the staged pack root, sandbox login PATH,
// effective environment and merged mise declaration used by the bootstrap. workspaceHome is the
// incoming workspace's physical home sidecar, not a symlink under the sandbox account home (which
// may still point into a different workspace until the bootstrap runs). An error is an unknown
// answer, so the caller starts the stage rather than silently treating it as ready.
func MissingProgramReadiness(vars map[string]string, home, workspace, workspaceHome string) ([]string, error) {
	probeVars := make(map[string]string, len(vars)+1)
	for key, value := range vars {
		probeVars[key] = value
	}
	probeVars["JAIL_HOME"] = home
	if workspace != "" {
		probeVars["YOLO_DARWIN_WORKSPACE"] = workspace
	}
	if workspaceHome != "" {
		probeVars[DarwinHomeSidecarEnv] = workspaceHome
	}
	e := DarwinEnvFrom(probeVars, home)
	root := e.Getenv("YOLO_PACK_ROOT")
	if root == "" {
		return nil, nil
	}
	packs, err := loadPackRootAsStaged(e, root)
	if err != nil {
		return nil, err
	}
	packs, err = packload.ApplyForks(packs)
	if err != nil {
		return nil, err
	}
	layout, _ := darwinHomeLayoutFor(e, packs)
	var missing []string
	for _, p := range readyProgramsOf(e, packs) {
		path, err := programRealBin(e, p.inst, workspace, layout)
		if err != nil {
			return nil, err
		}
		if !isExecutableFile(path) {
			missing = append(missing, p.Who)
		}
	}
	return missing, nil
}

// programRealBin is where the install-only launcher looks for its program. npm uses the
// effective configured prefix; installer and source launchers use the sandbox's local bin.
// resolveIncomingLayoutPath maps either through the same DarwinHomeLayout the bootstrap applies,
// including core links and selected pack workspace directories.
func programRealBin(e *Env, inst packdecl.Install, workspace string, layout DarwinHomeLayout) (string, error) {
	var path string
	if inst.Kind == "npm" {
		prefix := e.NpmPrefix
		if prefix == "" {
			// Match the bootstrap/launcher `${NPM_CONFIG_PREFIX:-$HOME/.npm-global}` default.
			prefix = filepath.Join(e.Home, ".npm-global")
		}
		if !filepath.IsAbs(prefix) {
			if workspace == "" {
				return "", fmt.Errorf("the workspace-relative NPM_CONFIG_PREFIX cannot be resolved")
			}
			if hasParentPathComponent(prefix) {
				return "", fmt.Errorf("the workspace-relative NPM_CONFIG_PREFIX contains a parent traversal")
			}
			prefix = filepath.Join(workspace, prefix)
		}
		path = filepath.Join(prefix, "bin", inst.Bin)
	} else {
		path = filepath.Join(e.LocalBin(), inst.Bin)
	}
	return resolveIncomingLayoutPath(e.Home, path, layout)
}

// resolveIncomingLayoutPath translates the sandbox account-home side of the links which this
// launch will lay to their incoming-workspace targets. This is the bootstrap's layout, derived
// from the staged selected packs, not a second list of known home roots. It must run before any
// filesystem lookup: the account home's existing symlinks still name the previous workspace.
// Unmapped symlinks below the account home are not evidence about this workspace, so they return
// an unknown result and admit the confined stage.
func resolveIncomingLayoutPath(home, path string, layout DarwinHomeLayout) (string, error) {
	if hasParentPathComponent(path) {
		return "", fmt.Errorf("program path contains a parent traversal: %s", path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("program path is not absolute: %s", path)
	}
	path = filepath.Clean(path)
	for traversals := 0; traversals < 40; traversals++ {
		if mapped, ok := incomingLayoutPath(path, layout); ok && mapped != path {
			path = mapped
			continue
		}
		root := filepath.VolumeName(path) + string(filepath.Separator)
		remainder := strings.TrimPrefix(path, root)
		parts := strings.Split(remainder, string(filepath.Separator))
		current := root
		followed := false
		for i, part := range parts {
			if part == "" {
				continue
			}
			current = filepath.Join(current, part)
			if mapped, ok := incomingLayoutPath(current, layout); ok && mapped != current {
				path = filepath.Join(mapped, filepath.Join(parts[i+1:]...))
				followed = true
				break
			}
			info, err := os.Lstat(current)
			if errors.Is(err, fs.ErrNotExist) {
				return path, nil
			}
			if err != nil {
				return "", fmt.Errorf("could not inspect program path %s: %w", current, err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if pathIsWithin(home, current) {
				return "", fmt.Errorf("program path traverses an unselected account-home link: %s", current)
			}
			target, err := os.Readlink(current)
			if err != nil {
				return "", fmt.Errorf("could not read program-path link %s: %w", current, err)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			path = filepath.Clean(filepath.Join(target, filepath.Join(parts[i+1:]...)))
			followed = true
			break
		}
		if !followed {
			return path, nil
		}
	}
	return "", fmt.Errorf("program path has too many symbolic-link traversals")
}

// incomingLayoutPath maps a path at or below one of the actual account-home links to the link's
// workspace target. The longest match wins because a selected pack link may be nested beneath a
// core link such as .config.
func incomingLayoutPath(path string, layout DarwinHomeLayout) (string, bool) {
	links := make([]DarwinHomeLink, 0, len(layout.Links)+len(layout.FileRedirects)+len(layout.HostFileRedirects))
	links = append(links, layout.Links...)
	links = append(links, layout.FileRedirects...)
	links = append(links, layout.HostFileRedirects...)
	bestLen := -1
	var mapped string
	for _, link := range links {
		root := filepath.Clean(link.Path)
		rel, err := filepath.Rel(root, path)
		if err != nil || !pathWithinRel(rel) || len(root) <= bestLen {
			continue
		}
		bestLen = len(root)
		target := filepath.Clean(link.Target)
		if !filepath.IsAbs(target) {
			// Home-root redirects use the same relative target the Darwin bootstrap lays.
			// Resolve it from the link's parent before translating its descendants.
			target = filepath.Join(filepath.Dir(root), target)
		}
		mapped = filepath.Join(target, rel)
	}
	return mapped, bestLen >= 0
}

func hasParentPathComponent(path string) bool {
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." {
			return true
		}
	}
	return false
}

func pathIsWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && pathWithinRel(rel)
}

func pathWithinRel(rel string) bool {
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
