package run

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
)

// macosuserconfigartifacts_test.go pins `yolo config drift` and `yolo config dump` inside a
// macos-user sandbox. Both read files the launch writes into <workspace>/.yolo for the session —
// the frozen workspace baseline and the merged config — which the container arm writes and the
// macos-user arm did not, so in a sandbox drift answered "no boot baseline" and dump re-assembled
// under the sandbox account's empty home, without the host's user scope.

// macosUserConfigLaunch drives Run down the macos-user arm with a fake backend over a workspace
// config and a user config, and returns what the backend was handed.
func macosUserConfigLaunch(t *testing.T, dryRun bool) (ws string, got macosUserCall, stderr string) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws = floortest.ResolvedTemp(t)
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"packages": ["jq"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, errb bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &errb, nil)
	o.DryRun = dryRun
	// The workspace config is new, so its approval is granted as the flag grants it.
	o.AcceptConfigChanges = true
	o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int {
		got = c
		return 0
	})
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), errb.String())
	}
	return ws, got, errb.String()
}

// The launch writes both artifacts, as the container arm's fresh launch does; a dry run writes
// neither. Fails on a tree whose macos-user arm does not call writeLaunchConfigArtifacts.
func TestMacosUserLaunchWritesTheConfigArtifacts(t *testing.T) {
	ws, _, stderr := macosUserConfigLaunch(t, false)
	for _, p := range []string{config.WorkspaceConfigBootPath(ws), config.WorkspaceAssembledConfigPath(ws)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a macos-user launch wrote no %s (%v), so an in-sandbox `yolo config %s` "+
				"has nothing to read\nstderr:\n%s", filepath.Base(p), err,
				map[bool]string{true: "drift", false: "dump"}[strings.HasSuffix(p, "config-boot.json")], stderr)
		}
	}
}

func TestMacosUserDryRunWritesNoConfigArtifacts(t *testing.T) {
	ws, got, _ := macosUserConfigLaunch(t, true)
	for _, p := range []string{config.WorkspaceConfigBootPath(ws), config.WorkspaceAssembledConfigPath(ws)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a macos-user --dry-run wrote %s; a plan render launches nothing", p)
		}
	}
	if v, ok := got.packEnv.Get(config.BootBaselineDigestEnv); ok {
		t.Errorf("a macos-user --dry-run hands its plan a baseline digest %v it never wrote", v)
	}
}

// IN THE SANDBOX: the session env carries the digest of the baseline the launch wrote, and with
// the session's own variables — YOLO_VERSION and YOLO_WORKSPACE, which the backend sets, and the
// sandbox account's home, which holds no user config — drift is in sync and the config an
// in-sandbox `yolo config dump` reads is the merged config the launch used. The control, with no
// YOLO_WORKSPACE, re-assembles without the host's user scope.
func TestMacosUserSandboxReadsTheLaunchConfig(t *testing.T) {
	ws, got, _ := macosUserConfigLaunch(t, false)
	if got.cfg == nil || got.packEnv == nil {
		t.Fatal("the fake backend was not called")
	}
	baseline, err := os.ReadFile(config.WorkspaceConfigBootPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bytes.TrimRight(baseline, "\n"))
	digest, _ := got.packEnv.Get(config.BootBaselineDigestEnv)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("the session env's %s is %v, want the digest of the baseline the launch wrote (%x)",
			config.BootBaselineDigestEnv, digest, sum)
	}
	merged, err := config.SnapshotJSON(got.cfg)
	if err != nil {
		t.Fatal(err)
	}

	// The sandbox: its own empty home, and the variables the session env carries.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	t.Setenv("YOLO_WORKSPACE", ws)
	t.Setenv(config.BootBaselineDigestEnv, digest.(string))
	if _, drift, ok, err := config.WorkspaceConfigDrift(ws); err != nil || !ok || drift {
		t.Errorf("in-sandbox drift: drift=%v ok=%v err=%v, want in sync", drift, ok, err)
	}
	inSandbox, err := config.LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := config.SnapshotJSON(inSandbox); j != merged {
		t.Errorf("the in-sandbox config is not the merged config the launch used:\n got: %s\nwant: %s", j, merged)
	}

	// THE CONTROL: without the session naming its workspace, the read re-assembles under the
	// sandbox's home, and the user scope's `packs` is gone.
	t.Setenv("YOLO_WORKSPACE", "")
	reassembled, err := config.LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := config.SnapshotJSON(reassembled); j == merged {
		t.Error("the control re-assembled the merged config too, so the comparison above proves nothing")
	}
}
