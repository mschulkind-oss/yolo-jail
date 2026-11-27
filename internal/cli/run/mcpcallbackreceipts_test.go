package run

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// A launch's receipt and failure notes are removed with its lease once its runtime is known
// gone; a live runtime keeps them, and another launch's files are never touched.
func TestManagedMCPCallbackForgetRemovesTheGoneLaunchsReceiptAndFailureNotes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "leases")
	o := callbackLeaseOptions(t, root, true, "")
	lease := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "receipt-cname", Runtime: "podman", Generation: "generation-a"}
	other := mcpsignin.CallbackLease{Version: 1, Port: 3119, Launch: "other-cname", Runtime: "podman", Generation: "generation-b"}
	for _, l := range []mcpsignin.CallbackLease{lease, other} {
		if disposition, _ := o.acquireCallbackLease(l); disposition != "publication pending" {
			t.Fatal(disposition)
		}
		if err := o.writeCallbackReceipt(l.Generation, nil); err != nil {
			t.Fatal(err)
		}
		if err := writeCallbackJSON(mcpsignin.CallbackReceiptPath(root, l.Generation)+".failure-"+strconv.Itoa(l.Port), map[string]any{"version": 1}); err != nil {
			t.Fatal(err)
		}
	}
	o.mcpCallbackGeneration = lease.Generation
	o.mcpCallbackLeases = []mcpsignin.CallbackLease{lease}
	files := func(generation string) []string {
		matches, _ := filepath.Glob(filepath.Join(root, "launch-"+generation+".json*"))
		return matches
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: "live-id"}
	}
	o.forgetGoneContainer(lease.Launch, "podman", "")
	if len(files(lease.Generation)) != 2 {
		t.Fatal("a live runtime lost its receipt files", files(lease.Generation))
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	o.forgetGoneContainer(lease.Launch, "podman", "")
	if got := files(lease.Generation); len(got) != 0 {
		t.Fatal("known-gone launch left receipt files", got)
	}
	if got := files(other.Generation); len(got) != 2 {
		t.Fatal("another launch's receipt files were touched", got)
	}
}
