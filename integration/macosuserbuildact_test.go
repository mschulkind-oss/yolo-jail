package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macosuserbuildact_test.go is the macos-user cell for four facts of the sealed build act
// (docs/reference/pi-extensions.md PPX-D46 to PPX-D49), all four met on the maintainer's Mac by one
// patched extension's build line, `… && shopt -s globstar && rm -f packages/**/*.test.ts`:
//
//   - the build's script runs under macOS's own /bin/bash, the platform's base system, so a build
//     line must be portable to it; that globstar line was the recipe's bug to fix (PPX-D46);
//   - a build line that ran and failed is said as one, naming its log, never as a build jail that
//     refused to start (PPX-D47);
//   - the build jail prints no line meant for an agent session: no ownerless list, no shared-network
//     line (PPX-D48);
//   - what the build line left running ends with it, so the act's guest pack tree is removed rather
//     than kept for `yolo prune` (PPX-D49).
//
// The fixture is macosuserpatchedext_test.go's. ⚠ Mac-only, behind requireMacosUser.

// buildJailLines is every line launch.log records from the build jail of key: the lines it marks
// "  [<key>] ".
func buildJailLines(t *testing.T, ws, key string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, ".yolo", "logs", "launch.log"))
	if err != nil {
		t.Fatalf("launch.log: %v", err)
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, "["+key+"] ") {
			out = append(out, l)
		}
	}
	return out
}

// noAgentSessionLines fails on any line of the build jail meant for an agent session (PPX-D48), or
// any sign it kept its guest pack tree (PPX-D49).
func noAgentSessionLines(t *testing.T, lines []string) {
	t.Helper()
	for _, l := range lines {
		for _, bad := range []string{"has no owner", "shares the host's network",
			"Kept guest pack tree"} {
			if strings.Contains(l, bad) {
				t.Errorf("the build jail printed %q: %s", bad, l)
			}
		}
	}
}

// TestMacosUserPatchedExtensionBuildJailPrintsNoAgentSessionLine: a portable build line (one that
// runs under macOS's own /bin/bash 3.2 and BSD tools, PPX-D46) builds and is delivered, and the build
// jail prints no agent-session line.
func TestMacosUserPatchedExtensionBuildJailPrintsNoAgentSessionLine(t *testing.T) {
	requireMacosUser(t)
	build := `x=AB && printf '%s_%s\n' "$(printf '%s' "$x" | tr 'A-Z' 'a-z')" "$(ls f.txt)" > built.txt`
	f := newMacExtFixture(t, "ten", build, []string{"built.txt"})

	r := f.launch(t)
	if r.rc != 0 || !strings.Contains(r.stdout, "PROBE_DONE") {
		t.Fatalf("the macos-user launch: rc %d, want the session to run the probe\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	if got, _ := probeLine(r.stdout, "BUILT"); got != "ab_f.txt" {
		t.Errorf("the build line's output is %q, want %q:\n%s",
			got, "ab_f.txt", r.stdout)
	}
	noAgentSessionLines(t, buildJailLines(t, f.ws, f.owner))
}

// TestMacosUserPatchedExtensionFailedBuildLineIsSaidAsOne: a build line that leaves a child running
// and fails refuses the launch, saying the build line ran and failed and naming its log, never that
// its build jail refused to start; the child ends with it, so no pack tree is kept; and the build
// jail prints no agent-session line.
func TestMacosUserPatchedExtensionFailedBuildLineIsSaidAsOne(t *testing.T) {
	requireMacosUser(t)
	f := newMacExtFixture(t, "ten", `sleep 120 & echo the-build-line-failed >&2; exit 7`, nil)

	r := f.launch(t)
	out := r.combined()
	if r.rc == 0 || strings.Contains(r.stdout, "PROBE_DONE") {
		t.Fatalf("a failed build line: rc %d, want a refusal before the session\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	for _, w := range []string{f.owner, "the build line ran and failed", "its log: "} {
		if !strings.Contains(out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, out)
		}
	}
	for _, not := range []string{"refused to start", "before its build line ran"} {
		if strings.Contains(out, not) {
			t.Errorf("a build line that ran is said as %q:\n%s", not, out)
		}
	}
	lines := buildJailLines(t, f.ws, f.owner)
	if !strings.Contains(strings.Join(lines, "\n"), "the-build-line-failed") {
		t.Errorf("launch.log does not hold the build line's own output:\n%s", strings.Join(lines, "\n"))
	}
	noAgentSessionLines(t, lines)
	if live := f.live(t); len(live) != 0 {
		t.Errorf("the failed build admitted store entries %v", live)
	}
}
