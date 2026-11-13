package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
)

// serverrefresh.go is the TRANSITIVE half of evergreen agent updates — the MCP servers a
// yolo-installed agent connects to (docs/design/program-delivery.md §3.5,
// OQ-PD12/OQ-PD12a). The agent CLIs went evergreen on 2026-09-04 and their servers did not:
// a yolo-installed MCP server moved only when the bootstrap reinstalled it, which for a warm
// home is never, because every install arm in the bootstrap is guarded by "is it missing?".
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
// would be inventing management this design declines. What actually freezes is the set yolo
// installs: the npm `program` each selected pack's `mcp` server runs (`packMCPServerPrograms`,
// which nothing installs at boot: absent until an agent's launcher installs it here, before
// that agent execs and spawns the server). The MCP presets' npm packages were the other half
// until the `mcp_presets` key retired (docs/reference/mcp-configuration.md, the retired mcp_presets key).
// `serverRefreshSet` is the
// one place that set is derived, from the declarations those installs read.
//
// A PACK SERVER'S PROGRAM HAS A LAUNCHER OF ITS OWN, and the server reaches the program
// through it (the launcher bakes the jail posture's launch flags). That launcher is generated
// with no update trigger and no server set of its own (GenerateAgentLaunchers): it runs at
// connect time, inside the agent's spawn, which is exactly where this file's one constraint
// says nothing may move. Its cold-install arm stays, for a server started with no agent's
// launcher in front of it.
//
// LANGUAGE SERVERS ARE NOT IN IT ANY MORE. They were — the LSP recipe table's npm and go
// arms, tracked by the `~/.yolo-installed-lsps` sentinel — until the table and every install
// path it fed were deleted (docs/reference/mcp-configuration.md#oq-lsp1). yolo installs no
// language server now, so there is none for it to keep current; the go arm this file carried
// served only that table and went with it.
//
// # Per-agent narrowing is a no-op TODAY, and this is where it would go
//
// §3.5 says the refresh happens for *"an agent whose config names that server"*, and notes
// that the set is a rendered per-agent fact rather than something to infer. In this tree it
// is the same set for every agent: a pack's `mcp` entry is composed into the one
// `YOLO_MCP_SERVERS` table the host hands the jail, and `Env.LoadMCPServers` builds one table
// from it that every agent's surface renders from. So
// every launcher is handed the same list, and the narrowing costs nothing because there is
// nothing to narrow. If that table ever becomes per-agent, `serverRefreshSet` grows an agent
// argument and nothing else here changes.
//
// # ABSENT and STALE are different acts
//
// The rule that looks like it covers both does not (§3.5's note). *Stale* — installed, past
// its interval — is bounded, and asks the registry first: a slow or offline registry, or one
// naming the installed version, costs the user nothing but a message. A NEWER version that was
// found and did not install is the one stale outcome that is not swallowed: it refuses the agent's
// launch (program-delivery.md PD-NF1; staleRefresh.refresh), unless the user's
// YOLO_USE_INSTALLED names the server's package. *Absent* — named by the
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
// (the path `_installed_version` already reads). Freshness is another stat. So a jail whose servers
// are all installed and all fresh runs a handful of stats and exits — measured 2026-09-09 at
// 6.5 ms per invocation, ten in a row.
//
// The comparison that matters, and the one that cannot drift: this is ONE process spawn on a
// path where the launcher already forks `date`, `stat` and `command -v` several times before
// it reaches the exec. And a jail with NO yolo-installed server pays nothing at all — the
// launcher's baked SERVERS_ENABLED is 0 and the call is never made.

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

// serverKind names the resolver servers are installed with. npm is the only one left: the go
// kind served the deleted LSP recipe's go arm alone. It is not a `via`: a pack server's program
// joins the set only when its `via` is npm (packMCPServerPrograms), and is then one more npm spec.
type serverKind string

const serverNpm serverKind = "npm"

// serverPkg is one yolo-installed server package, as declared.
type serverPkg struct {
	kind serverKind
	// spec is the declaration verbatim — what `npm install -g` or `go install` is handed.
	spec string
	// installedPath is the file whose existence answers "is it installed?".
	installedPath string
	// pinned is true when the declaration names a version, in which case there is nothing
	// for a refresh to resolve: the declaration already IS the answer, exactly as the npm
	// launcher's PINNED branch says of a pack's `package`.
	pinned bool
}

