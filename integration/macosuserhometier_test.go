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

// RUNBOOK ITEM 5 — THE PER-WORKSPACE HOME, on the hardware
// (docs/plans/runbooks/macos-user-manual-checks.md §5).
//
// WHAT IT SETTLES. macos-user's account mode runs every workspace as ONE sandbox account,
// /Users/_yolojail, so until 2026-09-12 every workspace shared one ~/.claude, one set of
// transcripts, and one composed briefing that concurrent launches raced to overwrite
// (docs/reference/macos-user-home-tiers.md, "The three tiers, and where each one lives"). The
// first fix linked the account home into each workspace's sidecar, and so could serve one
// workspace at a time. SLICE 12a REPLACED THAT GEOMETRY (MMP-D25 in
// docs/design/macos-user-model-plan.md): account mode now runs with HOME = <ws>/.yolo/home,
// the workspace sidecar itself, laid by entrypoint.DeriveDarwinWorkspaceHome exactly as the
// run-as-you mode lays it. So:
//
//   - the per-workspace directories (~/.claude, ~/.config, ~/.local, ~/.npm-global, ~/go,
//     ~/.yolo/bin) are REAL DIRECTORIES in HOME, under their dotted names;
//   - the links point OUT, to the machine root /Users/_yolojail (macosuser.SandboxHome(), which
//     is the launch's Machine and Shared home in account mode): ~/.cache, ~/Library/Caches and
//     each `scope: machine` pack dir such as ~/.claude-shared-credentials;
//   - nothing under /Users/_yolojail links into any workspace (links a pre-12a launch left there
//     linger and nothing reads them, OQ-MM4 in docs/design/macos-user-model.md), and there are
//     no sidecar MIRRORS any more: the shared_credentials hook's relative
//     `../.claude-shared-credentials/…` from ~/.claude resolves physically to
//     HOME/.claude-shared-credentials, which is itself the link to the machine root.
//
// WHY AN INTEGRATION TEST. The layout is pinned on Linux against a real filesystem, credential
// resolution included, by driving RunDarwinBootstrap with HOME equal to the sidecar
// (internal/entrypoint/darwinhomelayout_test.go,
// TestDarwinBootstrapLaysTheWorkspaceHomeWhenHomeIsTheSidecar). What it cannot reach: whether
// the SANDBOX UID can create and write <workspace>/.yolo/home through the shared-group ACL,
// whether the outward links resolve under a LOADED Seatbelt profile, and whether a SECOND
// workspace's launch gets its own home without emptying the first's. All three need a launch.
//
// ⚠ WHAT A RUN OF THIS TOUCHES OUTSIDE ITS WORKSPACES: the probe writes one file into the
// machine root's real .claude-shared-credentials, the directory that holds every workspace's
// Claude login, and removes it in the same script. It never writes .credentials.json itself.
func TestMacosUserHomeTierIsPerWorkspace(t *testing.T) {
	requireMacosUser(t)
	// THE PACK SELECTION IS THE POINT, not scaffolding. `.claude` and
	// `.claude-shared-credentials` are not names this backend knows: they are the claude
	// pack's `state` contributions at scope:workspace and scope:machine, and
	// InstallDarwinHomeLayout reads the two tier lists from packload.WritableDirs /
	// SharedDirs. With no pack selected there is no ~/.claude to separate and no
	// machine-scope link at all, so the whole item would pass vacuously.
	packHome(t, `{"packs": ["claude"]}`)

	wsA := macosUserWorkspace(t, `{}`)
	wsB := macosUserWorkspace(t, `{}`)
	sidecarA := filepath.Join(wsA, ".yolo", "home")
	sidecarB := filepath.Join(wsB, ".yolo", "home")
	// Unique per run and per workspace, derived from the fixture's own directory name, so
	// an old marker left anywhere can never be mistaken for this run's.
	markerA := "marker-" + filepath.Base(wsA) + ".jsonl"
	markerB := "marker-" + filepath.Base(wsB) + ".jsonl"
	credProbe := ".yolo-it-cred-probe-" + filepath.Base(wsA)

	rA := runMacosUser(t, wsA, macosUserHomeTierProbeA(macosUserSandboxHome, credProbe, markerA))
	if rA.rc != 0 {
		t.Fatalf("the first macos-user launch failed (rc %d), so nothing below can be read "+
			"as a statement about the home layout (runbook item 5).\n"+
			"If the failure names %s, the layout step itself refused — read its message: a "+
			"real directory where one of the home's outward links belongs is never migrated "+
			"(OQ-HT2).\nstdout:\n%s\nstderr:\n%s", rA.rc, sidecarA, rA.stdout, rA.stderr)
	}

	// ---- HOME is this workspace's own sidecar (MMP-D25) ----
	homeA := macosUserHomeProbeFields(t, rA.stdout, "HOME")["home"]
	if !samePath(homeA, sidecarA) {
		t.Fatalf("the session's $HOME is %q, want this workspace's own home %s.\n"+
			"Slice 12a runs account mode with HOME = <ws>/.yolo/home (MMP-D25). A HOME of %s "+
			"is the pre-12a geometry, one home for every workspace, and every check below "+
			"would be about the wrong directory. Run `just build-dev-bundle` and launch the "+
			"bundle it emits, not an older installed yolo.", homeA, sidecarA, macosUserSandboxHome)
	}

	// ---- The workspace tier: real directories in HOME, never links ----
	layout := macosUserHomeProbeFields(t, rA.stdout, "LAYOUT")
	for _, rel := range macosUserHomeTierRealDirs() {
		got, seen := layout[rel]
		switch {
		case !seen:
			t.Errorf("~/%s: the probe printed no line for it at all, so the launch did not "+
				"reach the layout step (runbook item 5)", rel)
		case got == "ABSENT":
			t.Errorf("~/%s does not exist in the workspace home %s. The layout lays it as a "+
				"real directory (entrypoint.DeriveDarwinWorkspaceHome); without it this "+
				"backend has nowhere per-workspace for that state (runbook item 5).", rel, sidecarA)
		case got != "REAL-DIR":
			t.Errorf("~/%s is %s, want a REAL directory in %s.\n"+
				"Since slice 12a the home IS the workspace sidecar, so a per-workspace dir "+
				"there is a plain directory (MMP-D25). A link here leads somewhere other "+
				"workspaces can reach too — for ~/.claude, a shared set of transcripts and a "+
				"briefing concurrent launches overwrite (runbook item 5).", rel, got, sidecarA)
		}
	}

	// ---- The machine tier: links OUT of HOME, to the machine root's real directories ----
	links := macosUserHomeProbeFields(t, rA.stdout, "LINKS")
	for rel, want := range macosUserHomeTierWantLinks(macosUserSandboxHome) {
		if got := links[rel]; got != want {
			t.Errorf("~/%s -> %s\nwant      %s\n"+
				"This is machine-wide state, reached from each workspace home by a link to "+
				"the machine root %s (entrypoint.DeriveDarwinWorkspaceHome; MMP-D25). "+
				"NOT-A-SYMLINK means this workspace keeps its own copy — for "+
				".claude-shared-credentials, one Claude login per project instead of one per "+
				"Mac (OQ-HT1).", rel, got, want, macosUserSandboxHome)
		}
	}
	machine := macosUserHomeProbeFields(t, rA.stdout, "MACHINE")
	if got := machine["machine-tier"]; got != "REAL-DIR" {
		t.Errorf("%s/.claude-shared-credentials is %s, want REAL-DIR.\n"+
			"The machine root holds the ONE real credential directory every workspace's "+
			"~/.claude-shared-credentials links to; a link or a missing directory there means "+
			"the chain ends somewhere else, and every jail's login with it (OQ-HT1).",
			macosUserSandboxHome, got)
	}

	// ---- The credential: the relative link the hook wrote, and that the chain resolves ----
	cred := macosUserHomeProbeFields(t, rA.stdout, "CRED")
	const wantCredTarget = "../.claude-shared-credentials/.credentials.json"
	if got := cred["cred-link"]; got != wantCredTarget {
		t.Errorf("~/.claude/.credentials.json -> %q, want %q.\n"+
			"The RELATIVE spelling is the whole mechanism: it is what makes credential "+
			"sharing one mechanism on every backend rather than a per-backend question every "+
			"pack would have to feature-detect (macos-user-home-tiers.md OQ-HT4). An absolute "+
			"target here means this backend grew a branch in the one hook they all share.", got, wantCredTarget)
	}
	// THE RESOLUTION IS PROVED WITH A PROBE FILE, NOT WITH .credentials.json, and that is
	// deliberate: on a real Mac that file is the human's live Claude login, and a test that
	// wrote through it to check that writing works would destroy the thing it measured. The
	// probe sits in the same machine-tier directory, is reached by the same relative
	// spelling from the same workspace-tier directory, and is removed by the same script —
	// so it exercises the identical chain (~/.claude → `..` → HOME/.claude-shared-credentials
	// → the machine root's real directory) and can cost nobody a login.
	if got := cred["probe-read"]; got != "machine-tier-bytes" {
		t.Errorf("reading through the relative credential chain gave %q, want %q.\n"+
			"The link's spelling is right (asserted above) and the bytes still do not arrive, "+
			"so the chain breaks at %s/.claude-shared-credentials: it does not reach %s's real "+
			"directory, or the sandbox profile does not open it. On a Mac this is a silent "+
			"logout in every jail (runbook item 5).", got, "machine-tier-bytes", sidecarA, macosUserSandboxHome)
	}

	// ---- The second workspace gets its OWN home ----
	rB := runMacosUser(t, wsB, macosUserHomeTierProbeB(markerB))
	if rB.rc != 0 {
		t.Fatalf("the second macos-user launch failed (rc %d), which is the half of runbook "+
			"item 5 that no unit test can reach — a second workspace run as the same "+
			"account.\nstdout:\n%s\nstderr:\n%s", rB.rc, rB.stdout, rB.stderr)
	}
	homeB := macosUserHomeProbeFields(t, rB.stdout, "HOME")["home"]
	if !samePath(homeB, sidecarB) {
		t.Fatalf("after launching a SECOND workspace, its $HOME is %q, want its own home %s.\n"+
			"Both workspaces would then share one state dir and everything below this line "+
			"is meaningless (runbook item 5; MMP-D25).", homeB, sidecarB)
	}
	if got := macosUserHomeProbeFields(t, rB.stdout, "LAYOUT")[".claude"]; got != "REAL-DIR" {
		t.Errorf("in the second workspace ~/.claude is %s, want a REAL directory in %s "+
			"(MMP-D25)", got, sidecarB)
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
			"the per-workspace home exists to end (runbook item 5; macos-user-home-tiers.md, "+
			"\"The three tiers, and where each one lives\").", markerA, projectsB)
	}

	// SEPARATED, NOT ERASED — and this is the assertion that tells the two apart. "B does
	// not see A's marker" is equally true of a launch that wipes ~/.claude every time, which
	// would be a worse bug wearing this test's green. So the first workspace's state is looked
	// for on the host, AFTER the second launch, under the HOME launch A reported rather than
	// under this test's own spelling of it.
	markerPath := filepath.Join(homeA, ".claude", "projects", markerA)
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("the first workspace's state is gone after the second workspace launched: "+
			"stat %s: %v\n"+
			"If this says `no such file`, the launches ISOLATED the workspaces by destroying "+
			"one — a second workspace's launch must never touch the first one's home. If it "+
			"says `permission denied`, this check could not be made from the host account "+
			"and the isolation above stands unconfirmed in this direction (runbook item 5).",
			markerPath, err)
	}
}

