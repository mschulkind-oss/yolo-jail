package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Agent library-model tests. They prove the selectable-agent surface: each
// agent installs on first use (lazy launcher), reports a version, and gets its
// config and briefing generated — all credential-free (no authenticated task
// runs).
//
// Every test drives a real container, so each calls requireJail(t) first, which
// also gates them out of `go test -short`. No test here calls t.Parallel():
// these are network-heavy (npm/go/native-installer fetches on first use) and can
// be per-arch flaky. A red cell in one arch means gating that pack out of that arch
// through packCase.vendorSkipArch below — not weakening the assertion here.

// TestAgentToolsAvailable confirms codex and copilot are both present inside a jail whose
// `packs` name them.
func TestAgentToolsAvailable(t *testing.T) {
	requireJail(t)
	requireRealPackInstalls(t)
	dir := writeProjectWithPacks(t, `{}`, "codex", "copilot")
	r := runYolo(t, dir, "codex --version && copilot --version")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
}

// TestAgentToolsAvailableDirect confirms copilot works when invoked directly
// (`yolo run -- copilot --version`), not via a login shell. This is the exact
// path that once failed with "copilot: command not found" because /mise/shims
// was absent from the non-login-shell PATH.
func TestAgentToolsAvailableDirect(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, `{}`, "copilot")
	// `command -v`, not `--version`: the bug this test exists for was "copilot: command not
	// found" — /mise/shims missing from the NON-LOGIN-shell PATH — and resolution is exactly
	// what that bug broke. Running the binary would additionally install it from the vendor,
	// which is a different question on a different trigger (docs/reference/agent-install-in-ci.md#three-triggers-matched-to-three-causes), and would put this
	// assertion behind a network fetch it never needed.
	r := runYoloDirect(t, dir, "bash", "-c", "command -v copilot")
	if r.rc != 0 {
		t.Fatalf("copilot did not resolve on the non-login PATH: rc %d\nstdout: %s\nstderr: %s",
			r.rc, r.stdout, r.stderr)
	}
}

// packCase describes one shipped agent PACK: its pack name, the binary its `install`
// declaration provides, that binary's version flag, the config file its `surfaces` render,
// and an auto-approve marker expected inside that file.
type packCase struct {
	pack         string
	binary       string
	versionArg   string
	configRel    string
	configDirEnv string
	marker       string
	// vendorSkipArch is the GOARCH the VENDOR does not publish this program for, with
	// nothing yolo can do about it. Empty for every pack whose vendor ships both arches.
	//
	// It is a field on the row rather than a condition in the test body so the fact lives
	// beside the pack it is about — and so `TestPackMatrixCoversEveryShippedProgram` still
	// sees a row here, which is what keeps a skipped pack from quietly leaving the matrix.
	vendorSkipArch   string
	vendorSkipReason string
}

