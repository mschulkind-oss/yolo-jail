package run

// skillcollision_test.go pins OQ-NC11 (docs/plans/notch-convergence.md#OQ-NC11, ruled 2026-09-28
// by parity) through the production call sites: a skill-name collision refuses a jail launch
// host-side and before any container work, an attach too, with the host's message; and the jail
// stages skills through the host's layer plan, so a namespaced pack's skill is /<pack>:<skill> in
// a jail — while the protections the jail's own staging had (the reserved child, the layer order,
// the workspace layer's confinement and cap) still hold.
//
// Everything drives Run, stagePacks or refreshJailBriefings, never hostskills.Collisions or
// jailcontent alone: a test of the callee stays green with the call site deleted.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// collidingSkillPacks configures the S5 measurement's shape: the claude pack, a shared pack
// shipping the skill `dup`, and the conventional local pack shipping its own `dup`. Returns the
// shared pack's directory.
func collidingSkillPacks(t *testing.T) (home, shared string) {
	t.Helper()
	home = packHome(t)
	localPackTree(t, home, "dup", "PERSONAL")
	shared = filepath.Join(t.TempDir(), "shared")
	writeSkillTree(t, filepath.Join(shared, "skills"), "dup")
	writeUserPacks(t, home, `["claude",{"source":"file://`+shared+`","name":"shared"}]`)
	return home, shared
}

// assertSkillCollisionRefusal checks the refusal carries the host's message: the destination,
// both packs, the shared pack's own source path, and both remedies.
func assertSkillCollisionRefusal(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{
		"packs: skills: refusing to compose",
		`~/.claude/skills: 2 packs both want the entry "dup"`,
		"pack shared",
		"pack local",
		filepath.Join("shared", "skills", "dup"),
		"RENAME one of them",
		`"skills_tier": "namespaced"`,
		"/<pack>:<skill>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// noContainerWork fails the test for any container verb that makes, changes or enters a container.
// A read-only `ps` is not one: the launch asks the runtime about stale containers before it
// stages, and the ruling's line is that no container EXISTS for a refused launch.
func noContainerWork(t *testing.T, execRec [][]string) {
	t.Helper()
	for _, argv := range execRec {
		if len(argv) < 2 {
			continue
		}
		switch argv[1] {
		case "run", "create", "start", "exec", "rm", "load":
			t.Errorf("the launch reached container work (%v) despite the skills collision", argv)
		}
	}
}

// THE RULING'S FIRST HALF: a launch refuses host-side, before the container exists.
func TestALaunchRefusesTwoPacksShippingOneSkillName(t *testing.T) {
	collidingSkillPacks(t)
	var stdout, stderr bytes.Buffer
	var execRec [][]string
	o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, &execRec)

	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1: two packs shipping `dup` to ~/.claude/skills must refuse the "+
			"launch\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	assertSkillCollisionRefusal(t, stdout.String())
	noContainerWork(t, execRec)
}

// AND AN ATTACH: a jail of this workspace is running, and the entry still refuses before it
// attaches — the pre-flight runs where the pack set becomes complete, which every invocation
// passes through before the attach decision.
func TestAnAttachRefusesTwoPacksShippingOneSkillName(t *testing.T) {
	collidingSkillPacks(t)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var stdout, stderr bytes.Buffer
	var execRec [][]string
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, &execRec)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		execRec = append(execRec, argv)
		switch {
		case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(strings.Join(argv, " "), "name=^/"+cname+"$"):
			return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
		case len(argv) >= 2 && argv[1] == "inspect":
			return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
		}
		return ExecResult{Ran: true, RC: 0}
	}

	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1: an attach must refuse the collision too\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "Attaching to existing jail") {
		t.Errorf("the attach went ahead:\n%s", stdout.String())
	}
	assertSkillCollisionRefusal(t, stdout.String())
	for _, argv := range execRec {
		if len(argv) >= 2 && argv[1] == "exec" {
			t.Errorf("the attach ran %v despite the collision", argv)
		}
	}
}

