package entrypoint

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// socket. A package var so tests can redirect it.
var cgdSocket = "/run/yolo-services/cgroup-delegate.sock"

// jail's .config overlay. A package var so tests can redirect it.
var hostNvimConfig = "/ctx/host-nvim-config"

// ---------------------------------------------------------------------------
// Performance logging
// ---------------------------------------------------------------------------
type perfEntry struct {
	elapsed float64
	label   string
}

// perfLog accumulates boot checkpoints.
type perfLog struct {
	start   time.Time
	entries []perfEntry
}

func newPerfLog() *perfLog { return &perfLog{start: time.Now()} }

// mark records a checkpoint with elapsed time.
func (p *perfLog) mark(label string) {
	p.entries = append(p.entries, perfEntry{
		elapsed: time.Since(p.start).Seconds(),
		label:   label,
	})
}

// dump writes the perf log to ~/.yolo-perf.log. Best-
// effort — all errors swallowed. This log is deliberately excluded from the
// tree-parity golden (it is wall-clock timing); the format is for human
// readability, not byte-parity.
func (p *perfLog) dump(home string) {
	if len(p.entries) == 0 {
		return
	}
	logPath := filepath.Join(home, ".yolo-perf.log")
	var b strings.Builder
	fmt.Fprintf(&b, "=== YOLO Jail Entrypoint Perf (%s) ===\n", time.Now().Format("2006-01-02 15:04:05"))
	prev := -1.0
	for _, e := range p.entries {
		delta := "       "
		if prev >= 0 {
			delta = fmt.Sprintf("+%.3fs", e.elapsed-prev)
		}
		fmt.Fprintf(&b, "  %7.3fs  %9s  %s\n", e.elapsed, delta, e.label)
		prev = e.elapsed
	}
	fmt.Fprintf(&b, "  Total: %.3fs\n\n", p.entries[len(p.entries)-1].elapsed)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(b.String())
	_ = f.Close()

	// Trim to last 50 runs.
	content, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	runs := strings.Split(string(content), "=== YOLO")
	if len(runs) > 51 {
		trimmed := "=== YOLO" + strings.Join(runs[len(runs)-50:], "=== YOLO")
		_ = os.WriteFile(logPath, []byte(trimmed), 0o644)
	}
}

// ---------------------------------------------------------------------------
// User-env hydration
// ---------------------------------------------------------------------------
// flattened here; the writer's 4-char escape for an embedded single quote (a
// single quote, a backslash, and two single quotes) is matched literally.
// RE2-safe (no backrefs).
var exportLineRe = regexp.MustCompile(
	`^\s*export\s+(?P<key>[A-Za-z_][A-Za-z0-9_]*)=(?:\$\{[A-Za-z_][A-Za-z0-9_]*:-'(?P<def>(?:[^']|'\\'')*)'\}|'(?P<sq>(?:[^']|'\\'')*)'|"(?P<dq>[^"]*)"|(?P<bare>\S*))\s*$`,
)

var (
	exportGroupDef  = exportLineRe.SubexpIndex("def")
	exportGroupSq   = exportLineRe.SubexpIndex("sq")
	exportGroupDq   = exportLineRe.SubexpIndex("dq")
	exportGroupBare = exportLineRe.SubexpIndex("bare")
	exportGroupKey  = exportLineRe.SubexpIndex("key")
)

// ~/.config/yolo-user-env.sh exports into the process env AND e.Vars so the
// early agent-config writers see the same values bash will. Launch-time env
// beats the file default (the ${KEY:-'value'} precedence). Unparseable lines
// are ignored. Sets os.Setenv so spawned children inherit the values.
func hydrateEnvFromUserEnvFile(e *Env) {
	f := filepath.Join(e.Home, ".config", "yolo-user-env.sh")
	data, err := os.ReadFile(f)
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimLeft(line, " \t\r\n\v\f"), "#") {
			continue
		}
		loc := exportLineRe.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		key := groupStr(line, loc, exportGroupKey)
		if _, ok := e.Vars[key]; ok {
			continue // launch-time env beats the file default
		}
		var raw string
		switch {
		case groupParticipated(loc, exportGroupDef):
			raw = groupStr(line, loc, exportGroupDef)
		case groupParticipated(loc, exportGroupSq):
			raw = groupStr(line, loc, exportGroupSq)
		case groupParticipated(loc, exportGroupDq):
			raw = groupStr(line, loc, exportGroupDq)
		default:
			// m.group("bare") or "" — bare always participates (\S* can match
			// empty); an empty bare yields "".
			raw = groupStr(line, loc, exportGroupBare)
		}
		// Reverse the writer's '\'' escape for single-quoted contexts.
		val := strings.ReplaceAll(raw, "'\\''", "'")
		e.Vars[key] = val
		_ = os.Setenv(key, val)
	}
}

