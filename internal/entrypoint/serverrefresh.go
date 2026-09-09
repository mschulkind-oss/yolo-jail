package entrypoint

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// serverrefresh.go is the TRANSITIVE half of evergreen agent updates — the MCP and LSP
// servers a yolo-installed agent connects to (docs/design/program-delivery.md §3.5,
// OQ-PD12/OQ-PD12a; ../plans/evergreen-agent-updates.md build-order step 7). The agent CLIs
// went evergreen on 2026-09-04 and their servers did not: a yolo-installed MCP or LSP server
// moved only when the bootstrap reinstalled it, which for a warm home is never, because
// every install arm in the bootstrap is guarded by "is it missing?".
//
// # The trigger is the agent's, not its own
//
// RULED: *"a server inherits the trigger of the agent that connects to it"* (§3.5). A server
// exists only to serve an agent; if no agent runs, nothing needs it current. So there is no
// boot step here and no timer — the generated agent launchers call this once, after they have
// finished with their own program and BEFORE they exec it. That ordering is the design's one
// hard constraint on this file: the agent spawns its servers itself, and a half-updated set
// at connect time is worse than a stale one.
//
// # What is in scope, and why it is smaller than "the MCP table"
//
// Only the servers YOLO INSTALLS. An `mcp_servers` entry whose argv is `npx -y <pkg>@latest`
// resolves on every spawn and is already current — §6.1's *unmanaged* tier, where refreshing
// would be inventing management this design declines. What actually freezes is the
// bootstrap-installed set: the npm packages the enabled MCP presets need
// (`mcpPresetNpmPackages`) and the LSP recipes' npm and go arms, which the
// `~/.yolo-installed-lsps` sentinel tracks. `serverRefreshSet` is the one place that set is
// derived, and it derives it from the same declarations the bootstrap installs from.
//
// # Per-agent narrowing is a no-op TODAY, and this is where it would go
//
// §3.5 says the refresh happens for *"an agent whose config names that server"*, and notes
// that the set is a rendered per-agent fact rather than something to infer. In this tree it
// is the same set for every agent: `YOLO_MCP_PRESETS` and `lsp_servers` are jail-global keys,
// and `Env.LoadMCPServers` builds one table that every agent's surface renders from. So every
// launcher is handed the same list, and the narrowing costs nothing because there is nothing
// to narrow. If MCP presets ever become per-pack, `serverRefreshSet` grows a pack argument
// and nothing else here changes.
//
// # ABSENT and STALE are different acts
//
// The rule that looks like it covers both does not (§3.5's note). *Stale* — installed, past
// its interval — is bounded and swallowed: the working copy on disk is the fallback, so a
// slow or offline registry costs the user nothing but a message. *Absent* — named by the
// config and not installed at all — has no fallback, so it is installed synchronously,
// unbounded, and its failure is REPORTED rather than swallowed.
//
// Reported is not fatal, and the distinction §3.5 draws is absent-vs-stale rather than
// fatal-vs-not: the agent's own binary is present and runnable, and refusing to start `claude`
// because `chrome-devtools-mcp` could not be fetched would be a worse outcome than starting
// it with one server missing — which is the failure the agent itself will report, in its own
// words, at connect time. So this returns an error naming the package and the launcher still
// execs. (The agent's own cold start is the same shape one level up: `_do_install || true`,
// and then the `-x $REAL_BIN` test decides.)
//
// # Why the common case costs no subprocess
//
// Presence is a STAT, not a probe: `$NPM_CONFIG_PREFIX/lib/node_modules/<pkg>/package.json`
// for npm (the path `_installed_version` already reads) and `$GOBIN/<bin>` for go (the test
// the bootstrap's go arm already makes). Freshness is another stat. So a jail whose servers
// are all installed and all fresh runs a handful of stats and exits, and the launcher pays
// one process spawn — measured at ~15 ms, against the several forks (`date`, `stat`,
// `command -v`) the launcher already makes on the same path. A jail with NO yolo-installed
// servers pays nothing at all: `serverRefreshEnabled` bakes the call out of the launcher.

// ServerRefreshInterval is the seconds between refreshes of one server package.
//
// It is the agent launchers' own `UPDATE_INTERVAL`, and it has to be: a server refresh that
// ran on a different clock than the agent that triggers it would make "the first invocation
// refreshes it, the second sees a fresh stamp" (§3.5) false for exactly one of the two.
// TestServerRefreshSharesTheLaunchersThrottle pins the two numbers together across Go and
// the two bash templates, because nothing else can.
const ServerRefreshInterval = 3600

