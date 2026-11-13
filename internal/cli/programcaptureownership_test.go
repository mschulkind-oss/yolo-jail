package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func ordinarySubscription(t *testing.T, key string) preparationRecord {
	t.Helper()
	records, err := preparationSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no ordinary subscription for %s in %+v", key, records)
	return preparationRecord{}
}

// A machine-wide ordinary subscription belongs to the USER selection. A second workspace whose
// effective vector differs (here, its own `packages` change the runtime fingerprint, so its
// launch identity is not the user's) must not deactivate it; only the user resolver may.
func TestOrdinarySubscriptionSurvivesAnotherWorkspacesDifferentVector(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, true)
	stubPreparationWorkerStart(t)
	selectedNPMSharedWorker(t)
	if _, err := prepareOrdinaryForLaunch(req, io.Discard); err != nil {
		t.Fatal(err)
	}
	key := ordinaryProgramKey(f.in)
	if r := ordinarySubscription(t, key); !r.Active {
		t.Fatalf("first launch did not register its user-authorized program: %+v", r)
	}

	other := t.TempDir()
	writeFile(t, filepath.Join(other, paths.WorkspaceConfigLegacyJSONCName), `{"packages":["hello"]}`)
	cfg, err := config.LoadConfig(other, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	req2 := req
	req2.Workspace = other
	req2.Fingerprint = "workspace-specific-" + req.Fingerprint
	req2.Env = ordinarySelectionEnvironment(cfg, req.Packs)
	// The second launch's own outcome is not under test; only what it does to the subscription.
	_, _ = prepareOrdinaryForLaunch(req2, io.Discard)
	if r := ordinarySubscription(t, key); !r.Active {
		t.Fatalf("another workspace's launch deactivated the user's machine-wide subscription: %+v", r)
	}

	// A real user-selection removal still revokes it, through the same launch registration:
	// the workspace still selects the pack, the user no longer does.
	data, err := os.ReadFile(paths.UserConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var user map[string]any
	if err := json.Unmarshal(data, &user); err != nil {
		t.Fatal(err)
	}
	user["packs"] = []any{}
	out, _ := json.Marshal(user)
	writeFile(t, paths.UserConfigPath(), string(out))
	_, _ = prepareOrdinaryForLaunch(req, io.Discard)
	if r := ordinarySubscription(t, key); r.Active {
		t.Fatalf("user-selection removal left the ordinary subscription active: %+v", r)
	}
}

// The policy switch is the other revocation: with agent_updates turned off for its owner, a launch
// from a workspace whose vector does not carry the program still deactivates it, because the
// user resolver no longer authorizes automatic work for it.
func TestOrdinarySubscriptionRevokedByAgentUpdatesOffFromAnotherWorkspace(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, true)
	stubPreparationWorkerStart(t)
	selectedNPMSharedWorker(t)
	if _, err := prepareOrdinaryForLaunch(req, io.Discard); err != nil {
		t.Fatal(err)
	}
	key := ordinaryProgramKey(f.in)
	if r := ordinarySubscription(t, key); !r.Active {
		t.Fatalf("first launch did not register its user-authorized program: %+v", r)
	}
	data, err := os.ReadFile(paths.UserConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var user map[string]any
	if err := json.Unmarshal(data, &user); err != nil {
		t.Fatal(err)
	}
	user["agent_updates"] = false
	out, _ := json.Marshal(user)
	writeFile(t, paths.UserConfigPath(), string(out))

	other := t.TempDir()
	writeFile(t, filepath.Join(other, paths.WorkspaceConfigLegacyJSONCName), `{"packages":["hello"]}`)
	cfg, err := config.LoadConfig(other, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	req2 := req
	req2.Workspace = other
	req2.Fingerprint = "workspace-specific-" + req.Fingerprint
	req2.Env = ordinarySelectionEnvironment(cfg, req.Packs)
	_, _ = prepareOrdinaryForLaunch(req2, io.Discard)
	r := ordinarySubscription(t, key)
	if r.Active {
		t.Fatalf("agent_updates off left the ordinary subscription active: %+v", r)
	}
	if !strings.Contains(r.Reason, "off for pack") || !strings.Contains(r.Reason, "agent_updates") || strings.Contains(r.Reason, "use a fresh launch") {
		t.Fatalf("policy-off revocation reason does not name the policy: %q", r.Reason)
	}

	// Turning the policy back on and launching re-registers it as pending work, not as an
	// active record still labelled inactive (seen in the 2026-11-13 runtime acceptance).
	user["agent_updates"] = true
	out, _ = json.Marshal(user)
	writeFile(t, paths.UserConfigPath(), string(out))
	if _, err := prepareOrdinaryForLaunch(req, io.Discard); err != nil {
		t.Fatal(err)
	}
	if r := ordinarySubscription(t, key); !r.Active || r.Phase != "pending" || r.Reason != "" {
		t.Fatalf("re-enabled subscription: active=%v phase=%q reason=%q, want active pending work", r.Active, r.Phase, r.Reason)
	}
}

// markCaptureJailHome stands in for the real run's per-container jail home: after the install
// child returns, its staging workspace's jail has a home under the agents dir.
func markCaptureJailHome(t *testing.T) *[]string {
	t.Helper()
	var homes []string
	prev := programCaptureChild
	t.Cleanup(func() { programCaptureChild = prev })
	programCaptureChild = func(ctx context.Context, workspace string, argv []string, w io.Writer) error {
		err := prev(ctx, workspace, argv, w)
		if len(argv) > 2 && argv[2] == "program-npm" {
			return err
		}
		home := filepath.Join(paths.AgentsDir(), stagingJailName(workspace))
		if mkErr := os.MkdirAll(filepath.Join(home, "claude"), 0o755); mkErr != nil {
			t.Error(mkErr)
		}
		homes = append(homes, home)
		return err
	}
	return &homes
}

// programStagingDirs lists every program staging entry: the workspace and its owner records.
func programStagingDirs(t *testing.T, f *selectedNPMFixture) []string {
	t.Helper()
	parent := filepath.Dir(f.store.StagingDir("program-x"))
	entries, err := os.ReadDir(parent)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "program-") {
			out = append(out, filepath.Join(parent, e.Name()))
		}
	}
	return out
}

