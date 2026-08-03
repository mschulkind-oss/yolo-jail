package entrypoint

import (
	"os"
	"path/filepath"
)

// darwin.go is the native-macOS generation entry (J2 §2): the analog of the
// Linux boot loop's content-generation steps, run in-process by the sandbox
// user via `yolo internal darwin-bootstrap`, run in-process rather than via a
// generated bootstrap script.
//
// It runs the SAME pure generators the container boot runs — they are already
// pure functions of *Env (env.go), so pointing Env.Home/Workspace at the
// sandbox user's real macOS paths makes them correct natively. The Linux-only
// boot steps (LD cache, cgroup delegation, port forwarding, the daemon
// supervisor, the container bootstrap/venv/cglimit/journalctl scripts) are
// deliberately NOT run here — they are no-ops or nonsensical on a native user.
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
	// LoginPath is the PATH to re-prepend in the login rc files (after macOS
	// path_helper reorders it). The caller assembles this from the sandbox
	// shims + darwin store dirs + system (macosuser.SandboxPath).
	LoginPath string
	// YoloLogScript is the yolo-log helper body (macosuser.MacosLogWrapperScript).
	// Passed in rather than generated here to keep this package free of the
	// macosuser dependency (macosuser imports entrypoint, not the reverse).
	YoloLogScript string
}

// RunDarwinBootstrap generates the sandbox user's jail config natively: the same
// shims/launchers/bashrc/mise/MCP/identity/per-agent writers the container runs,
// plus the two macOS-only pieces (yolo-log helper, login-rc PATH re-prepend).
//
// A12: a generator failure is FATAL here too, and returning it is the whole point
// — this path is easy to miss (it has its OWN nine genStep sites and its own
// configureAgent loop, so an earlier count of the fail-open sites missed it
// entirely) and its caller used to print "bootstrap ok" unconditionally. Every
// step still runs, so one invocation reports every problem; see genStep.
func RunDarwinBootstrap(e *Env, opts DarwinBootstrapOptions) error {
	genStep(e, "generate_shims", func() error { return GenerateShims(e) })
	genStep(e, "generate_agent_launchers", func() error { return GenerateAgentLaunchers(e) })
	genStep(e, "generate_package_manager_launchers", func() error { return GeneratePackageManagerLaunchers(e) })
	// Warn about any absent `requires` binary (generates nothing, so not a genStep). It
	// matters MORE here than in a container: macos-user bakes no image at all, so a
	// required tool comes from the user's own machine or not at all.
	AssertRequiredBins(e)
	genStep(e, "generate_bashrc", func() error { return GenerateBashrc(e) })
	genStep(e, "generate_mise_config", func() error { return ConfigureMisePrism(e) })
	genStep(e, "generate_mcp_wrappers", func() error { return GenerateMCPWrappers(e) })
	configureGit(e)
	jailPacks, packErr := LoadJailPacks(e)
	if packErr != nil {
		genStep(e, "load_packs", func() error { return packErr })
	}
	ConfigurePackSurfaces(e, jailPacks)
	RunPackHooks(e, jailPacks)

	// Stage host_files (YOLO_HOST_FILES), after the builtin agent surfaces, same
	// as the Linux boot loop. On macos-user the launcher passes only the
	// source-less entries (config.SourceLessHostFiles): there is no /ctx/host-user
	// mount to carry a source into — the design's accepted macos-user deficiency,
	// kept explicit rather than half-working (docs/plans/host-file-staging.md).
	genStep(e, "configure_host_files", func() error { return ConfigureHostFiles(e) })

	// macOS-only writers (the two pieces unique to the native-macOS bootstrap).
	genStep(e, "install_yolo_log", func() error { return InstallYoloLog(e, opts.YoloLogScript) })
	genStep(e, "write_login_rc", func() error { return WriteLoginRC(e, opts.LoginPath) })

	return genFailuresError(e)
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

// WriteLoginRC re-prepends loginPath to PATH in the login rc files (.zprofile,
// .zshrc, .bash_profile). macOS path_helper (/etc/zprofile, /etc/profile)
// reorders PATH to put /usr/local/bin first; these rc files run AFTER it, so the
// nix-store packages + agent shims win again. Bare binaries / plain `-c` shells
// don't read these and keep the baked env -i PATH. An empty loginPath is a
// no-op. This carries the (M1-unverified) OQ-1 path_helper fix.
func WriteLoginRC(e *Env, loginPath string) error {
	if loginPath == "" {
		return nil
	}
	rc := "# yolo-jail: re-prepend the sandbox PATH AFTER macOS path_helper\n" +
		"export PATH=\"" + loginPath + ":$PATH\"\n"
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile"} {
		if err := os.WriteFile(filepath.Join(e.Home, name), []byte(rc), 0o644); err != nil {
			return err
		}
	}
	return nil
}