// ServerRefreshTimeout bounds the WHOLE stale phase, in seconds — not each package.
//
// It is the launchers' `UPDATE_TIMEOUT` for the same reason as the interval, and it is a
// budget for the phase rather than a per-package bound because the thing being protected is
// the command the user typed: with a per-package bound, a set of five servers behind an
// unreachable registry would cost five times the number written here, and the promise §3.5
// makes ("a hung updater must not hang the command the user typed") would quietly be worth
// a fifth of what it says.
const ServerRefreshTimeout = 60

// serverKind distinguishes the two resolvers the bootstrap installs servers with. It is not
// a `via`: no pack contributes a server, and nothing here reads packdecl.
type serverKind string

const (
	serverNpm serverKind = "npm"
	serverGo  serverKind = "go"
)

// serverPkg is one yolo-installed server package, as declared.
type serverPkg struct {
	kind serverKind
	// spec is the declaration verbatim — what `npm install -g` or `go install` is handed.
	spec string
	// installedPath is the file whose existence answers "is it installed?".
	installedPath string
	// pinned is true when the declaration names a version, in which case there is nothing
	// for a refresh to resolve: the declaration already IS the answer, exactly as the npm
	// launcher's PINNED branch says of a pack's `package`. No shipped recipe or preset is
	// pinned today (`pyright`, `typescript`, `golang.org/x/tools/gopls@latest`), so this is
	// for a recipe or a user declaration that grows one.
	pinned bool
}

// stampName is the per-package throttle file's basename.
//
// PER PACKAGE, not per agent (§3.5): two agents that both connect to pyright must not both
// pay for it — the first invocation refreshes it and the second sees a fresh stamp. The
// sanitizer exists because a spec is a path-shaped or scope-shaped string
// (`@modelcontextprotocol/server-sequential-thinking`, `golang.org/x/tools/gopls@latest`) and
// this is a filename. Two specs that differ only in a sanitized character would share a
// stamp; npm package names and go module paths cannot differ that way, and a shared stamp
// would cost a missed refresh rather than a wrong install.
func (p serverPkg) stampName() string {
	flat := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, p.spec)
	return string(p.kind) + "-" + flat + ".stamp"
}

// installArgv is the command that installs or refreshes this package.
//
// It is the BOOTSTRAP'S OWN spelling in both arms, deliberately: `npm install -g
// --prefer-online <pkg>` is what the LSP npm arm runs, and `go install <pkg>` is what the go
// arm runs. A refresh that reached the registry differently from the install would make the
// two capable of landing different bytes for one declaration, which is the drift every
// generated-client comment in this repo is about. `--prefer-online` is what makes a refresh a
// refresh rather than a cache read.
func (p serverPkg) installArgv() []string {
	switch p.kind {
	case serverGo:
		return []string{"go", "install", p.spec}
	default:
		return []string{"npm", "install", "-g", "--prefer-online", p.spec}
	}
}

// serverRefreshSet derives the yolo-INSTALLED server set for this jail, npm arm then go arm.
//
// The npm arm is the MCP presets' packages plus the LSP recipes' npm packages; the go arm is
// the LSP recipes' go modules. Both LSP halves read BOTH the env list (what THIS launch asked
// for) and the sentinel (what the LAST bootstrap installed), for `catalogNpmOrphans`'s reason
// turned around: a server in the sentinel and not the env is on its way out — the bootstrap's
// uninstall loop will remove it — and refreshing it would fight that loop, so the env list
// wins and the sentinel only ever ADDS the entries a launch cannot see because its own env is
// empty. Which is the state that matters here: this runs from a launcher, not from boot.
func serverRefreshSet(e *Env) []serverPkg {
	nodeModules := filepath.Join(e.NpmPrefix, "lib", "node_modules")

	var out []serverPkg
	seen := map[string]bool{}
	addNpm := func(spec string) {
		spec = strings.TrimSpace(spec)
		if spec == "" || seen[string(serverNpm)+spec] {
			return
		}
		seen[string(serverNpm)+spec] = true
		name, version := splitNpmSpec(spec)
		out = append(out, serverPkg{
			kind:          serverNpm,
			spec:          spec,
			installedPath: filepath.Join(nodeModules, name, "package.json"),
			pinned:        npmSpecIsPinned(version) && version != "latest",
		})
	}
	addGo := func(spec string) {
		spec = strings.TrimSpace(spec)
		if spec == "" || seen[string(serverGo)+spec] {
			return
		}
		seen[string(serverGo)+spec] = true
		bin := goModuleBinName(spec)
		if bin == "" {
			return
		}
		_, version, _ := strings.Cut(spec, "@")
		out = append(out, serverPkg{
			kind:          serverGo,
			spec:          spec,
			installedPath: filepath.Join(e.GoBin(), bin),
			pinned:        version != "" && version != "latest",
		})
	}

	for _, pkg := range strings.Fields(mcpPresetNpmPackages(e)) {
		addNpm(pkg)
	}
	for _, pkg := range splitLSPInstallList(e.Getenv("YOLO_LSP_NPM_INSTALL")) {
		addNpm(pkg)
	}
	for _, pkg := range splitLSPInstallList(e.Getenv("YOLO_LSP_GO_INSTALL")) {
		addGo(pkg)
	}
	// The sentinel's job here is the launch whose env says nothing: an older host launcher,
	// or a re-render that did not carry the LSP vars. It never contradicts the env list —
	// addNpm/addGo dedupe — it only supplies what the env could not.
	for _, entry := range readLSPSentinel(e) {
		if pkg, ok := strings.CutPrefix(entry, "npm:"); ok {
			addNpm(pkg)
		}
		if pkg, ok := strings.CutPrefix(entry, "go:"); ok {
			addGo(pkg)
		}
	}
	return out
}