// packMatrix has one row per shipped agent pack; the subtest name is the pack name.
//
// Markers are the auto-approve settings each pack's surfaces assert (claude acceptEdits,
// copilot yolo, opencode allow, pi defaultProjectTrust, codex danger-full-access). They
// come from the pack's `managed`/`defaults` layers — there is no per-agent config generator
// any more. OMP owns a pure generated provider catalog, so its empty marker asserts only
// that the YAML surface was rendered before any provider is configured.
var packMatrix = []packCase{
	{pack: "claude", binary: "claude", versionArg: "--version",
		configRel: ".claude/settings.json", marker: "acceptEdits"},
	{pack: "copilot", binary: "copilot", versionArg: "--version",
		configRel: ".copilot/config.json", marker: "yolo"},
	{pack: "opencode", binary: "opencode", versionArg: "--version",
		configRel: ".config/opencode/opencode.json", marker: "allow"},
	{pack: "pi", binary: "pi", versionArg: "--version",
		configRel: "settings.json", configDirEnv: "PI_CODING_AGENT_DIR", marker: "defaultProjectTrust"},
	{pack: "codex", binary: "codex", versionArg: "--version",
		configRel: ".codex/config.toml", marker: "danger-full-access"},
	{pack: "agy", binary: "agy", versionArg: "--version",
		configRel: ".gemini/antigravity-cli/settings.json", marker: "permissionMode"},
	{pack: "omp", binary: "oh-omp", versionArg: "--version",
		configRel: "models.yml", configDirEnv: "PI_CODING_AGENT_DIR", marker: "",
		vendorSkipArch: "arm64",
		vendorSkipReason: "the npm package refuses to install: `oh-omp: unsupported " +
			"platform linux-arm64. Supported: darwin-arm64, linux-x64`. Nothing yolo can " +
			"do — and note the shape of the gap it exposes: a pack `program` cannot " +
			"DECLARE which platforms its vendor publishes for (only the `service` kind " +
			"carries `platforms`), so an arm64 Linux jail selecting this pack tries the " +
			"install and gets the vendor's error instead of yolo declining with a reason"},
	// Meta's Muse Code CLI. Its marker is NOT an auto-approve setting: the pack ships no
	// `permissions.default_profile` (that would make the interactive CLI read-only with no
	// in-place escape), so the surface's one load-bearing key is the `schema_version` the CLI
	// refuses to start without. Its vendor installer is PUBLIC (dev.meta.ai, no Meta account;
	// an optional `lookaside.facebook.com` authenticated fast path is used only when
	// ~/.config/muse/auth.json exists), so the real-install cells of packs.yml and
	// macos-user.yml are not gate-blocked — see packs/muse-code/pack.toml.
	{pack: "muse-code", binary: "muse", versionArg: "--version",
		configRel: ".config/muse/settings.json", marker: "schema_version"},
	// NOT AN AGENT: an MCP server program. It renders no config surface of its own (its `mcp`
	// entry lands in each agent's), so the file asserted is the `files` tree its entry runs,
	// which names the program it finds.
	{pack: "chrome-devtools", binary: "chrome-devtools-mcp", versionArg: "--version",
		configRel: ".local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper",
		marker:    "chrome-devtools-mcp"},
}

// TestPackMatrixCoversEveryShippedProgram is the forcing function this file spent its whole
// life without, and `agy` is what it costs.
//
// agy shipped in the very commit that created the pack system ("the pack manifest, the SIX
// official packs, and the loader") and was never in the matrix — not once, `git log -S agy`
// on this file is empty. The rows above are byte-identical before and after the conversion
// from the retired `agents` key: that change translated the list MECHANICALLY instead of
// re-deriving it, so a matrix written when there were five agents stayed five while the
// packs became six. It survived the pack reform, `pack footprint`, the ten-kind refactor,
// and a restructuring that WROTE DOWN that agy had no cell without adding one.
//
// The defect is not the missing row, it is that the row list is a hand-maintained mirror of
// something the filesystem already owns: packs/<name>/pack.json is the authority on which
// packs declare a program. This test makes the mirror checkable, the same way
// TestInheritCensusIsTotal does for config keys and TestEmbedMatchesTree does for the embed.
//
// It runs under -short (no container) ON PURPOSE: `just check-ci` and the check-go CI
// job are where a new pack is added, so that is where forgetting it must go red.
func TestPackMatrixCoversEveryShippedProgram(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range packMatrix {
		covered[tc.pack] = true
	}
	for _, p := range packload.Embedded() {
		if len(p.Decl.InstallContributions()) == 0 {
			continue // ships no program; nothing to install or version
		}
		if !covered[p.Name] {
			t.Errorf("pack %q declares a program but has no packMatrix row — every shipped "+
				"pack that installs something must be covered, or the next one is dropped the "+
				"way agy was. Add a row (pack, bin, versionArg, configRel, marker), and add the "+
				"pack to the install matrices of .github/workflows/packs.yml and "+
				".github/workflows/macos-user.yml (TestPackInstallsWorkflowMirrorsPackMatrix and "+
				"TestMacosUserPackInstallsWorkflowMirrorsPackMatrix check both).", p.Name)
		}
	}
}