// macosUserSandboxHome is the sandbox account's home, which since slice 12a is the account
// mode's MACHINE ROOT rather than its HOME (MMP-D25). Spelled here rather than imported from
// macosuser.SandboxHome() on purpose: this assertion is about what a launch really produced,
// and comparing it against the same constant the launch computed it from would hold only that
// the constant equals itself.
const macosUserSandboxHome = "/Users/_yolojail"

// macosUserHomeTierProbeA is the first launch's probe: HOME, the per-workspace dirs, the
// outward links and the machine root's real credential dir, the credential link and the
// relative chain it resolves through, and a marker in this workspace's transcripts dir.
// machine is the machine root the outward links lead to.
//
// Written as `key|value` lines inside named fences so one launch answers every question
// runbook item 5 asks and each assertion above stays independent (the suite's `section()`
// convention). `|` rather than `=` because a symlink target is a path and a path may
// contain almost anything else.
func macosUserHomeTierProbeA(machine, credProbe, marker string) string {
	return macosUserHomeTierProbeHome() +
		macosUserHomeTierProbeLayout(strings.Join(macosUserHomeTierRealDirs(), " ")) +
		fmt.Sprintf(`
echo "=== LINKS ==="
for p in .claude-shared-credentials .cache Library/Caches; do
  printf '%%s|%%s\n' "$p" "$(readlink "$HOME/$p" 2>/dev/null || echo NOT-A-SYMLINK)"
done
echo "=== END LINKS ==="
echo "=== MACHINE ==="
if [ -L '%[1]s/.claude-shared-credentials' ]; then m=SYMLINK
elif [ -d '%[1]s/.claude-shared-credentials' ]; then m=REAL-DIR
else m=ABSENT
fi
printf 'machine-tier|%%s\n' "$m"
echo "=== END MACHINE ==="
echo "=== CRED ==="
printf 'cred-link|%%s\n' "$(readlink "$HOME/.claude/.credentials.json" 2>/dev/null || echo ABSENT)"
printf 'machine-tier-bytes' > "$HOME/.claude-shared-credentials/%[2]s" 2>/dev/null
ln -sfn "../.claude-shared-credentials/%[2]s" "$HOME/.claude/%[2]s" 2>/dev/null
printf 'probe-read|%%s\n' "$(cat "$HOME/.claude/%[2]s" 2>/dev/null || echo UNREADABLE)"
rm -f "$HOME/.claude/%[2]s" "$HOME/.claude-shared-credentials/%[2]s"
echo "=== END CRED ==="
`, machine, credProbe) +
		macosUserHomeTierProbeMarker(marker)
}

