package entrypoint

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// socket. A package var so tests can redirect it.
var cgdSocket = "/run/yolo-services/cgroup-delegate.sock"

// jail's .config overlay. A package var so tests can redirect it.
var hostNvimConfig = paths.ContextHostNvimDir

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

// appendProvisionPerf adds the provisioning stage's duration and exit status to ~/.yolo-perf.log,
// as one line under the block this session's boot pass just dumped. It writes NO block header:
// the in-container profile prints the log's last `=== YOLO` block (buildSessionCmd), so a header
// here would hide the boot checkpoints that block holds. The stage runs after that dump, so the
// line lands in the same block. A sink, like dump: every error is dropped, and an empty home (no
// jail home known) writes nothing rather than a file in the working directory.
func appendProvisionPerf(home string, ms int64, rc int) {
	if home == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, ".yolo-perf.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "  provisioning stage: %dms (exit %d)\n", ms, rc)
	_ = f.Close()
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
	// Every error below stays dropped, and the reason is that there is nowhere for it
	// to go that is not this same file: the perf log is a SINK, and a sink cannot
	// report its own failure through itself. Routing it to e.Stderr instead would put
	// a timing-file error on the terminal of every launch that happens to have a
	// full disk, which is noise about the instrument rather than the jail. The
	// observable consequence of each failure is a missing or short ~/.yolo-perf.log,
	// which is self-evident to the only reader who looks.
	_, _ = f.WriteString(b.String())
	_ = f.Close()

	// Trim to last 50 runs. A failed trim leaves the log longer than 50 runs — the
	// next boot retries, and an over-long timing log has no consequence beyond size.
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

// EntryChannelSectionHeader marks the per-entry values in yolo-user-env.sh.
// The launcher rewrites this section for every launch and attach; readers that
// outlive one entry use it to replace their frozen selection safely.
const EntryChannelSectionHeader = "# --- per-entry channel (rewritten by every yolo launch) ---"

// HydrateEntryChannel applies the complete per-entry channel to e without
// changing the process environment. It returns false when the channel is
// absent or incomplete, including while its writer is truncating and rewriting
// the file in place. Long-lived services keep their last complete channel in
// that case rather than briefly acting on a torn selection.
func HydrateEntryChannel(e *Env) bool {
	data, err := os.ReadFile(filepath.Join(e.Home, ".config", "yolo-user-env.sh"))
	if err != nil {
		return false
	}
	values, ok := ParseEntryChannel(data)
	if !ok {
		return false
	}
	for key, value := range values {
		e.Vars[key] = value
	}
	return true
}

// ParseEntryChannel reads the per-entry channel section out of a yolo-user-env.sh body: every
// plain-form export below EntryChannelSectionHeader, unescaped. ok is false when the section is
// absent or incomplete (any of the three wire tables missing), which is also what a file caught
// mid-rewrite looks like. The host launcher reads a running jail's caller tokens back through it
// (internal/cli/run's runningCallerTokens), so the two sides share one grammar.
func ParseEntryChannel(data []byte) (map[string]string, bool) {
	values := map[string]string{}
	inChannel := false
	for _, line := range splitLines(string(data)) {
		if line == EntryChannelSectionHeader {
			inChannel = true
			continue
		}
		if !inChannel {
			continue
		}
		key, val, def, ok := parseExportLine(line)
		if !ok || def {
			continue
		}
		values[key] = val
	}
	for _, key := range WireTables() {
		if _, ok := values[key]; !ok {
			return nil, false
		}
	}
	return values, true
}

// ~/.config/yolo-user-env.sh exports into the process env AND e.Vars so the
// early agent-config writers see the same values bash will. The TWO line
// grammars are the precedence, read off the line itself: a def-form
// `export K=${K:-'v'}` line is an env_sources DEFAULT the launch-time env beats,
// and a plain-form `export K='v'` line is the per-entry CHANNEL (writeUserEnvFile's
// section — provider tables and the ungated pack env) and beats everything,
// the container's frozen environment included. That override is what makes an
// attach deliver: the launcher rewrites the file immediately before the exec, and
// this hydration is the first thing the exec'd boot does, so stale channel keys
// from an older entry cannot survive into this one. Unparseable lines are
// ignored. Sets os.Setenv so spawned children inherit the values.
//
// A DEF-FORM LINE NEVER SETS A LAUNCHER CONTRACT KEY (launcherContractKey). A default sets a
// key the launch did not, and env_sources values come from dotenv files the workspace lists,
// which the agent and the repository can write with no config prompt — so
// `YOLO_PROGRAMS_AUTOPRUNE=1` there turned on the destructive act only the user's own config may
// turn on. Such a line is skipped here, in e.Vars and in the process environment alike; the
// session's shell still sources the whole file for itself (activationPrefix), so only the
// boot's contract is protected. The plain-form channel is the launcher's own and is read whole.
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
		key, val, def, ok := parseExportLine(line)
		if !ok {
			continue
		}
		if def && launcherContractKey(key) {
			continue // an env_sources default never sets the launcher's contract
		}
		if _, present := e.Vars[key]; present && def {
			continue // a def-form default loses to launch-time env; a plain-form channel value never does
		}
		e.Vars[key] = val
		// os.Setenv fails on exactly two inputs — an empty key and a key containing
		// "=" or a NUL — and exportLineRe's `key` group is [A-Za-z_][A-Za-z0-9_]*,
		// which admits neither. There is no reachable error here to report.
		_ = os.Setenv(key, val)
	}
}