// launcherServers is the baked MCP/LSP server set, as a generated launcher carries it.
//
// A named type rather than two strings on the argument list: the two lists have the same
// SHAPE (whitespace-separated declarations) and different meanings, so a bare `"", ""` at a
// call site says nothing about either, and swapping them at one of eighteen call sites would
// compile. `launcherServers{}` also says "no servers" out loud, which is what every test but
// the refresh ones wants.
type launcherServers struct {
	// npm is the npm package declarations — the MCP presets' packages and the LSP recipes'
	// npm arm.
	npm string
	// gomods is the LSP recipes' go arm, as `go install` arguments.
	gomods string
}

func (s launcherServers) empty() bool { return s.npm == "" && s.gomods == "" }

// ServerRefreshSpecs renders the baked launcher values: the npm arm and the go arm as
// whitespace-separated declaration lists.
//
// WHITESPACE-SEPARATED SCALARS rather than bash arrays, and BAKED rather than read from the
// environment. Both halves are the same lesson written down twice already in this package.
//
// Baked, because macos-user execs its launchers under `env -i`: a `${YOLO_MCP_PRESETS:-}` in
// a launcher template reads populated on the container backends and empty on the one backend
// with no image to hide it — see capturesDir and receiptsFile, which are baked for exactly
// this.
//
// Scalars, because an empty bash array under `set -u` is unbound on the bash 3.2 macos-user
// runs against, which is why UPDATE_VERB needs a HAS_UPDATE_VERB gate beside it. A scalar
// that may be empty needs no gate. Splitting on whitespace loses nothing: the bootstrap
// already word-splits both lists unquoted (`for pkg in $YOLO_MCP_NPM`), so a package name
// containing a space has never been installable by yolo in the first place.
func ServerRefreshSpecs(e *Env) launcherServers {
	var npmList, goList []string
	for _, p := range serverRefreshSet(e) {
		if p.kind == serverGo {
			goList = append(goList, p.spec)
		} else {
			npmList = append(npmList, p.spec)
		}
	}
	return launcherServers{
		npm:    strings.Join(npmList, " "),
		gomods: strings.Join(goList, " "),
	}
}

// ServerRefreshRequest is one refresh, fully specified. Every seam a test needs is a field:
// the clock, the command runner and the sink, so the walk can be driven without a registry.
type ServerRefreshRequest struct {
	// Env supplies Home, NpmPrefix and GoBin. Nothing else is read from it.
	Env *Env
	// NpmSpecs and GoSpecs are the baked declaration lists, whitespace-separated, exactly
	// as ServerRefreshSpecs rendered them.
	NpmSpecs string
	GoSpecs  string
	// Updates is this jail's agent_updates policy for the pack that triggered the refresh.
	//
	// It gates the STALE phase and NOT the absent one, which is the launchers' own split
	// rather than a new rule: `_update_due` is the only place UPDATES_ENABLED is consulted,
	// and the cold-install arm above it runs whatever the policy says — because a policy
	// that froze a cold install would leave the jail with no program at all. The same
	// reading applied to a server: a frozen policy means "do not move it", never "leave the
	// agent unable to connect to it".
	Updates bool
	// Stderr receives every message. Never nil in production; a nil one is discarded.
	Stderr io.Writer
	// Now is the clock the throttle reads. Nil means time.Now.
	Now func() time.Time
	// Run executes one install. Nil means runServerInstall — the real thing.
	Run func(ctx context.Context, stderr io.Writer, argv []string) error
}

