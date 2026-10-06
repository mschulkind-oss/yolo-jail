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
	pack := droppedDirPack(t)
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
			runSteps(&bootRun{e: e, target: bootContainer, packsLoaded: true, packs: []*packload.Pack{pack}}, steps)

			fails := strings.Join(e.GenFailures(), "\n")
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