// JailGrantFileRel is where a container jail's --with-credentials grant reaches the jail, relative
// to the jail home (docs/design/credential-sources-separation.md §5.2, ES-D37): a per-launch file
// of plain `export K='v'` lines the launcher writes once, at the fresh launch, and never rewrites.
// On podman it is a `:ro` bind of a 0600 file in the launcher's own state, outside the workspace;
// on Apple Container a copy in the jail home, as that backend's env_sources channel is. Absent on
// a jail launched with no grant.
const JailGrantFileRel = ".config/yolo-grant-env.sh"

// hydrateEnvFromGrantFile exports the jail's grant (JailGrantFileRel) into the process env and
// e.Vars, so every process the boot and each session's entrypoint start inherits it: the jail
// holds the set for its life, the way the ruling reads ("a later session attached to that jail
// has the jail's set"). The values never pass through the runtime's container configuration, its
// inspect output or its database (ES-D37). No YOLO_ name is taken from it (launcherContractKey):
// the file holds providers' claimed credential names, and the launch's contract is the argv's.
// An absent file is a jail launched with no grant, and reads nothing.
func hydrateEnvFromGrantFile(e *Env) {
	data, err := os.ReadFile(filepath.Join(e.Home, JailGrantFileRel))
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		key, val, _, ok := parseExportLine(line)
		if !ok || launcherContractKey(key) {
			continue
		}
		e.Vars[key] = val
		_ = os.Setenv(key, val)
	}
}

// launcherContractKey reports whether key is in the namespace of the launcher's contract with
// the boot: every YOLO_ name. The boot's own environment carries each one the launch set (the
// podman argv, the bootstrap argv), so one it lacks is one the launch DID NOT set, and a file
// composed from env_sources must not set it in the launch's place: YOLO_PROGRAMS_AUTOPRUNE turns
// on a removal act, YOLO_PACK_ROOT names the pack tree the boot loads, YOLO_DARWIN_HOME_OVERLAY
// a tree the macos-user bootstrap copies over the account home, all outside the sandbox there.
// The whole prefix rather than a list of today's names, so a contract key added later is
// covered without anyone remembering this function.
func launcherContractKey(key string) bool { return strings.HasPrefix(key, "YOLO_") }

// parseExportLine reads one `export K=…` line in any of exportLineRe's four forms, returning
// the key, the value with the writer's single-quote escape reversed, whether the line is the
// def form (`export K=${K:-'v'}`, a default the launch-time environment beats), and whether it
// parsed at all. Shared by every reader of this grammar — the user env file
// (hydrateEnvFromUserEnvFile), its per-entry channel (ParseEntryChannel), the macos-user
// session env file (hydrateEnvFromSessionEnvFile) and the per-agent env files
// (parseAgentEnvLine, which adds the writer's `case` lines) — so they cannot come to disagree
// about a value's quoting.
// ParseExportLine is parseExportLine for the launcher, which reads a jail's grant file back on an
// attach (internal/cli/run's grantFileValues) in the grammar the jail reads it in.
func ParseExportLine(line string) (key, val string, def, ok bool) { return parseExportLine(line) }