// groupParticipated reports whether the named subgroup at index gi matched.
// A non-participating group has index pair (-1, -1) in FindStringSubmatchIndex;
// an empty match has a valid, possibly zero-width, index pair.
func groupParticipated(loc []int, gi int) bool {
	if gi < 0 || 2*gi+1 >= len(loc) {
		return false
	}
	return loc[2*gi] >= 0
}

// groupStr extracts the substring for group gi from loc, or "" if it did not
// participate.
func groupStr(s string, loc []int, gi int) string {
	if !groupParticipated(loc, gi) {
		return ""
	}
	return s[loc[2*gi]:loc[2*gi+1]]
}

// splitLines splits on \n and drops a trailing empty element from a final
// newline.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// ---------------------------------------------------------------------------
// Cgroup delegation availability check
// ---------------------------------------------------------------------------
// cgroup-delegate socket exists and print an availability line to stderr.
// Silent on absence beyond the notice (falls back to nice/timeout/ulimit).
func setupCgroupDelegation(w io.Writer) {
	if _, err := os.Stat(cgdSocket); err == nil {
		fmt.Fprintln(w, "  cgroup delegate: available (host-side daemon)")
	} else {
		fmt.Fprintln(w, "  cgroup delegate: not available (no host daemon socket)")
	}
}

// ---------------------------------------------------------------------------
// Workspace mise trust — REMOVED, deliberately
// ---------------------------------------------------------------------------
// There used to be a trustWorkspaceConfigs() here, running `mise trust` in /workspace on
// every boot. It is gone, and so are its two siblings (the .bashrc hook and the provisioning
// setupScript). Do not add another.
//
// MISE_TRUSTED_CONFIG_PATHS=/workspace is sufficient ON ITS OWN. Verified 2026-08-05: a config
// at an untrusted path reports `untrusted`, the same path with the env var set reports
// `trusted` with NO on-disk mark written and no `mise trust` ever run, and a config OUTSIDE the
// named path stays untrusted — so it is properly scoped rather than blanket-trust-everything.
//
// The calls were worse than redundant. `mise trust` records its mark under
// ~/.local/state/mise/trusted-configs/, and ~/.local is bound per-WORKSPACE
// (cli/run/assemble_parts.go), so every mark was workspace-local state that had to be
// re-earned in each jail and vanished with a pruned state dir. The env var rides the
// environment instead, which is where a fact about "this whole tree is ours" belongs.
//
// Keeping them also made the mark look load-bearing, which is how a false comment survived for
// months: the old code claimed `--all` "covers cwd+parents only" when `mise trust --help` says
// it walks subdirectories too — the walk that cost minutes per boot on a multi-repo workspace
// (PR #30).

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// nvim config copy)
// ---------------------------------------------------------------------------
// /ctx/host-nvim-config into HOME/.config/nvim (symlinks followed, dangling
// skipped, existing dirs merged). Best-effort — the nested-jail same-inode case
// (src and dst backed by the same overlay) is swallowed.
func copyHostNvimConfig(e *Env) {
	if fi, err := os.Stat(hostNvimConfig); err != nil || !fi.IsDir() {
		return
	}
	jailNvim := filepath.Join(e.Home, ".config", "nvim")
	// jail_nvim.parent.mkdir(parents=True, exist_ok=True)
	if err := os.MkdirAll(filepath.Dir(jailNvim), 0o755); err != nil {
		return
	}
	// copytree(dirs_exist_ok=True): merge into (or create) jailNvim.
	_ = copyTree(hostNvimConfig, jailNvim)
}

