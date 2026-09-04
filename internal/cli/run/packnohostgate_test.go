package run

// packnohostgate_test.go pins OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) AT THE LAUNCH:
// a FETCHED pack that was never approved gets every host claim it declares, and the launch
// goes ahead.
//
// # What this file used to be
//
// packrefusal_test.go, pinning OQ-TP6's opposite: a refused contribution refused the LAUNCH,
// and the fatal named the pack, the claim, and three ways out. Every one of those assertions
// is now inverted. The gate they protected was theatre — selecting a pack means writing
// `packs` in ~/.config/yolo-jail/config.jsonc as the host user, which is strictly more
// authority than the gate withheld, so it refused an actor who had already passed a stronger
// one (gate-placement-principle.md Test 1). OQ-TP6 is not overturned; its SUBJECT is gone.
//
// # Why the assertions are about the LAUNCH and not about packload's accessors
//
// Unchanged from the file this replaces, and it is the reason to keep this shape rather than
// test HonoredInstalls directly: the defect OQ-TP6 was written for was a predicate that
// answered correctly and was then IGNORED. A test over a predicate cannot see that. So these
// drive `stagePacks` — the production call site — from a genuinely fetched pack in the store
// with no lockfile entry, and ask what came out the other end.
//
// The mirror-image protection is what makes them worth keeping now: reintroducing
// `packMayAccessHost` and passing it to `packload.LoadDir` here would leave packload's own
// tests green (a Pack loaded with no gate still honors everything) and turn every assertion
// below red. Deleting the call site is exactly what these fail on.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// fetchedPackWithClaims builds a git+file:// pack — genuinely OriginFetched, with no network —
// whose pack.json carries the given contribution bodies, installs it into the store the launch
// resolves from, and selects it in the user config.
//
// git+file:// rather than a local path, and NO LOCKFILE IS WRITTEN. That combination is the
// whole fixture: it is the exact state that used to be refused — installed, selected, never
// approved — so a gate reintroduced anywhere on the launch path finds its unapproved branch
// here and the assertions below go red.
func fetchedPackWithClaims(t *testing.T, home, contributes string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	pj := `{"name":"acme","contributes":[` + contributes + `]}`
	if err := os.WriteFile(filepath.Join(repo, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		// CleanGitEnv first: hook-exported git state is ABSOLUTE from a linked
		// worktree and would redirect this helper onto the committer's index.
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-qm", "pack")

	src := "git+file://" + repo + "?ref=main"
	writeUserPacks(t, home, `[{"name": "acme", "source": "`+src+`"}]`)
	syncPackStore(t, src)
}

// writePack writes a pack.json into dir. It lived in packhostgate_test.go — the unit test of
// the deleted launch gate — and is kept here because a dozen unrelated fixtures call it.
func writePack(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THE RULING, end to end and through the production path: a fetched pack with a curl-piped
// installer, a host-file read and a host mount LAUNCHES, and keeps all three.
//
// Every claim here was separately fatal before 2026-09-04. The installer is the sharpest —
// it runs a downloaded script in the jail — and it is also the one whose refusal was refuted
// in-house before the gate was deleted: `npm install -g` from the same fetched tree runs
// `postinstall`, ungated (pack-execution-trust.md §2). The npm install beside it is kept from
// the retired version as the CONTROL: it was never gated, so it must still arrive, and a
// build that dropped every install would otherwise pass the installer assertion by accident.
func TestFetchedPackHostClaimsAreHonoredWithNoApproval(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	home := packHome(t)
	isolatePackModules(t)
	fakeLoopholes(t)
	fetchedPackWithClaims(t, home,
		`{"kind":"program","bin":"acme","via":"installer","url":"https://acme.test/i.sh"},`+
			`{"kind":"program","bin":"tsx","via":"npm","package":"tsx"},`+
			`{"kind":"reads-host","host":".netrc"},`+
			`{"kind":"mount","host":"datasets/acme","into":"acme"}`)

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out
	root, loaded, _, err := o.stagePacks("yolo-test-no-host-gate")
	if err != nil {
		t.Fatalf("a fetched pack that was never approved REFUSED THE LAUNCH:\n%v\n\n"+
			"OQ-TP9 deleted that gate: naming the pack in `packs` means editing the user "+
			"config as the host user, which already grants more than the gate withheld. If "+
			"this refusal is deliberate, rule on it in docs/design/trust-paths.md first.\n%s",
			err, out.String())
	}
	if root == "" || len(loaded) != 1 {
		t.Fatalf("staging returned root=%q and %d packs, want the one configured pack",
			root, len(loaded))
	}
	p := loaded[0]

	installs, refused := p.HonoredInstalls()
	if len(refused) != 0 {
		t.Errorf("an install was refused: %v", refused)
	}
	var sawInstaller, sawNpm bool
	for _, in := range installs {
		if in.InstallerURL == "https://acme.test/i.sh" {
			sawInstaller = true
		}
		if in.Bin == "tsx" {
			sawNpm = true
		}
	}
	if !sawInstaller {
		t.Errorf("the curl-piped installer did not survive the launch path: %+v\n"+
			"It reaches the jail through the staged pack.json, so a gate here is what "+
			"decides whether the launcher gets written", installs)
	}
	if !sawNpm {
		t.Errorf("the npm install — never gated, in any era — is missing too, so the "+
			"assertion above may be passing over a build that honors no installs: %+v", installs)
	}

	if g, r := p.HonoredHostFiles(); len(g) != 1 || len(r) != 0 {
		t.Errorf("the reads-host claim: %d granted / %d refused, want 1 / 0 (%v)", len(g), len(r), r)
	}
	if g, r := p.HonoredMounts(); len(g) != 1 || len(r) != 0 {
		t.Errorf("the mount claim: %d granted / %d refused, want 1 / 0 (%v)", len(g), len(r), r)
	}
}

// THE ARGV, which is the assertion the accessors above cannot make: a claim can be "granted"
// by packload and still never reach a container flag.
//
// hostFileArgs and hostMountArgs both read HonoredHostFiles/HonoredMounts, so a gate
// reintroduced in packload turns this red as well — the point of driving it from here is that
// a gate reintroduced at the ASSEMBLY layer (an `if` in either function) turns it red too, and
// nothing in packload would notice.
func TestFetchedPackHostGrantsReachTheContainerArgv(t *testing.T) {
	home := packHome(t)
	if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte("machine acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "datasets", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(
		`{"name":"acme","contributes":[`+
			`{"kind":"reads-host","host":".netrc"},`+
			`{"kind":"mount","host":"datasets/acme","into":"acme"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}

	o := &Options{}
	in := &assembleInput{
		wsState:      filepath.Join(home, ".yolo", "home"),
		mountTargets: map[string]struct{}{},
		packs:        []*packload.Pack{p},
	}
	fileArgs := strings.Join(o.hostFileArgs(in), " ")
	if !strings.Contains(fileArgs, packload.CtxRoot+"/host-acme/.netrc") {
		t.Errorf("the reads-host grant never became a mount:\n%s", fileArgs)
	}
	mountArgs := strings.Join(o.hostMountArgs(in), " ")
	if !strings.Contains(mountArgs, "/ctx/acme") {
		t.Errorf("the mount grant never became a mount:\n%s", mountArgs)
	}
}

// THE BRIEFING OVERLAY, the fifth gated claim and the one that had no reporter at all before
// OQ-TP6: a pack's `after: "host:<path>"` prepends the user's own file to the pack's prose,
// and the launch used to withhold it silently for a fetched pack, in one `&& p.MayAccessHost`
// inside prepare.go.
//
// Asserted on the briefing that gets WRITTEN, not on briefingDestinations, because the gate
// lived in the composition loop and nowhere else — a destination can carry the right `after`
// and still have it dropped one line later.
func TestFetchedPackBriefingOverlayPrependsTheUsersOwnFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("MY OWN RULES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("pack prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(
		`{"name":"acme","contributes":[{"kind":"briefing","from":"AGENTS.md",`+
			`"into":"AGENTS.md","after":"host:AGENTS.md"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}

	o := goldenOptions(t.TempDir(), home)
	staging, err := o.refreshJailBriefings("yolo-ws-tp9", newConfig(), "podman",
		stagedPacks{packs: []*packload.Pack{p}})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName("AGENTS.md")))
	if err != nil {
		t.Fatalf("no briefing was written: %v", err)
	}
	if !strings.Contains(string(body), "MY OWN RULES") {
		t.Errorf("the user's own AGENTS.md was not prepended to a fetched pack's prose:\n%s\n"+
			"That silence is the defect OQ-TP6 named and OQ-TP9 removed the cause of — a pack "+
			"whose only host claim is \"prepend my own file\" produced a jail with the pack's "+
			"prose and none of the user's, with nothing anywhere saying so", body)
	}

	// THE CONTROL, and it is not a formality: without it "the host file is in there" passes
	// for a build that prepends every host AGENTS.md to every briefing whether or not a pack
	// asked. The same fixture minus the `after` must NOT carry it.
	plainRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(plainRoot, "AGENTS.md"), []byte("pack prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plainRoot, "pack.json"), []byte(
		`{"name":"acme","contributes":[{"kind":"briefing","from":"AGENTS.md",`+
			`"into":"AGENTS.md"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	plain, probs := packload.LoadDir(plainRoot, "acme")
	if len(probs) > 0 {
		t.Fatalf("control fixture: %v", probs)
	}
	staging2, err := o.refreshJailBriefings("yolo-ws-tp9b", newConfig(), "podman",
		stagedPacks{packs: []*packload.Pack{plain}})
	if err != nil {
		t.Fatalf("refreshJailBriefings (control): %v", err)
	}
	body2, err := os.ReadFile(filepath.Join(staging2, briefingStagingName("AGENTS.md")))
	if err != nil {
		t.Fatalf("no control briefing was written: %v", err)
	}
	if strings.Contains(string(body2), "MY OWN RULES") {
		t.Errorf("a pack that declared NO `after` still got the host file prepended:\n%s\n"+
			"The assertion above cannot tell that apart from the feature working", body2)
	}
}

// A WRAPPED PLUGIN'S HOOK BODIES reach the jail, and the footprint marks them as code.
//
// The DELIVERY was never actually gated on this path — a plugin travels INSIDE the pack's
// skills tree, which the launch stages whole — which is why OQ-TP6 added HonoredPlugins to
// the refusal fold rather than to a delivery gate. With the fold gone, the hooks arrive and
// the footprint's ⚠ RUNS CODE line is what a user has instead.
//
// ⚠ AND THAT LINE IS NOT IN THE LAUNCH BANNER, which is a real gap and predates this ruling:
// a plugin claim is reported under KindSkills, and packloopholes.go classifies that kind
// disclosureSkip, so `yolo pack footprint` shows it and no launch does. Under the old gate a
// FETCHED pack's hooks were refused (so there was nothing to disclose) and an embedded pack's
// were nobody's concern; now every pack's arrive with only an on-demand report. Asserted
// against the footprint here because that is what exists — see the report accompanying
// OQ-TP9's implementation for the follow-up.
func TestWrappedPluginHooksAreDeliveredAndDisclosed(t *testing.T) {
	root := t.TempDir()
	md := filepath.Join(root, "skills", "acme-tools", ".claude-plugin")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"),
		[]byte(`{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"acme","contributes":[{"kind":"skills","from":"skills",`+
			`"into":".claude/skills"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	granted, refused := p.HonoredPlugins()
	if len(refused) != 0 {
		t.Errorf("a wrapped plugin's components were refused: %v", refused)
	}
	if len(granted) != 1 || !granted[0].RunsCode() {
		t.Fatalf("the plugin's code-running components did not survive: %+v", granted)
	}
	var claim *packload.Claim
	fp := packload.FootprintOf(p)
	for i := range fp.Claims {
		if fp.Claims[i].Target == "plugin:acme-tools" {
			claim = &fp.Claims[i]
		}
	}
	if claim == nil {
		t.Fatal("the wrapped plugin has no footprint claim — with the refusal gone, `yolo " +
			"pack footprint` is the only place a user learns this pack ships a hook their " +
			"agent will run at every tool call")
	}
	if !claim.ReviewWorthy || !strings.Contains(claim.Detail, "hooks") {
		t.Errorf("the plugin claim does not mark the hook as code to review: %+v", *claim)
	}
}

// EVERY host-crossing claim IS disclosed, for every pack — the inverse of
// TestNoHostCrossingClaimIsDisclosedForAnUnapprovedPack, which asserted that each of these
// was WITHHELD from the banner because it was about to be refused.
//
// Table-driven over the classes rather than one case per kind, for the reason the retired
// version gave and which survives the inversion: "which claims reach the banner" is a rule
// about host-crossing CLASSES, and stating it once is what stops the next crossing kind from
// being dropped silently.
func TestEveryHostCrossingClaimIsDisclosed(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		body     string
		fragment string
	}{
		{"reads-host", `{"kind":"reads-host","host":".netrc"}`, ".netrc"},
		{"mount", `{"kind":"mount","host":"datasets/acme","into":"acme"}`, "datasets/acme"},
		{"program installer", `{"kind":"program","bin":"acme","via":"installer",` +
			`"url":"https://acme.test/i.sh"}`, "acme.test/i.sh"},
		// "~/AGENTS.md", not the manifest's "host:AGENTS.md": the banner renders a claim as a
		// SENTENCE, and pack-execution-trust.md §6 requires a host path show the root it is
		// relative to rather than the prefix a manifest spells it with.
		{"briefing after host", `{"kind":"briefing","into":"AGENTS.md","after":"host:AGENTS.md"}`,
			"~/AGENTS.md"},
		// env was the NAMED EXCEPTION in the retired version — never origin-gated, so it was
		// the one claim an unapproved pack still got and the banner still printed. It is now
		// unexceptional, and is kept as the control: it must still print.
		{"env", `{"kind":"env","vars":{"ACME":"1"}}`, "ACME"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "pack.json"),
				[]byte(`{"contributes":[`+tc.body+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			p, probs := packload.LoadDir(root, "acme")
			if len(probs) > 0 {
				t.Fatalf("fixture: %v", probs)
			}
			var joined string
			for _, class := range []disclosureClass{disclosureRead, disclosureExec} {
				joined += renderLines(disclosedClaims([]*packload.Pack{p}, class))
			}
			if !strings.Contains(joined, tc.fragment) {
				t.Errorf("the launch does not disclose the pack's %s claim:\n%s\n"+
					"Since OQ-TP9 nothing is withheld, so a claim missing from the banner is a "+
					"crossing that happens with nobody told — which is strictly worse than the "+
					"gate that was deleted", tc.kind, joined)
			}
		})
	}
}

// AN ABSENT BIND SOURCE IS STILL SKIPPED, NOT REFUSED — the one guard the ruling did not
// touch, kept because a wider reading of "nothing is refused any more" would sweep it up.
//
// The line that separates them: the deleted gate was about a claim yolo UNDERSTOOD and
// DECLINED. A host path that is not there is neither, and making it fatal would refuse
// launches on any machine where an optional host dir does not exist.
//
// TWO DIRECTIONS, and the second is what makes the first mean anything. Without it "skipped"
// degenerates into "bind mounts do not work", which passes the first direction perfectly.
// YOLO_VERSION is cleared because the argv is a HOST-side computation: in-jail a loophole
// with bind mounts is Active() only if one of its CONTAINER paths already exists, so the
// whole loophole would drop out before runtimeArgsFor looked at a single source.
func TestAbsentBindSourceIsSkippedNotRefused(t *testing.T) {
	os.Unsetenv("YOLO_VERSION")
	isolatePackModules(t)
	fakeLoopholes(t)
	packRoot := t.TempDir()
	mod := filepath.Join(packRoot, "loopholes", "acme-proxy")
	if err := os.MkdirAll(filepath.Join(mod, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(
		`{"name":"acme-proxy","transport":"none","default_enabled":true,`+
			`"host_daemon":{"cmd":["/bin/true"],"publishes":"socket"},`+
			`"host_bind_mounts":[{"host":"{loophole_dir}/conf","container":"/etc/acme-present"},`+
			`{"host":"{loophole_dir}/gone","container":"/etc/acme-absent"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), []byte(
		`{"name":"acme","contributes":[{"kind":"loophole","from":"loopholes/acme-proxy"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	absentSrc := filepath.Join(mod, "gone")
	p, probs := packload.LoadDir(packRoot, "acme")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{p}))
	argv := strings.Join((&Options{}).loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman"), " ")
	if strings.Contains(argv, absentSrc) {
		t.Errorf("the absent bind source reached the container argv:\n%s\nNothing refuses over "+
			"it, so something has to drop it — and podman's own error for a nonexistent -v "+
			"source is a boot failure naming neither the pack nor the loophole", argv)
	}
	if !strings.Contains(argv, "/etc/acme-present") {
		t.Errorf("the PRESENT bind mount beside it did not cross either:\n%s\nSkipping what is "+
			"missing is adaptation; skipping what is there is a loophole that does not work, "+
			"and the assertion above cannot tell the two apart on its own", argv)
	}
}

// A CONTRIBUTION FROM THE FUTURE is still skipped and never fatal. Kept from the retired file
// because the guard is about SKEW TOLERANCE, not about consent: the host CLI is freshly built
// and the entrypoint is baked into the image at the last `just load`, so an unknown kind must
// cost a warning rather than a jail that will not start.
//
// It is now asserted on the LOAD rather than on packRefusals (which is deleted with the gate):
// a Pack carrying SkewNotes must still stage, and the notes must survive to be reported.
func TestSkewNotesAreNotFatal(t *testing.T) {
	future := &packload.Pack{
		Name: "acme", Decl: &packdecl.Manifest{},
		SkewNotes: []string{`pack acme: contribution 1: unknown kind "telepathy" — skipped`},
	}
	if fp := packload.FootprintOf(future); len(fp.Claims) != 0 {
		t.Errorf("a skipped contribution produced claims: %+v", fp.Claims)
	}
	if len(future.SkewNotes) != 1 {
		t.Error("the skew note vanished — a degraded jail must stay visible")
	}
}