func parseExportLine(line string) (key, val string, def, ok bool) {
	loc := exportLineRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return "", "", false, false
	}
	key = groupStr(line, loc, exportGroupKey)
	var raw string
	switch {
	case groupParticipated(loc, exportGroupDef):
		raw, def = groupStr(line, loc, exportGroupDef), true
	case groupParticipated(loc, exportGroupSq):
		raw = groupStr(line, loc, exportGroupSq)
	case groupParticipated(loc, exportGroupDq):
		raw = groupStr(line, loc, exportGroupDq)
	default:
		// bare always participates (\S* can match empty); an empty bare yields "".
		raw = groupStr(line, loc, exportGroupBare)
	}
	// Reverse the writer's '\'' escape for single-quoted contexts.
	return key, unescapeSingleQuoted(raw), def, true
}

// DarwinSessionEnvFileEnv names the macos-user SESSION ENV FILE on the bootstrap's argv: the
// root-owned 0600 file, with one read grant for the sandbox account, that carries everything the
// launch composed for the agent — the hydrated env_sources, the profile and provider channel,
// git identity, the terminal (macosuser's envfile.go; macosuser.SandboxEnvFileEnv is this
// constant). Its value is a PATH, never a value from the file, so the argv stays free of every
// credential the file holds.
const DarwinSessionEnvFileEnv = "YOLO_DARWIN_ENV_FILE"

