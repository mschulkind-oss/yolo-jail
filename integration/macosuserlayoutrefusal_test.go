package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// RUNBOOK ITEM 10 — a layout refusal whose remedy cannot reach the path it refused over
// (docs/plans/runbooks/macos-user-manual-checks.md §10; docs/reference/macos-user-home-tiers.md,
// "What is still shared, and what that costs").
//
// ⚠ THE RUNBOOK'S SECOND DEFECT IS NOT HERE, and must not be. A transient `LoadJailPacks`
// failure deriving an EMPTY layout needs a fault injected into the pack loader, a seam that
// does not exist, and its failure mode poisons a home on a Mac that belongs to somebody. It
// stays a runbook item, to be run — if ever — on a machine nobody cares about.
//
// WHAT IT GUARDS. The layout refuses to replace a real directory with a symlink (OQ-HT2: there
// is no migration, and nothing is copied, renamed or deleted for you). Refusing is correct.
// The defect this test was written for was the REMEDY: before slice 12a the account mode's
// HOME was /Users/_yolojail and the workspace sidecar held MIRRORS of its machine-scope dirs,
// and an occupied mirror got a refusal prescribing `sudo rm -rf /Users/_yolojail` — a command
// that never reaches the sidecar, destroys every jail's login, and leaves the next launch
// refusing identically.
//
// THE GEOMETRY NOW (slice 12a, MMP-D25 in docs/design/macos-user-model-plan.md). Account mode
// runs with HOME = <ws>/.yolo/home, laid by entrypoint.DeriveDarwinWorkspaceHome. There are no
// mirrors: <ws>/.yolo/home/.claude-shared-credentials is HOME's own OUTWARD link to the machine
// root's real /Users/_yolojail/.claude-shared-credentials. A real directory at that path is an
// occupied LINK of the home, and the refusal for a workspace home (occupiedWorkspaceHomeError)
// names it under "THIS WORKSPACE'S HOME" and offers to remove that path alone. Until the Mac
// run of 2026-11-03 it offered `sudo rm -rf <ws>/.yolo/home`, the workspace's whole home with
// its transcripts, under the pre-12a heading "the SANDBOX ACCOUNT HOME". This test holds:
//
//   - the launch REFUSES, before the agent runs, and names the occupied path;
//   - every command it offers removes EXACTLY that path — so following it fixes this
//     workspace and touches nothing else in its home;
//   - no command it offers names the machine root /Users/_yolojail, which holds every
//     workspace's login;
//   - nothing at the occupied path is changed (OQ-HT2), end to end.
//
// WHY AN INTEGRATION TEST WHEN A UNIT TEST PINS THE MESSAGE. The unit twin
// (TestDarwinHomeLayoutRefusalPrescribesARemedyThatReachesTheOccupiedPath) drives the layout
// and reads the error value. What it cannot see is the trip from there to a human: the refusal
// crosses genStep, genFailuresError's `\n  - ` join, a `sudo` boundary and the orchestrator's
// own abort message before anybody reads it, and a remedy that arrives reflowed or truncated
// is not a remedy. This is also the only check that the launch REFUSES rather than continuing.
func TestMacosUserLayoutRefusesAnOccupiedSidecarMirror(t *testing.T) {
	requireMacosUser(t)
	// The outward link exists because a PACK declared `.claude-shared-credentials` at
	// scope:machine. With no pack selected there is no link to occupy and this test would
	// launch cleanly and assert nothing.
	packHome(t, `{"packs": ["claude"]}`)

	ws := macosUserWorkspace(t, `{}`)
	home := filepath.Join(ws, ".yolo", "home")
	// Occupy the path where HOME's link to the machine root's credential dir belongs — the
	// state a container-era launch (Apple Container bound /home/agent straight at this
	// directory and mounted no shared dirs until 2026-08-24) leaves behind.
	occupied := filepath.Join(home, ".claude-shared-credentials")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatalf("creating the occupied path %s: %v", occupied, err)
	}
	// A file inside it, so the no-migration half of OQ-HT2 can be checked at the end:
	// the refusal must leave what it refused to migrate exactly where it is.
	sentinel := filepath.Join(occupied, ".credentials.json")
	if err := os.WriteFile(sentinel, []byte("a container-era credential"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", sentinel, err)
	}
	// Re-applied because the grant is INHERITED AT CREATION and these directories were
	// created after the fixture ran it. Without the grant the sandbox cannot write in the
	// home at all, and the launch would fail with a permission error several steps before
	// the refusal under test — a green-looking red for the wrong reason.
	if r := runCommand(t, ws, []string{"yolo-account", "share", ws}); r.rc != 0 {
		t.Fatalf("`yolo yolo-account share %s` failed (rc %d) after the fixture created "+
			"the occupied path:\n%s", ws, r.rc, r.combined())
	}

	const ranMarker = "=== THE AGENT RAN ==="
	r := runMacosUser(t, ws, `echo "`+ranMarker+`"`)
	out := r.combined()

	if r.rc == 0 {
		t.Fatalf("the launch SUCCEEDED with a real directory at %s, where the home's link "+
			"to %s/.claude-shared-credentials belongs.\nOQ-HT2 says a real path there is "+
			"never removed, renamed or copied — so the launch has to refuse. Continuing means "+
			"the shared_credentials hook's relative link resolves into that directory instead "+
			"of the machine root, and this workspace silently loses the Mac's one login "+
			"(runbook item 10).\nstdout:\n%s\nstderr:\n%s", occupied, macosUserSandboxHome, r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout, ranMarker) {
		t.Errorf("the agent RAN despite the refusal — %q reached stdout. A refusal that "+
			"still launches is a warning, and this one is about a layout the agent is "+
			"about to read its credentials through (runbook item 10).", ranMarker)
	}
	if !strings.Contains(out, occupied) {
		t.Fatalf("the refusal never names %s, so the reader cannot tell which path is in "+
			"the way and no remedy below can be checked (runbook item 10).\n"+
			"stdout:\n%s\nstderr:\n%s", occupied, r.stdout, r.stderr)
	}

	// THE ASSERTION THIS TEST EXISTS FOR, on the commands the refusal OFFERS rather than on
	// its prose: each must reach the occupied path, and none may reach past this workspace.
	remedies := macosUserLayoutRemedyPaths(out)
	if len(remedies) == 0 {
		t.Errorf("the refusal offers no `rm -rf` command at all, so a reader with only this "+
			"log has no next step (runbook item 10).\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}
	for _, p := range remedies {
		if filepath.Clean(p) != occupied {
			t.Errorf("the refusal offers `rm -rf %s` (all offered: %v), which is not %s, the "+
				"path it refused over, alone.\nA command that misses the path changes nothing "+
				"and the next launch refuses identically; one wider, such as this workspace's "+
				"home %s, destroys what is not in the way (runbook item 10).\nstdout:\n%s\nstderr:\n%s",
				p, remedies, occupied, home, r.stdout, r.stderr)
		}
	}
	if slices.Contains(remedies, macosUserSandboxHome) {
		t.Errorf("the refusal PRESCRIBES `rm -rf %s` (offered: %v) for a workspace-home "+
			"path.\nSince slice 12a that directory is the machine root — every workspace's "+
			"Claude login on this Mac — and it does not hold the offending directory, so the "+
			"refusal would recur (runbook item 10; MMP-D25).", macosUserSandboxHome, remedies)
	}

	// NO MIGRATION, end to end (OQ-HT2). The unit suite pins that Apply does not delete;
	// this pins that nothing LATER in the launch does either, on the one path where the
	// bytes are a credential.
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "a container-era credential" {
		t.Errorf("the refused launch modified or removed %s (read %q, err %v).\n"+
			"OQ-HT2 is explicit that nothing at an occupied path is copied, renamed or "+
			"deleted for the user — the whole reason the launch refuses instead of migrating "+
			"is that this file may be the only copy of a credential (runbook item 10).",
			sentinel, body, err)
	}
}

// macosUserPathReaches reports whether removing dir removes p: dir is p, or a directory
// above it. Lexical on purpose: both sides are spellings the launch printed or the fixture
// minted already resolved, and a `..` in a prescribed path is itself a defect worth failing.
func macosUserPathReaches(dir, p string) bool {
	dir, p = filepath.Clean(dir), filepath.Clean(p)
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// macosUserLayoutRemedyPaths returns the path argument of every `rm -rf` the launch's output
// OFFERS as a command — an INDENTED line whose first word is `rm` or `sudo rm`.
//
// The indentation is what separates a prescription from a mention, and that distinction is
// the test's whole subject: the refusal may NAME a reset in prose (to warn that it does not
// help) without PRESCRIBING it. Parsing commands rather than matching the message's prose
// also survives a rewording, which a refusal this long will get.
//
// A SHELL SPLITS THE LINE, as it does for the reader who pastes it: the refusal quotes a
// path with a space in it, and a reader cutting the line on its prefix would hand back the
// quotes, while one splitting on spaces would hand back the pieces. `rm` is a shell function
// printing its own argv, so nothing the line names is removed. A line no shell can parse is
// returned whole, so the comparison fails naming it.
//
// Its Linux-side twin is layoutRemedyPaths in internal/entrypoint — deliberately a second,
// tiny reader rather than an exported helper, because what crosses from that package to this
// one is the rendered bytes of a launch, and a shared parser would hide a reflow.
func macosUserLayoutRemedyPaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line == strings.TrimLeft(line, " \t") {
			continue // not an indented command line
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "sudo rm -rf ") && !strings.HasPrefix(trimmed, "rm -rf ") {
			continue
		}
		words, err := exec.Command("/bin/sh", "-c",
			"sudo() { \"$@\"; }\nrm() { printf '%s\\0' \"$@\"; }\n"+trimmed).Output()
		if err != nil {
			paths = append(paths, trimmed)
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(words), "\x00"), "\x00")
		paths = append(paths, args[1:]...) // past the -rf
	}
	return paths
}

// THE LINUX PREFLIGHT for the parser and the reach rule above, for the reason its twin in
// macosuserhometier_test.go gives: nobody who develops this repo can run the test above, so
// its reader would otherwise be exercised for the first time on the one machine the whole
// exercise was aimed at. What a Mac uniquely settles here is that the refusal REACHES a
// reader — across genStep, genFailuresError's join, a sudo boundary and the orchestrator's
// abort — and that the launch stops. Whether the reader can parse the bytes, and whether what
// it parses meets the Mac test's rule, does not need a Mac, so it is measured here, against a
// refusal the real workspace-home layout (account mode's since MMP-D25) produces.
//
// The workspace sits under "My Projects", so the path the refusal prescribes has a space in
// it and reaches the reader quoted: the form a Mac workspace takes as often as not, and the
// one a prefix-cutting reader misread.
//
// Not behind requireMacosUser and so not in the vacuity ledger: it exercises no backend. It
// carries the TestMacosUser prefix so `-run '^TestMacosUser'` selects it on the macOS job
// too.
func TestMacosUserLayoutRemedyParserReadsARealRefusal(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	machine := filepath.Join(base, "machine")
	home := filepath.Join(base, "My Projects", "ws", ".yolo", "home")
	occupied := filepath.Join(home, ".claude-shared-credentials")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	err = entrypoint.DeriveDarwinWorkspaceHome(
		entrypoint.DarwinHomes{Home: home, Machine: machine, Shared: machine},
		[]string{".claude"}, []string{".claude-shared-credentials"}).Apply()
	if err == nil {
		t.Fatal("a real directory where the home's outward link belongs must refuse (OQ-HT2)")
	}
	if !strings.Contains(err.Error(), occupied) {
		t.Errorf("the refusal never names the occupied path %s:\n%s", occupied, err)
	}
	got := macosUserLayoutRemedyPaths(err.Error())
	if len(got) == 0 {
		t.Fatalf("the parser read no command out of a real refusal. Either the refusal "+
			"stopped offering one, or this parser no longer recognises the form it writes, "+
			"in which case the Mac test above would fail saying the wrong thing.\nrefusal:\n%s", err)
	}
	for _, p := range got {
		if filepath.Clean(p) != occupied {
			t.Errorf("the parser read `rm -rf %s` (all: %v), which is not %s alone — the rule "+
				"the Mac test holds the refusal to. Either the refusal changed what it "+
				"prescribes, or the parser split a quoted path.\nrefusal:\n%s", p, got, occupied, err)
		}
		if macosUserPathReaches(p, machine) {
			t.Errorf("the refusal prescribes `rm -rf %s`, which reaches the machine root %s "+
				"(on a Mac, every workspace's login):\n%s", p, machine, err)
		}
	}
}
