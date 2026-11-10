package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func unsetContainersConfOverride(t *testing.T) {
	t.Helper()
	t.Setenv(ContainersConfOverrideEnv, "")
	_ = os.Unsetenv(ContainersConfOverrideEnv)
}

// An image that bakes the required file gets the override, in the process env (so podman,
// a child of the session, reads it) and in e.Vars.
func TestExportPodmanRequiredConfPointsTheOverrideAtTheBakedFile(t *testing.T) {
	unsetContainersConfOverride(t)
	path := filepath.Join(t.TempDir(), "yolo-required.conf")
	if err := os.WriteFile(path, []byte("[containers]\ncgroups = \"disabled\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{})
	exportPodmanRequiredConfFrom(e, path)
	exportPodmanRequiredConfFrom(e, path) // a session's pass re-runs the step
	if got := e.Getenv(ContainersConfOverrideEnv); got != path {
		t.Errorf("e.Vars %s = %q, want %q", ContainersConfOverrideEnv, got, path)
	}
	if got := os.Getenv(ContainersConfOverrideEnv); got != path {
		t.Errorf("process %s = %q, want %q", ContainersConfOverrideEnv, got, path)
	}
}

// The minimal image has no /etc/containers: podman refuses to start when the override names a
// missing file, so no variable at all.
func TestExportPodmanRequiredConfSkipsAnImageWithoutTheFile(t *testing.T) {
	for name, path := range map[string]string{
		"missing":   filepath.Join(t.TempDir(), "absent.conf"),
		"directory": t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			unsetContainersConfOverride(t)
			e := NewEnv(map[string]string{})
			exportPodmanRequiredConfFrom(e, path)
			if v, ok := e.Vars[ContainersConfOverrideEnv]; ok {
				t.Errorf("exported %s=%q for an image without the file", ContainersConfOverrideEnv, v)
			}
			if v, ok := os.LookupEnv(ContainersConfOverrideEnv); ok {
				t.Errorf("process %s=%q, want unset", ContainersConfOverrideEnv, v)
			}
		})
	}
}

// An override someone set on purpose is kept, and the boot says what that costs and what to do.
func TestExportPodmanRequiredConfKeepsAnOverrideSetOnPurposeAndSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yolo-required.conf")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	theirs := "/home/agent/my-override.conf"
	t.Setenv(ContainersConfOverrideEnv, theirs)
	var out bytes.Buffer
	e := NewEnv(map[string]string{ContainersConfOverrideEnv: theirs})
	e.Stderr = &out
	exportPodmanRequiredConfFrom(e, path)
	if got := os.Getenv(ContainersConfOverrideEnv); got != theirs {
		t.Errorf("process %s = %q, want the user's %q kept", ContainersConfOverrideEnv, got, theirs)
	}
	if msg := out.String(); !strings.Contains(msg, theirs) || !strings.Contains(msg, path) {
		t.Errorf("warning %q does not name both files", msg)
	}
}

const testRequiredPodmanConf = "[containers]\ncgroups = \"disabled\"\n[engine]\ncgroup_manager = \"cgroupfs\"\nevents_logger = \"file\"\n"

// foreignOverrideWarning runs the export with an override already pointing at a foreign file
// holding body, and returns what the boot printed and the variable it left.
func foreignOverrideWarning(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	required := filepath.Join(dir, "yolo-required.conf")
	theirs := filepath.Join(dir, "theirs.conf")
	for p, b := range map[string]string{required: testRequiredPodmanConf, theirs: body} {
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(ContainersConfOverrideEnv, theirs)
	var out bytes.Buffer
	e := NewEnv(map[string]string{ContainersConfOverrideEnv: theirs})
	e.Stderr = &out
	exportPodmanRequiredConfFrom(e, required)
	return out.String(), os.Getenv(ContainersConfOverrideEnv)
}

// A foreign override that already carries every required key, among keys of its own, is
// compliant: no warning, since there is nothing for its reader to do.
func TestExportPodmanRequiredConfIsQuietForACompliantForeignOverride(t *testing.T) {
	msg, kept := foreignOverrideWarning(t, testRequiredPodmanConf+"tmp_dir = \"/tmp/x\"\n[network]\ndns_servers = [\"1.1.1.1\"]\n")
	if msg != "" {
		t.Errorf("a compliant override drew a warning: %q", msg)
	}
	if !strings.HasSuffix(kept, "theirs.conf") {
		t.Errorf("the override became %q; the user's file must be kept", kept)
	}
}

// One missing key and one wrong value are each named, and the advice names verbs.
func TestExportPodmanRequiredConfNamesTheKeyAForeignOverrideLacks(t *testing.T) {
	msg, _ := foreignOverrideWarning(t, "[containers]\ncgroups = \"enabled\"\n[engine]\ncgroup_manager = \"cgroupfs\"\n")
	for _, want := range []string{
		"[engine] events_logger is missing",
		"[containers] cgroups is enabled, not disabled",
		"yolo config why env.set.CONTAINERS_CONF_OVERRIDE",
		"yolo config add env.unset",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning %q does not say %q", msg, want)
		}
	}
	if strings.Contains(msg, "cgroup_manager") {
		t.Errorf("warning %q names cgroup_manager, which the override sets correctly", msg)
	}
}

// A foreign override that is not TOML is said to be unreadable rather than judged.
func TestExportPodmanRequiredConfSaysAnUnparseableOverrideIsUnreadable(t *testing.T) {
	msg, _ := foreignOverrideWarning(t, "this is [not toml\n")
	if !strings.Contains(msg, "could not be read as TOML") {
		t.Errorf("warning %q does not say the override could not be read", msg)
	}
}

// THE CALL SITE: the container boot runs the export, on a session's pass too, and the
// production path is the file flake.nix bakes. MUTATION: delete the step, swap its body, or
// give it a notSessionPass, and this fails.
func TestExportPodmanRequiredConfStepIsWired(t *testing.T) {
	s := mustBootStep(t, "export_podman_required_conf")
	if !isRun(s, exportPodmanRequiredConfStep) {
		t.Fatal("export_podman_required_conf no longer runs exportPodmanRequiredConfStep — a stale " +
			"user containers.conf then turns cgroups back on and every nested `podman run` fails")
	}
	if s.excludedFrom(bootContainer) != "" || s.notSessionPass != "" {
		t.Errorf("export_podman_required_conf must run on every container pass (excluded %q, notSessionPass %q): "+
			"an exec's env is the container's, not the boot's", s.excludedFrom(bootContainer), s.notSessionPass)
	}
	flake, err := os.ReadFile(filepath.Join(repoRoot(t), "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(flake), "cat > $out"+ImagePodmanRequiredConf+" <<") {
		t.Errorf("flake.nix no longer bakes %s; the boot would then export nothing", ImagePodmanRequiredConf)
	}
}
