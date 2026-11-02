package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// journalGrantFixture makes the shipped set one grant-declaring `journal` pack (as T3's check
// test does) and a user config selecting it with `userAllow` appended. It returns a workspace.
func journalGrantFixture(t *testing.T, userAllow string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	shipped := packload.EmbeddedFS()
	packload.ReleaseEmbedded()
	t.Cleanup(func() {
		packload.ReleaseEmbedded()
		packload.SetEmbeddedFS(shipped)
	})
	packload.SetEmbeddedFS(fstest.MapFS{
		"journal/pack.toml": {Data: []byte("name = \"journal\"\n\n[[loophole]]\nfrom = \"loopholes/journal\"\n")},
		"journal/loopholes/journal/loophole.toml": {Data: []byte(`name = "journal"
description = "a grant-declaring fixture"
version = 1
transport = "loopback-tls"
access = "read the host's journal"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "journal", "--socket", "{socket}"]
publishes = "socket"

[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
`)},
	})
	// The journal's module is recorded as staging records it, so the test does not lean on the
	// lazy resolver that run's init() registers: sections_loopholes_test.go's isolatedModuleDir
	// clears it on cleanup, and every check test after it would then see no pack loophole.
	restore := loopholes.SnapshotPackModules()
	t.Cleanup(restore)
	var module []loopholes.PackModule
	for _, p := range packload.Embedded() {
		if p.Name == "journal" {
			module = append(module, loopholes.PackModule{Dir: filepath.Join(p.Root, "loopholes", "journal"),
				Pack: "journal", HostExecApproved: true})
		}
	}
	if len(module) != 1 {
		t.Fatalf("the fixture's journal pack is not in the shipped set: %v", module)
	}
	loopholes.SetPackModules(module)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, paths.UserConfigTOMLName), []byte("packs = ['journal']\n"+userAllow), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

// runCheckOn is runCheckOverStandardWorkspaceConfig over a chosen runtime ("podman" or
// "container"), optionally recording the approval as `--accept-config-changes` does.
func runCheckOn(t *testing.T, workspace, runtime, body string, accept bool) string {
	t.Helper()
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Workspace = workspace
	opts.AcceptConfigChanges = accept
	if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for name, contents := range map[string]string{"flake.nix": "{}", "go.mod": "module test\n", "flake.lock": "{}"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
	}
	opts.PathExists = func(path string) bool {
		if path == "/nix" || strings.HasPrefix(path, "/nix/") {
			return true
		}
		_, err := os.Stat(path)
		return err == nil
	}
	opts.Getenv = func(key string) string {
		if key == "YOLO_RUNTIME" {
			return runtime
		}
		return ""
	}
	opts.LookPath = func(name string) (string, bool) {
		switch name {
		case runtime, "nix", "python3":
			return "/usr/bin/" + name, true
		default:
			return "", false
		}
	}
	podmanAnswers(&opts, ExecResult{Stdout: "{}", Ran: true, RC: 0})
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version":        {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"container --version":     {Stdout: "container CLI version 0.5.0", Ran: true, RC: 0},
		"container system status": {Stdout: "apiserver is running", Ran: true, RC: 0},
		"nix --version":           {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"nix config show":         {Stdout: "", Ran: true, RC: 0},
		"python3":                 {Stdout: "ok", Ran: true, RC: 0},
		"podman images":           {Stdout: "", Ran: true, RC: 0},
		"podman ps":               {Stdout: "", Ran: true, RC: 0},
	})
	Check(opts)
	return stripANSI(out.String())
}

