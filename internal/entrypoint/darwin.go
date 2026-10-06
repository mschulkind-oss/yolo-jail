package entrypoint

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// darwin.go is the native-macOS generation entry (J2 §2): the analog of the
// Linux boot loop's content-generation steps, run in-process by the sandbox
// user via `yolo internal darwin-bootstrap`, run in-process rather than via a
// generated bootstrap script.
//
// It runs the SAME pure generators the container boot runs — they are already
// pure functions of *Env (env.go), so pointing Env.Home/Workspace at the
// sandbox user's real macOS paths makes them correct natively. Which steps this
// boot runs is the boot step table's answer (bootsteps.go): every container-only
// step (LD cache, cgroup delegation, port forwarding, the daemon supervisor, the
// container bootstrap/venv scripts, …) is excluded there by name, with its reason.
//
// Behavioral verification of the Mac side (does the sandbox user actually get a
// working PATH, do the login-rc files win after path_helper) is a Track M / M1
// checklist item; in-jail this is covered by unit tests on the pure writers and
// a GOOS=darwin cross-build.

// DarwinBootstrapOptions carries the sandbox-specific inputs the darwin
// generation entry needs beyond what Env already holds.
type DarwinBootstrapOptions struct {
	// MacosLog gates the yolo-log helper: "off" | "user" | "full".
	MacosLog string
	// YoloLogScript is the yolo-log helper body (macosuser.MacosLogWrapperScript).
	// Passed in rather than generated here to keep this package free of the
	// macosuser dependency (macosuser imports entrypoint, not the reverse).
	YoloLogScript string
	// Version is this binary's own build stamp (version.Baked, "" when unstamped), for the
	// boot log's header. The container's header reads YOLO_VERSION instead, and this
	// bootstrap's environment must never carry that variable: it is the jail marker, so
	// config.InJail would answer true for this process and for every child it spawns.
	Version string
}

// DarwinEnvFrom builds the bootstrap Env from the launcher's env-var contract, and
// is the ONE place that translation lives.
//
// It exists because the translation is not mechanical — three of these are darwin
// rebindings the container boot does not make (the real workspace instead of the
// literal /workspace, /usr/bin instead of /bin for shim realbins, BSD stat instead of
// GNU) — and a caller that assembled an Env by hand would be a second implementation
// of that contract, free to drift. It was one, briefly: the first darwin harness
// built its own Env, left Workspace at the container default, and every generator
// writing a workspace sidecar failed on `mkdir /workspace: read-only file system`.
//
// `lookup` is the environment reader (os.Getenv in production), passed so a test can
// exercise the real translation on a synthetic environment rather than reimplementing
// it.
func DarwinEnvFrom(vars map[string]string, home string) *Env {
	e := NewEnv(vars)
	e.Home = home
	// Native platform values (J2 §1 seams): the real workspace, the macOS shim bin,
	// and BSD stat.
	if ws := vars["YOLO_DARWIN_WORKSPACE"]; ws != "" {
		e.Workspace = ws
	}
	e.ShimBinDir = "/usr/bin"
	e.GNUStat = false
	// The MCP preset WRAPPERS are not generated on this backend (see RunDarwinBootstrap),
	// so nothing installs the npm packages behind them either. Set here, with the other
	// two platform seams, because it is the same kind of fact: what this environment can
	// actually provide, decided once at the translation rather than at each consumer.
	e.SkipMCPPresets = true
	// Program readiness is rendered into the same bootstrap on macos-user; the host admits
	// the confined stage when it cannot prove every selected program is already present.
	return e
}

