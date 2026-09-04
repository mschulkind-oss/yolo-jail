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

	// CONTENT — skills and pack briefings — copied over the home from the staged
	// overlay. This is the macos-user answer to the container's mounts: the host
	// composed the same trees the container path composes, laid them out at their
	// home-relative destinations, and staged the result root-owned; here it becomes
	// files. LAST among the writers on purpose — the per-agent surface writers above
	// create the agent home dirs this copies into (~/.claude and kin), so running it
	// earlier would either race them or have to re-create them itself.
	genStep(e, "install_home_overlay", func() error { return InstallHomeOverlay(e) })

	// macOS-only writers (the two pieces unique to the native-macOS bootstrap).
	genStep(e, "install_yolo_log", func() error { return InstallYoloLog(e, opts.YoloLogScript) })
	genStep(e, "write_login_rc", func() error { return WriteLoginRC(e, opts.LoginPath) })

	return genFailuresError(e)
}

// InstallHomeOverlay copies the staged CONTENT tree ($YOLO_DARWIN_HOME_OVERLAY) over
// the sandbox home. Unset or absent → no-op, which is the state of a launch whose packs
// declare no skills and no briefing.
//
// WHY A COPY RATHER THAN A MOUNT: this backend has none. The container path delivers
// each staged dir with a `-v …:ro` bind, which is also why its copy is READ-ONLY to the
// agent and this one is not — an agent here can edit its own skills, and the next launch
// overwrites them again. That is a real difference in kind and is recorded in the launch
// warning rather than papered over.
//
// The tree carries no schema: the host laid it out at the destinations the container
// would have mounted, so this walks it and writes files. Any mapping logic here would be
// a second implementation of the mount assembler's, which is the drift the transport
// unification exists to end (loophole-transport.md §8.4 makes the same argument about
// generated clients).
//
// OVERWRITE, NOT MERGE, per destination subtree: the overlay is authoritative for the
// paths it contains, exactly as a bind mount is. A skills dir that a pack stopped
// shipping must DISAPPEAR from the home, and a merge would keep serving it forever. The
// rest of the home — credentials, history, anything the agent wrote — is untouched,
// because the overlay simply does not contain those paths.
func InstallHomeOverlay(e *Env) error {
	src := e.Vars["YOLO_DARWIN_HOME_OVERLAY"]
	if src == "" {
		return nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			// Staged tree missing is not a boot failure: the launch may have raced a
			// teardown, and the agent is better off starting with no skills than not
			// starting. The warning is the record.
			e.warn("home overlay " + src + " is not present; skills and briefings were not delivered")
			return nil
		}
		return err
	}
	for _, ent := range entries {
		from := filepath.Join(src, ent.Name())
		to := filepath.Join(e.Home, ent.Name())
		if ent.IsDir() {
			// Replace the destination subtree wholesale — see OVERWRITE above.
			if err := os.RemoveAll(to); err != nil {
				return err
			}
			if err := copyTree(from, to); err != nil {
				return err
			}
			continue
		}
		body, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(to, body, 0o644); err != nil {
			return err
		}
	}
	return nil
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
