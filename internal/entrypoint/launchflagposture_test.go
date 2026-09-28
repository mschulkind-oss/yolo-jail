package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launchflagposture_test.go pins docs/plans/notch-convergence.md item 20 (row D9) in this
// package: every launch-flag fold here (the alias, every generated carrier, the wrapper
// enumeration) reads the RENDER TARGET's posture bit through launchAutonomy, never a literal.
// A jail Env folds the autonomous posture; a host-targeted Env the guarded one.

func twoPostureLaunchEnv(t *testing.T, hostTarget bool) *Env {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[{"kind":"autonomy",` +
		`"autonomous":{"launch":[{"bin":"acme","flags":["--no-prompts"]}]},` +
		`"guarded":{"launch":[{"bin":"guarded-only","flags":["--ask-first"]}]}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	e := aliasEnv(t, root, ``)
	e.hostTarget = hostTarget
	return e
}

func TestLaunchFlagFoldsReadTheRenderTargetsPosture(t *testing.T) {
	for _, tc := range []struct {
		name       string
		hostTarget bool
		wantBins   string
		wantFlag   string
	}{
		{"jail", false, "acme", "--no-prompts"},
		{"host", true, "guarded-only", "--ask-first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := twoPostureLaunchEnv(t, tc.hostTarget)
			packs, err := LoadJailPacks(e)
			if err != nil {
				t.Fatal(err)
			}
			bins := launchFlagBins(e, packs)
			if got := strings.Join(bins, ","); got != tc.wantBins {
				t.Errorf("launchFlagBins = %q, want %q: the enumeration must fold the target's posture",
					got, tc.wantBins)
			}
			inj := launchFlagsFor(e, packs, tc.wantBins)
			if inj == nil || strings.Join(inj.Flags, " ") != tc.wantFlag {
				t.Errorf("launchFlagsFor(%s) = %+v, want %s", tc.wantBins, inj, tc.wantFlag)
			}
		})
	}
}
