package image

import (
	"fmt"
	"strings"
)

// A FAILED IMAGE BUILD MUST FAIL AS ITSELF.
//
// The defect this file exists to remove: when the nix build failed,
// AutoLoadImage's `currentPath == ""` branch fell through to "is an image
// already loaded? then use it", printed `Using existing yolo-jail:latest
// image.`, and returned true — discarding buildTail, the ONE artifact that
// could have explained anything. Two live consequences:
//
//   - A developer got a working-looking jail running code that was not theirs.
//   - 2026-08-15: it made two macOS integration tests (TestExtraPackageLibFarm,
//     TestDevPackageLinksRuntimeLib) fail with `libzbar.so.0 not linked into
//     /lib`, when the real cause was that the per-workspace `packages:` image
//     build had failed and the run fell back to an image that of course had no
//     zbar in it. The symptom was reported two layers from the cause, so the
//     reader hunts the lib farm while the bug is in nix.
//
// The rule the report enforces: a run may end up on an image this invocation
// could not rebuild, but it must never be able to LOOK SUCCESSFUL while
// silently stale.

const (
	// BuildFailedMarker is the headline of every failed-build report, and the
	// stable string other tooling greps for. The integration harness matches on
	// it to attribute a downstream test failure to the build (see
	// integration/imagebuildfailure_test.go), so it is part of this package's
	// contract: change the wording around it freely, change THIS and update the
	// harness in the same commit.
	BuildFailedMarker = "IMAGE BUILD FAILED"

	// StaleImageEnv is the escape hatch out of the fatal default: any non-empty
	// value downgrades "refuse to launch" to "launch, but say loudly that this
	// image is stale". See the fatality argument in autoload.go.
	StaleImageEnv = "YOLO_ALLOW_STALE_IMAGE"
)

// buildTailTextLimit is how many captured stderr lines the report reproduces.
// buildImageStorePathArgs retains 30, and all 30 are worth printing: nix puts
// the actual error near the end but the failing derivation's name near the
// start, and a reader who has to go re-run the build by hand to see the rest
// has been handed the same non-answer this report exists to replace.
const buildTailTextLimit = 30

// buildFailureReport renders the whole failed-build announcement.
//
// title/remedy are the DiagnoseFailure classification (nixdiag's "needs a Linux
// builder" and friends); buildTail is nix's own stderr. Both are printed —
// the classification is a shortcut for known shapes, not a replacement for the
// evidence, and the classifier's fallback branch is literally "I don't know".
//
// staleAllowed picks the closing section: refusing (the default) or continuing
// with the staleness stated. Kept pure so both variants are pinned by tests
// without a nix, a runtime, or a container in sight.
func buildFailureReport(title, remedy string, buildTail []string, staleAllowed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s — the jail image was NOT rebuilt from this source tree.\n\n",
		BuildFailedMarker)

	// nix's own words come FIRST. Whatever else this report gets wrong, the line
	// that says what actually broke has to reach the human; withholding it is
	// the original defect, not a formatting choice.
	tail := buildTail
	if len(tail) > buildTailTextLimit {
		tail = tail[len(tail)-buildTailTextLimit:]
	}
	if len(tail) == 0 {
		b.WriteString("  The build produced no output at all — is `nix` on PATH?\n")
	} else {
		fmt.Fprintf(&b, "  What the build said (last %d line(s)):\n", len(tail))
		for _, line := range tail {
			b.WriteString("    | " + line + "\n")
		}
	}
	b.WriteString("\n")

	if title != "" {
		fmt.Fprintf(&b, "  Diagnosis: %s\n", title)
	}
	if remedy != "" && !remedyEchoesTail(remedy, buildTail) {
		for _, line := range strings.Split(remedy, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	b.WriteString("\n")

	if staleAllowed {
		fmt.Fprintf(&b, "  %s is set — CONTINUING ON A STALE IMAGE.\n", StaleImageEnv)
		b.WriteString("  Whatever runs next was built from a DIFFERENT source tree than this\n")
		b.WriteString("  one. Do not read anything it does as evidence about your changes.\n")
		return b.String()
	}

	if title != "" {
		fmt.Fprintf(&b, "Cannot start jail: %s.\n", title)
	} else {
		b.WriteString("Cannot start jail: the image build failed.\n")
	}
	b.WriteString("  Refusing to fall back to the already-loaded or cached image: it was\n")
	b.WriteString("  built from a DIFFERENT source tree, so anything observed next — a\n")
	b.WriteString("  passing test, a missing library, a feature that \"works\" — would be\n")
	b.WriteString("  evidence about code that is not the code you just changed. Reporting a\n")
	b.WriteString("  symptom two layers from its cause is what this refusal prevents.\n")
	fmt.Fprintf(&b, "  If the loaded image is knowingly fine (offline, out of disk, bisecting\n"+
		"  a host-side-only change), launch on it deliberately:\n"+
		"      %s=1 <your yolo command>\n", StaleImageEnv)
	return b.String()
}

// remedyEchoesTail reports whether the "remedy" is nothing but the stderr tail
// the report already printed verbatim.
//
// nixdiag.DiagnoseNixBuildFailure's UNCLASSIFIED branch returns exactly
// strings.Join(last 10 stderr lines) as its remediation. Printing the tail and
// then printing it again under "Diagnosis" doubles the noise precisely in the
// case where the reader has the least to go on. A CLASSIFIED remedy ("set up a
// Linux builder…") never matches, so it is never swallowed.
func remedyEchoesTail(remedy string, buildTail []string) bool {
	if len(buildTail) == 0 {
		return false
	}
	tail := buildTail
	if len(tail) > 10 {
		tail = tail[len(tail)-10:]
	}
	return strings.TrimSpace(remedy) == strings.TrimSpace(strings.Join(tail, "\n"))
}
