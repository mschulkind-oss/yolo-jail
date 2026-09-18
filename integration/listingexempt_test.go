package integration

// listingexempt_test.go pins that `go test -list` over this package ENUMERATES rather than
// refusing, and it drives the real invocation rather than the predicate.
//
// A test that called listingOnly() directly would stay green with the call site deleted from
// runSuite — the callee-pinned/call-site-unpinned shape AGENTS.md names and this repo has
// shipped six times. So this runs the command `apple-container.yml` actually runs, in a
// subprocess, and asserts it prints names.
//
// It terminates: the subprocess is ITSELF a listing, so it returns before any setup.

import (
	"os/exec"
	"strings"
	"testing"
)

// TestListingDoesNotRequireAFreshImage is the regression for a job that reported a broken
// test suite because an image was stale.
//
// `apple-container.yml` selects its subset with `go test -list '^TestAppleContainer'` and
// refuses to pass vacuously on an empty listing. On 2026-09-18 a flake edit moved
// `imageIdentity`, the self-hosted Mac's image went stale, TestMain's skew check refused, the
// listing printed nothing, and the guard fired naming its two stated causes — "the suite was
// renamed out from under this filter, or ./integration does not compile" — neither of which
// was true.
func TestListingDoesNotRequireAFreshImage(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("locating module root: %v", err)
	}
	cmd := exec.Command("go", "test", "-list", "^TestAppleContainer", "./integration")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a LISTING must not fail, whatever the loaded image is: %v\n%s", err, out)
	}

	var names int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "TestAppleContainer") {
			names++
		}
	}
	if names == 0 {
		t.Errorf("the listing selected no tests — this is the exact input that made "+
			"apple-container.yml report a renamed suite for a stale image:\n%s", out)
	}
}
