package run

// workspaceskills_test.go is the CALL-SITE half of the workspace skills layer
// (docs/reference/agent-briefings.md): every test here drives the REAL staging path — the shipped
// packs' own pack.json files, stagePacks, then refreshJailBriefings, which every launch, every
// attach and the macos-user arm call — and reads what landed in the staging dirs a jail binds.
// internal/jailcontent/workspaceskills_test.go pins the reader itself; these fail if the launcher
// stops handing it the workspace, the shipped declarations, the skip rule, the jail's view of the
// tree, or stops SAYING what happened.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const wsSkillsCname = "yolo-test-wsskills"

// wsSkillsLaunch is a temp HOME selecting `packs` (shipped names or JSON entries), a workspace,
// and Options whose stderr is captured.
func wsSkillsLaunch(t *testing.T, packsJSON string) (*Options, string, *bytes.Buffer) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, packsJSON)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	o.Stdout = discardBuf()
	stderr := &bytes.Buffer{}
	o.Stderr = stderr
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })
	return o, ws, stderr
}

// wsWrite writes rel under root.
func wsWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wsLink(t *testing.T, root, rel, target string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// stageWorkspaceSkills runs stagePacks then refreshJailBriefings on rt and returns the staging
// root.
func stageWorkspaceSkills(t *testing.T, o *Options, rt string) string {
	t.Helper()
	root, packs, briefings, err := o.stagePacks(wsSkillsCname)
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	staging, err := o.refreshJailBriefings(wsSkillsCname, jsonx.NewOrderedMap(), rt,
		stagedPacks{root: root, packs: packs, briefings: briefings}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	return staging
}

// has reports whether pack's staging dir holds a skill directory called name.
func has(t *testing.T, staging, pack, name string) bool {
	t.Helper()
	fi, err := os.Stat(filepath.Join(staging, jailcontent.SkillStagingName(pack), name))
	return err == nil && fi.IsDir()
}

// THE MOTIVATING CASE AND THE SKIP RULE, through the shipped packs' own declarations: a repo that
// ships `.claude/skills/review/` and `.agents/skills/lint/` reaches every agent exactly once.
// claude reads .claude/skills natively (no copy of review); pi, agy and codex read .agents/skills
// natively (no copy of lint), and none of them reads .claude/skills, so each gets review.
func TestWorkspaceSkillsReachEveryAgentOnceThroughTheShippedDeclarations(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["claude", "codex", "pi", "agy"]`)
	wsWrite(t, ws, ".claude/skills/review/SKILL.md", "---\nname: review\n---\n")
	wsWrite(t, ws, ".agents/skills/lint/SKILL.md", "---\nname: lint\n---\n")

	staging := stageWorkspaceSkills(t, o, "podman")

	for _, c := range []struct {
		pack, skill string
		want        bool
		why         string
	}{
		{"claude", "review", false, "claude reads .claude/skills natively"},
		{"claude", "lint", true, "claude does not read .agents/skills"},
		{"codex", "review", true, "codex does not read .claude/skills"},
		{"codex", "lint", false, "codex reads .agents/skills natively (0.157's repo skills root)"},
		{"pi", "review", true, "pi does not read .claude/skills"},
		{"pi", "lint", false, "pi reads .agents/skills natively and deduplicates by REAL path"},
		{"agy", "review", true, "agy does not read .claude/skills"},
		{"agy", "lint", false, "agy reads .agents/skills natively"},
	} {
		if got := has(t, staging, c.pack, c.skill); got != c.want {
			t.Errorf("%s has %s = %v, want %v: %s", c.pack, c.skill, got, c.want, c.why)
		}
	}
	for _, want := range []string{
		"Workspace skills from .claude/skills mirrored into codex, pi, agy: review",
		"Workspace skills from .agents/skills mirrored into claude: lint",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the launch must say what the workspace delivered; want %q in:\n%s", want, stderr)
		}
	}
}

// OQ-WS3: the source set is every SHIPPED pack's declaration, selected or not. A codex-only jail
// gets a repo's `.github/skills/` (declared only by copilot) and `.opencode/skills/` (only by
// opencode).
func TestWorkspaceSkillsComeFromPathsOnlyAnUnselectedPackDeclares(t *testing.T) {
	o, ws, _ := wsSkillsLaunch(t, `["codex"]`)
	wsWrite(t, ws, ".github/skills/gh-only/SKILL.md", "gh")
	wsWrite(t, ws, ".opencode/skills/oc-only/SKILL.md", "oc")

	staging := stageWorkspaceSkills(t, o, "podman")
	for _, name := range []string{"gh-only", "oc-only"} {
		if !has(t, staging, "codex", name) {
			t.Errorf("codex never received %s — the source set must not depend on which packs "+
				"are selected (OQ-WS3)", name)
		}
	}
}

// OQ-WS2 at the launch: the workspace can add but never shadow, and each shadowed name is said
// ONCE, however many destinations it was shadowed in.
func TestAWorkspaceSkillNeverShadowsABuiltinAndTheLaunchSaysSoOnce(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["codex", "pi"]`)
	wsWrite(t, ws, ".claude/skills/configuring-the-jail/SKILL.md", "the workspace's own version")

	staging := stageWorkspaceSkills(t, o, "podman")

	want, _ := builtinskills.FS.ReadFile("configuring-the-jail/SKILL.md")
	for _, pack := range []string{"codex", "pi"} {
		got, err := os.ReadFile(filepath.Join(staging, jailcontent.SkillStagingName(pack),
			"configuring-the-jail", "SKILL.md"))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: the workspace replaced yolo's built-in configuring-the-jail", pack)
		}
	}
	if n := strings.Count(stderr.String(), `"configuring-the-jail" from .claude/skills is shadowed by yolo's built-in skill`); n != 1 {
		t.Errorf("want exactly one shadow line for the name, got %d in:\n%s", n, stderr)
	}
}