// hydrateEnvFromSessionEnvFile is the macos-user twin of hydrateEnvFromUserEnvFile: it reads
// the session env file the launch installed before this bootstrap ran into e.Vars, so the
// generators — the MCP requires_env gate above all — see the environment the agent will
// have. Without it, a server gated on a shared env_sources variable was dropped from every
// agent config on this backend, although the agent's own environment carried the variable.
//
// THREE DIFFERENCES FROM ITS TWIN, all deliberate:
//
//   - A key the bootstrap's own environment already sets WINS. That environment is the
//     generator contract the launch composed for this process (macosuser.buildBootstrapEnv),
//     and the file is the agent's; where both name a key, they name the same value, or the
//     contract is the one the generators are written against.
//   - NO YOLO_ KEY IS TAKEN FROM THE FILE AT ALL (launcherContractKey). The file is plain-form
//     throughout, so a launcher line cannot be told from an env_sources one, and env_sources
//     includes the workspace's dotenv files, which the agent writes. A contract key the argv
//     lacks is one the launch did not set, and the bootstrap runs OUTSIDE Seatbelt: an
//     `export YOLO_PROGRAMS_AUTOPRUNE='1'` there made it delete files, and YOLO_PACK_ROOT or
//     YOLO_DARWIN_HOME_OVERLAY would have it load or copy trees of the agent's choosing.
//   - NOTHING IS os.Setenv'd. The file carries YOLO_VERSION, the jail marker config.InJail
//     reads off the process, and the credentials the gate asks about; in e.Vars they answer the
//     gate, while in the process environment they would make this unconfined process call
//     itself a jail and hand every credential to each child it spawns.
//
// The file is the LAUNCHED agent's environment, so it also carries the values the credential
// gate scoped to that agent alone. They are hydrated like the rest and their names recorded in
// e.sessionEnvKeys, so loadMCPTables can tell them from the shared composition by the
// per-agent env files (scopedMCPView).
//
// An unset variable is a launch that composed nothing (or a test), and reads nothing. A file
// named and unreadable is warned about, because the gate then answers wrongly for this launch.
func hydrateEnvFromSessionEnvFile(e *Env) {
	path := e.Getenv(DarwinSessionEnvFileEnv)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		e.warn("warning: could not read the session env file " + path + " (" + err.Error() +
			"), so an MCP server gated by requires_env on an env_sources variable is left out " +
			"of the agent configs this launch. Relaunch to rewrite the file.")
		return
	}
	for _, line := range splitLines(string(data)) {
		key, val, _, ok := parseExportLine(line)
		if !ok || launcherContractKey(key) {
			continue
		}
		if _, present := e.Vars[key]; present {
			continue
		}
		e.Vars[key] = val
		if e.sessionEnvKeys == nil {
			e.sessionEnvKeys = map[string]struct{}{}
		}
		e.sessionEnvKeys[key] = struct{}{}
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
	//
	// REPORTED at the top level: /ctx/host-nvim-config only exists because the launch
	// mounted it, so the user asked for this config and a failure means they open nvim
	// to a bare default. Still not fatal — an editor config is not a boot invariant —
	// but "the mount is there and the copy did not happen" is not something the jail
	// should keep to itself. The two silent returns above are the legitimately empty
	// cases (no mount at all).
	if err := copyTree(hostNvimConfig, jailNvim); err != nil {
		e.warn("Warning: copy host nvim config from " + hostNvimConfig + " to " +
			jailNvim + ": " + err.Error() + "; nvim starts with its defaults")
	}
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
			// Per-ENTRY failures are dropped by design and the reason is the
			// destination: this merges into a home the user also edits, so a single
			// unwritable subdirectory must not abort the other ninety. What the caller
			// gets is a partial copy, which the top-level report above cannot see —
			// accepted, because the alternative is either a fatal editor-config copy or
			// a per-file warning storm on a tree with hundreds of files. Fix the
			// permission and re-launch.
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
		// Dropped for the same reason as the recursive call above: one unwritable file
		// in an editor config tree may not cost the rest of the tree.
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
func miseUninstallRetired(e *Env) {
	miseUninstallTools(e, packload.EmbeddedRetireMiseTools())
}

// miseUninstallToolTimeout bounds each `mise uninstall`. A var so a test can reach
// the timeout branch without sleeping for it.
var miseUninstallToolTimeout = 30 * time.Second

// miseUninstallTools is the loop, split out from its one production caller so the
// tools are an argument — which is only a test seam, not a claim that the loop is
// cold. packload.RetiredMiseTools is a CORE list with entries in it, so this runs on
// every launch: MEASURED in a nested jail 2026-09-19, two retired tools, "ok in 21ms"
// and "ok in 18ms" — two per-launch steps that until now said nothing at all.
//
// EACH UNINSTALL IS REPORTED, and the bound is why. Retiring N tools can spend N ×
// 30 seconds of a launch — serially, before the agent's first prompt — and the
// version that discarded the error spent it without recording that it had. A
// timeout here also leaves the retired tool INSTALLED, so the next boot pays again:
// a silent one is a wait that recurs forever with no evidence on either end.
func miseUninstallTools(e *Env, tools []string) {
	for _, tool := range tools {
		toolName := strings.Trim(tool, `"`)
		cmd := exec.Command("mise", "uninstall", "--all", toolName)
		cmd.Stdout = nil
		cmd.Stderr = nil
		runBoundedStep(e, "mise uninstall --all "+toolName+" (retired tool)",
			miseUninstallToolTimeout, cmd)
	}
}

// ---------------------------------------------------------------------------
// Finalize PATH and exec bash
// ---------------------------------------------------------------------------
// BootPath is THE PATH the entrypoint hands the agent, and the authority the .bashrc
// export (shell.go) and AGENTS.md's "PATH order (exact)" line mirror.
//
// THE TWO GENERATED DIRS ARE NOW ADJACENT, AT THE FRONT, and that is B2
// (docs/design/program-delivery.md §3.5, OQ-PD12a). They are still different mechanisms
// and their ORDER relative to each other is still the whole point:
//
//   - BlockDir FIRST — blockers (grep, find). Interception is their whole job, so they
//     must precede everything, the launchers included: a tool that is both blocked and
//     pack-declared must resolve to the blocker.
//   - LaunchDir SECOND, AHEAD of the install prefixes — lazy installers/updaters (claude,
//     pnpm). It used to be last, after /bin, which made shadowing a baked binary
//     unrepresentable — and made the launcher unreachable the moment it succeeded, since
//     it installs into $NPM_CONFIG_PREFIX/bin or $HOME/.local/bin, both of which precede
//     it. The lazy INSTALL still worked (nothing was installed yet); the hourly UPDATE the
//     same script carries never ran again. Measured: claude.stamp untouched for nine days,
//     nineteen before that (OQ-PD8). Evergreen (OQ-PD12) needs the launcher to mediate
//     EVERY invocation, so the dir has to precede what it installs.
//
// What replaces the old position is a GENERATION-TIME check: no launcher is written for a
// name the image (or a declared mise tool) already provides — see launchercollision.go,
// which also explains why the check must ignore the install prefixes. Same outcome, weaker
// guarantee, and the trade is named in §3.5 rather than assumed.
//
// StorePackagesBin() sits IMMEDIATELY BEFORE /bin, and that position is chosen to be a
// no-op rather than a new rule. Under C4/C5 an opt-in launch delivers `packages:` — and
// the image's own bulk extras — from the mounted nix store instead of baking them
// (docs/reference/image-staging-vs-baking.md, "Store-delivered packages"). Those binaries are
// in /bin today, so
// putting the farm one step ahead of /bin leaves every precedence relation above it
// exactly as it was: the blockers still outrank them, the launchers still outrank them,
// and every per-project install prefix still outranks them. It is spelled
// UNCONDITIONALLY, even though most jails never opt in, because a PATH that varies by
// launch is a second authority in disguise — the dir simply does not exist on a jail that
// bakes, and a non-existent PATH entry costs nothing. What the farm's presence DOES gate
// is the launcher-collision check (imageProbePath), which reads the declaration rather
// than the directory.
//
// Extracted from execBash so the order is assertable without exec'ing a shell.
func BootPath(e *Env) string {
	return strings.Join(append(HomePathDirs(e), StorePackagesBin(), "/bin", "/usr/bin"), ":")
}

// HomePathDirs is the HEAD of BootPath: the two generated dirs, then every per-home install
// prefix, in the order every backend's PATH puts them. Exported for macosuser.SandboxPath, which
// takes its head from here rather than spelling a third copy (the copy it was had put
// ~/.local/bin third, so a tool installed there outranked a mise shim on macos-user alone).
// What follows the head is each platform's own: the store farm and the image's bins here, the
// darwin store prefix and the macOS system dirs there.
func HomePathDirs(e *Env) []string {
	return []string{e.BlockDir(), e.LaunchDir(), e.NpmBin(), e.MiseShims(), e.GoBin(), e.LocalBin()}
}

// executingLine is the "⚡ Executing: <command>" hand-over execBash prints for an
// exec-into command — the attach path; a fresh launch's generated script prints its own
// (run.executingBanner). Cyan unless color is off.
//
// The caller passes the NO_COLOR half of the one gate (tty.NoColor) over the JAIL's
// environment, and nothing else: the line was never terminal-gated, and the jail's
// environment is where the launcher's NO_COLOR arrives (run.Options.noColorEnvArgs, which
// an attach's exec carries too).
func executingLine(command string, color bool) string {
	if !color {
		return "⚡ Executing: " + command + "\n"
	}
	return "\033[1;36m⚡ Executing: " + command + "\033[0m\n"
}

// execBash set the final PATH, echo the command for the
// exec-into-existing path, source yolo-user-env.sh + activate mise, and exec
// bash --rcfile ~/.bashrc -c <activated command>. Never returns on success.
//
// announce is false for a command that prints its own "⚡ Executing" line: the first session's
// (run.buildSessionCmd's executingBanner). A pre-hold container's own command printed its own
// too, and is still recognized by the bootstrap it names.
func execBash(e *Env, command string, announce bool) error {
	// "PATH" is a literal, so the only two inputs os.Setenv rejects (an empty key, a
	// key holding "=" or NUL) are unreachable. Nothing to report.
	_ = os.Setenv("PATH", BootPath(e))

	isNewContainerCmd := strings.Contains(command, "yolo-bootstrap")
	if announce && command != "bash" && !isNewContainerCmd {
		fmt.Fprint(os.Stderr, executingLine(command, !tty.NoColor(os.Getenv)))
	}

	activatedCommand := activationPrefix(e) + command

	// exec bash --rcfile BASHRC -c activated. syscall.Exec does no PATH search,
	// so resolve bash on PATH first, then exec with argv[0]="bash".
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		return err
	}
	argv := []string{"bash", "--rcfile", e.BashrcPath(), "-c", activatedCommand}
	return sysExec(bashPath, argv, os.Environ())
}

