package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func retirementOptions(t *testing.T) *Options {
	t.Helper()
	o := goldenOptions("/ws", t.TempDir())
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true} // successful empty runtime listing: no container
	}
	return o
}

func writeRetirementOutcome(t *testing.T, cname, id, result string, o *Options) {
	t.Helper()
	if err := createMCPOutcomeGeneration(cname, id, o.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPTeardownOutcomeFor(cname, id, result, o.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
}

func TestEndSessionRetiresSelectedOutcomeOnlyAfterBothReadersFinish(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-two-ending-readers"
	id := strings.Repeat("a", 32)
	first, second := retirementOptions(t), retirementOptions(t)
	if err := createMCPOutcomeGeneration(cname, id, first.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	for _, o := range []*Options{first, second} {
		o.mcpTeardownID = id
		var err error
		o.sessionLock, _, err = takeSessionLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.registerMCPOutcomeReader(cname, id, "podman"); err != nil {
			t.Fatal(err)
		}
		defer o.closeMCPOutcomeReader()
		defer o.releaseSessionLock()
	}
	if err := writeMCPTeardownOutcomeFor(cname, id, "ok", first.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	// The second session already released its session count but has not read
	// the result: ONLY its reader lease now prevents the first from reaping.
	second.releaseSessionLock()
	if rc := first.endSession(cname, "podman", 0, time.Now(), 0, false); rc != 0 {
		t.Fatalf("first reader rc=%d", rc)
	}
	if got := readMCPTeardownOutcome(cname, id); got != "ok" {
		t.Fatalf("first reader removed the second reader's result: %q", got)
	}
	if rc := second.endSession(cname, "podman", 0, time.Now(), 0, false); rc != 0 {
		t.Fatalf("second reader rc=%d", rc)
	}
	for _, path := range []string{mcpTeardownOutcomePath(cname, id), filepath.Join(ownerPIDDir(), mcpOutcomeClaimName(cname, id)), filepath.Join(ownerPIDDir(), mcpOutcomeLeaseName(cname, id))} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("last reader left %s: %v", filepath.Base(path), err)
		}
	}
}

func TestRetireSelectedOutcomeRequiresActualAbsentJailAndExactClaim(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-retirement-guard"
	id := strings.Repeat("b", 32)
	o := retirementOptions(t)
	writeRetirementOutcome(t, cname, id, "failed", o)
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("removed result while keeper live")
	}
	releaseLock(live)
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{} }
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("removed result when runtime could not answer")
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: "container-id\n"}
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("removed result while current container exists")
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	claimPath := filepath.Join(ownerPIDDir(), mcpOutcomeClaimName(cname, id))
	original, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claimPath, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("removed result on malformed claim")
	}
	if err := os.WriteFile(claimPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if !o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("safe stopped generation did not retire")
	}
}

func TestCurrentWorkspaceRetryRetiresUnpublishedGenerationAfterReaderExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-unpublished"
	id := strings.Repeat("1", 32)
	o := retirementOptions(t)
	if err := createMCPOutcomeGeneration(cname, id, o.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	if err := o.registerMCPOutcomeReader(cname, id, "podman"); err != nil {
		t.Fatal(err)
	}
	o.retireWorkspaceMCPOutcomes(cname)
	if _, err := os.Lstat(filepath.Join(mcpOutcomeGenerationDir(cname), mcpOutcomeGenerationName(cname, id))); err != nil {
		t.Fatalf("published reader was not protected: %v", err)
	}
	o.closeMCPOutcomeReader()
	o.retireWorkspaceMCPOutcomes(cname)
	for _, path := range []string{filepath.Join(mcpOutcomeGenerationDir(cname), mcpOutcomeGenerationName(cname, id)), filepath.Join(ownerPIDDir(), mcpOutcomeLeaseName(cname, id))} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unpublished generation remains: %s %v", path, err)
		}
	}
}

func TestFailedTempUnlinkAndRollbackRemainUnreadableUntilGuardedRetirement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-rollback-failed"
	id := strings.Repeat("5", 32)
	o := retirementOptions(t)
	if err := createMCPOutcomeGeneration(cname, id, o.Workspace, "podman"); err != nil {
		t.Fatal(err)
	}
	removeTemp, removeFinal := removeMCPTeardownTemp, removeMCPTeardownFinal
	removeMCPTeardownTemp = func(*os.Root, string) error { return fmt.Errorf("injected temp unlink") }
	removeMCPTeardownFinal = func(*os.Root, string) error { return fmt.Errorf("injected rollback unlink") }
	if err := writeMCPTeardownOutcomeFor(cname, id, "ok", o.Workspace, "podman"); err == nil {
		t.Fatal("both failed unlinks reported success")
	}
	removeMCPTeardownTemp, removeMCPTeardownFinal = removeTemp, removeFinal
	t.Cleanup(func() { removeMCPTeardownTemp, removeMCPTeardownFinal = removeTemp, removeFinal })
	if got := readMCPTeardownOutcome(cname, id); got != "" {
		t.Fatalf("rollback failure certified an incomplete success: %q", got)
	}
	if !o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("could not retire own rejected two-link result after keeper was gone")
	}
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, id)); !os.IsNotExist(err) {
		t.Fatalf("rejected result remains after proven retirement: %v", err)
	}
}

