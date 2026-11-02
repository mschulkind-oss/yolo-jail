package run

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// inheritscope_copied_test.go is issue #70 through Run and the real installer: in run-as-you mode
// the Seatbelt-confined installer's hard link is refused (forced here with
// entrypoint.RefuseOverlayLinksForTest), so the inherited config is delivered as a COPY, and every
// relaunch was refused as "an existing user file in the way" because the host admitted a prior
// delivery only by its inode. The delivery record the host keeps in the staging root is what admits
// it now; these drive the production caller (macosUserInheritedScope), so deleting its use of the
// record turns them red, not only a test of the helper.

// THE REPORTED RELAUNCH: delivered as a copy, relaunched, dispatched again with the copy kept.
func TestMacosUserRelaunchAdmitsACopiedDelivery(t *testing.T) {
	treeLaunchHome(t, true)
	t.Cleanup(entrypoint.RefuseOverlayLinksForTest(unix.EPERM))
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil || f.dispatched != 1 {
		t.Fatalf("first Run() = %d, dispatched %d, installer error %v\nstderr:\n%s", rc, f.dispatched, err, stderr)
	}
	delivered := overlayFileInfo(t, f.delivered)
	staged := overlayFileInfo(t, filepath.Join(f.sealed, filepath.FromSlash(inheritPreflightLegacyRel)))
	if os.SameFile(delivered, staged) || delivered.Mode().Perm() != 0o444 {
		t.Fatalf("the refused link was not delivered as a sealed copy: %v", delivered)
	}
	body, err := os.ReadFile(f.delivered)
	if err != nil {
		t.Fatal(err)
	}
	rc, stderr, installErr := f.launch()
	if rc != 0 || installErr != nil || f.dispatched != 2 {
		t.Fatalf("relaunch over the copied delivery = %d, dispatched %d, installer error %v\nstderr:\n%s",
			rc, f.dispatched, installErr, stderr)
	}
	if strings.Contains(stderr, "is in the way") {
		t.Fatalf("the relaunch named the copied delivery as a file in the way:\n%s", stderr)
	}
	requireSameDelivery(t, f.delivered, delivered, body)
}

// A CHANGED USER CONFIG REPLACES A COPIED DELIVERY: the overlay is re-staged with the new body, the
// host lists the previous delivery's digest beside the new one, and the installer swaps it.
func TestMacosUserRelaunchReplacesACopiedDeliveryWhenTheConfigChanges(t *testing.T) {
	home := treeLaunchHome(t, true)
	t.Cleanup(entrypoint.RefuseOverlayLinksForTest(unix.EPERM))
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil {
		t.Fatalf("first Run() = %d, installer error %v\nstderr:\n%s", rc, err, stderr)
	}
	before, err := os.ReadFile(f.delivered)
	if err != nil {
		t.Fatal(err)
	}
	reverseUserPacks(t, home)
	rc, stderr, installErr := f.launch()
	if rc != 0 || installErr != nil || f.dispatched != 2 {
		t.Fatalf("relaunch after a config change = %d, dispatched %d, installer error %v\nstderr:\n%s",
			rc, f.dispatched, installErr, stderr)
	}
	after, err := os.ReadFile(f.delivered)
	staged, serr := os.ReadFile(filepath.Join(f.sealed, filepath.FromSlash(inheritPreflightLegacyRel)))
	if err != nil || serr != nil || bytes.Equal(before, after) || !bytes.Equal(after, staged) {
		t.Fatalf("the changed config did not replace the copied delivery (errors %v/%v):\nbefore %q\nafter %q", err, serr, before, after)
	}
}

// THE RECORD IS THE ADMISSION, AND THE AGENT CANNOT WRITE IT. Without the record the same copy is
// refused before dispatch and kept; and the record lives in the staging root, outside the workspace
// and its sidecar, sealed 0444.
func TestMacosUserRelaunchRefusesACopyTheRecordDoesNotList(t *testing.T) {
	treeLaunchHome(t, true)
	t.Cleanup(entrypoint.RefuseOverlayLinksForTest(unix.EPERM))
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil {
		t.Fatalf("first Run() = %d, installer error %v\nstderr:\n%s", rc, err, stderr)
	}
	record := entrypoint.InheritedDeliveryRecordPath(f.sealed)
	info, err := os.Lstat(record)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 {
		t.Fatalf("the launch left no sealed delivery record at %s: %v, %v", record, info, err)
	}
	for _, agentWritable := range []string{f.ws, f.sidecar} {
		if rel, err := filepath.Rel(agentWritable, record); err == nil && !strings.HasPrefix(rel, "..") {
			t.Fatalf("the delivery record %s is under %s, which the agent writes", record, agentWritable)
		}
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	owner := overlayFileInfo(t, f.delivered)
	body, err := os.ReadFile(f.delivered)
	if err != nil {
		t.Fatal(err)
	}
	rc, stderr, _ := f.launch()
	if rc == 0 || f.dispatched != 1 {
		t.Fatalf("a copy with no record was admitted: rc %d, dispatched %d\nstderr:\n%s", rc, f.dispatched, stderr)
	}
	if !strings.Contains(stderr, f.delivered+" is in the way") || !strings.Contains(stderr, f.delivered+".moved-aside") {
		t.Errorf("the refusal does not name the destination and the mv that clears it:\n%s", stderr)
	}
	requireSameDelivery(t, f.delivered, owner, body)
}

// requireSameDelivery fails unless path is still the file before, with body.
func requireSameDelivery(t *testing.T, path string, before os.FileInfo, body []byte) {
	t.Helper()
	after := overlayFileInfo(t, path)
	got, err := os.ReadFile(path)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !bytes.Equal(got, body) {
		t.Fatalf("%s changed: before %v, after %v, read error %v", path, before, after, err)
	}
}

// reverseUserPacks rewrites the host user config with its packs in the opposite order: the same
// selection, a different inherited body.
func reverseUserPacks(t *testing.T, home string) {
	t.Helper()
	p := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	packs := cfg["packs"]
	for i, j := 0, len(packs)-1; i < j; i, j = i+1, j-1 {
		packs[i], packs[j] = packs[j], packs[i]
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
