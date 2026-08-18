package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// npmlauncher_test.go pins the npm `program` launcher's VERSION handling.
//
// The defect it exists for: the template appended a literal `@latest` to the declared
// package string, so `foo@1.2.3` was installed as `foo@1.2.3@latest` — npm resolves
// nothing for that, and a version was therefore inexpressible rather than merely
// unpinned (docs/design/trust-paths.md §1, top row).
//
// Two halves, deliberately separate: a table over the PARSER (npm's scoped-package rule is
// the whole subtlety), and behavioural runs of the generated launcher against a fake `npm`
// that logs its argv. The second half is what makes the "pinned packages do not poll the
// registry" claim measurable instead of a comment — a text assertion on the template would
// pass just as happily if the poll ran anyway.

func TestSplitNpmSpec(t *testing.T) {
	cases := []struct {
		spec        string
		wantName    string
		wantVersion string
		wantInstall string
	}{
		// Unversioned: both spellings, scoped and bare. These are what every shipped pack
		// declares today and they must keep resolving to latest, hourly poll included.
		{"name", "name", "", "name@latest"},
		{"@scope/name", "@scope/name", "", "@scope/name@latest"},
		// Versioned. The scoped form is the one the old code could not represent at all.
		{"name@1.2.3", "name", "1.2.3", "name@1.2.3"},
		{"@scope/name@1.2.3", "@scope/name", "1.2.3", "@scope/name@1.2.3"},
		// A selector is not always an exact version: npm takes a dist-tag or a range in
		// the same position, and both are legitimate declarations.
		{"name@next", "name", "next", "name@next"},
		{"name@^1.0.0", "name", "^1.0.0", "name@^1.0.0"},
		{"@scope/name@next", "@scope/name", "next", "@scope/name@next"},
		{"name@>=1.0.0 <2.0.0", "name", ">=1.0.0 <2.0.0", "name@>=1.0.0 <2.0.0"},
		// An alias/URL selector keeps its own @ — we split once and pass the rest through.
		{"name@npm:other@1.2.3", "name", "npm:other@1.2.3", "name@npm:other@1.2.3"},
		// Degenerate spellings. A trailing @ is npm-invalid, so read it as "no version".
		{"name@", "name", "", "name@latest"},
		{"", "", "", "@latest"},
		{"  name@1.2.3  ", "name", "1.2.3", "name@1.2.3"},
		// The real shipped packages, so the table says what production looks like.
		{"@anthropic-ai/claude-code", "@anthropic-ai/claude-code", "", "@anthropic-ai/claude-code@latest"},
		{"opencode-ai", "opencode-ai", "", "opencode-ai@latest"},
	}
	for _, tc := range cases {
		name, version := splitNpmSpec(tc.spec)
		if name != tc.wantName || version != tc.wantVersion {
			t.Errorf("splitNpmSpec(%q) = (%q, %q), want (%q, %q)",
				tc.spec, name, version, tc.wantName, tc.wantVersion)
		}
		if got := npmInstallSpec(name, version); got != tc.wantInstall {
			t.Errorf("npmInstallSpec for %q = %q, want %q", tc.spec, got, tc.wantInstall)
		}
		if got, want := npmSpecIsPinned(version), tc.wantVersion != ""; got != want {
			t.Errorf("npmSpecIsPinned for %q = %v, want %v", tc.spec, got, want)
		}
	}
}

