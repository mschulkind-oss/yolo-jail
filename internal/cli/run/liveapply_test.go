package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// liveapply_test.go pins `yolo config apply` and the attach-time gate
// (docs/design/live-config-apply.md §3, §6; the plan's Task A "Tests first").

// applyJail is what the fake runtime answers about this workspace's jail.
type applyJail struct {
	running bool
	psFail  bool   // `podman ps` exits non-zero: the runtime could not be asked
	env     string // the container's frozen environment, one entry per line
}

// swapSeam replaces a package-level seam for one test.
func swapSeam[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// applyFixture is a host-side apply against a fake podman: a workspace whose config is
// bootConfig when the jail started (its effective-config record and its approval), then
// wsConfig on disk now.
func applyFixture(t *testing.T, bootConfig, wsConfig string, jail applyJail) (*Options, *strings.Builder) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if bootConfig != "" {
		writeWorkspaceConfig(t, ws, bootConfig)
		boot, err := config.LoadWorkspaceConfig(ws, false, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := config.WriteWorkspaceBootBaseline(ws, boot); err != nil {
			t.Fatal(err)
		}
		if err := config.RecordApproval(ws, boot, nil); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspaceConfig(t, ws, wsConfig)

	buf := &strings.Builder{}
	o := &Options{}
	fillDefaults(o)
	o.Workspace, o.Stdout, o.Stderr = ws, buf, buf
	o.Getenv = func(string) string { return "" }
	o.IsMacOS, o.IsLinux = false, true
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.PerfLoggingConfig = func() bool { return false }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	answeringPodman(o, minimalPodmanInfo)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[0] == "podman" {
			switch argv[1] {
			case "ps":
				if jail.psFail {
					return ExecResult{Ran: true, RC: 125, Stderr: "cannot connect to the podman socket"}
				}
				if jail.running {
					return ExecResult{Ran: true, Stdout: "abc123\n"}
				}
				return ExecResult{Ran: true}
			case "inspect":
				if jail.running {
					return ExecResult{Ran: true, Stdout: jail.env}
				}
				return ExecResult{Ran: true, RC: 125}
			}
		}
		return ExecResult{Ran: false}
	}
	return o, buf
}

// answer makes the gate's question read a, as a person at a terminal answers it.
func answer(o *Options, a string) {
	o.IsTTYStdin = func() bool { return true }
	o.IsTTYStderr = func() bool { return true }
	o.Stdin = strings.NewReader(a + "\n")
}

// In a jail the command refuses for the jail's own workspace, naming the host command (OQ-S2
// holds: recording an approval must not be reachable from the side being approved), and records
// nothing.
func TestConfigApplyRefusesInAJail(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return ""
	}
	t.Setenv("YOLO_VERSION", "9.9.9")
	t.Setenv("YOLO_WORKSPACE", o.Workspace)
	before, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	answer(o, "y")
	if rc := o.configApply(false); rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	out := buf.String()
	for _, want := range []string{"yolo config apply", "on the host"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
	if now, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !bytes.Equal(before, now) {
		t.Error("an in-jail apply rewrote the approval record")
	}
}

// A workspace a launch inside the jail would open is a nested jail's, whose own launch runs its
// gate in here: the apply is not refused there (LA-D17).
func TestConfigApplyInAJailServesANestedWorkspace(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return ""
	}
	t.Setenv("YOLO_VERSION", "9.9.9")
	t.Setenv("YOLO_WORKSPACE", "/workspace")
	answer(o, "y")
	if rc := o.configApply(false); rc == 1 {
		t.Fatalf("rc = 1, want the nested workspace's apply to run:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "not in this jail") {
		t.Errorf("a nested workspace's apply was refused:\n%s", buf.String())
	}
}

// THE GATE IS THE LAUNCH'S (LA-P1). The record a `y` writes is byte for byte the record a
// fresh launch's gate writes for the same config, so deleting the command's call to the gate
// fails here (nothing recorded), and so does a second gate that records differently.
func TestConfigApplyRecordsWhatALaunchsGateRecords(t *testing.T) {
	const cfg = `{"packages":["jq"],"network":{"ports":["8080:8080"]}}`
	o, buf := applyFixture(t, `{}`, cfg, applyJail{running: true})
	answer(o, "y")
	o.configApply(false)
	got, err := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	if err != nil {
		t.Fatalf("the apply recorded nothing: %v\n%s", err, buf.String())
	}

	l, lbuf := applyFixture(t, `{}`, cfg, applyJail{})
	answer(l, "y")
	merged, ok := l.loadAndValidateConfig()
	if !ok || !l.checkConfigChanges(merged, "podman") {
		t.Fatalf("the launch's gate did not pass:\n%s", lbuf.String())
	}
	want, _ := os.ReadFile(config.ApprovalSnapshotPath(l.Workspace))
	if !bytes.Equal(got, want) {
		t.Errorf("the apply's record differs from a launch's:\napply:\n%s\nlaunch:\n%s", got, want)
	}
	if !strings.Contains(buf.String(), "Accept these workspace config changes? [y/N]") {
		t.Errorf("the apply did not ask the launch's question:\n%s", buf.String())
	}
}

// N records nothing, applies nothing, and exits 1 with the gate's decline lines.
func TestConfigApplyDeclineRecordsNothing(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	before, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	bootBefore, _ := os.ReadFile(config.WorkspaceConfigBootPath(o.Workspace))
	answer(o, "n")
	if rc := o.configApply(false); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, buf.String())
	}
	if now, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !bytes.Equal(before, now) {
		t.Error("a declined apply rewrote the approval record")
	}
	if now, _ := os.ReadFile(config.WorkspaceConfigBootPath(o.Workspace)); !bytes.Equal(bootBefore, now) {
		t.Error("a declined apply folded a change into the effective-config record")
	}
	if !strings.Contains(buf.String(), "nothing was recorded") {
		t.Errorf("the decline does not say nothing was recorded:\n%s", buf.String())
	}
}

