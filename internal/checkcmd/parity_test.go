package checkcmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDifferentialParityInJail runs the native Go check and the live Python
// `python -m src.cli check --no-build` under identical in-jail conditions and
// asserts their ANSI-stripped stdout conveys the SAME sections/badges/counts.
// Skips when python3/uv is unavailable (Stage-15 exit criteria). Per the output
// contract we compare the graded lines (section headers + [PASS]/[WARN]/[FAIL]
// + Summary), not the incidental rich soft-wrapping or the exact remedy prose
// (those come from the shared engines and diverge only in whitespace).
func TestDifferentialParityInJail(t *testing.T) {
	root := repoRootDir(t)
	py := pythonRunner(t, root)
	if py == nil {
		t.Skip("python oracle unavailable (uv/python3 not found)")
	}

	// Share ONE HOME across both runs: Go's path helpers read $HOME from the
	// process env directly (not the Options.Getenv seam), so t.Setenv is the
	// only way to align them. COLUMNS is set wide so Python's rich console does
	// not soft-wrap long absolute paths (a terminal-width artifact, not content).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COLUMNS", "1000")

	env := append(os.Environ(),
		"YOLO_VERSION=0.0.0-parity",
		"HOME="+home,
		"COLUMNS=1000",
	)

	// Python side.
	pyCmd := py("-m", "src.cli", "check", "--no-build")
	pyCmd.Dir = root
	pyCmd.Env = env
	var pyOut bytes.Buffer
	pyCmd.Stdout = &pyOut
	pyCmd.Stderr = nil
	if err := pyCmd.Run(); err != nil {
		// exit 1 is a valid graded outcome (ExitError); a spawn failure after
		// python is confirmed present is real drift → FAIL, not skip (audit
		// 2026-07-18 §B5: live oracles fail closed).
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("python check failed to run: %v", err)
		}
	}

	// Go side — run the SAME workspace (root) with a real Options.
	var goOut bytes.Buffer
	o := NewDefaultOptions()
	o.Build = false
	o.Stdout = &goOut
	o.Workspace = root
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.0.0-parity"
		}
		return os.Getenv(k)
	}
	// Use the real seams; but pin the version so it matches Python's YOLO_VERSION
	// verbatim.
	o.Version = "0.0.0-parity"
	Check(o)

	pyGraded := gradedLines(stripANSI(pyOut.String()))
	goGraded := gradedLines(stripANSI(goOut.String()))

	if !equalLines(pyGraded, goGraded) {
		t.Errorf("graded-line mismatch\n--- python ---\n%s\n--- go ---\n%s",
			strings.Join(pyGraded, "\n"), strings.Join(goGraded, "\n"))
	}
}

// gradedLines keeps only the lines that carry the graded contract: section
// headers (non-indented, non-blank, no badge), badge lines, and the Summary
// count line. Note/detail lines and dim info are dropped (their prose comes
// from shared engines; whitespace/soft-wrap is not part of the contract).
var (
	badgeLineRe    = regexp.MustCompile(`^\s+\[(PASS|WARN|FAIL)\] `)
	summaryCountRe = regexp.MustCompile(`^\s+\d+ (passed|failed|warnings)`)
)

func gradedLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		switch {
		case badgeLineRe.MatchString(line):
			// Keep the badge + message but drop a trailing volatile path/version.
			out = append(out, normalizeGraded(line))
		case summaryCountRe.MatchString(line):
			out = append(out, strings.TrimSpace(line))
		case line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "Warning:"):
			// Section header (bold in the styled form). Exclude the "Version:"
			// line and stray warnings.
			if !strings.HasPrefix(line, "Version:") {
				out = append(out, line)
			}
		}
	}
	return out
}

// normalizeGraded strips host-specific tails (absolute paths, versions) from a
// badge line so the parity compare focuses on the badge + stable message stem.
func normalizeGraded(line string) string {
	// Cut everything after the first ": " that introduces a path/version value
	// for lines known to carry host-specific tails.
	for _, stem := range []string{
		"[PASS] nix:", "[PASS] podman:", "[PASS] container:",
		"[PASS] Home:", "[PASS] Mise (jail store):", "[PASS] Containers:",
		"[PASS] Agents:", "[PASS] Build:", "[PASS] flake.nix found:",
		"[PASS] Parsed user config:", "[PASS] No user config found:",
		"[PASS] Parsed workspace config:", "[PASS] nix build succeeded:",
		"[PASS] Runtime available:", "[PASS] Image loaded:",
	} {
		if idx := strings.Index(line, stem); idx >= 0 {
			return line[:idx+len(stem)]
		}
	}
	return strings.TrimRight(line, " ")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pythonRunner(t *testing.T, root string) func(args ...string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
