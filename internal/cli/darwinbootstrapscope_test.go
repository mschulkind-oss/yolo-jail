package cli

// darwinbootstrapscope_test.go pins the scope check on `yolo internal darwin-bootstrap` —
// the one host-side generation that does not run inside run.Run, and therefore the one the
// launch-time workspace-scope guard cannot cover. Everything it goes on to create is a bare
// mkdir under a workspace it is TOLD about: the home overlay under <ws>/.yolo/home, the
// prism sidecars, the adoption archive and the staged bootstrap script.
//
// What it can promise is narrower than the launch guard's, and the test says so: the roots it
// compares against are the SANDBOX ACCOUNT's (darwinBootstrapScopeHome), whatever HOME the run
// was handed. That is the promise being pinned here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sandboxAccountHome stands a resolved temporary directory in for /Users/_yolojail.
func sandboxAccountHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	saved := darwinBootstrapScopeHome
	darwinBootstrapScopeHome = func() string { return home }
	t.Cleanup(func() { darwinBootstrapScopeHome = saved })
	return home
}

func TestDarwinBootstrapRefusesABoundaryWorkspace(t *testing.T) {
	home := sandboxAccountHome(t)
	// The sandbox home this invocation runs as, and the workspace it was handed.
	t.Setenv("HOME", home)
	t.Setenv("JAIL_HOME", home)

	for _, ws := range []string{home, filepath.Join(home, paths.GlobalStorageRel())} {
		t.Setenv("YOLO_DARWIN_WORKSPACE", ws)

		if rc := runDarwinBootstrap(nil); rc != 1 {
			t.Errorf("runDarwinBootstrap with workspace %q returned %d, want 1 (refused)", ws, rc)
		}
		if _, err := os.Stat(paths.WorkspaceStateDir(ws)); !os.IsNotExist(err) {
			t.Errorf("%s exists — the bootstrap generated into a directory that may not be a "+
				"workspace (stat err: %v)", paths.WorkspaceStateDir(ws), err)
		}
	}
}