// The outcome block prints after the gate's diff and before its question, and again, as the
// result, after it (LA-P3). network.ports is restart-only: its line names the next step, the
// command never offers a restart, and the exit is 3 (approved, restart owed).
func TestConfigApplyNetworkPortsWaitsForRestart(t *testing.T) {
	o, buf := applyFixture(t, `{"network":{"ports":["1:1"]}}`, `{"network":{"ports":["2:2"]}}`,
		applyJail{running: true})
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	out := buf.String()
	line := "machine.ports: at next restart (yolo stop, then yolo)"
	diff := strings.Index(out, `+        "host": 2`)
	first := strings.Index(out, line)
	question := strings.Index(out, "[y/N]")
	last := strings.LastIndex(out, line)
	if diff < 0 || first < 0 || question < 0 {
		t.Fatalf("want the diff, the outcome line and the question:\n%s", out)
	}
	if !(diff < first && first < question && question < last) {
		t.Errorf("the outcome block must follow the diff, precede the question, and print again after:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "restart now") || strings.Contains(out, "[y/N] ") &&
		strings.Count(out, "[y/N]") > 1 {
		t.Errorf("the command offered a restart:\n%s", out)
	}
	if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace); !drifted {
		t.Error("a restart-only key was folded into the effective-config record")
	}
}

// A next-session key is folded into the effective-config record at once, so drift says nothing
// is pending, and the exit is 0.
func TestConfigApplyFoldsANextSessionKey(t *testing.T) {
	o, buf := applyFixture(t, `{"agents_md_extra":"one"}`, `{"agents_md_extra":"two"}`, applyJail{running: true})
	answer(o, "y")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "agents_md_extra: from your next yolo session") {
		t.Errorf("no next-session line:\n%s", buf.String())
	}
	if _, drifted, ok, err := config.WorkspaceConfigDrift(o.Workspace); err != nil || !ok || drifted {
		t.Errorf("after the apply the record must match the workspace: drifted=%v ok=%v err=%v", drifted, ok, err)
	}
}

// With no jail running the command records only and says the next `yolo` launches with the
// changes; a restart key changed, so the exit is 3.
func TestConfigApplyWithNoJailRunningRecordsOnly(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: false})
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"No jail is running", "the next `yolo`"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !strings.Contains(string(b), "jq") {
		t.Error("the approval was not recorded")
	}
}

// Could not ask is not "not running": record only, and name the runtime's error and the
// command that lists running containers.
func TestConfigApplyThatCannotAskRecordsOnly(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{psFail: true})
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"cannot connect to the podman socket", "podman ps"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not name %q:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !strings.Contains(string(b), "jq") {
		t.Error("the approval was not recorded")
	}
}

