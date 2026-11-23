package entrypoint

// serverrefresh_test.go drives the transitive MCP server refresh (program-delivery.md §3.5,
// OQ-PD12a).
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

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// --- the call site ---------------------------------------------------------------------

// writePackWithServerAgent stages a one-pack YOLO_PACK_ROOT declaring `program <bin>` with
// the given `via`, and an `mcp` server `acme` whose npm program is `acme-mcp-pkg`: a launch whose
// composed table holds `acme` (acmeServerWire) has one yolo-installed server, and one whose table
// does not has none.
//
// writePackWithProgram (launchercollision_test.go) hardcodes `via: npm`, which reaches ONE of
// the two launcher templates. That is the gap this exists to close: deleting the refresh call
// from the NATIVE template alone left every cell in this file green (measured), which is the
// unpinned-call-site shape AGENTS.md names.
func writePackWithServerAgent(t *testing.T, via, bin string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "srvpack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := `{"kind":"program","bin":"` + bin + `","via":"npm","package":"` + bin + `-pkg"}`
	if via == "native" {
		entry = `{"kind":"program","bin":"` + bin +
			`","via":"installer","url":"https://example.invalid/i.sh"}`
	}
	server := `{"kind":"program","bin":"acme-mcp","via":"npm","package":"acme-mcp-pkg"},` +
		`{"kind":"mcp","name":"acme","bin":"acme-mcp","config":{"command":"acme-mcp"}}`
	manifest := `{"name":"srvpack","contributes":[` + entry + `,` + server + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// acmeServerWire is the composed YOLO_MCP_SERVERS holding writePackWithServerAgent's server.
const acmeServerWire = `{"acme":{"command":"acme-mcp"}}`

// TestGeneratedLauncherCallsTheServerRefresh is the load-bearing cell. Delete the refresh
// block from EITHER template, or the splice that fills it, and the servers stop moving with
// nothing else failing.
//
// BOTH templates, in one table: they are separately-written strings and a fix applied to one
// is not a fix.
func TestGeneratedLauncherCallsTheServerRefresh(t *testing.T) {
	for _, via := range []string{"npm", "native"} {
		t.Run(via, func(t *testing.T) {
			const bin = "yolo-not-a-real-agent"
			home := t.TempDir()
			e := NewEnv(map[string]string{
				"JAIL_HOME":        home,
				"YOLO_PACK_ROOT":   writePackWithServerAgent(t, via, bin),
				"YOLO_MCP_SERVERS": acmeServerWire,
				// The deleted LSP recipe's two lists, as an older host launcher would still
				// send them. Nothing may bake them: yolo installs no language server now
				// (docs/reference/mcp-configuration.md#oq-lsp1).
				"YOLO_LSP_GO_INSTALL":  "golang.org/x/tools/gopls@latest",
				"YOLO_LSP_NPM_INSTALL": "pyright",
			})
			if err := GenerateAgentLaunchers(e); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(e.LaunchDir(), bin))
			if err != nil {
				t.Fatal(err)
			}
			got := string(body)

			for _, want := range []string{
				// The call itself, and the exact argv the CLI parses. A rename on either
				// side leaves the launcher invoking a subcommand `yolo internal` refuses.
				"yolo internal refresh-servers",
				`--home="$HOME"`,
				`--npm="$SERVERS_NPM"`,
				`--updates="$UPDATES_ENABLED"`,
				// The BAKED set. Reading these from the environment instead is the
				// macos-user `env -i` defect capturesDir and receiptsFile are baked to
				// avoid, so the values have to be IN the script.
				"\nSERVERS_ENABLED=1\n",
				"acme-mcp-pkg",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the %s launcher is missing %q — the transitive refresh is "+
						"not wired, so a yolo-installed MCP server only ever moves "+
						"when the bootstrap reinstalls it:\n%s", via, want, got)
				}
			}
			for _, gone := range []string{"pyright", "gopls", "SERVERS_GO", "--go="} {
				if strings.Contains(got, gone) {
					t.Errorf("the %s launcher still carries %q — a language server reached "+
						"the refresh set after the LSP recipe's deletion:\n%s", via, gone, got)
				}
			}
			// Ordering is the design's ONE hard constraint (§3.5): the refresh must
			// complete before the exec, because the agent spawns its servers itself.
			call := strings.Index(got, "if [ \"$SERVERS_ENABLED\" = \"1\" ]")
			// The exec is matched by its HEAD, not by the whole line: since DP-B44 the
			// argv is ${YOLO_ARGV[@]…} rather than "$@", and pinning the tail would make
			// this cell fail for a reason that has nothing to do with the refresh order.
			execAt := strings.LastIndex(got, `exec "$REAL_BIN"`)
			if call < 0 || execAt < 0 || call > execAt {
				t.Errorf("the %s launcher does not order the refresh before the exec "+
					"(call=%d exec=%d) — a half-updated server set at connect time is "+
					"worse than a stale one", via, call, execAt)
			}
		})
	}
}

