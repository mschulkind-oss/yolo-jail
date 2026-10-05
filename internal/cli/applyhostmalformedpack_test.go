package cli

// applyhostmalformedpack_test.go pins the host notch's handling of a configured pack whose
// manifest HAS PROBLEMS but still loads: packload.LoadDir returns the pack AND its problems, and
// every host verb reading packs through resolveConfiguredPack used to keep the pack and drop the
// problems. So a pack `yolo pack lint`, `yolo check` and every launch refuse was applied
// partially by `yolo host apply --assert`, at rc=0. The case the posture-list review found was a
// pack with two `autonomy` contributions (notch-scoped-config-contributions.md NS-D14).
//
// Each test drives a real command path against a t.TempDir() home, and each has a control: the
// same fixture minus the second `autonomy` contribution, which must do what the malformed one
// may not. So deleting the manifest check in resolveConfiguredPack turns every test here red
// except the footer's, which is footerHostPacks' own check.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// malformedProblem is the words of the fixture's one manifest problem, as the strict decoder
// (packdecl.validateSingleAutonomy) spells it.
const malformedProblem = `second "autonomy" contribution`

// badPackJSON is the manifest of the pack "bad": its skills, then contribs (a raw JSON fragment,
// trailing comma included, or ""), then one `autonomy` contribution — and, when malformed, a
// SECOND one, which is the manifest's one problem. The pack still LOADS either way
// (packload.LoadDir returns it), which is what made the problem discardable.
func badPackJSON(contribs string, malformed bool) string {
	m := `{"name":"bad","description":"d","contributes":[
  {"kind":"skills","from":"skills","into":".claude/skills"},` + contribs + `
  {"kind":"autonomy","guarded":{"launch":[{"bin":"claude","flags":[]}]}}`
	if malformed {
		m += `,
  {"kind":"autonomy","autonomous":{"launch":[{"bin":"claude","flags":["--x"]}]}}`
	}
	return m + `]}`
}

// malformedPackHome writes the pack "bad" (badPackJSON) as a LOCAL pack with one skill, and a
// user config selecting `claude` beside it plus extra top-level keys (a raw JSON fragment,
// leading comma included, or ""). It returns the home and the pack dir. HOME and
// XDG_CONFIG_HOME point inside the temp home; YOLO_PACK_ROOT is cleared for gitPackHome's
// reason; the cwd is a temp dir, so no verb here finds a workspace config.
func malformedPackHome(t *testing.T, contribs string, malformed bool, extra string) (home, packDir string) {
	t.Helper()
	home = t.TempDir()
	packDir = filepath.Join(t.TempDir(), "bad")
	writeFile(t, filepath.Join(packDir, "pack.json"), badPackJSON(contribs, malformed))
	writeFile(t, filepath.Join(packDir, "skills", "badskill", "SKILL.md"),
		"---\nname: badskill\ndescription: d\n---\nBad body.\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(t.TempDir())
	selectPacksWith(t, home, `"claude",{"source":"file://`+packDir+`","name":"bad"}`, extra)
	return home, packDir
}

// The fixture is what the tests below say it is: the malformed manifest LOADS, with exactly the
// one problem, and the clean one loads with none.
func TestMalformedPackFixtureLoadsWithOneProblem(t *testing.T) {
	_, dir := malformedPackHome(t, "", true, "")
	p, probs := packload.LoadDir(dir, "bad")
	if p == nil || len(probs) != 1 || !strings.Contains(probs[0], malformedProblem) {
		t.Fatalf("LoadDir = %v, %v; want the pack and exactly the one problem", p, probs)
	}
	_, dir = malformedPackHome(t, "", false, "")
	if p, probs := packload.LoadDir(dir, "bad"); p == nil || len(probs) != 0 {
		t.Fatalf("clean fixture: LoadDir = %v, %v; want the pack and no problem", p, probs)
	}
}

// NO HALF STATES, for a malformed manifest. `--assert` refuses the WHOLE apply: non-zero, nothing
// written anywhere in the home, and the report names the pack, its problem and the fix — the
// refusal the launch makes of the same pack.
func TestApplyHostAssertRefusesAPackWithManifestProblems(t *testing.T) {
	home, _ := malformedPackHome(t, "", true, "")
	before := hashTree(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Fatalf("--assert over a pack with manifest problems must refuse (non-zero rc)\n%s", report)
	}
	if after := hashTree(t, home); after != before {
		t.Errorf("a refused apply wrote into the home — `claude` resolves fine, so a partial "+
			"apply is the half state this refusal exists to prevent\n%s", report)
	}
	for _, want := range []string{"bad", malformedProblem, "manifest", "yolo pack lint",
		"Nothing was written"} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal must contain %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "yolo pack install") {
		t.Errorf("a manifest problem is not a fetch problem, so the remedy is not `yolo pack "+
			"install`:\n%s", report)
	}
}