// activationPrefix is what every session's shell runs before its command: the frozen user
// env file, when there is one, then mise's environment. The provisioning stage gets the same
// prefix (runProvisionStage), as it did when it was the first clause of the container's own
// command.
func activationPrefix(e *Env) string {
	userEnvFile := filepath.Join(e.Home, ".config", "yolo-user-env.sh")
	sourceUserEnv := ""
	if pathExists(userEnvFile) {
		sourceUserEnv = `. "` + userEnvFile + `" 2>/dev/null; `
	}
	return sourceUserEnv + `eval "$(mise env -s bash)" 2>/dev/null; `
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
	// THE DISK I/O PRIORITY, FIRST: before EnvFromOS (the second image removes its re-exec
	// marker from the environment that call copies) and before attachPassLog (a re-exec
	// after it would rotate boot.log twice). Nothing above this line may start a process
	// or touch a file, because on the path that applies a priority the first image never
	// gets past it (iopriority.go). What it found is reported once the log is open.
	ioOutcome := applyIOPriority(os.Args)

	// A SESSION'S HANGUP IS NOT A BOOT (sessionhangup.go): a launcher whose terminal closed asks
	// the jail to end its session's processes, and this invocation does that and nothing else —
	// no environment, no log, no generator. Only the priority above comes first, as it must for
	// every invocation.
	if len(args) == 2 && args[0] == HangupSessionArg {
		return hangUpSession(args[1])
	}

	// WHICH OF THREE THINGS THIS INVOCATION IS (jailmain.go): the container's main process, the
	// jail's first session, or any other session. For a session, command is what its shell runs;
	// for the main process it is the provisioning stage it records for the first session.
	mode, command := parseEntryArgs(args)

	// A SESSION RECORDS ITSELF under the id its launcher named, before anything that can wait,
	// so a launcher signalled during the boot's waits can still hang it up (sessionhangup.go).
	// This process's pid is the session's for good: execBash keeps it. A jail's main process
	// is no session, and an exec with no id is one this launcher cannot hang up. A session whose
	// hangup reached the jail first ends here, before it begins (JL-D77), and the jail's first
	// session, ending so, leaves provisioning to the sessions waiting for it (JL-D80).
	if id := os.Getenv(SessionIDEnv); id != "" && mode != modeHold {
		first := mode == modeFirstSession && os.Getenv(JailMainEnv) == JailMainHold
		if err := enterSession(id, first, os.Stderr); err != nil {
			return err
		}
	}

	// Covers the REFUSAL path (genFailuresError below returns before the exec). The
	// successful path releases explicitly just above execBash, since a deferred call never
	// runs once this process has been replaced. ReleaseEmbedded is idempotent, so both. What
	// it gives back is this process's lease on the build's shared tree (in-jail, under the
	// workspace's .local overlay), or a per-process fallback tree, which it deletes.
	defer packload.ReleaseEmbedded()

	e := EnvFromOS()

	// THE MAIN PROCESS SAYS ITS BOOT HAS BEGUN, before the boot, so a session exec'd into the
	// container meanwhile waits for it rather than finding nothing. A SESSION of a jail whose
	// main process is a hold waits for that boot and for provisioning BEFORE its own pass
	// (jailmain.go, JL-D33): the first session takes provisioning, every other waits for its
	// outcome. Both before the boot log, so a session refused here leaves pid 1's log alone.
	var gate *sessionGate
	provisioner := false
	switch {
	case mode == modeHold:
		markBoot(bootBooting)
	case e.Getenv(JailMainEnv) == JailMainHold:
		gate = newSessionGate(tty.IsTerminalFile(os.Stdin), os.Stderr)
		first := mode == modeFirstSession && gate.registerFirst(os.Getpid())
		if err := gate.awaitBoot(); err != nil {
			return err
		}
		if first {
			p, err := gate.claim()
			if err != nil {
				return err
			}
			provisioner = p
		} else {
			p, err := gate.await()
			if err != nil {
				return err
			}
			provisioner = p
		}
	}

	// Everything the boot says now also lands in <workspace>/.yolo/boot.log, which
	// outlives the container and is therefore readable after a boot that REFUSED —
	// the state OQ-R2's flip makes reachable, where there is no jail left to ask.
	// Never fatal: any failure here yields plain stderr. See bootlog.go.
	//
	// A SESSION'S PASS IS NOT THE JAIL'S BOOT (attachPassLog): in a jail whose main process is a
	// hold, boot.log is that process's, and a session's pass goes to the session log, the first
	// session's to the log alone, since the main process's boot has just printed the same lines
	// on this terminal.
	blog := attachPassLog(e, mode, gate != nil, os.Stderr)
	// The last refused boot's record goes with this boot, which writes its own if it refuses too.
	clearBootRefusal(e)
	reportIOPriority(e, ioOutcome)

	p := newPerfLog()
	p.mark("start")

	// EVERY STEP OF THE BOOT'S CONTENT, from the one table the macos-user bootstrap runs
	// too (bootsteps.go). The container runs each step the table does not exclude from it,
	// in table order, and marks the perf log after each. The reachability witness is the
	// table's last step, so it runs after every generator and above the gate below. A SESSION'S
	// PASS (gate != nil: an exec into a jail whose main process is a hold, the first session
	// included) also skips the steps that are the jail's own boot alone (notSessionPass), the
	// durable dir's walk and launch line among them (DS-D11); a jail with no hold keeps its one pass.
	//
	// There used to be a second os.Setenv of PATH after the steps, hand-spelling the same
	// list BootPath builds — for `mise trust`, the only subprocess that ran below this point.
	// That subprocess went with trustWorkspaceConfigs (3a309da4), and nothing between here
	// and execBash spawns a child or resolves a name on PATH any more, so the write was
	// dead. It also never MATCHED, its comment's claim notwithstanding: it omitted
	// e.LocalBin() from its first commit onward. BootPath is the single authority, applied
	// once in execBash; TestBootPathIsTheOnlyPathAuthority refuses a second spelling.
	runBootSteps(&bootRun{e: e, target: bootContainer, perf: p, sessionPass: gate != nil})

	// NOTE: We intentionally do NOT call `mise hook-env` here (flock deadlock).
	p.dump(e.Home)

	// A12: abort BEFORE handing control to the agent. Everything above has run, so
	// this reports every broken generator at once rather than one per restart.
	if err := genFailuresError(e); err != nil {
		// A provisioner that cannot reach its stage lets the lock go with no outcome, which a
		// waiter reads as abandoned; the main process records a refused boot, which refuses
		// every session that arrives.
		if provisioner {
			gate.abandon()
		}
		if mode == modeHold {
			markBoot(bootRefused)
		}
		// THE STRUCTURED CAUSE beside boot.log (bootrefusal.go), which a build jail's host act reads
		// to say what went wrong in plain words; before the hold, which may never end.
		recordBootRefusal(e)
		// THE HOLD, and its two phases straddle blog.finish deliberately: the notice
		// goes out while the log is still open (so a held boot's own instructions are
		// in boot.log), and the block happens after it is closed (so a hold nobody
		// ever releases cannot truncate the log of the refusal it exists to explain).
		// A launch that did not set YOLO_HOLD_ON_REFUSAL gets two no-ops. This is the
		// ONLY refusal that holds — hold.go states why the exec failure below does
		// not, and it is the one that would be wrong to.
		holdWait := beginHold(e)
		blog.finish(err)
		holdWait()
		return err
	}

	// Closed BEFORE the exec, not deferred: execBash replaces this process, so a
	// deferred close would never run and the log would end mid-sentence on every
	// SUCCESSFUL boot — the opposite of the signal it exists to carry.
	blog.finish(nil)
	// Released for the same reason, and the deferred release above is what covers the
	// refusal path instead. Safe here rather than earlier because the boot renders from the
	// MOUNTED tree (LoadJailPacks): the only thing this process took from the embedded copy
	// is EmbeddedRetireMiseTools, a list of tool-name strings, so no path into it survives
	// into the shell we are about to become. (The lease fd is close-on-exec besides.)
	packload.ReleaseEmbedded()
	// THE MAIN PROCESS HOLDS instead of running a command: it records the stage for the first
	// session, says the boot is done, and keeps the container up (holdJail). Its status says
	// whether a SIGTERM ended it (holdExitStatus).
	//
	// Beside the hold, for as long as it lasts, the main process records which versions of the
	// shared tool store this workspace uses (miseuserecord.go), which is how the host tells the
	// versions no jail on the machine has used for 30 days (OQ-DF4).
	if mode == modeHold {
		startMiseUseRecorder(e)
		return holdExitStatus(holdJail(command, os.Stderr))
	}
	// THE PROVISIONING STAGE, on this session's terminal, after its own pass and before its
	// command, which is where it ran when it was the container's own first clause. Its status
	// is the session's when it refused, so `yolo -- …` returns what the stage returned.
	if provisioner {
		if rc := provisionThisSession(e, gate, runProvisionStage); rc != 0 {
			return &ExitStatus{Code: rc}
		}
	}
	return execBash(e, command, mode != modeFirstSession)
}