// TestNpmLauncherBodyCarriesNameAndSpecSeparately pins the structural half: the rendered
// launcher must hold the NAME and the INSTALL SPEC as two different variables. Collapsing
// them is exactly the old bug, and it is invisible from the outside until npm is called.
func TestNpmLauncherBodyCarriesNameAndSpecSeparately(t *testing.T) {
	cases := []struct{ pkg, wantPKG, wantSPEC, wantPINNED string }{
		{"foo", "foo", "foo@latest", "0"},
		{"foo@1.2.3", "foo", "foo@1.2.3", "1"},
		{"@scope/foo", "@scope/foo", "@scope/foo@latest", "0"},
		{"@scope/foo@2.0.0", "@scope/foo", "@scope/foo@2.0.0", "1"},
	}
	for _, tc := range cases {
		body := npmAgentLauncher(&packdecl.Install{Kind: "npm", Bin: "foo", Package: tc.pkg}, "/stamps")
		for _, want := range []string{
			`PKG="` + tc.wantPKG + `"`,
			`SPEC="` + tc.wantSPEC + `"`,
			`PINNED="` + tc.wantPINNED + `"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("package %q: launcher missing %s\n%s", tc.pkg, want, body)
			}
		}
		// The literal that caused the defect must not survive anywhere in a versioned
		// launcher: `$PKG@latest` is what turned foo@1.2.3 into foo@1.2.3@latest.
		if strings.Contains(body, "$PKG@latest") {
			t.Errorf("package %q: launcher still appends @latest to the package string\n%s", tc.pkg, body)
		}
	}
	// The package-manager launcher is the second site that hardcoded it.
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.LauncherDir(), "pnpm"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "$PKG@latest") {
		t.Errorf("pnpm launcher still hardcodes @latest:\n%s", body)
	}
	// ...and its behaviour is unchanged: pnpm carries no version, so it still installs
	// pnpm@latest byte-for-byte.
	if !strings.Contains(string(body), `SPEC="pnpm@latest"`) {
		t.Errorf("pnpm must still resolve to pnpm@latest:\n%s", body)
	}
}

// --- behavioural half -------------------------------------------------------------

// npmProbe is one temp jail-home wired with a fake `npm` and a fake `jq`, so the generated
// launcher can be RUN and its registry traffic observed.
type npmProbe struct {
	home    string
	fakeBin string
	logPath string
}

// newNpmProbe writes the fakes. `npm install` materializes the binary and the
// node_modules package.json the launcher reads; `npm view` answers with
// $FAKE_LATEST. Every invocation is appended to a log, which is the measurement.
//
// $FAKE_INSTALL_FAIL makes `npm install` fail the way a real one does — non-zero, nothing
// written — which is the only way to reach the branch that decides whether a pinned
// launcher may record a spec it never got.
func newNpmProbe(t *testing.T, bin string) *npmProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "npm.log")

	npm := `#!/bin/bash
printf '%s\n' "$*" >> "` + logPath + `"
case "${1:-}" in
install)
    if [ -n "${FAKE_INSTALL_FAIL:-}" ]; then
        echo "npm ERR! network unreachable" >&2
        exit 1
    fi
    spec="${@: -1}"
    name="${spec%@*}"
    ver="${spec##*@}"
    ver="${FAKE_INSTALLED_VERSION:-$ver}"
    mkdir -p "$NPM_CONFIG_PREFIX/bin" "$NPM_CONFIG_PREFIX/lib/node_modules/$name"
    printf '{"version":"%s"}\n' "$ver" > "$NPM_CONFIG_PREFIX/lib/node_modules/$name/package.json"
    printf '#!/bin/sh\necho RAN\n' > "$NPM_CONFIG_PREFIX/bin/` + bin + `"
    chmod +x "$NPM_CONFIG_PREFIX/bin/` + bin + `"
    ;;
view)
    echo "${FAKE_LATEST:-0}"
    ;;
esac
exit 0
`
	// A 5-line stand-in for `jq -r .version <file>`: the launcher only ever asks that one
	// question, and depending on the developer's jq being present would make this test
	// skip silently on the machines that matter least.
	jq := `#!/bin/bash
f="${@: -1}"
[ -f "$f" ] || exit 1
line=$(tr -d ' \t' < "$f")
line="${line#*\"version\":\"}"
printf '%s\n' "${line%%\"*}"
`
	for name, body := range map[string]string{"npm": npm, "jq": jq} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &npmProbe{home: home, fakeBin: fakeBin, logPath: logPath}
}

// run renders the launcher for pkg, executes it, and returns the fake npm's argv log.
func (p *npmProbe) run(t *testing.T, bin, pkg string, env ...string) []string {
	t.Helper()
	body := npmAgentLauncher(
		&packdecl.Install{Kind: "npm", Bin: bin, Package: pkg},
		filepath.Join(p.home, "stamps"),
	)
	script := filepath.Join(p.home, bin)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = append([]string{
		"HOME=" + p.home,
		"PATH=" + p.fakeBin + ":" + os.Getenv("PATH"),
	}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, out)
	}
	return p.log(t)
}

func (p *npmProbe) log(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(p.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil // Split would hand back one empty line and read as "npm ran"
	}
	return strings.Split(trimmed, "\n")
}

func (p *npmProbe) truncateLog(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(p.logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// agePastInterval backdates the launcher's stamp beyond UPDATE_INTERVAL (3600s), which is
// the trigger for the hourly registry poll.
func (p *npmProbe) agePastInterval(t *testing.T, bin string) {
	t.Helper()
	stamp := filepath.Join(p.home, "stamps", bin+".stamp")
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
}

func hasArgv(log []string, substr string) bool {
	for _, line := range log {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// TestNpmLauncherInstallsTheDeclaredVersion is the defect, reduced: a scoped package with a
// version must reach npm intact.
func TestNpmLauncherInstallsTheDeclaredVersion(t *testing.T) {
	p := newNpmProbe(t, "tool")
	log := p.run(t, "tool", "@scope/tool@2.0.0")

	if !hasArgv(log, "install -g --prefer-online @scope/tool@2.0.0") {
		t.Errorf("declared version did not reach npm:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "@latest") {
		t.Errorf("a declared version must not be re-suffixed with @latest:\n%s", strings.Join(log, "\n"))
	}
}

// TestUnpinnedNpmLauncherIsUnchanged is the no-policy-change guard: nothing about an
// unversioned declaration may move — same @latest install, same hourly poll.
func TestUnpinnedNpmLauncherIsUnchanged(t *testing.T) {
	p := newNpmProbe(t, "tool")

	log := p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("an unversioned package must still install @latest:\n%s", strings.Join(log, "\n"))
	}

	// Stamp older than the interval → the poll must run, see a newer latest, and update.
	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log = p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9", "FAKE_INSTALLED_VERSION=9.9.9")
	if !hasArgv(log, "view tool version") {
		t.Errorf("the hourly registry poll must still run for an unpinned package:\n%s",
			strings.Join(log, "\n"))
	}
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Errorf("a newer latest must still trigger an update:\n%s", strings.Join(log, "\n"))
	}
}

// TestUnpinnedNpmLauncherDoesNotReinstallWhenCurrent is the half of "unchanged" the
// poll's own refactor put at risk: the comparison it exists to make.
//
// The hourly poll reads the INSTALLED version out of
// `$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json` and compares it with the
// registry's. Both halves moved when the branch was extracted into
// `_poll_and_update` / `_installed_version`, and if either stops matching — the
// lookup handed $SPEC instead of $PKG is the near miss, since the two are now
// different strings — the comparison never comes out equal and the launcher runs a
// full `npm install -g` on the first invocation after EVERY interval, forever. That
// is precisely the reinstall storm the pinned branch was written to avoid, arriving
// on the unpinned path where every shipped pack lives.
//
// A test that only ever asserts "an update did happen" cannot see it: an unconditional
// reinstall satisfies that assertion too. So this one pins the negative.
func TestUnpinnedNpmLauncherDoesNotReinstallWhenCurrent(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")

	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log := p.run(t, "tool", "tool", "FAKE_LATEST=1.0.0")

	if !hasArgv(log, "view tool version") {
		t.Fatalf("the poll must still run for an unpinned package:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "install") {
		t.Errorf("the installed version already IS the registry's latest; reinstalling here "+
			"is an unconditional hourly `npm install -g` for every jail:\n%s", strings.Join(log, "\n"))
	}
}

// TestPinnedNpmLauncherNeverPollsTheRegistry: the poll asks for the registry's `latest`,
// which is not an answer to any question a pinned package has. Left running, it also never
// compares equal for a tag or a range — so it would reinstall hourly, forever.
func TestPinnedNpmLauncherNeverPollsTheRegistry(t *testing.T) {
	for _, pkg := range []string{"tool@1.2.3", "tool@next", "tool@^1.0.0"} {
		p := newNpmProbe(t, "tool")
		p.run(t, "tool", pkg, "FAKE_INSTALLED_VERSION=1.2.3")

		p.agePastInterval(t, "tool")
		p.truncateLog(t)
		log := p.run(t, "tool", pkg, "FAKE_LATEST=9.9.9", "FAKE_INSTALLED_VERSION=1.2.3")

		if len(log) != 0 {
			t.Errorf("%s: a pinned package must touch npm exactly once — got:\n%s",
				pkg, strings.Join(log, "\n"))
		}
	}
}

// TestPinnedNpmLauncherFollowsTheDeclaration is what makes a pin usable rather than
// merely expressible: with a binary already installed, the pinned branch skips the
// registry — so if it did not also notice the DECLARATION moving, changing a pack from
// 1.2.3 to 1.3.0 would silently keep running 1.2.3 for the life of the jail home.
func TestPinnedNpmLauncherFollowsTheDeclaration(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3")
	p.truncateLog(t)

	// Same jail home, new declaration, stamp still fresh.
	log := p.run(t, "tool", "tool@1.3.0")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Errorf("a moved declaration must be installed:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "view") {
		t.Errorf("and it must be noticed offline, without a registry poll:\n%s",
			strings.Join(log, "\n"))
	}

	// Re-running with the SAME declaration must then be inert.
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool@1.3.0"); len(log) != 0 {
		t.Errorf("an unchanged pin must not reinstall:\n%s", strings.Join(log, "\n"))
	}
}

// TestPinnedNpmLauncherRetriesAFailedUpgrade is the hole the recorded-spec mechanism opens
// if the spec is recorded unconditionally, and it is worse than the bug the mechanism fixes.
//
// An UPGRADE is not the cold case: REAL_BIN is already there holding the PREVIOUS version,
// so a failed `npm install` does not fall into the "not installed, retry unconditionally"
// branch. Record the new spec anyway and the pinned branch goes quiet too — both exits are
// shut, and one offline minute pins the jail to the old binary for the life of the home,
// with no hourly retry, no retry at boot and no message. The unpinned path cannot wedge
// this way: its next poll still sees the two versions differ and tries again. This test is
// the asymmetry, closed.
func TestPinnedNpmLauncherRetriesAFailedUpgrade(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3") // installs, records tool@1.2.3
	p.truncateLog(t)

	// The declaration moves while the registry is unreachable. The attempt is made and
	// fails; the binary on disk is still 1.2.3.
	log := p.run(t, "tool", "tool@1.3.0", "FAKE_INSTALL_FAIL=1")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Fatalf("the moved declaration must still be attempted:\n%s", strings.Join(log, "\n"))
	}
	p.truncateLog(t)

	// Registry back. Nothing about the DECLARATION changed, but 1.3.0 was never installed,
	// so the launcher must try again rather than treat the failure as done.
	log = p.run(t, "tool", "tool@1.3.0")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Errorf("a failed pinned upgrade must be retried, not recorded as installed:\n%s",
			strings.Join(log, "\n"))
	}
	// ...and it must still be noticed offline — a retry is not a licence to poll.
	if hasArgv(log, "view") {
		t.Errorf("the retry must not reach for the registry's latest:\n%s", strings.Join(log, "\n"))
	}

	// Once it succeeds the launcher goes quiet again, so the retry is bounded by the
	// failure and not a permanent reinstall loop.
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool@1.3.0"); len(log) != 0 {
		t.Errorf("a pin that finally installed must stop reinstalling:\n%s", strings.Join(log, "\n"))
	}
}
