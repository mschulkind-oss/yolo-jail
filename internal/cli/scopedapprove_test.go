package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// This selected synthetic native declaration supplies cap metadata only. Its
// explicitly bound test revalidator supplies no Git/GitHub or execution authority.
func scopedNativeApprovalFixture(t *testing.T) (*brokerstore.Store, brokerstore.Request, string, *time.Time) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Cleanup(packload.ReleaseEmbedded)
	module := filepath.Join(home, "lab", "loopholes", "native-cap-lab")
	manifest := filepath.Join(module, "loophole.toml")
	writeApprovePackFile(t, filepath.Join(home, "lab", "pack.toml"), "name = 'lab'\n[[loophole]]\nfrom = 'loopholes/native-cap-lab'\n")
	writeApprovePackFile(t, manifest, "name = 'native-cap-lab'\ntransport = 'loopback-tls'\n[brokered]\nsource = 'native-cap-lab'\nremote_host = 'fixture.invalid'\nread_max_for = '1h'\n[host_daemon]\ncmd = ['false', '{repository_scope}', '{socket}']\npublishes = 'socket'\n")
	writeApprovePackFile(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), fmt.Sprintf("packs = ['src/lab']\n[pack_sources]\nsrc = %q\n", home))
	stores, diagnostics, err := approvalStores()
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("selected declaration discovery: %v %v", err, diagnostics)
	}
	store := storeForSource(stores, "native-cap-lab")
	if store == nil || store.ApprovalStatus != "selected" {
		t.Fatalf("synthetic cap source not selected: %+v", store)
	}
	if store.Revalidate != nil {
		t.Fatal("a selected synthetic native declaration fabricated native authority")
	}
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	store.Now = func() time.Time { return now }
	store.NativeTimedPolicy = true
	store.Entries = brokerstore.OperationEntries
	store.TimedLimit = func(q brokerstore.Request) (brokerstore.TimedBound, error) {
		return brokercore.SelectedTimedLimit("native-cap-lab", q)
	}
	q := brokerstore.Request{Jail: "lab-jail", Start: "lab-start", Workspace: filepath.Join(home, "workspace"), Set: "lab-read", Repo: "fixture.invalid/team/project", Argv: []string{"read", "exact-query"}, StdinDigest: "fixture-intent", Policy: brokerstore.Policy{Window: brokerstore.TimedGrantDefault, MaxWindow: 12 * time.Hour, Uses: 1}, CallReads: []brokerstore.CallRead{{Cell: "read", Chain: []brokerstore.CallResource{{Type: "repo", ID: "fixture.invalid/team/project"}}}}}
	q.Digest = brokerstore.CommandDigest(q)
	store.Revalidate = func(current brokerstore.Request) (brokerstore.Policy, error) {
		if current.Set != q.Set || current.Repo != q.Repo || !brokerstore.SameWorkspace(current.Workspace, q.Workspace) || current.Jail != q.Jail || current.Start != q.Start || current.Digest != q.Digest || brokerstore.CommandDigest(current) != q.Digest || len(current.CallReads) != 1 || len(current.CallWrites) != 0 {
			return brokerstore.Policy{}, errors.New("fixture intent or read role changed; deny the fixture request")
		}
		return q.Policy, nil
	}
	filed, err := store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	if store.Dir != paths.BrokerSourceDir("native-cap-lab") {
		t.Fatal("fixture bypassed the discovered public store")
	}
	return store, filed, manifest, &now
}