// TestPackInstallsVersionsAndConfigures runs, for each shipped agent pack, a single jail
// session that: exercises the lazy launcher's install path via `<bin> --version`; confirms
// the post-install/update stamp file exists (proving the update path ran); and greps the
// generated config for the pack's auto-approve marker (proving the surface rendered).
//
// It is the strongest end-to-end statement that the transition is real: every one of these
// launchers is generated from the pack's `install` declaration, and every config file from
// its `surfaces`, with no Go code naming any of these tools. The matrix entries are PACK
// names now, not agent names — the only registry left is the packs/ directory.
// TestPackRendersConfigAndLauncher is the EVERY-PUSH half of the per-pack matrix: for each
// shipped agent pack, one jail proves that the pack's `surfaces` rendered its config with the
// expected auto-approve marker, and that the pack's `install` declaration produced a launcher.
//
// It installs NOTHING, and separating it out is the point (docs/reference/agent-install-in-ci.md#what-each-assertion-needs).
// Of the three assertions its sibling makes, only this one is genuinely per-pack — five
// packs have five codecs, paths and marker keys, and a render bug in one says nothing about
// the others — and it was the one held hostage by a network install it never needed, because
// the sibling bundled `<bin> --version` and the config grep into a single shell command.
//
// Asserting the LAUNCHER exists rather than that the binary runs is the P3 line (docs/reference/agent-install-in-ci.md#p3): the
// launcher's existence proves the pack's `install` declaration was read and rendered, which
// is yolo's job. Whether the vendor's current release then installs is the vendor's, and it
// is asked by TestPackInstallsVersionsAndConfigures on the triggers that can cause it.
func TestPackRendersConfigAndLauncher(t *testing.T) {
	requireJail(t)
	for _, tc := range packMatrix {
		t.Run(tc.pack, func(t *testing.T) {
			requireJail(t)
			if tc.vendorSkipArch != "" && runtime.GOARCH == tc.vendorSkipArch {
				t.Skipf("%s is not installable on %s: %s", tc.binary, runtime.GOARCH,
					tc.vendorSkipReason)
			}
			dir := writeProjectWithPacks(t, `{}`, tc.pack)
			cmd := fmt.Sprintf(
				"%s && test -x \"$HOME/.yolo/bin/launch/%s\"",
				packConfigCheck(tc), tc.binary,
			)
			r := runYolo(t, dir, cmd)
			if r.rc != 0 {
				t.Fatalf("%s: config/launcher render failed: rc %d\nstdout: %s\nstderr: %s",
					tc.pack, r.rc, r.stdout, r.stderr)
			}
		})
	}
}

func TestPackInstallsVersionsAndConfigures(t *testing.T) {
	if t.Name() != packInstallsTest {
		t.Fatalf("this test is named %s but packInstallsTest says %s, and packs.yml's "+
			"install cells select it by that constant's value", t.Name(), packInstallsTest)
	}
	requireJail(t)
	requireRealPackInstalls(t)
	for _, tc := range packMatrix {
		t.Run(tc.pack, func(t *testing.T) {
			requireJail(t)
			if tc.vendorSkipArch != "" && runtime.GOARCH == tc.vendorSkipArch {
				t.Skipf("%s is not installable on %s: %s", tc.binary, runtime.GOARCH,
					tc.vendorSkipReason)
			}
			dir := writeProjectWithPacks(t, `{}`, tc.pack)
			checkPackInstall(t, tc, runYolo(t, dir, packInstallProbe(t, tc)))
		})
	}
}