// stampName is the per-package throttle file's basename.
//
// PER PACKAGE, not per agent (§3.5): two agents that both connect to one server must not
// both pay for it — the first invocation refreshes it and the second sees a fresh stamp. The
// sanitizer exists because a spec can be a scope-shaped string (`@scope/name`) and this is
// a filename. Two specs that
// differ only in a sanitized character would share a stamp; npm package names cannot differ
// that way, and a shared stamp would cost a missed refresh rather than a wrong install.
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
// `--prefer-online` is what makes a refresh a refresh rather than a cache read. (It was also
// the deleted LSP npm arm's spelling, which is where it came from: a refresh that reached the
// registry differently from the install would make the two capable of landing different
// bytes for one declaration.)
func (p serverPkg) installArgv() []string {
	return []string{"npm", "install", "-g", "--prefer-online", p.spec}
}

// serverRefreshSet derives the yolo-INSTALLED server set for this jail: the npm program each
// of packs' held `mcp` servers runs, deduplicated by declaration.
func serverRefreshSet(e *Env, packs []*packload.Pack) []serverPkg {
	nodeModules := filepath.Join(e.NpmPrefix, "lib", "node_modules")

	var specs []string
	for _, inst := range packMCPServerPrograms(e, packs) {
		specs = append(specs, inst.Package)
	}
	var out []serverPkg
	seen := map[string]bool{}
	for _, spec := range specs {
		if seen[spec] {
			continue
		}
		seen[spec] = true
		name, version := splitNpmSpec(spec)
		out = append(out, serverPkg{
			kind:          serverNpm,
			spec:          spec,
			installedPath: filepath.Join(nodeModules, name, "package.json"),
			pinned:        npmSpecIsPinned(version) && version != "latest",
		})
	}
	return out
}

func ServerRefreshNpmSpecs(e *Env, packs []*packload.Pack) string {
	return ServerRefreshSpecs(e, packs).npm
}

// packMCPServerPrograms is the npm `program` of packs that each held `mcp` server's `bin` names
// (packload.HeldMCPServers), for a server this jail's composed YOLO_MCP_SERVERS still carries:
// the host composes every selected pack's entry into that table and leaves a user's `null` in it
// as a removal, so a name absent or null there is a server no agent here starts, and an older
// host that composed no table renders none. One per bin, in declaration order; the program is
// the first declaration of that bin, the one GenerateAgentLaunchers writes a launcher for.
//
// Only an npm program with no declared install flags: the refresh hands `npm install -g` the
// declared package alone (serverPkg.installArgv), and the baked list has no room for flags, so a
// program that declares some is left to its own launcher, which carries them, and keeps that
// launcher's trigger. An installer program is left the same way: the refresh has no arm for it.
func packMCPServerPrograms(e *Env, packs []*packload.Pack) []packdecl.Install {
	held := composedMCPServerNames(e)
	if len(held) == 0 {
		return nil
	}
	programs := map[string]packdecl.Install{}
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, inst := range installs {
			if _, dup := programs[inst.Bin]; !dup {
				programs[inst.Bin] = inst
			}
		}
	}
	var out []packdecl.Install
	seen := map[string]bool{}
	for _, s := range packload.HeldMCPServers(packs) {
		bin := s.Server.Bin
		if bin == "" || seen[bin] || !held[s.Server.Name] {
			continue
		}
		inst, ok := programs[bin]
		if !ok || inst.Kind != "npm" || len(inst.Flags) > 0 {
			continue
		}
		seen[bin] = true
		out = append(out, inst)
	}
	return out
}

