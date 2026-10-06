package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// durableBriefing is the claude briefing refreshJailBriefings wrote, and its storage-classes
// section.
func durableBriefing(t *testing.T, o *Options, rt string) (body, section string) {
	t.Helper()
	cfg, packs := persistenceFixture(t, "")
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, rt, stagedPacks{packs: packs}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatalf("no briefing written: %v", err)
	}
	body = string(raw)
	return body, sectionOf(body, "## Storage classes: what survives a restart")
}

func durableOptions(t *testing.T) (*Options, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	var out bytes.Buffer
	o.Stdout = &out
	return o, &out
}

// A FRESH LAUNCH MAKES THE DURABLE DIR, THE BRIEFING LEADS WITH IT, AND THE ARGV EXPORTS IT —
// one value for all three, so the agent is never told a path the variable does not hold
// (docs/design/durable-scratch-space.md §5.2). On both container backends the path is the
// workspace bind's spelling, with no mount of its own (DS-D8).
func TestAFreshLaunchMakesTheDurableDirAndBothBriefingAndArgvName(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			o, out := durableOptions(t)
			o.ensureDurableDir(rt, newConfig())
			if fi, err := os.Lstat(durable.HostPath(o.Workspace)); err != nil || !fi.IsDir() {
				t.Fatalf("the launch made no durable dir: %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("a successful launch printed %q", out.String())
			}
			_, sec := durableBriefing(t, o, rt)
			lead := "**`$YOLO_DURABLE_DIR`** (`/workspace/.yolo/durable`) is scratch space for"
			if !strings.Contains(sec, "\n\n"+lead) {
				t.Errorf("the %s briefing does not lead with the durable dir:\n%s", rt, sec)
			}

			home := t.TempDir()
			in := relocationInput(t, rt, "/ws/.yolo/home", nil)
			in.durableDir = o.durableJailPath()
			in.scratchID = "0123456789abcdef"
			argv := goldenOptions("/ws", home).assembleRunCmd(in)
			if v, ok := envValue(argv, durable.EnvVar); !ok || v != durable.ContainerJailPath {
				t.Errorf("the %s argv exports %s=%q (present %v), want %s", rt, durable.EnvVar, v, ok,
					durable.ContainerJailPath)
			}
		})
	}
}

// A durable dir that cannot be made is said once on the launch terminal, the briefing says
// the same and names no path, and nothing is exported — never a refusal (DS-D2).
func TestALinkedYoloGivesNoDurableDirAndSaysWhy(t *testing.T) {
	o, out := durableOptions(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(o.Workspace, ".yolo")); err != nil {
		t.Fatal(err)
	}
	d := o.ensureDurableDir("podman", newConfig())
	if d.Path != "" || o.durableJailPath() != "" {
		t.Fatalf("a linked .yolo produced a durable dir %q", d.Path)
	}
	if want := "Durable dir: unavailable this launch: `.yolo` is a symbolic link."; !strings.Contains(out.String(), want) {
		t.Errorf("the launch did not say %q:\n%s", want, out.String())
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("the launcher wrote through the link: %v", entries)
	}
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.durableDir = o.durableJailPath()
	if _, ok := envValue(goldenOptions("/ws", t.TempDir()).assembleRunCmd(in), durable.EnvVar); ok {
		t.Error("a launch with no durable dir exported the variable")
	}
	// The briefing's answer, from the same value.
	sec := strings.Join(jailcontentSection(t, o.durable), "\n")
	if !strings.Contains(sec, "**No durable directory this launch**: `.yolo` is a symbolic link.") ||
		strings.Contains(sec, "$YOLO_DURABLE_DIR") {
		t.Errorf("the briefing does not say why there is no durable dir, or names one:\n%s", sec)
	}
}