// A MANIFEST WITH A FIELD FROM A NEWER YOLO is the case a host yolo OLDER than a field meets
// (notch-scoped-config-contributions.md §4.5, the host-manifest-read row). It used to not decode:
// LoadDir substituted an empty manifest and still returned the pack, so the old resolver applied
// the pack's conventional skills/ as if it declared nothing. Since docs/design/patched-forks.md
// PF-D60 the use read ignores and names the field, so the pack resolves, and a launch runs it;
// the apply still refuses it whole, naming the field (PF-D62), because a real home is never
// rendered around what this yolo cannot read.
func TestApplyHostAssertRefusesAPackWhoseManifestDoesNotDecode(t *testing.T) {
	home, dir := malformedPackHome(t, "", false, "")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"bad","description":"d",`+
		`"fieldFromANewerYolo":1,"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
	before := hashTree(t, home)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 || hashTree(t, home) != before {
		t.Fatalf("--assert over an undecodable manifest must refuse and write nothing; rc=%d\n%s",
			rc, report)
	}
	for _, want := range []string{"bad", `unknown field "fieldFromANewerYolo"`, "Nothing was written"} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal must contain %q:\n%s", want, report)
		}
	}
}

// The control: the SAME pack minus the second `autonomy` contribution applies, and its skill
// lands. Otherwise "nothing was written" above could pass for a reason unrelated to the problem.
func TestApplyHostWritesTheSamePackOnceItsManifestIsClean(t *testing.T) {
	home, _ := malformedPackHome(t, "", false, "")
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("fixture control: rc=%d\n%s", rc, report)
	}
	mustExist(t, filepath.Join(home, ".claude", "skills", "badskill", "SKILL.md"),
		"a clean manifest applies")
}

// THE DRY RUN SAYS SO: exit 0 (information, OQ-RO5), the pack and its problem named, and a
// verdict that an --assert would REFUSE.
func TestApplyHostDryRunSaysAMalformedPackWouldBeRefused(t *testing.T) {
	malformedPackHome(t, "", true, "")
	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("the dry run is information and exits 0 (OQ-RO5); rc=%d\n%s", rc, report)
	}
	for _, want := range []string{"An --assert would REFUSE", "bad", malformedProblem} {
		if !strings.Contains(report, want) {
			t.Errorf("the dry run must contain %q:\n%s", want, report)
		}
	}
}

