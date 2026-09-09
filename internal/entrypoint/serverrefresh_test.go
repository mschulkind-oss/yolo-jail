package entrypoint

// serverrefresh_test.go drives the transitive MCP/LSP refresh (program-delivery.md §3.5,
// OQ-PD12a; evergreen-agent-updates.md step 7).
//
// THE FIRST TWO CELLS ARE THE CALL-SITE HALF, and they are first on purpose. AGENTS.md's
// *"a test that pins the CALLEE while the CALL SITE is unpinned is not a test"* is the shape
// this repo has shipped wrong five times, and it applies exactly here: RefreshServers can be
// unit-tested to a fare-thee-well while nothing calls it, and the feature would then be off
// with every cell below green. So the generated launchers are driven through
// GenerateAgentLaunchers — the function boot.go calls — and asserted to CONTAIN the call with
// the baked set in it.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- the call site ---------------------------------------------------------------------

// TestGeneratedLauncherCallsTheServerRefresh is the load-bearing cell. Delete the refresh
// block from either template, or the splice that fills it, and the servers stop moving with
// nothing else failing.
func TestGeneratedLauncherCallsTheServerRefresh(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":            home,
		"YOLO_PACK_ROOT":       writePackWithProgram(t, "agenty", "yolo-not-a-real-agent"),
		"YOLO_MCP_PRESETS":     `["sequential-thinking"]`,
		"YOLO_LSP_GO_INSTALL":  "golang.org/x/tools/gopls@latest",
		"YOLO_LSP_NPM_INSTALL": "pyright",
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.LaunchDir(), "yolo-not-a-real-agent"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	for _, want := range []string{
		// The call itself, and the exact argv the CLI parses. A rename on either side
		// leaves the launcher invoking a subcommand `yolo internal` refuses.
		"yolo internal refresh-servers",
		`--home="$HOME"`,
		`--npm="$SERVERS_NPM"`,
		`--go="$SERVERS_GO"`,
		`--updates="$UPDATES_ENABLED"`,
		// The BAKED set. Reading these from the environment instead is the macos-user
		// `env -i` defect capturesDir and receiptsFile are baked to avoid, so the values
		// have to be IN the script.
		"\nSERVERS_ENABLED=1\n",
		"@modelcontextprotocol/server-sequential-thinking",
		"pyright",
		"golang.org/x/tools/gopls@latest",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the generated launcher is missing %q — the transitive refresh is not "+
				"wired, so a yolo-installed MCP/LSP server only ever moves when the "+
				"bootstrap reinstalls it:\n%s", want, got)
		}
	}
	// Ordering is the design's ONE hard constraint (§3.5): the refresh must complete
	// before the exec, because the agent spawns its servers itself.
	call := strings.Index(got, "if [ \"$SERVERS_ENABLED\" = \"1\" ]")
	execAt := strings.LastIndex(got, `exec "$REAL_BIN" "$@"`)
	if call < 0 || execAt < 0 || call > execAt {
		t.Errorf("the refresh is not ordered before the exec (call=%d exec=%d) — a "+
			"half-updated server set at connect time is worse than a stale one", call, execAt)
	}
}

// TestALauncherWithNoServersCarriesNoRefresh is the other arm of the same call site, and it
// is what keeps the cell above from passing against a generator that bakes the call
// unconditionally. A jail with no yolo-installed server must pay no process spawn.
func TestALauncherWithNoServersCarriesNoRefresh(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": writePackWithProgram(t, "agenty", "yolo-not-a-real-agent"),
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.LaunchDir(), "yolo-not-a-real-agent"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nSERVERS_ENABLED=0\n") {
		t.Errorf("a jail with no yolo-installed servers did not bake SERVERS_ENABLED=0, so "+
			"every agent invocation spawns a subprocess to discover there is nothing to "+
			"do:\n%s", body)
	}
}

// TestServerRefreshSharesTheLaunchersThrottle is the cross-language pin. The interval and
// the timeout are each spelled in THREE places — this Go file and the two bash templates —
// and §3.5's "the first invocation refreshes it, the second sees a fresh stamp" is false for
// one of the two the moment they disagree.
func TestServerRefreshSharesTheLaunchersThrottle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		goValue int
		bashVar string
	}{
		{"interval", ServerRefreshInterval, "UPDATE_INTERVAL"},
		{"timeout", ServerRefreshTimeout, "UPDATE_TIMEOUT"},
	} {
		// `\b` on the value, not a trailing newline: the npm template carries a
		// same-line comment after UPDATE_INTERVAL, and matching the newline would make
		// this cell pass or fail on comment placement instead of on the number.
		want := regexp.MustCompile(`(?m)^` + tc.bashVar + `=` +
			strconv.Itoa(tc.goValue) + `\b`)
		for tplName, tpl := range map[string]string{
			"npm":    npmLauncherTemplate,
			"native": nativeLauncherTemplate,
		} {
			if !want.MatchString(tpl) {
				t.Errorf("the %s launcher template does not spell %s=%d, so the server "+
					"refresh (%s) and the agent update it rides with are on different "+
					"clocks", tplName, tc.bashVar, tc.goValue, tc.name)
			}
		}
	}
}

