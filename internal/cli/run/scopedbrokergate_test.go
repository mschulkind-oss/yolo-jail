package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func scopedBrokerLaunchGateFixture(t *testing.T) (*Options, *strings.Builder, string) {
	t.Helper()
	o, output := applyFixture(t, "", "{}", applyJail{})
	if err := os.Remove(filepath.Join(o.Workspace, config.WorkspaceLegacyConfigName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "tasks-loop")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks-loop","transport":"loopback-tls","call_broker":{"source":"local-tasks","front":"cli","program":"fixture-task","classifier":["yolo","internal","call-broker-classify","circleci"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["all"]}]}}`
	path := filepath.Join(module, "manifest.jsonc")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "task-pack", HostExecApproved: true}})
	return o, output, path
}

func scopedNativeApplyFixture(t *testing.T) (*Options, *strings.Builder) {
	t.Helper()
	o, output := applyFixture(t, "", "{}", applyJail{running: true})
	if err := os.Remove(filepath.Join(o.Workspace, config.WorkspaceLegacyConfigName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), []byte("[requests.git]\npush = [\"main\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml")
	if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("packs = [\"git\"]\n[allow.git]\non_request = [\"push\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(o.Workspace, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, ".git", "config"), []byte("[remote \"origin\"]\nurl = git@github.com:o/r.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "native-fixture")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"native-fixture","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["yolo","internal","daemon","git-broker","--scope-file","{repository_scope}"],"publishes":"socket"},"brokered":{"source":"git","remote_host":"*","grants":["branch"],"read_capability":"fetch","kind_capabilities":{"branch":"push"}}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "native-test", HostExecApproved: true}})
	baseline, err := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteWorkspaceBootBaseline(o.Workspace, "podman-self", baseline); err != nil {
		t.Fatal(err)
	}
	return o, output
}

func TestScopedBrokerLaunchNativeMetadataNeverReachesExecutableCarrier(t *testing.T) {
	o, output := scopedNativeApplyFixture(t)
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatalf("native fixture config refused: %s", output)
	}
	answer(o, "y")
	if !o.runConfigGate(cfg, "podman", nil) {
		t.Fatalf("explicit gate refused: %s", output)
	}
	if len(config.ApprovedGrants(o.Workspace, "git")) == 0 || len(o.approvedScopes["native-fixture"]) != 0 || len(o.approvedGrants["native-fixture"]) != 0 {
		t.Fatalf("approval data became executable: record=%v runtime=%v %v", config.ApprovedGrants(o.Workspace, "git"), o.approvedScopes, o.approvedGrants)
	}
}

func TestScopedBrokerApplyNativeMetadataAndLegacyWithdrawal(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-write-only", true: "withdraw-live-read"}[withdraw], func(t *testing.T) {
			o, output := scopedNativeApplyFixture(t)
			live := brokerscope.File{Source: "git", LaunchID: "fixture", Repos: nil, Grants: nil}
			if withdraw {
				live.Repos = []string{"git@github.com:o/r.git"}
				live.Grants = []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "github.com/o/r"}}
				prior, err := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
				if err := config.RecordApproval(o.Workspace, prior, &config.ScopeCheck{Sources: []config.ScopeSource{{Source: "git", Label: "native-fixture", Form: brokerscope.FormRemoteURL,
					Read: brokerscope.Read{Remotes: []brokerscope.Remote{{Name: "origin", Repo: "git@github.com:o/r.git"}}}, Grants: live.Grants}}}); err != nil {
					t.Fatal(err)
				}
			}
			swapSeam(t, &liveScopeFilesSeam, func(context.Context, string, string, string) ([]liveScopeFile, error) {
				return []liveScopeFile{{File: live, LiveScope: true}}, nil
			})
			writes := 0
			swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, _ string, _ brokerscope.File, repos []string, grants []brokerscope.Grant) error {
				writes++
				if len(repos) != 0 || len(grants) != 0 {
					return errors.New("latent write addition reached live native authority")
				}
				return nil
			})
			answer(o, "y")
			if rc := o.configApply(false); rc != 3 || !strings.Contains(output.String(), "approval metadata recorded; scoped native write dispatch inactive in this build") {
				t.Fatalf("metadata apply promised activation: rc=%d %s", rc, output)
			}
			if writes != map[bool]int{false: 0, true: 1}[withdraw] || len(config.ApprovedGrants(o.Workspace, "git")) == 0 {
				t.Fatalf("metadata/withdrawal effects: writes=%d record=%v output=%s", writes, config.ApprovedGrants(o.Workspace, "git"), output)
			}
			if withdraw && !strings.Contains(output.String(), "applied now") {
				t.Fatalf("legacy withdrawal was not truthfully applied: %s", output)
			}
			if strings.Contains(output.String(), "A restart ends every session") {
				t.Fatalf("metadata-only result promised restart activation: %s", output)
			}
			// The marker is not a dispatch switch on the next real apply either.
			live.Repos, live.Grants = nil, nil
			output.Reset()
			writes = 0
			if rc := o.configApply(false); rc != 3 || writes != 0 || !strings.Contains(output.String(), "approval metadata recorded; scoped native write dispatch inactive") {
				t.Fatalf("marked reapply executed unsupported writes: rc=%d writes=%d %s", rc, writes, output)
			}
		})
	}
}