// genFailuresError turns the collected refusals into the single error that aborts
// the boot (A12), or nil when every step succeeded. The message names each failing
// step, because "config generation failed" alone would send the user back into the
// logs to find out which one.
//
// TWO HEADINGS, because the boot refuses for two kinds of reason and the reader looks
// in different places for each. A failed boot step (genStep) is about what the boot
// writes; a service refusal (Env.refuseService) is about a service the jail needs, and
// listing the wire bridge under "config generator(s) failed" sent its reader to the
// config (docs/reference/loopback-tls-reachability.md OQ-R8). The step heading counts
// BOOT STEPS, not config generators, since not every genStep is a generator.
//
// The service heading ends with the hatch ONLY when the hatch would get the boot past
// every refusal listed: this is the boot's last line, and a way forward that leads
// into the next refusal is not one (R-D4 in that doc).
func genFailuresError(e *Env) error {
	fails, svcs := e.GenFailures(), e.serviceRefusals
	if len(fails) == 0 && len(svcs) == 0 {
		return nil
	}
	var sections []string
	if len(svcs) > 0 {
		sections = append(sections, serviceRefusalsSection(svcs, len(fails) == 0))
	}
	if len(fails) > 0 {
		sections = append(sections, fmt.Sprintf("%d boot step(s) failed:\n  - %s",
			len(fails), strings.Join(fails, "\n  - ")))
	}
	msg := "refusing to start the jail: " + strings.Join(sections, "\nand ")
	return fmt.Errorf("%s%s", msg, aclHint(e, fails))
}

