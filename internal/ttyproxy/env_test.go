//go:build linux

package ttyproxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestObserverEnvReachesTheChildOnBothPaths pins Observer.Env on the pty path, the one every
// interactive launch takes, and on the plain path a launch without a terminal takes: the child
// gets the variable on top of the environment it inherits, and this process does not. A launch
// in a herdr pane puts HERDR_AGENT on its session's runtime client this way (run's herdragent.go).
func TestObserverEnvReachesTheChildOnBothPaths(t *testing.T) {
	const probe = "YOLO_TTYPROXY_ENV_PROBE"
	t.Setenv(probe, "")
	t.Setenv("YOLO_TTYPROXY_INHERITED", "kept")
	cmd := func(out string) []string {
		return []string{"sh", "-c", `printf '%s/%s' "$` + probe + `" "$YOLO_TTYPROXY_INHERITED" > "$0"`, out}
	}
	check := func(t *testing.T, out string) {
		t.Helper()
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("the child wrote nothing: %v", err)
		}
		if string(got) != "claude/kept" {
			t.Errorf("the child saw %q, want the Observer's variable over the inherited environment (claude/kept)", got)
		}
		if v := os.Getenv(probe); v != "" {
			t.Errorf("this process's own environment gained %s=%q", probe, v)
		}
	}
	obs := Observer{Env: []string{probe + "=claude"}}

	t.Run("pty", func(t *testing.T) {
		master, _ := fakeHostTTY(t)
		out := filepath.Join(t.TempDir(), "env")
		run := startProxied(t, master, cmd(out), obs)
		select {
		case rc := <-run.done:
			if rc != 0 {
				t.Fatalf("rc = %d", rc)
			}
		case <-time.After(childDeadline):
			t.Fatal("the proxied child never exited")
		}
		check(t, out)
	})

	t.Run("plain", func(t *testing.T) {
		devnull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		origIn := os.Stdin
		os.Stdin = devnull
		t.Cleanup(func() { os.Stdin = origIn; _ = devnull.Close() })
		out := filepath.Join(t.TempDir(), "env")
		if rc, err := RunWithProxyObserved(cmd(out), nil, nil, obs); err != nil || rc != 0 {
			t.Fatalf("RunWithProxyObserved = %d, %v", rc, err)
		}
		check(t, out)
	})
}