// --- the walk --------------------------------------------------------------------------

// refreshProbe is one refresh, with the clock and the installer replaced.
type refreshProbe struct {
	e        *Env
	stampDir string
	// ran records every install argv, in order.
	ran [][]string
	// fail names the specs whose install must fail.
	fail map[string]bool
	// out collects everything the refresh said, so a cell can assert it SAID it.
	out  strings.Builder
	now  time.Time
	ctxs []context.Context
}

func newRefreshProbe(t *testing.T, vars map[string]string) *refreshProbe {
	t.Helper()
	home := t.TempDir()
	all := map[string]string{"JAIL_HOME": home}
	for k, v := range vars {
		all[k] = v
	}
	p := &refreshProbe{
		e:    NewEnv(all),
		fail: map[string]bool{},
		now:  time.Unix(1_700_000_000, 0),
	}
	p.stampDir = filepath.Join(home, ".cache", "yolo-agent-stamps", "servers")
	// Real prefixes, so the presence stats have somewhere to look.
	for _, d := range []string{
		filepath.Join(p.e.NpmPrefix, "lib", "node_modules"),
		p.e.GoBin(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// installed marks an npm package present, the way npm does: a package.json under the global
// node_modules. This is the exact path serverRefreshSet probes.
func (p *refreshProbe) installed(t *testing.T, pkg string) {
	t.Helper()
	dir := filepath.Join(p.e.NpmPrefix, "lib", "node_modules", pkg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installedGo marks a go module's binary present under $GOBIN.
func (p *refreshProbe) installedGo(t *testing.T, bin string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.e.GoBin(), bin), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// stamp writes a per-package throttle file aged by `age`.
func (p *refreshProbe) stamp(t *testing.T, kind serverKind, spec string, age time.Duration) time.Time {
	t.Helper()
	if err := os.MkdirAll(p.stampDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.stampDir, serverPkg{kind: kind, spec: spec}.stampName())
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	at := p.now.Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return at
}

// stampTime reads a package's throttle mtime. It FAILS on an absent stamp: a cell that
// wanted "no stamp" would otherwise read a zero time and compare equal to nothing.
func (p *refreshProbe) stampTime(t *testing.T, kind serverKind, spec string) time.Time {
	t.Helper()
	st, err := os.Stat(filepath.Join(p.stampDir, serverPkg{kind: kind, spec: spec}.stampName()))
	if err != nil {
		t.Fatalf("no stamp for %s %s: %v", kind, spec, err)
	}
	return st.ModTime()
}

func (p *refreshProbe) run(npmSpecs, goSpecs string, updates bool) error {
	return RefreshServers(ServerRefreshRequest{
		Env:      p.e,
		NpmSpecs: npmSpecs,
		GoSpecs:  goSpecs,
		Updates:  updates,
		Stderr:   &p.out,
		Now:      func() time.Time { return p.now },
		Run: func(ctx context.Context, _ io.Writer, argv []string) error {
			p.ran = append(p.ran, argv)
			p.ctxs = append(p.ctxs, ctx)
			if p.fail[argv[len(argv)-1]] {
				return errors.New("probe: install refused")
			}
			return nil
		},
	})
}

// hasStamp reports whether a throttle file exists for this package.
func (p *refreshProbe) hasStamp(kind serverKind, spec string) bool {
	_, err := os.Stat(filepath.Join(p.stampDir, serverPkg{kind: kind, spec: spec}.stampName()))
	return err == nil
}

// An ABSENT server is installed synchronously, and the install carries NO deadline — there
// is no working copy to fall back to, so a bound here would trade a definite failure for a
// silent one (§3.5's note: ABSENT and STALE resolve differently).
func TestAnAbsentServerIsInstalledUnbounded(t *testing.T) {
	p := newRefreshProbe(t, nil)
	if err := p.run("pyright", "", true); err != nil {
		t.Fatalf("an absent server that installs cleanly must not report an error: %v", err)
	}
	if len(p.ran) != 1 {
		t.Fatalf("ran %v, want one install of pyright", p.ran)
	}
	if got := strings.Join(p.ran[0], " "); got != "npm install -g --prefer-online pyright" {
		t.Errorf("install argv = %q, want the bootstrap's own npm spelling", got)
	}
	if dl, ok := p.ctxs[0].Deadline(); ok {
		t.Errorf("the absent install carries a deadline (%v) — the stale rule's bound "+
			"applies to a package that HAS a working copy, not to one that does not", dl)
	}
	if !p.hasStamp(serverNpm, "pyright") {
		t.Error("a successful cold install left no stamp, so the next invocation would " +
			"treat it as never-refreshed")
	}
}

// An absent server that cannot be installed is REPORTED — the error names it — because the
// agent is about to try to connect to something that is not there.
func TestAnAbsentServerThatCannotBeInstalledIsReported(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.fail["pyright"] = true
	err := p.run("pyright", "", true)
	if err == nil {
		t.Fatal("an absent server that failed to install was swallowed — §3.5 requires it " +
			"reported, because there is no working copy behind it")
	}
	if !strings.Contains(err.Error(), "pyright") {
		t.Errorf("the error does not name the package: %v", err)
	}
	if !strings.Contains(p.out.String(), "pyright") {
		t.Errorf("nothing was said on stderr about the failed install:\n%s", p.out.String())
	}
}

// A STALE server is refreshed under a deadline, and a failure is SWALLOWED: the copy on disk
// is the fallback, so an offline registry costs the user a message and nothing else.
func TestAStaleServerIsRefreshedBoundedAndSwallowed(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "pyright")
	staleAt := p.stamp(t, serverNpm, "pyright", 2*ServerRefreshInterval*time.Second)
	p.fail["pyright"] = true

	if err := p.run("pyright", "", true); err != nil {
		t.Fatalf("a failed refresh of an INSTALLED server must not be an error: %v", err)
	}
	if len(p.ran) != 1 {
		t.Fatalf("ran %v, want one refresh", p.ran)
	}
	if _, ok := p.ctxs[0].Deadline(); !ok {
		t.Error("the stale refresh carries no deadline — a hung updater would hang the " +
			"command the user typed")
	}
	// MTIME, not existence. The stamp file is this cell's own fixture, so
	// `hasStamp` is vacuously true and a mutation that skipped the touch on failure kept
	// it green (measured). What must have happened is that the stamp MOVED to now: without
	// it an offline jail would spend the whole budget again on every single agent
	// invocation. That is _do_install's rule (`touch "$STAMP"` sits outside its success
	// branch) and it matters more here, because the cost is paid before an exec.
	if at := p.stampTime(t, serverNpm, "pyright"); !at.Equal(p.now) {
		t.Errorf("a FAILED refresh did not advance the stamp (%v, was %v) — one attempt "+
			"per interval is the throttle, and success is not its condition", at, staleAt)
	}
}

// A stamp inside the interval is the throttle, and it is per PACKAGE: two agents that both
// connect to pyright must not both pay for it.
func TestAFreshStampDoesNoWork(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "pyright")
	p.stamp(t, serverNpm, "pyright", 10*time.Second)
	if err := p.run("pyright", "", true); err != nil {
		t.Fatal(err)
	}
	if len(p.ran) != 0 {
		t.Errorf("a fresh stamp still ran %v", p.ran)
	}
}

// A PINNED declaration already IS the answer to "which version", so there is nothing for a
// refresh to resolve — the npm launcher's PINNED branch, applied to a server.
func TestAPinnedServerIsNeverRefreshed(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "pyright")
	if err := p.run("pyright@1.2.3", "", true); err != nil {
		t.Fatal(err)
	}
	if len(p.ran) != 0 {
		t.Errorf("a pinned server was refreshed anyway: %v", p.ran)
	}
	// `@latest` is a declaration to MOVE, not a pin, and reading it as one would freeze
	// every LSP recipe the tree ships (`golang.org/x/tools/gopls@latest`).
	p2 := newRefreshProbe(t, nil)
	p2.installedGo(t, "gopls")
	if err := p2.run("", "golang.org/x/tools/gopls@latest", true); err != nil {
		t.Fatal(err)
	}
	if len(p2.ran) != 1 {
		t.Errorf("@latest was read as a pin, so the shipped go LSP recipe would never " +
			"move: ran nothing")
	}
}

// agent_updates gates the STALE half and NOT the absent one — the launchers' own split
// (`_update_due` is the only consumer of UPDATES_ENABLED; the cold-install arm above it runs
// whatever the policy says). A policy that froze a cold install would leave the agent unable
// to connect to a server at all, which is not what "do not move it" means.
func TestAFrozenPolicyStopsRefreshesButNotColdInstalls(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "pyright") // installed and stale
	p.stamp(t, serverNpm, "pyright", 2*ServerRefreshInterval*time.Second)
	if err := p.run("pyright chrome-devtools-mcp", "", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.ran) != 1 {
		t.Fatalf("ran %v, want ONLY the absent chrome-devtools-mcp", p.ran)
	}
	if last := p.ran[0][len(p.ran[0])-1]; last != "chrome-devtools-mcp" {
		t.Errorf("installed %q under a frozen policy; the stale one must be skipped and "+
			"the absent one must not be", last)
	}
}

// §3.5's contention rule, both halves: an invocation that cannot take the install-prefix
// lock PROCEEDS WITHOUT UPDATING and SAYS SO — it must not wait and must not fail. And it
// leaves no stamp, because nothing was attempted.
func TestALockedPrefixProceedsWithoutUpdatingAndSaysSo(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "pyright")
	staleAt := p.stamp(t, serverNpm, "pyright", 2*ServerRefreshInterval*time.Second)

	// The lock a competing npm agent launcher would hold, at the path its template names.
	lock := filepath.Join(p.e.NpmPrefix, ".yolo-update.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := p.run("pyright", "", true); err != nil {
		t.Fatalf("a held lock must not be an error: %v", err)
	}
	if len(p.ran) != 0 {
		t.Errorf("refreshed %v while another update held the prefix lock", p.ran)
	}
	if !strings.Contains(p.out.String(), "another update holds") {
		t.Errorf("the held lock was silent; §3.5 requires it said:\n%s", p.out.String())
	}
	// The stamp file is this cell's own fixture, so EXISTENCE proves nothing — what must
	// not have happened is a TOUCH. A refreshed stamp here would throttle the retry for an
	// hour over a lock that is gone in a second.
	if at := p.stampTime(t, serverNpm, "pyright"); !at.Equal(staleAt) {
		t.Errorf("a refresh that never ran touched the stamp (%v, was %v)", at, staleAt)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("the competing holder's lock was removed: %v", err)
	}
}