// macosUserHomeTierProbeB is the second workspace's probe: its HOME, whether its ~/.claude is
// its own real directory, and what that directory's transcripts dir holds.
func macosUserHomeTierProbeB(marker string) string {
	return macosUserHomeTierProbeHome() + macosUserHomeTierProbeLayout(".claude") +
		macosUserHomeTierProbeMarker(marker)
}

// macosUserHomeTierProbeHome prints the session's HOME.
func macosUserHomeTierProbeHome() string {
	return `
echo "=== HOME ==="
printf 'home|%s\n' "$HOME"
echo "=== END HOME ==="
`
}

// macosUserHomeTierProbeLayout prints, for each space-separated home-relative path in rels,
// REAL-DIR, ABSENT, NOT-A-DIR, or `SYMLINK -> <target>`.
func macosUserHomeTierProbeLayout(rels string) string {
	return `
echo "=== LAYOUT ==="
for p in ` + rels + `; do
  if [ -L "$HOME/$p" ]; then t="SYMLINK -> $(readlink "$HOME/$p")"
  elif [ -d "$HOME/$p" ]; then t=REAL-DIR
  elif [ -e "$HOME/$p" ]; then t=NOT-A-DIR
  else t=ABSENT
  fi
  printf '%s|%s\n' "$p" "$t"
done
echo "=== END LAYOUT ==="
`
}

