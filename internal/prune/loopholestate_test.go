package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
)

// writeRetiredGeneration lays down one retired generation holding one loophole's state.
func writeRetiredGeneration(t *testing.T, stateRoot, stamp, loophole, content string) {
	t.Helper()
	dir := filepath.Join(stateRoot, RetiredLoopholeStateDir, stamp, loophole, "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.key"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// THE CONTRACT BETWEEN THE TWO PACKAGES. The launch path (internal/packstage) writes the
// archive; this package reclaims it. A name mismatch is a sweep that silently finds nothing
// while the archive grows forever — the same class of defect PruneHostArchiveBuckets' V3
// bucket enumeration fixed, so it is pinned rather than commented.
func TestRetiredDirNameMatchesTheWriter(t *testing.T) {
	if RetiredLoopholeStateDir != packstage.RetiredLoopholeStateDir {
		t.Errorf("prune sweeps %q but the launch path writes %q — the sweep would find nothing",
			RetiredLoopholeStateDir, packstage.RetiredLoopholeStateDir)
	}
}

// Keep-newest-N, oldest evicted first, and the ones kept are byte-intact. The generations
// hold private keys, so "kept" has to mean readable, not merely present.
func TestPruneRetiredLoopholeStateKeepsNewest(t *testing.T) {
	stateRoot := t.TempDir()
	writeRetiredGeneration(t, stateRoot, "20260101-000000", "acme-proxy", "OLDEST")
	writeRetiredGeneration(t, stateRoot, "20260202-000000", "acme-proxy", "MIDDLE")
	writeRetiredGeneration(t, stateRoot, "20260303-000000", "acme-proxy", "NEWEST")

	bytesRemoved, removed, names := PruneRetiredLoopholeState(stateRoot, 1, true)
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	if bytesRemoved == 0 {
		t.Error("reclaimed 0 bytes while removing two generations")
	}
	if _, err := os.Lstat(filepath.Join(stateRoot, RetiredLoopholeStateDir, "20260101-000000")); !os.IsNotExist(err) {
		t.Error("the oldest generation survived")
	}
	kept := filepath.Join(stateRoot, RetiredLoopholeStateDir, "20260303-000000",
		"acme-proxy", "state", "ca.key")
	data, err := os.ReadFile(kept)
	if err != nil || string(data) != "NEWEST" {
		t.Errorf("the kept generation is not intact: %q %v", data, err)
	}
	// The report names WHAT went, not only when: a bare stamp cannot tell a user whether the
	// key they were about to look for is the one that just went.
	for _, n := range names {
		if !strings.Contains(n, "acme-proxy") {
			t.Errorf("removed name %q does not name the loophole", n)
		}
	}
}

// DRY-RUN REPORTS AND TOUCHES NOTHING — prune's whole default mode.
func TestPruneRetiredLoopholeStateDryRunDoesNotMutate(t *testing.T) {
	stateRoot := t.TempDir()
	writeRetiredGeneration(t, stateRoot, "20260101-000000", "acme-proxy", "OLD")
	writeRetiredGeneration(t, stateRoot, "20260202-000000", "acme-proxy", "NEW")

	_, removed, _ := PruneRetiredLoopholeState(stateRoot, 1, false)
	if removed != 1 {
		t.Fatalf("dry-run reported %d removals, want 1", removed)
	}
	if _, err := os.Lstat(filepath.Join(stateRoot, RetiredLoopholeStateDir, "20260101-000000")); err != nil {
		t.Errorf("dry-run removed a generation: %v", err)
	}
}

// PRUNE DOES NOT DELETE WHAT IT CANNOT EXPLAIN. A directory whose name is not a stamp is
// left alone — which here also protects a user who moved something into the archive by hand.
func TestPruneRetiredLoopholeStateIgnoresNonStampDirs(t *testing.T) {
	stateRoot := t.TempDir()
	writeRetiredGeneration(t, stateRoot, "20260101-000000", "acme-proxy", "OLD")
	writeRetiredGeneration(t, stateRoot, "20260202-000000", "acme-proxy", "NEW")
	mine := filepath.Join(stateRoot, RetiredLoopholeStateDir, "my-backup")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	PruneRetiredLoopholeState(stateRoot, 0, true)
	if _, err := os.Lstat(mine); err != nil {
		t.Errorf("a non-stamp directory was removed: %v", err)
	}
}

// A LIVE state dir is not in the archive and must never be swept: the sweeper walks
// <state>/.retired only, so a loophole still in use keeps its state through any prune.
func TestPruneRetiredLoopholeStateLeavesLiveStateAlone(t *testing.T) {
	stateRoot := t.TempDir()
	live := filepath.Join(stateRoot, "claude-oauth-broker")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "ca.key"), []byte("LIVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRetiredGeneration(t, stateRoot, "20260101-000000", "acme-proxy", "OLD")
	PruneRetiredLoopholeState(stateRoot, 0, true)
	if _, err := os.ReadFile(filepath.Join(live, "ca.key")); err != nil {
		t.Errorf("a LIVE loophole's state was swept: %v", err)
	}
}

// A missing archive is the normal case (nobody has ever deselected a loophole-bearing pack)
// and must report nothing rather than erroring or inventing a directory.
func TestPruneRetiredLoopholeStateMissingArchiveIsSilent(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "nonexistent")
	b, n, names := PruneRetiredLoopholeState(stateRoot, 3, true)
	if b != 0 || n != 0 || names != nil {
		t.Errorf("missing archive reported %d/%d/%v", b, n, names)
	}
	if _, err := os.Lstat(stateRoot); err == nil {
		t.Error("the sweep created the state root")
	}
}

// The section is WIRED INTO THE REPORT, in the dry-run baseline every user sees. A sweeper
// nothing calls is the shape of defect this whole item exists to fix, so the header is
// asserted from the command, not the function.
func TestPruneReportHasTheRetiredLoopholeStateSection(t *testing.T) {
	o, gs := baseOpts(t)
	writeRetiredGeneration(t, filepath.Join(gs, "state"), "20260101-000000", "acme-proxy", "OLD")
	writeRetiredGeneration(t, filepath.Join(gs, "state"), "20260202-000000", "acme-proxy", "MID")
	writeRetiredGeneration(t, filepath.Join(gs, "state"), "20260303-000000", "acme-proxy", "NEW")
	writeRetiredGeneration(t, filepath.Join(gs, "state"), "20260404-000000", "acme-proxy", "NEWEST")
	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !hasLine(&buf, "Retired loophole state") {
		t.Errorf("the report has no retired-loophole-state section:\n%s", buf.String())
	}
	// keep=hostArchiveKeep(3), so the oldest of four is reported.
	if !strings.Contains(buf.String(), "20260101-000000 (acme-proxy)") {
		t.Errorf("the section does not name the generation it would remove:\n%s", buf.String())
	}
	// Dry-run: still on disk.
	if _, err := os.Lstat(filepath.Join(gs, "state", RetiredLoopholeStateDir, "20260101-000000")); err != nil {
		t.Errorf("the dry-run report removed a generation: %v", err)
	}
}