// packInstallProbe is the shell a real vendor-install cell runs inside the jail, and every
// backend that runs one SHARES it: TestPackInstallsVersionsAndConfigures above (podman, the
// Pack Installs workflow) and TestMacosUserPackInstallsVersionsAndConfigures (macos-user, the
// macos-user nightly; docs/reference/agent-install-in-ci.md#OQ-CI7). One function, so the
// darwin cell asks exactly the question the Linux cell asks and the two cannot drift.
//
// `<bin> --version` exercises the lazy launcher. Prepared installs must prove the exact identity
// the launch decided, plus its installed spec, without consulting the legacy install stamp; only
// a legacy installer is required to write that stamp. The remaining checks pin the rendered
// marker and declared project skills dirs.
func packInstallProbe(t *testing.T, tc packCase) string {
	t.Helper()
	stamp := "$HOME/.cache/yolo-agent-stamps/" + tc.binary + ".stamp"
	specFile := "$HOME/.cache/yolo-agent-stamps/" + tc.binary + ".spec"
	captureMarker := "$HOME/.cache/yolo-program-captures/" + tc.binary
	in := shippedProgram(t, tc.pack, tc.binary)
	npmName, npmVersion := packdecl.SplitNpmSpec(in.Package)
	expectedSpec := packdecl.NpmInstallSpec(npmName, npmVersion)
	// A program declaring a PRE-LAUNCH REFRESH (packdecl.Refresh; pi's is
	// `update --extensions`) has its launcher run the REAL vendor binary with that
	// argv before the exec whenever the refresh's stamp is due — which on a fresh
	// machine it is. AGENTS.md allows `--version` probes only, so the refresh's stamp
	// is touched first, and checkPackInstall looks for the refresh's own line so a
	// drifted stamp path fails rather than quietly running the vendor's update.
	// The stamp is beside the refresh's lock since XB-D14
	// (docs/design/pi-extension-store-builds.md), named by the launcher's own helper.
	touchRefresh := ""
	if refreshStamp := packRefreshStamp(tc); refreshStamp != "" {
		touchRefresh = fmt.Sprintf("mkdir -p \"$(dirname %s)\" && touch %s && ", refreshStamp, refreshStamp)
	}
	capturePrefix := `"` + tc.binary + `":{"declared":"` + in.Package + `","compatibility":"`
	prepared := fmt.Sprintf(`_capture_vector=${YOLO_PROGRAM_CAPTURES:-}
[ -n "$_capture_vector" ] || _capture_vector='{}'
_capture_prefix=%s
case "$_capture_vector" in
  *"$_capture_prefix"*) _capture=${_capture_vector#*"$_capture_prefix"}; _capture=${_capture%%%%\"*} ;;
  *) _capture= ;;
esac
if [ -n "$_capture" ]; then
  printf '%%s' "$_capture" | grep -Eq '^[a-f0-9]{64}$' || { echo 'PROBE_FAIL: launch capture identity is malformed' >&2; exit 1; }
  [ -f "%s" ] || { echo 'PROBE_FAIL: prepared capture marker is missing' >&2; exit 1; }
  [ "$(cat "%s")" = "$_capture" ] || { echo 'PROBE_FAIL: prepared capture marker differs from launch identity' >&2; exit 1; }
  [ -f "%s" ] || { echo 'PROBE_FAIL: prepared install spec is missing' >&2; exit 1; }
  [ "$(cat "%s")" = %s ] || { echo 'PROBE_FAIL: prepared install spec differs from shipped declaration' >&2; exit 1; }
else
  [ -f "%s" ] || { echo 'PROBE_FAIL: legacy installer did not write its install stamp' >&2; exit 1; }
fi`,
		shquote.Quote(capturePrefix), captureMarker, captureMarker, specFile, specFile,
		shquote.Quote(expectedSpec), stamp)
	return fmt.Sprintf(`probe_fail() { echo "PROBE_FAIL: $1" >&2; exit 1; }
%s%s%s %s || probe_fail 'version command failed'
%s
%s || probe_fail 'rendered config marker is missing'`,
		touchRefresh, seedRefreshSeen(t, tc.pack, tc.binary), tc.binary, tc.versionArg,
		prepared, packConfigCheck(tc)) + projectDirsProbe(t, tc.pack, tc.binary)
}

