package run

import (
	"strings"
	"testing"
	"time"
)

// acrobinds_test.go guards the version floor on Apple Container's `:ro` support.
//
// THE BELIEF THIS REPLACED WAS FALSE, AND IT WAS FALSE FOR MONTHS WITHOUT ANYTHING
// SAYING SO. Four places in the tree stated that Apple Container "accepts
// `-v src:dest:ro` and IGNORES the suffix" (apple/container#889, last observed on
// 0.12.3). The one test that could have checked —
// TestAppleContainerIgnoresReadOnlyBinds — had never executed: its `imageExists`
// helper spoke podman's argv, so it skipped every run it ever had. The first run in
// which it actually ran reported `MEASURED: Apple Container HONORED a :ro bind.`
// (2026-09-14, macOS 26.5 arm64, `container` 1.1.0).
//
// WHY A FLOOR AND NOT A FLIP. The two error directions are not symmetric. Wrongly
// believing `:ro` is honored hands an agent WRITE access to a host directory the
// user granted read-only, silently — the mount succeeds either way. Wrongly
// believing it is ignored costs a feature and prints why. So everything uncertain
// stays refused, and that is what the last two rows below are for.

// acOptions builds an Options whose `container --version` answers with `out`, or
// which has no `container` on PATH at all when out is "".
func acOptions(out string) (*Options, *int) {
	calls := 0
	o := &Options{
		LookPath: func(name string) (string, bool) {
			if name == "container" && out != "" {
				return "/usr/local/bin/container", true
			}
			return "", false
		},
		Exec: func([]string, string, []string, time.Duration) ExecResult {
			calls++
			return ExecResult{Ran: true, RC: 0, Stdout: out}
		},
	}
	return o, &calls
}

// TestROBindsFloorIsFailClosed is the table. `refuse` is what the launch does, and the
// rows below the floor are the ones that matter most.
func TestROBindsFloorIsFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		rt      string
		version string // `container --version` output; "" = no container on PATH
		refuse  bool
	}{
		{"podman honors :ro and always did", "podman", "", false},
		{"macos-user has no binds at all, so the question does not arise", "macos-user", "", false},
		{"AC at the measured floor honors it",
			"container", "container CLI version 1.1.0 (build: release, commit: unspeci)", false},
		{"AC above the floor honors it",
			"container", "container CLI version 1.2.0 (build: release)", false},
		{"AC far above the floor honors it",
			"container", "container CLI version 2.0 (build: release)", false},

		// ⚠ EVERY UNCERTAIN ANSWER REFUSES. Each of these is a way to end up with a
		// writable mount the user asked to be read-only, which is the failure this
		// floor exists to prevent.
		{"AC 0.12.3 is the version #889 was observed on",
			"container", "container CLI version 0.12.3 (build: release)", true},
		{"AC just below the floor still refuses",
			"container", "container CLI version 1.0.9 (build: release)", true},
		{"a version line with no version in it refuses",
			"container", "container CLI version unknown", true},
		{"no container on PATH refuses", "container", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := acOptions(tc.version)
			got := o.roBindsUnsupported(tc.rt)
			if tc.refuse != (got != "") {
				t.Fatalf("refuse=%v, want %v (reason: %q)", got != "", tc.refuse, got)
			}
			if !tc.refuse {
				return
			}
			// A refusal the user cannot act on is half a refusal; podman is the
			// backend that has always honored this.
			if !strings.Contains(got, "podman") {
				t.Errorf("the refusal does not name the way forward:\n%s", got)
			}
		})
	}
}

// TestROBindsProbeIsMemoized pins the property four call sites depend on.
//
// The argv asks twice, the host-mount grants once and the BRIEFING once. If they could
// get different answers, a jail would be TOLD something the launch did not do — which is
// backend-parity.md §6's defect, and the reason roBindsUnsupported has one implementation
// at all. A failed probe is cached too: it must not be retried per call, and must not read
// as a different answer the second time.
func TestROBindsProbeIsMemoized(t *testing.T) {
	o, calls := acOptions("container CLI version 1.1.0 (build: release)")
	for i := 0; i < 4; i++ {
		if o.roBindsUnsupported("container") != "" {
			t.Fatalf("call %d refused a version at the floor", i)
		}
	}
	if *calls != 1 {
		t.Errorf("`container --version` ran %d times, want 1 — four consumers must share "+
			"one answer, and a probe per call site is how they drift", *calls)
	}

	// The failed-probe half, which is the one a naive memo forgets.
	o2, calls2 := acOptions("")
	for i := 0; i < 3; i++ {
		if o2.roBindsUnsupported("container") == "" {
			t.Fatalf("call %d allowed :ro with no container on PATH", i)
		}
	}
	if *calls2 != 0 {
		t.Errorf("Exec ran %d times with no container on PATH, want 0", *calls2)
	}
}

// TestVersionAtLeastIsNumericAndFailsClosed covers the comparator directly, including the
// shapes that must NOT read as "new enough".
func TestVersionAtLeastIsNumericAndFailsClosed(t *testing.T) {
	yes := [][2]string{
		{"1.1.0", "1.1.0"}, {"1.1.1", "1.1.0"}, {"1.2", "1.1.0"},
		{"2.0.0", "1.1.0"}, {"1.10.0", "1.9.0"}, // 10 > 9 numerically, not lexically
	}
	for _, c := range yes {
		if !versionAtLeast(c[0], c[1]) {
			t.Errorf("versionAtLeast(%q, %q) = false, want true", c[0], c[1])
		}
	}
	no := [][2]string{
		{"1.0.9", "1.1.0"}, {"0.12.3", "1.1.0"}, {"1.1", "1.1.1"},
		{"", "1.1.0"}, {"abc", "1.1.0"}, {"1.x.0", "1.1.0"},
	}
	for _, c := range no {
		if versionAtLeast(c[0], c[1]) {
			t.Errorf("versionAtLeast(%q, %q) = true, want false", c[0], c[1])
		}
	}
}