// RunDarwinBootstrap generates the sandbox user's jail config natively: the same
// shims/launchers/bashrc/mise/identity/per-agent writers the container runs, plus the
// macOS-only pieces (the home layout and overlay, the provisioning script, the yolo-log
// helper, the login-rc PATH re-prepend).
//
// It runs THE boot step table (bootsteps.go), the one the container entrypoint runs, and
// each step this boot does not run is an exclusion declared there with its reason. It was a
// second, hand-kept list, and a step the container gained was missing here unless someone
// remembered it (docs/plans/notch-convergence.md, row D10).
//
// A12: a generator failure is FATAL here too, and returning it is the whole point — its
// caller used to print "bootstrap ok" unconditionally. Every step still runs, so one
// invocation reports every problem; see genStep.
//
// IT KEEPS THE CONTAINER'S BOOT LOG, <workspace>/.yolo/boot.log, rotated to boot.log.prev the
// same way (attachDarwinBootLog): a terminal line lands in both, a log-only note in the log
// alone, and the log's last line says whether the bootstrap refused. That is also what the
// orphan catalog's one line points at for the names.
func RunDarwinBootstrap(e *Env, opts DarwinBootstrapOptions) error {
	stderr, logOnly := e.Stderr, e.LogOnly
	blog := attachDarwinBootLog(e, opts.Version)
	runBootSteps(&bootRun{e: e, target: bootDarwin, darwin: opts})
	err := genFailuresError(e)
	blog.finish(err)
	// The log is closed, so the writers it installed go back to the caller's: a MultiWriter
	// over a closed file stops at the file, and a later write through this Env would reach
	// neither sink.
	e.Stderr, e.LogOnly = stderr, logOnly
	return err
}

// attachDarwinBootLog is attachBootLog for the macos-user bootstrap: the same
// <workspace>/.yolo/boot.log, written by the sandbox account through the workspace grant that
// already lets it create <workspace>/.yolo/prism, plus one header line naming this backend and
// the binary's version (the container's header reads YOLO_VERSION, which this env lacks).
//
// ONLY FOR A WORKSPACE THE LAUNCH NAMED. Every launch and every capture sets
// YOLO_DARWIN_WORKSPACE (macosuser.buildBootstrapEnv), and DarwinEnvFrom turns it into
// e.Workspace. An Env without it is a test or a hand-run, and its WorkspaceDir is the
// container's literal /workspace, which is not this backend's workspace: inside a jail it is
// the live jail's own, whose boot.log a test would rotate away. e.Workspace cannot answer
// this, because NewEnv never leaves it empty. Never fatal, like the container's: every
// failure returns nil, and e.Stderr still writes where it wrote.
func attachDarwinBootLog(e *Env, version string) *bootLog {
	if e.Getenv("YOLO_DARWIN_WORKSPACE") == "" {
		return nil
	}
	stderr := e.Stderr
	if stderr == nil {
		// io.MultiWriter cannot take a nil writer, and a nil Stderr discards (Env.Stderr).
		stderr = io.Discard
	}
	bl := attachBootLog(e, stderr)
	if bl == nil {
		e.Stderr = stderr
		return nil
	}
	if version == "" {
		version = "unstamped"
	}
	fmt.Fprintf(bl.f, "  macos-user bootstrap, yolo %s\n", version)
	return bl
}