// RefreshServers walks the baked server set: install what is absent, refresh what is stale,
// and leave everything else alone. It returns an error only for an ABSENT package it could
// not install, because that is the one outcome with no working copy behind it.
func RefreshServers(req ServerRefreshRequest) error {
	if req.Env == nil {
		return fmt.Errorf("refresh-servers: no environment")
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	now := req.Now
	if now == nil {
		now = time.Now
	}
	run := req.Run
	if run == nil {
		run = runServerInstall
	}

	pkgs := serverPkgsFromSpecs(req.Env, req.NpmSpecs, req.GoSpecs)
	if len(pkgs) == 0 {
		return nil
	}
	stampDir := filepath.Join(req.Env.Home, ".cache", "yolo-agent-stamps", "servers")

	var absentFailures []string
	var stale []serverPkg
	for _, p := range pkgs {
		if !pathExists(p.installedPath) {
			// UNBOUNDED and REPORTED. There is nothing on disk to fall back to, so a
			// bound here would trade a definite failure for a silent one.
			fmt.Fprintf(stderr, "  Installing %s server %s...\n", p.kind, p.spec)
			if err := run(context.Background(), stderr, p.installArgv()); err != nil {
				absentFailures = append(absentFailures, p.spec)
				fmt.Fprintf(stderr, "  ⚠ %s server %s is not installed and could not be "+
					"installed (%v) — the agent will not be able to connect to it.\n",
					p.kind, p.spec, err)
				continue
			}
			touchServerStamp(stampDir, p, now())
			continue
		}
		if p.pinned || !req.Updates {
			continue
		}
		if serverRefreshDue(stampDir, p, now()) {
			stale = append(stale, p)
		}
	}

	if len(stale) > 0 {
		refreshStaleServers(req.Env, stampDir, stale, stderr, now, run)
	}

	if len(absentFailures) > 0 {
		return fmt.Errorf("could not install: %s", strings.Join(absentFailures, ", "))
	}
	return nil
}

// refreshStaleServers spends ONE ServerRefreshTimeout budget across the whole stale set,
// under the install-prefix lock, and stamps every package it attempted.
//
// STAMPED ON FAILURE TOO, which is `_do_install`'s rule (`touch "$STAMP"` sits outside its
// success branch) and matters more here: without it an offline jail would spend the whole
// budget again on every single agent invocation, for a set that cannot be refreshed. One
// attempt per interval is the throttle; success is not its condition.
func refreshStaleServers(e *Env, stampDir string, stale []serverPkg, stderr io.Writer,
	now func() time.Time, run func(context.Context, io.Writer, []string) error) {
	ctx, cancel := context.WithTimeout(context.Background(),
		ServerRefreshTimeout*time.Second)
	defer cancel()

	// ONE LOCK PER INSTALL PREFIX (§3.5's contention rule), and the SAME lock dirs the
	// launchers take: the npm arm writes into $NPM_CONFIG_PREFIX, which is exactly what an
	// npm agent launcher's `_locked_update` is protecting. Taking a different lock would
	// leave the two able to write that prefix at the same moment while both believed they
	// had serialized. A launcher calls this AFTER dropping its own lock, so this cannot
	// deadlock against its caller.
	held := map[string]bool{}
	defer func() {
		for dir := range held {
			releasePrefixLock(dir)
		}
	}()

	for _, p := range stale {
		lockDir := serverLockDir(e, p.kind)
		if !held[lockDir] {
			if !acquirePrefixLock(lockDir, now()) {
				// PROCEED WITHOUT UPDATING AND SAY SO — §3.5 forbids both waiting and
				// failing here. No stamp: nothing was attempted, so the next invocation
				// should still try.
				fmt.Fprintf(stderr, "  %s servers: another update holds %s — "+
					"running the installed versions.\n", p.kind, lockDir)
				continue
			}
			held[lockDir] = true
		}
		fmt.Fprintf(stderr, "  Refreshing %s server %s...\n", p.kind, p.spec)
		if err := run(ctx, stderr, p.installArgv()); err != nil {
			// SWALLOWED, by ruling: a server already installed is used as-is when the
			// refresh does not land. The message is the whole consequence.
			fmt.Fprintf(stderr, "  ⚠ %s server %s could not be refreshed (%v) — "+
				"keeping the installed version.\n", p.kind, p.spec, err)
		}
		touchServerStamp(stampDir, p, now())
		if ctx.Err() != nil {
			// The budget is spent. Everything still on the list keeps its old bytes,
			// which is the fallback the stale rule names.
			fmt.Fprintf(stderr, "  ⚠ server refresh ran out of its %ds budget — "+
				"the rest keep their installed versions.\n", ServerRefreshTimeout)
			return
		}
	}
}

// serverPkgsFromSpecs re-derives serverPkg values from the launcher's baked lists. It is the
// other end of ServerRefreshSpecs: the paths and the pin verdict are computed HERE rather
// than baked, so a launcher generated against one home cannot carry stale paths into another.
func serverPkgsFromSpecs(e *Env, npmSpecs, goSpecs string) []serverPkg {
	nodeModules := filepath.Join(e.NpmPrefix, "lib", "node_modules")
	var out []serverPkg
	for _, spec := range strings.Fields(npmSpecs) {
		name, version := splitNpmSpec(spec)
		if name == "" {
			continue
		}
		out = append(out, serverPkg{
			kind:          serverNpm,
			spec:          spec,
			installedPath: filepath.Join(nodeModules, name, "package.json"),
			pinned:        npmSpecIsPinned(version) && version != "latest",
		})
	}
	for _, spec := range strings.Fields(goSpecs) {
		bin := goModuleBinName(spec)
		if bin == "" {
			continue
		}
		_, version, _ := strings.Cut(spec, "@")
		out = append(out, serverPkg{
			kind:          serverGo,
			spec:          spec,
			installedPath: filepath.Join(e.GoBin(), bin),
			pinned:        version != "" && version != "latest",
		})
	}
	return out
}

// serverLockDir names the lock for the prefix this kind installs into. The npm spelling is
// the npm launcher template's `LOCK_DIR` verbatim; nothing but this file writes $GOBIN, so
// the go arm's lock has only itself to serialize against — it exists so the rule reads the
// same for both arms rather than having an exception a reader has to remember.
func serverLockDir(e *Env, kind serverKind) string {
	if kind == serverGo {
		return filepath.Join(e.GoPath, ".yolo-update.lock")
	}
	return filepath.Join(e.NpmPrefix, ".yolo-update.lock")
}

// serverStaleLockAge is the launchers' STALE_LOCK: a lock nobody released must not freeze
// refreshes for the life of the home. Ten times the bound on one attempt.
const serverStaleLockAge = 10 * ServerRefreshTimeout

// acquirePrefixLock is a NON-BLOCKING mkdir, which is the launchers' `_take_lock` in Go and
// is a ruling rather than a shortcut: there is no flock in the image and none on a stock
// macOS, and §3.5 requires an invocation that cannot take the lock to proceed without
// updating.
func acquirePrefixLock(dir string, now time.Time) bool {
	if err := os.Mkdir(dir, 0o755); err == nil {
		return true
	}
	st, err := os.Stat(dir)
	if err != nil {
		return false
	}
	if now.Sub(st.ModTime()) <= serverStaleLockAge*time.Second {
		return false
	}
	_ = os.Remove(dir)
	return os.Mkdir(dir, 0o755) == nil
}

func releasePrefixLock(dir string) { _ = os.Remove(dir) }

// serverRefreshDue reports whether p is past its interval. An absent stamp is due — a package
// installed by the bootstrap has never been refreshed by this mechanism, and treating "no
// record" as fresh is how a warm home stays frozen forever, which is the defect this file
// exists to fix.
func serverRefreshDue(stampDir string, p serverPkg, now time.Time) bool {
	st, err := os.Stat(filepath.Join(stampDir, p.stampName()))
	if err != nil {
		return true
	}
	return now.Sub(st.ModTime()) > ServerRefreshInterval*time.Second
}

func touchServerStamp(stampDir string, p serverPkg, now time.Time) {
	if err := os.MkdirAll(stampDir, 0o755); err != nil {
		return
	}
	path := filepath.Join(stampDir, p.stampName())
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_ = f.Close()
	}
	_ = os.Chtimes(path, now, now)
}

// runServerInstall runs one install argv.
//
// YOLO_BYPASS_SHIMS=1 for the reason every other yolo-run install sets it: the blockers in
// ~/.yolo/bin/block are unconditional once generated, and an npm postinstall that shells out
// to `grep -r` must not have this install refuse. Output goes to the launcher's stderr
// because that is where the user is looking — they typed an agent's name and are watching it
// start.
func runServerInstall(ctx context.Context, stderr io.Writer, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty install argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "YOLO_BYPASS_SHIMS=1")
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	return cmd.Run()
}
