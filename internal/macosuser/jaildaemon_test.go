package macosuser

// jaildaemon_test.go pins the guest half of the loophole lifecycle (jaildaemon.go; OQ-DP8 and
// OQ-DP9 of docs/design/declaration-parity.md): the darwin in-jail binaries are staged into the
// sandbox's root-owned prefix, the declared daemon command reaches the supervisor VERBATIM, the
// supervisor runs UNDER the session's Seatbelt profile as the sandbox account, and it reads its
// caller tokens from a root-owned 0600 file only that account is granted read on.
//
// Each test drives the orchestrator (RunMacosUser) or the plan builder the orchestrator calls,
// so deleting the call site — BuildRunPlanWithDaemons' composition, buildPlan's Options pass,
// startJailDaemons' call before the agent — fails a test here.
//
// ⚠ NONE OF THIS HAS EXECUTED ON A MAC. What only a Mac can settle: that sandbox-exec admits
// the supervisor and its children under the session profile, that a darwin yolo-jaild
// cross-compiled on Linux is signed well enough to exec, that `sudo -n` relays the stop's
// SIGTERM, that the `user:` ACE really lets _yolojail read the 0600 file, that the log wrapper
// may create supervisor.log in the workspace overlay from inside the profile and the host user
// can read it through the inherited group ACE, and how long the chain takes to reach the
// supervisor's readiness line against supervisorReadyBound (JD-8).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// openAIAdapterDaemons is the payload the launch hands this backend for the shipped OpenAI
// refresh adapter, with its caller token and endpoint beside it — the daemon env's shape.
func openAIAdapterDaemons(src string) JailDaemons {
	env := jsonx.NewOrderedMap()
	env.Set(jailDaemonsEnv, `[{"name":"openai-auth-broker","cmd":["yolo-jaild","openai-auth-adapter","--listen","127.0.0.1:1460"],"restart":"on-failure"}]`)
	env.Set("YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN", strings.Repeat("ab", 32))
	env.Set("YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", "/private/tmp/yolo-host-services-x/openai-auth-broker.endpoint")
	return JailDaemons{Env: env, GuestBinSource: src}
}

func guestPlan(t *testing.T, jd JailDaemons) RunPlan {
	t.Helper()
	return BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"codex"},
		[]string{"codex"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), mockDarwin(), nil, jd, FloorStage{},
		PlanSession{})
}

// THE DECLARED COMMAND RUNS VERBATIM, INSIDE THE PROFILE (OQ-DP8's "runs exactly as declared",
// OQ-DP9's "confined"). The supervisor is `<GuestBinDir>/yolo-jaild supervise` behind
// `sandbox-exec -f <the session profile>`; the daemon's own argv reaches it unchanged in the
// payload, and resolves `yolo-jaild` on the PATH the argv sets, which carries GuestBinDir.
func TestTheGuestPlanStartsTheDeclaredDaemonVerbatimInsideTheSeatbeltProfile(t *testing.T) {
	plan := guestPlan(t, openAIAdapterDaemons("/opt/yolo/share/yolo-jail/bin/darwin-arm64"))
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a well-formed guest plan fails its invariants:\n%s", strings.Join(problems, "\n"))
	}
	argv := strings.Join(plan.JailDaemonArgv, " ")
	confined := "/usr/bin/sandbox-exec -f " + plan.ProfilePath + " -- "
	supervise := GuestBinaryPath(JaildName, "") + " supervise"
	ci, si := strings.Index(argv, confined), strings.Index(argv, supervise)
	if ci < 0 || si < 0 || si < ci {
		t.Fatalf("the supervisor is not started under the session's Seatbelt profile "+
			"(confined at %d, supervise at %d):\n%s", ci, si, argv)
	}
	if !strings.HasPrefix(argv, "sudo -n --set-home --user="+SandboxUser+" /usr/bin/env -i ") {
		t.Errorf("the supervisor does not run as the sandbox account through a non-interactive sudo:\n%s", argv)
	}
	// The PATH the supervisor (and so every daemon it execs) resolves names on.
	onPath := false
	for _, w := range plan.JailDaemonArgv {
		if v, ok := strings.CutPrefix(w, "PATH="); ok {
			for _, dir := range strings.Split(v, ":") {
				onPath = onPath || dir == GuestBinDir("")
			}
		}
	}
	if !onPath {
		t.Errorf("GuestBinDir is not on the supervisor's PATH, so the declared `yolo-jaild …` "+
			"argv would resolve nothing:\n%s", argv)
	}
	want := `export YOLO_JAIL_DAEMONS='[{"name":"openai-auth-broker","cmd":["yolo-jaild","openai-auth-adapter","--listen","127.0.0.1:1460"],"restart":"on-failure"}]'`
	if !strings.Contains(plan.DaemonEnvFileContent, want) {
		t.Errorf("the daemon env file does not carry the declared argv verbatim:\n%s", plan.DaemonEnvFileContent)
	}
	if strings.Contains(argv, "abab") {
		t.Errorf("the caller token rides the supervisor's argv:\n%s", argv)
	}
	if got := strings.Join(plan.JailDaemonNames, ","); got != "openai-auth-broker" {
		t.Errorf("JailDaemonNames = %q", got)
	}
}