// TestDarwinBootstrapAdmitsTheWorkspacesYoloItselfLaysOut is the scheduled macos-user job's
// refusals (runs 37522721810 through 37790269472), on Linux:
//
//   - a SECOND launch of a workspace, whose account home links ~/.config into that workspace's
//     own sidecar <ws>/.yolo/home/config, which by then holds a yolo-jail directory. Following
//     the link found "yolo's user config directory" inside the workspace;
//   - an install capture or fork build, which hands the bootstrap a staging HOME inside the
//     staging tree it names as the workspace. Checking HOME found "your home directory" there.
//
// Neither is the sandbox account's boundary, and both must be admitted. The account's own home
// and its state dir stay refused (the test above).
func TestDarwinBootstrapAdmitsTheWorkspacesYoloItselfLaysOut(t *testing.T) {
	home := sandboxAccountHome(t)

	relaunch := filepath.Join(resolvedDir(t), "ws")
	sidecarConfig := filepath.Join(paths.WorkspaceStateDir(relaunch), "home", "config")
	if err := os.MkdirAll(filepath.Join(sidecarConfig, "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sidecarConfig, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if b := darwinBootstrapScopeBreach(relaunch); b != nil {
		t.Errorf("a relaunched workspace whose sidecar the account's ~/.config links to was refused: %v", b)
	}

	capture := filepath.Join(resolvedDir(t), "yolo-captures", "somebin")
	if err := os.MkdirAll(filepath.Join(capture, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(capture, "home"))
	if b := darwinBootstrapScopeBreach(capture); b != nil {
		t.Errorf("a capture staging tree holding its own staging HOME was refused: %v", b)
	}

	for _, ws := range []string{home, filepath.Dir(home), filepath.Join(home, paths.GlobalStorageRel())} {
		if darwinBootstrapScopeBreach(ws) == nil {
			t.Errorf("workspace %q was admitted, but it is or contains the sandbox account's home, or sits in its state dir", ws)
		}
	}
}

// resolvedDir is t.TempDir with its symlinks resolved, minted resolved so no comparison
// against code that resolves them can forget (the darwin /var/folders class).
func resolvedDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestDarwinBootstrapRunsOnlyAsTheSandboxAccount pins the identity gate. The scope check above
// guards the SANDBOX account's home, so a human hand-running the bootstrap with their own home as
// the workspace would pass it, and the generators would plant a .yolo there. Run as any uid other
// than the one owning the sandbox home, the bootstrap must refuse before it creates anything.
func TestDarwinBootstrapRunsOnlyAsTheSandboxAccount(t *testing.T) {
	sandboxAccountHome(t)
	human := resolvedDir(t)
	t.Setenv("HOME", human)
	t.Setenv("JAIL_HOME", "")
	t.Setenv("YOLO_DARWIN_WORKSPACE", human)

	saved := darwinBootstrapEuid
	darwinBootstrapEuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { darwinBootstrapEuid = saved })

	if rc := runDarwinBootstrap(nil); rc != 1 {
		t.Errorf("runDarwinBootstrap as a uid that does not own the sandbox home returned %d, want 1", rc)
	}
	if _, err := os.Stat(paths.WorkspaceStateDir(human)); !os.IsNotExist(err) {
		t.Errorf("%s exists: the bootstrap generated into the invoking user's home (stat err: %v)",
			paths.WorkspaceStateDir(human), err)
	}
}

// TestDarwinBootstrapKeepsTheBootLogOfARelaunch is the second launch of a workspace: the account
// home's ~/.config links into that workspace's sidecar, which by then holds a yolo-jail directory.
// The bootstrap admits the workspace, and its boot log must still be written: the log's creator
// asks the process-wide scope rule, which followed that link and refused, so every relaunch lost
// its boot.log silently.
func TestDarwinBootstrapKeepsTheBootLogOfARelaunch(t *testing.T) {
	home := sandboxAccountHome(t)
	ws := filepath.Join(resolvedDir(t), "ws")
	sidecarConfig := filepath.Join(paths.WorkspaceStateDir(ws), "home", "config")
	if err := os.MkdirAll(filepath.Join(sidecarConfig, "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sidecarConfig, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	clearLaunchEnv(t)
	// The bootstrap's generators switch this process to the jail's tolerant manifest decoder;
	// restore it so later tests' strict-load refusals still bite.
	t.Cleanup(packload.OverrideSkewTolerance(false))
	t.Setenv("HOME", home)
	t.Setenv("JAIL_HOME", home)
	t.Setenv("YOLO_DARWIN_WORKSPACE", ws)

	runDarwinBootstrap(nil) // the generation's own result does not matter here; the log does
	if _, err := os.Stat(filepath.Join(paths.WorkspaceStateDir(ws), "boot.log")); err != nil {
		t.Errorf("a relaunch's bootstrap wrote no boot.log: %v", err)
	}
	if paths.WorkspaceScopeBreach(home) == nil {
		t.Error("the bootstrap's scope rule outlived the call: the process-HOME rule admits HOME itself")
	}
}

// clearLaunchEnv unsets the generator contract a jail or a CI runner may carry (YOLO_*, and the
// install prefixes the bootstrap catalogs), so a full bootstrap run reads only what the test set.
func clearLaunchEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "YOLO_") || strings.HasPrefix(k, "MISE_") || k == "NPM_CONFIG_PREFIX" || k == "GOPATH" {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
}

// THE SEALED BUILD ACT'S BOOTSTRAP, THROUGH THE ENTRY (PPX-D48): the word macosuser puts on its argv
// makes the boot a sealed build's, which its boot log records, and the same bootstrap without it is a
// launch's. Any other argument is refused before anything is generated, since only yolo writes this
// argv. Red with runDarwinBootstrap's options no longer carrying the parsed flag.
func TestTheDarwinBootstrapTakesTheSealedBuildWordFromItsArgv(t *testing.T) {
	const skipped = "a sealed build jail runs no agent"
	for _, sealed := range []bool{true, false} {
		t.Run(map[bool]string{true: "sealed", false: "a launch's"}[sealed], func(t *testing.T) {
			home := sandboxAccountHome(t)
			ws := filepath.Join(resolvedDir(t), "ws")
			clearLaunchEnv(t)
			t.Cleanup(packload.OverrideSkewTolerance(false))
			t.Setenv("HOME", home)
			t.Setenv("JAIL_HOME", home)
			t.Setenv("YOLO_DARWIN_WORKSPACE", ws)
			var args []string
			if sealed {
				args = []string{macosuser.DarwinBootstrapSealedFlag}
			}
			runDarwinBootstrap(args)
			log, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(ws), "boot.log"))
			if err != nil {
				t.Fatalf("no boot.log: %v", err)
			}
			if got := strings.Contains(string(log), skipped); got != sealed {
				t.Errorf("the boot log records a sealed build = %v, want %v:\n%s", got, sealed, log)
			}
		})
	}
	if rc := runDarwinBootstrap([]string{"--sealed"}); rc != 2 {
		t.Errorf("an unknown argument ran the bootstrap (rc %d)", rc)
	}
}

