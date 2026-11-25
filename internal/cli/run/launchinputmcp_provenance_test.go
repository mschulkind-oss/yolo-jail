package run

import (
	"bytes"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ownerShapedProvenanceHome selects the owner's shape of pack list: shipped packs, a bare local
// pack, a fork of a program a base installs, a namespaced source and a skills-only projection.
// It returns the loaded user config.
func ownerShapedProvenanceHome(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	src := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bare/basepack/pack.toml", "name=\"basepack\"\n[[program]]\nbin=\"tool\"\nvia=\"npm\"\npackage=\"tool\"\n")
	write("bare/forkpack/pack.toml", "name=\"forkpack\"\n[[program]]\nbin=\"tool\"\nvia=\"source\"\nfork_of=\"basepack\"\nsource=\"git+file:///nonexistent/yolo-test/tool?ref=main\"\nbuild=\"make install\"\nproduces=[\".local/bin/tool\"]\n")
	write("bare/mine/pack.toml", "name=\"mine\"\n[[skills]]\nfrom=\"skills\"\n")
	write("bare/mine/skills/alpha/SKILL.md", "alpha\n")
	write("bare/mine/skills/beta/SKILL.md", "beta\n")
	write("team/tools/pack.toml", "name=\"tools\"\n[[skills]]\nfrom=\"skills\"\n")
	write("team/tools/skills/gamma/SKILL.md", "gamma\n")
	write("team/tools/skills/delta/SKILL.md", "delta\n")
	toml := "packs = [\"pi\", \"codex\", \"claude\", \"copilot\", \"git\", \"github\", \"own/basepack\", \"own/forkpack\", \"own/mine\",\n" +
		"  {pack = \"team/tools\", skills = {only = [\"gamma\"]}}]\n" +
		"[pack_sources]\nown = {path = \"" + filepath.Join(src, "bare") + "\", bare = true}\n" +
		"team = {path = \"" + filepath.Join(src, "team") + "\", bare = false}\n"
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

// A selection the user made and the launch staged is the SAME selection, so provenance holds for
// every legitimate shape of pack entry: red when the staged copy of a pack (a fork's rewritten
// base, a projection, a namespaced import) differs from the user-scope read in a way that has
// nothing to do with MCP.
func TestProvenanceHoldsForTheOwnersPackShapes(t *testing.T) {
	user := ownerShapedProvenanceHome(t)
	selected, notes, problems, complete := config.UserScopeSelectedPacksWithNotes()
	if !complete || len(problems) != 0 {
		t.Fatalf("user selection incomplete: %v %v", problems, notes)
	}
	var out bytes.Buffer
	o := retireOptions(t, &out)
	staged, ok := o.stageRunPacks("yolo-provenance")
	if !ok {
		t.Fatalf("staging failed:\n%s", out.String())
	}
	frozen := frozenMCPForLaunch(user, user, selected, staged.packs)
	if !frozen.Proven {
		t.Fatalf("provenance failed for a legitimately selected pack set: %s", frozen.Unproven)
	}
}

func TestProvenanceNamesAPackMissingFromTheLaunchsTree(t *testing.T) {
	user := ownerShapedProvenanceHome(t)
	selected, _, _, _ := config.UserScopeSelectedPacksWithNotes()
	var staged []*packload.Pack
	for _, p := range selected {
		if p.Name != "own/mine" {
			staged = append(staged, p)
		}
	}
	frozen := frozenMCPForLaunch(user, user, selected, staged)
	if frozen.Proven || !strings.Contains(frozen.Unproven, "own/mine") || !strings.Contains(frozen.Unproven, "not in this launch's pack tree") {
		t.Fatalf("reason = %q", frozen.Unproven)
	}
}

func TestProvenanceNamesAPackWhoseDeclarationDiffers(t *testing.T) {
	user := ownerShapedProvenanceHome(t)
	selected, _, _, _ := config.UserScopeSelectedPacksWithNotes()
	var staged []*packload.Pack
	for _, p := range selected {
		c := *p
		if p.Name == "own/mine" {
			d := *p.Decl
			d.Description = d.Description + " changed"
			c.Decl = &d
		}
		staged = append(staged, &c)
	}
	frozen := frozenMCPForLaunch(user, user, selected, staged)
	if frozen.Proven || !strings.Contains(frozen.Unproven, "own/mine") || !strings.Contains(frozen.Unproven, "reads differently") || !strings.Contains(frozen.Unproven, "Description") {
		t.Fatalf("reason = %q", frozen.Unproven)
	}
}

// The same equivalence for the remote shapes: a git skills import with `only` under a namespace
// and a bare git pack, both fetched into the store the way a launch's refresh leaves them.
func TestProvenanceHoldsForFetchedGitPacks(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, data)
		}
	}
	put := func(rel, data string) {
		t.Helper()
		file := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "--initial-branch=main")
	put("corpus/pack.toml", "[[skills]]\nfrom = 'skills'\n")
	put("corpus/skills/chosen/SKILL.md", "chosen\n")
	put("corpus/skills/other/SKILL.md", "other\n")
	put("tools/pack.toml", "[[skills]]\nfrom = 'skills'\n[[program]]\nbin = 'tool'\nvia = 'npm'\npackage = 'tool'\n")
	put("tools/skills/one/SKILL.md", "one\n")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	url := "file://" + fileURLPath(repo)
	user := testconfig.User(t, "packs = ['claude', 'pi', {pack = 'demo/corpus', skills = {only = ['chosen']}}, 'bare/tools']\n"+
		"[pack_sources.demo]\ngit = '"+url+"'\nref = 'main'\n"+
		"[pack_sources.bare]\ngit = '"+url+"'\nref = 'main'\nbare = true\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	var specs []packsrc.RefreshPack
	for _, e := range entries {
		if !e.Embedded() {
			specs = append(specs, e.RefreshSpec(false))
		}
	}
	refreshed, err := store.Refresh(specs, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refreshed {
		if r.Err != nil {
			t.Fatalf("fetch fixture: %+v", r)
		}
	}
	userCfg, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	selected, _, problems, complete := config.UserScopeSelectedPacksWithNotes()
	if !complete || len(problems) != 0 {
		t.Fatalf("user selection incomplete: %v", problems)
	}
	var out bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: &out, Stderr: &out}
	const cname = "yolo-test-provenance-git"
	root, packs, _, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	t.Cleanup(func() { discardPackTree(cname, root) })
	frozen := frozenMCPForLaunch(userCfg, userCfg, selected, packs)
	if !frozen.Proven {
		t.Fatalf("provenance failed for fetched git packs: %s", frozen.Unproven)
	}
}