// --record-only approves and touches no running jail: a live mount change waits for restart.
func TestConfigApplyRecordOnlyTouchesNoJail(t *testing.T) {
	src := t.TempDir()
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
	liveMounts(t)
	injected := 0
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error {
		injected++
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(true); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if injected != 0 {
		t.Error("--record-only injected a mount")
	}
	if !strings.Contains(buf.String(), "at next restart") {
		t.Errorf("a recorded-only live change must say it waits:\n%s", buf.String())
	}
}

// liveMounts makes the C seams answer for a jail that takes live mounts and recorded none.
func liveMounts(t *testing.T) *[]liveCtxMount {
	t.Helper()
	rec := &[]liveCtxMount{}
	swapSeam(t, &ctxMountsLiveSeam, func(*Options, string, string, []string) (bool, string) { return true, "" })
	swapSeam(t, &readCtxMountRecordSeam, func(string, []string) ([]liveCtxMount, bool) { return *rec, true })
	swapSeam(t, &writeCtxMountRecordSeam, func(_, _ string, e []liveCtxMount) error {
		*rec = append([]liveCtxMount(nil), e...)
		return nil
	})
	swapSeam(t, &diffCtxMountsSeam, func(have []liveCtxMount, want []configCtxMount) ([]configCtxMount, []liveCtxMount) {
		var add []configCtxMount
		for _, w := range want {
			found := false
			for _, h := range have {
				found = found || (h.Source == w.source && h.Dest == w.dest && h.RW == w.rw)
			}
			if !found {
				add = append(add, w)
			}
		}
		var remove []liveCtxMount
		for _, h := range have {
			found := false
			for _, w := range want {
				found = found || (h.Source == w.source && h.Dest == w.dest && h.RW == w.rw)
			}
			if !found {
				remove = append(remove, h)
			}
		}
		return add, remove
	})
	return rec
}

func TestApplyPlanRequiresVerifiedLiveScopeAfterApprovalIsUnchanged(t *testing.T) {
	bootConfig := `{"brokered":{"github":{"repos":["o/old"]}}}`
	currentConfig := `{"brokered":{"github":{"repos":["o/new"]}}}`
	scope := &config.ScopeCheck{Sources: []config.ScopeSource{{Source: "github", Label: "github-broker",
		Entry: []config.EntryRepo{{Repo: "o/new", Files: []string{"yolo-jail.jsonc"}}}}}}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "absent live scope"},
		{name: "discovery error", err: errors.New("source lock wait timed out")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := applyFixture(t, bootConfig, currentConfig, applyJail{running: true})
			current := mapOf(t, currentConfig)
			if err := config.RecordApproval(o.Workspace, current, scope); err != nil {
				t.Fatal(err)
			}
			if changes, err := config.ScopeChanges(o.Workspace, scope); err != nil || len(changes.ScopeLabels) != 0 {
				t.Fatalf("scope approval is not unchanged: changes=%+v err=%v", changes, err)
			}
			swapSeam(t, &liveScopeFilesSeam, func(context.Context, string, string, string) ([]liveScopeFile, error) {
				return nil, tc.err
			})
			run := &applyRun{cfg: current, current: current, rt: "podman", cname: "yolo-ws-abcd1234",
				mode: applyMode{running: true}}
			plan := o.applyPlan(run, scope)
			if len(plan) != 1 || plan[0].outcome != outcomeNextRestart {
				t.Fatalf("applyPlan = %+v, want one restart-required change", plan)
			}
			if !strings.Contains(plan[0].reason, "no running broker") && tc.err == nil {
				t.Errorf("absent live scope reason = %q", plan[0].reason)
			}
			if tc.err != nil && !strings.Contains(plan[0].reason, tc.err.Error()) {
				t.Errorf("discovery error %q was not preserved in %q", tc.err, plan[0].reason)
			}
			if strings.Contains(plan[0].reason, "already matches") {
				t.Errorf("unchanged approval was treated as proof the live scope matched: %+v", plan[0])
			}
		})
	}
}

func TestConfigApplyRestoresTheInvalidatedMountRecordThroughRealApply(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement bool
	}{
		{name: "add then later removal"},
		{name: "remove then replacement add", replacement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var boot, current string
			var initial []ctxMountEntry
			var wantActions []string
			var laterRemoval bool
			if tc.replacement {
				root := resolvedRunTemp(t)
				oldSource, newSource := filepath.Join(root, "old", "repo"), filepath.Join(root, "new", "repo")
				for _, dir := range []string{oldSource, newSource} {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				boot = `{"mounts":["` + oldSource + `"]}`
				current = `{"mounts":["` + newSource + `"]}`
				initial = []ctxMountEntry{{Source: oldSource, Dest: "/ctx/repo"}}
				wantActions = []string{"remove:" + oldSource, "add:" + newSource}
			} else {
				source := filepath.Join(resolvedRunTemp(t), "addition")
				if err := os.MkdirAll(source, 0o700); err != nil {
					t.Fatal(err)
				}
				boot = `{}`
				current = `{"mounts":["` + source + `"]}`
				wantActions = []string{"add:" + source, "remove:" + source}
				laterRemoval = true
			}

			const token = "captured-launch-token"
			env := entrypoint.ContractTagsEnv + "=" + launchContractTagsValue() + "\n" +
				ctxMountsTokenEnv + "=" + token + "\n"
			o, buf := applyFixture(t, boot, current, applyJail{running: true, env: env})
			o.PathExists = func(string) bool { return false }
			answeringPodman(o, rootlessInfo)
			cname := runtime.FromWorkspace(o.Workspace)
			if err := writeCtxMountRecord(cname, token, initial); err != nil {
				t.Fatal(err)
			}
			mounted := append([]ctxMountEntry(nil), initial...)
			parsed, err := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			wantMounts := o.configCtxMounts("podman", parsed, nil)
			var final []ctxMountEntry
			for _, m := range wantMounts {
				final = append(final, ctxMountEntry{Source: m.source, Dest: m.dest, RW: m.rw})
			}
			var actions []string
			// The only mocked production seams are the namespace mutations themselves. Record
			// invalidation, postmutation writes, reads, planning, and baseline folding are real.
			swapSeam(t, &injectCtxMountSeam, func(_ *Options, _, _ string, m configCtxMount, _ time.Time) error {
				mounted = append(mounted, ctxMountEntry{Source: m.source, Dest: m.dest, RW: m.rw})
				actions = append(actions, "add:"+m.source)
				return nil
			})
			swapSeam(t, &removeCtxMountSeam, func(_ *Options, _, _ string, e liveCtxMount, _ time.Time) error {
				found := false
				kept := mounted[:0:0]
				for _, m := range mounted {
					if m.Dest == e.Dest {
						found = m == e
						continue
					}
					kept = append(kept, m)
				}
				if !found {
					return fmt.Errorf("namespace does not contain mount %+v", e)
				}
				mounted = kept
				actions = append(actions, "remove:"+e.Source)
				return nil
			})

			applyAndCheck := func(want []ctxMountEntry) {
				t.Helper()
				answer(o, "y")
				if rc := o.configApply(false); rc != 0 {
					t.Fatalf("config apply rc = %d, want 0\n%s", rc, buf.String())
				}
				got, ok := readCtxMountRecord(cname, []string{ctxMountsTokenEnv + "=" + token})
				if !ok || !slices.Equal(got, want) {
					t.Fatalf("real mount record = %+v, %v; want %+v", got, ok, want)
				}
				if ctxMountRecordInvalidated(cname, []string{ctxMountsTokenEnv + "=" + token}) {
					t.Fatal("successful apply left its live-mount record invalidated")
				}
				if !slices.Equal(mounted, want) {
					t.Fatalf("mock namespace mounts = %+v; want %+v", mounted, want)
				}
				if _, drifted, ok, err := config.WorkspaceConfigDrift(o.Workspace); err != nil || !ok || drifted {
					t.Fatalf("successful mount change was not folded: drifted=%v ok=%v err=%v", drifted, ok, err)
				}
			}

			if tc.replacement {
				applyAndCheck(final)
			} else {
				applyAndCheck(final)
				if laterRemoval {
					writeWorkspaceConfig(t, o.Workspace, `{}`)
					applyAndCheck(nil)
				}
			}
			if !reflect.DeepEqual(actions, wantActions) {
				t.Fatalf("namespace mutations = %v; want %v", actions, wantActions)
			}
		})
	}
}

