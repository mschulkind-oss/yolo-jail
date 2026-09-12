package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// RUNBOOK ITEM 5 — THE PER-WORKSPACE HOME LAYOUT, never run on hardware
// (docs/plans/runbooks/macos-user-manual-checks.md §5).
//
// WHAT IT SETTLES. macos-user has ONE sandbox account, /Users/_yolojail, so until
// 2026-09-12 the machine tier, the workspace tier and the session tier were the same
// directory: every workspace shared one ~/.claude, one set of transcripts, and one
// composed briefing that concurrent launches raced to overwrite
// (docs/design/macos-user-home-tiers.md §2). Alternative A′ gives the account a workspace
// tier without moving HOME — every directory the podman argv binds from
// <workspace>/.yolo/home becomes a SYMLINK from the account home into that same sidecar,
// and every pack-declared `scope: machine` dir stays put and is MIRRORED back into the
// sidecar so the shared_credentials hook's relative link still resolves.
//
// WHY AN INTEGRATION TEST, in this repo's own terms. The layout is pinned on Linux against
// a real filesystem, credential resolution included, by driving RunDarwinBootstrap
// (internal/entrypoint/darwinhomelayout_test.go). Three things it cannot reach are exactly
// what §10's warning lists as unmeasured: whether the SANDBOX UID can create
// <workspace>/.yolo/home through the shared-group ACL at all, whether the layout survives a
// LOADED Seatbelt profile, and whether a SECOND workspace's launch really repoints the
// account home rather than inheriting the first's. All three need a launch.
//
// ⚠ WHAT A RUN OF THIS LEAVES BEHIND, because it is inherent to a one-account backend
// rather than a defect: the last launch's links in /Users/_yolojail point into a workspace
// this test then deletes, so the account home is left holding DANGLING symlinks. That is
// self-healing — ensureLayoutSymlink repoints a link yolo itself wrote on the next launch —
// and it is the same state a human gets by deleting any workspace they last launched. It is
// also why this file must not run concurrently with another macos-user test: they would
// fight over one account home. The package's no-t.Parallel() rule is what holds that.
func TestMacosUserHomeTierIsPerWorkspace(t *testing.T) {
	requireMacosUser(t)
	// THE PACK SELECTION IS THE POINT, not scaffolding. `.claude` and
	// `.claude-shared-credentials` are not names this backend knows: they are the claude
	// pack's `state` contributions at scope:workspace and scope:machine, and
	// InstallDarwinHomeLayout reads the two tier lists from packload.WritableDirs /
	// SharedDirs. With no pack selected there is no workspace-tier link to observe and no
	// mirror at all, so the whole item would pass vacuously.
	packHome(t, `{"packs": ["claude"]}`)

	wsA := macosUserWorkspace(t, `{}`)
	wsB := macosUserWorkspace(t, `{}`)
	sidecarA := filepath.Join(wsA, ".yolo", "home")
	sidecarB := filepath.Join(wsB, ".yolo", "home")
	// Unique per run and per workspace, derived from the fixture's own directory name, so
	// an old marker left in a shared account home can never be mistaken for this run's.
	markerA := "marker-" + filepath.Base(wsA) + ".jsonl"
	markerB := "marker-" + filepath.Base(wsB) + ".jsonl"
	credProbe := ".yolo-it-cred-probe-" + filepath.Base(wsA)

	rA := runMacosUser(t, wsA, macosUserHomeTierProbeA(sidecarA, credProbe, markerA))
	if rA.rc != 0 {
		t.Fatalf("the first macos-user launch failed (rc %d), so nothing below can be read "+
			"as a statement about the home layout (runbook item 5).\n"+
			"If the failure names %s, the layout step itself refused — read its message: a "+
			"real directory where a layout symlink belongs is never migrated (OQ-HT2).\n"+
			"stdout:\n%s\nstderr:\n%s", rA.rc, sidecarA, rA.stdout, rA.stderr)
	}

	// ---- The workspace tier: every one of these is a symlink INTO this workspace ----
	wantLinks := macosUserHomeTierWantLinks(sidecarA)
	layout := macosUserHomeProbeFields(t, rA.stdout, "LAYOUT")
	for rel, want := range wantLinks {
		got, seen := layout[rel]
		switch {
		case !seen:
			t.Errorf("~/%s: the probe printed no line for it at all, so the launch did not "+
				"reach the layout step (runbook item 5)", rel)
		case got == "ABSENT":
			t.Errorf("~/%s does not exist in the sandbox home. Every directory the podman "+
				"argv binds from <workspace>/.yolo/home has to be a symlink here, or this "+
				"backend has no workspace tier for it (runbook item 5, macos-user-home-tiers.md §5).",
				rel)
		case got == "NOT-A-SYMLINK":
			t.Errorf("~/%s is a REAL path in the shared account home, not a symlink into %s.\n"+
				"Every workspace on this Mac then shares it — which for ~/.claude means one "+
				"set of transcripts and one composed briefing that concurrent launches race "+
				"to overwrite (runbook item 5; macos-user-home-tiers.md §2).", rel, sidecarA)
		case got != want:
			t.Errorf("~/%s -> %s\nwant                 %s\n"+
				"It points somewhere other than THIS workspace's sidecar. If the two differ "+
				"only by a /private prefix or a symlinked parent, the fixture and the "+
				"launcher resolved the workspace path differently rather than the layout "+
				"being wrong (runbook item 5).", rel, got, want)
		}
	}

	// ---- The machine tier: stays in the account home, and is mirrored into the sidecar ----
	machine := macosUserHomeProbeFields(t, rA.stdout, "MACHINE")
	if got := machine["machine-tier"]; got != "REAL-DIR" {
		t.Errorf("~/.claude-shared-credentials is %s, want REAL-DIR.\n"+
			"A `scope: machine` dir is the ONE tier this backend already had right: it must "+
			"stay in /Users/_yolojail so every workspace's jail reaches one login. A symlink "+
			"here would put credentials in the per-workspace tier and make you re-authenticate "+
			"per project (runbook item 5; OQ-HT1).", got)
	}
	if got, want := machine["mirror"], macosUserSandboxHome+"/.claude-shared-credentials"; got != want {
		t.Errorf("%s/.claude-shared-credentials -> %s\nwant %s\n"+
			"This is the MIRROR, and it is not decoration: the shared_credentials hook writes "+
			"a RELATIVE link (`../.claude-shared-credentials/…`) by design, and the kernel "+
			"resolves `..` PHYSICALLY — so from ~/.claude, which is a link into the sidecar, "+
			"`..` lands in the SIDECAR. Without this mirror the credential dangles "+
			"(macos-user-home-tiers.md §5.3).", sidecarA, got, want)
	}

	// ---- The credential: the relative link the hook wrote, and that the chain resolves ----
	cred := macosUserHomeProbeFields(t, rA.stdout, "CRED")
	const wantCredTarget = "../.claude-shared-credentials/.credentials.json"
	if got := cred["cred-link"]; got != wantCredTarget {
		t.Errorf("~/.claude/.credentials.json -> %q, want %q.\n"+
			"The RELATIVE spelling is the whole mechanism: it is what makes credential "+
			"sharing one mechanism on every backend rather than a per-backend question every "+
			"pack would have to feature-detect (macos-user-home-tiers.md §5.0). An absolute "+
			"target here means this backend grew a branch in the one hook they all share.", got, wantCredTarget)
	}
	// THE RESOLUTION IS PROVED WITH A PROBE FILE, NOT WITH .credentials.json, and that is
	// deliberate: on a real Mac that file is the human's live Claude login, and a test that
	// wrote through it to check that writing works would destroy the thing it measured. The
	// probe sits in the same machine-tier directory, is reached by the same relative
	// spelling from the same workspace-tier directory, and is removed by the same script —
	// so it exercises the identical chain (~/.claude link → sidecar → `..` → mirror →
	// account home) and can cost nobody a login.
	if got := cred["probe-read"]; got != "machine-tier-bytes" {
		t.Errorf("reading through the relative credential chain gave %q, want %q.\n"+
			"The link's spelling is right (asserted above) and the bytes still do not arrive, "+
			"so the chain breaks at the SIDECAR: `..` from ~/.claude resolves physically into "+
			"%s, and whatever is at %s/.claude-shared-credentials is not reaching the account "+
			"home's real directory. On a Mac this is a silent logout in every jail "+
			"(runbook item 5; macos-user-home-tiers.md §5.3).", got, "machine-tier-bytes", sidecarA, sidecarA)
	}

	// ---- The second workspace gets its OWN tier ----
	rB := runMacosUser(t, wsB, macosUserHomeTierProbeB(markerB))
	if rB.rc != 0 {
		t.Fatalf("the second macos-user launch failed (rc %d), which is the half of runbook "+
			"item 5 that no unit test can reach — one account home being repointed at a "+
			"different workspace.\nstdout:\n%s\nstderr:\n%s", rB.rc, rB.stdout, rB.stderr)
	}
	layoutB := macosUserHomeProbeFields(t, rB.stdout, "LAYOUT")
	if got, want := layoutB[".claude"], filepath.Join(sidecarB, "claude"); got != want {
		t.Fatalf("after launching a SECOND workspace, ~/.claude -> %s, want %s.\n"+
			"The account home was not repointed, so both workspaces are still sharing one "+
			"state dir and everything below this line is meaningless (runbook item 5).", got, want)
	}
	projectsB := macosUserHomeProbeFields(t, rB.stdout, "MARKER")["projects"]
	if !strings.Contains(projectsB, markerB) {
		t.Errorf("the second workspace's own marker %q is missing from its ~/.claude/projects "+
			"(%q) — the probe could not write its own state, so the isolation check below "+
			"proves nothing", markerB, projectsB)
	}
	if strings.Contains(projectsB, markerA) {
		t.Errorf("the second workspace SEES the first workspace's transcript marker %q in "+
			"~/.claude/projects (%q).\nThe workspace tier did not separate: this is the leak "+
			"the Seatbelt profile already denies under /Users and the shared home handed back "+
			"anyway (runbook item 5; macos-user-home-tiers.md §2 symptom 2).", markerA, projectsB)
	}

	// SEPARATED, NOT ERASED — and this is the assertion that tells the two apart. "B does
	// not see A's marker" is equally true of a layout that wipes ~/.claude on every launch,
	// which would be a worse bug wearing this test's green. So the first workspace's state
	// is looked for where the layout says it lives, on the host, AFTER the second launch.
	//
	// The path is DERIVED from what launch A reported its own ~/.claude resolved to, not
	// rebuilt from this test's idea of the subtree spelling — a spelling this test also
	// asserts, and a check that assumed it would be circular.
	markerPath := filepath.Join(layout[".claude"], "projects", markerA)
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("the first workspace's state is gone after the second workspace launched: "+
			"stat %s: %v\n"+
			"If this says `no such file`, the layout ISOLATED the workspaces by destroying "+
			"one — a second workspace must repoint the account home, never empty the first "+
			"one's sidecar. If it says `permission denied`, this check could not be made and "+
			"the isolation above stands unconfirmed in this direction (runbook item 5).",
			markerPath, err)
	}
}