// The launch's notice says WHY, not only that: the recorded reason is the sentence, so a reader
// is told which pack and what to do. Red if the notice goes back to the one fixed sentence.
func TestTheUnprovenNoticeCarriesTheRecordedReason(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the unproven notice is printed on Linux podman")
	}
	why := "user pack own/mine is not in this launch's pack tree, so retry the launch"
	in := LaunchInputContext{Runtime: "podman", MCPUnproven: why}
	var stderr bytes.Buffer
	(&Options{Stderr: &stderr}).noteMCPAuthShare(in, emptyShare(t))
	if !strings.Contains(stderr.String(), why) || !strings.Contains(stderr.String(), "no sign-in was shared") {
		t.Fatalf("notice = %q", stderr.String())
	}
	stderr.Reset()
	(&Options{Stderr: &stderr}).noteMCPAuthShare(LaunchInputContext{Runtime: "podman"}, emptyShare(t))
	if !strings.Contains(stderr.String(), "yolo pack ls") {
		t.Fatalf("a launch with no recorded reason still names a next step: %q", stderr.String())
	}
}

// The reason must reach the keeper, whose process prints the notice: an unproven launch's plan
// carries it, and a launch with none stays out of the plan.
func TestAnUnprovenReasonCrossesIntoTheKeeperPlan(t *testing.T) {
	o := &Options{}
	if o.frozenLaunchMCPForKeeper() != nil {
		t.Fatal("a launch with no reason carried MCP state")
	}
	o.launchInputMCP = frozenLaunchMCP{Unproven: "because"}
	got := o.frozenLaunchMCPForKeeper()
	if got == nil || got.Unproven != "because" || got.Proven {
		t.Fatalf("plan carried %+v", got)
	}
}

// A user selection that does not resolve is the reason, named by pack with its fetch step.
func TestTheSelectionSnapshotRecordsWhyItIsIncomplete(t *testing.T) {
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "packs = ['claude', {git = 'https://example.invalid/acme/craft', ref = 'main'}]\n[mcp_servers.s]\nurl = 'https://s.invalid/mcp'\nsign_in = 'once'\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: t.TempDir()}
	got := o.snapshotLaunchMCPSelection(cfg, "podman")
	if got.valid || !strings.Contains(got.why, "craft") || !strings.Contains(got.why, "yolo pack install") {
		t.Fatalf("snapshot = valid %v why %q", got.valid, got.why)
	}
}

func emptyShare(t *testing.T) []byte {
	t.Helper()
	data, err := produceMCPAuthShare(LaunchInputContext{Container: "jail-test", Workspace: t.TempDir(), Runtime: "podman"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A selection snapshot that was not valid hands its reason to the launch's frozen MCP state, so the
// notice has something to say. Red if freezeLaunchMCPComposition stops recording it.
func TestAnInvalidSnapshotsReasonIsFrozenForTheLaunch(t *testing.T) {
	o := &Options{Workspace: t.TempDir()}
	o.freezeLaunchMCPComposition(nil, nil, launchMCPSelection{why: "pack craft was not read"})
	if o.launchInputMCP.Proven || o.launchInputMCP.Unproven != "pack craft was not read" {
		t.Fatalf("frozen = %+v", o.launchInputMCP)
	}
}

// The launch input the producer receives carries the reason. Red if writeLaunchInput stops copying it.
func TestWriteLaunchInputCarriesTheUnprovenReasonToTheProducer(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	var seen string
	o := &Options{Workspace: workspace, launchInputMCP: frozenLaunchMCP{Unproven: "because pack craft"},
		launchInputProducers: map[string]LaunchInputProducer{
			"fixture": {StateDir: func(LaunchInputContext) string { return state },
				Produce: func(in LaunchInputContext) ([]byte, error) { seen = in.MCPUnproven; return []byte("x"), nil }},
		}}
	fillDefaults(o)
	o.launchInputMountArgv = []string{"podman", "run", "-v", workspace + ":/workspace"}
	lp := &loopholes.Loophole{Name: "fixture", HostDaemon: &loopholes.HostDaemon{LaunchInput: "fixture"}}
	if !o.writeLaunchInput(lp, "yolo-unproven-fixture") {
		t.Fatal("publication failed")
	}
	if seen != "because pack craft" {
		t.Fatalf("producer saw MCPUnproven %q", seen)
	}
}