// A LAUNCH WITH NO DAEMON AND NO GUEST CLIENT'S ENDPOINT PAYS NOTHING: no binary staged, no
// file, no supervisor. Neither trigger fires, so not one guest binary is copied, a guest
// source that happens to be named included.
func TestAGuestPlanWithNoDaemonStagesAndStartsNothing(t *testing.T) {
	for _, jd := range []JailDaemons{{}, {GuestBinSource: "/src"}} {
		plan := guestPlan(t, jd)
		if len(plan.JailDaemonArgv) != 0 || plan.DaemonEnvFile != "" || plan.GuestBinSource != "" ||
			len(plan.GuestClients) != 0 {
			t.Errorf("a plan with no daemon carries guest artifacts: %+v %q %v",
				plan.JailDaemonArgv, plan.GuestBinSource, plan.GuestClients)
		}
		for _, c := range plan.StageCommands {
			for _, name := range GuestBinaries {
				if len(c) == 4 && c[0] == mvBin && c[3] == GuestBinaryPath(name, "") {
					t.Errorf("a plan with no daemon and no client endpoint stages %s", name)
				}
			}
		}
	}
}

// serialEndpointEnv is a launch env carrying the serial loophole's published endpoint, the
// variable yolo-serial reads.
func serialEndpointEnv() *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	env.Set(paths.SerialEndpointEnv, "/private/tmp/yolo-host-services-x/serial.endpoint")
	return env
}

