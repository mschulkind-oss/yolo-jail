package integration

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// RUNBOOK ITEM 10 — the two layout defects a mutation pass found on 2026-09-12, neither of
// which leaves the tree red (docs/plans/runbooks/macos-user-manual-checks.md §10;
// docs/design/macos-user-home-tiers.md §10, the two bullets after "NOT fixed here").
//
// ⚠ THIS FILE COVERS THE FIRST ONLY. The second — a transient `LoadJailPacks` failure
// deriving an EMPTY link set, so `install_home_overlay` writes a REAL ~/.claude and every
// later launch refuses forever — is deliberately NOT automated and must not be. It needs a
// fault injected into the pack loader, which is a seam that does not exist; and its failure
// mode is a permanently poisoned account home whose only remedy destroys the machine tier
// the shared-credentials hook exists to preserve. That is a state no CI job should be able
// to reach by accident, on a Mac that belongs to somebody. It stays a runbook item, to be
// run — if ever — on a machine nobody cares about.
//
// WHAT THE FIRST ONE IS. The layout refuses to replace a real directory with a symlink
// (OQ-HT2: there is no migration, and nothing is copied, renamed or deleted for you).
// Refusing is correct. The defect was the REMEDY: every refusal prescribed
// `sudo rm -rf /Users/_yolojail`, which reaches a path in the ACCOUNT HOME and does not
// touch one in the WORKSPACE SIDECAR — and a `SharedDirs` MIRROR lives in the sidecar. So
// an occupied mirror produced a refusal that told the reader to destroy their credentials
// and then refuse identically on the next launch, because the offending directory had never
// been in the account. A remedy that cannot reach the path it names is worse than none: it
// looks actionable.
//
// WHY IT IS REACHABLE AT ALL, and ⚠ NOT for the reason the runbook gives. The runbook says
// `run/prepare.go`'s `migrateOldOverlay` "COPIES every packload.SharedDirs entry into
// <ws>/.yolo/home". It copies the other way — `migrateOldOverlay(wsState/<dir>,
// GlobalHome/<dir>)`, old source first (internal/cli/run/storagehelpers.go) — so it never
// creates one. The real route is the one that call exists to REPAIR: Apple Container
// mounted no shared dirs until 2026-08-24 and bound /home/agent straight at the workspace
// state dir, so on any Mac that ran an AC jail before then, the agent's own
// `.claude-shared-credentials` is sitting in that workspace's sidecar as a real directory —
// exactly where this backend now needs a mirror. The defect is reachable; the runbook's
// mechanism for it is inverted.
//
// WHY AN INTEGRATION TEST WHEN A UNIT TEST PINS THE MESSAGE. The unit twin
// (TestDarwinHomeLayoutRefusalPrescribesARemedyThatReachesTheOccupiedPath) drives
// InstallDarwinHomeLayout and reads the error value. What it cannot see is the trip from
// there to a human: the refusal crosses genStep, genFailuresError's `\n  - ` join, a `sudo`
// boundary and the orchestrator's own abort message before anybody reads it, and a remedy
// that arrives reflowed or truncated is not a remedy. This is also the only check that the
// launch REFUSES rather than continuing — the unit test asserts an error is returned, not
// that anything stops.
func TestMacosUserLayoutRefusesAnOccupiedSidecarMirror(t *testing.T) {
	requireMacosUser(t)
	// The mirror exists because a PACK declared `.claude-shared-credentials` at
	// scope:machine. With no pack selected there is no mirror to occupy and this test
	// would launch cleanly and assert nothing.
	packHome(t, `{"packs": ["claude"]}`)

	ws := macosUserWorkspace(t, `{}`)
	// The runbook's own recipe, one directory: occupy a MIRROR path in the sidecar.
	mirror := filepath.Join(ws, ".yolo", "home", ".claude-shared-credentials")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatalf("creating the occupied mirror path %s: %v", mirror, err)
	}
	// A file inside it, so the no-migration half of OQ-HT2 can be checked at the end:
	// the refusal must leave what it refused to migrate exactly where it is.
	sentinel := filepath.Join(mirror, ".credentials.json")
	if err := os.WriteFile(sentinel, []byte("a container-era credential"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", sentinel, err)
	}
	// Re-applied because the grant is INHERITED AT CREATION and these directories were
	// created after the fixture ran it. Without the grant the sandbox cannot write in the
	// sidecar at all, and the launch would fail with a permission error several steps
	// before the refusal under test — a green-looking red for the wrong reason.
	if r := runCommand(t, ws, []string{"macos-fix-permissions", ws}); r.rc != 0 {
		t.Fatalf("`yolo macos-fix-permissions %s` failed (rc %d) after the fixture created "+
			"the occupied mirror:\n%s", ws, r.rc, r.combined())
	}

	const ranMarker = "=== THE AGENT RAN ==="
	r := runMacosUser(t, ws, `echo "`+ranMarker+`"`)
	out := r.combined()

	if r.rc == 0 {
		t.Fatalf("the launch SUCCEEDED with a real directory at %s, where the layout's "+
			"mirror symlink belongs.\nOQ-HT2 says a real path there is never removed, "+
			"renamed or copied — so the launch has to refuse. Continuing means the "+
			"shared_credentials hook's relative link resolves into that directory instead of "+
			"the account home, and every jail on this Mac silently loses its login "+
			"(runbook item 10).\nstdout:\n%s\nstderr:\n%s", mirror, r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout, ranMarker) {
		t.Errorf("the agent RAN despite the refusal — %q reached stdout. A refusal that "+
			"still launches is a warning, and this one is about a layout the agent is "+
			"about to read its credentials through (runbook item 10).", ranMarker)
	}
	if !strings.Contains(out, mirror) {
		t.Fatalf("the refusal never names %s, so the reader cannot tell which path is in "+
			"the way and no remedy below can be checked (runbook item 10).\n"+
			"stdout:\n%s\nstderr:\n%s", mirror, r.stdout, r.stderr)
	}

	// THE ASSERTION THIS TEST EXISTS FOR. Collect the commands the refusal OFFERS and
	// require one of them to name the occupied path. Naming the account home in PROSE is
	// correct and expected — the sentence "resetting the account does not touch these" is
	// what stops a reader running it from memory — so the check is on the prescription,
	// not on the mention.
	remedies := macosUserLayoutRemedyPaths(out)
	if !slices.Contains(remedies, mirror) {
		t.Errorf("the refusal offers these commands:\n  rm -rf %v\nand none of them touches "+
			"%s, which is the path it just refused over.\nFollowing what it offers therefore "+
			"changes nothing and the next launch refuses identically. This is runbook item "+
			"10's first defect: the mirror is in the WORKSPACE SIDECAR and the account reset "+
			"reaches only /Users/_yolojail.\nstdout:\n%s\nstderr:\n%s",
			remedies, mirror, r.stdout, r.stderr)
	}
	if slices.Contains(remedies, macosUserSandboxHome) {
		t.Errorf("the refusal PRESCRIBES `rm -rf %s` (offered: %v) for a workspace-sidecar "+
			"path.\nNothing in the account home is occupied here, so that command destroys "+
			"the machine tier — every jail's Claude login on this Mac — and leaves the "+
			"offending directory, and therefore the refusal, exactly where they were "+
			"(runbook item 10).", macosUserSandboxHome, remedies)
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

// macosUserLayoutRemedyPaths returns the path argument of every `rm -rf` the launch's output
// OFFERS as a command — an INDENTED line whose first word is `rm` or `sudo rm`.
//
// The indentation is what separates a prescription from a mention, and that distinction is
// the test's whole subject: the refusal is expected to NAME the account reset (to warn that
// it does not help here) while not PRESCRIBING it. Parsing commands rather than matching the
// message's prose also survives a rewording, which a refusal this long will get.
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
		for _, prefix := range []string{"sudo rm -rf ", "rm -rf "} {
			if rest, ok := strings.CutPrefix(trimmed, prefix); ok {
				paths = append(paths, strings.TrimSpace(rest))
				break
			}
		}
	}
	return paths
}

// THE LINUX PREFLIGHT for the parser above, for the reason its twin in
// macosuserhometier_test.go gives: nobody who develops this repo can run the test above, so
// its reader would otherwise be exercised for the first time on the one machine the whole
// exercise was aimed at. What a Mac uniquely settles here is that the refusal REACHES a
// reader — across genStep, genFailuresError's join, a sudo boundary and the orchestrator's
// abort — and that the launch stops. Whether the reader can parse the bytes does not need a
// Mac, so it is measured here, against a refusal produced by the real layout.
//
// Not behind requireMacosUser and so not in the vacuity ledger: it exercises no backend. It
// carries the TestMacosUser prefix so `-run '^TestMacosUser'` selects it on the macOS job
// too.
func TestMacosUserLayoutRemedyParserReadsARealRefusal(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	mirror := filepath.Join(sidecar, ".claude-shared-credentials")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	err := entrypoint.DeriveDarwinHomeLayout(home, sidecar,
		[]string{".claude"}, []string{".claude-shared-credentials"}).Apply()
	if err == nil {
		t.Fatal("a real directory at the mirror path must refuse (OQ-HT2)")
	}
	got := macosUserLayoutRemedyPaths(err.Error())
	if len(got) != 1 || got[0] != mirror {
		t.Fatalf("the parser read %v out of a real refusal, want exactly [%s].\n"+
			"Either the refusal stopped offering a remedy the occupied path can be fixed by "+
			"— runbook item 10's defect, returning — or this parser no longer recognises the "+
			"form the refusal writes, in which case the Mac test above would fail saying the "+
			"wrong thing.\nrefusal:\n%s", got, mirror, err)
	}
	// The account home is expected to be NAMED — the warning that resetting it does not
	// help is what stops a reader running it from memory — and expected NOT to be offered.
	if !strings.Contains(err.Error(), home) {
		t.Errorf("the refusal never mentions the account home %s, so a reader who already "+
			"knows the `sudo rm -rf` remedy has nothing telling them it is the wrong one "+
			"here:\n%s", home, err)
	}
}