// copyTree copies src into dst, following symlinks (symlinks=False), skipping
// dangling symlinks (ignore_dangling_symlinks=True), merging into existing dirs
// (merging into existing dirs). Same-inode source/destination files
// (nested-jail shared overlay) are skipped. All per-entry errors are
// best-effort.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		srcPath := filepath.Join(src, ent.Name())
		dstPath := filepath.Join(dst, ent.Name())
		// os.Stat follows symlinks (symlinks=False copies targets).
		fi, err := os.Stat(srcPath)
		if err != nil {
			// Dangling symlink (target missing) -> skip (ignore_dangling_symlinks).
			continue
		}
		if fi.IsDir() {
			_ = copyTree(srcPath, dstPath)
			continue
		}
		// Same-inode guard: nested jail where src and dst are the same file.
		if same, _ := sameFile(srcPath, dstPath); same {
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		_ = os.WriteFile(dstPath, data, fi.Mode().Perm())
	}
	return nil
}

// sameFile reports whether a and b refer to the same underlying file. Missing
// b (the common case: fresh copy) is not "same".
func sameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, nil
	}
	return os.SameFile(fa, fb), nil
}

// ---------------------------------------------------------------------------
// Deferred subprocess side effects
// ---------------------------------------------------------------------------
// The pure content generators (ConfigureMisePrism, ConfigureClaudePrism)
// deliberately SKIP the two subprocess side effects — those are boot
// orchestration, not content. main() re-attaches them at the matching ordering
// points (generate_mise_config tail; configure_claude tail, in the per-agent loop).
// uninstall --all <tool>` for each retired tool (idempotent, best-effort, 30s
// timeout). tool_name is the registry token with surrounding quotes stripped.
func miseUninstallRetired() {
	for _, tool := range packload.EmbeddedRetireMiseTools() {
		toolName := strings.Trim(tool, `"`)
		cmd := exec.Command("mise", "uninstall", "--all", toolName)
		cmd.Stdout = nil
		cmd.Stderr = nil
		_ = runWithTimeoutSeconds(cmd, 30)
	}
}

// (Reuses claudeLSPPluginOrder from claude.go, which carries the same pairs.)
// uninstall Claude Code LSP plugins to match the configured LSP servers. Reads
// ~/.claude/plugins/installed_plugins.json for the current set. All claude
// invocations are best-effort (30s timeout, YOLO_BYPASS_SHIMS=1 in the env).
func installClaudePlugins(e *Env) {
	pluginsMeta := filepath.Join(e.ClaudeDir(), "plugins", "installed_plugins.json")
	installed := map[string]struct{}{}
	if raw, err := os.ReadFile(pluginsMeta); err == nil {
		if decoded, derr := jsonx.Decode(raw); derr == nil {
			if m, ok := decoded.(*jsonx.OrderedMap); ok {
				if pv, ok := m.Get("plugins"); ok {
					if pm, ok := pv.(*jsonx.OrderedMap); ok {
						for _, k := range pm.Keys() {
							installed[k] = struct{}{}
						}
					}
				}
			}
		}
	}

	lspServers := LoadLSPServers(e)
	for _, pm := range claudeLSPPluginOrder {
		_, wanted := lspServers.Get(pm.lsp)
		_, present := installed[pm.plugin]
		if wanted && !present {
			runClaudeCLI(e, "plugins", "install", pm.plugin)
		} else if present && !wanted {
			runClaudeCLI(e, "plugins", "uninstall", pm.plugin)
		}
	}
}

