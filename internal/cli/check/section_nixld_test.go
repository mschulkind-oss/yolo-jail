package check

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// runNixLDSection wires a minimal Options for the nix-ld section alone: in-jail
// (YOLO_VERSION set) unless overridden, an injected MiseNode, and an Exec stub
// that returns the given result for the node --version probe.
func runNixLDSection(t *testing.T, inJail bool, node string, probe ExecResult) string {
	t.Helper()
	var out bytes.Buffer
	getenv := func(k string) string {
		if k == "YOLO_VERSION" && inJail {
			return "9.9.9-test"
		}
		return ""
	}
	o := &Options{
		Getenv:      getenv,
		MiseNode:    func() string { return node },
		Exec:        func([]string, string, []string, time.Duration) ExecResult { return probe },
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
	}
	fillDefaults(o)
	// fillDefaults installs real seams for nil fields; re-pin the two we control
	// so the real glob/env don't leak in.
	o.Getenv = getenv
	o.MiseNode = func() string { return node }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return probe }
	r := newReporter(&out, false)
	o.sectionNixLD(r)
	return out.String()
}

func TestNixLDSectionEnvFreePass(t *testing.T) {
	got := runNixLDSection(t, true, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"})
	if !strings.Contains(got, "FHS loader (nix-ld)") {
		t.Fatalf("expected section header, got:\n%s", got)
	}
	if !strings.Contains(got, "runs env-free: v22.20.0 (nix-ld OK)") {
		t.Errorf("expected env-free PASS line, got:\n%s", got)
	}
}

func TestNixLDSectionRegressionFails(t *testing.T) {
	got := runNixLDSection(t, true, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, RC: 1,
			Stderr: "node: error while loading shared libraries: libstdc++.so.6: cannot open shared object file: No such file or directory\n"})
	if !strings.Contains(got, "fails under a scrubbed environment") {
		t.Errorf("expected regression FAIL line, got:\n%s", got)
	}
	if !strings.Contains(got, "libstdc++.so.6") {
		t.Errorf("expected the loader error detail in the message, got:\n%s", got)
	}
	if !strings.Contains(got, "nix-ld") {
		t.Errorf("expected a nix-ld remedy note, got:\n%s", got)
	}
}

func TestNixLDSectionSkippedOnHost(t *testing.T) {
	got := runNixLDSection(t, false, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"})
	if strings.Contains(got, "FHS loader (nix-ld)") {
		t.Errorf("section must be skipped on the host, got:\n%s", got)
	}
}

func TestNixLDSectionSkippedWithoutMiseNode(t *testing.T) {
	got := runNixLDSection(t, true, "", ExecResult{Ran: false})
	if strings.Contains(got, "FHS loader (nix-ld)") {
		t.Errorf("section must be skipped when no mise node is installed, got:\n%s", got)
	}
}

func TestNixLDSectionTimeout(t *testing.T) {
	got := runNixLDSection(t, true, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, Timeout: true})
	if !strings.Contains(got, "timed out") {
		t.Errorf("expected a timeout FAIL line, got:\n%s", got)
	}
}