// THE RUN-AS-YOU BOOTSTRAP RUNS AS YOU, on a Mac with no /Users/_yolojail at all: its identity
// check reads the real home the launcher named (YOLO_DARWIN_REAL_HOME, which you own), not the
// account's. Run as you it may generate only into the workspace's own HOME: a hand-run whose HOME
// is your real home is refused before anything is created, the promise the account mode's
// identity check made. Fails if the identity check goes back to requiring the account's home, or
// if the self-mode HOME guard is deleted.
func TestDarwinBootstrapRunsAsYouOnlyIntoTheWorkspaceHome(t *testing.T) {
	saved := darwinBootstrapScopeHome
	darwinBootstrapScopeHome = func() string { return filepath.Join(resolvedDir(t), "no-account") }
	t.Cleanup(func() { darwinBootstrapScopeHome = saved })
	real := resolvedDir(t)
	ws := filepath.Join(real, "code", "alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.DarwinRealHomeEnv, "")
	if darwinBootstrapIdentityRefusal() == nil {
		t.Fatal("with no account home and no real home named, the identity check passed")
	}
	t.Setenv(paths.DarwinRealHomeEnv, real)
	if err := darwinBootstrapIdentityRefusal(); err != nil {
		t.Fatalf("the run-as-you bootstrap was refused as you: %v", err)
	}
	if b := darwinBootstrapScopeBreach(ws); b != nil {
		t.Errorf("a workspace inside your home was refused in the run-as-you mode: %v", b)
	}
	if darwinBootstrapScopeBreach(real) == nil {
		t.Error("your real home itself was admitted as a workspace")
	}

	// A hand-run with HOME at the real home: refused, nothing created.
	t.Setenv("JAIL_HOME", "")
	t.Setenv("HOME", real)
	t.Setenv(entrypoint.DarwinHomeSidecarEnv, "")
	t.Setenv("YOLO_DARWIN_WORKSPACE", ws)
	if rc := runDarwinBootstrap(nil); rc != 1 {
		t.Errorf("a run-as-you bootstrap with HOME at your real home returned %d, want 1", rc)
	}
	if _, err := os.Stat(paths.WorkspaceStateDir(ws)); !os.IsNotExist(err) {
		t.Errorf("the refused bootstrap created %s (%v)", paths.WorkspaceStateDir(ws), err)
	}

	// The launch's own shape: HOME is the sidecar, named as such, under the workspace.
	sidecar := paths.LegacyHomeState(ws)
	t.Setenv("HOME", sidecar)
	t.Setenv(entrypoint.DarwinHomeSidecarEnv, sidecar)
	if err := darwinBootstrapSelfHomeRefusal(); err != nil {
		t.Errorf("the launch's own HOME was refused: %v", err)
	}
	// And a setup's home, the one a launch binds once the legacy home has moved (named-setups.md
	// §9): admitted as the sidecar it is, but not when HOME and the sidecar name different homes.
	setupHome := paths.SetupHomeState(ws, "seatbelt-self")
	t.Setenv("HOME", setupHome)
	t.Setenv(entrypoint.DarwinHomeSidecarEnv, setupHome)
	if err := darwinBootstrapSelfHomeRefusal(); err != nil {
		t.Errorf("the launch's own setup HOME was refused: %v", err)
	}
	t.Setenv(entrypoint.DarwinHomeSidecarEnv, sidecar)
	if err := darwinBootstrapSelfHomeRefusal(); err == nil {
		t.Error("a HOME naming one workspace home and a sidecar naming another was admitted")
	}
}

// A RUN-AS-YOU CAPTURE'S BOOTSTRAP RUNS AS YOU (MMP-D22): told your real home, with HOME its
// staging home inside the staging tree it names as the workspace and no sidecar, it passes the
// identity check and the self-mode HOME guard and writes its boot log, where it used to be refused
// for a HOME that was not <workspace>/.yolo/home. A staging "home" that IS your real home is still
// refused before anything is created. Fails if the guard loses its capture shape, or admits one
// whose HOME is your home.
func TestDarwinBootstrapAdmitsARunAsYouCaptureStagingHome(t *testing.T) {
	saved := darwinBootstrapScopeHome
	darwinBootstrapScopeHome = func() string { return filepath.Join(resolvedDir(t), "no-account") }
	t.Cleanup(func() { darwinBootstrapScopeHome = saved })
	real := resolvedDir(t)
	staging := filepath.Join(resolvedDir(t), "yolo-captures", "somebin")
	stagingHome := macosuser.CaptureStagingHome(staging)
	if err := os.MkdirAll(stagingHome, 0o755); err != nil {
		t.Fatal(err)
	}
	clearLaunchEnv(t)
	t.Cleanup(packload.OverrideSkewTolerance(false))
	t.Setenv(paths.DarwinRealHomeEnv, real)
	t.Setenv("HOME", stagingHome)
	t.Setenv("JAIL_HOME", stagingHome)
	t.Setenv(entrypoint.DarwinHomeSidecarEnv, "")
	t.Setenv("YOLO_DARWIN_WORKSPACE", staging)
	if err := darwinBootstrapIdentityRefusal(); err != nil {
		t.Fatalf("the run-as-you capture bootstrap failed its identity check: %v", err)
	}
	if err := darwinBootstrapSelfHomeRefusal(); err != nil {
		t.Fatalf("the run-as-you capture's staging home was refused: %v", err)
	}
	runDarwinBootstrap(nil) // the generation's own result does not matter here; getting past the guards does
	if _, err := os.Stat(filepath.Join(paths.WorkspaceStateDir(staging), "boot.log")); err != nil {
		t.Errorf("the run-as-you capture's bootstrap wrote no boot.log, so a guard refused it: %v", err)
	}

	// The capture shape with your real home as its staging home: refused, nothing created.
	outer := resolvedDir(t)
	real = macosuser.CaptureStagingHome(outer)
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.DarwinRealHomeEnv, real)
	t.Setenv("HOME", real)
	t.Setenv("JAIL_HOME", real)
	t.Setenv("YOLO_DARWIN_WORKSPACE", outer)
	if err := darwinBootstrapSelfHomeRefusal(); err == nil {
		t.Error("a capture-shaped bootstrap whose staging home is your real home was admitted")
	}
	if rc := runDarwinBootstrap(nil); rc != 1 {
		t.Errorf("a capture-shaped bootstrap into your real home returned %d, want 1", rc)
	}
	if _, err := os.Stat(paths.WorkspaceStateDir(outer)); !os.IsNotExist(err) {
		t.Errorf("the refused bootstrap created %s (%v)", paths.WorkspaceStateDir(outer), err)
	}
}