// A GUEST CLIENT'S ENDPOINT ALONE STAGES THE WHOLE GUEST SET, AND STARTS NO SUPERVISOR. The
// serial loophole runs no jail daemon, so before the guest clients a macos-user sandbox was
// told where the serial bridge is and had no yolo-serial to dial it with. Every guest binary is
// copied to a temp name in GuestBinDir and renamed into place; the supervisor, its env file and
// its log stay the daemons' alone.
func TestASerialEndpointWithNoDaemonStagesEveryGuestBinaryAndNoSupervisor(t *testing.T) {
	src := "/opt/homebrew/Cellar/yolo-jail/1.0/share/yolo-jail/bin/darwin-arm64"
	plan := BuildRunPlanWithDaemons(probeWS, jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, serialEndpointEnv(),
		mockDarwin(), nil, JailDaemons{GuestBinSource: src}, FloorStage{}, PlanSession{})
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a well-formed client-only plan fails its invariants:\n%s", strings.Join(problems, "\n"))
	}
	if len(plan.JailDaemonArgv) != 0 || plan.DaemonEnvFile != "" || plan.SupervisorLog != "" {
		t.Errorf("a client-only plan starts a supervisor: %v %q %q",
			plan.JailDaemonArgv, plan.DaemonEnvFile, plan.SupervisorLog)
	}
	if got := strings.Join(plan.GuestClients, ","); got != "yolo-serial" || plan.GuestBinSource != src {
		t.Errorf("GuestClients = %q, GuestBinSource = %q", got, plan.GuestBinSource)
	}
	for _, name := range GuestBinaries {
		dst := GuestBinaryPath(name, "")
		copied, renamed := -1, -1
		for i, c := range plan.StageCommands {
			if len(c) == 4 && c[0] == cpBin && c[2] == src+"/"+name && c[3] == dst+".new" {
				copied = i
			}
			if len(c) == 4 && c[0] == mvBin && c[2] == dst+".new" && c[3] == dst {
				renamed = i
			}
		}
		if copied < 0 || renamed < 0 || renamed < copied {
			t.Errorf("%s is not staged copy-then-rename into %s (copy %d, rename %d)",
				name, GuestBinDir(""), copied, renamed)
		}
	}
}

// guestClientInvariants fails a plan whose session env carries a client's endpoint and stages
// no client.
func TestPlanInvariantsCatchAGuestClientEndpointWithNoClient(t *testing.T) {
	good := BuildRunPlanWithDaemons(probeWS, jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, serialEndpointEnv(),
		mockDarwin(), nil, JailDaemons{GuestBinSource: "/src"}, FloorStage{}, PlanSession{})
	p := good
	p.StageCommands = nil
	for _, c := range good.StageCommands {
		if len(c) == 4 && c[0] == mvBin && c[3] == GuestBinaryPath("yolo-serial", "") {
			continue
		}
		p.StageCommands = append(p.StageCommands, c)
	}
	if problems := guestClientInvariants(p); len(problems) == 0 || len(PlanInvariants(p)) == 0 {
		t.Error("PlanInvariants accepted a serial endpoint with no yolo-serial staged")
	}
	// And a plan built with no source at all, which is what a launch whose resolver was skipped
	// would hand it.
	none := BuildRunPlanWithDaemons(probeWS, jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, serialEndpointEnv(),
		mockDarwin(), nil, JailDaemons{}, FloorStage{}, PlanSession{})
	if len(guestClientInvariants(none)) == 0 {
		t.Error("PlanInvariants accepted a serial endpoint with no guest source to stage from")
	}
}

// THE ORCHESTRATOR RESOLVES THE GUEST SET FOR A CLIENT ALONE, and refuses, naming the client and
// its loophole, when it cannot. Deleting the client half of guestBinariesWanted fails this.
func TestTheOrchestratorResolvesTheGuestSetForAClientAlone(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	asked := 0
	d.GuestBinaries = func(string) (string, error) { asked++; return "/opt/yolo/bin/darwin-arm64", nil }
	d.StartBackground = func([]string) (Background, error) {
		t.Error("a client-only launch started a supervisor")
		return Background{Stop: func() {}}, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(probeWS)
	o.PackEnv = serialEndpointEnv()
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	if asked != 1 {
		t.Errorf("Deps.GuestBinaries was asked %d times for a launch carrying the serial endpoint", asked)
	}
	if !strings.Contains(strings.Join(rec, "\n"), "run:sudo "+mvBin+" -f "+GuestBinaryPath("yolo-serial", "")+".new "+
		GuestBinaryPath("yolo-serial", "")) {
		t.Errorf("the launch did not stage yolo-serial:\n%s", strings.Join(rec, "\n"))
	}

	rec = nil
	d = mockDeps(&rec)
	d.GuestBinaries = func(string) (string, error) { return "", errFake("nix build .#guestPrefix failed") }
	buf.Reset()
	d.Out = &buf
	if rc := RunMacosUser(d, o); rc != 1 {
		t.Fatalf("rc = %d, want the refusal\n%s", rc, buf.String())
	}
	for _, want := range []string{"nix build .#guestPrefix failed", "yolo-serial", "the serial loophole",
		`"loopholes": {"serial": {"enabled": false}}`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "<name>") {
		t.Errorf("the refusal leaves the loophole as a placeholder although it knows it:\n%s", buf.String())
	}

	// No resolver wired at all: a yolo bug, said so, with the same way past it.
	rec = nil
	d = mockDeps(&rec)
	d.GuestBinaries = nil
	buf.Reset()
	d.Out = &buf
	if rc := RunMacosUser(d, o); rc != 1 {
		t.Fatalf("rc = %d with no resolver, want the refusal\n%s", rc, buf.String())
	}
	for _, want := range []string{"yolo bug", "github.com/mschulkind-oss/yolo-jail/issues",
		`"loopholes": {"serial": {"enabled": false}}`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the no-resolver refusal names no next step (%q):\n%s", want, buf.String())
		}
	}
	for _, r := range rec {
		if strings.HasPrefix(r, "proxy:") {
			t.Error("the agent ran")
		}
	}
}