// packConfigCheck reads the config destination the pack's program uses. A session-home pack must
// publish its relocation variable; checking the shared path instead could pass while the launched
// program reads a different file.
func packConfigCheck(tc packCase) string {
	if tc.configDirEnv == "" {
		return fmt.Sprintf(`grep -q %s "$HOME/%s"`, shquote.Quote(tc.marker), tc.configRel)
	}
	return fmt.Sprintf(`: "${%s:?%s is required for the session config}" && grep -q %s "$%s/%s"`,
		tc.configDirEnv, tc.configDirEnv, shquote.Quote(tc.marker), tc.configDirEnv, tc.configRel)
}

// packRefreshStamp is the pre-launch refresh's stamp packInstallProbe touches, quoted for the
// probe's shell, or "" when the shipped pack declares no refresh on the binary.
func packRefreshStamp(tc packCase) string {
	r := shippedRefresh(tc.pack, tc.binary)
	if r == nil {
		return ""
	}
	return "\"$HOME/" + entrypoint.RefreshStampRel(r.Lock, tc.binary) + "\""
}

// checkPackInstall is packInstallProbe's verdict, shared for the probe's reason: the probe must
// exit 0, and the launcher must not have run the pack's pre-launch refresh, a vendor command
// beyond `--version` that AGENTS.md's no-agent-tests rule forbids.
func checkPackInstall(t *testing.T, tc packCase, r result) {
	t.Helper()
	if r.rc != 0 {
		t.Fatalf("%s: install/version/config check failed: rc %d\nstdout: %s\nstderr: %s",
			tc.pack, r.rc, r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout+r.stderr, "Refreshing "+tc.binary+" (") {
		t.Errorf("%s: the launcher ran the pack's pre-launch refresh before the "+
			"--version probe; the pre-touched stamp at %s no longer throttles it:\n%s",
			tc.pack, packRefreshStamp(tc), r.stderr)
	}
}

// TestPackSelectionPrunesUnselected confirms a codex-only jail installs codex but NOT
// copilot/claude: their lazy launchers under $HOME/.yolo/bin/launch are absent, and
// copilot's config dir is never generated.
//
// This is now the END-TO-END check on pack OPT-IN, and it is the assertion that would have
// caught the bug the unit tests missed. Embedded packs were briefly staged wholesale and
// filtered afterwards, so every pack rendered in-jail regardless of selection — a real jail
// halted its boot with eleven read-only-filesystem errors from packs nobody asked for. The
// mount is the filter, and this proves it from outside.
//
// The launchers moved out of ~/.yolo/bin/block (blockers only, first on PATH) into
// ~/.yolo/bin/launch (lazy installers, second on PATH since B2). Asserting on the OLD path would have
// stayed green for the wrong reason — nothing is ever written there now.
func TestPackSelectionPrunesUnselected(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, `{}`, "codex")
	// No `codex --version` here. Every assertion below is about which launchers and configs
	// yolo GENERATED for the selected pack and withheld for the others, and none of them
	// needs the vendor's tarball — the install was incidental to the test's subject and put
	// its pruning assertion behind a network fetch (docs/reference/agent-install-in-ci.md#three-triggers-matched-to-three-causes).
	cmd := strings.Join([]string{
		"test -x $HOME/.yolo/bin/launch/codex",
		"! test -e $HOME/.yolo/bin/launch/copilot",
		"! test -e $HOME/.yolo/bin/launch/claude",
		"! test -e $HOME/.yolo/bin/block/codex",
		"! test -e $HOME/.copilot/config.json",
	}, " && ")
	r := runYolo(t, dir, cmd)
	if r.rc != 0 {
		t.Fatalf("selection pruning failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
}

// TestJailConfigsPresent confirms the persistent per-agent jail configs in the
// shared home are visible inside the jail (copilot config + codex config).
func TestJailConfigsPresent(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir, "ls /home/agent/.copilot/config.json && ls /home/agent/.codex/config.toml")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.combined())
	}
}

