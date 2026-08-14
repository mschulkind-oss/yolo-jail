package config

// The §4.3a placement rule was ruled and then implemented by nobody: a user-config
// entry `command: ["python3", "/workspace/tool.py"]` validated clean and spawned an
// agent-writable script on the host at every launch. G1 (scope) gates who may write
// the declaration; nothing looked at the target. These tests pin the target check.

import (
	"path/filepath"
	"strings"
	"testing"
)

// validateScopedIn is validateScoped with the workspace dir chosen by the caller,
// so a test can name a path INSIDE it in the config text.
func validateScopedIn(t *testing.T, ws, userJSON, wsJSON string, resolver LoopholeResolver) (errs, warns []string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	if wsJSON == "" {
		wsJSON = "{}"
	} else {
		write(t, filepath.Join(ws, WorkspaceConfigName), wsJSON)
	}
	if userJSON == "" {
		userJSON = "{}"
	}
	merged := MergeConfig(decode(t, userJSON), decode(t, wsJSON))
	return ValidateConfig(merged, ws, resolver)
}

// A user-scope install is a legitimate declaration — and still refused when the
// program it names lives in the workspace the jail mounts :rw. The user wrote the
// entry; the agent owns the file it points at.
func TestInlineLoopholeCommandInsideTheWorkspaceIsRefused(t *testing.T) {
	ws := t.TempDir()
	errs, _ := validateScopedIn(t, ws,
		`{"loopholes": {"svc": {"command": ["python3", "`+ws+`/tool.py"]}}}`, "", nil)
	hits := containing(errs, "config.loopholes.svc.command[1]")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want one placement refusal", errs)
	}
	for _, want := range []string{
		filepath.Join(ws, "tool.py"),
		"bind-mounts :rw",
		"may not live where an agent writes",
		"§4.3a",
	} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("refusal %q does not carry %q", hits[0], want)
		}
	}
}

// doctor_cmd is the second host execution, run by two read-only-looking commands,
// so it gets the same check as `command`.
func TestInlineLoopholeDoctorCmdInsideTheWorkspaceIsRefused(t *testing.T) {
	ws := t.TempDir()
	errs, _ := validateScopedIn(t, ws,
		`{"loopholes": {"svc": {"command": ["/usr/bin/daemon"], "doctor_cmd": ["`+ws+`/check.sh"]}}}`,
		"", nil)
	if hits := containing(errs, "config.loopholes.svc.doctor_cmd[0]", "§4.3a"); len(hits) != 1 {
		t.Fatalf("errors = %v, want one placement refusal for doctor_cmd", errs)
	}
}

// A relative program name resolves against the workspace, because the daemon is
// spawned with no explicit cwd and inherits yolo's — so "./tool.py" is the same
// hole spelled shorter.
func TestInlineLoopholeRelativeCommandResolvesAgainstTheWorkspace(t *testing.T) {
	ws := t.TempDir()
	errs, _ := validateScopedIn(t, ws,
		`{"loopholes": {"svc": {"command": ["python3", "./tool.py"]}}}`, "", nil)
	hits := containing(errs, "config.loopholes.svc.command[1]", "§4.3a")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want one placement refusal", errs)
	}
	if !strings.Contains(hits[0], filepath.Join(ws, "tool.py")) {
		t.Errorf("the refusal must name the resolved path: %q", hits[0])
	}
}

// The jail home tree is the other tree this launch hands an agent: it IS
// /home/agent inside the container, so a daemon parked there is agent-writable
// even though it is under the user's own home.
func TestInlineLoopholeCommandInsideTheJailHomeIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".local/share/yolo-jail/home", "tool.py")
	errs, _ := validateScopedIn(t, t.TempDir(),
		`{"loopholes": {"svc": {"command": ["python3", "`+target+`"]}}}`, "", nil)
	if hits := containing(errs, "config.loopholes.svc.command[1]", "jail home tree"); len(hits) != 1 {
		t.Fatalf("errors = %v, want one placement refusal naming the jail home tree", errs)
	}
}

// The shapes that must stay clean: a program outside both trees, a PATH lookup,
// the bundled ["yolo","internal","daemon",…] form, framework placeholders, and
// flags. A false positive here refuses a working loophole at every launch.
func TestPlacementRuleLeavesLegitimateInstallsAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	for _, cmd := range []string{
		`["yolo", "internal", "daemon", "host-processes", "--endpoint", "{endpoint}"]`,
		`["/usr/local/bin/mydaemon", "--socket", "{socket}"]`,
		`["python3", "` + filepath.Join(home, "tools/daemon.py") + `"]`,
		`["mydaemon", "--flag=a/b"]`,
	} {
		errs, _ := validateScopedIn(t, ws,
			`{"loopholes": {"svc": {"command": `+cmd+`}}}`, "", nil)
		if hits := containing(errs, "§4.3a"); len(hits) != 0 {
			t.Errorf("command %s drew a placement refusal: %v", cmd, hits)
		}
	}
}

// A `sh -c` script body is not a path, however many slashes it contains. Reading
// one as a relative path resolved against the workspace refuses a working daemon at
// every launch — the false positive that costs more than the miss.
func TestPlacementRuleDoesNotReadAScriptBodyAsAPath(t *testing.T) {
	ws := t.TempDir()
	errs, _ := validateScopedIn(t, ws,
		`{"loopholes": {"svc": {"command": ["sh", "-c", "sleep 300 & echo $! > /tmp/pid; cp seed/x \"$1\"", "svc"]}}}`,
		"", nil)
	if hits := containing(errs, "§4.3a"); len(hits) != 0 {
		t.Errorf("a script body drew a placement refusal: %v", hits)
	}
}

// A workspace file carrying the install already draws the G1 scope error, which
// refuses the whole entry and names the fix. Adding the placement refusal there
// would report one mistake twice.
func TestWorkspaceInstallDrawsTheScopeErrorOnly(t *testing.T) {
	ws := t.TempDir()
	errs, _ := validateScopedIn(t, ws, "",
		`{"loopholes": {"svc": {"command": ["python3", "`+ws+`/tool.py"]}}}`, nil)
	if hits := containing(errs, "user-scope only"); len(hits) != 1 {
		t.Fatalf("errors = %v, want the one scope error", errs)
	}
	if hits := containing(errs, "§4.3a"); len(hits) != 0 {
		t.Errorf("the scope error already refuses the entry; placement added %v", hits)
	}
}

// An OVERRIDE of a manifest-backed loophole cannot install anything, so the
// placement rule has nothing to say about it — its `command` is already refused as
// not overridable.
func TestPlacementRuleSkipsAnOverride(t *testing.T) {
	ws := t.TempDir()
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	errs, _ := validateScopedIn(t, ws,
		`{"loopholes": {"svc": {"command": ["python3", "`+ws+`/tool.py"]}}}`, "", resolver)
	if hits := containing(errs, "not overridable"); len(hits) != 1 {
		t.Fatalf("errors = %v, want the not-overridable error", errs)
	}
	if hits := containing(errs, "§4.3a"); len(hits) != 0 {
		t.Errorf("an override installs nothing; placement added %v", hits)
	}
}