// composedMCPServerNames is each server YOLO_MCP_SERVERS carries as an entry: not a null, which
// is a removal, and not a malformed value, which the render drops (mcpServersWith).
func composedMCPServerNames(e *Env) map[string]bool {
	raw := e.Getenv("YOLO_MCP_SERVERS")
	if raw == "" {
		return nil
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	table, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	names := map[string]bool{}
	for _, name := range table.Keys() {
		if v, _ := table.Get(name); v != nil {
			_, names[name] = v.(*jsonx.OrderedMap)
		}
	}
	return names
}

// launcherServers is the baked MCP server set, as a generated launcher carries it.
//
// A named type rather than a bare string on the argument list: a `""` at a call site says
// nothing about what it is, and `launcherServers{}` says "no servers" out loud, which is what
// every test but the refresh ones wants. It carried a second, go-module list until the LSP
// recipe that was that list's only source was deleted.
type launcherServers struct {
	// npm is the npm package declarations — the pack servers' programs.
	npm string
}

func (s launcherServers) empty() bool { return s.npm == "" }

// ServerRefreshSpecs renders the baked launcher value: the npm arm as a whitespace-separated
// declaration list.
//
// WHITESPACE-SEPARATED SCALARS rather than bash arrays, and BAKED rather than read from the
// environment. Both halves are the same lesson written down twice already in this package.
//
// Baked, because macos-user execs its launchers under `env -i`: a `${YOLO_MCP_SERVERS:-}` in
// a launcher template reads populated on the container backends and empty on the one backend
// with no image to hide it — see capturesDir and receiptsFile, which are baked for exactly
// this.
//
// Scalars, because an empty bash array under `set -u` is unbound on the bash 3.2 macos-user
// runs against, which is why UPDATE_VERB needs a HAS_UPDATE_VERB gate beside it. A scalar
// that may be empty needs no gate. Splitting on whitespace loses nothing: `npm install -g`
// takes the list as separate words anyway, so a package name
// containing a space has never been installable by yolo in the first place.
func ServerRefreshSpecs(e *Env, packs []*packload.Pack) launcherServers {
	var npmList []string
	for _, p := range serverRefreshSet(e, packs) {
		npmList = append(npmList, p.spec)
	}
	return launcherServers{npm: strings.Join(npmList, " ")}
}

// ServerRefreshRequest is one refresh, fully specified. Every seam a test needs is a field:
// the clock, the command runner and the sink, so the walk can be driven without a registry.
type ServerRefreshRequest struct {
	// Context owns this request's lock waits and install subprocesses; nil means Background.
	Context context.Context
	// Fresh makes stale refresh failures fatal to the explicit request instead of falling back.
	Fresh bool
	// Env supplies Home and NpmPrefix. Nothing else is read from it.
	Env *Env
	// NpmSpecs is the baked declaration list, whitespace-separated, exactly as
	// ServerRefreshSpecs rendered it.
	NpmSpecs string
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
	// View asks the registry for pkg's latest version: the STALE phase's version query (PD-NF1),
	// which tells "a newer version did not install" from "nothing was known to be newer". Nil
	// means runServerView, `npm view <pkg> version`.
	View func(ctx context.Context, pkg string) (string, error)
	// Bin is the program whose launcher asked (`--bin`), the command a refusal tells the user to
	// run again; empty for a hand invocation.
	Bin string
	// Getenv reads the user's patchrecovery.UseInstalledEnv. Nil means os.Getenv: the hatch is the
	// command's, typed in front of the agent, so it arrives in this process's environment.
	Getenv func(string) string
}

// ServerUpdateRefusal is RefreshServers' error when a STALE server's newer version was found and
// did not install (PD-NF1): the refusal has been said on Stderr, and the launcher must not exec
// its agent. `yolo internal refresh-servers` exits UpdateRefusedStatus for it.
type ServerUpdateRefusal struct {
	// Specs are the refused declarations, in the order they were refreshed.
	Specs []string
}

func (r *ServerUpdateRefusal) Error() string {
	return "a newer version of MCP server " + strings.Join(r.Specs, ", ") + " failed to install"
}

// serverRefused is one STALE server whose newer version did not install.
type serverRefused struct {
	p                 serverPkg
	installed, latest string
	err               error
}

// RefreshServers walks the baked server set: install what is absent, refresh what is stale,
// and leave everything else alone. An ordinary refresh returns an error for an ABSENT package it
// could not install, the one outcome with no working copy behind it, and a *ServerUpdateRefusal
// for a STALE package whose newer version did not install (PD-NF1), which takes precedence.
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
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}

	pkgs := serverPkgsFromSpecs(req.Env, req.NpmSpecs)
	if len(pkgs) == 0 {
		return nil
	}
	stampDir := filepath.Join(req.Env.Home, ".cache", "yolo-agent-stamps", "servers")

	var absentFailures []string
	var stale []serverPkg
	for _, p := range pkgs {
		if bin, identity := req.Env.ProgramCaptureForSpec(p.spec); identity != "" {
			marker := ProgramCaptureMarker(req.Env.Home, bin)
			previous, _ := os.ReadFile(marker)
			warm := strings.TrimSpace(string(previous)) == identity && pathExists(p.installedPath)
			{
				argv := []string{"yolo", "internal", "capture-materialize", "--store=" + capturesDir(req.Env), "--bin=" + bin, "--home=" + req.Env.Home, "--declared=" + p.spec, "--compatibility=" + identity, "--receipts=" + receiptsFile(req.Env)}
				delivery := req.Env.ProgramCaptureDeliveries()[bin]
				if delivery.Runtime != "" {
					argv = append(argv, "--runtime="+delivery.Runtime, "--node-floor="+delivery.NodeFloor)
				}
				if warm {
					argv = append(argv, "--validate-only")
				}
				if err := run(ctx, stderr, argv); err != nil {
					return fmt.Errorf("prepared MCP package %s could not be delivered: %w — retry a fresh launch", p.spec, err)
				}
				if warm {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(marker, []byte(identity+"\n"), 0o644); err != nil {
					return err
				}
			}
			continue // this launch's generation never updates in place
		}
		if !pathExists(p.installedPath) {
			// UNBOUNDED and REPORTED. There is nothing on disk to fall back to, so a
			// bound here would trade a definite failure for a silent one.
			fmt.Fprintf(stderr, "  Installing %s server %s...\n", p.kind, p.spec)
			if err := run(ctx, stderr, p.installArgv()); err != nil {
				absentFailures = append(absentFailures, p.spec)
				fmt.Fprintf(stderr, "  ⚠ %s server %s is not installed and could not be "+
					"installed (%v) — the agent will not be able to connect to it.\n",
					p.kind, p.spec, err)
				continue
			}
			touchServerStamp(stampDir, p, now())
			continue
		}
		if p.pinned || (!req.Updates && !req.Fresh) {
			continue
		}
		if req.Fresh || serverRefreshDue(stampDir, p, now()) {
			stale = append(stale, p)
		}
	}

	if len(stale) > 0 {
		view := req.View
		if view == nil {
			view = runServerView
		}
		sr := staleRefresh{strict: req.Fresh, e: req.Env, stampDir: stampDir, stderr: stderr, now: now,
			run: run, view: view, bin: req.Bin, hatch: patchrecovery.Read(req.Getenv, patchrecovery.UseInstalledEnv)}
		if err := sr.refresh(ctx, stale); err != nil {
			return err
		}
	}

	if len(absentFailures) > 0 {
		return fmt.Errorf("could not install: %s", strings.Join(absentFailures, ", "))
	}
	return nil
}