// InstallHomeOverlay copies the staged CONTENT tree ($YOLO_DARWIN_HOME_OVERLAY) over
// the sandbox home. Unset or absent → no-op, which is the state of a launch whose packs
// declare no skills and no briefing.
//
// WHY A COPY RATHER THAN A MOUNT: this backend has none. The container path delivers
// each staged dir with a `-v …:ro` bind, which is also what makes its copy READ-ONLY to the
// agent. Here the read-only half is the session's Seatbelt profile (G14): every write,
// rename and unlink of what this copies is denied to the agent at the physical path it lands
// at (macosuser.ResolveHomeReadonly). That deny cannot get in THIS function's way, because
// the bootstrap runs outside the sandbox — its argv carries no sandbox-exec — so the next
// launch replaces the delivered trees exactly as it always has. The files are the sandbox
// user's own, so the file mode protects nothing; the profile is the whole guarantee, and only
// a Mac run proves the kernel honors it.
//
// The host laid the tree out at the destinations the container would have mounted, and
// listed those destinations beside it (HomeOverlayManifestName). This installs each listed
// destination and nothing else — darwinoverlay.go says why the list has to exist (G36:
// inferring the granularity from the tree deleted pi's whole state dir on every launch).
// The list names roots the tree already spells, so there is still no mapping logic here to
// drift from the mount assembler's.
//
// OVERWRITE, NOT MERGE, per destination: the overlay is authoritative for the paths it
// lists, exactly as a bind mount is. A skill a pack stopped shipping must DISAPPEAR from
// the home, and a merge would keep serving it forever. The rest of the home — credentials,
// sessions, generated settings, anything the agent wrote beside or above a destination —
// is untouched, because no destination contains it.
//
// ⚠ IT LANDS AT THE PATH THE PROFILE PROTECTS, OR NOWHERE. The content rules are computed on
// the host before this runs, by joining each destination onto the resolved workspace and
// account home through the layout's links (macosuser.ResolveHomeReadonly). That is the path
// the kernel reports only if nothing below those two bases is a symbolic link the layout did
// not lay, and the sidecar is in the agent-writable workspace. So `packs` derives the SAME
// layout the layout step laid (darwinHomeLayoutFor): a link in the sidecar refuses the
// delivery, exactly as it refused the layout, and each destination's route from the home
// passes through no link but the layout's own (overlayLinks.route).
func InstallHomeOverlay(e *Env, packs []*packload.Pack) error {
	src := e.Vars["YOLO_DARWIN_HOME_OVERLAY"]
	if src == "" {
		return nil
	}
	layout, _ := darwinHomeLayoutFor(e, packs)
	if linked := layout.linkedSidecarPaths(); len(linked) > 0 {
		return fmt.Errorf("skills, briefings and pack files were not delivered: %w", &LinkedSidecarError{Links: linked})
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			// Staged tree missing is not a boot failure: the launch may have raced a
			// teardown, and the agent is better off starting with no skills than not
			// starting. The warning is the record.
			e.warn("home overlay " + src + " is not present; skills, briefings and pack files were not delivered")
			return nil
		}
		return err
	}
	roots, err := openOverlayInstallRoots(e.Home, e.DarwinSidecar())
	if err != nil {
		return err
	}
	defer closeOverlayRoots(roots)
	return installHomeOverlayDestinations(src, e.Home, roots, overlayLinksOf(layout))
}

// InstallYoloLog writes the yolo-log helper to ~/.local/bin/yolo-log (0755) —
// the macOS unified-logging analog of the Linux jail's yolo-journalctl bridge.
// An empty script is a no-op (the "off" mode still writes a stub via the
// caller's MacosLogWrapperScript, so empty only happens if the caller opts out).
func InstallYoloLog(e *Env, script string) error {
	if script == "" {
		return nil
	}
	binDir := filepath.Join(e.Home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	return writeExecutable(filepath.Join(binDir, "yolo-log"), script)
}

// WriteLoginRC re-prepends the sandbox PATH in the login rc files (.zprofile, .zshrc,
// .bash_profile). macOS path_helper (/etc/zprofile, /etc/profile) reorders PATH to put
// /usr/local/bin first; these rc files run AFTER it, so the nix-store packages + agent
// shims win again. Bare binaries / plain `-c` shells don't read these and keep the baked
// env -i PATH. This carries the OQ-1 path_helper fix, measured on hardware 2026-09-10.
//
// ⚠ THE PATH IS READ FROM THE ENVIRONMENT, NOT BAKED, and that is the point. $HOME is
// shared by every workspace on this machine — the home-tier layout deliberately leaves it
// that way (macos-user-home-tiers.md §5, "stated residuals") — and this file sits at its
// root, below every symlink the layout lays. A literal PATH here is therefore ONE
// workspace's `packages:` store dirs written into a file the next workspace's login shell
// reads: the same cross-workspace race the sidecar closed for briefings, in three files
// nobody would think to look at.
//
// The launch exports the value (macosuser.sandboxEnvPairs, from the same SandboxPath call
// that builds PATH itself), so the indirection costs nothing and cannot disagree with the
// PATH it is restoring. Unset — a shell nothing yolo launched — leaves PATH alone, which is
// the honest answer: there is no workspace to re-prepend for.
func WriteLoginRC(e *Env) error {
	rc := "# yolo-jail: re-prepend the sandbox PATH AFTER macOS path_helper reorders it.\n" +
		"# The value is NOT baked here: this file is in a home every workspace shares.\n" +
		"if [ -n \"${" + DarwinLoginPathEnv + ":-}\" ]; then\n" +
		"  export PATH=\"$" + DarwinLoginPathEnv + ":$PATH\"\n" +
		"fi\n"
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile"} {
		if err := os.WriteFile(filepath.Join(e.Home, name), []byte(rc), 0o644); err != nil {
			return err
		}
	}
	return nil
}
