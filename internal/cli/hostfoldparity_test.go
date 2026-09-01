package cli

// hostfoldparity_test.go pins the ONE pack env fold across notches.
//
// The host composes the process env it execs from the same ordered sequence the jail's
// env block reduces (packload.EnvFold, reduced by packload.EnvVarsFor). It did not
// always: it folded every pack's static env first and every selected variant's env after
// that, so a key pack A's variant and pack B's static both write had a different winner
// per notch — the jail said the later pack's static, the host the earlier pack's variant.
// This file is the pin: one two-pack fixture through both folds, one winner asserted.
//
// It is a call-site test in the sense the review asked for: delete the host's fold and
// the host side stops answering for the keys the fold writes; revert the host to the
// all-static-then-all-variant order and the two sides disagree. Either way this fails.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// writeFoldParityPacks writes the two-pack fixture both folds consume, and selects it.
//
// alpha installs claude and declares the variant `p`; beta installs pi and declares CROSS
// statically. alpha is the FIRST configured pack, so the per-pack fold (alpha's static,
// alpha's variant, beta's static) answers "beta's static" for CROSS, while the retired
// all-static-then-all-variant order answers "alpha's variant" — the two orders disagree
// on exactly this fixture, which is what makes it the fixture.
//
// SHARED_OUT is the same shape with a removal: alpha's variant nulls it and beta's static
// sets it, so the fold's own ordering has to decide it too. GONE is a null nothing
// assigns over, inherited from the shell — the case the host's removals-last rule exists
// for, and the one place the two notches are ALLOWED to differ (the jail starts from an
// empty env, so it has nothing to remove).
func writeFoldParityPacks(t *testing.T, home string) {
	t.Helper()
	base := filepath.Join(home, "packs")
	manifests := map[string]string{
		"alpha": `{"name":"alpha","contributes":[` +
			`{"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},` +
			`{"kind":"profile","name":"p","env":{` +
			`"CROSS":"alpha-variant",` +
			`"ALPHA_ONLY":"yes",` +
			`"SHARED_OUT":null,` +
			`"GONE":null}}]}`,
		"beta": `{"name":"beta","contributes":[` +
			`{"kind":"program","bin":"pi","via":"npm","package":"@acme/pi"},` +
			`{"kind":"env","vars":{"CROSS":"beta-static","SHARED_OUT":"beta-static","BETA_ONLY":"yes"}}]}`,
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
	  "pack_profiles": {"claude": "p"}
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
	t.Setenv("GONE", "from-shell") // what the invoking shell had, for the null below
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

	for _, key := range []string{"CROSS", "SHARED_OUT"} {
		if jail[key] == "" {
			t.Fatalf("the jail fold has no %s; the fixture stopped exercising the fold order", key)
		}
		if host[key] != jail[key] {
			t.Errorf("%s: host = %q, jail = %q — the two notches folded the same packs to "+
				"different winners", key, host[key], jail[key])
		}
	}
	// The ruling itself, not only the parity: the LATER pack's static wins, which is the
	// per-pack order and not the retired all-static-then-all-variant one.
	if host["CROSS"] != "beta-static" {
		t.Errorf("CROSS = %q, want beta-static (a later pack's static beats an earlier "+
			"pack's variant under the per-pack fold)", host["CROSS"])
	}
	if host["SHARED_OUT"] != "beta-static" {
		t.Errorf("SHARED_OUT = %q, want beta-static — a removal a later fold entry assigns "+
			"over is the fold's decision, not the host's to re-make last", host["SHARED_OUT"])
	}
	// Each pack's own contribution still arrives: the variant's literals and the other
	// pack's static map are untouched by the reordering.
	if host["ALPHA_ONLY"] != "yes" || host["BETA_ONLY"] != "yes" {
		t.Errorf("the fold lost a pack's own env: alpha=%q beta=%q", host["ALPHA_ONLY"], host["BETA_ONLY"])
	}
	// And the one removal the fold was the last word on still removes — beating the shell
	// this process inherited, which is what the host's removals-last rule is for.
	if v, present := host["GONE"]; present {
		t.Errorf("GONE = %q, want it removed: a null nothing assigns over must still unset, "+
			"including over the inherited environment", v)
	}
}

// TestHostFoldParityWithoutAProfile keeps the no-variant launch honest: with nothing
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
		t.Error("an unselected variant contributed env to the host launch")
	}
}