// projectDirsProbe is the `project_dirs` WITNESS (docs/reference/agent-briefings.md, the workspace layer): the
// shell that asserts each project-scope skills directory the pack declares is named, AT PROJECT
// SCOPE, in the agent it just installed. It pins nothing but strings and runs nothing — the
// vendor's files are grepped, never executed (AGENTS.md's no-agent-tests rule) — so an agent that
// stops naming its own project path fails here, the silent break a docs citation would not catch.
//
// PROJECT SCOPE, NOT A BARE SUBSTRING. `.codex/skills` is also the tail of `~/.codex/skills`, the
// HOME dir, and a bare grep passed on that alone: codex's only two `.codex/skills` literals are
// home-dir prose, so the witness said nothing while the declaration it vouched for was missing the
// directory codex does read (`.agents/skills`). An occurrence counts only when the byte before it
// is not part of a path — not `/`, not `~`, not a letter, digit or `_` — which is how a repo path
// is written in prose, a string literal or a markdown table: "files to .claude/skills/ in your
// project", a quoted ".agents/skills", a backticked .pi/skills/.
//
// A declared row the bundle does not carry as text at all — a path assembled from segments, or a
// bundle shipping its code compressed — is in projectDirsNotText with where it was measured
// instead, and is logged rather than checked: a witness that cannot tell must say so, not pass.
// What this can never do is find a directory the agent reads and the pack does not declare.
//
// The installed tree is found from the launcher's own REAL_BIN, resolved, and widened to the
// nearest npm package root when there is one (an npm bundle names its paths in files beside
// its bin, not in the bin). The npm prefix default is read from that launcher too: macos-user's
// invoking shell need not export the variable that the launcher sets inside its own process.
// "" when the pack declares nothing checkable.
func projectDirsProbe(t *testing.T, pack, bin string) string {
	t.Helper()
	checks := projectDirsChecks(t, pack)
	if len(checks) == 0 {
		return ""
	}
	return fmt.Sprintf(` && BIN=%s && eval "$(grep -E '^(export NPM_CONFIG_PREFIX=|REAL_BIN=)' "$HOME/.yolo/bin/launch/%s")" && `+
		`root=$(readlink -f "$REAL_BIN") && d=$(dirname "$root") && `+
		`for i in 1 2 3 4; do if [ -f "$d/package.json" ]; then root=$d; break; fi; d=$(dirname "$d"); done && `+
		`%s`, bin, bin, strings.Join(checks, " && "))
}

// projectDirsChecks is one shell check per witnessed project_dirs row of pack, each grepping the
// installed tree at "$root" and echoing a PROJECT_DIRS line before failing. A row in
// projectDirsFromSegments passes on either form: the path as text at project scope, or every one
// of its segment literals somewhere in the tree.
func projectDirsChecks(t *testing.T, pack string) []string {
	t.Helper()
	var checks []string
	for _, d := range declaredProjectDirs(pack) {
		if why, ok := projectDirsNotText[pack][d]; ok {
			t.Logf("%s's project_dirs row %s is not witnessed by the installed bundle: %s", pack, d, why)
			continue
		}
		pattern := `(^|[^/~A-Za-z0-9_])` + regexp.QuoteMeta(d)
		alt := ""
		if segs, ok := projectDirsFromSegments[pack][d]; ok {
			var all []string
			for _, lit := range segs.literals {
				all = append(all, fmt.Sprintf(`grep -rqaF -- %s "$root"`, shquote.Quote(lit)))
			}
			alt = " || { " + strings.Join(all, " && ") + "; }"
		}
		checks = append(checks, fmt.Sprintf(
			`{ grep -rqaE -- %s "$root"%s || { echo %s; false; }; }`,
			shquote.Quote(pattern), alt,
			shquote.Quote("PROJECT_DIRS: "+pack+" declares "+d+" but the installed bundle never names it at project scope")))
	}
	return checks
}