// staleRefresh is one STALE phase: the request's seams, and the user's hatch.
type staleRefresh struct {
	strict   bool
	e        *Env
	stampDir string
	stderr   io.Writer
	now      func() time.Time
	run      func(context.Context, io.Writer, []string) error
	view     func(context.Context, string) (string, error)
	bin      string
	hatch    patchrecovery.Scope
	// parent is the caller's context, whose cancellation is an interrupt rather than the budget.
	parent context.Context
}

// refresh spends ONE ServerRefreshTimeout budget across the whole stale set, under the
// install-prefix lock.
//
// IT ASKS BEFORE IT INSTALLS (PD-NF1). A server whose registry did not answer, or answered the
// installed version, is left as it is and stamped: nothing was known to be newer, which is not a
// failure. A server whose NEWER version was found and did not install — npm failed, or the budget
// ran out under it — is a REFUSAL: said here, returned as *ServerUpdateRefusal, and the launcher
// does not exec its agent. It is not stamped, so the next invocation retries it. The user's
// patchrecovery.UseInstalledEnv, naming the server's package (or "1"), keeps the installed version
// instead, stamped.
//
// STAMPED ON EVERY OTHER OUTCOME, which is `_do_install`'s rule and matters more here: without it
// an offline jail would spend the whole budget again on every single agent invocation, for a set
// that cannot be refreshed.
//
// An explicit fresh request (strict) asks nothing first: it installs every stale server, and any
// failure refuses that request, as it always has.
func (s staleRefresh) refresh(parent context.Context, stale []serverPkg) error {
	s.parent = parent
	ctx, cancel := context.WithTimeout(parent, ServerRefreshTimeout*time.Second)
	defer cancel()
	var failures []string
	var refused []serverRefused

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

	budgetSpent := func() {
		fmt.Fprintf(s.stderr, "  ⚠ server refresh was cancelled or ran out of its %ds budget — "+
			"the rest keep their installed versions.\n", ServerRefreshTimeout)
	}
	for _, p := range stale {
		lockDir := serverLockDir(s.e, p.kind)
		if !held[lockDir] {
			if !acquirePrefixLock(lockDir, s.now()) {
				// PROCEED WITHOUT UPDATING AND SAY SO — §3.5 forbids both waiting and
				// failing here, and a busy lock checked nothing (PD-NF5). No stamp: nothing
				// was attempted, so the next invocation should still try.
				fmt.Fprintf(s.stderr, "  %s servers: another update holds %s — "+
					"running the installed versions.\n", p.kind, lockDir)
				if s.strict {
					failures = append(failures, fmt.Sprintf("%s server lock is busy", p.spec))
				}
				continue
			}
			held[lockDir] = true
		}
		if !s.strict {
			stop, r := s.refreshOne(ctx, p)
			if r != nil {
				refused = append(refused, *r)
			}
			if stop {
				budgetSpent()
				break
			}
			continue
		}
		fmt.Fprintf(s.stderr, "  Refreshing %s server %s...\n", p.kind, p.spec)
		if err := s.run(ctx, s.stderr, p.installArgv()); err != nil {
			// An explicit fresh request refuses to proceed after this failed refresh, without
			// claiming the old bytes are fresh.
			fmt.Fprintf(s.stderr, "  ⚠ %s server %s could not be refreshed (%v).\n", p.kind, p.spec, err)
			failures = append(failures, fmt.Sprintf("%s server refresh failed: %v", p.spec, err))
		}
		touchServerStamp(s.stampDir, p, s.now())
		if ctx.Err() != nil {
			budgetSpent()
			failures = append(failures, ctx.Err().Error())
			break
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("fresh server preparation failed: %s", strings.Join(failures, "; "))
	}
	if len(refused) != 0 {
		writeServerRefusal(s.stderr, s.bin, refused)
		r := &ServerUpdateRefusal{}
		for _, f := range refused {
			r.Specs = append(r.Specs, f.p.spec)
		}
		return r
	}
	return nil
}

// refreshOne is one ordinary STALE server: ask, then install only what is newer. stop reports
// that the budget ended the phase; a non-nil refusal is a newer version that did not install.
func (s staleRefresh) refreshOne(ctx context.Context, p serverPkg) (stop bool, refusal *serverRefused) {
	name, _ := splitNpmSpec(p.spec)
	installed := installedServerVersion(p)
	latest, err := s.view(ctx, name)
	latest = strings.TrimSpace(latest)
	if err != nil || latest == "" {
		if ctx.Err() != nil {
			return true, nil
		}
		fmt.Fprintf(s.stderr, "  %s server %s: could not ask the npm registry for a newer version — "+
			"keeping the installed %s.\n", p.kind, p.spec, orInstalledVersion(installed))
		touchServerStamp(s.stampDir, p, s.now())
		return false, nil
	}
	if latest == installed {
		touchServerStamp(s.stampDir, p, s.now())
		return false, nil
	}
	fmt.Fprintf(s.stderr, "  Refreshing %s server %s %s → %s...\n", p.kind, p.spec, orInstalledVersion(installed), latest)
	if err := s.run(ctx, s.stderr, p.installArgv()); err != nil {
		if errors.Is(s.parent.Err(), context.Canceled) {
			// The caller's cancellation is the launcher's Ctrl-C (refreshservers.go's NotifyContext):
			// the user's own interrupt is the hatch at that moment (PD-NF4).
			fmt.Fprintf(s.stderr, "  ⚠ %s server %s: its refresh was interrupted — keeping the installed version.\n",
				p.kind, p.spec)
			touchServerStamp(s.stampDir, p, s.now())
			return true, nil
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("it ran out of the %ds server refresh budget", ServerRefreshTimeout)
		}
		if s.hatch.Covers(patchrecovery.Subject{Key: p.spec, Name: name}) {
			fmt.Fprintf(s.stderr, "  ⚠ %s server %s %s → %s did not install (%v) — keeping the installed version, "+
				"as %s asks.\n", p.kind, p.spec, orInstalledVersion(installed), latest, err, patchrecovery.UseInstalledEnv)
			touchServerStamp(s.stampDir, p, s.now())
			return ctx.Err() != nil, nil
		}
		return ctx.Err() != nil, &serverRefused{p: p, installed: installed, latest: latest, err: err}
	}
	touchServerStamp(s.stampDir, p, s.now())
	return ctx.Err() != nil, nil
}

// orInstalledVersion names an installed version, or says it is one when its number is unknown.
func orInstalledVersion(v string) string {
	if v == "" {
		return "version"
	}
	return v
}

// installedServerVersion reads the installed package's version, "" when it cannot tell.
func installedServerVersion(p serverPkg) string {
	data, err := os.ReadFile(p.installedPath)
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// writeServerRefusal says the refusal in the launchers' shape (PD-NF1): an ERROR line per server,
// the fact under it, Repair:, then Start anyway: with the hatch naming every refused package.
func writeServerRefusal(w io.Writer, bin string, refused []serverRefused) {
	var names []string
	for _, r := range refused {
		name, _ := splitNpmSpec(r.p.spec)
		names = append(names, name)
		fmt.Fprintf(w, "yolo: ERROR: %s server %s %s → %s: installing the newer version failed\n",
			r.p.kind, name, orInstalledVersion(r.installed), r.latest)
		fmt.Fprintf(w, "  %s: %v (its output is above).\n", strings.Join(r.p.installArgv(), " "), r.err)
	}
	cmd := bin
	if cmd == "" {
		cmd = "yolo"
	}
	fmt.Fprintf(w, "\nRepair:\n  fix what stopped it (its output is above), then run %s again, which retries the update:\n    %s\n", cmd, cmd)
	hatches := UseInstalledHatches(strings.Join(names, ","), bin)
	if len(refused) == 1 && refused[0].installed != "" {
		hatches = WithInstalledVersion(hatches, refused[0].installed)
	}
	fmt.Fprint(w, patchrecovery.Section(patchrecovery.StartAnyway, hatches, false))
}

// runServerView is the production View: `npm view <pkg> version`, bounded by ctx and by npm's own
// fetch timeout, its stdout the answer.
func runServerView(ctx context.Context, pkg string) (string, error) {
	cmd := exec.CommandContext(ctx, "npm", "view", pkg, "version", "--fetch-retries=0", "--fetch-timeout=10000")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "YOLO_BYPASS_SHIMS=1")
	out, err := cmd.Output()
	return string(out), err
}

// serverPkgsFromSpecs re-derives serverPkg values from the launcher's baked list. It is the
// other end of ServerRefreshSpecs: the paths and the pin verdict are computed HERE rather
// than baked, so a launcher generated against one home cannot carry stale paths into another.
func serverPkgsFromSpecs(e *Env, npmSpecs string) []serverPkg {
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
	return out
}

// serverLockDir names the lock for the prefix this kind installs into: the npm launcher
// template's `LOCK_DIR` verbatim, since the two write the same prefix.
func serverLockDir(e *Env, kind serverKind) string {
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
	// The remove's error is dropped because the Mkdir on the next line is the real
	// answer: if the stale lock could not be cleared, the Mkdir fails and this returns
	// false, which is the whole contract — "could not take the lock" — and §3.5 requires
	// an invocation that cannot take it to proceed without updating.
	_ = os.Remove(dir)
	return os.Mkdir(dir, 0o755) == nil
}

// releasePrefixLock drops the lock dir. The error stays dropped and the staleness
// rule above is why: a lock that fails to release is indistinguishable from one held
// by a process that died, and acquirePrefixLock already breaks any lock older than
// serverStaleLockAge. So the worst case is one refresh interval of no refreshes,
// which self-heals — and a warning about it would fire on a path the user cannot act
// on anyway.
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
	// Both errors stay dropped, and serverRefreshDue is why: an absent or un-touched
	// stamp reads as DUE, so every failure here degrades to "refresh again next
	// invocation" — the safe direction, and the one this file's own comment says it
	// chose deliberately ("treating 'no record' as fresh is how a warm home stays
	// frozen forever"). A stamp that cannot be written is a repeated refresh, not a
	// missed one, and the refresh itself already reports its own outcome.
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "YOLO_BYPASS_SHIMS=1")
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	return cmd.Run()
}