// The shadow line names the PACK that took the name — the one field (PackSkillSource.Pack) the
// launcher adds when it converts a pack's sources.
func TestAWorkspaceSkillShadowedByAPackNamesThePack(t *testing.T) {
	house := filepath.Join(t.TempDir(), "house")
	wsWrite(t, house, "skills/house-rule/SKILL.md", "the pack's")
	o, ws, stderr := wsSkillsLaunch(t, `["codex", {"source":"file://`+house+`","name":"house"}]`)
	wsWrite(t, ws, ".claude/skills/house-rule/SKILL.md", "the workspace's")

	staging := stageWorkspaceSkills(t, o, "podman")
	got, _ := os.ReadFile(filepath.Join(staging, jailcontent.SkillStagingName("codex"), "house-rule", "SKILL.md"))
	if string(got) != "the pack's" {
		t.Errorf("the workspace replaced a pack's skill: %q", got)
	}
	if !strings.Contains(stderr.String(), `"house-rule" from .claude/skills is shadowed by pack house's skill`) {
		t.Errorf("the shadow must name the pack; stderr:\n%s", stderr)
	}
}

// P5 AT THE LAUNCH: a committed `.agents/skills/x/SKILL.md → ~/.ssh/config`. The jail starts, no
// staged byte is the secret's, and the launch names the refused link.
func TestAnEscapingWorkspaceSymlinkIsRefusedAndNamedAtLaunch(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["codex", "claude"]`)
	home := os.Getenv("HOME")
	const secret = "Host *\n  IdentityFile ~/.ssh/the-key-that-must-not-cross\n"
	wsWrite(t, home, ".ssh/config", secret)
	wsLink(t, ws, ".agents/skills/x/SKILL.md", filepath.Join(home, ".ssh", "config"))
	wsLink(t, ws, ".claude/skills", filepath.Join(home, ".ssh"))

	staging := stageWorkspaceSkills(t, o, "podman")

	_ = filepath.WalkDir(filepath.Join(paths.AgentsDir(), wsSkillsCname), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if data, rerr := os.ReadFile(p); rerr == nil && bytes.Contains(data, []byte("the-key-that-must-not-cross")) {
			t.Errorf("a host file outside the workspace was staged for the jail at %s", p)
		}
		return nil
	})
	if _, err := os.Lstat(filepath.Join(staging, jailcontent.SkillStagingName("codex"), "x", "SKILL.md")); err == nil {
		t.Error("the escaping SKILL.md was staged")
	}
	for _, want := range []string{
		"Workspace skills: refused .agents/skills/x/SKILL.md — a symlink that resolves outside the workspace",
		"Workspace skills: refused .claude/skills — a symlink that resolves outside the workspace",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("want %q in:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr.String(), ".ssh") {
		t.Errorf("a refusal names the entry, never its target:\n%s", stderr)
	}
}

// The container's per-side shadows are what the jail does NOT see of the host's tree, so a link
// into one is refused there — and followed on macos-user, which shadows nothing.
func TestPerSidePathsAreNotMirroredIntoAContainer(t *testing.T) {
	for _, tc := range []struct {
		rt      string
		refused bool
	}{{"podman", true}, {"macos-user", false}} {
		t.Run(tc.rt, func(t *testing.T) {
			o, ws, stderr := wsSkillsLaunch(t, `["codex"]`)
			wsWrite(t, ws, "node_modules/pkg/helper.js", "host-built")
			wsWrite(t, ws, ".claude/skills/x/SKILL.md", "x")
			wsLink(t, ws, ".claude/skills/x/lib", "../../../node_modules/pkg")

			staging := stageWorkspaceSkills(t, o, tc.rt)
			_, err := os.Stat(filepath.Join(staging, jailcontent.SkillStagingName("codex"), "x", "lib", "helper.js"))
			if got := err != nil; got != tc.refused {
				t.Errorf("lib/helper.js refused=%v, want %v; stderr:\n%s", got, tc.refused, stderr)
			}
			if said := strings.Contains(stderr.String(), "refused .claude/skills/x/lib — resolves into node_modules, a per-side path"); said != tc.refused {
				t.Errorf("per-side refusal named=%v, want %v; stderr:\n%s", said, tc.refused, stderr)
			}
		})
	}
}

// An absolute link the agent wrote INSIDE the jail names the workspace by the jail's mount path;
// in a container that is followed into the workspace, and never read from the host's own path of
// that name.
func TestAJailSpelledLinkIsReadAsTheWorkspace(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["codex"]`)
	wsWrite(t, ws, "docs/ref.md", "the workspace's doc")
	wsWrite(t, ws, ".claude/skills/x/SKILL.md", "x")
	wsLink(t, ws, ".claude/skills/x/ref.md", jailWorkspace+"/docs/ref.md")

	staging := stageWorkspaceSkills(t, o, "podman")
	got, err := os.ReadFile(filepath.Join(staging, jailcontent.SkillStagingName("codex"), "x", "ref.md"))
	if err != nil || string(got) != "the workspace's doc" {
		t.Errorf("a %s/… link should read the workspace, got %q, %v; stderr:\n%s", jailWorkspace, got, err, stderr)
	}
}