// projectDirsFromSegments is, per pack, each declared project_dirs row its installed bundle
// builds from segments rather than spelling as one path, with the literals that together ARE
// that search. Unlike projectDirsNotText this still checks: every literal must be in the
// bundle, so the row fails when the search it names is gone. Each literal is one minification
// keeps — a string constant or a property name, never a mangled identifier.
var projectDirsFromSegments = map[string]map[string]segmentWitness{
	"opencode": {
		// opencode 1.19.4, measured 2026-10-22 from the opencode-linux-x64 binary's strings, never
		// run: ConfigPaths.directories walks up from the project for `.opencode` dirs
		// (`up({targets:[".opencode"],start:…,stop:…})`) and the skill scan globs each one with
		// `{skill,skills}/**/SKILL.md`. 1.18.34 searched the same way; its only `.opencode/skills`
		// literal was a config example in its embedded docs, which 1.19.4 dropped.
		".opencode/skills": {literals: []string{`targets:[".opencode"]`, `"{skill,skills}/**/SKILL.md"`}},
	},
}

// segmentWitness is the literals that, all present, witness a row built from segments.
type segmentWitness struct{ literals []string }

// declaredProjectDirs is what the shipped pack declares, from its own pack.json.
func declaredProjectDirs(pack string) []string {
	for _, p := range packload.Embedded() {
		if p.Name == pack {
			return p.Decl.ProjectSkillDirs()
		}
	}
	return nil
}

// projectDirsNotText is, per pack, each declared project_dirs row its installed bundle does not
// carry as text at project scope, and where the row was measured instead. Each was checked
// against the installed bundle on 2026-09-27; TestProjectDirsNotTextNamesDeclaredRows keeps the
// table from naming a row no pack declares.
var projectDirsNotText = map[string]map[string]string{
	"codex": {
		".codex/skills": "codex 0.157.0 joins `skills` onto every project config layer's `.codex` folder " +
			"(codex-rs/ext/skills/src/host_roots.rs at rust-v0.157.0, roots_from_layer_stack); its only " +
			"`.codex/skills` literals are prose about the home dir",
		".agents/skills": "codex 0.157.0 builds it from the constants `.agents` and `skills` " +
			"(host_roots.rs, repo_agents_skill_roots); the binary's one `.agents/skills` literal belongs " +
			"to an external-agent migration",
	},
	"copilot": {
		".github/skills": copilotCompressed,
		".agents/skills": copilotCompressed,
		".claude/skills": copilotCompressed,
	},
	"opencode": {
		".claude/skills": opencodeHomeOnly,
		".agents/skills": opencodeHomeOnly,
	},
}

// TestProjectDirsNotTextNamesDeclaredRows keeps projectDirsNotText honest under -short, where a
// pack's declaration changes: every row it exempts from the probe must be one that pack still
// declares, or the table goes on excusing a row that is gone — or one that was corrected into
// a different path and now goes unchecked.
func TestProjectDirsNotTextNamesDeclaredRows(t *testing.T) {
	for pack, rows := range projectDirsFromSegments {
		declared := map[string]bool{}
		for _, d := range declaredProjectDirs(pack) {
			declared[d] = true
		}
		for d := range rows {
			if !declared[d] {
				t.Errorf("projectDirsFromSegments[%q] names %s, which pack %s does not declare", pack, d, pack)
			}
			if _, both := projectDirsNotText[pack][d]; both {
				t.Errorf("%s's row %s is in both projectDirsNotText and projectDirsFromSegments; the exemption hides the segment check", pack, d)
			}
		}
	}
	for pack, rows := range projectDirsNotText {
		declared := map[string]bool{}
		for _, d := range declaredProjectDirs(pack) {
			declared[d] = true
		}
		for d := range rows {
			if !declared[d] {
				t.Errorf("projectDirsNotText[%q] exempts %s, which pack %s does not declare", pack, d, pack)
			}
		}
	}
}