func TestProgramCaptureReclaimsProducerWorkspaceOnceKnownGone(t *testing.T) {
	f := newSelectedNPMFixture(t)
	homes := markCaptureJailHome(t)
	cold := f.advance(t, false)
	if cold.Entry == "" || len(*homes) != 1 {
		t.Fatalf("cold capture %+v homes %v", cold, *homes)
	}
	if left := programStagingDirs(t, f); len(left) != 0 {
		t.Fatalf("admitted capture leaked its producer staging: %v", left)
	}
	for _, home := range *homes {
		if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("admitted capture leaked its per-container jail home %s (%v)", home, err)
		}
	}
}

func TestProgramCaptureRetainsProducerWorkspaceWhenLivenessUnknown(t *testing.T) {
	f := newSelectedNPMFixture(t)
	homes := markCaptureJailHome(t)
	prevWait := forkBuildGoneWait
	forkBuildGoneWait = 0
	// The resolve child's jail is known gone; once the install child has returned, the
	// runtime can no longer account for its jail.
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, len(*homes) == 0 }
	t.Cleanup(func() { forkBuildGoneWait = prevWait })
	boundary, err := registerNPMCaptureRequest(context.Background(), f.store, f.in)
	if err != nil {
		t.Fatal(err)
	}
	_, err = advanceNPMCapture(context.Background(), f.store, f.in, "recipe-v1", false, boundary, io.Discard, func(commit func() error) error { return commit() })
	if err == nil {
		t.Fatal("a capture whose jail is not known gone was admitted")
	}
	left := programStagingDirs(t, f)
	retained := false
	for _, path := range left {
		if info, statErr := os.Lstat(path); statErr == nil && info.IsDir() {
			retained = true
		}
	}
	if !retained {
		t.Fatalf("unknown liveness did not retain the producer staging: %v (err %v)", left, err)
	}
	if len(*homes) != 1 {
		t.Fatalf("install child did not run: %v", *homes)
	}
	if _, err := os.Lstat((*homes)[0]); err != nil {
		t.Fatalf("unknown liveness reclaimed the per-container jail home: %v", err)
	}
}
