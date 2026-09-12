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
	// YoloLogScript is the yolo-log helper body (macosuser.MacosLogWrapperScript).
	// Passed in rather than generated here to keep this package free of the
	// macosuser dependency (macosuser imports entrypoint, not the reverse).
	YoloLogScript string
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
	return e
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
	// THE HOME LAYOUT, ABOVE EVERY GENERATOR — the workspace tier this backend otherwise
	// has no way to express (docs/design/macos-user-home-tiers.md, alternative A′).
	//
	// ABOVE genStep #1 AND NOT MERELY "BEFORE THE PACK HOOKS", because ~/.yolo/bin is
	// itself one of the links and GenerateShims writes THROUGH it: a shim generated into
	// the account home before the link was laid would be a blocker in the wrong tier, and
	// a link laid over the directory it had just created would refuse (the layout never
	// removes a real directory — OQ-HT2).
	//
	// The packs are loaded HERE rather than at their old position below, because the two
	// pack-declared tier lists ARE the layout: scope:workspace dirs become the links,
	// scope:machine dirs become the mirrors the shared_credentials hook's relative link
	// resolves through. A failure to load them is still reported at its old place, so the
	// boot log reads in the same order it always has.
	jailPacks, packErr := LoadJailPacks(e)
	genStep(e, "darwin_home_layout", func() error { return InstallDarwinHomeLayout(e, jailPacks) })
	genStep(e, "generate_shims", func() error { return GenerateShims(e) })
	genStep(e, "generate_agent_launchers", func() error { return GenerateAgentLaunchers(e) })
	genStep(e, "generate_package_manager_launchers", func() error { return GeneratePackageManagerLaunchers(e) })
	// Warn about any absent `requires` binary (generates nothing, so not a genStep). It
	// matters MORE here than in a container: macos-user bakes no image at all, so a
	// required tool comes from the user's own machine or not at all.
	AssertRequiredBins(e)
	genStep(e, "generate_bashrc", func() error { return GenerateBashrc(e) })
	genStep(e, "generate_mise_config", func() error { return ConfigureMisePrism(e) })
	// NO MCP WRAPPERS HERE (Open Decision #4, resolved 2026-09-03 in favour of the
	// option the plan recommended: skip and say so).
	//
	// Their bodies are Linux-absolute — /usr/bin/chromium (mcp_wrappers.go),
	// `exec /bin/node`, /etc/fonts/fonts.conf — with no GOOS guard, and this backend
	// bakes no image, so on macOS all three paths are simply absent (verified on
	// macOS 26.5). Generating them anyway put three executables in the sandbox home
	// that fail the moment anything execs one, and "harmless until something execs
	// one" stopped being true the day mcp_presets reached a real Mac config.
	//
	// SKIPPED, NOT PORTED. A darwin variant would have to find Chrome, node and a
	// fontconfig on a machine yolo did not provision, and guess wrong on most of
	// them. An absent wrapper that says so beats a present one that lies — the same
	// ruling `workspace_readonly` got on this backend (d0961f2c).
	if len(e.LoadMCPPresetNames()) > 0 {
		e.warn("mcp_presets are not delivered on macos-user: the preset wrappers hardcode " +
			"Linux paths (/usr/bin/chromium, /bin/node, /etc/fonts) that this backend does " +
			"not provision. Configure the MCP server directly in `mcp_servers` if you need " +
			"it here.")
	}
	configureGit(e)
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
	genStep(e, "write_login_rc", func() error { return WriteLoginRC(e) })

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
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			// Staged tree missing is not a boot failure: the launch may have raced a
			// teardown, and the agent is better off starting with no skills than not
			// starting. The warning is the record.
			e.warn("home overlay " + src + " is not present; skills and briefings were not delivered")
			return nil
		}
		return err
	}
	return installOverlayTree(src, e.Home)
}

// installOverlayTree copies one level of the overlay into dst and recurses.
//
// ⚠ IT DESCENDS THROUGH A SYMLINK AND REPLACES AT A REAL DIRECTORY, and that one rule is
// what makes the delivery land at the granularity a bind mount has.
//
// The container mounts each staged tree AT ITS DESTINATION — <staging>/skills-claude over
// /home/agent/.claude/skills — and leaves the parent alone. This used to RemoveAll the
// overlay's TOP-level entry, which for a skills dest of `.claude/skills` is `~/.claude`:
// the whole state dir, including the credential symlink the pack hooks had written minutes
// earlier and the transcripts under projects/. Under the home-tier layout that same
// RemoveAll unlinks the sidecar symlink and puts a real directory in its place, so the next
// boot's layout refuses (OQ-HT2 never deletes one) — a backend that bricks itself after one
// launch.
//
// A symlink in the home is a LAYOUT link: it marks a path the home merely passes through on
// its way to a destination, so descending through it is exactly right. The first real
// directory is the destination itself, and replacing it wholesale is what makes a skills
// dir a pack stopped shipping DISAPPEAR rather than linger forever.
//
// The one shape it reads differently from a bind: an overlay destination that IS a layout
// link (a pack declaring skills into `.claude` itself, which no pack does) would be merged
// into rather than replaced. Stale files in a state dir, never a destroyed one.
func installOverlayTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		from := filepath.Join(src, ent.Name())
		to := filepath.Join(dst, ent.Name())
		if ent.IsDir() {
			if isSymlinkPath(to) {
				if err := installOverlayTree(from, to); err != nil {
					return err
				}
				continue
			}
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

// isSymlinkPath reports whether path is a symlink (not whether what it points at exists).
func isSymlinkPath(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
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
