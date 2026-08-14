package config

// The §4.3a placement rule was ruled and then implemented by nobody: a user-config
// entry `command: ["python3", "/workspace/tool.py"]` validated clean and spawned an
// agent-writable script on the host at every launch. G1 (scope) gates who may write
// the declaration; nothing looked at the target. These tests pin the target check.

import (
	"os"
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

// --- The MANIFEST faces (§4.3a landing item 1a, "still owed"): a manifest's own
// host_daemon.cmd and doctor_cmd, and the module DIR.

// The dir face. A module dir inside the mounted workspace is refused BY NAME, and
// the message has to say the refusal covers the whole module — otherwise the reader
// moves the one script the argv named and believes they are done.
func TestManifestModuleDirInsideTheWorkspaceIsRefused(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mod := filepath.Join(ws, "loopholes/acme")
	probs := LoopholeManifestPlacementProblems(LoopholeManifestPlacement{
		Name:          "acme",
		ModuleDir:     mod,
		HostDaemonCmd: []string{"python3", filepath.Join(mod, "srv.py")},
	}, ws)
	if len(probs) != 1 {
		t.Fatalf("problems = %v, want exactly one (the dir refusal subsumes the argv)", probs)
	}
	for _, want := range []string{
		"loophole 'acme'", "module dir " + mod, "bind-mounts :rw",
		"WHOLE module", "{loophole_dir}", "dlopen",
		"Install the loophole outside that tree", "§4.3a",
	} {
		if !strings.Contains(probs[0], want) {
			t.Errorf("refusal does not carry %q:\n  %s", want, probs[0])
		}
	}
}

// The jail home tree is the other tree, and it is the one that matters WITHOUT a
// workspace: the doctor path carries none, so "" must narrow the rule rather than
// disable it. A check that silently passes when it has no workspace is how the
// manifest faces stayed missing for a batch.
func TestManifestModuleDirInsideTheJailHomeIsRefusedWithNoWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mod := filepath.Join(home, ".local/share/yolo-jail/home", "loopholes/acme")
	probs := LoopholeManifestPlacementProblems(LoopholeManifestPlacement{
		Name: "acme", ModuleDir: mod,
	}, "")
	if hits := containing(probs, "jail home tree", "module dir"); len(hits) != 1 {
		t.Fatalf("problems = %v, want one refusal naming the jail home tree", probs)
	}
}

// A SYMLINKED module dir pointing into the workspace is refused. The trees are not
// symlink-resolved (deliberately), so the dir has to be tested in both spellings or
// the answer depends on which side happened to be canonical.
func TestManifestModuleDirThroughASymlinkIsRefused(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	real := filepath.Join(ws, "loopholes/acme")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "acme")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	probs := LoopholeManifestPlacementProblems(LoopholeManifestPlacement{
		Name: "acme", ModuleDir: link,
	}, ws)
	if hits := containing(probs, "module dir", "bind-mounts :rw"); len(hits) != 1 {
		t.Fatalf("problems = %v, want the symlink target refused", probs)
	}
}

// With the module dir outside both trees, the argv faces still apply on their own:
// a manifest whose daemon lives safely but whose doctor_cmd reaches into the
// workspace is the mixed case, and BOTH argvs are named separately because they have
// separate fixes.
func TestManifestArgvFacesApplyWhenTheModuleDirIsFine(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mod := filepath.Join(t.TempDir(), "acme")
	probs := LoopholeManifestPlacementProblems(LoopholeManifestPlacement{
		Name:          "acme",
		ModuleDir:     mod,
		HostDaemonCmd: []string{"python3", filepath.Join(mod, "srv.py"), "--socket", "{socket}"},
		DoctorCmd:     []string{"python3", filepath.Join(ws, "check.py")},
	}, ws)
	if hits := containing(probs, "doctor_cmd[1]", filepath.Join(ws, "check.py")); len(hits) != 1 {
		t.Fatalf("problems = %v, want the doctor_cmd refusal", probs)
	}
	if hits := containing(probs, "host_daemon.cmd"); len(hits) != 0 {
		t.Errorf("the daemon lives outside both trees; got %v", hits)
	}
	if hits := containing(probs, "loophole 'acme'"); len(hits) != 1 {
		t.Errorf("every manifest-face message must name the loophole: %v", probs)
	}
}

// A daemon argv reaching into the workspace from a legitimately-placed module dir is
// the other half of the mixed case.
func TestManifestHostDaemonCmdInsideTheWorkspaceIsRefused(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	probs := LoopholeManifestPlacementProblems(LoopholeManifestPlacement{
		Name:          "acme",
		ModuleDir:     filepath.Join(t.TempDir(), "acme"),
		HostDaemonCmd: []string{"python3", filepath.Join(ws, "tool.py")},
	}, ws)
	if hits := containing(probs, "host_daemon.cmd[1]", "Move the program outside that tree"); len(hits) != 1 {
		t.Fatalf("problems = %v, want the host_daemon.cmd refusal", probs)
	}
}

// The shapes that must stay clean, because a false positive refuses a working
// loophole at every launch: a bundled-shaped manifest, an empty manifest, and a
// module dir under the user's own home but outside the jail-home tree.
func TestManifestPlacementLeavesLegitimateLoopholesAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	for _, p := range []LoopholeManifestPlacement{
		{Name: "host-processes",
			ModuleDir:     filepath.Join(home, "tools/loopholes/host-processes"),
			HostDaemonCmd: []string{"yolo", "internal", "daemon", "host-processes", "--endpoint", "{endpoint}"},
			DoctorCmd:     []string{"yolo", "internal", "daemon", "host-processes", "--self-check"}},
		{Name: "acme"},
		{},
	} {
		if probs := LoopholeManifestPlacementProblems(p, ws); len(probs) != 0 {
			t.Errorf("%+v drew %v", p, probs)
		}
	}
}
