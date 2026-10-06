package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// stubApplyJailLaunch replaces the launch `yolo apply` runs at the jail notch with one that
// counts its calls and returns rc, so a test sees the verb reach it without a container.
func stubApplyJailLaunch(t *testing.T, rc int) *int {
	t.Helper()
	calls := 0
	prev := applyJailLaunch
	applyJailLaunch = func() int { calls++; return rc }
	t.Cleanup(func() { applyJailLaunch = prev })
	return &calls
}

// TestApplyAtJailRunsTheLaunchsReadinessAct pins JR-D1 (docs/design/jail-notch-readiness.md):
// at the jail notch `yolo apply` IS the launch `yolo -- true` performs, which installs every
// program a selected pack declares (OQ-JR1), and its status is that launch's. It used to be a
// pointer that said the launch installs nothing; the line it prints now says what the launch
// does, attach included, since an attach is the one launch that installs nothing.
func TestApplyAtJailRunsTheLaunchsReadinessAct(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
	calls := stubApplyJailLaunch(t, 78)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "jail"}, &out, &errw, false, nil); rc != 78 {
		t.Errorf("apply --at jail rc = %d, want the launch's own status (78)\nstdout:\n%s\nstderr:\n%s",
			rc, out.String(), errw.String())
	}
	if *calls != 1 {
		t.Fatalf("apply --at jail ran the launch %d times, want once", *calls)
	}
	got := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"readiness act", "`yolo -- true`", "installs every program they declare",
		"builds the image (on a container runtime; macos-user has none)", "attached to instead, and that installs nothing"} {
		if !strings.Contains(got, want) {
			t.Errorf("apply --at jail does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "NOT installed by the launch") {
		t.Errorf("apply --at jail still says the launch installs no program:\n%s", got)
	}
}

// --dry-run at the jail notch launches nothing and prints the description the launch would
// provision.
func TestApplyAtJailDryRunLaunchesNothing(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
	calls := stubApplyJailLaunch(t, 1)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "jail", "--dry-run"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --at jail --dry-run rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	if *calls != 0 {
		t.Errorf("--dry-run launched a jail")
	}
	if !strings.Contains(out.String(), "nothing is launched") {
		t.Errorf("--dry-run does not say it launched nothing:\n%s", out.String())
	}
}

// THE SEAM'S PRODUCTION VALUE is `yolo -- true`: run's own pipeline, with `true` as the command.
// A test that stubbed the seam and stopped there would pass for a verb that launched anything.
func TestApplyJailLaunchIsYoloDashDashTrue(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
	var seen run.Options
	launched := 0
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; launched++; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })

	if rc := applyJailLaunch(); rc != 0 || launched != 1 {
		t.Fatalf("applyJailLaunch rc=%d, launched %d time(s); want the run pipeline once", rc, launched)
	}
	if !slices.Equal(seen.Args, []string{"true"}) {
		t.Errorf("the launch's command is %q, want exactly [true]", seen.Args)
	}
}

// `--at jail` IS THE NOTCH THE LAUNCH IS JUDGED AT. The launch re-reads `confinement` unless it
// is told otherwise (run.refuseUnbuiltNotch), so a verb that dropped the flag refused a
// `confinement: host` config's `yolo apply --at jail`, naming the config key the user had
// overridden. Driven through applyMain with the run pipeline substituted, so the assertion is
// downstream of the flag's whole path: the verb's parse, applyJailLaunch, runRun's parse.
func TestApplyAtJailCarriesTheFlagIntoTheLaunch(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement": "host"}`)
	var seen run.Options
	launched := 0
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; launched++; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "jail"}, &out, &errw, false, nil); rc != 0 || launched != 1 {
		t.Fatalf("apply --at jail rc=%d, launched %d time(s)\nstdout:\n%s\nstderr:\n%s",
			rc, launched, out.String(), errw.String())
	}
	if seen.Notch != "jail" {
		t.Errorf("the launch's notch override is %q, want \"jail\": it would re-read `confinement: host` and refuse", seen.Notch)
	}
}

// At the jail notch the line must not promise the install on macos-user, whose stage does not run
// the readiness act yet (JR-D2).
func TestApplyAtJailDoesNotPromiseTheInstallOnMacosUser(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
	stubApplyJailLaunch(t, 0)
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "jail"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errw.String())
	}
	got := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(got, "on macos-user each installs the first time it is run") {
		t.Errorf("apply --at jail promises the install on every backend:\n%s", got)
	}
	var help, herr bytes.Buffer
	applyMain([]string{"--help"}, &help, &herr, false, nil)
	if h := strings.Join(strings.Fields(help.String()), " "); !strings.Contains(h, "(not yet on macos-user)") {
		t.Errorf("apply --help promises the install on every backend:\n%s", h)
	}
}

// TestApplyUsageSaysTheJailNotchProvisions pins `yolo apply --help` to the same truth: at the
// jail notch the verb is the launch's readiness act, which installs the declared programs.
func TestApplyUsageSaysTheJailNotchProvisions(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--help"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --help rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	help := strings.Join(strings.Fields(out.String()+errw.String()), " ")
	if !strings.Contains(help, "yolo apply — ") {
		t.Fatalf("apply --help did not print the usage:\n%s", help)
	}
	for _, want := range []string{"installs every program they declare", "a jail already running is attached to, installing nothing"} {
		if !strings.Contains(help, want) {
			t.Errorf("apply --help does not say %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "declared programs still install on first use") {
		t.Errorf("apply --help still says declared programs install on first use:\n%s", help)
	}
}

// TestTopLevelHelpSaysApplyRunsTheJailsReadinessAct pins the `yolo --help` blurb for apply to the
// same truth as its own usage.
func TestTopLevelHelpSaysApplyRunsTheJailsReadinessAct(t *testing.T) {
	var blurb string
	for _, c := range commandHelp {
		if c.name == "apply" {
			blurb = c.blurb
		}
	}
	if blurb == "" {
		t.Fatal("commandHelp has no apply entry")
	}
	if strings.Contains(blurb, "points at the launch") {
		t.Errorf("yolo --help still says apply only points at the launch: %q", blurb)
	}
	if !strings.Contains(blurb, "readiness act") {
		t.Errorf("yolo --help's apply blurb does not say what it does at jail: %q", blurb)
	}
}