// ---------------------------------------------------------------------------
// Finalize PATH and exec bash
// ---------------------------------------------------------------------------
// BootPath is THE PATH the entrypoint hands the agent, and the authority the .bashrc
// export (shell.go) and AGENTS.md's "PATH order (exact)" line mirror.
//
// The two generated dirs sit at OPPOSITE ENDS on purpose:
//
//   - BlockDir FIRST — blockers (grep, find). Interception is their whole job, so they
//     must precede the real binary.
//   - LaunchDir LAST, after /bin and /usr/bin — lazy installers (claude, pnpm). They
//     only need to run when nothing else provides the name, so ordering them here makes
//     shadowing a baked binary UNREPRESENTABLE rather than something the launcher has to
//     detect. See Env.LaunchDir for the defect this closed.
//
// Extracted from execBash so the order is assertable without exec'ing a shell.
func BootPath(e *Env) string {
	return strings.Join([]string{
		e.BlockDir(), e.NpmBin(), e.MiseShims(), e.GoBin(), e.LocalBin(), "/bin", "/usr/bin",
		e.LaunchDir(),
	}, ":")
}

// execBash set the final PATH, echo the command for the
// exec-into-existing path, source yolo-user-env.sh + activate mise, and exec
// bash --rcfile ~/.bashrc -c <activated command>. Never returns on success.
func execBash(e *Env, command string) error {
	_ = os.Setenv("PATH", BootPath(e))

	isNewContainerCmd := strings.Contains(command, "yolo-bootstrap")
	if command != "bash" && !isNewContainerCmd {
		// \033[1;36m⚡ Executing: <command>\033[0m\n
		fmt.Fprintf(os.Stderr, "\033[1;36m⚡ Executing: %s\033[0m\n", command)
	}

	userEnvFile := filepath.Join(e.Home, ".config", "yolo-user-env.sh")
	sourceUserEnv := ""
	if pathExists(userEnvFile) {
		sourceUserEnv = `. "` + userEnvFile + `" 2>/dev/null; `
	}
	activatedCommand := sourceUserEnv + `eval "$(mise env -s bash)" 2>/dev/null; ` + command

	// exec bash --rcfile BASHRC -c activated. syscall.Exec does no PATH search,
	// so resolve bash on PATH first, then exec with argv[0]="bash".
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		return err
	}
	argv := []string{"bash", "--rcfile", e.BashrcPath(), "-c", activatedCommand}
	return sysExec(bashPath, argv, os.Environ())
}

