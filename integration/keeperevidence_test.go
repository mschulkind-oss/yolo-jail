package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// keeperEvidenceLines is how many trailing lines of each keeper log a failed launch reports.
const keeperEvidenceLines = 60

// keeperEvidence is the tail of every keeper log a launch wrote, for the failure output of a
// launch that failed. The logs live under the launch's HOME (".local/share/yolo-jail/logs"), which
// a test's temp-dir cleanup deletes, so a flake whose cause is in the keeper's log (a stopped jail
// that would not go, 2026-11) otherwise leaves nothing to read. Only logs written since the launch
// began are included, since a shared HOME holds other launches' logs.
func keeperEvidence(home string, since time.Time) string {
	logs, _ := filepath.Glob(filepath.Join(home, ".local", "share", "yolo-jail", "logs", "jail-keeper-*.log"))
	sort.Strings(logs)
	var b strings.Builder
	for _, path := range logs {
		info, err := os.Stat(path)
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		note := ""
		if len(lines) > keeperEvidenceLines {
			note = fmt.Sprintf(" (last %d of %d lines)", keeperEvidenceLines, len(lines))
			lines = lines[len(lines)-keeperEvidenceLines:]
		}
		fmt.Fprintf(&b, "keeper log %s%s:\n%s\n", filepath.Base(path), note, strings.Join(lines, "\n"))
	}
	return b.String()
}

// reportKeeperEvidence is runLaunch's one place a failed launch's keeper logs reach the test's
// output, so every run* helper inherits it and no test opts in.
func reportKeeperEvidence(t *testing.T, env []string, since time.Time) string {
	t.Helper()
	ev := keeperEvidence(launchHome(env), since)
	if ev != "" {
		t.Logf("the launch failed; the keeper's log, kept here because the test's temp dir is deleted:\n%s", ev)
	}
	return ev
}

// THE CALL SITE, BY BEHAVIOR: a launch that exits nonzero hands back the keeper logs it wrote, a
// launch that succeeds does not, and one written before the launch is left out. Delete the
// reportKeeperEvidence call from runLaunch and the first subtest fails. No container.
func TestAFailedLaunchCarriesTheKeeperLogsItWrote(t *testing.T) {
	dir := resolvedTempDir(t)
	home := resolvedTempDir(t)
	logs := filepath.Join(home, ".local", "share", "yolo-jail", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(logs, "jail-keeper-yolo-old.log")
	if err := os.WriteFile(old, []byte("from an earlier launch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "fake-yolo")
	script := "#!/bin/sh\n" +
		"mkdir -p \"$HOME/.local/share/yolo-jail/logs\"\n" +
		"echo 'keeper: yolo-x is still there after its stop' > \"$HOME/.local/share/yolo-jail/logs/jail-keeper-yolo-x.log\"\n" +
		"exit \"$FAKE_RC\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := yoloBin
	yoloBin = fake
	t.Cleanup(func() { yoloBin = saved })

	t.Run("failed", func(t *testing.T) {
		res, _ := runLaunch(t, dir, []string{"run", "--", "true"}, withEnv("HOME="+home, "FAKE_RC=3"))
		if res.rc != 3 {
			t.Fatalf("the fake launcher exited %d, want 3", res.rc)
		}
		if !strings.Contains(res.keeperLogs, "still there after its stop") {
			t.Errorf("the failed launch carries no keeper log: %q", res.keeperLogs)
		}
		if strings.Contains(res.keeperLogs, "earlier launch") {
			t.Errorf("a log from before the launch was reported: %q", res.keeperLogs)
		}
	})
	t.Run("succeeded", func(t *testing.T) {
		res, _ := runLaunch(t, dir, []string{"run", "--", "true"}, withEnv("HOME="+home, "FAKE_RC=0"))
		if res.keeperLogs != "" {
			t.Errorf("a launch that succeeded reported keeper logs: %q", res.keeperLogs)
		}
	})
}
