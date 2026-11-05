package run

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func writeCurrentKeeperRecord(t *testing.T, cname string, rec keeperRecord) {
	t.Helper()
	(&Options{Getpid: os.Getpid}).writeOwnerPID(cname)
	rec.PID, rec.Birth = os.Getpid(), keeperProcessBirth(os.Getpid())
	if rec.Birth == "" {
		t.Fatal("cannot observe the fixture keeper's kernel birth")
	}
	if err := writeKeeperRecord(cname, rec); err != nil {
		t.Fatal(err)
	}
}

func recordRunningMCPTestTree(t *testing.T, cname, kind string) {
	t.Helper()
	tree := filepath.Join(paths.PackTreeRoot(cname), "test-generation")
	if err := os.MkdirAll(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	var packs []*packload.Pack
	if kind != "non-mcp" {
		src := officialPack(t, "mcp-auth")
		name := "mcp-auth"
		if kind == "third-party-producer" {
			name = "custom-mcp"
		}
		dir := filepath.Join(tree, name)
		if err := os.CopyFS(dir, os.DirFS(src.Root)); err != nil {
			t.Fatal(err)
		}
		if kind == "third-party-producer" {
			oldModule := filepath.Join(dir, "loopholes", "mcp-auth-broker")
			newModule := filepath.Join(dir, "loopholes", "custom-mcp-producer")
			if err := os.Rename(oldModule, newModule); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{filepath.Join(dir, "pack.toml"), filepath.Join(newModule, "loophole.toml")} {
				body, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				body = []byte(strings.ReplaceAll(string(body), "mcp-auth-broker", "custom-mcp-producer"))
				if err := os.WriteFile(file, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		p, problems := packload.LoadDirForUse(dir, name)
		if len(problems) != 0 {
			t.Fatal(problems)
		}
		packs = append(packs, p)
	}
	if err := packload.WritePackTreeRecord(tree, packs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LivePackTreeRecord(cname), []byte("test-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAttachRefusesUnknownSelectedKeeperBeforeAgentExec(t *testing.T) {
	for _, tc := range []struct {
		name, tree string
		enter      bool
	}{
		{"missing-running-tree", "missing", false},
		{"selected-running-tree", "selected", false},
		{"third-party-producer-tree", "third-party-producer", false},
		{"verified-non-mcp-running-tree", "non-mcp", true},
		{"incomplete-running-record", "incomplete", false},
		{"selected-record-without-id", "required-no-id", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, nil, nil)
			const cname = "yolo-ws-abcd1234"
			if err := os.Remove(keeperRecordPath(cname)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if tc.tree == "selected" || tc.tree == "non-mcp" || tc.tree == "third-party-producer" {
				recordRunningMCPTestTree(t, cname, tc.tree)
			}
			if tc.tree == "incomplete" || tc.tree == "required-no-id" {
				if err := writeKeeperRecord(cname, keeperRecord{PID: o.Getpid(), Started: time.Now(), Workspace: o.Workspace, Runtime: "podman", MCPTeardownKnown: tc.tree == "required-no-id", MCPTeardownRequired: tc.tree == "required-no-id"}); err != nil {
					t.Fatal(err)
				}
			}
			live, err := holdLivenessLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseLock(live)
			other, _, err := takeSessionLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer other.release()
			rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
			if restarted || execed != tc.enter || (rc == 0) != tc.enter {
				t.Fatalf("rc=%d restarted=%v execed=%v, enter=%v: %s", rc, restarted, execed, tc.enter, stderr.String())
			}
			if !tc.enter && (!strings.Contains(stderr.String(), "selected MCP teardown requirement") || !strings.Contains(stderr.String(), "yolo stop")) {
				t.Fatalf("missing recovery advice: %s", stderr.String())
			}
		})
	}
}

func TestAttachCapturesCurrentSelectedKeeperIDBeforeEndingSession(t *testing.T) {
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, nil, nil)
	o.Getpid = os.Getpid
	const cname = "yolo-ws-abcd1234"
	oldID, currentID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	if err := createMCPOutcomeGeneration(cname, currentID, o.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPTeardownOutcome(cname, oldID, "ok"); err != nil {
		t.Fatal(err)
	}
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	o.writeOwnerPID(cname)
	if err := writeKeeperRecord(cname, keeperRecord{PID: o.Getpid(), Birth: keeperProcessBirth(o.Getpid()), Started: time.Now(), Workspace: o.Workspace, Runtime: "podman", MCPTeardownKnown: true, MCPTeardownRequired: true, MCPTeardownID: currentID}); err != nil {
		t.Fatal(err)
	}
	other, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
	if rc != 0 || restarted || !execed || o.mcpTeardownID != currentID {
		other.release()
		t.Fatalf("attach rc=%d restarted=%v execed=%v id=%q: %s", rc, restarted, execed, o.mcpTeardownID, stderr.String())
	}
	other.release()
	ending, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	// The same attach's captured ID must govern its ending quit even when
	// the keeper's final harvest fails and its log is unavailable.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		f, err := openSessionLock(cname)
		if err == nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
			_ = writeMCPTeardownOutcomeFor(cname, currentID, "failed", o.Workspace, "podman")
			_ = f.Close()
		}
		releaseLock(live)
	}()
	o.sessionLock = ending
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: "abc123\n"}
	}
	if rc := o.endSession(cname, "podman", 0, time.Now(), 0, false); rc == 0 {
		t.Fatal("attached ending session accepted failed harvest with unreadable log")
	}
	<-finished
}

func TestMCPTeardownOutcomeNeverOverwritesAnExistingFinalName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-existing-outcome"
	id := strings.Repeat("d", 32)
	if err := writeMCPTeardownOutcome(cname, id, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPTeardownOutcome(cname, id, "ok"); err == nil {
		t.Fatal("a second keeper overwrote the first result")
	}
	if got := readMCPTeardownOutcome(cname, id); got != "failed" {
		t.Fatalf("original result changed to %q", got)
	}
}

func TestMCPTeardownLinkFailureCannotPublishSelectedSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-link-failure"
	id := strings.Repeat("3", 32)
	original := linkMCPTeardownFinal
	linkMCPTeardownFinal = func(*os.Root, string, string) error { return errors.New("injected link failure") }
	t.Cleanup(func() { linkMCPTeardownFinal = original })
	if err := writeMCPTeardownOutcome(cname, id, "ok"); err == nil {
		t.Fatal("failed link was reported successful")
	}
	if got := readMCPTeardownOutcome(cname, id); got != "" {
		t.Fatalf("link failure published %q", got)
	}
}