// macos-user receives the layer through the SAME composed tree it already copies: the home
// overlay Run hands the backend holds the workspace skill at pi's destination.
func TestWorkspaceSkillsReachTheMacosUserHome(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["pi"]`)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsWrite(t, ws, ".claude/skills/review/SKILL.md", "---\nname: review\n---\n")

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, homeOverlay macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		reached = true
		if _, err := os.Stat(filepath.Join(homeOverlay.Tree, ".pi", "agent", "skills", "review", "SKILL.md")); err != nil {
			t.Errorf("the workspace skill never reached the macos-user home overlay: %v", err)
		}
		// And its destination is LISTED: the sandbox install replaces only what the overlay's
		// destination list names (G36), and the profile protects only Dests (G14), so a
		// mirrored skill outside both would be neither delivered nor write-protected.
		body, err := os.ReadFile(filepath.Join(homeOverlay.Tree, entrypoint.HomeOverlayManifestName))
		if err != nil {
			t.Errorf("the macos-user home overlay carries no destination list: %v", err)
		}
		var listed struct {
			Destinations []string `json:"destinations"`
		}
		_ = json.Unmarshal(body, &listed)
		if !containsStr(listed.Destinations, ".pi/agent/skills") {
			t.Errorf("the destination list %v does not name .pi/agent/skills, where the workspace "+
				"skill is laid out", listed.Destinations)
		}
		if !reflect.DeepEqual(listed.Destinations, homeOverlay.Dests) {
			t.Errorf("the profile protects %v and the install replaces %v", homeOverlay.Dests, listed.Destinations)
		}
		return 0
	}
	if rc := Run(*o); rc != 0 || !reached {
		t.Fatalf("Run() = %d, reached=%v\nstdout:\n%s\nstderr:\n%s", rc, reached, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Workspace skills from .claude/skills mirrored into pi: review") {
		t.Errorf("the macos-user arm must say what it mirrored too:\n%s", stderr.String())
	}
}

// RE-STAGED ON EVERY ENTRY: refreshJailBriefings is what an attach runs, so a second call after a
// workspace edit is the attach's view.
func TestAnAttachReStagesTheWorkspaceAsItStands(t *testing.T) {
	o, ws, _ := wsSkillsLaunch(t, `["codex"]`)
	wsWrite(t, ws, ".claude/skills/x/SKILL.md", "v1")
	staging := stageWorkspaceSkills(t, o, "podman")
	wsWrite(t, ws, ".claude/skills/x/SKILL.md", "v2")
	wsWrite(t, ws, ".claude/skills/new/SKILL.md", "n")
	stageWorkspaceSkills(t, o, "podman")

	got, _ := os.ReadFile(filepath.Join(staging, jailcontent.SkillStagingName("codex"), "x", "SKILL.md"))
	if string(got) != "v2" || !has(t, staging, "codex", "new") {
		t.Errorf("the second entry did not re-stage the workspace: x=%q new=%v", got, has(t, staging, "codex", "new"))
	}
}

// Mechanism A writes nothing into the repo: after a launch with every shape of source, the
// workspace tree is unchanged — `git status` would show nothing.
func TestTheWorkspaceLayerWritesNothingIntoTheWorkspace(t *testing.T) {
	o, ws, _ := wsSkillsLaunch(t, `["claude", "codex", "pi"]`)
	wsWrite(t, ws, ".claude/skills/review/SKILL.md", "r")
	wsWrite(t, ws, ".agents/skills/lint/SKILL.md", "l")
	wsLink(t, ws, ".agents/skills/lint/out", "/etc")
	before := wsTreeListing(t, ws)
	stageWorkspaceSkills(t, o, "podman")
	if after := wsTreeListing(t, ws); after != before {
		t.Errorf("the launch changed the workspace:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func wsTreeListing(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == ".yolo" || strings.HasPrefix(rel, ".yolo"+string(filepath.Separator)) {
			return nil // the state dir is yolo's by design (storage-and-config.md)
		}
		fi, _ := os.Lstat(p)
		b.WriteString(rel + " " + fi.Mode().String() + "\n")
		return nil
	})
	return b.String()
}

// A workspace with no skills directory says NOTHING — the common case must be silent, or the
// lines that matter stop being read.
func TestAWorkspaceWithNoSkillsSaysNothing(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["codex", "claude"]`)
	wsWrite(t, ws, "README.md", "a repo with no skills")
	stageWorkspaceSkills(t, o, "podman")
	if strings.Contains(stderr.String(), "Workspace skills") {
		t.Errorf("a workspace with no skills dir must be silent:\n%s", stderr)
	}
}