// agentSkillsPack writes a local agent pack declaring the destination .<name>/skills, and returns
// its config entry.
func agentSkillsPack(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, `{"name":"`+name+`","contributes":[`+
		`{"kind":"skills","into":".`+name+`/skills","agent":"`+name+`"}]}`)
	return `{"source":"file://` + dir + `","name":"` + name + `"}`
}

// stageJailSkills runs the jail content path — stagePacks, then refreshJailBriefings, which is
// what calls PrepareSkillsWith — and returns the staging root and what the launch said on stderr.
func stageJailSkills(t *testing.T, o *Options, cname string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o.Stdout, o.Stderr = &stdout, &stderr
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("stagePacks: %v\n%s", err, stdout.String())
	}
	staging, err := o.refreshJailBriefings(cname, jsonx.NewOrderedMap(), "podman",
		stagedPacks{root: root, packs: packs, briefings: briefings}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	return staging, stderr.String()
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(data)
}

// THE RULING'S SECOND HALF: the jail honors skills_tier. A namespaced pack's skill lands in a
// subtree of its own with a plugin manifest named for the pack, so it invokes as /house:review
// in a jail exactly as at the host — the remedy the refusal offers is now true here.
func TestAJailNamespacesAPackThatAskedForIt(t *testing.T) {
	home := packHome(t)
	base := t.TempDir()
	house := filepath.Join(base, "house")
	writeSkillTree(t, filepath.Join(house, "skills"), "review")
	if err := os.MkdirAll(house, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, house, `{"name":"house","description":"house rules","skills_tier":"namespaced"}`)
	writeUserPacks(t, home, "["+agentSkillsPack(t, base, "alphacli")+
		`,{"source":"file://`+house+`","name":"house"}]`)
	o := goldenOptions(t.TempDir(), home)

	staging, _ := stageJailSkills(t, o, "yolo-test-nc11-tier")
	dest := filepath.Join(staging, jailcontent.SkillStagingName("alphacli"))
	if _, err := os.Stat(filepath.Join(dest, "house", "skills", "review", "SKILL.md")); err != nil {
		t.Errorf("the namespaced pack's skill is not at house/skills/review: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "review")); !os.IsNotExist(err) {
		t.Errorf("a namespaced pack's skill was staged flat, as /review (%v)", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(readFileT(t, filepath.Join(dest, "house", ".claude-plugin", "plugin.json"))), &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "house" {
		t.Errorf("the subtree's plugin manifest names %v, want house — the invocation namespace", m["name"])
	}
	// The built-in suite is still there, beside the subtree.
	if _, err := os.Stat(filepath.Join(dest, "configuring-the-jail", "SKILL.md")); err != nil {
		t.Errorf("the built-in suite is gone: %v", err)
	}
}

// THE RESERVED CHILD, STILL FENCED. packs/claude reserves `synced`; a home an earlier apply had
// adopted the sync root on carries it in the local pack (pack-system.md, ST-R), and a clone
// can carry one in a workspace skills dir. Neither reaches the jail's ~/.claude/skills, and each
// withholding is said.
func TestAJailStillFencesAReservedChild(t *testing.T) {
	home := packHome(t)
	localPackTree(t, home, "mine", "PERSONAL")
	bucket := filepath.Join(home, ".config", "yolo-jail", "local", "skills", "synced", "1111_2222")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "SKILL.md"), []byte("identity bucket"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	wsSynced := filepath.Join(ws, ".agents", "skills", "synced")
	if err := os.MkdirAll(wsSynced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsSynced, "SKILL.md"), []byte("from a clone"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)

	staging, said := stageJailSkills(t, o, "yolo-test-nc11-reserved")
	dest := filepath.Join(staging, jailcontent.SkillStagingName("claude"))
	if _, err := os.Lstat(filepath.Join(dest, "synced")); !os.IsNotExist(err) {
		t.Errorf("the reserved child reached the jail's ~/.claude/skills (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "mine", "SKILL.md")); err != nil {
		t.Errorf("the fence cost the local pack's ordinary skill: %v", err)
	}
	for _, want := range []string{"synced", "pack local", "skills Claude Code syncs from your claude.ai account", "withheld"} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch did not say %q about the withheld child:\n%s", want, said)
		}
	}
	if !strings.Contains(said, `"synced" from .agents/skills is shadowed`) {
		t.Errorf("the workspace's `synced` was not reported as held off the reserved name:\n%s", said)
	}
}

// THE PRIORITY ORDER, UNCHANGED: the workspace < yolo's built-ins < packs, the local pack among
// them. A pack's skill replaces a built-in of its name (a legitimate reason to ship one), the
// local pack's does too, and a workspace skill fills only names nothing above took.
func TestAJailKeepsTheSkillPriorityOrder(t *testing.T) {
	home := packHome(t)
	base := t.TempDir()
	localPackTree(t, home, "diagnosing-the-jail", "LOCAL-OVERRIDE")
	house := filepath.Join(base, "house")
	for name, body := range map[string]string{"configuring-the-jail": "PACK-OVERRIDE", "house-skill": "PACK"} {
		dir := filepath.Join(house, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home, "["+agentSkillsPack(t, base, "alphacli")+
		`,{"source":"file://`+house+`","name":"house"}]`)
	ws := t.TempDir()
	for _, name := range []string{"configuring-the-jail", "house-skill", "ws-only"} {
		dir := filepath.Join(ws, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("WORKSPACE"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := goldenOptions(ws, home)

	staging, said := stageJailSkills(t, o, "yolo-test-nc11-order")
	dest := filepath.Join(staging, jailcontent.SkillStagingName("alphacli"))
	for name, want := range map[string]string{
		"configuring-the-jail": "PACK-OVERRIDE", // pack > built-in (and > workspace)
		"house-skill":          "PACK",          // pack > workspace
		"ws-only":              "WORKSPACE",     // the workspace fills a free name
	} {
		if got := readFileT(t, filepath.Join(dest, name, "SKILL.md")); got != want {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}
	if got := readFileT(t, filepath.Join(dest, "diagnosing-the-jail", "SKILL.md")); !strings.Contains(got, "LOCAL-OVERRIDE") {
		t.Errorf("the local pack's skill lost to the built-in of its name: %q", got)
	}
	for _, want := range []string{`"configuring-the-jail" from .agents/skills is shadowed by pack house's skill`,
		`"house-skill" from .agents/skills is shadowed by pack house's skill`} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch did not say %q:\n%s", want, said)
		}
	}
}

// THE WORKSPACE LAYER'S CONFINEMENT AND CAP, UNDER THE ONE COMPOSER: the pack layers moved to
// hostskills' copier, which follows links, and the workspace must not have moved with them. A
// link out of the workspace is refused and never read; a skill past the shipped byte cap is
// refused before a byte of it is read; an ordinary workspace skill still arrives.
func TestTheWorkspaceLayerStaysConfinedAndCapped(t *testing.T) {
	home := packHome(t)
	base := t.TempDir()
	writeUserPacks(t, home, "["+agentSkillsPack(t, base, "alphacli")+"]")
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("SECRET-KEY-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(".agents/skills/fine/SKILL.md", "fine")
	if err := os.MkdirAll(filepath.Join(ws, ".agents", "skills", "esc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(ws, ".agents", "skills", "esc", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	mk(".agents/skills/big/SKILL.md", "big")
	fh, err := os.Create(filepath.Join(ws, ".agents", "skills", "big", "vendored.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fh.Truncate(32<<20 + 1); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)

	staging, said := stageJailSkills(t, o, "yolo-test-nc11-ws")
	dest := filepath.Join(staging, jailcontent.SkillStagingName("alphacli"))
	if got := readFileT(t, filepath.Join(dest, "fine", "SKILL.md")); got != "fine" {
		t.Errorf("an ordinary workspace skill did not arrive: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "esc", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("a link out of the workspace was staged (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "big")); !os.IsNotExist(err) {
		t.Errorf("a skill past the byte cap was staged (%v)", err)
	}
	_ = filepath.Walk(staging, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() && info.Size() < 1<<20 {
			if data, _ := os.ReadFile(p); strings.Contains(string(data), "SECRET-KEY-BYTES") {
				t.Errorf("the host file behind the escaping link was read into %s", p)
			}
		}
		return nil
	})
	for _, want := range []string{"Workspace skills: refused .agents/skills/esc", "Workspace skills: refused .agents/skills/big",
		"32 MiB"} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch did not say %q:\n%s", want, said)
		}
	}
}

// writeWrappedPluginPack writes a pack at <base>/<name> whose skills/ carries the plugin
// acme-tools (a manifest, a nested skill and a hook), at the given skills_tier.
func writeWrappedPluginPack(t *testing.T, base, name, tier string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	plug := filepath.Join(dir, "skills", "acme-tools")
	for rel, body := range map[string]string{
		".claude-plugin/plugin.json": `{"name":"acme-tools","hooks":"./hooks/pre-tool-use.json"}`,
		"skills/nested/SKILL.md":     "from the plugin",
		"hooks/pre-tool-use.json":    `{"matcher":"*"}`,
	} {
		p := filepath.Join(plug, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"name":"` + name + `"}`
	if tier != "" {
		manifest = `{"name":"` + name + `","skills_tier":"` + tier + `"}`
	}
	writePack(t, dir, manifest)
	return `{"source":"file://` + dir + `","name":"` + name + `"}`
}

// WRAPPED PLUGINS, AS THE HOST DELIVERS THEM: verbatim with yolo's marker when the pack is
// namespaced; at the flat default only the plugin's skills arrive, and the components a flat
// skills dir cannot carry are named at the launch rather than lost.
func TestAJailDeliversAWrappedPluginAsTheHostDoes(t *testing.T) {
	t.Run("namespaced", func(t *testing.T) {
		home := packHome(t)
		base := t.TempDir()
		writeUserPacks(t, home, "["+agentSkillsPack(t, base, "alphacli")+","+
			writeWrappedPluginPack(t, base, "wrapper", "namespaced")+"]")
		staging, _ := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-nc11-plugin-ns")
		dest := filepath.Join(staging, jailcontent.SkillStagingName("alphacli"))
		if _, err := os.Stat(filepath.Join(dest, "acme-tools", "hooks", "pre-tool-use.json")); err != nil {
			t.Errorf("the plugin tree did not arrive verbatim: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(readFileT(t, filepath.Join(dest, "acme-tools", ".claude-plugin", "plugin.json"))), &m); err != nil {
			t.Fatal(err)
		}
		if m["x-yolo-managed-by"] != "yolo-jail" || m["hooks"] == nil {
			t.Errorf("the delivered manifest lost a field or lacks yolo's marker: %v", m)
		}
	})
	t.Run("flat", func(t *testing.T) {
		home := packHome(t)
		base := t.TempDir()
		writeUserPacks(t, home, "["+agentSkillsPack(t, base, "alphacli")+","+
			writeWrappedPluginPack(t, base, "wrapper", "")+"]")
		staging, said := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-nc11-plugin-flat")
		dest := filepath.Join(staging, jailcontent.SkillStagingName("alphacli"))
		if got := readFileT(t, filepath.Join(dest, "nested", "SKILL.md")); got != "from the plugin" {
			t.Errorf("the plugin's skill did not arrive flat: %q", got)
		}
		if _, err := os.Stat(filepath.Join(dest, "acme-tools")); !os.IsNotExist(err) {
			t.Errorf("a flat pack's plugin tree was copied whole (%v)", err)
		}
		if !strings.Contains(said, "Skills:") || !strings.Contains(said, "cannot arrive") {
			t.Errorf("the refused hook was not named at the launch:\n%s", said)
		}
	})
}