// ---------------------------------------------------------------------------
// Main orchestration)
// ---------------------------------------------------------------------------
// Main is the side-effecting boot sequence (entrypoint.main()).
// It reproduces the exact ordering and perf-log labels,
// wiring the pure generators together and re-attaching the two deferred
// subprocess side effects (mise uninstall, claude plugins). On success it never
// returns (execs bash). It returns an error only if the final exec itself fails.
func Main(args []string) error {
	command := "bash"
	if len(args) > 0 {
		command = strings.Join(args, " ")
	}

	e := EnvFromOS()
	// Everything the boot says now also lands in <workspace>/.yolo/boot.log, which
	// outlives the container and is therefore readable after a boot that REFUSED —
	// the state OQ-R2's flip makes reachable, where there is no jail left to ask.
	// Never fatal: any failure here yields plain stderr. See bootlog.go.
	blog := attachBootLog(e, os.Stderr)

	p := newPerfLog()
	p.mark("start")

	// Hydrate env_sources values before any configure_* so MCP env ${VAR}
	// interpolation sees them. bash sources the same file again at shell time.
	hydrateEnvFromUserEnvFile(e)
	p.mark("hydrate_user_env")

	// Populate /run/localtime + /run/timezone from $TZ before anything else.
	configureTimezone(e)
	p.mark("configure_timezone")

	// Populate /run/ld.so.cache from the /lib farm.
	generateLdCache()
	p.mark("generate_ld_cache")

	// Generators. A12: a failure is FATAL — each step still runs so one boot
	// reports every problem, then genFailuresError aborts before exec'ing the
	// agent. See genStep.
	genStep(e, "generate_shims", func() error { return GenerateShims(e) })
	p.mark("generate_shims")
	genStep(e, "generate_agent_launchers", func() error { return GenerateAgentLaunchers(e) })
	p.mark("generate_agent_launchers")
	genStep(e, "generate_package_manager_launchers", func() error { return GeneratePackageManagerLaunchers(e) })
	p.mark("generate_package_manager_launchers")
	// `requires` asserts presence and generates nothing, so it is not a genStep: an absent
	// required binary is a WARNING naming the bin, not a boot failure (see
	// AssertRequiredBins). Run after the launchers so a `program` the same set of packs
	// installs is already represented on BootPath.
	AssertRequiredBins(e)
	p.mark("assert_required_bins")

	// The orphan catalog is informational too, and for the same reason `requires` is:
	// nothing is half-written — a package is installed that this launch's declarations do
	// not account for. OQ-PD4 ruled that dropping a pack does not delete its program, so
	// this NAMES orphans and removes nothing (program-delivery.md §10 step four). It runs
	// here, before the bootstrap, on purpose: what is on disk now is what the LAST launch
	// installed, which is the only state in which "undeclared" means anything.
	CatalogInstalledOrphans(e)
	p.mark("catalog_installed_orphans")

	// Build the combined CA bundle BEFORE bashrc and before any child spawn, so
	// the env vars we export propagate to every child the entrypoint spawns.
	if bundle, err := GenerateCABundle(e); err != nil {
		e.warn("Warning: generate_ca_bundle: " + err.Error())
	} else {
		setEnvBoth(e, "SSL_CERT_FILE", bundle)
		setEnvBoth(e, "REQUESTS_CA_BUNDLE", bundle)
		setEnvBoth(e, "CURL_CA_BUNDLE", bundle)
		setEnvBoth(e, "GIT_SSL_CAINFO", bundle)
	}
	p.mark("generate_ca_bundle")

	genStep(e, "generate_bashrc", func() error { return GenerateBashrc(e) })
	p.mark("generate_bashrc")
	genStep(e, "generate_bootstrap_script", func() error { return GenerateBootstrapScript(e) })
	p.mark("generate_bootstrap_script")
	genStep(e, "generate_venv_precreate_script", func() error { return GenerateVenvPrecreateScript(e) })
	p.mark("generate_venv_precreate_script")
	genStep(e, "generate_mise_config", func() error { return ConfigureMisePrism(e) })
	// Deferred side effect: mise uninstall of retired tools (generate_mise_config tail).
	miseUninstallRetired()
	p.mark("generate_mise_config")

	// Copy host nvim config into the writable .config/ overlay.
	copyHostNvimConfig(e)
	p.mark("nvim_config")

	genStep(e, "generate_mcp_wrappers", func() error { return GenerateMCPWrappers(e) })
	p.mark("generate_mcp_wrappers")
	// Git identity is host-composed and :ro-mounted by the CLI (see
	// gitIdentityMountArgs) — no entrypoint action on the container path.
	// Skills are mounted :ro by the CLI — no entrypoint action needed.
	p.mark("skills_skipped")

	// Render every PACK-DECLARED surface. One loop over declarations — no switch on
	// tool names, because core does not know any (see packsurfaces.go).
	jailPacks, packErr := LoadJailPacks(e)
	if packErr != nil {
		// A pack that parsed on the host and not here means the mounted tree disagrees
		// with what was staged. Fatal (A12): rendering a subset would yield a jail whose
		// config is quietly incomplete.
		genStep(e, "load_packs", func() error { return packErr })
	}
	ConfigurePackSurfaces(e, jailPacks)
	RunPackHooks(e, jailPacks)
	p.mark("configure_pack_surfaces")

	// Stage the user's host_files entries (YOLO_HOST_FILES) through the same
	// composition engine, after the builtin agent surfaces so a user entry never
	// races a builtin (the config layer already forbids one at a builtin path).
	genStep(e, "configure_host_files", func() error { return ConfigureHostFiles(e) })
	p.mark("configure_host_files")

	// e.Stderr, not os.Stderr: this is a boot diagnostic and belongs in the log.
	setupCgroupDelegation(e.Stderr)
	p.mark("cgroup_delegation")
	// No generate step for yolo-cglimit / yolo-journalctl any more: the image
	// bakes both (flake.nix shippedBinaries). All that is left is unlinking the
	// scripts an older entrypoint wrote into ~/.local/bin, which PRECEDES /bin on
	// PATH and would otherwise shadow the binaries forever.
	genStep(e, "cleanup_stale_wrappers", func() error { return RemoveStaleGeneratedClients(e) })
	p.mark("cleanup_stale_wrappers")

	// Per-container runtime plumbing.
	setupPublishedPortLocalnet(e)
	p.mark("published_port_localnet")
	startContainerPortForwarding(e)
	p.mark("port_forwarding")

	// Start the jail-daemon supervisor (child of PID 1; kernel-reaped on exit).
	startJailDaemonSupervisor(e)
	p.mark("jail_daemon_supervisor")

	// Set PATH including mise shims so tools like copilot/gemini/claude are found
	// (matches the pre-exec PATH set in main(), used by the mise trust subprocess).
	_ = os.Setenv("PATH", strings.Join([]string{
		e.BlockDir(), e.NpmBin(), e.MiseShims(), e.GoBin(), "/bin", "/usr/bin",
		e.LaunchDir(),
	}, ":"))

	// The in-jail reachability witness runs LAST, and both halves of that are
	// deliberate. Its finding is then the closest thing to the agent's first prompt
	// instead of being buried under pack rendering; and it sits immediately above
	// genFailuresError, the boot's existing "refuse before handing over control"
	// gate, which is what OQ-R2's fatal plugs into — a service this jail cannot use
	// now REFUSES the launch here, having first let every generator above run so one
	// boot reports every problem. See reachability.go — it is the only check here
	// that can only be answered from INSIDE the jail, because `yolo check` runs
	// host-side and substitutes 127.0.0.1 for the advertised host.
	ProbeServiceReachability(e)
	p.mark("probe_service_reachability")

	// NOTE: We intentionally do NOT call `mise hook-env` here (flock deadlock).
	p.dump(e.Home)

	// A12: abort BEFORE handing control to the agent. Everything above has run, so
	// this reports every broken generator at once rather than one per restart.
	if err := genFailuresError(e); err != nil {
		blog.finish(err)
		return err
	}

	// Closed BEFORE the exec, not deferred: execBash replaces this process, so a
	// deferred close would never run and the log would end mid-sentence on every
	// SUCCESSFUL boot — the opposite of the signal it exists to carry.
	blog.finish(nil)
	return execBash(e, command)
}