// The attach path's pack records carry the pack name too, or an attach's shadow line would say
// "a pack" where the fresh launch named it.
func TestAdoptedPackRecordsNameTheirPack(t *testing.T) {
	house := filepath.Join(t.TempDir(), "house")
	wsWrite(t, house, "skills/house-rule/SKILL.md", "x")
	p, probs := packload.LoadDir(house, "house")
	if len(probs) != 0 {
		t.Fatalf("LoadDir: %v", probs)
	}
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil) })
	adoptPackRecords([]*packload.Pack{p})
	got := jailcontent.PackSkillDirs()
	if len(got) != 1 || got[0].Pack != "house" {
		t.Errorf("adoptPackRecords must carry the pack's name: %+v", got)
	}
}

// A disclosure line cannot be forged by a name: a skill directory named with a newline and
// console markup is printed quoted, on its own line.
func TestAWorkspaceNameCannotForgeADisclosureLine(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["codex"]`)
	evil := "x\n[dim]Workspace skills: nothing refused"
	wsWrite(t, ws, ".claude/skills/"+evil+"/SKILL.md", "x")
	stageWorkspaceSkills(t, o, "podman")
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "[dim]Workspace skills: nothing refused") ||
			strings.HasPrefix(line, "Workspace skills: nothing refused") {
			t.Errorf("a skill name forged its own disclosure line:\n%s", stderr)
		}
	}
	if !strings.Contains(stderr.String(), `"x\n\x5bdim]Workspace skills: nothing refused"`) {
		t.Errorf("the name should be printed Go-quoted with its bracket escaped:\n%s", stderr)
	}
}

// A `git clone` can carry a path longer than PATH_MAX inside a skill, and the reader reaches it.
// That used to fail the launch — and every attach after it — with ENAMETOOLONG and leave the
// scratch tree in $TMPDIR. Through the real refresh the launch now goes ahead, the entry is named,
// and no scratch tree remains.
func TestADeepPathInAWorkspaceSkillRefusesTheEntryNotTheLaunch(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["claude"]`)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	wsWrite(t, ws, ".agents/skills/x/SKILL.md", "x")
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	deep := ".agents/skills/x/" + strings.Repeat(strings.Repeat("b", 200)+"/", 22)
	if err := root.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(deep+"leaf.md", []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}

	staging := stageWorkspaceSkills(t, o, "podman")

	if !has(t, staging, "claude", "x") {
		t.Errorf("the rest of the skill should still reach claude; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr.String(), "longer than 512 bytes") {
		t.Errorf("the launch must name the entry it refused:\n%s", stderr)
	}
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "yolo-workspace-skills-") {
			t.Errorf("the launch left its scratch tree behind: %s", e.Name())
		}
	}
}

