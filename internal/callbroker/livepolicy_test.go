package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func liveRootApprovalFixture(t *testing.T) (*onceFixture, string) {
	t.Helper()
	f := selectedOnceFixtureForApply(t, false, true, false, true)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_SETUP", "podman-self")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("YOLO_CONFIG", "")
	restore := loopholes.SnapshotPackModules()
	loopholes.ResetPackModules()
	t.Cleanup(restore)
	// Public config apply probes this account-free fake runtime; it never launches a jail.
	podman := "#!/bin/sh\ncase \"$1\" in info) printf '{}\\n';; ps) printf 'fixture-running\\n';; inspect) printf 'YOLO_VERSION=fixture\\n';; *) exit 1;; esac\n"
	if err := os.WriteFile(filepath.Join(f.home, "bin", "podman"), []byte(podman), 0700); err != nil {
		t.Fatal(err)
	}
	writeLiveRootRequests(t, f, []string{"team:beta"})
	initial, err := config.LoadWorkspaceConfig(f.h.workspace, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteWorkspaceBootBaseline(f.h.workspace, "podman-self", initial); err != nil {
		t.Fatal(err)
	}
	if err := config.RecordApproval(f.h.workspace, initial, nil); err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("YOLO_TEST_MATCHED_YOLO")
	if binary == "" {
		binary = filepath.Join(f.home, "bin", "yolo")
		goTool, err := callBrokerTestGoTool()
		if err != nil {
			t.Fatal(err)
		}
		if out, err := callBrokerTestBuild(goTool, binary, "../../cmd/yolo").CombinedOutput(); err != nil {
			t.Fatalf("build public approve fixture: %v %s", err, out)
		}
	}
	t.Chdir(f.h.workspace)
	// Actual public initial gate establishes the complete validated ask view;
	// the normal protected producer binds it to this fixture's private input/lease.
	var gateOutput bytes.Buffer
	if rc := run.ConfigApply([]string{"--accept-config-changes"}, &gateOutput, &gateOutput, false); rc != 0 && rc != 3 {
		t.Fatalf("initial public delivered-ask gate: %d %s", rc, gateOutput.String())
	}
	model := "legacy/1"
	if config.PolicyModelApproved(f.h.workspace, f.h.input.Source) {
		model = "scoped/1"
	}
	asks, err := callbrokerinput.CaptureDelivered(f.h.input.Source, f.h.input.Pack, model, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := callbrokerinput.PublishDelivered(f.scopePath, mustRead(t, f.inputPath), asks); err != nil {
		t.Fatal(err)
	}
	return f, binary
}

func writeLiveRootRequests(t *testing.T, f *onceFixture, roots []string) {
	t.Helper()
	var values []string
	for _, root := range roots {
		values = append(values, "\""+root+"\"")
	}
	body := "[call_roots]\nlocal-tasks = [" + strings.Join(values, ",") + "]\n[requests.\"src/test-pack\"]\nread = true\n[requests.local-tasks.working_set]\nchange = \"ask\"\n[requests.local-tasks.all]\nchange = \"ask\"\nblocked = false\n"
	if err := os.WriteFile(filepath.Join(f.h.workspace, config.WorkspaceTOMLConfigName), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func applyLiveRoots(t *testing.T, f *onceFixture, roots []string) {
	t.Helper()
	writeLiveRootRequests(t, f, roots)
	var out bytes.Buffer
	if rc := run.ConfigApply([]string{"--accept-config-changes"}, &out, &out, false); rc != 0 || !strings.Contains(out.String(), "applied now") {
		t.Fatalf("public root-only apply: rc=%d %s", rc, out.String())
	}
	sf, err := brokerscope.ReadFile(f.scopePath)
	if err != nil || !slices.Equal(sf.Repos, roots) {
		t.Fatalf("public apply did not publish requested roots: %+v %v", sf, err)
	}
}

func publicLiveRootApprove(t *testing.T, f *onceFixture, binary, id string) ([]byte, error) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), binary, "approve", id)
	cmd.Dir = f.h.workspace
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

func TestLiveRootPublicApplyThenStandaloneApproveUsesCurrentRoot(t *testing.T) {
	f, binary := liveRootApprovalFixture(t)
	inputBefore := mustRead(t, f.inputPath)
	// A workspace edit alone is not live root authority.
	writeLiveRootRequests(t, f, []string{"team:alpha", "team:beta"})
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict != "refuses" {
		t.Fatalf("raw added root admitted: %+v %v", v, err)
	}
	if len(f.pending(t)) != 0 || f.effects(t) != 0 {
		t.Fatal("raw root filed or executed a write")
	}
	applyLiveRoots(t, f, []string{"team:alpha", "team:beta"})
	if !bytes.Equal(inputBefore, mustRead(t, f.inputPath)) || slices.Contains(f.h.input.Roots, "team:alpha") {
		t.Fatal("root-only apply rewrote frozen input instead of publishing live root")
	}
	v, _, err := f.call(t, "act", "update", "a1")
	if err != nil || v.Verdict != "asks" || len(f.pending(t)) != 1 || f.effects(t) != 0 {
		t.Fatalf("new live root did not file request: %+v %v", v, err)
	}
	q := f.pending(t)[0]
	if output, err := publicLiveRootApprove(t, f, binary, q.ID); err != nil {
		t.Fatalf("public standalone approve rejected newly live root: %v %s", err, output)
	}
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("approved new-root request did not execute once: %+v %v effects=%d", v, err, f.effects(t))
	}
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "asks" || f.effects(t) != 1 {
		t.Fatalf("new-root approval replayed: %+v %v effects=%d", v, err, f.effects(t))
	}
}

func fileLiveRootRequest(t *testing.T, f *onceFixture) brokerstore.Request {
	t.Helper()
	applyLiveRoots(t, f, []string{"team:alpha", "team:beta"})
	v, _, err := f.call(t, "act", "update", "a1")
	if err != nil || v.Verdict != "asks" || len(f.pending(t)) != 1 || f.effects(t) != 0 {
		t.Fatalf("new-root request: %+v %v effects=%d", v, err, f.effects(t))
	}
	return f.pending(t)[0]
}

func TestLiveRootStandaloneApprovalRefusesWithdrawnRootBeforeAnswer(t *testing.T) {
	f, binary := liveRootApprovalFixture(t)
	q := fileLiveRootRequest(t, f)
	applyLiveRoots(t, f, []string{"team:beta"})
	before, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := publicLiveRootApprove(t, f, binary, q.ID); err == nil {
		t.Fatalf("standalone answer admitted withdrawn classifier root: %s", out)
	}
	after, err := f.store.Snapshot()
	if err != nil || len(after.Grants) != len(before.Grants) || after.Requests[0].State != before.Requests[0].State || f.effects(t) != 0 {
		t.Fatalf("withdrawn root answered or spent: %+v %v", after, err)
	}
}

func TestLiveRootRemovedAfterAnswerNeverSpendsOrExecutes(t *testing.T) {
	f, binary := liveRootApprovalFixture(t)
	q := fileLiveRootRequest(t, f)
	if out, err := publicLiveRootApprove(t, f, binary, q.ID); err != nil {
		t.Fatalf("initial answer: %v %s", err, out)
	}
	before, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	applyLiveRoots(t, f, []string{"team:beta"})
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict != "refuses" {
		t.Fatalf("withdrawn live root executed: %+v", v)
	}
	after, err := f.store.Snapshot()
	if err != nil || len(after.Grants) != len(before.Grants) || len(after.Grants) != 1 || after.Grants[0].Uses != before.Grants[0].Uses || f.effects(t) != 0 {
		t.Fatalf("withdrawal spent approval: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestLiveRootStandaloneApprovalRetainsSelectedLaunchBindings(t *testing.T) {
	for _, mutation := range []string{"source", "declaration", "lease", "input-path", "program", "workspace", "jail", "context"} {
		t.Run(mutation, func(t *testing.T) {
			f, binary := liveRootApprovalFixture(t)
			q := fileLiveRootRequest(t, f)
			switch mutation {
			case "source", "input-path":
				in := f.h.input
				if mutation == "source" {
					in.Source = "another-source"
				} else {
					in.ScopeFile += ".other"
				}
				data, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "program":
				if err := os.WriteFile(f.h.program.path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "declaration":
				path := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
				data := bytes.Replace(mustRead(t, path), []byte(`"name":"change"`), []byte(`"name":"changed"`), 1)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "lease":
				start, err := brokerstore.NewStartID()
				if err != nil {
					t.Fatal(err)
				}
				_, lease, err := brokercore.BindLease(f.h.input.Source, f.scopePath, f.scope, start)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := brokercore.CloseLease(f.h.input.Source, lease); err != nil {
						t.Error(err)
					}
				})
			case "workspace", "jail", "context":
				// A retained request with mismatched intent still cannot borrow this launch
				// when the standalone public approver freshly installs its authority adapter.
				q.ID = ""
				if mutation == "workspace" {
					q.Workspace = t.TempDir()
					// File a distinct stdin intent: the store intentionally joins an
					// identical pending command by jail/start/set/digest, not workspace.
					q.StdinDigest = fmt.Sprintf("%x", sha256.Sum256([]byte("different-workspace-intent")))
				} else if mutation == "jail" {
					q.Jail = "another-jail"
				} else {
					q.ContextDigest = strings.Repeat("ab", 32)
					q.ContextName = "other"
				}
				q.Digest = brokerstore.CommandDigest(q)
				var err error
				ingress := &brokerstore.Store{Dir: f.store.Dir, Source: f.store.SourceName()}
				q, err = ingress.File(q)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if out, err := publicLiveRootApprove(t, f, binary, q.ID); err == nil {
				t.Fatalf("changed %s approved: %s", mutation, out)
			}
			after, err := f.store.Snapshot()
			if err != nil || len(after.Grants) != len(before.Grants) || f.effects(t) != 0 {
				t.Fatalf("changed binding spent/added authority: %+v %v", after, err)
			}
		})
	}
}
