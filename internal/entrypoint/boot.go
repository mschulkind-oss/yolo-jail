package entrypoint

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A package var so tests can redirect it.
var workspaceDir = "/workspace"

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
// Workspace mise trust
// ---------------------------------------------------------------------------
// --quiet` in /workspace when it is a directory. Output discarded. Belt-and-
// suspenders atop MISE_TRUSTED_CONFIG_PATHS.
func trustWorkspaceConfigs() {
	if fi, err := os.Stat(workspaceDir); err != nil || !fi.IsDir() {
		return
	}
	cmd := exec.Command("mise", "trust", "--all", "--quiet")
	cmd.Dir = workspaceDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Run()
}

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
// The pure content generators (GenerateMiseConfig, ConfigureClaude) deliberately
// SKIP the two subprocess side effects — those are boot orchestration, not
// content. main() re-attaches them at the matching ordering points
// (generate_mise_config tail; configure_claude tail, inside the per-agent loop).
// uninstall --all <tool>` for each retired tool (idempotent, best-effort, 30s
// timeout). tool_name is the registry token with surrounding quotes stripped.
func miseUninstallRetired() {
	for _, tool := range agents.AllMiseRetire {
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

	claudeBin := filepath.Join(e.Home, ".local", "bin", "claude")
	if !pathExists(claudeBin) {
		claudeBin = "claude"
	}
	runClaude := func(args ...string) {
		cmd := exec.Command(claudeBin, args...)
		cmd.Env = envWith(os.Environ(), "YOLO_BYPASS_SHIMS", "1")
		cmd.Stdout = nil
		cmd.Stderr = nil
		_ = runWithTimeoutSeconds(cmd, 30)
	}

	lspServers := LoadLSPServers(e)
	for _, pm := range claudeLSPPluginOrder {
		_, wanted := lspServers.Get(pm.lsp)
		_, present := installed[pm.plugin]
		if wanted && !present {
			runClaude("plugins", "install", pm.plugin)
		} else if present && !wanted {
			runClaude("plugins", "uninstall", pm.plugin)
		}
	}
}

// ---------------------------------------------------------------------------
// Finalize PATH and exec bash
// ---------------------------------------------------------------------------
// execBash set the final PATH, echo the command for the
// exec-into-existing path, source yolo-user-env.sh + activate mise, and exec
// bash --rcfile ~/.bashrc -c <activated command>. Never returns on success.
func execBash(e *Env, command string) error {
	localBin := e.LocalBin()
	path := strings.Join([]string{
		e.ShimDir(), e.NpmBin(), e.MiseShims(), e.GoBin(), localBin, "/bin", "/usr/bin",
	}, ":")
	_ = os.Setenv("PATH", path)

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
	e.Stderr = os.Stderr

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

	// Generators (each best-effort — never abort boot; a generator error is
	// warned and boot continues).
	genStep(e, "generate_shims", func() error { return GenerateShims(e) })
	p.mark("generate_shims")
	genStep(e, "generate_agent_launchers", func() error { return GenerateAgentLaunchers(e) })
	p.mark("generate_agent_launchers")
	genStep(e, "generate_package_manager_launchers", func() error { return GeneratePackageManagerLaunchers(e) })
	p.mark("generate_package_manager_launchers")

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
	genStep(e, "generate_mise_config", func() error { return GenerateMiseConfig(e) })
	// Deferred side effect: mise uninstall of retired tools (generate_mise_config tail).
	miseUninstallRetired()
	p.mark("generate_mise_config")

	// Copy host nvim config into the writable .config/ overlay.
	copyHostNvimConfig(e)
	p.mark("nvim_config")

	genStep(e, "generate_mcp_wrappers", func() error { return GenerateMCPWrappers(e) })
	p.mark("generate_mcp_wrappers")
	configureGit(e)
	p.mark("configure_git")
	configureJJ(e)
	p.mark("configure_jj")
	// Skills are mounted :ro by the CLI — no entrypoint action needed.
	p.mark("skills_skipped")

	// Configure only the selected agents (YOLO_AGENTS), in order.
	for _, agent := range LoadAgents(e) {
		configureAgent(e, agent)
		p.mark("configure_" + agent)
	}

	setupCgroupDelegation(os.Stderr)
	p.mark("cgroup_delegation")
	genStep(e, "generate_cglimit_script", func() error { return GenerateCglimitScript(e) })
	p.mark("cglimit_script")
	genStep(e, "generate_journalctl_script", func() error { return GenerateJournalctlScript(e) })
	p.mark("journalctl_script")
	genStep(e, "cleanup_stale_wrappers", func() error { return GenerateYoloWrapper(e) })
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
		e.ShimDir(), e.NpmBin(), e.MiseShims(), e.GoBin(), "/bin", "/usr/bin",
	}, ":"))

	trustWorkspaceConfigs()
	p.mark("trust_workspace_configs")

	// NOTE: We intentionally do NOT call `mise hook-env` here (flock deadlock).
	p.dump(e.Home)

	return execBash(e, command)
}

// configureAgent runs the content writer for a selected agent, then re-attaches
// any deferred subprocess side effect for that agent. Unknown agents are no-ops.
func configureAgent(e *Env, agent string) {
	switch agent {
	case "claude":
		genStep(e, "configure_claude", func() error { return ConfigureClaude(e) })
		// Deferred side effect: claude plugins install/uninstall (configure_claude tail).
		installClaudePlugins(e)
	case "copilot":
		if prismEnabledFor(e, "copilot") {
			genStep(e, "configure_copilot", func() error { return ConfigureCopilotPrism(e) })
		} else {
			genStep(e, "configure_copilot", func() error { return ConfigureCopilot(e) })
		}
	case "gemini":
		if prismEnabledFor(e, "gemini") {
			genStep(e, "configure_gemini", func() error { return ConfigureGeminiPrism(e) })
		} else {
			genStep(e, "configure_gemini", func() error { return ConfigureGemini(e) })
		}
	case "opencode":
		genStep(e, "configure_opencode", func() error { return ConfigureOpencode(e) })
	case "pi":
		if prismEnabledFor(e, "pi") {
			genStep(e, "configure_pi", func() error { return ConfigurePiPrism(e) })
		} else {
			genStep(e, "configure_pi", func() error { return ConfigurePi(e) })
		}
	case "codex":
		genStep(e, "configure_codex", func() error { return ConfigureCodex(e) })
	case "agy":
		// agy is born on the prism — no bespoke fallback, so no prismEnabledFor gate.
		genStep(e, "configure_agy", func() error { return ConfigureAgyPrism(e) })
	}
}

// genStep runs a generator, warning (never aborting) on error: a failed step
// prints a warning and boot continues.
func genStep(e *Env, label string, fn func() error) {
	if err := fn(); err != nil {
		e.warn("Warning: " + label + ": " + err.Error())
	}
}

// setEnvBoth sets key=val in both the process env (so children inherit) and
// e.Vars (so later generators reading e.Getenv agree).
func setEnvBoth(e *Env, key, val string) {
	e.Vars[key] = val
	_ = os.Setenv(key, val)
}