// A read-only mount added to a jail that takes live mounts: injected, recorded in the host's
// mount record, folded into the effective-config record, `applied now`, exit 0.
func TestConfigApplyInjectsALiveMount(t *testing.T) {
	src := resolvedRunTemp(t)
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
	rec := liveMounts(t)
	var got []configCtxMount
	swapSeam(t, &injectCtxMountSeam, func(_ *Options, _, _ string, m configCtxMount, _ time.Time) error {
		got = append(got, m)
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if len(got) != 1 || got[0].source != src {
		t.Fatalf("injected %v, want the one source %s", got, src)
	}
	if len(*rec) != 1 || (*rec)[0].Source != src {
		t.Errorf("the host's mount record is %v, want the injected mount", *rec)
	}
	if !strings.Contains(buf.String(), "(read-only): applied now") {
		t.Errorf("no applied-now line:\n%s", buf.String())
	}
	if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace); drifted {
		t.Error("the applied mounts key was not folded")
	}
}

func TestConfigApplyFilesystemRecordRestoreFailureKeepsRemovalPending(t *testing.T) {
	src := filepath.Join(resolvedRunTemp(t), "addition")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	const token = "captured-launch-token"
	env := entrypoint.ContractTagsEnv + "=" + launchContractTagsValue() + "\n" +
		ctxMountsTokenEnv + "=" + token + "\n"
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true, env: env})
	o.PathExists = func(string) bool { return false }
	answeringPodman(o, rootlessInfo)
	cname := runtime.FromWorkspace(o.Workspace)
	if err := writeCtxMountRecord(cname, token, nil); err != nil {
		t.Fatal(err)
	}

	injected, removed := 0, 0
	namespaceHasMount := false
	swapSeam(t, &injectCtxMountSeam, func(_ *Options, _, _ string, _ configCtxMount, _ time.Time) error {
		injected++
		namespaceHasMount = true
		// The real invalidator has just left its tombstone. Replace that isolated file with an
		// empty directory: updateCtxMountRecord's real read fails, and the real invalidator must
		// recover durable unknown state before returning from this successful namespace mutation.
		path := ctxMountRecordPath(cname)
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Mkdir(path, 0o700)
	})
	swapSeam(t, &removeCtxMountSeam, func(*Options, string, string, liveCtxMount, time.Time) error {
		removed++
		namespaceHasMount = false
		return nil
	})

	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("record restore failure rc = %d, want restart-required 3\n%s", rc, buf.String())
	}
	if injected != 1 || !namespaceHasMount {
		t.Fatalf("namespace injections = %d, mount remains = %v; want one successful injection", injected, namespaceHasMount)
	}
	rec, err := loadCtxMountRecord(cname)
	if err != nil || rec.Token != "" || len(rec.Entries) != 0 {
		t.Fatalf("real recovery record = %+v, err %v; want an empty-token tombstone", rec, err)
	}
	envLines := strings.Split(strings.TrimSuffix(env, "\n"), "\n")
	if !ctxMountRecordInvalidated(cname, envLines) {
		t.Fatal("the inspected launch token trusted the recovered tombstone")
	}
	if _, ok := readCtxMountRecord(cname, envLines); ok {
		t.Fatal("the tombstone answered as the inspected jail's mount state")
	}
	out := buf.String()
	for _, want := range []string{"the mount succeeded, but its host record could not be updated", "is a directory", "at next restart"} {
		if !strings.Contains(out, want) {
			t.Errorf("record failure output does not contain %q:\n%s", want, out)
		}
	}
	if _, drifted, _, err := config.WorkspaceConfigDrift(o.Workspace); err != nil || !drifted {
		t.Fatalf("failed record update was folded: drifted=%v err=%v", drifted, err)
	}

	writeWorkspaceConfig(t, o.Workspace, `{}`)
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("later removal rc = %d, want restart-required 3\n%s", rc, buf.String())
	}
	if removed != 0 || !namespaceHasMount || !strings.Contains(buf.String(), "host invalidated this jail's mount record") {
		t.Fatalf("later removal trusted unknown namespace state (removals %d, mount remains %v):\n%s", removed, namespaceHasMount, buf.String())
	}
}