func TestScopedApproveSelectedNativeCapPromptAndPublicAnswer(t *testing.T) {
	for _, duration := range []string{"1h", "1h1s", "default"} {
		t.Run(duration, func(t *testing.T) {
			store, q, _, _ := scopedNativeApprovalFixture(t)
			var out, errs bytes.Buffer
			if rc := approveMain(context.Background(), nil, false, store, &out, &errs); rc != 0 || !strings.Contains(out.String(), "maximum 1h") || !strings.Contains(out.String(), "role read") {
				t.Fatalf("current prompt: rc=%d out=%q errs=%q", rc, out.String(), errs.String())
			}
			out.Reset()
			errs.Reset()
			args := []string{q.ID, "--for", duration}
			if duration == "default" {
				entries, err := store.DecideWorking(q.ID, brokerstore.Decision{})
				if err != nil || len(entries) != 1 || !entries[0].Expires.Equal(q.Filed.Add(brokerstore.TimedGrantDefault)) {
					t.Fatalf("shared default: %+v %v", entries, err)
				}
				return
			}
			rc := approveMain(context.Background(), args, false, store, &out, &errs)
			snapshot, err := store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if duration == "1h1s" {
				if rc != approveExitPolicy || len(snapshot.Working) != 0 || snapshot.Requests[0].State != "pending" || !strings.Contains(errs.String(), "role read") || !strings.Contains(errs.String(), "--for 1h") {
					t.Fatalf("over cap: rc=%d snapshot=%+v errs=%q", rc, snapshot, errs.String())
				}
			} else if rc != 0 || len(snapshot.Working) != 1 || !snapshot.Working[0].Expires.Equal(q.Filed.Add(time.Hour)) || !strings.HasPrefix(snapshot.Working[0].Resource, "broker-op/1:") {
				t.Fatalf("at cap: rc=%d snapshot=%+v errs=%q", rc, snapshot, errs.String())
			}
		})
	}
}

func TestScopedApproveOmittedNativeReadCapUsesSharedDefault(t *testing.T) {
	store, q, manifest, _ := scopedNativeApprovalFixture(t)
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeApprovePackFile(t, manifest, strings.Replace(string(raw), "read_max_for = '1h'\n", "", 1))
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), nil, false, store, &out, &errs); rc != 0 || !strings.Contains(out.String(), "maximum 15m") {
		t.Fatalf("default prompt: %d %q %q", rc, out.String(), errs.String())
	}
	out.Reset()
	errs.Reset()
	if rc := approveMain(context.Background(), []string{q.ID, "--for", "16m"}, false, store, &out, &errs); rc != approveExitPolicy || !strings.Contains(errs.String(), "--for 15m") {
		t.Fatalf("default check: %d %q", rc, errs.String())
	}
	entries, err := store.DecideWorking(q.ID, brokerstore.Decision{})
	if err != nil || len(entries) != 1 || !entries[0].Expires.Equal(q.Filed.Add(brokerstore.TimedGrantDefault)) {
		t.Fatalf("omitted cap default: %+v %v", entries, err)
	}
}

func TestScopedApproveCurrentCapAndExpiredStatePrecedeDuration(t *testing.T) {
	for _, value := range []string{"not-a-duration", "0", "-1m", "2h"} {
		t.Run(value, func(t *testing.T) {
			store, q, _, now := scopedNativeApprovalFixture(t)
			*now = q.Expires()
			store.TimedLimit = func(brokerstore.Request) (brokerstore.TimedBound, error) {
				t.Error("expired request examined its cap")
				return brokerstore.TimedBound{}, os.ErrPermission
			}
			var out, errs bytes.Buffer
			if rc := approveMain(context.Background(), []string{q.ID, "--for", value}, false, store, &out, &errs); rc != approveExitAlreadyDecided || !strings.Contains(errs.String(), "expired") {
				t.Fatalf("expiry precedence: rc=%d errs=%q", rc, errs.String())
			}
		})
	}
	store, q, manifest, _ := scopedNativeApprovalFixture(t)
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeApprovePackFile(t, manifest, strings.Replace(string(raw), "read_max_for = '1h'", "read_max_for = '30m'", 1))
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), []string{q.ID, "--for", "1h"}, false, store, &out, &errs); rc != approveExitPolicy || !strings.Contains(errs.String(), "--for 30m") {
		t.Fatalf("cap was not reread: rc=%d errs=%q", rc, errs.String())
	}
}