// jailcontentSection renders the section for d over a podman-shaped map, the way
// refreshJailBriefings hands it over; used where the fixture's workspace cannot hold a
// briefing (a linked .yolo).
func jailcontentSection(t *testing.T, d *jailcontent.DurableDir) []string {
	t.Helper()
	cfg, packs := persistenceFixture(t, "")
	out := jailcontent.BriefingContent(jailcontent.BriefingInput{Workspace: "/w", Mechanism: "podman",
		Persistence: persistenceMapFor("podman", cfg, packs, "/w", false), Durable: d})
	return strings.Split(sectionOf(out, "## Storage classes: what survives a restart"), "\n")
}

// A `workspace_readonly` entry covering the durable dir means the jail could not write it,
// so the launcher does not make it and says so (§5.6).
func TestAReadonlyEntryCoveringTheDurableDirMakesNone(t *testing.T) {
	for _, entry := range []string{".", ".yolo", ".yolo/durable"} {
		t.Run(entry, func(t *testing.T) {
			o, out := durableOptions(t)
			d := o.ensureDurableDir("podman", newConfig("workspace_readonly", []any{entry}))
			if d.Path != "" {
				t.Fatalf("made a durable dir under workspace_readonly %q", entry)
			}
			if _, err := os.Lstat(durable.HostPath(o.Workspace)); !os.IsNotExist(err) {
				t.Errorf("the directory was created anyway: %v", err)
			}
			if !strings.Contains(out.String(), "the `workspace_readonly` entry `"+entry+"` makes it read-only") {
				t.Errorf("no reason printed:\n%s", out.String())
			}
		})
	}
	o, _ := durableOptions(t)
	if d := o.ensureDurableDir("podman", newConfig("workspace_readonly", []any{"src"})); d.Path == "" {
		t.Errorf("an unrelated workspace_readonly entry cost the durable dir: %s", d.Unavailable)
	}
}

// macos-user reaches the directory at the workspace's real path: there is no /workspace.
func TestMacosUserDurableDirIsTheRealPath(t *testing.T) {
	o, _ := durableOptions(t)
	if d := o.ensureDurableDir("macos-user", newConfig()); d.Path != durable.HostPath(o.Workspace) {
		t.Errorf("macos-user durable dir = %q, want %q", d.Path, durable.HostPath(o.Workspace))
	}
}

// A nested jail launched from the enclosing jail's /tmp is told its durable dir lasts only as
// long as that jail; a host launch, or one elsewhere, is not.
func TestANestedJailInTheOuterTmpGetsTheCaveat(t *testing.T) {
	o, _ := durableOptions(t)
	o.Workspace = "/tmp/yolo-nested"
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return ""
	}
	if c := o.durableCaveat(); !strings.Contains(c, "enclosing jail's per-launch `/tmp`") {
		t.Errorf("caveat = %q", c)
	}
	o.Workspace = "/home/agent/proj"
	if c := o.durableCaveat(); c != "" {
		t.Errorf("a workspace outside the per-launch set got %q", c)
	}
	o.Workspace = "/tmp/yolo-nested"
	o.Getenv = func(string) string { return "" }
	if c := o.durableCaveat(); c != "" {
		t.Errorf("a host launch from /tmp got %q", c)
	}
}