// A successful mount whose host record cannot be updated is not synchronized. The stale record is
// invalidated, the effective baseline stays pending, and a later removal waits for restart rather
// than trusting the record that predated the injected mount.
func TestConfigApplyRecordFailureInvalidatesMountStateAndKeepsRemovalPending(t *testing.T) {
	src := t.TempDir()
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
	rec := liveMounts(t)
	recordValid := true
	swapSeam(t, &readCtxMountRecordSeam, func(string, []string) ([]liveCtxMount, bool) {
		return append([]liveCtxMount(nil), (*rec)...), recordValid
	})
	swapSeam(t, &ctxMountsLiveSeam, func(*Options, string, string, []string) (bool, string) {
		if !recordValid {
			return false, "the host's live-mount record is unavailable"
		}
		return true, ""
	})
	swapSeam(t, &ctxMountRecordInvalidatedSeam, func(string, []string) bool { return !recordValid })
	invalidated := 0
	swapSeam(t, &invalidateCtxMountRecordSeam, func(string) error {
		invalidated++
		recordValid = false
		return nil
	})
	injected := 0
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error {
		injected++
		return nil
	})
	swapSeam(t, &writeCtxMountRecordSeam, func(string, string, []liveCtxMount) error {
		return errors.New("record disk is full")
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("record failure rc = %d, want restart-required 3\n%s", rc, buf.String())
	}
	if injected != 1 || invalidated != 2 {
		t.Fatalf("injections %d, invalidations %d; want one injection and invalidation before and after the failed record write", injected, invalidated)
	}
	if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace); !drifted {
		t.Fatal("a mount whose record failed was folded as applied")
	}
	if !strings.Contains(buf.String(), "record disk is full") || !strings.Contains(buf.String(), "at next restart") {
		t.Fatalf("the failed record was not reported as pending:\n%s", buf.String())
	}

	writeWorkspaceConfig(t, o.Workspace, `{}`)
	answer(o, "y")
	removed := 0
	swapSeam(t, &removeCtxMountSeam, func(*Options, string, string, liveCtxMount, time.Time) error {
		removed++
		return nil
	})
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("removal rc = %d, want restart-required 3\n%s", rc, buf.String())
	}
	if removed != 0 || !strings.Contains(buf.String(), "host invalidated this jail's mount record") {
		t.Fatalf("removal trusted invalid state (removed %d):\n%s", removed, buf.String())
	}
}

func TestConfigApplyDoesNotMutateMountWhenRecordCannotBeInvalidated(t *testing.T) {
	src := t.TempDir()
	o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
	liveMounts(t)
	swapSeam(t, &invalidateCtxMountRecordSeam, func(string) error {
		return errors.New("record directory is read-only")
	})
	injected := 0
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error {
		injected++
		return nil
	})
	answer(o, "y")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want restart-required 3\n%s", rc, buf.String())
	}
	if injected != 0 || !strings.Contains(buf.String(), "could not invalidate the live-mount record before changing mounts") {
		t.Fatalf("mount changed without an invalidated host record (injections %d):\n%s", injected, buf.String())
	}
}

// A failed step reports, stays pending (no fold), and the exit is 3; a step past its deadline
// is at next restart with the reason. The step is handed the deadline and ends by it
// (TestConfigApplyBoundsEveryCallOfAMountStep runs the real driver against it).
func TestConfigApplyAFailedOrSlowMountStaysPending(t *testing.T) {
	for _, c := range []struct {
		name   string
		inject func(*Options, string, string, configCtxMount, time.Time) error
		want   string
	}{
		{"fails", func(*Options, string, string, configCtxMount, time.Time) error {
			return errors.New("move_mount: EPERM")
		},
			"move_mount: EPERM"},
		{"slow", func(_ *Options, _, _ string, _ configCtxMount, deadline time.Time) error {
			if left := time.Until(deadline); left <= 0 || left > 50*time.Millisecond {
				return errors.New("handed no step deadline")
			}
			time.Sleep(time.Until(deadline))
			return errors.New("the mount step did not finish within 50ms")
		}, "did not finish within"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := t.TempDir()
			o, buf := applyFixture(t, `{}`, `{"mounts":["`+src+`"]}`, applyJail{running: true})
			liveMounts(t)
			swapSeam(t, &applyStepDeadline, 50*time.Millisecond)
			swapSeam(t, &injectCtxMountSeam, c.inject)
			answer(o, "y")
			if rc := o.configApply(false); rc != 3 {
				t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
			}
			out := buf.String()
			if !strings.Contains(out, "at next restart") || !strings.Contains(out, c.want) {
				t.Errorf("want at next restart naming %q:\n%s", c.want, out)
			}
			if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace); !drifted {
				t.Error("a key with a failed element was folded")
			}
		})
	}
}

