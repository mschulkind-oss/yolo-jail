package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestShimArgvFilterContract executes the generated blocked-tool shims and
// asserts the frozen argv-filter contract (message/suggestion text + exit code
// 127), mirroring tests/test_entrypoint.py::TestShimGeneration. This is the
// behavioral half of parity: the tree golden pins the shim BYTES; this pins
// what those bytes DO when run.
func TestShimArgvFilterContract(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	t.Run("grep_blocks_only_recursive", func(t *testing.T) {
		home := t.TempDir()
		e := NewEnv(map[string]string{
			"JAIL_HOME": home,
			"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"grep -r is blocked",` +
				`"suggestion":"Use rg","block_flags":["--recursive","-r","-R","-*[rR]*"]}]`,
		})
		if err := GenerateShims(e); err != nil {
			t.Fatal(err)
		}
		shim := filepath.Join(e.ShimDir(), "grep")

		// Recursive forms -> blocked (127) with the message.
		for _, argv := range [][]string{
			{"-r", "foo", "."}, {"-R", "foo", "."}, {"--recursive", "foo"},
			{"-rn", "foo", "."}, {"-Rn", "foo", "."}, {"-inRw", "foo"},
		} {
			rc, _, stderr := runShim(t, shim, argv, "")
			if rc != 127 {
				t.Errorf("argv %v: rc=%d, want 127", argv, rc)
			}
			if !strings.Contains(stderr, "blocked") && !strings.Contains(stderr, "rg") {
				t.Errorf("argv %v: stderr missing message: %q", argv, stderr)
			}
		}
		// --regexp must NOT be blocked (long option that contains r/R).
		if rc, _, _ := runShim(t, shim, []string{"--regexp=foo", "/dev/null"}, ""); rc == 127 {
			t.Errorf("--regexp must not be blocked")
		}
		// Plain pipe-filter usage must NOT be blocked.
		if rc, _, _ := runShim(t, shim, []string{"foo"}, "bar\nfoo\nbaz\n"); rc == 127 {
			t.Errorf("plain grep must not be blocked")
		}
		// Short non-recursive flag must NOT be blocked.
		if rc, _, _ := runShim(t, shim, []string{"-n", "foo", "/dev/null"}, ""); rc == 127 {
			t.Errorf("-n must not be blocked")
		}
		// Suggestion text present on a blocked path.
		_, _, stderr := runShim(t, shim, []string{"-r", "x"}, "")
		if !strings.Contains(stderr, "Suggestion: Use rg") {
			t.Errorf("missing suggestion line: %q", stderr)
		}
	})

	t.Run("unconditional_block_no_fallthrough", func(t *testing.T) {
		home := t.TempDir()
		e := NewEnv(map[string]string{
			"JAIL_HOME":         home,
			"YOLO_BLOCK_CONFIG": `[{"name":"curl","message":"curl is blocked","suggestion":"Use wget"}]`,
		})
		if err := GenerateShims(e); err != nil {
			t.Fatal(err)
		}
		shim := filepath.Join(e.ShimDir(), "curl")
		body, _ := os.ReadFile(shim)
		if strings.Contains(string(body), "exec ") {
			t.Errorf("curl shim (no real_bin) must not exec-fallthrough:\n%s", body)
		}
		rc, _, stderr := runShim(t, shim, []string{"http://x"}, "")
		if rc != 127 {
			t.Errorf("curl rc=%d, want 127", rc)
		}
		if !strings.Contains(stderr, "curl is blocked") || !strings.Contains(stderr, "Suggestion: Use wget") {
			t.Errorf("curl stderr=%q", stderr)
		}
	})

	t.Run("bypass_env_lets_through", func(t *testing.T) {
		home := t.TempDir()
		e := NewEnv(map[string]string{
			"JAIL_HOME":         home,
			"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"grep blocked","suggestion":"Try rg"}]`,
		})
		if err := GenerateShims(e); err != nil {
			t.Fatal(err)
		}
		shim := filepath.Join(e.ShimDir(), "grep")
		body, _ := os.ReadFile(shim)
		// Unconditional grep block still fatthroughs to /bin/grep (real_bin set).
		if !strings.Contains(string(body), "exec /bin/grep") {
			t.Errorf("grep unconditional shim should exec /bin/grep:\n%s", body)
		}
	})
}

// runShim executes the shim under /bin/sh with argv and stdin, returning the
// exit code, stdout, and stderr. Runs with a clean-ish env (no YOLO_BYPASS_SHIMS).
func runShim(t *testing.T, shim string, argv []string, stdin string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(shim, argv...)
	cmd.Env = []string{"PATH=/bin:/usr/bin"}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			t.Fatalf("run shim %s %v: %v", shim, argv, err)
		}
	}
	return rc, stdout.String(), stderr.String()
}
