package integration

import (
	"os"
	"strings"
	"testing"
)

// THE WARMUP'S ONE SKIP RULE, pinned from Linux under -short (warmupSkipReason in
// harness_test.go says what it is for). Two tests, because a rule and its call site fail
// separately: the first pins the decision in both directions, the second pins that warmJail
// still asks it — the shape AGENTS.md names ("does it fail if I delete the call site?").

// TestWarmupSkipReason: only a job that DECLARED itself an image-delivery job loses the
// warmup, whatever it declared; every other run — every GOOS included — keeps it.
func TestWarmupSkipReason(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		wantSkip bool
	}{
		{"an ordinary run, no declaration", nil, false},
		{"the podman archive-delivery job", map[string]string{macArchiveDeclareEnv: "podman"}, true},
		{"the Apple Container archive-delivery step", map[string]string{macArchiveDeclareEnv: "container"}, true},
		// The declaration's own gate rejects a bad value later (requireMacArchiveDelivery);
		// the warmup must not second-guess it into running a delivery anyway.
		{"a malformed declaration still names a delivery job", map[string]string{macArchiveDeclareEnv: "docker"}, true},
		{"whitespace is not a declaration", map[string]string{macArchiveDeclareEnv: "  "}, false},
		// The other suites' declarations are not delivery jobs: their subject is a LAUNCH,
		// and a warmup is exactly what keeps the first launch's one-time cost out of the
		// first test's budget.
		{"the Apple Container parity job", map[string]string{appleContainerDeclareEnv: "1"}, false},
		{"the macos-user job", map[string]string{macosUserDeclareEnv: "1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			why := warmupSkipReason(getenv)
			if (why != "") != tc.wantSkip {
				t.Fatalf("warmupSkipReason = %q, want skip=%v", why, tc.wantSkip)
			}
			if why != "" && !strings.Contains(why, macArchiveDeclareEnv) {
				t.Errorf("the skip reason %q does not name %s, so a CI log reader cannot tell "+
					"which declaration turned the warmup off", why, macArchiveDeclareEnv)
			}
		})
	}
}

// TestWarmJailConsultsItsSkipRule is the CALL-SITE half, read from the source for the reason
// TestRequireAppleContainerInstallsTheLateSkipGuard gives: warmJail runs from TestMain against
// a real runtime, so no -short test can drive it.
//
// It also pins that the darwin skip is GONE rather than merely bypassed. The skip was removed
// on 2026-09-25 so the macOS nightly would re-measure a darwin warmup
// (docs/reference/agent-install-in-ci.md#suite-warmup); a GOOS test quietly re-added here would
// stop that measurement with every Linux gate green. If the measurement says the skip should
// come back, delete that half of this test in the same commit and cite the run.
func TestWarmJailConsultsItsSkipRule(t *testing.T) {
	src, err := os.ReadFile("harness_test.go")
	if err != nil {
		t.Fatalf("reading harness_test.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "\nfunc warmJail() {")
	if i < 0 {
		t.Fatal("warmJail is gone from harness_test.go; this test has lost its subject")
	}
	j := strings.Index(body[i+1:], "\n}\n")
	if j < 0 {
		t.Fatal("could not find the end of warmJail")
	}
	fn := body[i : i+1+j]
	if !strings.Contains(fn, "warmupSkipReason(os.Getenv)") {
		t.Error("warmJail no longer calls warmupSkipReason(os.Getenv). An image-delivery job " +
			"would then take a warmup, and a warmup IS a delivery: the archive-delivery tests " +
			"would measure a store the warmup had already half-filled.")
	}
	if strings.Contains(fn, `GOOS == "darwin"`) {
		t.Error("warmJail branches on darwin again. The darwin skip was removed so the macOS " +
			"nightly re-measures a darwin warmup; restoring it needs that measurement cited, " +
			"and this assertion deleted in the same commit.")
	}
}