// THE BINARIES LAND IN THE ROOT-OWNED PREFIX, AND NOWHERE ELSE (OQ-DP8's "they never reach the
// host PATH"). Every guest binary's copy goes to a temp name under GuestBinDir and is renamed
// into place there (a fresh inode, for macOS's per-vnode signature cache), and GuestBinDir is
// under the root-owned state dir, which is on the SANDBOX's PATH and on no host user's.
func TestGuestBinariesAreStagedIntoTheRootOwnedPrefixOnly(t *testing.T) {
	src := "/opt/homebrew/Cellar/yolo-jail/1.0/share/yolo-jail/bin/darwin-arm64"
	plan := guestPlan(t, openAIAdapterDaemons(src))
	if !strings.HasPrefix(GuestBinDir(""), stateDir+"/") {
		t.Fatalf("GuestBinDir %s is not under the root-owned state dir", GuestBinDir(""))
	}
	if StagedYoloPath("") != GuestBinaryPath("yolo", "") {
		t.Errorf("the staged yolo %s is not in the guest prefix beside the in-jail binaries", StagedYoloPath(""))
	}
	for _, name := range GuestBinaries {
		dst := GuestBinaryPath(name, "")
		var copied, renamed bool
		for _, c := range plan.StageCommands {
			if len(c) == 4 && c[0] == cpBin && c[2] == src+"/"+name && c[3] == dst+".new" {
				copied = true
			}
			if len(c) == 4 && c[0] == mvBin && c[2] == dst+".new" && c[3] == dst {
				renamed = true
			}
			if len(c) > 0 && (c[0] == cpBin || c[0] == mvBin) && strings.HasSuffix(c[len(c)-1], "/"+name) &&
				!strings.HasPrefix(c[len(c)-1], GuestBinDir("")+"/") {
				t.Errorf("a stage command writes %s outside the guest prefix: %v", name, c)
			}
		}
		if !copied || !renamed {
			t.Errorf("%s is not staged copy-to-temp-then-rename into %s (copied %v, renamed %v)",
				name, dst, copied, renamed)
		}
	}
}

