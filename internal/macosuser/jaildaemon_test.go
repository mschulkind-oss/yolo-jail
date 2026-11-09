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
	"fmt"
	"os"
	"os/exec"
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
	supervise := filepath.Join(plan.SessionGuestBinDir, JaildName) + " supervise"
	ci, si := strings.Index(argv, confined), strings.Index(argv, supervise)
	if ci < 0 || si < 0 || si < ci {
		t.Fatalf("the supervisor is not started under the session's Seatbelt profile "+
			"(confined at %d, supervise at %d):\n%s", ci, si, argv)
	}
	if !strings.HasPrefix(argv, "/usr/bin/sudo -n --set-home --user="+SandboxUser+" /usr/bin/env -i ") {
		t.Errorf("the supervisor does not run as the sandbox account through a non-interactive sudo:\n%s", argv)
	}
	// The PATH the supervisor (and so every daemon it execs) resolves names on.
	onPath := false
	for _, w := range plan.JailDaemonArgv {
		if v, ok := strings.CutPrefix(w, "PATH="); ok {
			for _, dir := range strings.Split(v, ":") {
				onPath = onPath || dir == plan.SessionGuestBinDir
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

// A LAUNCH WITH NO DAEMON AND NO GUEST CLIENT'S ENDPOINT STAGES ONLY ITS SESSION Yolo: no guest
// helper is copied, no supervisor is started and the plan carries no guest-helper source.
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
				if argvMentions(c, filepath.Join(plan.SessionGuestBinDir+".new", name)) {
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

// Same-workspace sessions from different matched bundles keep their own guest executable paths.
// The paths include each session's existing ID under the existing scoped guest-bin root; neither
// session may replace the other's yolo or guest daemon while both are alive.
func TestSameWorkspaceGuestBundlesUseDistinctSessionPaths(t *testing.T) {
	workspace := "/Users/Shared/yolo/proj"
	makePlan := func(id, source string) RunPlan {
		env := jsonx.NewOrderedMap()
		env.Set(jailDaemonsEnv, `[{"name":"openai-auth-broker","cmd":["yolo-jaild","openai-auth-adapter"],"restart":"on-failure"}]`)
		return BuildRunPlanWithDaemons(workspace, jsonx.NewOrderedMap(), []string{"codex"},
			[]string{"codex"}, "/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(),
			mockDarwin(), nil, JailDaemons{Env: env, GuestBinSource: source}, FloorStage{}, PlanSession{ID: id})
	}
	a := makePlan("aaaaaaaaaaaaaaaa", "/bundles/A/bin/darwin-arm64")
	b := makePlan("bbbbbbbbbbbbbbbb", "/bundles/B/bin/darwin-arm64")
	if a.Cname != b.Cname {
		t.Fatalf("fixture must be same-workspace, got cname %q and %q", a.Cname, b.Cname)
	}
	for _, name := range append([]string{"yolo"}, GuestBinaries...) {
		pathA, pathB := filepath.Join(a.SessionGuestBinDir, name), filepath.Join(b.SessionGuestBinDir, name)
		if pathA == pathB {
			t.Errorf("session A and B share %s at %s", name, pathA)
		}
		root := GuestBinDir(a.StagedDir) + "/"
		if !strings.HasPrefix(pathA, root) || !strings.HasPrefix(pathB, root) {
			t.Errorf("session binaries escaped the existing guest-bin root: %s / %s", pathA, pathB)
		}
	}
	if !validGuestBundleIdentity(a.GuestBundleIdentity) || !validGuestBundleIdentity(b.GuestBundleIdentity) ||
		a.GuestBundleIdentity == b.GuestBundleIdentity {
		t.Errorf("distinct selected bundle source keys did not produce distinct valid identities: %q / %q",
			a.GuestBundleIdentity, b.GuestBundleIdentity)
	}
	for _, plan := range []RunPlan{a, b} {
		pathValue, ok := argvEnvValue(plan.LaunchArgv, "PATH")
		if !ok || !strings.Contains(":"+pathValue+":", ":"+plan.SessionGuestBinDir+":") {
			t.Errorf("the session launch PATH does not use its child %s: %s", plan.SessionGuestBinDir, pathValue)
		}
		if !containsArg(plan.BootstrapArgv, plan.StagedYolo) || filepath.Dir(plan.StagedYolo) != plan.SessionGuestBinDir {
			t.Errorf("bootstrap yolo is outside its own child: %+v", plan)
		}
	}
	if filepath.Dir(a.StagedYolo) == filepath.Dir(b.StagedYolo) {
		t.Errorf("the two agent PATH entries share a guest-bin child: %s", filepath.Dir(a.StagedYolo))
	}
}

func TestGuestBundleIdentityReadsSnapshotStampAndSupportsLegacySources(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	stampID := strings.Repeat("c", 64)
	if err := os.WriteFile(filepath.Join(rootA, "yolo-build-stamp.json"),
		[]byte(`{"version":"dev","commit":"abc","snapshotIdentity":"`+stampID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GuestBundleIdentity(rootA)
	if err != nil || got != stampID {
		t.Fatalf("GuestBundleIdentity returned %q, %v; want stamp identity %q", got, err, stampID)
	}
	legacyA, err := GuestBundleIdentity(rootB)
	if err != nil || !validGuestBundleIdentity(legacyA) || legacyA == stampID {
		t.Errorf("legacy bundle did not get a safe source-key digest: %q, %v", legacyA, err)
	}
	legacyB, err := GuestBundleIdentity(t.TempDir())
	if err != nil || legacyA == legacyB {
		t.Errorf("distinct legacy source keys reused identity %q: %v", legacyA, err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "yolo-build-stamp.json"), []byte(`{"snapshotIdentity":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GuestBundleIdentity(rootB); err == nil {
		t.Error("an invalid stamped identity silently fell back to the legacy source key")
	}
}

// The guest client's endpoint alone stages the whole guest set, and starts no supervisor. The
// serial loophole runs no jail daemon, so before the guest clients a macos-user sandbox was told
// where the serial bridge is and had no yolo-serial to dial it with. Every guest binary is copied
// to a temp name in GuestBinDir and renamed into place; the supervisor, its env file and its log
// stay the daemons' alone.
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
		tmp := filepath.Join(plan.SessionGuestBinDir+".new", name)
		copied, renamed := -1, -1
		for i, c := range plan.StageCommands {
			if len(c) == 4 && c[0] == cpBin && c[2] == src+"/"+name && c[3] == tmp {
				copied = i
			}
			if len(c) == 4 && c[0] == mvBin && c[2] == plan.SessionGuestBinDir+".new" && c[3] == plan.SessionGuestBinDir {
				renamed = i
			}
		}
		if copied < 0 || renamed < 0 || renamed < copied {
			t.Errorf("%s is not composed into the private session child before its atomic publish (copy %d, rename %d)",
				name, copied, renamed)
		}
	}
}

func TestSameWorkspaceGuestBundleStagingKeepsBothSessionChildrenImmutable(t *testing.T) {
	root := t.TempDir()
	sourceA, sourceB := t.TempDir(), t.TempDir()
	writeBundle := func(dir, tag string) {
		t.Helper()
		for _, name := range append([]string{"yolo"}, GuestBinaries...) {
			content := []byte("#!/bin/sh\nprintf '%s\\n' '" + tag + "/" + name + "'\n")
			if err := os.WriteFile(filepath.Join(dir, name), content, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeBundle(sourceA, "A")
	writeBundle(sourceB, "B")
	idA, idB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	sessionA, sessionB := "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"
	runCommands := func(commands [][]string) {
		t.Helper()
		for _, command := range commands {
			if out, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("stage failed: %v: %v\n%s", err, command, out)
			}
		}
	}
	runCommands(StageGuestSessionCommands(filepath.Join(sourceA, "yolo"), sourceA, idA, sessionA, root))
	pathA := GuestSessionBinDir(idA, sessionA, root)
	if !strings.HasPrefix(pathA, GuestBinDir(root)+"/") {
		t.Fatalf("A escaped existing scoped root: %s", pathA)
	}
	bytesA := map[string][]byte{}
	inodesA := map[string]os.FileInfo{}
	for _, name := range append([]string{"yolo"}, GuestBinaries...) {
		path := filepath.Join(pathA, name)
		bytesA[name], _ = os.ReadFile(path)
		inodesA[name], _ = os.Stat(path)
		if inodesA[name] == nil || inodesA[name].Mode().Perm() != 0o555 {
			t.Fatalf("A/%s is not sealed 0555: %v", name, inodesA[name])
		}
		if out, err := exec.Command(path).Output(); err != nil || strings.TrimSpace(string(out)) != "A/"+name {
			t.Fatalf("A/%s did not execute its own bytes: %q, %v", name, out, err)
		}
	}
	if info, err := os.Stat(pathA); err != nil || info.Mode().Perm() != 0o555 {
		t.Fatalf("A child is not sealed: %v, %v", info, err)
	}
	runCommands(StageGuestSessionCommands(filepath.Join(sourceB, "yolo"), sourceB, idB, sessionB, root))
	pathB := GuestSessionBinDir(idB, sessionB, root)
	if pathA == pathB {
		t.Fatal("different bundle identities and session tokens reused one child")
	}
	for _, name := range append([]string{"yolo"}, GuestBinaries...) {
		path := filepath.Join(pathA, name)
		got, err := os.ReadFile(path)
		info, statErr := os.Stat(path)
		if err != nil || statErr != nil || string(got) != string(bytesA[name]) || !os.SameFile(inodesA[name], info) {
			t.Errorf("staging B changed A/%s bytes or inode: %q, %v / %v", name, got, err, statErr)
		}
		if out, err := exec.Command(filepath.Join(pathB, name)).Output(); err != nil ||
			strings.TrimSpace(string(out)) != "B/"+name {
			t.Errorf("B/%s did not execute its own bytes: %q, %v", name, out, err)
		}
	}
	runCommands(GuestSessionRemoveCommands(sessionA, root))
	if _, err := os.Stat(pathA); !os.IsNotExist(err) {
		t.Errorf("ending A did not remove only A's child: %v", err)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Errorf("ending A removed B's still-live child: %v", err)
	}
	runCommands(GuestSessionRemoveCommands(sessionB, root))
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
		if argvMentions(c, filepath.Join(good.SessionGuestBinDir+".new", "yolo-serial")) {
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
	joined := strings.Join(rec, "\n")
	if !strings.Contains(joined, "cp -f /opt/yolo/bin/darwin-arm64/yolo-serial "+GuestBinDir("")+"/") ||
		!strings.Contains(joined, "mv -f "+GuestBinDir("")+"/") {
		t.Errorf("the launch did not stage yolo-serial into and publish its private guest-bin child:\n%s", joined)
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
		"`yolo pack remove serial` on the host"} {
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
		"`yolo pack remove serial` on the host"} {
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
	if len(plan.StageCommands) > 0 && filepath.Dir(plan.StagedYolo) != plan.SessionGuestBinDir {
		t.Errorf("staged yolo %s is outside the session guest child %s", plan.StagedYolo, plan.SessionGuestBinDir)
	}
	for _, name := range GuestBinaries {
		tmp := filepath.Join(plan.SessionGuestBinDir+".new", name)
		var copied, published bool
		for _, c := range plan.StageCommands {
			if len(c) == 4 && c[0] == cpBin && c[2] == src+"/"+name && c[3] == tmp {
				copied = true
			}
			if len(c) == 4 && c[0] == mvBin && c[2] == plan.SessionGuestBinDir+".new" && c[3] == plan.SessionGuestBinDir {
				published = true
			}
			if len(c) > 0 && (c[0] == cpBin || c[0] == mvBin) && strings.Contains(c[len(c)-1], "/"+name) &&
				!strings.HasPrefix(c[len(c)-1], GuestBinDir("")+"/") {
				t.Errorf("a stage command writes %s outside the scoped guest-bin root: %v", name, c)
			}
		}
		if !copied || !published {
			t.Errorf("%s is not composed into the sealed session child %s (copied %v, published %v)",
				name, plan.SessionGuestBinDir, copied, published)
		}
	}
}

// THE CALLER-TOKEN FILE'S PERMISSIONS ARE THE BOUNDARY (OQ-DP9: "no network isolation on
// macos-user, so the token files' ownership and permissions are the boundary"). The supervisor's
// file is written by the HOST USER at 0600 into a 0700 directory, with no sudo, and ONE `user:` ACE
// grants the sandbox account read — search on the directory, read on the file, never write, and
// never a `group:` ACE, because SandboxGroup holds the host user too. Driven through
// RunMacosUser, so the installer call and its mode are the launch's, not a helper's.
func TestTheDaemonTokenFileIsHostOwned0600AndReadableByTheSandboxAccountOnly(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var modes []string
	d.WriteHostFile = func(path, content string, mode os.FileMode) bool {
		rec = append(rec, "write:"+path)
		modes = append(modes, fmt.Sprintf("%s %#o", path, mode))
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
		t.Fatalf("the daemon env file %s is not in the staging root's env dir", file)
	}
	found := false
	for _, m := range modes {
		if m == file+" 0600" {
			found = true
		}
	}
	if !found {
		t.Errorf("the daemon env file is not written 0600 (writes: %v)", modes)
	}
	joined := strings.Join(rec, "\n")
	dir := pathParent(file)
	for _, want := range []string{
		"run:" + chmodBin + " 0700 " + dir,
		"run:" + chmodBin + " +a user:" + SandboxUser + " allow search " + dir,
		"run:" + chmodBin + " +a user:" + SandboxUser + " allow " + sandboxFileReadRights + " " + file,
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
	if !strings.Contains(joined, "stop\nrun:"+rmBin+" -f "+file) {

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
	boot, start, proxy, stop := idx("run:/usr/bin/sudo -n --user="+SandboxUser+" /usr/bin/env -i"), idx("start:"),
		idx("proxy:"), idx("stop")
	if boot < 0 || start < 0 || proxy < 0 || stop < 0 || !(boot < start && start < proxy && proxy < stop) {
		t.Fatalf("want bootstrap < supervisor start < agent < supervisor stop; got %d %d %d %d:\n%s",
			boot, start, proxy, stop, strings.Join(rec, "\n"))
	}
	if !strings.Contains(rec[start], "/usr/bin/sandbox-exec -f ") ||
		!strings.Contains(rec[start], GuestBinDir("")+"/") || !strings.Contains(rec[start], "/yolo-jaild supervise") {
		t.Errorf("the started argv is not the confined supervisor: %s", rec[start])
	}
	if !strings.Contains(buf.String(), "Started openai-auth-broker inside the sandbox") {
		t.Errorf("the launch does not disclose the daemons it started:\n%s", buf.String())
	}
	key := launchedSessionKey(t, rec, o.Workspace)
	_, sessionID, ok := parseSessionKey(key)
	if !ok {
		t.Fatalf("invalid launched session key %q", key)
	}
	cleanup := GuestSessionRemoveCommands(sessionID, "")
	cleanupAt := recAt(rec, "run:"+strings.Join(cleanup[0], " "))
	if cleanupAt <= stop {
		t.Errorf("the session's own guest child was not removed after its supervisor stopped (cleanup %d, stop %d):\n%s",
			cleanupAt, stop, strings.Join(rec, "\n"))
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
				if len(c) == 4 && c[0] == cpBin && c[2] == "/src/"+JaildName &&
					c[3] == filepath.Join(p.SessionGuestBinDir+".new", JaildName) {
					continue
				}
				keep = append(keep, c)
			}
			p.StageCommands = keep
		},
		"no payload in the file":      func(p *RunPlan) { p.DaemonEnvFileContent = "export X='y'\n" },
		"missing session-bin cleanup": func(p *RunPlan) { p.SessionGuestBinRemoveCommands = nil },
		"session PATH omitted": func(p *RunPlan) {
			for i, arg := range p.LaunchArgv {
				if strings.HasPrefix(arg, "PATH=") {
					p.LaunchArgv[i] = "PATH=/usr/bin:/bin"
					return
				}
			}
			p.LaunchArgv = append(p.LaunchArgv, "PATH=/usr/bin:/bin")
		},
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
		installFails       bool // the host write failed
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
			install := d.WriteHostFile
			d.WriteHostFile = func(path, content string, mode os.FileMode) bool {
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
				if (tc.grantFails && argv[0] == chmodBin) || (tc.rmFail && argv[0] == rmBin) {
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
			if !strings.Contains(strings.Join(rec, "\n"), "run:"+rmBin+" -f "+daemonsEnv) {
				t.Errorf("the half-written daemons env file %s is never removed:\n%s",
					daemonsEnv, strings.Join(rec, "\n"))
			}
			record := filepath.Join(dir, key+sessionRecordSuffix)
			_, err := os.Lstat(record)
			if tc.rmFail {
				if err != nil {
					t.Errorf("the daemons env file could not be removed and the record naming it is gone: %v", err)
				}
				if !strings.Contains(out.String(), rmBin+" -f "+strings.Join(SessionFilePaths(key, ""), " ")) {
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
	identity, err := GuestBundleIdentity(o.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	child := GuestSessionBinDir(identity, SessionPlaceholder, "")
	want := "  guest bins: " + PrebuiltGuestBinDir(o.RepoRoot) + " → " + child + " (for yolo-serial)"
	if !strings.Contains(out, "jail daemons: none run in the sandbox for this launch") || !strings.Contains(out, want) {
		t.Errorf("the dry run does not name the guest set staged for yolo-serial (want %q):\n%s", want, out)
	}
	if !strings.Contains(out, cpBin+" -f "+PrebuiltGuestBinDir(o.RepoRoot)+"/yolo-serial "+child+".new/yolo-serial") ||
		!strings.Contains(out, mvBin+" -f "+child+".new "+child) {
		t.Errorf("the dry run's privileged commands do not stage and publish yolo-serial in its private child:\n%s", out)
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

// TestSessionGuestBinOfAcceptsOnlyASessionChild pins the parser the darwin integration tests use
// to read this session's guest-bin child off the path the sandbox resolved a client to: it
// round-trips GuestSessionBinDir, accepts the spelling a Mac run printed, and refuses every
// layout that is not one session's private child, the pre-isolation shared root first.
func TestSessionGuestBinOfAcceptsOnlyASessionChild(t *testing.T) {
	identity := strings.Repeat("cd51f15c", 8)
	child := GuestSessionBinDir(identity, "6028c3c06fbe2e9c", "")
	got, err := SessionGuestBinOf(filepath.Join(child, JaildName), JaildName, "")
	if err != nil || got != child {
		t.Fatalf("SessionGuestBinOf(%s) = %q, %v; want %s", filepath.Join(child, JaildName), got, err, child)
	}
	observed := GuestBinDir("") + "/d472112004b95a18-" + identity + "/yolo-serial"
	if got, err := SessionGuestBinOf(observed, "yolo-serial", ""); err != nil || got != filepath.Dir(observed) {
		t.Errorf("the observed in-sandbox path %s did not parse: %q, %v", observed, got, err)
	}
	sd := t.TempDir()
	if got, err := SessionGuestBinOf(filepath.Join(GuestSessionBinDir(identity, "0f83b8180063257e", sd), "yolo-log"),
		"yolo-log", sd); err != nil || filepath.Dir(got) != GuestBinDir(sd) {
		t.Errorf("a session child under another state dir did not parse: %q, %v", got, err)
	}
	for _, bad := range []struct{ path, name string }{
		{GuestBinaryPath("yolo-serial", ""), "yolo-serial"}, // the shared root, the pre-isolation layout
		{filepath.Join(GuestSessionBinDir(identity, SessionPlaceholder, ""), "yolo-ps"), "yolo-ps"},
		{filepath.Join(child+".new", "yolo-ps"), "yolo-ps"}, // the unpublished temp
		{filepath.Join(child, "sub", "yolo-ps"), "yolo-ps"},
		{filepath.Join(child, "yolo-ps"), "yolo-log"},
		{filepath.Join(GuestBinDir(""), "6028c3c06fbe2e9c", "yolo-ps"), "yolo-ps"},
		{filepath.Join(GuestBinDir(""), "6028c3c06fbe2e9c-"+strings.ToUpper(identity), "yolo-ps"), "yolo-ps"},
		{"/usr/local/bin/yolo-ps", "yolo-ps"},
		{"bin/" + filepath.Base(child) + "/yolo-ps", "yolo-ps"},
		{GuestBinDir("") + "/x/../" + filepath.Base(child) + "/yolo-ps", "yolo-ps"},
		{"MISSING", "yolo-ps"},
		{"", "yolo-ps"},
	} {
		if got, err := SessionGuestBinOf(bad.path, bad.name, ""); err == nil {
			t.Errorf("SessionGuestBinOf(%q, %q) accepted %q; want a refusal", bad.path, bad.name, got)
		}
	}
}