// `yolo check --accept-config-changes` collects the grants it records through the launch's own
// collector (run.CollectScopeCheck, the check's collectScopeCheck), shows them before it records,
// and records them in the approval record's grant part, so the next launch's gate asks nothing.
// The launch's half of this pin is the test of the same name in internal/cli/run.
func TestCheckAndLaunchCollectTheSameGrantSources(t *testing.T) {
	ws := journalGrantFixture(t, "\n[allow.journal]\nalways = [\"read\"]\n")
	calls := 0
	orig := collectScopeCheck
	t.Cleanup(func() { collectScopeCheck = orig })
	collectScopeCheck = func(in run.ScopeInput) *config.ScopeCheck {
		calls++
		return orig(in)
	}
	out := runCheckOn(t, ws, "podman", "", true)
	if calls == 0 {
		t.Fatalf("yolo check never called the launch's collector:\n%s", out)
	}
	for _, want := range []string{
		"Loophole grants changed since the last approval; recording them as approved:",
		"+ journal: read your user journal (from your user config, for every project)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("yolo check does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(config.ApprovalLoopholeGrantsPath(ws)); err != nil {
		t.Fatalf("no grant part recorded: %v\n%s", err, out)
	}
	if got := config.RecordedLoopholeGrants(ws)["journal"]; len(got) != 1 || got[0] != "read" {
		t.Fatalf("recorded grants %v, want [read]", got)
	}
}

// On Apple Container the journal does not run, so a project's request for its grants stays out of
// the config-change gate (LG-D10): `yolo check` names it inert in the inert report's words, and the
// gate's own report has no grant line.
func TestAnInertLoopholesRequestStaysOutOfTheGate(t *testing.T) {
	ws := journalGrantFixture(t, "\n[allow.journal]\non_request = [\"read_all\"]\n")
	body := "[requests.journal]\nread_all = true\n"
	out := runCheckOn(t, ws, "container", body, false)
	if !strings.Contains(out, "journal: this project requests read_all, which the config-change gate does not show:") ||
		!strings.Contains(out, "It grants nothing here, and nothing is missing to install.") {
		t.Errorf("yolo check does not name the inert request:\n%s", out)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	scope := brokeredScopeForCheck(ws, nil, "container", read)
	if scope != nil && len(scope.LoopholeGrants) != 0 {
		t.Fatalf("an inert loophole's grants reached the gate: %+v", scope.LoopholeGrants)
	}
	rep, err := config.ScopeChanges(ws, scope)
	if err != nil || rep.GrantsChanged || len(rep.GrantBlock) != 0 {
		t.Fatalf("the gate shows a grant line for an inert loophole: %+v err=%v", rep, err)
	}
	// On podman the same request is in the gate.
	podman := runCheckOn(t, ws, "podman", body, false)
	if strings.Contains(podman, "which the config-change gate does not show") {
		t.Errorf("a running loophole's request was called inert:\n%s", podman)
	}
}

// A grant `yolo config migrate` carried into the approval record from a retired switch file, which
// no file of the project requests: the grant stands, and `yolo check` names it with the `yolo
// request` line that writes it (docs/design/loophole-access-approval.md §6, LG-D11). Once the
// project's file states the request, the note goes.
func TestCheckReportsARecordGrantTheProjectFileDoesNotState(t *testing.T) {
	ws := journalGrantFixture(t, "")
	if err := config.CarryLoopholeGrants(ws, map[string][]string{"journal": {"read"}}); err != nil {
		t.Fatal(err)
	}
	out := runCheckOn(t, ws, "podman", "", false)
	for _, want := range []string{
		"the approval record grants journal read to this project, carried from `yolo loopholes enable` by `yolo config migrate`, but this project's files do not request it.",
		"yolo request journal read",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("yolo check does not say %q:\n%s", want, out)
		}
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	scope := brokeredScopeForCheck(ws, nil, "podman", read)
	if got := scope.LoopholeGrantStandings(ws)["journal"]["read"].Standing; got != config.LoopholeGrantGranted {
		t.Errorf("the carried grant does not stand: standing %q", got)
	}
	out = runCheckOn(t, ws, "podman", "[requests.journal]\nread = true\n", false)
	if strings.Contains(out, "carried from `yolo loopholes enable`") {
		t.Errorf("yolo check still names a grant the project's file now states:\n%s", out)
	}
}
