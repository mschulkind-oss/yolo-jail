package cli

// skewedpack_test.go pins the host notch's handling of a configured pack holding a contribution
// this yolo cannot read (docs/design/patched-forks.md PF-D68 to PF-D70): a field a newer yolo
// reads. The one pack resolver reads it as a USE READ (packload.LoadDirForUse), so the pack
// resolves and the contribution is skipped and named. Each verb then keeps its own disposition:
// `yolo host --` launches and prints the line, `yolo host apply --assert` refuses and writes
// nothing, and the launch gate renders nothing and launches on the last apply.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// skewedSkillsContribution is the fixture's unreadable contribution: the pack's own skills, with
// a field no yolo knows, so the skill must not reach the home while it is skipped.
const skewedSkillsContribution = `{"kind":"skills","from":"skills","into":".claude/skills",` +
	`"fieldFromANewerYolo":true}`

// skewedPackHome is malformedPackHome's clean "bad" pack with its skills contribution replaced by
// skewedSkillsContribution, and with badProfileContribs beside it so a launch can select a
// profile the pack declares. extra is the user config's extra top-level keys.
func skewedPackHome(t *testing.T, extra string) (home, packDir string) {
	t.Helper()
	home, packDir = malformedPackHome(t, "", false, extra)
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"bad","description":"d","contributes":[`+
		skewedSkillsContribution+`,`+badProfileContribs+
		`{"kind":"autonomy","guarded":{"launch":[{"bin":"claude","flags":[]}]}}]}`)
	return home, packDir
}

// `yolo pack lint` STAYS STRICT: the authoring read refuses the field a launch skips, naming it,
// so an author still hears about a typo.
func TestPackLintRefusesAFieldALaunchSkips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","contributes":[`+
		`{"kind":"env","vars":{"A":"1"},"field_from_a_newer_yolo":1}]}`)
	var out, errw bytes.Buffer
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
	got := out.String() + errw.String()
	if rc == 0 || strings.Contains(got, "pack ok") || !strings.Contains(got, `unknown field "field_from_a_newer_yolo"`) {
		t.Fatalf("lint must refuse the field by name (rc=%d):\n%s", rc, got)
	}
}

// `yolo host -- claude` LAUNCHES over the skipped contribution — the request's whole point: a
// pack written for a newer yolo used to refuse every launch on this host — and the pack's other
// contributions reach it (the profile only it declares resolves), and the launch says what it
// skipped, naming the pack, the kind and the field.
func TestHostLaunchRunsAPackWithAFieldFromANewerYolo(t *testing.T) {
	skewedPackHome(t, `,"profile":{"claude":"bp"}`)
	rc, reached, errw := hostExecRun(t, "claude")
	if rc != 0 || !reached {
		t.Fatalf("a pack holding a field from a newer yolo must not refuse the launch; rc=%d "+
			"reached=%v\n%s", rc, reached, errw)
	}
	for _, want := range []string{"Warning: pack bad: contributes[0]: skipping the skills contribution",
		`unknown field "fieldFromANewerYolo"`, "update yolo", "yolo pack lint"} {
		if !strings.Contains(errw, want) {
			t.Errorf("the launch must say what it skipped (%q):\n%s", want, errw)
		}
	}
	if strings.Contains(errw, `no profile named "bp"`) {
		t.Errorf("the pack's readable contributions must reach the launch:\n%s", errw)
	}
}

// `yolo host apply --assert` REFUSES the same set and writes nothing: rendering a real home around
// a skipped contribution would retire what an earlier render of it wrote (PF-D70). The report names
// the pack, the field and the remedy; the control is the same pack without the field, whose skill
// lands.
func TestApplyHostAssertRefusesAPackHoldingAContributionItCannotRead(t *testing.T) {
	home, _ := skewedPackHome(t, "")
	before := hashTree(t, home)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Fatalf("--assert over a skipped contribution must refuse\n%s", report)
	}
	if after := hashTree(t, home); after != before {
		t.Errorf("a refused apply wrote into the home\n%s", report)
	}
	for _, want := range []string{"cannot be read whole", "bad", `unknown field "fieldFromANewerYolo"`,
		"update yolo", "read whole", "Nothing was written"} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal must contain %q:\n%s", want, report)
		}
	}

	home, packDir := skewedPackHome(t, "")
	writeFile(t, filepath.Join(packDir, "pack.json"), badPackJSON("", false))
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("control: the same pack without the field must apply; rc=%d\n%s", rc, report)
	}
	mustExist(t, filepath.Join(home, ".claude", "skills", "badskill", "SKILL.md"), "the control applies")
}

// THE LAUNCH GATE (host_apply_on_launch) renders nothing over the same set, as for any incomplete
// one, but LAUNCHES on the last apply, because the launch reads the pack as this yolo can and an
// unresolvable pack is the only set it refuses (NC-D5).
func TestHostApplyGateLaunchesOnTheLastApplyOverASkippedContribution(t *testing.T) {
	home, _ := skewedPackHome(t, `,"host_management":"own","host_apply_on_launch":true`)
	t.Setenv("YOLO_VERSION", "")
	setGateTTY(t, false)
	// The gate's lock file is the gate's own, not a render (hookHome's reason).
	if lock := tryHostApplyLock(home); lock != nil {
		lock.Close()
	}
	before := hashTree(t, home)

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("the gate must launch over a pack whose only fault is a skipped contribution:\n%s",
			errw.String())
	}
	if after := hashTree(t, home); after != before {
		t.Errorf("the gate rendered a pack set with a skipped contribution:\n%s", errw.String())
	}
	for _, want := range []string{"did not render your host configuration", "bad", "update yolo",
		"Launching claude against the configuration your last apply left in place"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the gate's line must contain %q:\n%s", want, errw.String())
		}
	}
}