// nestInOuterTmp makes o an in-jail launcher whose workspace is a fresh directory in the
// enclosing jail's per-launch /tmp: the nested-verification loop's shape. Spelled /tmp, not
// t.TempDir(), which is not under /tmp on darwin; durableCaveat's test is lexical.
func nestInOuterTmp(t *testing.T, o *Options) {
	t.Helper()
	ws, err := os.MkdirTemp("/tmp", "yolo-nested-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(ws) })
	o.Workspace = ws
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
}

// nestedLifetime is the caveat as the briefing renders it, in the lifetime sentence's place.
const nestedLifetime = "Use any layout under it.\n⚠ This workspace is itself inside the enclosing " +
	"jail's per-launch `/tmp`, so this directory lasts only as long as that jail. It sits in "

// A NESTED JAIL'S BRIEFING STATES ITS DURABLE DIR'S LIFETIME ONCE, AND TRUTHFULLY: the
// caveat, in place of "yolo never deletes it", which the enclosing yolo's cleanup of its
// /tmp makes false. Through ensureDurableDir and refreshJailBriefings, so deleting the
// fresh launch's caveat assignment fails here.
func TestANestedFreshLaunchBriefsTheCaveatAsTheLifetime(t *testing.T) {
	o, _ := durableOptions(t)
	nestInOuterTmp(t, o)
	o.ensureDurableDir("podman", newConfig())
	_, sec := durableBriefing(t, o, "podman")
	if !strings.Contains(sec, nestedLifetime) || strings.Contains(sec, "never deletes it") {
		t.Errorf("the nested briefing does not give the caveat as the lifetime:\n%s", sec)
	}
}

// AN ATTACH MAKES NOTHING AND INHERITS: its briefing names the directory the running jail's
// launch exported, read from that jail's frozen environment, and a jail started without one
// is told it has none. A nested jail's attach keeps the caveat: it rewrites the briefing the
// jail reads, so dropping it there put "yolo never deletes it" back for every agent started
// after the first terminal. Driven through the real attachExisting.
func TestAnAttachBriefsTheDurableDirTheJailWasStartedWith(t *testing.T) {
	packs := claudePackFixture(t)
	exported := currentJailEnv + durable.EnvVar + "=" + durable.ContainerJailPath + "\n"
	for _, tc := range []struct {
		name, env, want string
		nested          bool
	}{
		{"exported", exported,
			"**`$YOLO_DURABLE_DIR`** (`/workspace/.yolo/durable`) is scratch space for", false},
		{"started without one", currentJailEnv,
			"**No durable directory this launch**: this jail was started without one (by an older " +
				"launcher, or a launch that could not make it); a fresh launch tries again.", false},
		{"nested in the outer jail's /tmp", exported, nestedLifetime, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tune func(*Options, *jsonx.OrderedMap)
			if tc.nested {
				tune = func(o *Options, _ *jsonx.OrderedMap) { nestInOuterTmp(t, o) }
			}
			o, cfg, channel, stderr := attachFixture(t, tc.env, packs, nil, tune)
			if rc, _, _ := attachToExec(t, o, cfg, packs, channel); rc != 0 {
				t.Fatalf("attach rc=%d\n%s", rc, stderr.String())
			}
			if _, err := os.Lstat(durable.HostPath(o.Workspace)); !os.IsNotExist(err) {
				t.Errorf("an attach made the durable dir: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(paths.AgentsDir(), "yolo-ws-abcd1234",
				briefingStagingName(claudeBriefingDest)))
			if err != nil {
				t.Fatalf("the attach wrote no briefing: %v", err)
			}
			sec := sectionOf(string(raw), "## Storage classes: what survives a restart")
			if !strings.Contains(sec, tc.want) {
				t.Errorf("the attach's briefing does not say %q:\n%s", tc.want, sec)
			}
			if tc.nested && strings.Contains(sec, "never deletes it") {
				t.Errorf("the nested attach's briefing says yolo never deletes it:\n%s", sec)
			}
		})
	}
}

// AN ATTACH TO A JAIL STARTED WITHOUT ONE SAYS WHY, when the cause is still there: the words a
// fresh launch would print for a covering `workspace_readonly` entry or a linked `.yolo`,
// not a promise that the next launch makes it, which is false for both. It creates nothing.
func TestAnAttachWithoutADurableDirNamesACauseThatPersists(t *testing.T) {
	o, _ := durableOptions(t)
	d := durableDirFromLaunchEnv(nil, newConfig("workspace_readonly", []any{".yolo"}), o.Workspace)
	if d.Path != "" || d.Unavailable != "the `workspace_readonly` entry `.yolo` makes it read-only" {
		t.Errorf("readonly: %+v", d)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(o.Workspace, ".yolo")); err != nil {
		t.Fatal(err)
	}
	d = durableDirFromLaunchEnv(nil, newConfig(), o.Workspace)
	if d.Path != "" || d.Unavailable != "`.yolo` is a symbolic link" {
		t.Errorf("linked .yolo: %+v", d)
	}
	if strings.Contains(d.Unavailable, "next fresh launch makes it") {
		t.Errorf("the attach promised a launch would make it: %q", d.Unavailable)
	}
}