// macosUserSandboxHome is the sandbox account's home, spelled here rather than imported
// from macosuser.SandboxHome() on purpose: this assertion is about what a launch really
// produced, and comparing it against the same constant the launch computed it from would
// hold only that the constant equals itself.
const macosUserSandboxHome = "/Users/_yolojail"

// macosUserHomeTierProbeA is the first launch's probe: the layout, the machine tier and its
// mirror, the credential link and the relative chain it resolves through, and a marker in
// this workspace's transcripts dir.
//
// Written as `key|value` lines inside named fences so one launch answers every question
// runbook item 5 asks and each assertion above stays independent (the suite's `section()`
// convention). `|` rather than `=` because a symlink target is a path and a path may
// contain almost anything else.
func macosUserHomeTierProbeA(sidecar, credProbe, marker string) string {
	return fmt.Sprintf(`
echo "=== LAYOUT ==="
for p in .claude .config .local .npm-global go .yolo/bin; do
  t=$(readlink "$HOME/$p" 2>/dev/null) || t=""
  if [ -z "$t" ]; then
    if [ -e "$HOME/$p" ]; then t="NOT-A-SYMLINK"; else t="ABSENT"; fi
  fi
  printf '%%s|%%s\n' "$p" "$t"
done
echo "=== END LAYOUT ==="
echo "=== MACHINE ==="
if [ -L "$HOME/.claude-shared-credentials" ]; then m=SYMLINK
elif [ -d "$HOME/.claude-shared-credentials" ]; then m=REAL-DIR
else m=ABSENT
fi
printf 'machine-tier|%%s\n' "$m"
printf 'mirror|%%s\n' "$(readlink '%[1]s/.claude-shared-credentials' 2>/dev/null || echo NOT-A-SYMLINK)"
echo "=== END MACHINE ==="
echo "=== CRED ==="
printf 'cred-link|%%s\n' "$(readlink "$HOME/.claude/.credentials.json" 2>/dev/null || echo ABSENT)"
printf 'machine-tier-bytes' > "$HOME/.claude-shared-credentials/%[2]s" 2>/dev/null
ln -sfn "../.claude-shared-credentials/%[2]s" "$HOME/.claude/%[2]s" 2>/dev/null
printf 'probe-read|%%s\n' "$(cat "$HOME/.claude/%[2]s" 2>/dev/null || echo UNREADABLE)"
rm -f "$HOME/.claude/%[2]s" "$HOME/.claude-shared-credentials/%[2]s"
echo "=== END CRED ==="
echo "=== MARKER ==="
mkdir -p "$HOME/.claude/projects"
: > "$HOME/.claude/projects/%[3]s"
printf 'projects|%%s\n' "$(ls -1 "$HOME/.claude/projects" | tr '\n' ' ')"
echo "=== END MARKER ==="
`, sidecar, credProbe, marker)
}