func TestScopedBrokerApplyMixedNativeMetadataAndLegacyRead(t *testing.T) {
	o, output := scopedNativeApplyFixture(t)
	user := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml")
	if err := os.WriteFile(user, []byte("packs = [\"git\", \"github\"]\n[allow.git]\non_request = [\"push\"]\n[allow.github]\nalways = [\"read\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "legacy-reader")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"legacy-reader","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["yolo","internal","daemon","github-broker","--scope-file","{repository_scope}"],"publishes":"socket"},"brokered":{"source":"github","remote_host":"github.com","read_capability":"read"}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules(append(loopholes.PackModules(), loopholes.PackModule{Dir: module, Pack: "legacy-test", HostExecApproved: true}))
	swapSeam(t, &liveScopeFilesSeam, func(_ context.Context, source, _, _ string) ([]liveScopeFile, error) {
		return []liveScopeFile{{File: brokerscope.File{Source: source, LaunchID: "fixture"}, LiveScope: true}}, nil
	})
	var effects []string
	swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, _ string, file brokerscope.File, repos []string, grants []brokerscope.Grant) error {
		effects = append(effects, file.Source)
		if file.Source != "github" || len(repos) != 1 || !brokerscope.HoldsRead(grants) {
			return errors.New("unsupported metadata reached runtime or normal read was lost")
		}
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 || len(effects) != 1 || effects[0] != "github" || !strings.Contains(output.String(), "legacy-reader repository scope") || !strings.Contains(output.String(), "applied now") || !strings.Contains(output.String(), "approval metadata recorded; scoped native write dispatch inactive") {
		t.Fatalf("mixed apply effects/report: rc=%d effects=%v %s", rc, effects, output)
	}
}

func TestScopedBrokerApplyLegacyUnmarkedMetadataDoesNotClaimRecording(t *testing.T) {
	o, output := scopedNativeApplyFixture(t)
	current, err := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.RecordApproval(o.Workspace, current, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	if rc := o.configApply(false); rc != 3 || strings.Contains(output.String(), "approval metadata recorded") || config.PolicyModelApproved(o.Workspace, "git") {
		t.Fatalf("unmarked no-op claimed model recording: rc=%d %s", rc, output)
	}
	after, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	if !bytes.Equal(before, after) || !strings.Contains(output.String(), "legacy source remains unmarked") {
		t.Fatalf("legacy no-op changed history or hid the boundary: %s", output)
	}
}

func TestScopedBrokerApplyFatalRequestPreservesMarkedApproval(t *testing.T) {
	o, output := scopedNativeApplyFixture(t)
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 || !config.PolicyModelApproved(o.Workspace, "git") {
		t.Fatalf("initial metadata approval failed: rc=%d %s", rc, output)
	}
	paths := []string{config.ApprovalScopePath(o.Workspace), config.ApprovalScopeSourcesPath(o.Workspace), config.ApprovalSnapshotPath(o.Workspace)}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		var err error
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), []byte("[requests.git]\npush = [\"bad..name\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writes := 0
	swapSeam(t, &replaceScopeReposSeam, func(context.Context, string, brokerscope.File, []string, []brokerscope.Grant) error {
		writes++
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 1 || writes != 0 {
		t.Fatalf("fatal request reached apply: rc=%d writes=%d %s", rc, writes, output)
	}
	for i, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before[i], after) {
			t.Fatalf("fatal request mutated marked approval %s: %v", path, err)
		}
	}
}

func TestScopedBrokerApplyNativeMetadataCancellationHasNoEffects(t *testing.T) {
	o, output := scopedNativeApplyFixture(t)
	calls := 0
	swapSeam(t, &replaceScopeReposSeam, func(context.Context, string, brokerscope.File, []string, []brokerscope.Grant) error {
		calls++
		return nil
	})
	answer(o, "n")
	if rc := o.configApply(false); rc != 1 || calls != 0 || len(config.ApprovedGrants(o.Workspace, "git")) != 0 {
		t.Fatalf("cancelled native metadata affected records/runtime: rc=%d calls=%d %s", rc, calls, output)
	}
}

func TestScopedBrokerLaunchAndApplyInactiveNeverPromptOrApprove(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "launch", true: "apply"}[apply], func(t *testing.T) {
			o, output, _ := scopedBrokerLaunchGateFixture(t)
			var hooks *gateHooks
			if apply {
				hooks = &gateHooks{apply: true}
			}
			if !o.runConfigGate(jsonx.NewOrderedMap(), "podman", hooks) {
				t.Fatalf("inactive source blocked gate: %s", output)
			}
			if len(o.approvedScopes["tasks-loop"]) != 0 || strings.Contains(output.String(), "selected call-broker declaration") || strings.Contains(output.String(), "Approve") {
				t.Fatalf("inactive source prompted/approved: %s %+v", output, o.approvedScopes)
			}
			if roots := config.ApprovedScope(o.Workspace, "local-tasks"); len(roots) != 0 {
				t.Fatalf("inactive source recorded roots: %v", roots)
			}
			set := loopholes.NewHostSet(loopholes.LaunchBlock(jsonx.NewOrderedMap()))
			o.writeScopeFiles("fixture-no-root", set.All())
			if o.scopeFiles["tasks-loop"] != "" {
				t.Fatal("inactive source obtained the scope carrier required to start pack code")
			}
			handles := o.startLoopholes("fixture-no-root", "podman", jsonx.NewOrderedMap())
			t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("fixture-no-root", false), "", "") })
			if len(handles) != 0 {
				t.Fatalf("inactive source started a host daemon: %+v", handles)
			}
		})
	}
}