// TestALauncherWithNoServersCarriesNoRefresh is the other arm of the same call site, and it
// is what keeps the cell above from passing against a generator that bakes the call
// unconditionally. A jail with no yolo-installed server must pay no process spawn.
func TestALauncherWithNoServersCarriesNoRefresh(t *testing.T) {
	for _, via := range []string{"npm", "native"} {
		t.Run(via, func(t *testing.T) {
			const bin = "yolo-not-a-real-agent"
			home := t.TempDir()
			e := NewEnv(map[string]string{
				"JAIL_HOME":      home,
				"YOLO_PACK_ROOT": writePackWithServerAgent(t, via, bin),
			})
			if err := GenerateAgentLaunchers(e); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(e.LaunchDir(), bin))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "\nSERVERS_ENABLED=0\n") {
				t.Errorf("the %s launcher in a jail with no yolo-installed servers did "+
					"not bake SERVERS_ENABLED=0, so every agent invocation spawns a "+
					"subprocess to discover there is nothing to do:\n%s", via, body)
			}
		})
	}
}

// TestServerRefreshSharesTheLaunchersThrottle is the cross-language pin. The timeout is
// spelled in THREE places — this Go file and the two bash templates —
// and §3.5's "the first invocation refreshes it, the second sees a fresh stamp" is false for
// one of the two the moment they disagree.
func TestServerRefreshSharesTheLaunchersThrottle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		goValue int
		bashVar string
	}{
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
	e         *Env
	markerDir string
	// id is the jail-start record the probe wrote; other starts' markers are under otherID.
	id string
	// ran records every install argv, in order.
	ran [][]string
	// fail names the specs whose install must fail.
	fail map[string]bool
	// out collects everything the refresh said, so a cell can assert it SAID it.
	out  strings.Builder
	now  time.Time
	ctxs []context.Context
	// latest is what the registry answers for every package's version, "" or viewErr for a
	// registry that did not answer; "9.9.9" (newer than installed's 1.0.0) unless a cell says
	// otherwise. viewed records each package asked about.
	latest  string
	viewErr error
	viewed  []string
	// hatch is the user's YOLO_USE_INSTALLED for the run.
	hatch string
}