func TestMCPTeardownTempUnlinkFailureCannotPublishSelectedSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-unlink-failure"
	id := strings.Repeat("4", 32)
	original := removeMCPTeardownTemp
	removeMCPTeardownTemp = func(*os.Root, string) error { return errors.New("injected temp unlink failure") }
	t.Cleanup(func() { removeMCPTeardownTemp = original })
	if err := writeMCPTeardownOutcome(cname, id, "ok"); err == nil {
		t.Fatal("failed temp unlink was reported successful")
	}
	if got := readMCPTeardownOutcome(cname, id); got != "" {
		t.Fatalf("incomplete publication confirmed %q", got)
	}
}

func TestMCPTeardownCloseFailureNeverPublishesReadableSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-close-failure"
	id := strings.Repeat("c", 32)
	original := closeMCPTeardownTemp
	closeMCPTeardownTemp = func(f *os.File) error {
		_ = f.Close()
		return errors.New("injected close failure")
	}
	t.Cleanup(func() { closeMCPTeardownTemp = original })
	if err := writeMCPTeardownOutcome(cname, id, "ok"); err == nil {
		t.Fatal("close failure was reported successful")
	}
	if readMCPTeardownOutcome(cname, id) != "" {
		t.Fatal("a failed publication is readable as success")
	}
	if _, err := os.Stat(mcpTeardownOutcomePath(cname, id)); !os.IsNotExist(err) {
		t.Fatalf("a final name became visible after failed close: %v", err)
	}
	entries, err := os.ReadDir(ownerPIDDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), id) {
			t.Fatalf("owned temporary result remains: %s", entry.Name())
		}
	}
}