// genFailuresError turns the collected generator failures into the single error
// that aborts the boot (A12), or nil when every step succeeded. The message names
// each failing step, because "config generation failed" alone would send the user
// back into the logs to find out which one.
func genFailuresError(e *Env) error {
	fails := e.GenFailures()
	if len(fails) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to start the jail: %d config generator(s) failed:\n  - %s",
		len(fails), strings.Join(fails, "\n  - "))
}

// genStep runs a config generator. A failure is FATAL (A12 ruling: "a pack
// failure means a jail should not start — failures should be loud and halting").
//
// It used to warn and DISCARD the error, so a failed generator still yielded a
// running jail whose agent silently read a missing or half-written config. That is
// the worst outcome available for a config surface: the jail looks healthy and the
// misconfiguration only shows up as inexplicable agent behavior later.
//
// Failures are COLLECTED rather than returned at the first error, for two reasons:
// every remaining step still runs, so one boot reports every problem instead of
// making the user restart once per bug; and the steps are largely independent, so
// stopping early would hide unrelated breakage behind the first failure. Main
// converts a non-empty set into the error that aborts the boot.
//
// This is not a licence to route optional inputs through here: a generator must
// return nil when its input is legitimately ABSENT (InstallYoloLog with no script,
// WriteLoginRC with no login path, RemoveStaleGeneratedClients finding no stale files all
// do exactly that). Only a real failure — an unwritable path, a malformed value,
// an unreadable declared file — reaches this.
func genStep(e *Env, label string, fn func() error) {
	if err := fn(); err != nil {
		e.warn("Error: " + label + ": " + err.Error())
		e.genFailure(label + ": " + err.Error())
	}
}

// setEnvBoth sets key=val in both the process env (so children inherit) and
// e.Vars (so later generators reading e.Getenv agree).
func setEnvBoth(e *Env, key, val string) {
	e.Vars[key] = val
	_ = os.Setenv(key, val)
}