// macosUserHomeTierProbeB is the second workspace's probe: where the account home now
// points, and what this workspace's own transcripts dir holds.
func macosUserHomeTierProbeB(marker string) string {
	return fmt.Sprintf(`
echo "=== LAYOUT ==="
printf '.claude|%%s\n' "$(readlink "$HOME/.claude" 2>/dev/null || echo NOT-A-SYMLINK)"
echo "=== END LAYOUT ==="
echo "=== MARKER ==="
mkdir -p "$HOME/.claude/projects"
: > "$HOME/.claude/projects/%[1]s"
printf 'projects|%%s\n' "$(ls -1 "$HOME/.claude/projects" | tr '\n' ' ')"
echo "=== END MARKER ==="
`, marker)
}

// macosUserHomeProbeFields slices one named fence out of a probe's stdout and parses its
// `key|value` lines.
//
// An EMPTY fence is fatal rather than an empty map: every caller below would otherwise
// report a long list of "the probe printed no line for it" errors for one cause — the
// sandbox produced no output — and send the reader looking at the layout instead of at the
// launch.
func macosUserHomeProbeFields(t *testing.T, stdout, name string) map[string]string {
	t.Helper()
	body := section(stdout, "=== "+name+" ===", "=== END "+name+" ===")
	if strings.TrimSpace(body) == "" {
		t.Fatalf("the sandbox produced no %s probe output, so nothing about it can be "+
			"asserted. The launch exited 0, so the shell ran and this fence did not — read "+
			"the whole stdout:\n%s", name, stdout)
	}
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "|"); ok {
			out[k] = v
		}
	}
	return out
}