// The old record can survive a failed write by the next keeper. A selected
// running tree must not be admitted under that earlier launch's known-off fact.
func TestAttachRejectsStaleKnownOffRecordBeforeExecAndRecordsRefusal(t *testing.T) {
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, nil, nil)
	const cname = "yolo-ws-abcd1234"
	recordRunningMCPTestTree(t, cname, "selected")
	if err := writeKeeperRecord(cname, keeperRecord{PID: o.Getpid() + 1, Started: time.Now().Add(-time.Hour), Workspace: o.Workspace, Runtime: "podman", MCPTeardownKnown: true}); err != nil {
		t.Fatal(err)
	}
	o.writeOwnerPID(cname)
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	o.armLaunchRecord()
	rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
	o.recordLaunchExit(rc)
	if rc == 0 || restarted || execed || !strings.Contains(stderr.String(), "selected MCP teardown") {
		t.Fatalf("stale known-off attach rc=%d restarted=%v execed=%v: %s", rc, restarted, execed, stderr.String())
	}
	line := launchLines(t)
	if len(line) != 1 || !strings.Contains(line[0], "outcome=not-started") || !strings.Contains(line[0], "rc=1") {
		t.Fatalf("refused attach was recorded as attached: %q", line)
	}
}

func TestAttachDistinguishesReusedPIDFromCurrentKnownOffRecord(t *testing.T) {
	for _, tc := range []struct {
		name, birth, tree string
		enter             bool
	}{
		{"same-pid-different-birth", "prior-process-birth", "", false},
		{"same-pid-missing-birth", "", "", false},
		{"stale-off-negative-running-tree", "prior-process-birth", "non-mcp", true},
		{"current-known-off", keeperProcessBirth(os.Getpid()), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, nil, nil)
			o.Getpid = os.Getpid
			const cname = "yolo-ws-abcd1234"
			o.writeOwnerPID(cname)
			if tc.tree != "" {
				recordRunningMCPTestTree(t, cname, tc.tree)
			}
			if err := writeKeeperRecord(cname, keeperRecord{PID: o.Getpid(), Birth: tc.birth, Started: time.Now(), Workspace: o.Workspace, Runtime: "podman", MCPTeardownKnown: true}); err != nil {
				t.Fatal(err)
			}
			live, err := holdLivenessLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseLock(live)
			rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
			if restarted || execed != tc.enter || (rc == 0) != tc.enter {
				t.Fatalf("rc=%d restarted=%v execed=%v: %s", rc, restarted, execed, stderr.String())
			}
		})
	}
}

// Even a current, birth-bound selected record is not enough to enter when its
// result generation cannot be registered; no attach or agent exec was started.
func TestCurrentSelectedKeeperMissingOutcomeGenerationRefusesBeforeAttach(t *testing.T) {
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, nil, nil)
	const cname = "yolo-ws-abcd1234"
	recordRunningMCPTestTree(t, cname, "selected")
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	writeCurrentKeeperRecord(t, cname, keeperRecord{Workspace: o.Workspace, Runtime: "podman", MCPTeardownKnown: true,
		MCPTeardownRequired: true, MCPTeardownID: strings.Repeat("f", 32)})
	o.armLaunchRecord()
	rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
	o.recordLaunchExit(rc)
	if rc != 1 || restarted || execed || o.mcpTeardownID != "" || o.mcpOutcomeReader != nil ||
		!strings.Contains(stderr.String(), "cannot register a selected MCP teardown reader") ||
		strings.Contains(stderr.String(), "Attaching to existing jail") {
		t.Fatalf("selected generation refusal failed: rc=%d restarted=%v execed=%v: %s", rc, restarted, execed, stderr.String())
	}
	lines := launchLines(t)
	if len(lines) != 1 || !strings.Contains(lines[0], "outcome=not-started rc=1") {
		t.Fatalf("refusal journal: %q", lines)
	}
}

// The admission refusal itself must precede the irreversible machine-wide
// attached line and the attach announcement, not merely prevent the exec.
func TestMissingSelectedKeeperRecordHasNotStartedLaunchOutcome(t *testing.T) {
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, nil, nil)
	const cname = "yolo-ws-abcd1234"
	recordRunningMCPTestTree(t, cname, "selected")
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	o.armLaunchRecord()
	rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
	o.recordLaunchExit(rc) // Run's deferred fallback for a refusal, at the caller's return.
	if rc != 1 || restarted || execed || strings.Contains(stderr.String(), "Attaching to existing jail") {
		t.Fatalf("refusal reached attach announcement/exec: rc=%d restarted=%v execed=%v: %s", rc, restarted, execed, stderr.String())
	}
	line := launchLines(t)
	if len(line) != 1 || !strings.Contains(line[0], "outcome=not-started rc=1") {
		t.Fatalf("refusal outcome is not not-started: %q", line)
	}
}