func newRefreshProbe(t *testing.T, vars map[string]string) *refreshProbe {
	t.Helper()
	home := t.TempDir()
	all := map[string]string{"JAIL_HOME": home}
	for k, v := range vars {
		all[k] = v
	}
	p := &refreshProbe{
		e:      NewEnv(all),
		fail:   map[string]bool{},
		now:    time.Unix(1_700_000_000, 0),
		latest: "9.9.9",
	}
	p.markerDir = filepath.Join(home, filepath.FromSlash(LaunchChecksDirRel), "servers")
	p.id = "start-1"
	p.writeStart(t, p.id)
	// A real prefix, so the presence stats have somewhere to look.
	if err := os.MkdirAll(filepath.Join(p.e.NpmPrefix, "lib", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
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

// writeStart writes the jail-start record the way the boot does (the boot is its only writer).
func (p *refreshProbe) writeStart(t *testing.T, id string) {
	t.Helper()
	path := filepath.Join(p.e.Home, filepath.FromSlash(JailStartRel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.id = id
}

// markerFile is the per-package check marker for one start: servers/<package>.<id>. The
// package part is spelled here on purpose, so the production name is pinned, not shared.
func (p *refreshProbe) markerFile(kind serverKind, spec, id string) string {
	flat := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, spec)
	return filepath.Join(p.markerDir, string(kind)+"-"+flat+"."+id)
}

func (p *refreshProbe) touchMarker(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(p.markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// otherStart seeds a marker an EARLIER jail start left: it must not silence this start.
func (p *refreshProbe) otherStart(t *testing.T, kind serverKind, spec string) {
	t.Helper()
	p.touchMarker(t, p.markerFile(kind, spec, "start-0"))
}

// checked seeds this start's marker: the package has had its check since the jail started.
func (p *refreshProbe) checked(t *testing.T, kind serverKind, spec string) {
	t.Helper()
	p.touchMarker(t, p.markerFile(kind, spec, p.id))
}

func (p *refreshProbe) run(npmSpecs string, updates bool) error {
	return RefreshServers(ServerRefreshRequest{
		Env:      p.e,
		NpmSpecs: npmSpecs,
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
		View: func(_ context.Context, pkg string) (string, error) {
			p.viewed = append(p.viewed, pkg)
			return p.latest, p.viewErr
		},
		Bin: "claude",
		Getenv: func(k string) string {
			if k == "YOLO_USE_INSTALLED" {
				return p.hatch
			}
			return ""
		},
	})
}

// hasStamp reports whether THIS start's marker exists for the package.
func (p *refreshProbe) hasStamp(kind serverKind, spec string) bool {
	_, err := os.Stat(p.markerFile(kind, spec, p.id))
	return err == nil
}

// An ABSENT server is installed synchronously, and the install carries NO deadline — there
// is no working copy to fall back to, so a bound here would trade a definite failure for a
// silent one (§3.5's note: ABSENT and STALE resolve differently).
func TestAnAbsentServerIsInstalledUnbounded(t *testing.T) {
	p := newRefreshProbe(t, nil)
	if err := p.run("chrome-devtools-mcp", true); err != nil {
		t.Fatalf("an absent server that installs cleanly must not report an error: %v", err)
	}
	if len(p.ran) != 1 {
		t.Fatalf("ran %v, want one install of chrome-devtools-mcp", p.ran)
	}
	if got := strings.Join(p.ran[0], " "); got != "npm install -g --prefer-online chrome-devtools-mcp" {
		t.Errorf("install argv = %q, want the bootstrap's own npm spelling", got)
	}
	if dl, ok := p.ctxs[0].Deadline(); ok {
		t.Errorf("the absent install carries a deadline (%v) — the stale rule's bound "+
			"applies to a package that HAS a working copy, not to one that does not", dl)
	}
	if !p.hasStamp(serverNpm, "chrome-devtools-mcp") {
		t.Error("a successful cold install left no stamp, so the next invocation would " +
			"treat it as never-refreshed")
	}
}

// An absent server that cannot be installed is REPORTED — the error names it — because the
// agent is about to try to connect to something that is not there.
func TestAnAbsentServerThatCannotBeInstalledIsReported(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.fail["chrome-devtools-mcp"] = true
	err := p.run("chrome-devtools-mcp", true)
	if err == nil {
		t.Fatal("an absent server that failed to install was swallowed — §3.5 requires it " +
			"reported, because there is no working copy behind it")
	}
	if !strings.Contains(err.Error(), "chrome-devtools-mcp") {
		t.Errorf("the error does not name the package: %v", err)
	}
	if !strings.Contains(p.out.String(), "chrome-devtools-mcp") {
		t.Errorf("nothing was said on stderr about the failed install:\n%s", p.out.String())
	}
}

// A STALE server whose registry did not answer is left as it is, bounded and swallowed: nothing
// says a newer version exists (PD-NF1), so an offline registry costs the user a message and
// nothing else. The stamp moves, so an offline jail does not ask again on every invocation.
func TestAStaleServerIsRefreshedBoundedAndSwallowed(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")
	p.viewErr = errors.New("ENOTFOUND")

	if err := p.run("chrome-devtools-mcp", true); err != nil {
		t.Fatalf("a registry that did not answer is not an error: %v", err)
	}
	if len(p.viewed) != 1 || p.viewed[0] != "chrome-devtools-mcp" || len(p.ran) != 0 {
		t.Fatalf("viewed %v and ran %v, want one version query and no install", p.viewed, p.ran)
	}
	if !strings.Contains(p.out.String(), "could not ask the npm registry for a newer version — keeping the installed 1.0.0") {
		t.Errorf("the could-not-check line is missing:\n%s", p.out.String())
	}
	// The marker is KEPT (LU-D2): an offline jail does not spend the budget again until the
	// next start.
	if !p.hasStamp(serverNpm, "chrome-devtools-mcp") {
		t.Error("a refresh that could not check left no marker, so it would ask again this start")
	}

	// The registry answers the installed version: nothing is newer, nothing is installed.
	p.writeStart(t, "start-2")
	p.viewErr, p.latest = nil, "1.0.0"
	if err := p.run("chrome-devtools-mcp", true); err != nil || len(p.ran) != 0 {
		t.Fatalf("a current server was installed (%v) or failed (%v)", p.ran, err)
	}
	if !p.hasStamp(serverNpm, "chrome-devtools-mcp") {
		t.Errorf("a server found current left no marker for this start")
	}
}

// A STALE server whose NEWER version was found and did not install REFUSES (PD-NF1): the refusal
// is said in the launchers' shape, naming the versions and the hatch, the error is the typed
// refusal the CLI maps to UpdateRefusedStatus, and the stamp does not move, so the next invocation
// retries. The install is bounded. The hatch, naming the server's package, keeps the installed
// version instead; one naming something else does not.
func TestAStaleServerWhoseNewerVersionFailsRefuses(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")
	p.fail["chrome-devtools-mcp"] = true

	err := p.run("chrome-devtools-mcp", true)
	var refusal *ServerUpdateRefusal
	if !errors.As(err, &refusal) || len(refusal.Specs) != 1 || refusal.Specs[0] != "chrome-devtools-mcp" {
		t.Fatalf("a newer server that did not install must be refused, got %v", err)
	}
	if len(p.ran) != 1 {
		t.Fatalf("ran %v, want one install", p.ran)
	}
	if _, ok := p.ctxs[0].Deadline(); !ok {
		t.Error("the stale install carries no deadline — a hung updater would hang the command the user typed")
	}
	for _, want := range []string{
		"yolo: ERROR: npm server chrome-devtools-mcp 1.0.0 → 9.9.9: installing the newer version failed\n",
		"  npm install -g --prefer-online chrome-devtools-mcp: probe: install refused (its output is above).\n",
		"\nRepair:\n", "then run claude again", "    claude\n",
		"\nStart anyway:\n", useInstalledLines("chrome-devtools-mcp", "claude", "1.0.0"),
	} {
		if !strings.Contains(p.out.String(), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, p.out.String())
		}
	}
	if p.hasStamp(serverNpm, "chrome-devtools-mcp") {
		t.Errorf("a refused server kept its marker: the next invocation would not retry it")
	}

	p.hatch = "other"
	if err := p.run("chrome-devtools-mcp", true); !errors.As(err, &refusal) {
		t.Errorf("a hatch naming another program must not cover this server: %v", err)
	}
	for _, hatch := range []string{"chrome-devtools-mcp", "1"} {
		p.hatch = hatch
		p.out.Reset()
		if err := p.run("chrome-devtools-mcp", true); err != nil {
			t.Errorf("YOLO_USE_INSTALLED=%s must keep the installed server: %v", hatch, err)
		}
		if !strings.Contains(p.out.String(), "keeping the installed version, as YOLO_USE_INSTALLED asks") {
			t.Errorf("YOLO_USE_INSTALLED=%s: the kept server is not said:\n%s", hatch, p.out.String())
		}
		p.writeStart(t, p.id+"x")
	}
}

// A budget that runs out under the install of a NEWER version is that install failing: refused. A
// Ctrl-C there (the caller's cancellation) is the user's own hatch (PD-NF4): the installed version
// is kept, and nothing is refused.
func TestAStaleServerWhoseBudgetRunsOutAfterANewerWasFoundRefuses(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "ctrl-c"}[interrupted], func(t *testing.T) {
			p := newRefreshProbe(t, nil)
			p.installed(t, "chrome-devtools-mcp")
			p.otherStart(t, serverNpm, "chrome-devtools-mcp")
			// A parent DEADLINE stands in for the budget, which the child context inherits.
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := RefreshServers(ServerRefreshRequest{Context: ctx, Env: p.e, NpmSpecs: "chrome-devtools-mcp", Updates: true,
				Stderr: &p.out, Now: func() time.Time { return p.now }, Bin: "claude", Getenv: func(string) string { return "" },
				View: func(context.Context, string) (string, error) { return "9.9.9", nil },
				Run: func(ctx context.Context, _ io.Writer, _ []string) error {
					if interrupted {
						cancel()
					}
					<-ctx.Done()
					return ctx.Err()
				}})
			var refusal *ServerUpdateRefusal
			if interrupted {
				if err != nil || !strings.Contains(p.out.String(), "its refresh was interrupted — keeping the installed version") {
					t.Errorf("a Ctrl-C at the install must keep the installed server, got %v:\n%s", err, p.out.String())
				}
				return
			}
			if !errors.As(err, &refusal) {
				t.Fatalf("an install the budget ended after a newer version was found must refuse, got %v", err)
			}
			if !strings.Contains(p.out.String(), "ran out of the 60s server refresh budget") {
				t.Errorf("the refusal must say the budget ran out:\n%s", p.out.String())
			}
		})
	}
}

func TestFreshServerRefreshFailureAndCancellationAreFatal(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")
	p.fail["chrome-devtools-mcp"] = true
	if err := RefreshServers(ServerRefreshRequest{Env: p.e, NpmSpecs: "chrome-devtools-mcp", Updates: true,
		Fresh: true, Stderr: &p.out, Now: func() time.Time { return p.now },
		Run: func(context.Context, io.Writer, []string) error { return errors.New("registry unavailable") }}); err == nil {
		t.Fatal("explicit fresh request accepted the historical installed server after its refresh failed")
	}
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RefreshServers(ServerRefreshRequest{Context: ctx, Fresh: true, Env: p.e,
			NpmSpecs: "chrome-devtools-mcp", Updates: true, Stderr: io.Discard,
			Now: func() time.Time { return p.now }, Run: func(ctx context.Context, _ io.Writer, _ []string) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("fresh server refresh did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "fresh server preparation failed") {
			t.Fatalf("cancelled fresh server refresh = %v, want a strict refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled fresh server refresh remained blocked")
	}
}

// serverRefreshDue is the whole rule (LU-D1): a package is due when this jail start has not
// checked it, and not otherwise. TestAServerChecksOncePerJailStart drives it through
// RefreshServers, so deleting the call site fails it.
func TestAServerChecksOncePerJailStart(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	for i := 0; i < 2; i++ {
		if err := p.run("chrome-devtools-mcp", true); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.viewed) != 1 || len(p.ran) != 1 {
		t.Fatalf("two invocations of one start asked %v and installed %v, want one of each", p.viewed, p.ran)
	}
	p.writeStart(t, "start-2")
	if err := p.run("chrome-devtools-mcp", true); err != nil {
		t.Fatal(err)
	}
	if len(p.viewed) != 2 {
		t.Errorf("a new jail start did not check again: asked %v", p.viewed)
	}
	// The marker is keyed by package: another package of the same start is still due.
	p.installed(t, "other-mcp")
	if err := p.run("chrome-devtools-mcp other-mcp", true); err != nil {
		t.Fatal(err)
	}
	if len(p.viewed) != 3 || p.viewed[2] != "other-mcp" {
		t.Errorf("a checked package silenced another's check: asked %v", p.viewed)
	}
	if !p.hasStamp(serverNpm, "other-mcp") {
		t.Error("the check left no servers/<package>.<id> marker")
	}
}

// A marker of an earlier start never silences this one, and neither does an old-style stamp.
func TestAnEarlierStartsMarkerDoesNotSilenceThisStart(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")
	old := filepath.Join(p.e.Home, ".cache", "yolo-agent-stamps", "servers", "npm-chrome-devtools-mcp.stamp")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.run("chrome-devtools-mcp", true); err != nil {
		t.Fatal(err)
	}
	if len(p.viewed) != 1 {
		t.Errorf("an old marker or stamp silenced the check: asked %v", p.viewed)
	}
}

// No record (an older boot, a failed write): check nothing, run the installed version, and say
// the next step. The refresh never writes the record: the boot is its only writer (LU-D6).
func TestNoJailStartRecordChecksNothingAndSaysSo(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	if err := os.Remove(filepath.Join(p.e.Home, filepath.FromSlash(JailStartRel))); err != nil {
		t.Fatal(err)
	}
	if err := p.run("chrome-devtools-mcp", true); err != nil {
		t.Fatal(err)
	}
	if len(p.viewed) != 0 || len(p.ran) != 0 {
		t.Errorf("checked without a record: asked %v installed %v", p.viewed, p.ran)
	}
	for _, want := range []string{"no start record", "Relaunch the jail"} {
		if !strings.Contains(p.out.String(), want) {
			t.Errorf("the missing-record line lacks %q:\n%s", want, p.out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(p.e.Home, filepath.FromSlash(JailStartRel))); err == nil {
		t.Error("the refresh wrote the jail-start record; the boot is its only writer")
	}
}

// The could-not-ask line names its next step (LU-D2's disclosure).
func TestAnUnreachableServerRegistryNamesTheNextStep(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.viewErr = errors.New("ENOTFOUND")
	if err := p.run("chrome-devtools-mcp", true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"asks again the next time this jail starts", "yolo pack update"} {
		if !strings.Contains(p.out.String(), want) {
			t.Errorf("the could-not-ask line lacks %q:\n%s", want, p.out.String())
		}
	}
}

// Every outcome but a refusal keeps the marker: current, offline and the user's hatch above, and
// a Ctrl-C here. A refusal (covered above) releases it.
func TestAServerInterruptedByCtrlCKeepsItsMarker(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := RefreshServers(ServerRefreshRequest{Context: ctx, Env: p.e, NpmSpecs: "chrome-devtools-mcp", Updates: true,
		Stderr: &p.out, Now: func() time.Time { return p.now }, Bin: "claude", Getenv: func(string) string { return "" },
		View: func(context.Context, string) (string, error) { return "9.9.9", nil },
		Run: func(ctx context.Context, _ io.Writer, _ []string) error {
			cancel()
			return ctx.Err()
		}})
	if err != nil {
		t.Fatalf("a Ctrl-C is the user's hatch, not a refusal: %v", err)
	}
	if !p.hasStamp(serverNpm, "chrome-devtools-mcp") {
		t.Error("a Ctrl-C at the install released the marker")
	}
}

// A PINNED declaration already IS the answer to "which version", so there is nothing for a
// refresh to resolve — the npm launcher's PINNED branch, applied to a server.
func TestAPinnedServerIsNeverRefreshed(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	if err := p.run("chrome-devtools-mcp@1.2.3", true); err != nil {
		t.Fatal(err)
	}
	if len(p.ran) != 0 {
		t.Errorf("a pinned server was refreshed anyway: %v", p.ran)
	}
	// `@latest` is a declaration to MOVE, not a pin, and reading it as one would freeze
	// every server declared that way.
	p2 := newRefreshProbe(t, nil)
	p2.installed(t, "chrome-devtools-mcp")
	if err := p2.run("chrome-devtools-mcp@latest", true); err != nil {
		t.Fatal(err)
	}
	if len(p2.ran) != 1 {
		t.Errorf("@latest was read as a pin, so a server declared that way would never " +
			"move: ran nothing")
	}
}

// agent_updates gates the STALE half and NOT the absent one — the launchers' own split
// (`_update_due` is the only consumer of UPDATES_ENABLED; the cold-install arm above it runs
// whatever the policy says). A policy that froze a cold install would leave the agent unable
// to connect to a server at all, which is not what "do not move it" means.
func TestAFrozenPolicyStopsRefreshesButNotColdInstalls(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp") // installed and stale
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")
	const absent = "@modelcontextprotocol/server-sequential-thinking"
	if err := p.run("chrome-devtools-mcp "+absent, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.ran) != 1 {
		t.Fatalf("ran %v, want ONLY the absent %s", p.ran, absent)
	}
	if last := p.ran[0][len(p.ran[0])-1]; last != absent {
		t.Errorf("installed %q under a frozen policy; the stale one must be skipped and "+
			"the absent one must not be", last)
	}
}

// §3.5's contention rule, both halves: an invocation that cannot take the install-prefix
// lock PROCEEDS WITHOUT UPDATING and SAYS SO — it must not wait and must not fail. And it
// leaves no stamp, because nothing was attempted.
func TestALockedPrefixProceedsWithoutUpdatingAndSaysSo(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")

	// The lock a competing npm agent launcher would hold, at the path its template names.
	lock := filepath.Join(p.e.NpmPrefix, ".yolo-update.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := p.run("chrome-devtools-mcp", true); err != nil {
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
	if p.hasStamp(serverNpm, "chrome-devtools-mcp") {
		t.Errorf("a refresh that never ran left a marker")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("the competing holder's lock was removed: %v", err)
	}
}

// The lock is RELEASED, or the next hour's refresh finds it held by nobody.
func TestTheRefreshReleasesThePrefixLock(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.otherStart(t, serverNpm, "chrome-devtools-mcp")
	if err := p.run("chrome-devtools-mcp", true); err != nil {
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
	e := chromeServerEnv(t, `{"tavily":{"command":"npx","args":["-y","tavily-mcp@latest"]},`+
		`"local":{"command":"/workspace/mine.py"}}`, true)
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	got := ServerRefreshSpecs(e, packs)
	if strings.Contains(got.npm, "tavily") || strings.Contains(got.npm, "mine.py") {
		t.Errorf("the refresh set reached into mcp_servers (%q). Those entries are the "+
			"UNMANAGED tier — an `npx -y pkg@latest` argv is current every spawn", got.npm)
	}
	if !strings.Contains(got.npm, "chrome-devtools-mcp") {
		t.Errorf("the selected pack server's program is missing from the set (%q), so the one "+
			"thing that does freeze is the one thing not refreshed", got.npm)
	}
}

// LANGUAGE SERVERS ARE OUT OF SCOPE, because yolo no longer installs any: the recipe table
// that did, its YOLO_LSP_*_INSTALL lists and the ~/.yolo-installed-lsps sentinel are deleted
// (docs/reference/mcp-configuration.md#oq-lsp1). Both retired inputs are present here, as an
// upgraded jail has them, and neither may put a package into the set — refreshing a server
// nothing declares would reinstall exactly what the catalog now reports as an orphan.
func TestTheServerSetIgnoresTheRetiredLSPInputs(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:typescript-language-server\ngo:github.com/isaacphi/mcp-language-server\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{
		"JAIL_HOME":            home,
		"YOLO_LSP_NPM_INSTALL": "pyright",
		"YOLO_LSP_GO_INSTALL":  "golang.org/x/tools/gopls@latest",
	})
	if got := ServerRefreshSpecs(e, nil); !got.empty() {
		t.Errorf("the refresh set carries %q with no pack server held; a retired LSP "+
			"input reached it", got.npm)
	}
}

// --- the splice ------------------------------------------------------------------------

// TestTheRefreshCallQuotesTheServerLists is the BEHAVIOURAL half of the splice contract for
// the newest sentinel, and it exists because `bash -n` cannot answer the question.
// TestLauncherTemplatesParseWithHostileValues feeds the hostile value through this list
// too, but a raw `SERVERS_NPM=$(touch witness)` is perfectly valid bash — measured: splicing
// the list raw left that cell GREEN. Only running the launcher and reading the argv the
// subcommand actually received settles it.
//
// It is also the strongest form of the call-site pin: a launcher that never invoked the
// refresh would log no argv at all.
func TestTheRefreshCallQuotesTheServerLists(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	yoloLog := filepath.Join(home, "yolo.log")
	argvLogger(t, fakeBin, "yolo", yoloLog, "")
	argvLogger(t, fakeBin, "npm", filepath.Join(home, "npm.log"),
		`if [ "${1:-}" = install ]; then
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/sh\necho LAUNCHED\n' > "$NPM_CONFIG_PREFIX/bin/tool"
    chmod +x "$NPM_CONFIG_PREFIX/bin/tool"
fi`)

	// A LIST whose first element carries the payload, so the cell measures both halves at
	// once: the value must be data, and the list must still arrive as ONE argument (the
	// Go side splits it, not the shell).
	npmList := hostileValue("-srvnpm") + " second-pkg"
	body := npmAgentLauncher("probe",
		&packdecl.Install{Kind: "npm", Bin: "tool", Package: "tool"},
		filepath.Join(home, "stamps"),
		filepath.Join(home, "ws", ".yolo", "receipts.jsonl"), true,
		launcherServers{npm: npmList}, nil)

	out, rc := runLauncher(t, home, "tool", body, fakeBin)
	if rc != 0 {
		t.Errorf("launcher failed (rc=%d) — a hostile server list must be data, not code:\n%s",
			rc, out)
	}
	assertNoWitness(t, home, "-srvnpm", "the refresh call's --npm list")

	log := logLines(t, yoloLog)
	if len(log) == 0 {
		t.Fatalf("the launcher never invoked `yolo internal refresh-servers` — the "+
			"transitive refresh is not called at all:\n%s", out)
	}
	for _, want := range []string{
		"internal", "refresh-servers",
		"--npm=" + npmList,
	} {
		if !hasExactArg(log, want) {
			t.Errorf("the refresh subcommand did not receive %q as one intact argument.\n"+
				"got argv:\n%s", want, strings.Join(log, "\n"))
		}
	}
	// And the agent still ran: the refresh is a step before the exec, never instead of it.
	if !strings.Contains(out, "LAUNCHED") {
		t.Errorf("the launcher did not exec the agent after refreshing its servers:\n%s", out)
	}
}

func TestFreshServerRefreshBypassesPolicyAndEveryAttemptStampButKeepsPins(t *testing.T) {
	p := newRefreshProbe(t, nil)
	p.installed(t, "chrome-devtools-mcp")
	p.checked(t, serverNpm, "chrome-devtools-mcp")
	attempts := 0
	req := ServerRefreshRequest{Env: p.e, Fresh: true, Updates: false, NpmSpecs: "chrome-devtools-mcp",
		Stderr: &p.out, Now: func() time.Time { return p.now },
		Run: func(context.Context, io.Writer, []string) error { attempts++; return errors.New("offline") }}
	for i := 0; i < 2; i++ {
		if err := RefreshServers(req); err == nil {
			t.Fatal("fresh failed attempt was treated as success")
		}
	}
	if attempts != 2 {
		t.Fatalf("fresh retries = %d; want two despite automatic off and recent failed stamp", attempts)
	}
	req.NpmSpecs = "chrome-devtools-mcp@1.2.3"
	if err := RefreshServers(req); err != nil || attempts != 2 {
		t.Fatalf("explicit pin refreshed: attempts=%d err=%v", attempts, err)
	}
}