// macosUserHomeTierWantLinks is the workspace tier: every account-home path that must be a
// symlink into this workspace's sidecar, and the exact target.
//
// SPELLED OUT rather than derived from paths.HomeSurfaces() + packload.WritableDirs, for
// the reason the floor test spells its binary names: a table derived from the deriver is
// satisfied by whatever the deriver currently says, and the deriver is half of what is under
// test. The spelling is not left unchecked either — the Linux preflight below applies the
// REAL DeriveDarwinHomeLayout to a real filesystem and holds it to this same table, so a
// wrong entry here fails on the machine that develops this repo rather than on a Mac.
//
// The runbook names the first three. The other three are the same rule and cost nothing
// extra to observe in the same launch.
func macosUserHomeTierWantLinks(sidecar string) map[string]string {
	return map[string]string{
		".claude":     filepath.Join(sidecar, "claude"),     // the claude pack's scope:workspace state
		".config":     filepath.Join(sidecar, "config"),     // assemble_parts.go's config bind
		".local":      filepath.Join(sidecar, "local"),      // paths.HomeSurfaces
		".npm-global": filepath.Join(sidecar, "npm-global"), // paths.HomeSurfaces
		"go":          filepath.Join(sidecar, "go"),         // paths.HomeSurfaces
		".yolo/bin":   filepath.Join(sidecar, "yolo-bin"),   // the generated-script anchor
	}
}