// A read-write mount add carries the launch's disclosure line in the outcome block.
func TestConfigApplyDisclosesAReadWriteMount(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	liveMounts(t)
	src := t.TempDir()
	swapSeam(t, &diffCtxMountsSeam, func([]liveCtxMount, []configCtxMount) ([]configCtxMount, []liveCtxMount) {
		return []configCtxMount{{source: src, dest: "/ctx/rw", rw: true}}, nil
	})
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error { return nil })
	answer(o, "y")
	o.configApply(false)
	if !strings.Contains(buf.String(), "Read-write mount:") {
		t.Errorf("a read-write add must carry the launch's disclosure:\n%s", buf.String())
	}
}

// GitHub scope: with every live broker file taking live scope, the apply replaces Repos with
// what the gate approved, on each file, and says applied now.
func TestConfigApplyReplacesTheLiveScope(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	if _, err := config.WriteWorkspaceBootBaseline(o.Workspace, jsonx.NewOrderedMap()); err != nil {
		t.Fatal(err)
	}
	writeUserConfigOf(t, gbOn())
	answer(o, "y")
	f := liveScopeFile{Path: "/scope/1", LiveScope: true,
		File: brokerscope.File{Source: "gbsrc", Repos: []string{"o/old"}}}
	swapSeam(t, &liveScopeFilesSeam, func(_ context.Context, source, _, _ string) ([]liveScopeFile, error) {
		if source != "gbsrc" {
			return nil, nil
		}
		return []liveScopeFile{f}, nil
	})
	var replaced []string
	swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, path string, _ brokerscope.File, repos []string, _ []brokerscope.Grant) error {
		if path != "/scope/1" {
			t.Errorf("replaced %s", path)
		}
		replaced = repos
		return nil
	})
	rc := o.liveApply(withUserReadGrant(gbOn()), "podman", "yolo-ws", nil, applyMode{running: true})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if len(replaced) != 1 || replaced[0] != "o/r" {
		t.Errorf("the live file's repos became %v, want the approved [o/r]", replaced)
	}
	if !strings.Contains(buf.String(), "gb repository scope") || !strings.Contains(buf.String(), "applied now") {
		t.Errorf("no applied-now scope line:\n%s", buf.String())
	}
}

// A remote-url scope compares exactly (docs/design/git-broker-plan.md ID-2): a running broker's
// file holding a URL that differs from the approved one only in case is a change, so the apply
// names it and rewrites the file, rather than reading it as "already matches" and leaving the
// old spelling admitted.
func TestConfigApplyReplacesACaseOnlyRemoteURLChange(t *testing.T) {
	o, buf, _ := brokeredFixtureOn(t, true, "*")
	if _, err := config.WriteWorkspaceBootBaseline(o.Workspace, jsonx.NewOrderedMap()); err != nil {
		t.Fatal(err)
	}
	writeUserConfigOf(t, gbOn())
	answer(o, "y")
	f := liveScopeFile{Path: "/scope/1", LiveScope: true, File: brokerscope.File{Source: "gbsrc",
		Form: brokerscope.FormRemoteURL, Repos: []string{"git@github.com:O/R.git"}}}
	swapSeam(t, &liveScopeFilesSeam, func(_ context.Context, source, _, _ string) ([]liveScopeFile, error) {
		if source != "gbsrc" {
			return nil, nil
		}
		return []liveScopeFile{f}, nil
	})
	var replaced []string
	swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, _ string, _ brokerscope.File, repos []string, _ []brokerscope.Grant) error {
		replaced = repos
		return nil
	})
	if rc := o.liveApply(withUserReadGrant(gbOn()), "podman", "yolo-ws", nil, applyMode{running: true}); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if len(replaced) != 1 || replaced[0] != "git@github.com:o/r.git" {
		t.Errorf("the live file's repos became %v, want the approved [git@github.com:o/r.git]", replaced)
	}
	if !strings.Contains(buf.String(), "gb repository scope") || strings.Contains(buf.String(), "already matches") {
		t.Errorf("no scope change line for a case-only URL change:\n%s", buf.String())
	}
}