// A --dry-run (macos-user's) describes the launch and makes nothing in the workspace.
func TestADryRunMakesNoDurableDir(t *testing.T) {
	o, _ := durableOptions(t)
	o.DryRun = true
	d := o.ensureDurableDir("macos-user", newConfig())
	if d.Path != durable.HostPath(o.Workspace) {
		t.Errorf("a dry run described durable dir %q, want %q", d.Path, durable.HostPath(o.Workspace))
	}
	if _, err := os.Lstat(filepath.Join(o.Workspace, ".yolo")); !os.IsNotExist(err) {
		t.Errorf("a dry run created the workspace's .yolo: %v", err)
	}
}

// THE CALL SITES, which the tests above cannot reach without a container: runContainer makes
// the durable dir on the fresh path — after its last attach decision, before the briefing
// that leads with it — and hands the same value to the argv; Run's macos-user arm makes it
// before that arm's briefing and puts it on the launch env. Deleting any one of these leaves
// every other test here green, so each is pinned by position.
func TestTheDurableDirIsMadeOnEveryFreshLaunchPath(t *testing.T) {
	rc := funcDecl(t, "run.go", "runContainer")
	var ensure, lastAttach, refresh, assemble token.Pos
	ast.Inspect(rc, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch skelCallee(call) {
			case "ensureDurableDir":
				ensure = call.Pos()
			case "attachExisting":
				if call.Pos() > lastAttach {
					lastAttach = call.Pos()
				}
			case "refreshJailBriefings":
				refresh = call.Pos()
			case "assembleRunCmd":
				assemble = call.Pos()
			}
		}
		return true
	})
	if ensure == token.NoPos {
		t.Fatal("runContainer never calls ensureDurableDir: no container launch makes the durable dir")
	}
	if !(lastAttach < ensure && ensure < refresh && refresh < assemble) {
		t.Errorf("runContainer's order is wrong (last attach %d, ensure %d, briefing %d, argv %d): the "+
			"durable dir must be made on the fresh path only, before the briefing and the argv",
			lastAttach, ensure, refresh, assemble)
	}
	// The assembly input carries it: `durableDir: o.durableJailPath()`.
	var carried bool
	ast.Inspect(rc, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok && skelIdent(kv.Key) == "durableDir" {
			if call, ok := kv.Value.(*ast.CallExpr); ok && skelCallee(call) == "durableJailPath" {
				carried = true
			}
		}
		return true
	})
	if !carried {
		t.Error("runContainer's assembleInput does not carry durableDir: o.durableJailPath(), so the " +
			"container never gets $YOLO_DURABLE_DIR")
	}

	run := funcDecl(t, "run.go", "Run")
	var mEnsure, mRefresh token.Pos
	var setsEnv bool
	ast.Inspect(run, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "ensureDurableDir":
			mEnsure = call.Pos()
		case "refreshJailBriefings":
			if mRefresh == token.NoPos {
				mRefresh = call.Pos()
			}
		case "Set":
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && skelIdent(sel.X) == "launchEnv" && len(call.Args) == 2 {
				if a, ok := call.Args[0].(*ast.SelectorExpr); ok && a.Sel.Name == "EnvVar" && skelIdent(a.X) == "durable" {
					setsEnv = true
				}
			}
		}
		return true
	})
	if mEnsure == token.NoPos || !(mEnsure < mRefresh) {
		t.Error("Run's macos-user arm does not make the durable dir before its briefing")
	}
	if !setsEnv {
		t.Error("Run's macos-user arm never puts $YOLO_DURABLE_DIR on the launch env")
	}
}
