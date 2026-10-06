package cli

// packlinthookstate_test.go pins `yolo pack lint` to the boot's shared-dir hook check
// (docs/design/pack-conventions.md §7, PC-D15). A `shared_credentials` or `shared_directory`
// hook links into its `at`, and the boot refuses one whose `at` names no machine-scope `state`
// the pack declares. Lint used to check only that `at` was present, so it printed "pack ok" for
// a pack whose every boot then failed.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestPackLintRefusesASharedDirHookWhoseAtNamesNoMachineState(t *testing.T) {
	for _, hook := range []string{"shared_credentials", "shared_directory"} {
		for _, c := range []struct{ name, state string }{
			{"no state at all", ``},
			{"a workspace-scope state at that path", `{"kind":"state","at":".x-shared"},`},
			{"a machine state at another path",
				`{"kind":"state","at":".y-shared","scope":"machine","because":"one login"},`},
		} {
			t.Run(hook+" with "+c.name, func(t *testing.T) {
				// Lint is a host read: never inherit a tolerant decoder another test left on.
				t.Cleanup(packload.OverrideSkewTolerance(false))
				dir := filepath.Join(t.TempDir(), "local")
				manifest := `{"name":"local","contributes":[` +
					c.state + `{"kind":"hook","hook":"` + hook + `","from":".x/thing","at":".x-shared"}]}`
				writeFile(t, filepath.Join(dir, "pack.json"), manifest)
				// The sentence depends on the pack (a workspace state at the path is told to
				// change its scope), so take it from this manifest as the jail decodes it.
				decl, problems, _ := packdecl.DecodeTolerant([]byte(manifest))
				if len(problems) != 0 {
					t.Fatalf("the jail's decode refused the fixture: %v", problems)
				}

				var out, errw bytes.Buffer
				rc := packMain([]string{"lint", dir}, &out, &errw, false)
				got := out.String() + errw.String()
				if rc == 0 || strings.Contains(got, "pack ok") {
					t.Fatalf("lint passed a hook the boot refuses (rc=%d):\n%s", rc, got)
				}
				// The boot's own sentence, next step included: the two share one message.
				if want := decl.UndeclaredHookStateProblem(".x-shared"); !strings.Contains(got, want) {
					t.Errorf("lint does not print the boot's refusal %q:\n%s", want, got)
				}
			})
		}
	}
}

// The other direction, so the fix cannot be "refuse every shared-dir hook": the hook beside the
// machine state it links into lints clean, and so does an unshare_directory hook whose `at` the
// pack no longer declares, which is that hook's whole purpose.
func TestPackLintPassesASharedDirHookBesideItsMachineState(t *testing.T) {
	for _, c := range []struct{ name, contributions string }{
		{"shared_directory", `{"kind":"state","at":".x-shared","scope":"machine","because":"one store"},` +
			`{"kind":"hook","hook":"shared_directory","from":".x/thing","at":".x-shared"}`},
		{"unshare_directory", `{"kind":"hook","hook":"unshare_directory","from":".x/thing","at":".x-shared"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "local")
			writeFile(t, filepath.Join(dir, "pack.json"),
				`{"name":"local","contributes":[`+c.contributions+`]}`)
			var out, errw bytes.Buffer
			if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
				t.Fatalf("lint refused a hook the boot runs (rc=%d):\n%s%s", rc, out.String(), errw.String())
			}
		})
	}
}
