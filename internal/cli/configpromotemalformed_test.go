package cli

// configpromotemalformed_test.go pins `yolo config promote` refusing to WRITE into a destination
// pack whose manifest has problems — the ones every launch and `yolo host apply --assert` refuse
// the pack over (resolveConfiguredPack, notch-scoped-config-contributions.md NS-D14).
//
// The bug: promote reads its fold through configuredPacksForInspection, which reports such a pack
// as "not inspected" and carries on, and then wrote the promoted keys into that very manifest,
// cleared them from the capture overlay, exited 0 and said "The next launch renders these keys
// from there". No launch renders anything from a pack every launch refuses. The value was kept
// (it is in pack.json), so the fault was the claim and the recorded decision that promote, as an
// inspection verb, "writes nothing".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoAutonomies is a manifest body whose one problem is a second `autonomy` contribution
// (packdecl.validateSingleAutonomy); without the second it is clean.
func twoAutonomies(name string, malformed bool) string {
	m := `{"name":"` + name + `","contributes":[
  {"kind":"autonomy","guarded":{"launch":[{"bin":"claude","flags":[]}]}}`
	if malformed {
		m += `,
  {"kind":"autonomy","autonomous":{"launch":[{"bin":"claude","flags":["--x"]}]}}`
	}
	return m + `]}`
}

// A PROMOTION INTO A MALFORMED DESTINATION IS REFUSED AND WRITES NOTHING: non-zero, the manifest
// byte-identical, the key still captured, and the refusal names the destination, its problem and
// `yolo pack lint`. Both destinations a promotion can write are covered: the conventional local
// pack (`--to local`, the default) and a configured local pack (`--to pack:<name>`). The control
// is the same manifest without the second `autonomy`, which promotes.
func TestPromoteRefusesToWriteIntoAPackWhoseManifestHasProblems(t *testing.T) {
	for _, dest := range []string{"local", "pack:mine"} {
		for _, malformed := range []bool{true, false} {
			var w *promoteWorld
			var manifest string
			if dest == "local" {
				w = newPromoteWorld(t, `["claude"]`)
				manifest = filepath.Join(w.home, ".config", "yolo-jail", "local", "pack.json")
				writeFile(t, manifest, twoAutonomies("local", malformed))
			} else {
				home := t.TempDir()
				dir := filepath.Join(home, "packs", "mine")
				manifest = filepath.Join(dir, "pack.json")
				writeFile(t, manifest, twoAutonomies("mine", malformed))
				w = newPromoteWorld(t, `["claude",{"source":"file://`+dir+`","name":"mine"}]`)
			}
			w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)
			before, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}

			out, errw, rc := w.run("claude", "--to", dest, "--keys", "autoMemoryEnabled",
				"--accept-promotion")
			after, _ := os.ReadFile(manifest)
			captured := strings.Contains(w.overlayFor("claude", "settings"), "autoMemoryEnabled")
			if !malformed {
				if rc != 0 || string(after) == string(before) || captured {
					t.Fatalf("%s fixture control: a clean destination must promote; rc=%d\n%s%s",
						dest, rc, out, errw)
				}
				continue
			}
			if rc == 0 {
				t.Errorf("%s: promote into a manifest every launch refuses exited 0\n%s%s", dest, out, errw)
			}
			if string(after) != string(before) {
				t.Errorf("%s: a refused promotion rewrote the destination manifest:\n%s", dest, after)
			}
			if !captured {
				t.Errorf("%s: a refused promotion cleared the capture overlay", dest)
			}
			if strings.Contains(out, "The next launch renders") {
				t.Errorf("%s: promote claims a launch renders keys from a pack every launch refuses:\n%s",
					dest, out)
			}
			for _, want := range []string{malformedProblem, "yolo pack lint", "Nothing was written"} {
				if !strings.Contains(errw, want) {
					t.Errorf("%s: the refusal must contain %q:\n%s", dest, want, errw)
				}
			}
		}
	}
}

// THE DRY RUN AND THE PLAN SAY SO, and exit 0 (information): the dry run names the flag's
// refusal instead of inviting a re-run that would be refused, and --plan names the destination as
// unwritable rather than only as a pack whose overlay "could outrank this promotion".
func TestPromoteDryRunAndPlanSayAMalformedDestinationIsRefused(t *testing.T) {
	for _, mode := range []string{"dry run", "--plan"} {
		w := newPromoteWorld(t, `["claude"]`)
		manifest := filepath.Join(w.home, ".config", "yolo-jail", "local", "pack.json")
		writeFile(t, manifest, twoAutonomies("local", true))
		w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)
		before, _ := os.ReadFile(manifest)

		args := []string{"claude"}
		if mode == "--plan" {
			args = append(args, "--plan")
		}
		out, errw, rc := w.run(args...)
		if rc != 0 {
			t.Fatalf("%s: rc=%d, want 0 (information)\n%s%s", mode, rc, out, errw)
		}
		if after, _ := os.ReadFile(manifest); string(after) != string(before) {
			t.Errorf("%s wrote the manifest", mode)
		}
		if !strings.Contains(out, "destination local has manifest problems") {
			t.Errorf("%s must say the destination cannot be written:\n%s", mode, out)
		}
		if mode == "dry run" && strings.Contains(out, "Re-run with") {
			t.Errorf("the dry run invites a re-run that would be refused:\n%s", out)
		}
	}
}
