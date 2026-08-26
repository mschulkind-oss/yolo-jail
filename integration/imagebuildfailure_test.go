package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// FAILED IMAGE BUILD → ONE HONEST LINE, NOT A DOWNSTREAM ASSERTION.
//
// THE PROBLEM, measured. The `packages:` tests (packages_test.go) each trigger a
// per-workspace --impure image build INSIDE the CLI under test. On 2026-08-15
// that build failed on the macOS nightly, the CLI fell back to the
// already-loaded image, the jail came up, and the tests reported
// `libzbar.so.0 not linked into /lib` — a lib-farm assertion for a nix error.
// The reader spends the morning on the lib farm; the bug is two layers up.
//
// This is a DIFFERENT axis from imageskew_test.go, and both are needed. Skew
// asks "is the image the suite is about to reuse built from this source tree?"
// once, at TestMain, and refuses to start when it is not. It cannot see this
// case at all: the image it checked was fine, and the build that failed happened
// later, per-test, inside a `yolo run` for a workspace with its own `packages:`
// list. So the detection has to live where that output arrives — at every run*
// helper — and it does, via runCommand.
//
// WHY MATCH ON OUTPUT rather than on the exit code. The CLI now exits non-zero
// on a failed build (internal/image: fatal by default), so a nonzero rc alone
// would already fail the test — but as `zbar lib-farm probe script failed (rc
// 1)`, which is the same misattribution in a smaller font. The marker names the
// cause. It also catches the case the exit code cannot: a run that CONTINUED on
// a stale image because YOLO_ALLOW_STALE_IMAGE was set in the environment. That
// is a legitimate choice for a human at a terminal and never a legitimate basis
// for an integration result, so the harness fails on the report either way.

// imageBuildFailureSection extracts the CLI's failed-build report from a run's
// combined output, or "" when there is none.
//
// Pure, and covered under -short: the guard has to keep working in an
// environment where no container runs, because a harness guard that silently
// stops matching returns the suite to exactly the behavior it exists to remove.
// The 60-line cap keeps a t.Fatalf readable — the report is ~25 lines and
// everything after it is the rest of a jail launch.
func imageBuildFailureSection(combined string) string {
	i := strings.Index(combined, image.BuildFailedMarker)
	if i < 0 {
		return ""
	}
	lines := strings.Split(combined[i:], "\n")
	if len(lines) > 60 {
		lines = lines[:60]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// failIfImageBuildFailed aborts the calling test when the CLI reported a failed
// image build, quoting the report. Called by runCommand, so every run* helper
// inherits it and no test has to remember.
func failIfImageBuildFailed(t *testing.T, args []string, r result) {
	t.Helper()
	section := imageBuildFailureSection(r.combined())
	if section == "" {
		return
	}
	t.Fatalf("THE JAIL IMAGE BUILD FAILED — this test never ran against the image it "+
		"asked for.\nEvery assertion below it would describe some OTHER image, so the "+
		"failure is reported here, at its cause.\n\n"+
		"  command : yolo %s\n  exit code: %d\n\n%s",
		strings.Join(args, " "), r.rc, section)
}

// TestImageBuildFailureSectionMatchesWhatTheCLIPrints is the load-bearing test
// of this file: it drives the REAL image.AutoLoadImage (with the build seam
// forced to fail) and asserts the harness's matcher finds the report the CLI
// actually emits. A hand-written fixture string would pass forever while the two
// sides drifted apart — and a silently non-matching guard is indistinguishable
// from no guard, which is the failure mode this whole file is about.
//
// No container, no nix: it runs under -short with the pre-commit gate.
func TestImageBuildFailureSectionMatchesWhatTheCLIPrints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	const nixSaid = "error: builder for '/nix/store/aaa-zbar.drv' failed with exit code 1"
	var out bytes.Buffer
	res := image.AutoLoadImage(image.AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{nixSaid}
		},
		// An image IS present — the fallback that used to make this silent.
		Run:       func([]string) (int, bool) { return 0, true },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if res.OK {
		t.Fatalf("AutoLoadImage returned true after a failed build; the harness guard "+
			"assumes a failed build cannot present as success:\n%s", out.String())
	}
	if res.Ref != "" {
		t.Errorf("a failed load returned ref %q; there is no image to name", res.Ref)
	}

	section := imageBuildFailureSection(out.String())
	if section == "" {
		t.Fatalf("the harness cannot find the CLI's failed-build report — the guard is "+
			"dead and every packages_test failure will be misattributed again.\n"+
			"CLI output was:\n%s", out.String())
	}
	if !strings.Contains(section, nixSaid) {
		t.Errorf("the extracted section drops nix's own error, which is the only line "+
			"that explains anything:\n%s", section)
	}
}

// TestImageBuildFailureSectionIgnoresOrdinaryOutput: the guard must not fire on
// a normal run, or it converts every green suite into a mystery.
func TestImageBuildFailureSectionIgnoresOrdinaryOutput(t *testing.T) {
	for _, ordinary := range []string{
		"",
		"Image load needed: nix store path changed\n  new: /nix/store/abc\nDone: loaded image\n",
		// The post-C3 shape of the same run: podman's image arrives over a pipe,
		// so the size line reports what was STREAMED rather than what was cached.
		"Image load needed: nix store path changed\n  new: /nix/store/abc\n" +
			"  Streamed image: 3.3 GB\nDone: loaded image\n",
		"Using existing localhost/yolo-jail:latest image.\n",
	} {
		if got := imageBuildFailureSection(ordinary); got != "" {
			t.Errorf("guard fired on ordinary output %q → %q", ordinary, got)
		}
	}
}