// macosUserHomeTierProbeMarker writes marker into ~/.claude/projects and lists that dir.
func macosUserHomeTierProbeMarker(marker string) string {
	return fmt.Sprintf(`
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

// macosUserHomeTierRealDirs is the workspace tier: every home-relative path that must be a
// real directory in the workspace home, the claude pack's `.claude` included.
//
// SPELLED OUT rather than derived from paths.HomeSurfaces() + packload.WritableDirs, for the
// reason the floor test spells its binary names: a table derived from the deriver is
// satisfied by whatever the deriver currently says, and the deriver is half of what is under
// test. The spelling is not left unchecked either — the Linux preflight below applies the REAL
// DeriveDarwinWorkspaceHome to a real filesystem and holds it to this same table, so a wrong
// entry here fails on the machine that develops this repo rather than on a Mac.
func macosUserHomeTierRealDirs() []string {
	return []string{
		".claude",     // the claude pack's scope:workspace state
		".config",     // the container's config bind, a real dir here
		".local",      // paths.HomeSurfaces
		".npm-global", // paths.HomeSurfaces
		"go",          // paths.HomeSurfaces
		".yolo/bin",   // the generated-script anchor
	}
}

// macosUserHomeTierWantLinks is the machine tier as the workspace home sees it: every
// home-relative path that must be a link OUT to the machine root, and its exact target.
// Spelled out for macosUserHomeTierRealDirs' reason, and held to the real deriver by the same
// preflight. In account mode the machine root and the shared credential store are both
// /Users/_yolojail (macosuser.LaunchHomes, MMP-D25), so one root serves all three.
func macosUserHomeTierWantLinks(machine string) map[string]string {
	return map[string]string{
		".claude-shared-credentials": filepath.Join(machine, ".claude-shared-credentials"), // the claude pack's scope:machine state
		".cache":                     filepath.Join(machine, ".cache"),
		"Library/Caches":             filepath.Join(machine, "Library", "Caches"),
	}
}

// THE LINUX PREFLIGHT for everything above that is not the Mac: it lays a REAL workspace home
// with the real deriver, runs the REAL probe script against it, and parses the output with the
// REAL parser — then makes the same assertions the launch-backed test makes.
//
// WHY IT IS HERE AND NOT A COMMENT SAYING "reviewed carefully". Nobody who develops this
// repo can run the test above; it skips on every machine that sees it until a Mac with a
// sandbox account picks it up. Everything between writing the probe and reading its answer
// is therefore unexercised code of the most brittle kind — a shell script built by
// Sprintf, and a parser for its output — and the first machine to find a quoting bug in it
// would be the one machine the whole exercise was aimed at, hours into a CI run. What a
// Mac uniquely settles is the SANDBOX: the ACL, the privilege transition, the loaded
// Seatbelt profile, a second workspace run as the same account. The plumbing does not
// need a Mac, so it is measured here.
//
// It is NOT behind requireMacosUser and so is NOT in the vacuity ledger, deliberately: it
// exercises no backend, and counting it would let the macOS job satisfy its own
// "something ran" check without a sandbox ever starting. It carries the TestMacosUser
// prefix so `-run '^TestMacosUser'` selects it there too.
//
// `..` resolves PHYSICALLY on both kernels — measured on Linux 2026-09-11 and confirmed on
// macOS 26.5 the same day (macos-user-home-tiers.md, "The mirror, and the relative credential
// link it exists for") — which is what makes the credential half of this transfer.
func TestMacosUserHomeTierProbeReadsARealLayout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH, so the probe script cannot be exercised here")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	machine := filepath.Join(base, "machine")
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	// The REAL deriver, with account mode's homes (HOME the sidecar, one machine root as both
	// Machine and Shared) and the tier lists the claude pack declares — `.claude` at
	// scope:workspace and `.claude-shared-credentials` at scope:machine — so this fixture is
	// the shape the launch above produces rather than a hand-built lookalike.
	layout := entrypoint.DeriveDarwinWorkspaceHome(
		entrypoint.DarwinHomes{Home: sidecar, Machine: machine, Shared: machine},
		[]string{".claude"}, []string{".claude-shared-credentials"})
	if err := layout.Apply(); err != nil {
		t.Fatalf("applying the real layout: %v", err)
	}
	// The credential symlink the shared_credentials hook writes, in the hook's own
	// RELATIVE spelling (entrypoint.linkSharedCredential computes it with filepath.Rel).
	// The layout does not write this one; the hook does, and the probe reads it.
	cred := filepath.Join(sidecar, ".claude", ".credentials.json")
	if err := os.Symlink("../.claude-shared-credentials/.credentials.json", cred); err != nil {
		t.Fatalf("writing the hook's credential symlink: %v", err)
	}

	const marker = "marker-preflight.jsonl"
	const credProbe = ".yolo-it-cred-probe-preflight"
	run := func(script string) string {
		t.Helper()
		// bash -c, not -lc: the login shell matters for the PATH re-prepend, which is
		// runbook item 6's subject and not this one's, and sourcing a developer's profile
		// here would make the fixture depend on the machine.
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+sidecar)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the probe script did not run cleanly — it is built by Sprintf and this "+
				"is where a quoting bug surfaces, rather than on a Mac: %v\n%s", err, out)
		}
		return string(out)
	}
	stdout := run(macosUserHomeTierProbeA(machine, credProbe, marker))

	if got := macosUserHomeProbeFields(t, stdout, "HOME")["home"]; got != sidecar {
		t.Errorf("home|%s, want %s", got, sidecar)
	}
	got := macosUserHomeProbeFields(t, stdout, "LAYOUT")
	for _, rel := range macosUserHomeTierRealDirs() {
		if got[rel] != "REAL-DIR" {
			t.Errorf("~/%s|%s, want REAL-DIR.\nEither the probe misreads a directory the real "+
				"deriver made, or macosUserHomeTierRealDirs disagrees with "+
				"DeriveDarwinWorkspaceHome — and the second would make the Mac test fail for a "+
				"reason that has nothing to do with the Mac.\nfull output:\n%s", rel, got[rel], stdout)
		}
	}
	links := macosUserHomeProbeFields(t, stdout, "LINKS")
	for rel, want := range macosUserHomeTierWantLinks(machine) {
		if links[rel] != want {
			t.Errorf("~/%s|%s, want %s: the probe or macosUserHomeTierWantLinks disagrees with "+
				"the real deriver's outward link\nfull output:\n%s", rel, links[rel], want, stdout)
		}
	}
	if m := macosUserHomeProbeFields(t, stdout, "MACHINE")["machine-tier"]; m != "REAL-DIR" {
		t.Errorf("machine-tier|%s, want REAL-DIR", m)
	}
	c := macosUserHomeProbeFields(t, stdout, "CRED")
	if want := "../.claude-shared-credentials/.credentials.json"; c["cred-link"] != want {
		t.Errorf("cred-link|%s, want %s", c["cred-link"], want)
	}
	if c["probe-read"] != "machine-tier-bytes" {
		t.Errorf("probe-read|%s, want machine-tier-bytes — the relative chain through "+
			"HOME's outward link did not resolve, which on a Mac reads as a lost login",
			c["probe-read"])
	}
	if p := macosUserHomeProbeFields(t, stdout, "MARKER")["projects"]; !strings.Contains(p, marker) {
		t.Errorf("projects|%s, want it to contain %s", p, marker)
	}
	// The probe cleans up after itself: it writes into the MACHINE tier, which on a real
	// Mac is the human's live credential directory, and a probe file left there would
	// outlive the workspace and every later launch.
	for _, leftover := range []string{
		filepath.Join(machine, ".claude-shared-credentials", credProbe),
		filepath.Join(sidecar, ".claude", credProbe),
	} {
		if _, err := os.Lstat(leftover); err == nil {
			t.Errorf("the probe left %s behind; on a Mac the machine tier is "+
				"/Users/_yolojail, shared by every workspace on the machine", leftover)
		}
	}

	// And the second workspace's probe, which is a different script and would otherwise
	// be exercised for the first time on a Mac.
	outB := run(macosUserHomeTierProbeB("marker-preflight-b.jsonl"))
	if got := macosUserHomeProbeFields(t, outB, "LAYOUT")[".claude"]; got != "REAL-DIR" {
		t.Errorf(".claude|%s, want REAL-DIR", got)
	}
	if p := macosUserHomeProbeFields(t, outB, "MARKER")["projects"]; !strings.Contains(p, "marker-preflight-b.jsonl") {
		t.Errorf("projects|%s, want it to contain the second probe's marker", p)
	}

	// THE CONTRAST: a linked directory must read as a link, not as REAL-DIR, or every
	// REAL-DIR assertion on the Mac would pass for a probe that cannot tell the two apart.
	if err := os.Symlink(machine, filepath.Join(sidecar, "yolo-it-linked")); err != nil {
		t.Fatal(err)
	}
	linked := macosUserHomeProbeFields(t, run(macosUserHomeTierProbeLayout("yolo-it-linked")), "LAYOUT")
	if want := "SYMLINK -> " + machine; linked["yolo-it-linked"] != want {
		t.Errorf("a linked dir reads as %q, want %q: the probe cannot tell a link from a real "+
			"directory, so the Mac test's REAL-DIR assertions would prove nothing",
			linked["yolo-it-linked"], want)
	}
}
