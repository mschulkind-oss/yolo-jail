package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// sealedbuild_test.go pins PPX-D41 (docs/design/patched-extensions.md; sealedbuild.go) at the boot
// step that renders pack surfaces: a sealed build jail renders no pack-declared surface and runs no
// pack hook, so a contributing pack that declares its own surface under a directory only a pack the
// seal dropped makes writable no longer refuses the build jail's boot. The maintainer's first launch
// with patched extensions met it: pack matt declares a pi `automode` surface under
// ~/.pi/agent/extensions/, the seal narrowed each build to matt, and every build jail refused with
// `configure_pi_automode: mkdir … read-only file system`.

// droppedDirPack is the contributing pack of that shape: a surface for another pack's agent, fed by
// a host read, under that agent's home directory, and a hook whose link lies there too.
func droppedDirPack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"contributes":[
		{"kind":"state","at":".matt-shared","scope":"machine","because":"one store per machine"},
		{"kind":"hook","hook":"shared_directory","from":".pi/agent/matt-store","at":".matt-shared"},
		{"kind":"config","config":[{"agent":"pi","codec":"json","name":"automode",
			"path":"~/.pi/agent/extensions/pi-automode/config.json","readsHost":true,
			"managed":{"autoMode":{"allowInsideWorkingDirectory":true}}}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(dir, "matt")
	if len(problems) > 0 {
		t.Fatalf("the fixture pack does not load: %v", problems)
	}
	return p
}

// bootStepNamed is the boot table's step of that name, the call site a test drives.
func bootStepNamed(t *testing.T, name string) bootStep {
	t.Helper()
	for _, s := range bootSteps() {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("the boot step table has no %s", name)
	return bootStep{}
}

func TestASealedBuildJailRendersNoPackSurfaceAndRunsNoPackHook(t *testing.T) {
	steps := []bootStep{bootStepNamed(t, "configure_pack_surfaces"), bootStepNamed(t, "generate_mise_config")}
	// An overlay onto a surface no selected pack owns: the user's own jail names the orphan, and a
	// sealed build jail, whose seal leaves a contributing pack's overlays and lists ownerless by
	// construction, composes nothing onto a surface and so names none.
	packs := []*packload.Pack{droppedDirPack(t), overlayContributorPack(t, "acme-fzf", map[string]any{"k": 1})}
	for _, tc := range []struct {
		name   string
		vars   map[string]string
		sealed bool
	}{
		{"a sealed build jail, told so", map[string]string{SealedBuildEnv: "1"}, true},
		{"a patched extension's build jail from a host that predates the variable",
			map[string]string{TreeBuildEnv: "pi-subagents"}, true},
		{"the user's own jail", map[string]string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			// THE DROPPED PACK'S DIRECTORY CANNOT BE WRITTEN, as ~/.pi cannot be in a build jail
			// whose seal dropped pack pi: a regular file, so every mkdir under it fails, as root too.
			if err := os.WriteFile(filepath.Join(home, ".pi"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			var stderr, log bytes.Buffer
			e := &Env{Home: home, Workspace: t.TempDir(), Vars: tc.vars, Stderr: &stderr, LogOnly: &log}
			withCtxRoot(t, t.TempDir(), "matt")
			runSteps(&bootRun{e: e, target: bootContainer, packsLoaded: true, packs: packs}, steps)

			fails := strings.Join(e.GenFailures(), "\n")
			if named := strings.Contains(stderr.String(), "no effect"); named == tc.sealed {
				t.Errorf("the orphaned overlay is named %v, want %v:\n%s", named, !tc.sealed, stderr.String())
			}
			if !tc.sealed {
				// The fixture reproduces the refusal wherever the boot renders the pack.
				for _, w := range []string{"configure_pi_automode", "hook_matt_shared_directory"} {
					if !strings.Contains(fails, w) {
						t.Errorf("the user's own boot did not fail %s over the unwritable directory:\n%s", w, fails)
					}
				}
				return
			}
			if fails != "" {
				t.Errorf("the sealed build jail's boot refused:\n%s\n%s", fails, stderr.String())
			}
			if !strings.Contains(log.String(), sealedBuildSkipNote) {
				t.Errorf("the boot log does not say what the sealed build jail skipped:\n%s", log.String())
			}
			// CORE'S OWN SURFACES STILL RENDER: mise/config, which the build's toolchain reads.
			if _, err := os.Stat(filepath.Join(home, ".config", "mise", "config.toml")); err != nil {
				t.Errorf("the sealed build jail rendered no mise config: %v", err)
			}
		})
	}
}

// ONLY THE LAUNCHER SAYS A JAIL IS A SEALED BUILD: the boot reads the gate from the environment
// the jail was started with, before hydrate_user_env folds ~/.config/yolo-user-env.sh into it.
// That file carries a selected pack's ungated `env` vars and the user's env_sources, so a pack
// declaring YOLO_SEALED_BUILD (or YOLO_TREE_BUILD), or an env_sources entry of either name, would
// otherwise switch off every pack's surfaces and hooks in the user's own jail, and the boot would
// say a sealed build ran. Nor can the file clear a gate the launcher set. The macos-user boot runs
// no sealed build (FP-D3), and its environment relays the same channel, so it never reads one.
func TestOnlyTheLauncherCanMakeABootASealedBuild(t *testing.T) {
	steps := []bootStep{bootStepNamed(t, "hydrate_user_env"), bootStepNamed(t, "configure_pack_surfaces")}
	pack := droppedDirPack(t)
	for _, tc := range []struct {
		name    string
		target  bootTarget
		launch  map[string]string
		envFile string
		sealed  bool
	}{
		{"a pack env var naming the gate", bootContainer, map[string]string{}, "export YOLO_SEALED_BUILD='1'\n", false},
		{"an env_sources entry naming the gate", bootContainer, map[string]string{},
			"export YOLO_SEALED_BUILD=${YOLO_SEALED_BUILD:-'1'}\n", false},
		{"a pack env var naming the tree gate", bootContainer, map[string]string{}, "export YOLO_TREE_BUILD='ext'\n", false},
		{"a channel line clearing the launcher's gate", bootContainer, map[string]string{SealedBuildEnv: "1"},
			"export YOLO_SEALED_BUILD=''\nexport YOLO_TREE_BUILD=''\n", true},
		{"a macos-user boot whose relayed environment names the gate", bootDarwin,
			map[string]string{SealedBuildEnv: "1"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, ".pi"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".config", "yolo-user-env.sh"), []byte(tc.envFile), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{SealedBuildEnv, TreeBuildEnv} {
				t.Setenv(k, os.Getenv(k)) // hydration os.Setenv's what it reads; restore it after
			}
			var stderr, log bytes.Buffer
			e := &Env{Home: home, Workspace: t.TempDir(), Vars: tc.launch, Stderr: &stderr, LogOnly: &log}
			withCtxRoot(t, t.TempDir(), "matt")
			runSteps(&bootRun{e: e, target: tc.target, packsLoaded: true, packs: []*packload.Pack{pack}}, steps)

			fails := strings.Join(e.GenFailures(), "\n")
			skipped := strings.Contains(log.String(), sealedBuildSkipNote)
			if skipped != tc.sealed {
				t.Errorf("the boot skipped the pack surfaces = %v, want %v:\n%s", skipped, tc.sealed, log.String())
			}
			if rendered := strings.Contains(fails, "configure_pi_automode"); rendered == tc.sealed {
				t.Errorf("the boot rendered the pack surface = %v, want %v:\n%s", rendered, !tc.sealed, fails)
			}
		})
	}
}