// `yolo config apply` replaces a running launch's grants as it replaces its repositories
// (docs/design/git-broker-plan.md ID-10): a grant the gate approves that the live file lacks is a
// scope change, named on its line, and the seam writes the approved grants.
func TestConfigApplyReplacesGrants(t *testing.T) {
	o, buf, _ := brokeredFixtureGrants(t)
	if _, err := config.WriteWorkspaceBootBaseline(o.Workspace, jsonx.NewOrderedMap()); err != nil {
		t.Fatal(err)
	}
	user := gbOn()
	brokered, _ := grantKindsConfig("gbsrc", "create-branch").Get("brokered")
	user.Set("brokered", brokered)
	writeUserConfigOf(t, user)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`)
	answer(o, "y")
	f := liveScopeFile{Path: "/scope/1", LiveScope: true, File: brokerscope.File{Source: "gbsrc",
		Form: brokerscope.FormRemoteURL, Repos: []string{"git@github.com:o/r.git"}}}
	swapSeam(t, &liveScopeFilesSeam, func(_ context.Context, source, _, _ string) ([]liveScopeFile, error) {
		if source != "gbsrc" {
			return nil, nil
		}
		return []liveScopeFile{f}, nil
	})
	var replaced []brokerscope.Grant
	called := false
	swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, _ string, _ brokerscope.File, _ []string, grants []brokerscope.Grant) error {
		called, replaced = true, grants
		return nil
	})
	// The config as the gate's read composes it, which the apply requires its cfg to be.
	read, err := config.ReadWorkspaceForGate(o.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := config.ComposeWithWorkspaceRead(o.Workspace, read.Config)
	if err != nil {
		t.Fatal(err)
	}
	if rc := o.liveApply(merged, "podman", "yolo-ws", nil, applyMode{running: true}); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	want := []brokerscope.Grant{{Kind: "read", Repo: "*"}, {Kind: "create-branch", Repo: "github.com/o/r"}}
	if !called || !reflect.DeepEqual(replaced, want) {
		t.Fatalf("the live file's grants became %v (called %v), want %v", replaced, called, want)
	}
	if !strings.Contains(buf.String(), "+grant create-branch github.com/o/r") || !strings.Contains(buf.String(), "applied now") {
		t.Errorf("no applied-now line naming the grant:\n%s", buf.String())
	}
}

// A broker that cannot take live scope: recorded, at next restart.
func TestConfigApplyScopeOnAnOldBrokerWaits(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	if _, err := config.WriteWorkspaceBootBaseline(o.Workspace, jsonx.NewOrderedMap()); err != nil {
		t.Fatal(err)
	}
	writeUserConfigOf(t, gbOn())
	answer(o, "y")
	swapSeam(t, &liveScopeFilesSeam, func(_ context.Context, _, _, _ string) ([]liveScopeFile, error) {
		return []liveScopeFile{{Path: "/scope/1", File: brokerscope.File{Repos: nil}}}, nil
	})
	swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, _ string, _ brokerscope.File, _ []string, _ []brokerscope.Grant) error {
		t.Error("an old broker's file was rewritten")
		return nil
	})
	if rc := o.liveApply(withUserReadGrant(gbOn()), "podman", "yolo-ws", nil, applyMode{running: true}); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 1 {
		t.Errorf("the scope was not recorded: %v", got)
	}
}

// AN ATTACH WITH NO TERMINAL asks nothing, records nothing, and prints one notice naming
// `yolo config apply` and `yolo config drift` when a change is pending (LA-D9).
func TestAttachWithoutATerminalPrintsTheNotice(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	before, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	merged, _ := o.loadAndValidateConfig()
	buf.Reset()
	o.applyAtAttach("yolo-ws", "podman", merged, nil)
	out := buf.String()
	if strings.Contains(out, "[y/N]") {
		t.Errorf("a non-interactive attach asked:\n%s", out)
	}
	for _, want := range []string{"yolo config apply", "yolo config drift"} {
		if !strings.Contains(out, want) {
			t.Errorf("the notice does not name %q:\n%s", want, out)
		}
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("the notice must be one line:\n%s", out)
	}
	if now, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !bytes.Equal(before, now) {
		t.Error("a non-interactive attach recorded an approval")
	}

	// Nothing pending: nothing said.
	o2, buf2 := applyFixture(t, `{"packages":["jq"]}`, `{"packages":["jq"]}`, applyJail{running: true})
	merged2, _ := o2.loadAndValidateConfig()
	buf2.Reset()
	o2.applyAtAttach("yolo-ws", "podman", merged2, nil)
	if buf2.String() != "" {
		t.Errorf("an attach with nothing pending said something:\n%s", buf2.String())
	}
}

// AN ATTACH AT A TERMINAL asks; an N records nothing, and its lines say the session starts and
// name `yolo config apply`, never "Exiting." (LA-D14).
func TestAttachAtATerminalDeclineStillStarts(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	before, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	merged, _ := o.loadAndValidateConfig()
	answer(o, "n")
	o.applyAtAttach("yolo-ws", "podman", merged, nil)
	out := buf.String()
	if !strings.Contains(out, "[y/N]") {
		t.Fatalf("an interactive attach did not ask:\n%s", out)
	}
	if strings.Contains(out, "Exiting.") {
		t.Errorf("a declined attach said it exits:\n%s", out)
	}
	for _, want := range []string{"yolo config apply", "session starts"} {
		if !strings.Contains(out, want) {
			t.Errorf("the decline does not say %q:\n%s", want, out)
		}
	}
	if now, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !bytes.Equal(before, now) {
		t.Error("a declined attach recorded an approval")
	}
}

// An approval already recorded (by --record-only, or `yolo check --accept-config-changes`) is
// applied at an interactive attach with no second question.
func TestAttachAppliesAnAlreadyRecordedApproval(t *testing.T) {
	o, buf := applyFixture(t, `{"agents_md_extra":"one"}`, `{"agents_md_extra":"two"}`, applyJail{running: true})
	cur, _ := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
	if err := config.RecordApproval(o.Workspace, cur, nil); err != nil {
		t.Fatal(err)
	}
	merged, _ := o.loadAndValidateConfig()
	answer(o, "")
	o.applyAtAttach("yolo-ws", "podman", merged, nil)
	out := buf.String()
	if strings.Contains(out, "[y/N]") {
		t.Errorf("an already-approved change was asked about again:\n%s", out)
	}
	if !strings.Contains(out, "agents_md_extra: from your next yolo session") {
		t.Errorf("the recorded change was not applied:\n%s", out)
	}
}

// The call-site pin (AGENTS.md: a test that pins the callee while the call site is unpinned is
// not a test): attachExisting calls applyAtAttach once, after it counts the session and before
// it refreshes the briefings.
func TestAttachExistingRunsTheApplyGate(t *testing.T) {
	fn := methodDecl(t, "run.go", "attachExisting")
	var hook, session, refresh []int
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "applyAtAttach":
				hook = append(hook, int(call.Pos()))
			case "holdSessionLock":
				session = append(session, int(call.Pos()))
			case "refreshJailBriefings":
				refresh = append(refresh, int(call.Pos()))
			}
		}
		return true
	})
	if len(hook) != 1 {
		t.Fatalf("attachExisting calls applyAtAttach %d times, want once", len(hook))
	}
	if len(session) == 0 || len(refresh) == 0 || !(session[0] < hook[0] && hook[0] < refresh[0]) {
		t.Errorf("applyAtAttach must run after the session count and before the briefing refresh")
	}
}

// The command reaches the apply through the registered entry, which parses its flags.
func TestConfigApplyEntryParsesItsFlags(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := ConfigApply([]string{"--help"}, &out, &errw, false); rc != 0 ||
		!strings.Contains(out.String(), "--record-only") {
		t.Errorf("--help: rc=%d\n%s", rc, out.String())
	}
	out.Reset()
	if rc := ConfigApply([]string{"--bogus"}, &out, &errw, false); rc != 2 {
		t.Errorf("an unknown flag: rc=%d, want 2", rc)
	}
}

// `yolo host-run -- yolo config apply` (docs/reference/host-run-mailbox.md): the host terminal is
// the command's stdin, and its stdout and stderr are pipes host-run copies to that terminal as
// they arrive. A human is there to answer, so the apply asks, and a y records the change. The
// owner's first run of exactly this refused "no terminal" over a question it could have asked.
// A launch with the same streams still refuses: its stderr is the launch stream, and a
// redirected one is a log nobody is reading.
func TestConfigApplyAsksWhenOnlyStdinIsATerminal(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	o.IsTTYStdin = func() bool { return true }
	o.IsTTYStderr = func() bool { return false }
	o.Stdin = strings.NewReader("y\n")
	if rc := o.configApply(false); rc == 1 {
		t.Fatalf("rc = 1, want the apply to ask and approve:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Accept these workspace config changes? [y/N]") {
		t.Errorf("the apply did not ask its question:\n%s", buf.String())
	}
	if _, err := os.Stat(config.ApprovalSnapshotPath(o.Workspace)); err != nil {
		t.Errorf("a y under host-run recorded nothing: %v\n%s", err, buf.String())
	}

	l, lbuf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{})
	l.IsTTYStdin = func() bool { return true }
	l.IsTTYStderr = func() bool { return false }
	l.Stdin = strings.NewReader("y\n")
	merged, ok := l.loadAndValidateConfig()
	if !ok {
		t.Fatalf("load failed:\n%s", lbuf.String())
	}
	if l.checkConfigChanges(merged, "podman") {
		t.Errorf("a launch with a redirected stderr asked its question:\n%s", lbuf.String())
	}
}

// With no terminal at all the apply refuses, exits non-zero, records nothing, and names its own
// next step: running `yolo config apply` where it can ask. It never offers the launch's
// --accept-config-changes "for THIS LAUNCH ONLY", since there is no launch to re-run.
func TestConfigApplyRefusalNamesTheApplysNextStep(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{"packages":["jq"]}`, applyJail{running: true})
	before, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace))
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStderr = func() bool { return false }
	if rc := o.configApply(false); rc != 1 {
		t.Fatalf("rc = %d, want 1 for a change nobody approved:\n%s", rc, buf.String())
	}
	out := buf.String()
	if strings.Contains(out, config.AcceptConfigChangesFlag) || strings.Contains(out, "THIS LAUNCH") {
		t.Errorf("the apply's refusal offers the launch's approval:\n%s", out)
	}
	for _, want := range []string{"`yolo config apply` has no terminal", config.ApplyRetryStep(o.Workspace)} {
		if !strings.Contains(out, want) {
			t.Errorf("the apply's refusal does not say %q:\n%s", want, out)
		}
	}
	if now, _ := os.ReadFile(config.ApprovalSnapshotPath(o.Workspace)); !bytes.Equal(before, now) {
		t.Error("a refused apply rewrote the approval record")
	}
}
