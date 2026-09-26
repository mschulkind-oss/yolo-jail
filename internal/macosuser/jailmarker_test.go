package macosuser

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// OQ-FT13 (docs/design/agent-footer.md): a macos-user session is at the jail notch, so the
// launch sets the marker every container launch sets, YOLO_VERSION, to the launcher's
// version. Without it config.InJail() answers "host" inside the Seatbelt sandbox, and the
// agent footer says `host` there.

// TestSandboxCarriesTheJailMarker: the session env file, the one channel that reaches the
// agent and the provisioning stage, exports YOLO_VERSION as the launcher's version.
func TestSandboxCarriesTheJailMarker(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // the launcher runs on the host, where no marker is set
	opts := newOpts("/Users/Shared/proj")
	plan := buildPlan(mockDeps(nil), opts, nil)
	want := version.Get(opts.RepoRoot)
	if want == "" {
		t.Fatal("version.Get returned nothing, so no marker could be set")
	}
	if !SandboxEnvFileSets(plan.EnvFileContent, "YOLO_VERSION", want) {
		t.Errorf("the session env file does not export YOLO_VERSION=%q; a macos-user footer would say "+
			"`host` inside the sandbox:\n%s", want, plan.EnvFileContent)
	}
}

// TestJailMarkerIsTheLaunchersLastWord: no composed layer can take the marker away, since it
// is the one fact every `yolo` in the sandbox asks "am I in a jail?" of. The composed channel
// (which carries the env_sources the credential gate let through, last) and the caller's own
// sandbox env both set it empty here, and the file still carries the launcher's value.
func TestJailMarkerIsTheLaunchersLastWord(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	opts := newOpts(ws)
	opts.PackEnv = jsonx.NewOrderedMap()
	opts.PackEnv.Set("YOLO_VERSION", "")
	opts.SandboxEnv = jsonx.NewOrderedMap()
	opts.SandboxEnv.Set("YOLO_VERSION", "")
	var out bytes.Buffer
	deps := mockDeps(nil)
	deps.Out = &out
	plan := buildPlan(deps, opts, nil)
	if want := version.Get(opts.RepoRoot); !SandboxEnvFileSets(plan.EnvFileContent, "YOLO_VERSION", want) {
		t.Errorf("a composed layer replaced the jail marker; env file:\n%s", plan.EnvFileContent)
	}
	if n := strings.Count(plan.EnvFileContent, "export YOLO_VERSION="); n != 1 {
		t.Errorf("the env file exports YOLO_VERSION %d times, want once:\n%s", n, plan.EnvFileContent)
	}
}

// TestJailMarkerReachesTheSandboxedProcess runs the launch argv's own env-file reader
// (ExecWithEnvFile) over the rendered file, under `env -i` as the launch does, and asks the
// wrapped process what YOLO_VERSION is. It is the half a plan inspection cannot show: that
// the value in the file is the value the agent's process sees.
func TestJailMarkerReachesTheSandboxedProcess(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	opts := newOpts("/Users/Shared/proj")
	plan := buildPlan(mockDeps(nil), opts, nil)
	file := filepath.Join(t.TempDir(), "session.env")
	if err := os.WriteFile(file, []byte(plan.EnvFileContent), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := ExecWithEnvFile(file, []string{"/bin/sh", "-c", `printf %s "$YOLO_VERSION"`})
	cmd := exec.Command("/usr/bin/env", append([]string{"-i"}, argv...)...)
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the env-file reader: %v", err)
	}
	if want := version.Get(opts.RepoRoot); string(got) != want {
		t.Errorf("the sandboxed process sees YOLO_VERSION=%q, want %q", got, want)
	}
}

// TestDryRunPlanNamesTheJailMarker: the dry run names every variable the env file sets, so
// the marker is in the printed list, through the real front door (RunMacosUser).
func TestDryRunPlanNamesTheJailMarker(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	var out bytes.Buffer
	deps := mockDeps(nil)
	deps.Out = &out
	opts := newOpts("/Users/Shared/proj")
	opts.DryRun = true
	RunMacosUser(deps, opts)
	var keys string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "sets, values not shown:") {
			keys = line
		}
	}
	if !strings.Contains(keys, "YOLO_VERSION") {
		t.Errorf("the dry-run plan's env-file keys do not name YOLO_VERSION: %q\n%s", keys, out.String())
	}
}