// The REASON of a refusal is a disclosure's text too. A link whose target is a long component
// then a newline and a counterfeit line fails with a path error that spells the target, and a
// per-side path from the repo's own mise.toml is named in the per-side refusal: neither may
// forge a line, and the first may not leak the target either (a refusal names the entry, never
// its target).
func TestARefusalReasonCannotForgeADisclosureLine(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["claude"]`)
	forged := "Workspace skills: nothing was refused"
	wsWrite(t, ws, ".agents/skills/x/SKILL.md", "x")
	wsLink(t, ws, ".agents/skills/x/long.md", strings.Repeat("A", 300)+"\n"+forged)
	evilVenv := "venv\n" + forged
	wsWrite(t, ws, "mise.toml", "[env._.python]\nvenv = \"venv\\n"+forged+"\"\n")
	wsWrite(t, ws, evilVenv+"/lib/helper.py", "host-built")
	wsLink(t, ws, ".agents/skills/x/lib", "../../../"+evilVenv+"/lib")

	stageWorkspaceSkills(t, o, "podman")

	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), forged) {
			t.Errorf("a refusal's reason forged its own disclosure line:\n%s", stderr)
		}
	}
	if strings.Contains(stderr.String(), "AAAAAAAA") {
		t.Errorf("a refusal printed the text of the link's target:\n%s", stderr)
	}
	for _, want := range []string{"refused .agents/skills/x/long.md — unreadable", "refused .agents/skills/x/lib — "} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("want %q in:\n%s", want, stderr)
		}
	}
}

// R4 AT THE GRAIN OF A NAME, through the shipped declarations and in both orders: `.claude/skills`
// and `.agents/skills` both carry a `lint`, with different content. claude reads the first
// natively and pi the second, so whichever copy wins the name, the agent reading the OTHER copy
// natively must not be sent the winner — it would see two skills called lint — and the collision
// line says which agent reads the losing copy.
func TestAnAgentReadingALosingCopyNativelyIsSentNoOther(t *testing.T) {
	for _, tc := range []struct {
		packs, winner, loser, reader string
	}{
		{`["claude", "pi"]`, ".claude/skills", ".agents/skills", "pi"},
		{`["pi", "claude"]`, ".agents/skills", ".claude/skills", "claude"},
	} {
		t.Run(tc.packs, func(t *testing.T) {
			o, ws, stderr := wsSkillsLaunch(t, tc.packs)
			wsWrite(t, ws, ".claude/skills/lint/SKILL.md", "CLAUDE")
			wsWrite(t, ws, ".agents/skills/lint/SKILL.md", "AGENTS")

			staging := stageWorkspaceSkills(t, o, "podman")

			for _, pack := range []string{"claude", "pi"} {
				if has(t, staging, pack, "lint") {
					t.Errorf("%s reads a lint natively and was sent another; stderr:\n%s", pack, stderr)
				}
			}
			want := `"lint" is in both ` + tc.winner + ` and ` + tc.loser + ` — the copy in ` + tc.winner +
				` is the one delivered; ` + tc.reader + ` reads the copy in ` + tc.loser + ` natively and is sent no other`
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("want %q in:\n%s", want, stderr)
			}
		})
	}
}

// §10's fifth bullet, in its exact form: a repo with `.agents/skills/configuring-the-jail/` and
// an agent that reads `.agents/skills` natively. yolo still stages its built-in under that name;
// the agent also sees the repo's copy natively, which yolo cannot prevent, and the launch says so.
func TestANativelyReadSkillUnderABuiltinNameIsSaid(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["pi"]`)
	wsWrite(t, ws, ".agents/skills/configuring-the-jail/SKILL.md", "the repo's own version")

	staging := stageWorkspaceSkills(t, o, "podman")

	want, _ := builtinskills.FS.ReadFile("configuring-the-jail/SKILL.md")
	got, err := os.ReadFile(filepath.Join(staging, jailcontent.SkillStagingName("pi"), "configuring-the-jail", "SKILL.md"))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("pi's staged configuring-the-jail must be yolo's built-in")
	}
	line := "Workspace skills: pi reads .agents/skills/configuring-the-jail natively, so yolo cannot keep it " +
		"from competing with yolo's built-in skill of that name"
	if !strings.Contains(stderr.String(), line) {
		t.Errorf("want %q in:\n%s", line, stderr)
	}
}

// The held-back line, where no collision says it: the repo's `.agents/skills/x` is a link into
// node_modules — refused in a container, whose jail sees its own node_modules there, and where pi
// resolves it natively to whatever the jail has. So `.claude/skills/x` is not sent to pi, and
// since the refused entry was never a copy to collide with, the launch says so on its own line.
func TestAHeldBackSkillIsSaidWhenNoCollisionSaysIt(t *testing.T) {
	o, ws, stderr := wsSkillsLaunch(t, `["pi"]`)
	wsWrite(t, ws, "node_modules/x/SKILL.md", "the host's per-side copy")
	wsLink(t, ws, ".agents/skills/x", "../../node_modules/x")
	wsWrite(t, ws, ".claude/skills/x/SKILL.md", "claude's x")

	staging := stageWorkspaceSkills(t, o, "podman")

	if has(t, staging, "pi", "x") {
		t.Errorf("pi reads .agents/skills/x natively and was sent claude's x too; stderr:\n%s", stderr)
	}
	line := `Workspace skills: "x" from .claude/skills was not sent to pi, which reads a skill of that name in .agents/skills natively`
	if !strings.Contains(stderr.String(), line) {
		t.Errorf("want %q in:\n%s", line, stderr)
	}
}