// TestProjectDirsWitnessReadsSegments runs opencode's generated checks under -short against
// fixture bundles: the segment form of `.opencode/skills` passes, as does the path spelled at
// project scope, and a bundle missing either segment — the search gone — still fails.
func TestProjectDirsWitnessReadsSegments(t *testing.T) {
	checks := projectDirsChecks(t, "opencode")
	if len(checks) == 0 {
		t.Fatal("opencode has no witnessed project_dirs row; the .opencode/skills check vanished")
	}
	const glob, walk = `ie="{skill,skills}/**/SKILL.md"`, `e.up({targets:[".opencode"],start:r,stop:t})`
	for _, tc := range []struct {
		name, bundle string
		pass         bool
	}{
		{"segments, as 1.19.4 ships", walk + ";" + glob, true},
		{"path as text at project scope", `"paths": [".opencode/skills", "/abs"]`, true},
		{"glob without the .opencode walk", glob + `;"~/.opencode/skills"`, false},
		{"walk without the skills glob", walk, false},
		{"neither", `nothing here`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "bundle.js"), []byte(tc.bundle), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", strings.Join(checks, " && "))
			// YOLO_BYPASS_SHIMS: a jail's guardrails pack refuses `grep -r` by PATH.
			cmd.Env = append(os.Environ(), "root="+root, "YOLO_BYPASS_SHIMS=1")
			out, err := cmd.CombinedOutput()
			if tc.pass && err != nil {
				t.Errorf("want pass, got %v:\n%s", err, out)
			}
			if !tc.pass && (err == nil || !strings.Contains(string(out), "PROJECT_DIRS: opencode declares .opencode/skills")) {
				t.Errorf("want a PROJECT_DIRS failure, got err=%v:\n%s", err, out)
			}
		})
	}
}

const (
	copilotCompressed = "copilot 1.0.88 ships its JavaScript compressed inside one executable, so no path " +
		"in it is text; the row is 1.0.48's strings (docs/reference/agent-briefings.md, where each agent reads skills)"
	opencodeHomeOnly = "opencode names it only as a home dir (`~/.claude/skills`, `~/.agents/skills`); its " +
		"project walk is built from segments, and the row is its documented project search " +
		"(docs/reference/agent-briefings.md, where each agent reads skills)"
)

// seedRefreshSeen returns shell that marks the pack's due_on_change content as already
// refreshed, so the pre-launch refresh stays throttled for a --version probe (AGENTS.md: no
// agent runs beyond --version). A pack whose refresh declares due_on_change is ALSO due when
// the watched files' content was never refreshed, which on a fresh CI home it never was, so
// touching the hourly stamp alone no longer suffices. The key is computed exactly as the
// launcher's _refresh_content_key does (internal/entrypoint/prelaunchrefresh.go); if that
// formula drifts, the "Refreshing" check below fails rather than quietly running the vendor.
// The list is read from the shipped pack's own declaration, so it cannot drift from pack.json.
func seedRefreshSeen(t *testing.T, pack, bin string) string {
	t.Helper()
	r := shippedRefresh(pack, bin)
	if r == nil || len(r.DueOnChange) == 0 {
		return ""
	}
	var parts []string
	for _, f := range r.DueOnChange {
		parts = append(parts, fmt.Sprintf(
			`if [ -f "$HOME/%[1]s" ]; then printf '%%s %%s\n' '%[1]s' "$(cksum < "$HOME/%[1]s")"; else printf '%%s absent\n' '%[1]s'; fi`, f))
	}
	// Beside the refresh's lock, where the launcher keeps it since XB-D14
	// (docs/design/pi-extension-store-builds.md), named by the launcher's own helper.
	seen := "$HOME/" + entrypoint.RefreshSeenRel(r.Lock, bin)
	return fmt.Sprintf(`k=$({ %s; } | cksum | tr ' ' '-') && mkdir -p "%s" && touch "%s/$k" && `,
		strings.Join(parts, "; "), seen, seen)
}

// shippedRefresh is the pre-launch refresh the shipped pack declares on bin, or nil.
func shippedRefresh(pack, bin string) *packdecl.Refresh {
	for _, p := range packload.Embedded() {
		if p.Name != pack {
			continue
		}
		for _, in := range p.Decl.InstallContributions() {
			if in.Bin == bin && in.Refresh != nil {
				return in.Refresh
			}
		}
	}
	return nil
}
