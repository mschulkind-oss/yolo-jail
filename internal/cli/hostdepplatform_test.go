package cli

// hostdepplatform_test.go pins the HOST's call site of the one installable-program predicate
// (packdecl.DepRequirement.UnpublishedReason, docs/plans/notch-convergence.md item 7, row B5): a
// `program` whose `platforms` exclude this host is not offered an install by `yolo host apply`
// and not counted missing by `yolo check-deps`, as a jail declines its launcher. The jail's call
// site is pinned in internal/entrypoint (launcherplatform_test.go).

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// unpublishedProgramHome selects claude beside a pack whose one program installs through a
// vendor installer, with platforms (a JSON fragment such as `,"platforms":["plan9"]`, or "").
// plan9 is a real GOOS no test host runs, so the program is unpublished on every machine.
func unpublishedProgramHome(t *testing.T, platforms string) {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "unpub")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"unpub","description":"d","contributes":[`+
			`{"kind":"program","bin":"yolo-unpublished-probe-bin","via":"installer",`+
			`"url":"https://example.invalid/install.sh"`+platforms+`}]}`)
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"unpub"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

var unpublishedRemedy = packdecl.InstallerRemedy("https://example.invalid/install.sh")

// HOST APPLY OFFERS NO INSTALL THE VENDOR DOES NOT PUBLISH. Before, `platforms` was ignored at
// the host: the program was a missing-dependency blocker whose remedy was the vendor's
// installer, for a build that does not exist.
func TestHostApplyOffersNoInstallTheVendorDoesNotPublish(t *testing.T) {
	// The control: published everywhere, the same program IS a missing dependency with the
	// installer as its remedy — so the case below has an offer to withhold.
	unpublishedProgramHome(t, "")
	survey, report := surveyApply(t)
	if got := survey.MissingDeps(); len(got) != 1 || !strings.Contains(report, unpublishedRemedy) {
		t.Fatalf("fixture control: a published program must be missing with its installer "+
			"offered; missing=%v\n%s", got, report)
	}

	unpublishedProgramHome(t, `,"platforms":["plan9"]`)
	survey, report = surveyApply(t)
	if got := survey.MissingDeps(); len(got) != 0 {
		t.Errorf("an unpublished program was counted missing (a blocker): %v\n%s", got, report)
	}
	if strings.Contains(report, unpublishedRemedy) {
		t.Errorf("host apply offered the vendor's installer for a build it does not publish:\n%s", report)
	}
	if _, _, _, unpublished := survey.Deps(); unpublished != 1 {
		t.Errorf("want the program counted as unpublished, got %d\n%s", unpublished, report)
	}
	// The verdict names it by default; the per-contribution line carrying the predicate's
	// sentence is detail-on-demand, like every other dep line.
	if want := "1 with no build for this host (yolo-unpublished-probe-bin)"; !strings.Contains(report, want) {
		t.Errorf("the verdict must name the unpublished program; want %q\n%s", want, report)
	}
	why := survey.deps["yolo-unpublished-probe-bin"].Unpublished
	for _, want := range []string{"plan9", "nothing can be installed to fix it"} {
		if !strings.Contains(why, want) {
			t.Errorf("the finding must carry the predicate's reason; want %q in %q", want, why)
		}
	}
}

// CHECK-DEPS IS THE SAME PROBE: an unpublished program is a line with its reason, not a MISSING
// with a remedy, and not an exit 1 — nothing is missing that anything could install.
func TestCheckDepsCountsNoUnpublishedProgramMissing(t *testing.T) {
	unpublishedProgramHome(t, "")
	var out, errw bytes.Buffer
	if rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false); rc != 1 ||
		!strings.Contains(out.String(), unpublishedRemedy) {
		t.Fatalf("fixture control: a published missing program must exit 1 with its remedy; rc=%d\n%s",
			rc, out.String())
	}

	unpublishedProgramHome(t, `,"platforms":["plan9"]`)
	out.Reset()
	errw.Reset()
	rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	got := out.String()
	if strings.Contains(got, unpublishedRemedy) || strings.Contains(got, "yolo-unpublished-probe-bin MISSING") {
		t.Errorf("check-deps offered an install the vendor does not publish:\n%s", got)
	}
	if !strings.Contains(got, "no build for this host") {
		t.Errorf("check-deps must name the reason:\n%s", got)
	}
	if strings.Contains(got, "MISSING") {
		t.Skipf("this host lacks a shipped dependency too (rc=%d), so the exit code says nothing "+
			"about the unpublished program:\n%s", rc, got)
	}
	if rc != 0 {
		t.Errorf("an unpublished program alone must not fail check-deps; rc=%d\n%s", rc, got)
	}
}
