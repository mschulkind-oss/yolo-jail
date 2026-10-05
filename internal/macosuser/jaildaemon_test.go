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
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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

// A LAUNCH WITH NO DAEMON PAYS NOTHING: no binary staged, no file, no supervisor.
func TestAGuestPlanWithNoDaemonStagesAndStartsNothing(t *testing.T) {
	plan := guestPlan(t, JailDaemons{})
	if len(plan.JailDaemonArgv) != 0 || plan.DaemonEnvFile != "" || plan.GuestBinSource != "" {
		t.Errorf("a plan with no daemon carries guest-daemon artifacts: %+v", plan.JailDaemonArgv)
	}
	for _, c := range plan.StageCommands {
		if len(c) == 4 && c[0] == mvBin && c[3] == GuestBinaryPath(JaildName, "") {
			t.Errorf("a plan with no daemon stages %s", JaildName)
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