// THE CALLER-TOKEN FILE'S PERMISSIONS ARE THE BOUNDARY (OQ-DP9: "no network isolation on
// macos-user, so the token files' ownership and permissions are the boundary"). The supervisor's
// file is written ROOT-OWNED 0600 through `sudo tee` into a 0700 directory, and ONE `user:` ACE
// grants the sandbox account read — search on the directory, read on the file, never write, and
// never a `group:` ACE, because SandboxGroup holds the host user too. Driven through
// RunMacosUser, so the installer call and its mode are the launch's, not a helper's.
func TestTheDaemonTokenFileIsRootOwned0600AndReadableByTheSandboxAccountOnly(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var modes []string
	d.InstallRootFile = func(path, content, mode string) bool {
		rec = append(rec, "install:"+path)
		modes = append(modes, path+" "+mode)
		return true
	}
	fakeSupervisor(&d, &rec, "/Users/Shared/yolo/proj", "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts("/Users/Shared/yolo/proj")
	o.JailDaemons = openAIAdapterDaemons("")
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	// The session's own file, named by the key this launch minted (sessionfiles.go).
	file := SandboxDaemonEnvFile(launchedSessionKey(t, rec, "/Users/Shared/yolo/proj"), "")
	if !strings.HasPrefix(file, stateDir+"/"+sandboxEnvLeaf+"/") {
		t.Fatalf("the daemon env file %s is not in the root-owned env dir", file)
	}
	found := false
	for _, m := range modes {
		if m == file+" 0600" {
			found = true
		}
	}
	if !found {
		t.Errorf("the daemon env file is not installed root-owned 0600 (installs: %v)", modes)
	}
	joined := strings.Join(rec, "\n")
	dir := pathParent(file)
	for _, want := range []string{
		"run:sudo " + chmodBin + " 0700 " + dir,
		"run:sudo " + chmodBin + " +a user:" + SandboxUser + " allow search " + dir,
		"run:sudo " + chmodBin + " +a user:" + SandboxUser + " allow " + sandboxFileReadRights + " " + file,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the launch does not run %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "group:"+SandboxGroup+" allow "+sandboxFileReadRights+" "+file) ||
		strings.Contains(sandboxFileReadRights, "write") {
		t.Errorf("the token file is granted to the group (which holds the host user) or writable")
	}
	// Swept when the session ends, after the supervisor stops.
	if !strings.Contains(joined, "stop\nrun:sudo "+rmBin+" -f "+file) {
		t.Errorf("the daemon env file is not swept after the supervisor stops:\n%s", joined)
	}
}

// THE SUPERVISOR STARTS AFTER THE BOOTSTRAP AND BEFORE THE AGENT, AND STOPS AFTER IT. Deleting
// the startJailDaemons call from RunMacosUser fails this.
func TestTheOrchestratorStartsTheSupervisorBeforeTheAgentAndStopsItAfter(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	fakeSupervisor(&d, &rec, "/Users/Shared/yolo/proj", "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts("/Users/Shared/yolo/proj")
	o.JailDaemons = openAIAdapterDaemons("")
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	idx := func(prefix string) int {
		for i, r := range rec {
			if strings.HasPrefix(r, prefix) {
				return i
			}
		}
		return -1
	}
	boot, start, proxy, stop := idx("run:sudo --user="+SandboxUser+" /usr/bin/env -i"), idx("start:"),
		idx("proxy:"), idx("stop")
	if boot < 0 || start < 0 || proxy < 0 || stop < 0 || !(boot < start && start < proxy && proxy < stop) {
		t.Fatalf("want bootstrap < supervisor start < agent < supervisor stop; got %d %d %d %d:\n%s",
			boot, start, proxy, stop, strings.Join(rec, "\n"))
	}
	if !strings.Contains(rec[start], "/usr/bin/sandbox-exec -f ") ||
		!strings.Contains(rec[start], GuestBinaryPath(JaildName, "")+" supervise") {
		t.Errorf("the started argv is not the confined supervisor: %s", rec[start])
	}
	if !strings.Contains(buf.String(), "Started openai-auth-broker inside the sandbox") {
		t.Errorf("the launch does not disclose the daemons it started:\n%s", buf.String())
	}
}

// A GUEST THAT CANNOT GET ITS BINARIES REFUSES THE LAUNCH, before any privileged step: the
// served set already pointed the agent at the daemon's address.
func TestAMissingGuestBinaryRefusesTheLaunch(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.GuestBinaries = func(string) (string, error) { return "", errFake("nix build .#guestPrefix failed") }
	d.StartBackground = func([]string) (Background, error) {
		t.Error("the supervisor started without its binary")
		return Background{Stop: func() {}}, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts("/Users/Shared/yolo/proj")
	o.JailDaemons = openAIAdapterDaemons("")
	if rc := RunMacosUser(d, o); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "nix build .#guestPrefix failed") {
		t.Errorf("the refusal does not say why:\n%s", buf.String())
	}
	for _, r := range rec {
		if strings.HasPrefix(r, "proxy:") {
			t.Error("the agent ran")
		}
	}
}

// PlanInvariants' guest rules each fail the plan they describe.
func TestPlanInvariantsCatchAnUnconfinedOrMissingSupervisor(t *testing.T) {
	good := guestPlan(t, openAIAdapterDaemons("/src"))
	for name, mutate := range map[string]func(*RunPlan){
		"no supervisor": func(p *RunPlan) { p.JailDaemonArgv = nil },
		"unconfined": func(p *RunPlan) {
			var out []string
			for i := 0; i < len(p.JailDaemonArgv); i++ {
				if p.JailDaemonArgv[i] == "/usr/bin/sandbox-exec" {
					i += 3 // drop sandbox-exec -f <profile> --
					continue
				}
				out = append(out, p.JailDaemonArgv[i])
			}
			p.JailDaemonArgv = out
		},
		"binary not staged": func(p *RunPlan) {
			var keep [][]string
			for _, c := range p.StageCommands {
				if len(c) == 4 && c[0] == mvBin && c[3] == GuestBinaryPath(JaildName, "") {
					continue
				}
				keep = append(keep, c)
			}
			p.StageCommands = keep
		},
		"no payload in the file": func(p *RunPlan) { p.DaemonEnvFileContent = "export X='y'\n" },
		"no log redirect": func(p *RunPlan) {
			var out []string
			for i := 0; i < len(p.JailDaemonArgv); i++ {
				if p.JailDaemonArgv[i] == supervisorLogWrapper {
					out = out[:len(out)-2] // drop /bin/sh -c
					i += 2                 // and the wrapper, its $0 and the log path
					continue
				}
				out = append(out, p.JailDaemonArgv[i])
			}
			p.JailDaemonArgv = out
		},
		"log elsewhere": func(p *RunPlan) { p.SupervisorLog = "/tmp/supervisor.log" },
	} {
		p := good
		mutate(&p)
		if len(PlanInvariants(p)) == 0 {
			t.Errorf("%s: PlanInvariants accepted the plan", name)
		}
	}
}

// A DAEMONS ENV FILE WHOSE INSTALL FAILED HALF-WAY IS STILL REMOVED THROUGH THE TEARDOWN. The tee
// may have written the supervisor's caller tokens before the chmod or the read ACE failed, and the
// file's per-session name means no later launch rewrites it; so the refusal removes it, and a
// removal that fails keeps the session's record for the next sweep, as the env file's and the
// profile's do. Driven through RunMacosUser, so it fails if startJailDaemons returns without the
// teardown's removal.
func TestADaemonsEnvFileWhoseInstallFailsIsRemovedThroughTheTeardown(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	for _, tc := range []struct {
		name               string
		installFails       bool // the tee ran and the chmod after it failed
		grantFails, rmFail bool
	}{
		{name: "the install", installFails: true},
		{name: "the read ACE", grantFails: true},
		{name: "the install, and its removal", installFails: true, rmFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var rec []string
			d := mockDeps(&rec)
			d.SessionRecordDir = func() string { return dir }
			fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
			d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
			install := d.InstallRootFile
			d.InstallRootFile = func(path, content, mode string) bool {
				ok := install(path, content, mode)
				return ok && !(tc.installFails && strings.HasSuffix(path, ".daemons.env"))
			}
			run := d.Run
			d.Run = func(argv []string) int {
				rc := run(argv)
				last := argv[len(argv)-1]
				if !strings.HasSuffix(last, ".daemons.env") {
					return rc
				}
				if (tc.grantFails && argv[1] == chmodBin) || (tc.rmFail && argv[1] == rmBin) {
					return 1
				}
				return rc
			}
			var out bytes.Buffer
			d.Out = &out
			o := newOpts(ws)
			o.JailDaemons = openAIAdapterDaemons("")
			if rc := RunMacosUser(d, o); rc != 1 {
				t.Fatalf("rc = %d, want the refusal\n%s", rc, out.String())
			}
			key := launchedSessionKey(t, rec, ws)
			daemonsEnv := SandboxDaemonEnvFile(key, "")
			if !strings.Contains(strings.Join(rec, "\n"), "run:sudo "+rmBin+" -f "+daemonsEnv) {
				t.Errorf("the half-written daemons env file %s is never removed:\n%s",
					daemonsEnv, strings.Join(rec, "\n"))
			}
			record := filepath.Join(dir, key+sessionRecordSuffix)
			_, err := os.Lstat(record)
			if tc.rmFail {
				if err != nil {
					t.Errorf("the daemons env file could not be removed and the record naming it is gone: %v", err)
				}
				if !strings.Contains(out.String(), "sudo "+rmBin+" -f "+strings.Join(SessionFilePaths(key, ""), " ")) {
					t.Errorf("the failed removal does not warn with the command:\n%s", out.String())
				}
			} else if !os.IsNotExist(err) {
				t.Errorf("every removal succeeded and the record is still there (%v)", err)
			}
		})
	}
}

// A DRY RUN NAMES THE GUEST SET STAGED FOR A CLIENT ALONE: "no jail daemon" is not "nothing
// staged into the guest", and the plan is how a user tells them apart.
func TestTheDryRunNamesTheGuestSetStagedForAClient(t *testing.T) {
	d := mockDeps(nil)
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(probeWS)
	o.PackEnv = serialEndpointEnv()
	o.DryRun = true
	if rc := RunMacosUser(d, o); rc != 0 {
		t.Fatalf("dry run rc = %d\n%s", rc, buf.String())
	}
	out := buf.String()
	want := "  guest bins: " + PrebuiltGuestBinDir(o.RepoRoot) + " → " + GuestBinDir("") + " (for yolo-serial)"
	if !strings.Contains(out, "jail daemons: none run in the sandbox for this launch") || !strings.Contains(out, want) {
		t.Errorf("the dry run does not name the guest set staged for yolo-serial (want %q):\n%s", want, out)
	}
	if !strings.Contains(out, "sudo "+mvBin+" -f "+GuestBinaryPath("yolo-serial", "")+".new "+GuestBinaryPath("yolo-serial", "")) {
		t.Errorf("the dry run's privileged commands do not stage yolo-serial:\n%s", out)
	}
}

// A DRY RUN THAT SEES NO CLIENT'S ENDPOINT SAYS IT CANNOT SEE ONE. The run pipeline's dry run starts
// no host service and names only the credential service's endpoint, so "no guest bins" in a
// render is not "no guest bins at launch": the line names each client and its loophole, so a
// user with serial on is not told the sandbox gets no yolo-serial.
func TestTheDryRunSaysItCannotSeeAGuestClientsEndpoint(t *testing.T) {
	d := mockDeps(nil)
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(probeWS)
	o.DryRun = true
	if rc := RunMacosUser(d, o); rc != 0 {
		t.Fatalf("dry run rc = %d\n%s", rc, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "a dry run starts no host service") {
		t.Errorf("the dry run does not say it cannot see a client's endpoint:\n%s", out)
	}
	for _, c := range GuestClients {
		if !strings.Contains(out, c.Binary+" (the "+c.Loophole+" loophole's client)") {
			t.Errorf("the dry run does not name %s and its loophole:\n%s", c.Binary, out)
		}
	}
}
