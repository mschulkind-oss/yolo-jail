package run

// keeperpins_test.go pins the keeper's call sites that no behavioral unit test reaches
// (docs/design/jail-lifetime-last-session-wins.md step 3): each fails when its call is deleted.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEveryFreshLaunchWaitsForAnOldKeeper is JL-D28 (1) at its call site: runContainer's arrival
// makes its attach decision in a loop, and once no attach took the launch, asks whether a keeper
// still holds the name and waits for it (with the launch lock released, awaitPreviousKeeper) before
// deciding again, so one container name never has two keepers.
func TestEveryFreshLaunchWaitsForAnOldKeeper(t *testing.T) {
	var loop *ast.ForStmt
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if f, ok := n.(*ast.ForStmt); ok && loop == nil && callsIn(f.Body)["attachExisting"] {
			loop = f
		}
		return true
	})
	if loop == nil {
		t.Fatal("runContainer's attach decision is no longer a loop; re-anchor this pin, do not delete it")
	}
	pos := map[string]token.Pos{}
	ast.Inspect(loop.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name := skelCallee(call); pos[name] == token.NoPos {
				pos[name] = call.Pos()
			}
		}
		return true
	})
	for _, name := range []string{"holdLaunchLock", "attachExisting", "probeKeeper", "awaitPreviousKeeper"} {
		if pos[name] == token.NoPos {
			t.Errorf("runContainer's arrival loop no longer calls %s", name)
		}
	}
	if !(pos["holdLaunchLock"] < pos["attachExisting"] && pos["attachExisting"] < pos["probeKeeper"] &&
		pos["probeKeeper"] < pos["awaitPreviousKeeper"]) {
		t.Error("the arrival must take the launch lock, try the attach, and only then wait for an old keeper")
	}
	calls := callsIn(funcDecl(t, "keeperspawn.go", "awaitPreviousKeeper"))
	if !calls["releaseLaunchLock"] || !calls["waitForKeeper"] {
		t.Error("awaitPreviousKeeper must release the launch lock and wait for the keeper, never wait holding it")
	}
}

// TestAnAttachSkewRestartWaitsForTheOldKeeper is JL-D26 at its call site: the restart stops the
// jail, and before the fresh launch it continues as spawns a keeper of its own, waits for the old
// one's liveness lock, holding the launch lock throughout.
func TestAnAttachSkewRestartWaitsForTheOldKeeper(t *testing.T) {
	pos := map[string]token.Pos{}
	ast.Inspect(funcDecl(t, "contracttags.go", "restartJailForAttach"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name := skelCallee(call); pos[name] == token.NoPos {
				pos[name] = call.Pos()
			}
		}
		return true
	})
	if pos["stopJail"] == token.NoPos || pos["probeKeeper"] == token.NoPos || pos["waitForKeeper"] == token.NoPos ||
		!(pos["stopJail"] < pos["probeKeeper"] && pos["probeKeeper"] < pos["waitForKeeper"]) {
		t.Error("restartJailForAttach must stop the jail, then wait for its keeper to be gone")
	}
	if pos["releaseLaunchLock"] != token.NoPos {
		t.Error("the restart releases the launch lock; it must hold it throughout (JL-D26)")
	}
}

// TestATeardownRemovesOnlyItsOwnOwnerFile is JL-D28 (4): the chain removes the owner-PID file only
// while it names the process running the chain, so a teardown that runs late cannot take the next
// jail's keeper's file away.
func TestATeardownRemovesOnlyItsOwnOwnerFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	const cname = "yolo-owner-file"
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPIDFile(cname), []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions("/ws", t.TempDir())
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	o.Getpid = func() int { return 1 }
	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)
	if pid, ok := readOwnerPID(cname); !ok || pid != 99 {
		t.Fatalf("a teardown removed another keeper's owner file (%d, %v)", pid, ok)
	}
	o.Getpid = func() int { return 99 }
	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)
	if _, ok := readOwnerPID(cname); ok {
		t.Error("the keeper's own teardown left its owner file")
	}
	if callsIn(funcDecl(t, "lifecycle.go", "stopJail"))["clearOwnerPID"] {
		t.Error("stopJail removes the owner file unconditionally again")
	}
}

// TestTheKeepersSelfExecsGoThroughItsOwnBinary: a daemon's argv and the scratch remover's are built
// with selfExecArgv, which gives a keeper its own inode (JL-D5), never execx.SelfExecArgv directly.
func TestTheKeepersSelfExecsGoThroughItsOwnBinary(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"scratchremoval.go", "spawnScratchRemover"},
		{"loopholesruntime.go", "resolveDaemonArgv"},
	} {
		calls := callsIn(funcDecl(t, tc.file, tc.fn))
		if !calls["selfExecArgv"] {
			t.Errorf("%s no longer builds its self-exec with selfExecArgv", tc.fn)
		}
		if calls["SelfExecArgv"] {
			t.Errorf("%s calls execx.SelfExecArgv, which re-resolves the binary's path", tc.fn)
		}
	}
}

