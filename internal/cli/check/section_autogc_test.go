package check

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// runAutoGCSection wires a minimal Options for sectionAutoGC alone: nix present
// (unless hasNix=false), and an Exec stub returning the given `nix config show`
// result for the config probe.
func runAutoGCSection(t *testing.T, hasNix bool, probe ExecResult) string {
	t.Helper()
	var out bytes.Buffer
	o := &Options{
		LookPath:    func(string) (string, bool) { return "/bin/nix", hasNix },
		Exec:        func([]string, string, []string, time.Duration) ExecResult { return probe },
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
	}
	fillDefaults(o)
	o.LookPath = func(string) (string, bool) { return "/bin/nix", hasNix }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return probe }
	r := newReporter(&out, false)
	o.sectionAutoGC(r)
	return out.String()
}

func TestAutoGCSectionMinFreeZeroWarns(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 0,
		Stdout: "max-free = 9223372036854775807\nmin-free = 0\n"})
	if !strings.Contains(got, "Nix auto-GC") {
		t.Fatalf("expected section header, got:\n%s", got)
	}
	if !strings.Contains(got, "min-free = 0") || !strings.Contains(got, "automatic GC is OFF") {
		t.Errorf("expected the min-free=0 WARN, got:\n%s", got)
	}
	if !strings.Contains(got, "storage §1") {
		t.Errorf("expected the remedy to note §1 rooting makes this safe, got:\n%s", got)
	}
}

func TestAutoGCSectionMinFreeSetPasses(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 0,
		Stdout: "min-free = 53687091200\nmax-free = 214748364800\n"})
	if !strings.Contains(got, "min-free is set") || !strings.Contains(got, "50.0 GiB") {
		t.Errorf("expected the configured-floor PASS with a human size, got:\n%s", got)
	}
}

func TestAutoGCSectionSkippedNoNix(t *testing.T) {
	got := runAutoGCSection(t, false, ExecResult{Ran: false})
	if strings.Contains(got, "Nix auto-GC") {
		t.Errorf("section must be skipped when nix is absent, got:\n%s", got)
	}
}

func TestAutoGCSectionSkippedOnUnreadableConfig(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 1})
	if strings.Contains(got, "Nix auto-GC") {
		t.Errorf("section must stay silent when the config can't be read, got:\n%s", got)
	}
}

func TestAutoGCSectionSkippedWhenKeyAbsent(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 0, Stdout: "max-jobs = auto\n"})
	if strings.Contains(got, "Nix auto-GC") {
		t.Errorf("section must stay silent when min-free is absent, got:\n%s", got)
	}
}
