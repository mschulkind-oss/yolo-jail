package run

// shareddirsources_test.go pins ensureSharedDirSources (shareddirsources.go): every
// machine-scope shared dir a SELECTED pack declares has its bind source in the machine store
// before the argv names it, including a CONFIGURED pack's, which storage.EnsureGlobalStorage
// never created (it runs before config loads and knows only the shipped packs).

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// configuredSharedDirPack loads a non-embedded pack declaring one machine-scope shared dir.
func configuredSharedDirPack(t *testing.T) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	manifest := `{"name": "mytool", "contributes": [{"kind": "state", "at": ".mytool-shared",
		"scope": "machine", "because": "a login shared across workspaces"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "mytool")
	if len(problems) != 0 {
		t.Fatalf("loading the configured pack: %v", problems)
	}
	if got := packload.SharedDirs([]*packload.Pack{p}); len(got) != 1 || got[0] != ".mytool-shared" {
		t.Fatalf("the fixture pack declares shared dirs %v, want [.mytool-shared]", got)
	}
	return p
}

// TestAConfiguredPacksSharedDirGetsABindSource: after ensureSharedDirSources, every -v source
// the assembled argv takes from the machine store exists, on both container backends. Before
// it, the configured pack's source did not, and podman refused the container with a bare
// "statfs …: no such file or directory".
func TestAConfiguredPacksSharedDirGetsABindSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	packs := append(packsFixture(t, "claude"), configuredSharedDirPack(t))
	src := filepath.Join(paths.GlobalHome(), ".mytool-shared")

	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			if err := os.RemoveAll(paths.GlobalHome()); err != nil {
				t.Fatal(err)
			}
			if err := ensureSharedDirSources(packs); err != nil {
				t.Fatalf("ensureSharedDirSources: %v", err)
			}
			if info, err := os.Stat(src); err != nil || !info.IsDir() {
				t.Fatalf("the configured pack's shared dir has no bind source at %s: %v", src, err)
			}

			o := goldenOptions(t.TempDir(), home)
			o.Stdout, o.Stderr = discardBuf(), discardBuf()
			argv := o.assembleRunCmd(&assembleInput{
				cfg: newConfig(), rt: rt, cname: "yolo-shared-src", imageRef: goldenImageRef,
				jailPrefix: goldenJailPrefix, packs: packs, homeSkeleton: "/skeleton",
				wsState: t.TempDir(), miseStore: "/mise-store", yoloVersion: "9.9.9-test",
				mountTargets: map[string]struct{}{}, writableHomeDirs: config.WritableHomeDirs(newConfig(), packs),
			})
			named := 0
			for i := 0; i+1 < len(argv); i++ {
				if argv[i] != "-v" || !strings.HasPrefix(argv[i+1], paths.GlobalHome()+string(filepath.Separator)) {
					continue
				}
				named++
				source := strings.SplitN(argv[i+1], ":", 2)[0]
				if info, err := os.Stat(source); err != nil || !info.IsDir() {
					t.Errorf("the argv binds %s from the machine store, and it does not exist: %v", argv[i+1], err)
				}
			}
			if !strings.Contains(strings.Join(argv, " "), src+":/home/agent/.mytool-shared") {
				t.Errorf("the %s argv does not bind the configured pack's shared dir; this test no "+
					"longer covers it", rt)
			}
			if named < 2 {
				t.Errorf("the %s argv binds %d dirs from the machine store, want claude's and the "+
					"configured pack's", rt, named)
			}
		})
	}
}

// An existing shared dir is left exactly as it is: it holds the machine's shared login. And a
// path that would leave the store is refused rather than created.
func TestEnsureSharedDirSourcesLeavesTheStoreAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cred := filepath.Join(paths.GlobalHome(), ".mytool-shared", "token")
	if err := os.MkdirAll(filepath.Dir(cred), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureSharedDirSources([]*packload.Pack{configuredSharedDirPack(t)}); err != nil {
		t.Fatalf("ensureSharedDirSources: %v", err)
	}
	if b, err := os.ReadFile(cred); err != nil || string(b) != "secret" {
		t.Errorf("the existing shared dir's content changed: %q, %v", b, err)
	}
	if info, _ := os.Stat(filepath.Dir(cred)); info == nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the existing shared dir's mode changed: %v", info)
	}
}

// TestRunContainerCreatesTheSharedDirSources pins the call site: runContainer creates the
// selected packs' shared-dir sources after its last attach decision (an attach binds nothing
// new), before it assembles the argv, and on BOTH container backends, so never inside the
// podman-only arm. Delete the call and a configured pack's machine-scope dir has no bind
// source again, with every behavioral test above still green.
func TestRunContainerCreatesTheSharedDirSources(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var lastAttach, assemble token.Pos
	var calls []*ast.CallExpr
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "attachExisting":
			if call.Pos() > lastAttach {
				lastAttach = call.Pos()
			}
		case "assembleRunCmd":
			assemble = call.Pos()
		case "ensureSharedDirSources":
			calls = append(calls, call)
		}
		return true
	})
	if len(calls) != 1 {
		t.Fatalf("runContainer calls ensureSharedDirSources %d times, want once: without it a "+
			"configured pack's machine-scope shared dir has no bind source and podman refuses the "+
			"container", len(calls))
	}
	call := calls[0]
	if lastAttach == token.NoPos || assemble == token.NoPos {
		t.Fatal("runContainer's attach or assembly anchor moved; re-anchor this pin, do not delete it")
	}
	if call.Pos() < lastAttach || call.Pos() > assemble {
		t.Error("ensureSharedDirSources is not between runContainer's last attach decision and its argv assembly")
	}
	if len(call.Args) != 1 || skelIdent(call.Args[0]) != "loadedPacks" {
		t.Error("ensureSharedDirSources is not handed loadedPacks, the selection the argv binds from")
	}
	for _, st := range fd.Body.List {
		if ifs, ok := st.(*ast.IfStmt); ok && skelIsPodmanArm(ifs.Cond) && callsIn(ifs.Body)["ensureSharedDirSources"] {
			t.Error("ensureSharedDirSources runs inside a podman-only arm, but Apple Container binds the " +
				"shared dirs from the machine store too (appleContainerBaseMounts)")
		}
	}
}
