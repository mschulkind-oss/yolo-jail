package cli

// hostfoldparity_test.go pins the ONE pack env fold across notches.
//
// The host composes the process env it execs from the same ordered sequence the jail's
// env block reduces (packload.EnvFold, reduced by packload.EnvVarsFor). It did not
// always: it folded every pack's static env first and every selected profile's env after
// that, so a key pack A's gated entry and pack B's static both write had a different
// winner per notch — the jail said the later pack's static, the host the earlier pack's
// gated value. This file is the pin: one two-pack fixture through both folds, one winner
// asserted.
//
// It is a call-site test in the sense the review asked for: delete the host's fold and
// the host side stops answering for the keys the fold writes; revert the host to the
// all-static-then-all-gated order and the two sides disagree. Either way this fails.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// writeFoldParityPacks writes the two-pack fixture both folds consume, and selects it.
//
// alpha installs claude and declares the profile `p` with a gated env entry; beta installs
// pi and declares CROSS statically. alpha is the FIRST configured pack, so the per-pack
// fold (alpha's static, alpha's gated entry, beta's static) answers "beta's static" for
// CROSS, while the retired all-static-then-all-gated order answers "alpha's gated value" —
// the two orders disagree on exactly this fixture, which is what makes it the fixture.
//
// The removal half this fixture used to carry (a profile body nulling a key nothing
// assigned over) is gone with the body (OQ-PT8): both `vars` maps are plain strings, so
// the fold's operations are assignments only. env_sources nulls are the removals a host
// launch still has, and TestComposeHostEnvOrdering pins one beating the inherited shell.
func writeFoldParityPacks(t *testing.T, home string) {
	t.Helper()
	base := filepath.Join(home, "packs")
	manifests := map[string]string{
		"alpha": `{"name":"alpha","contributes":[` +
			`{"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},` +
			`{"kind":"profile","name":"p","provider":"p"},` +
			`{"kind":"env","profile":"p","vars":{` +
			`"CROSS":"alpha-gated",` +
			`"ALPHA_ONLY":"yes"}}]}`,
		"beta": `{"name":"beta","contributes":[` +
			`{"kind":"program","bin":"pi","via":"npm","package":"@acme/pi"},` +
			`{"kind":"env","vars":{"CROSS":"beta-static","BETA_ONLY":"yes"}}]}`,
	}
	for name, manifest := range manifests {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userCfg(t, home, `{
	  "packs": [
	    {"source": "file://`+filepath.Join(base, "alpha")+`", "name": "alpha"},
	    {"source": "file://`+filepath.Join(base, "beta")+`", "name": "beta"}
	  ],
	  "use_profiles": {"claude": "p"}
	}`)
}

// hostEnvMap applies the composition over the inherited environment, as exec would.
func hostEnvMap(t *testing.T, agent, profile string) map[string]string {
	t.Helper()
	env, _, err := composeHostEnv(agent, profile, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

func TestHostFoldMatchesTheJailFoldWinner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeFoldParityPacks(t, home)

	packs, err := loadedHostPacks()
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Fatalf("loadedHostPacks = %d packs, want the fixture's two; the fixture only means "+
			"anything if both packs are in the delivery order the config declared", len(packs))
	}

	// The jail notch's fold: EnvVarsFor over the launch's profile table — what the
	// container argv's env block is built from.
	table := map[string]string{"claude": "p"}
	jail := packload.EnvVarsFor(packs, table)
	// The host notch's: the same packs, the same selection, the env `yolo host -- claude`
	// would exec with.
	host := hostEnvMap(t, "claude", "")

	for _, key := range []string{"CROSS", "BETA_ONLY"} {
		if jail[key] == "" {
			t.Fatalf("the jail fold has no %s; the fixture stopped exercising the fold order", key)
		}
		if host[key] != jail[key] {
			t.Errorf("%s: host = %q, jail = %q — the two notches folded the same packs to "+
				"different winners", key, host[key], jail[key])
		}
	}
	// The ruling itself, not only the parity: the LATER pack's static wins, which is the
	// per-pack order and not the retired all-static-then-all-gated one.
	if host["CROSS"] != "beta-static" {
		t.Errorf("CROSS = %q, want beta-static (a later pack's static beats an earlier "+
			"pack's gated entry under the per-pack fold)", host["CROSS"])
	}
	// Each pack's own contribution still arrives: the gated entry's literals and the
	// other pack's static map are untouched by the reordering.
	if host["ALPHA_ONLY"] != "yes" || host["BETA_ONLY"] != "yes" {
		t.Errorf("the fold lost a pack's own env: alpha=%q beta=%q", host["ALPHA_ONLY"], host["BETA_ONLY"])
	}
}

// TestHostFoldParityWithoutAProfile keeps the no-profile launch honest: with nothing
// selected the two folds are the plain static merge, and the host must not lose it.
func TestHostFoldParityWithoutAProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeFoldParityPacks(t, home)

	packs, err := loadedHostPacks()
	if err != nil {
		t.Fatal(err)
	}
	jail := packload.EnvVarsFor(packs, nil)
	host := hostEnvMap(t, "pi", "")
	if jail["CROSS"] != "beta-static" || host["CROSS"] != jail["CROSS"] {
		t.Errorf("CROSS: host = %q, jail = %q, want both beta-static", host["CROSS"], jail["CROSS"])
	}
	if _, present := host["ALPHA_ONLY"]; present {
		t.Error("an unselected profile contributed env to the host launch")
	}
}