// THE LINUX PREFLIGHT for everything above that is not the Mac: it lays a REAL layout with
// the real deriver, runs the REAL probe script against it, and parses the output with the
// REAL parser — then makes the same assertions the launch-backed test makes.
//
// WHY IT IS HERE AND NOT A COMMENT SAYING "reviewed carefully". Nobody who develops this
// repo can run the test above; it skips on every machine that sees it until a Mac with a
// sandbox account picks it up. Everything between writing the probe and reading its answer
// is therefore unexercised code of the most brittle kind — a shell script built by
// Sprintf, and a parser for its output — and the first machine to find a quoting bug in it
// would be the one machine the whole exercise was aimed at, hours into a CI run. What a
// Mac uniquely settles is the SANDBOX: the ACL, the privilege transition, the loaded
// Seatbelt profile, one account home repointed between workspaces. The plumbing does not
// need a Mac, so it is measured here.
//
// It is NOT behind requireMacosUser and so is NOT in the vacuity ledger, deliberately: it
// exercises no backend, and counting it would let the macOS job satisfy its own
// "something ran" check without a sandbox ever starting. It carries the TestMacosUser
// prefix so `-run '^TestMacosUser'` selects it there too.
//
// `..` resolves PHYSICALLY on both kernels — measured on Linux 2026-09-11 and confirmed on
// macOS 26.5 the same day (macos-user-home-tiers.md §5.3) — which is what makes the
// credential half of this transfer.
func TestMacosUserHomeTierProbeReadsARealLayout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH, so the probe script cannot be exercised here")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// The REAL deriver, with the tier lists the claude pack declares — `.claude` at
	// scope:workspace and `.claude-shared-credentials` at scope:machine — so this fixture
	// is the shape the launch above produces rather than a hand-built lookalike.
	layout := entrypoint.DeriveDarwinHomeLayout(home, sidecar,
		[]string{".claude"}, []string{".claude-shared-credentials"})
	if err := layout.Apply(); err != nil {
		t.Fatalf("applying the real layout: %v", err)
	}
	// The credential symlink the shared_credentials hook writes, in the hook's own
	// RELATIVE spelling (entrypoint.linkSharedCredential computes it with filepath.Rel).
	// The layout does not write this one; the hook does, and the probe reads it.
	cred := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.Symlink("../.claude-shared-credentials/.credentials.json", cred); err != nil {
		t.Fatalf("writing the hook's credential symlink: %v", err)
	}

	const marker = "marker-preflight.jsonl"
	const credProbe = ".yolo-it-cred-probe-preflight"
	// bash -c, not -lc: the login shell matters for the PATH re-prepend, which is runbook
	// item 6's subject and not this one's, and sourcing a developer's profile here would
	// make the fixture depend on the machine.
	cmd := exec.Command("bash", "-c", macosUserHomeTierProbeA(sidecar, credProbe, marker))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe script did not run cleanly — it is built by Sprintf and this "+
			"is where a quoting bug surfaces, rather than on a Mac: %v\n%s", err, out)
	}
	stdout := string(out)

	got := macosUserHomeProbeFields(t, stdout, "LAYOUT")
	for rel, want := range macosUserHomeTierWantLinks(sidecar) {
		if got[rel] != want {
			t.Errorf("~/%s|%s, want %s.\nEither the probe cannot read a link the real "+
				"deriver wrote, or macosUserHomeTierWantLinks disagrees with "+
				"DeriveDarwinHomeLayout — and the second would make the Mac test fail for a "+
				"reason that has nothing to do with the Mac.\nfull output:\n%s",
				rel, got[rel], want, stdout)
		}
	}
	machine := macosUserHomeProbeFields(t, stdout, "MACHINE")
	if machine["machine-tier"] != "REAL-DIR" {
		t.Errorf("machine-tier|%s, want REAL-DIR", machine["machine-tier"])
	}
	if want := filepath.Join(home, ".claude-shared-credentials"); machine["mirror"] != want {
		t.Errorf("mirror|%s, want %s", machine["mirror"], want)
	}
	c := macosUserHomeProbeFields(t, stdout, "CRED")
	if want := "../.claude-shared-credentials/.credentials.json"; c["cred-link"] != want {
		t.Errorf("cred-link|%s, want %s", c["cred-link"], want)
	}
	if c["probe-read"] != "machine-tier-bytes" {
		t.Errorf("probe-read|%s, want machine-tier-bytes — the relative chain through the "+
			"sidecar mirror did not resolve, which on a Mac reads as a lost login",
			c["probe-read"])
	}
	if p := macosUserHomeProbeFields(t, stdout, "MARKER")["projects"]; !strings.Contains(p, marker) {
		t.Errorf("projects|%s, want it to contain %s", p, marker)
	}
	// The probe cleans up after itself: it writes into the MACHINE tier, which on a real
	// Mac is the human's live credential directory, and a probe file left there would
	// outlive the workspace and every later launch.
	for _, leftover := range []string{
		filepath.Join(home, ".claude-shared-credentials", credProbe),
		filepath.Join(sidecar, "claude", credProbe),
	} {
		if _, err := os.Lstat(leftover); err == nil {
			t.Errorf("the probe left %s behind in the machine tier; on a Mac that is "+
				"/Users/_yolojail, shared by every workspace on the machine", leftover)
		}
	}

	// And the second workspace's probe, which is a different script and would otherwise
	// be exercised for the first time on a Mac.
	cmdB := exec.Command("bash", "-c", macosUserHomeTierProbeB("marker-preflight-b.jsonl"))
	cmdB.Env = append(os.Environ(), "HOME="+home)
	outB, err := cmdB.CombinedOutput()
	if err != nil {
		t.Fatalf("the second probe script did not run cleanly: %v\n%s", err, outB)
	}
	if got := macosUserHomeProbeFields(t, string(outB), "LAYOUT")[".claude"]; got != filepath.Join(sidecar, "claude") {
		t.Errorf(".claude|%s, want %s", got, filepath.Join(sidecar, "claude"))
	}
	if p := macosUserHomeProbeFields(t, string(outB), "MARKER")["projects"]; !strings.Contains(p, "marker-preflight-b.jsonl") {
		t.Errorf("projects|%s, want it to contain the second probe's marker", p)
	}
}