// TestNoCallSiteReachesExecxSelfExecArgvDirectly is PH-D4 at every call site: a self-exec that serves
// a jail goes through selfExecArgv, which execs the launch's pin and never re-resolves
// os.Executable() (docs/design/pinned-host-helpers.md section 3.3). The one caller outside the rule
// is SelfExecPath, whose caller is a patched fork's build child.
func TestNoCallSiteReachesExecxSelfExecArgvDirectly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		scanned++
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "SelfExecArgv" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "execx" &&
					!(file == "keeperspawn.go" && fd.Name.Name == "SelfExecPath") {
					t.Errorf("%s: %s calls execx.SelfExecArgv, which re-resolves os.Executable(); "+
						"go through Options.selfExecArgv, which execs the launch's pin", file, fd.Name.Name)
				}
				return true
			})
		}
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d files: the glob no longer reaches the package", scanned)
	}
}

// TestThePinIsResolvedByRunAndExecdByTheKeeperSpawn pins the two call sites no behavioral test
// reaches: Run resolves the pin before it spawns anything (PH-D3), and the keeper is spawned from
// that pin on every OS, Linux included, never from /proc/self/exe or os.Executable() (PH-D5).
func TestThePinIsResolvedByRunAndExecdByTheKeeperSpawn(t *testing.T) {
	if !callsIn(funcDecl(t, "run.go", "Run"))["resolveHelperPin"] {
		t.Error("Run no longer resolves the launch's pin at pre-flight")
	}
	spawn := callsIn(funcDecl(t, "keeperspawn.go", "realSpawnKeeper"))
	if !spawn["pinnedArgv"] {
		t.Error("realSpawnKeeper no longer execs the pin")
	}
	if spawn["keeperSelfExe"] || spawn["SelfExecArgv"] {
		t.Error("realSpawnKeeper spawns the keeper from /proc/self/exe or os.Executable(), not the pin")
	}
}

// TestNoOtherPackageSelfExecsTheRunningBinaryForAHelperThatServesALaunch is PH-D4 beyond this
// package: a helper that serves a launch (a jail or a `yolo host` session) is exec'd from that
// launch's pin, so no non-test code in the packages that start helpers calls execx.SelfExecArgv or
// run.SelfExecPath, which resolve the running binary. The callers below serve no launch, and each
// names why; a new caller must be added here with its own reason, or use the pin.
func TestNoOtherPackageSelfExecsTheRunningBinaryForAHelperThatServesALaunch(t *testing.T) {
	// file (slash path under internal/) + top-level declaration -> why it may use the running binary.
	allowed := map[string]string{
		"cli/run/keeperspawn.go SelfExecPath":                        "SelfExecPath itself, for a child that serves no launch (the patched fork's build child)",
		"cli/forkbuildchild.go forkBuildChildExec":                   "the patched fork's build child: it serves a build, not a jail, and must be this binary",
		"cli/backgroundadvance.go startBackgroundAdvance":            "operator verb's detached background advance: serves no jail",
		"cli/shareddemand.go sharedPreparationSpawn":                 "preparation scheduler bookkeeping for an operator verb: serves no jail",
		"cli/preparationscheduler.go preparationSchedulerProcess":    "the preparation scheduler: an operator-started singleton that serves no jail",
		"cli/preparationscheduler.go ensurePreparationScheduler":     "the preparation scheduler: compares its own binary, serves no jail",
		"cli/preparationscheduler.go runPreparationSchedulerCommand": "the preparation scheduler: compares its own binary, serves no jail",
		"openaiauthhost/privatedaemon.go invokedExec":                "`yolo openai-auth`: an operator verb with no launch, whose daemon ends with it",
		"loopholes/settingscheck.go RunSettingsCheck":                "the settings validator (`yolo check` and a launch's pre-flight): a one-shot check that exits, not a helper that serves a launch",
		"loopholes/runtime.go runOne":                                "`yolo check`'s doctor_cmd: a one-shot check, not a start",
	}
	dirs := []string{"cli", "cli/run", "launchservice", "openaiauthhost", "macosuser", "loopholes"}
	used := map[string]bool{}
	scanned := 0
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join("..", "..", filepath.FromSlash(dir), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			scanned++
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, decl := range f.Decls {
				name := ""
				switch d := decl.(type) {
				case *ast.FuncDecl:
					name = d.Name.Name
				case *ast.GenDecl:
					if len(d.Specs) > 0 {
						if vs, ok := d.Specs[0].(*ast.ValueSpec); ok && len(vs.Names) > 0 {
							name = vs.Names[0].Name
						}
					}
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, _ := sel.X.(*ast.Ident)
					isSelf := x != nil && ((x.Name == "execx" && sel.Sel.Name == "SelfExecArgv") ||
						(x.Name == "run" && sel.Sel.Name == "SelfExecPath"))
					if !isSelf {
						return true
					}
					key := dir + "/" + filepath.Base(file) + " " + name
					if _, ok := allowed[key]; ok {
						used[key] = true
						return true
					}
					t.Errorf("%s: %s calls %s.%s, which resolves the running binary; a helper that serves a launch "+
						"is exec'd from its pin (Options.selfExecArgv, helperpin). If this serves no launch, add %q "+
						"to the allowlist with the reason", dir, name, x.Name, sel.Sel.Name, key)
					return true
				})
			}
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files: the globs no longer reach the packages", scanned)
	}
	for key := range allowed {
		if !used[key] {
			t.Errorf("allowlist entry %q matches no call: remove it", key)
		}
	}
}