// serviceRefusalsSection is genFailuresError's service heading and its list. alone says
// no config generator failed, which is half of whether the hatch line is offered.
func serviceRefusalsSection(svcs []serviceRefusal, alone bool) string {
	head := "it cannot use a service it needs:"
	if len(svcs) > 1 {
		head = "it cannot use services it needs:"
	}
	var b strings.Builder
	b.WriteString(head)
	hatchable := alone
	for _, s := range svcs {
		b.WriteString("\n  - " + s.msg)
		hatchable = hatchable && s.hatchable
	}
	if hatchable {
		b.WriteString("\n  If you only need a shell, or this jail can do without what is listed, launch " +
			"anyway:\n      " + paths.AllowUnreachableServicesEnv + "=1 <your yolo command>")
	}
	return b.String()
}

// aclHint appends the macos-user ACL diagnosis when the failures look like the
// sandbox uid cannot write the workspace, and "" otherwise.
//
// WHY IT LIVES AT THE FAILURE AND NOT ONLY IN A PREFLIGHT. The launch-side probe
// checks the workspace ROOT, which is the case that can be predicted cheaply. It
// cannot predict every path a generator will touch, and a full-tree check is not
// affordable on the hot path — measured 2026-09-03 at ~0.16ms per object, so
// ~16s on a repo with a fat node_modules, which is exactly the cost 84c55268
// removed by switching to an inheriting ACE. So the second line of defence is not
// a better prediction: it is making the failure explain itself, which needs no
// prediction at all and covers every path yolo writes.
//
// Gated on darwin AND on the errors actually being permission-shaped, because a
// generator can fail for a dozen unrelated reasons and appending an ACL lecture to
// a JSON parse error would be worse than silence.
func aclHint(e *Env, fails []string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	denied := false
	for _, f := range fails {
		if strings.Contains(f, "permission denied") {
			denied = true
			break
		}
	}
	if !denied {
		return ""
	}
	ws := e.WorkspaceDir()
	return "\n\nOn the macos-user backend this is usually the workspace ACL: the sandbox\n" +
		"user needs a shared-group grant on " + ws + ", and macOS applies one\n" +
		"only when a directory is CREATED — never retroactively, and never to a\n" +
		"grant left naming an account that was deleted and recreated.\n" +
		"  yolo macos-fix-permissions " + ws
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
// InstallDarwinHomeLayout with no sidecar, RemoveStaleGeneratedClients finding no stale
// files all do exactly that). Only a real failure — an unwritable path, a malformed value,
// an unreadable declared file — reaches this.
func genStep(e *Env, label string, fn func() error) {
	genStepAbout(e, label, genAbout{}, fn)
}

// genStepAbout is genStep for a step whose record can say what it was doing and whose
// contribution it was (bootrefusal.go): a pack's surface, its hook.
func genStepAbout(e *Env, label string, about genAbout, fn func() error) {
	if err := fn(); err != nil {
		e.warn("Error: " + label + ": " + err.Error())
		e.genStepFailure(label, about, err)
	}
}

// setEnvBoth sets key=val in both the process env (so children inherit) and
// e.Vars (so later generators reading e.Getenv agree).
func setEnvBoth(e *Env, key, val string) {
	e.Vars[key] = val
	// Every caller passes a compile-time literal key, and os.Setenv rejects only an
	// empty key or one containing "=" / NUL — so there is no reachable error. e.Vars
	// above is set unconditionally either way, which is what the generators read.
	_ = os.Setenv(key, val)
}