// The lock is RELEASED, or the next hour's refresh finds it held by nobody.
func TestTheRefreshReleasesThePrefixLock(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "pyright")
	p.stamp(t, serverNpm, "pyright", 2*ServerRefreshInterval*time.Second)
	if err := p.run("pyright", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.e.NpmPrefix, ".yolo-update.lock")); err == nil {
		t.Error("the refresh kept the install-prefix lock, which freezes every later " +
			"update until the stale-lock age expires")
	}
}

// --- the scope -------------------------------------------------------------------------

// Only the servers YOLO INSTALLS are in scope. An `mcp_servers` entry whose argv is
// `npx -y <pkg>@latest` resolves on every spawn and is already current (§6.1's UNMANAGED
// tier); refreshing it would be inventing management the design declines.
func TestTheServerSetIgnoresTheMCPServerTable(t *testing.T) {
	e := NewEnv(map[string]string{
		"JAIL_HOME": t.TempDir(),
		"YOLO_MCP_SERVERS": `{"tavily":{"command":"npx","args":["-y","tavily-mcp@latest"]},` +
			`"local":{"command":"/workspace/mine.py"}}`,
		"YOLO_MCP_PRESETS": `["chrome-devtools"]`,
	})
	got := ServerRefreshSpecs(e)
	if strings.Contains(got.npm, "tavily") || strings.Contains(got.npm, "mine.py") {
		t.Errorf("the refresh set reached into mcp_servers (%q). Those entries are the "+
			"UNMANAGED tier — an `npx -y pkg@latest` argv is current every spawn", got.npm)
	}
	if !strings.Contains(got.npm, "chrome-devtools-mcp") {
		t.Errorf("the ENABLED preset's package is missing from the set (%q), so the one "+
			"thing that does freeze is the one thing not refreshed", got.npm)
	}
}

// The set is derived from BOTH halves of the LSP declaration: the env list is what THIS
// launch asked for, and the sentinel is what the LAST bootstrap installed. A launcher runs
// from a shell, not from boot, so the sentinel is what a re-render with no LSP vars has.
func TestTheServerSetReadsTheEnvListAndTheSentinel(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:typescript-language-server\ngo:github.com/isaacphi/mcp-language-server\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{
		"JAIL_HOME":            home,
		"YOLO_LSP_NPM_INSTALL": "pyright",
	})
	got := ServerRefreshSpecs(e)
	for _, want := range []string{"pyright", "typescript-language-server"} {
		if !strings.Contains(got.npm, want) {
			t.Errorf("npm set %q is missing %q", got.npm, want)
		}
	}
	if !strings.Contains(got.gomods, "github.com/isaacphi/mcp-language-server") {
		t.Errorf("go set %q is missing the sentinel's go entry", got.gomods)
	}
	// Declared twice is installed once.
	if n := strings.Count(got.npm, "pyright"); n != 1 {
		t.Errorf("npm set %q names pyright %d times", got.npm, n)
	}
}
