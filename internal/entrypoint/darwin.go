package entrypoint

import (
	"fmt"
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
	// The MCP preset WRAPPERS are not generated on this backend (see RunDarwinBootstrap),
	// so nothing installs the npm packages behind them either. Set here, with the other
	// two platform seams, because it is the same kind of fact: what this environment can
	// actually provide, decided once at the translation rather than at each consumer.
	e.SkipMCPPresets = true
	return e
}

// RunDarwinBootstrap generates the sandbox user's jail config natively: the same
// shims/launchers/bashrc/mise/MCP/identity/per-agent writers the container runs,
// plus the two macOS-only pieces (yolo-log helper, login-rc PATH re-prepend).
//
// A12: a generator failure is FATAL here too, and returning it is the whole point
// — this path is easy to miss (it has its OWN genStep sites and its own
// configureAgent loop, so an earlier count of the fail-open sites missed it
// entirely) and its caller used to print "bootstrap ok" unconditionally. The
// count is deliberately not restated: it moves whenever a generator is added,
// and the fact that matters is that this list is a SECOND one. Every
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
	// LAST of the three launch-dir steps (launchwrapper.go), and on THIS backend it is
	// also what delivers a pack's launch flags to the prompt at all: the account's login
	// shell is zsh, which reads none of the bash rc files the aliases are written into
	// (DP-B43). The launch dir is second on macosuser.SandboxPath and is re-prepended by
	// WriteLoginRC, so a launcher — installer or wrapper — is on the path a typed name
	// takes here, and the alias never was.
	genStep(e, "deliver_launch_flags", func() error { return DeliverLaunchFlags(e) })
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

	// Stage host_files (YOLO_HOST_FILES), after the builtin agent surfaces, same as the
	// Linux boot loop.
	//
	// ⚠ THE macos-user CARVE-OUT THAT STOOD HERE IS GONE (DP-L1, 2026-09-13). It read: the
	// launcher passes only the SOURCE-LESS entries, because there is no /ctx/host-user
	// mount to carry a source into. There is no mount now either — what changed is that
	// the launcher COPIES each source-bearing entry's bytes into a root-owned tree and
	// names it with YOLO_CTX_ROOT, which `hostUserPath` resolves through. So this step reads
	// the same wire and the same directory it does under a container; the relocation is
	// the only difference, and it is the one Apple Container already uses.
	genStep(e, "configure_host_files", func() error { return ConfigureHostFiles(e) })

	// CONTENT — skills and pack briefings — copied over the home from the staged
	// overlay. This is the macos-user answer to the container's mounts: the host
	// composed the same trees the container path composes, laid them out at their
	// home-relative destinations, and staged the result root-owned; here it becomes
	// files. LAST among the writers on purpose — the per-agent surface writers above
	// create the agent home dirs this copies into (~/.claude and kin), so running it
	// earlier would either race them or have to re-create them itself.
	genStep(e, "install_home_overlay", func() error { return InstallHomeOverlay(e, jailPacks) })

	// THE PROVISIONING STAGE'S SCRIPT (step 8 of macos-user-provisioning.md half two).
	// Written LAST among the generators that produce content, because it is the only one
	// nothing here consumes: the stage is a separate, Seatbelt-confined process the
	// launcher runs after this bootstrap returns (macosuser.ProvisionArgv). Its
	// interpolations read the pack set and the MCP/LSP config, which every step above has
	// already had its turn with, so writing it here cannot observe a half-built home.
	genStep(e, "generate_bootstrap_script", func() error { return GenerateDarwinBootstrapScript(e) })

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
		return fmt.Errorf("skills and briefings were not delivered: %w", &LinkedSidecarError{Links: linked})
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
	roots, err := overlayInstallRoots(e.Home, e.DarwinSidecar())
	if err != nil {
		return err
	}
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