func TestScopedBrokerLaunchAndApplyDeclarationCancellationBlocksConsumer(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "launch", true: "apply"}[apply], func(t *testing.T) {
			o, output, manifestPath := scopedBrokerLaunchGateFixture(t)
			if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), []byte("[call_roots]\nlocal-tasks = [\"team:alpha\"]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var hooks *gateHooks
			if apply {
				hooks = &gateHooks{apply: true}
			}
			answer(o, "y")
			if !o.runConfigGate(jsonx.NewOrderedMap(), "podman", hooks) {
				t.Fatalf("first root not admitted: %s", output)
			}
			before, err := os.ReadFile(config.ApprovalScopePath(o.Workspace))
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.Replace(body, []byte(`"tables":["all"]`), []byte(`"tables":["all"],"max_for":"1h"`), 1)
			if err := os.WriteFile(manifestPath, body, 0o644); err != nil {
				t.Fatal(err)
			}
			output.Reset()
			answer(o, "n")
			if o.runConfigGate(jsonx.NewOrderedMap(), "podman", hooks) {
				t.Fatalf("cancelled declaration let consumer proceed: %s", output)
			}
			if !strings.Contains(output.String(), "removed") || !strings.Contains(output.String(), "added") || strings.Contains(output.String(), "read from") || !strings.Contains(output.String(), "next") {
				t.Fatalf("declaration-only prompt/next step: %s", output)
			}
			after, _ := os.ReadFile(config.ApprovalScopePath(o.Workspace))
			if !bytes.Equal(before, after) {
				t.Fatal("cancelled gate wrote declaration approval")
			}
			output.Reset()
			answer(o, "y")
			if !o.runConfigGate(jsonx.NewOrderedMap(), "podman", hooks) || !strings.Contains(output.String(), "max_for=1h") {
				t.Fatalf("cancelled declaration did not prompt next gate: %s", output)
			}
		})
	}
}