func TestRetirementRefusesForeignResultInodeAndLinkedMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-foreign-result"
	id := strings.Repeat("2", 32)
	o := retirementOptions(t)
	writeRetirementOutcome(t, cname, id, "ok", o)
	path := mcpTeardownOutcomePath(cname, id)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"id":"`+id+`","result":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("deleted an unowned inode at the selected result path")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("foreign result lost: %v", err)
	}
	claim := filepath.Join(ownerPIDDir(), mcpOutcomeClaimName(cname, id))
	if err := os.Link(claim, claim+".other"); err != nil {
		t.Fatal(err)
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("accepted a hard-linked ownership claim")
	}
}

// A RESULT WHOSE INODE NUMBER MATCHES BUT WHOSE GENERATION DOES NOT IS NOT OURS. ext4 and XFS
// hand a freed inode number straight to the next new file, so the test above only meets that
// replacement on such a filesystem (CI's, not this jail's btrfs). This one forges the
// mismatch on any filesystem that reports a generation: delete the generation comparison
// and retirement removes a file the claim never named.
func TestRetirementRefusesAResultWhoseInodeGenerationDiffers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-reused-inode-number"
	id := strings.Repeat("3", 32)
	o := retirementOptions(t)
	writeRetirementOutcome(t, cname, id, "ok", o)
	claimPath := filepath.Join(ownerPIDDir(), mcpOutcomeClaimName(cname, id))
	raw, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	var claim mcpOutcomeClaim
	if err := json.Unmarshal(raw, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Generation == nil {
		t.Skip("this filesystem reports no inode generation; its inode numbers are not reused")
	}
	other := *claim.Generation + 1
	claim.Generation = &other
	forged, _ := json.Marshal(claim)
	if err := os.WriteFile(claimPath, forged, 0o600); err != nil {
		t.Fatal(err)
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("retired a result whose inode generation is not the claimed one")
	}
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, id)); err != nil {
		t.Fatalf("result at a reused inode number was deleted: %v", err)
	}
}

func TestCurrentWorkspaceRetryAfterReaderDiesUsesReleasedKernelLease(t *testing.T) {
	const cname = "yolo-mcp-dead-reader"
	id := strings.Repeat("e", 32)
	if os.Getenv("YOLO_TEST_MCP_READER_CHILD") == "1" {
		reader, err := holdMCPOutcomeReader(cname, id)
		if err != nil {
			os.Exit(2)
		}
		defer reader.Close()
		fmt.Println("reader-ready")
		<-time.After(30 * time.Second)
		return
	}
	t.Setenv("HOME", t.TempDir())
	o := retirementOptions(t)
	writeRetirementOutcome(t, cname, id, "ok", o)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCurrentWorkspaceRetryAfterReaderDiesUsesReleasedKernelLease$")
	cmd.Env = append(os.Environ(), "YOLO_TEST_MCP_READER_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "reader-ready\n" {
		t.Fatalf("reader did not register: %q %v", line, err)
	}
	if o.retireMCPOutcome(cname, "podman", id) {
		t.Fatal("retired outcome while child still held reader lease")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child did not die")
	}
	o.retireWorkspaceMCPOutcomes(cname)
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, id)); !os.IsNotExist(err) {
		t.Fatalf("child's released kernel lease blocked cleanup: %v", err)
	}
}

func TestCurrentWorkspaceRetryPreservesForeignGenerationAndScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-foreign-generation"
	id := strings.Repeat("6", 32)
	o := retirementOptions(t)
	writeRetirementOutcome(t, cname, id, "ok", o)
	generation := filepath.Join(mcpOutcomeGenerationDir(cname), mcpOutcomeGenerationName(cname, id))
	if err := os.Remove(generation); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign-generation")
	if err := os.WriteFile(foreign, []byte(`{"id":"`+id+`","cname":"`+cname+`","workspace":"/ws","runtime":"podman"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, generation); err != nil {
		t.Fatal(err)
	}
	o.retireWorkspaceMCPOutcomes(cname)
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, id)); err != nil {
		t.Fatalf("linked generation retired an owned result: %v", err)
	}
	if err := os.Remove(generation); err != nil {
		t.Fatal(err)
	}
	if err := createMCPOutcomeGeneration(cname, id, "/other-workspace", "podman"); err != nil {
		t.Fatal(err)
	}
	o.retireWorkspaceMCPOutcomes(cname)
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, id)); err != nil {
		t.Fatalf("wrong workspace retired result: %v", err)
	}
	if err := createMCPOutcomeGeneration(cname, id, o.Workspace, "podman"); !os.IsExist(err) {
		t.Fatalf("reused random ID overwrote existing generation: %v", err)
	}
}

func TestCurrentWorkspaceRetryRetiresOnlyItsClaimedGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-mcp-workspace-retry"
	id, foreign := strings.Repeat("c", 32), strings.Repeat("d", 32)
	o := retirementOptions(t)
	writeRetirementOutcome(t, cname, id, "ok", o)
	if err := os.WriteFile(mcpTeardownOutcomePath(cname, foreign), []byte(`{"id":"`+foreign+`","result":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	o.retireWorkspaceMCPOutcomes(cname)
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, id)); !os.IsNotExist(err) {
		t.Fatalf("claimed result remains: %v", err)
	}
	if _, err := os.Lstat(mcpTeardownOutcomePath(cname, foreign)); err != nil {
		t.Fatalf("unclaimed foreign result was removed: %v", err)
	}
}