// THE MACHINE DOCUMENT CARRIES THE PROBLEMS AS A LIST, so a consumer branches on the class
// without parsing the reason: outcome `refused`, and the pack with `manifest_problems`.
func TestApplyHostJSONCarriesAMalformedPacksProblems(t *testing.T) {
	malformedPackHome(t, "", true, "")
	var out, errw bytes.Buffer
	if rc := applyHostFormatted(&out, &errw, false, false, nil, "json", nil); rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	var doc struct {
		Outcome    string `json:"outcome"`
		Unresolved []struct {
			Name         string   `json:"name"`
			Reason       string   `json:"reason"`
			NeedsInstall bool     `json:"needs_install"`
			Problems     []string `json:"manifest_problems"`
		} `json:"unresolved_packs"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if doc.Outcome != outcomeRefused {
		t.Errorf("outcome = %q, want %q", doc.Outcome, outcomeRefused)
	}
	if len(doc.Unresolved) != 1 {
		t.Fatalf("unresolved_packs = %+v, want the one malformed pack", doc.Unresolved)
	}
	u := doc.Unresolved[0]
	if u.Name != "bad" || u.NeedsInstall || len(u.Problems) != 1 ||
		!strings.Contains(u.Problems[0], malformedProblem) || !strings.Contains(u.Reason, malformedProblem) {
		t.Errorf("unresolved_packs[0] = %+v, want bad with its one manifest problem", u)
	}
	// Stated once per pack: LoadDir's "pack <name>: " prefix is not repeated inside a list that
	// already sits under the pack's name.
	if strings.HasPrefix(u.Problems[0], "pack bad: ") {
		t.Errorf("manifest_problems repeats the pack name: %q", u.Problems[0])
	}
}

// THE LAUNCH GATE (`yolo host -- <bin>` with host management on) renders NOTHING over a
// malformed pack and names it with its problem — the incomplete-set disposition — and refuses the
// launch, as every launch-shaped verb does (NC-D5).
func TestHostApplyGateRendersNothingOverAMalformedPack(t *testing.T) {
	for _, malformed := range []bool{true, false} {
		home, _ := malformedPackHome(t, "", malformed, `,"host_apply_on_launch":true`)
		t.Setenv("YOLO_VERSION", "")
		setGateTTY(t, false)
		if lock := tryHostApplyLock(home); lock != nil {
			lock.Close()
		}
		before := hashTree(t, home)
		var errw bytes.Buffer
		launched := hostApplyGate(&errw, nil, "claude")
		if launched != !malformed {
			t.Fatalf("malformed=%v: the gate launched = %v:\n%s", malformed, launched, errw.String())
		}
		wrote := hashTree(t, home) != before
		if !malformed {
			if !wrote {
				t.Fatalf("fixture control: the gate rendered nothing over a clean set:\n%s", errw.String())
			}
			continue
		}
		if wrote {
			t.Errorf("the gate rendered a set holding a malformed pack:\n%s", errw.String())
		}
		for _, want := range []string{"did not render", "bad", malformedProblem, "yolo pack lint"} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("the gate's report must contain %q:\n%s", want, errw.String())
			}
		}
	}
}

// `yolo host env` (and `yolo host --`, which composes through the same composeHostVars) REFUSES
// over a malformed pack, naming it and its problem, and prints nothing for a shell to eval: a
// launch-shaped verb refuses a pack set it cannot complete at every notch (NC-D5), as a jail launch
// refuses this config. It used to compose without the pack and warn, which read as the pack's
// env simply being missing.
func TestHostEnvRefusesAMalformedPack(t *testing.T) {
	const envContrib = `{"kind":"env","vars":{"BAD_PACK_VAR":"from-bad"}},`
	for _, malformed := range []bool{true, false} {
		malformedPackHome(t, envContrib, malformed, "")
		var out, errw bytes.Buffer
		rc := hostMain([]string{"env", "--agent", "bash"}, &out, &errw, false, nil)
		if !malformed {
			if rc != 0 || !strings.Contains(out.String(), "BAD_PACK_VAR") {
				t.Fatalf("fixture control: a clean pack's env did not compose: rc=%d\n%s%s",
					rc, out.String(), errw.String())
			}
			continue
		}
		if rc == 0 || out.Len() != 0 {
			t.Errorf("yolo host env composed over a malformed pack: rc=%d\n%s", rc, out.String())
		}
		for _, want := range []string{"bad", malformedProblem, "yolo pack lint", "a jail launch refuses"} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("the refusal must contain %q:\n%s", want, errw.String())
			}
		}
	}
}

// `yolo host apply --revert` reads no surface from a malformed pack, and says the keys only it
// declares stay recorded — its disposition for any unresolvable pack.
func TestHostRevertNamesAMalformedPack(t *testing.T) {
	malformedPackHome(t, "", true, "")
	var out, errw bytes.Buffer
	if rc := hostRevert(&out, &errw, false, false); rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{"bad could not be resolved", "stay recorded", malformedProblem} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the revert must say %q:\n%s", want, errw.String())
		}
	}
	for _, p := range hostRevertCandidates(&bytes.Buffer{}) {
		if p.Name == "bad" {
			t.Errorf("a malformed pack is a revert candidate")
		}
	}
}

// `yolo capture` does not capture an installer a malformed pack declares: the pack is named as
// not searched, with its problem, and the clean control finds the installer.
func TestCaptureTargetSkipsAMalformedPacksInstaller(t *testing.T) {
	const installer = `{"kind":"program","bin":"badtool","via":"installer","url":"https://example/i.sh"},`
	for _, malformed := range []bool{true, false} {
		malformedPackHome(t, installer, malformed, "")
		target, err := resolveCaptureTarget("badtool")
		if !malformed {
			if err != nil || target == nil || target.Pack != "bad" {
				t.Fatalf("fixture control: target=%+v err=%v, want bad's installer", target, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("a malformed pack's installer was captured: %+v", target)
		}
		for _, want := range []string{"not searched", "bad", malformedProblem} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the capture error must contain %q: %v", want, err)
			}
		}
	}
}

// `yolo config render` is read-only inspection, so it REPORTS rather than refuses: rc 0, the
// malformed pack's overlay is not folded into the preview, and stderr names the pack and its
// problem.
func TestConfigRenderReportsAMalformedPackAndFoldsNothingFromIt(t *testing.T) {
	const overlay = `{"kind":"config-overlay","surface":"claude/settings",` +
		`"config":{"managed":{"badOverlayKey":"from-bad"}}},`
	for _, malformed := range []bool{true, false} {
		malformedPackHome(t, overlay, malformed, "")
		var out, errw bytes.Buffer
		if rc := configRender(jailPreviewTargetForTest(t), []string{"claude/settings"}, &out, &errw, false); rc != 0 {
			t.Fatalf("malformed=%v: config render rc=%d\n%s%s", malformed, rc, out.String(), errw.String())
		}
		folded := strings.Contains(out.String(), "badOverlayKey")
		if !malformed {
			if !folded {
				t.Fatalf("fixture control: a clean pack's overlay was not folded:\n%s%s",
					out.String(), errw.String())
			}
			continue
		}
		if folded {
			t.Errorf("config render folded a malformed pack's overlay:\n%s", out.String())
		}
		for _, want := range []string{"not folded", "bad", malformedProblem} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("config render must report %q:\n%s", want, errw.String())
			}
		}
	}
}

// `yolo check-deps` probes nothing a malformed pack declares, names the pack with its problem,
// and exits non-zero: an unprobed pack is not a clean bill of health.
func TestCheckDepsNamesAMalformedPackAndProbesNothingFromIt(t *testing.T) {
	const dep = `{"kind":"requires","bin":"yolo-test-malformed-pack-dep"},`
	for _, malformed := range []bool{true, false} {
		malformedPackHome(t, dep, malformed, "")
		var out, errw bytes.Buffer
		rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
		probed := strings.Contains(out.String(), "yolo-test-malformed-pack-dep")
		if !malformed {
			if !probed {
				t.Fatalf("fixture control: a clean pack's dep was not probed:\n%s%s", out.String(), errw.String())
			}
			continue
		}
		if probed {
			t.Errorf("check-deps probed a malformed pack's dep:\n%s", out.String())
		}
		if rc == 0 {
			t.Errorf("check-deps exited 0 with a pack it could not read:\n%s", out.String())
		}
		for _, want := range []string{"bad could not be resolved", malformedProblem} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("check-deps must say %q:\n%s", want, out.String())
			}
		}
	}
}

// THE FOOTER'S PACK SET (footerHostPacks, its own resolver) skips a malformed pack, as the host
// launch it mirrors does: a provider only that pack declares is absent from the footer's tables.
func TestHostFooterTablesSkipAMalformedPack(t *testing.T) {
	const provider = `{"kind":"provider","name":"badprov",` +
		`"endpoints":{"openai":{"base_url":"https://bad.example/v1"}}},`
	for _, malformed := range []bool{true, false} {
		malformedPackHome(t, provider, malformed,
			`,"profiles":{"bp":{"provider":"badprov"}},"profile":{"claude":"bp"}`)
		tables := hostFooterTables()
		has := strings.Contains(tables.Providers, "bad.example")
		if !malformed {
			if !has {
				t.Fatalf("fixture control: a clean pack's provider is not in the footer's table: %+v", tables)
			}
			continue
		}
		if has {
			t.Errorf("the footer composed a malformed pack's provider: %s", tables.Providers)
		}
	}
}
