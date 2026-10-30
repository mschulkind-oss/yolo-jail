package macosuser

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const (
	testSessionID  = "0123456789abcdef"
	testUserTmp    = "/private/var/folders/ab/xyz/T"
	testSessionTmp = testUserTmp + "/yolo-01234567"
)

// sessionTmpPlan is a plan whose session env names an endpoint in a session's host-services dir,
// as the run pipeline's macos-user arm publishes one, built in the given mode.
func sessionTmpPlan(t *testing.T, mode Mode, env *jsonx.OrderedMap) RunPlan {
	t.Helper()
	return sessionTmpPlanWith(t, mode, env, nil)
}

// sessionTmpPlanWith is sessionTmpPlan with the session edited first.
func sessionTmpPlanWith(t *testing.T, mode Mode, env *jsonx.OrderedMap, edit func(*PlanSession)) RunPlan {
	t.Helper()
	if env == nil {
		env = jsonx.NewOrderedMap()
		env.Set("YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT", testServicesDir+"/claude-oauth-broker.endpoint")
	}
	session := PlanSession{ID: testSessionID, Mode: mode}
	if mode == ModeSelf {
		session.HostHome, session.HostUser, session.DarwinUserTempDir = "/Users/you", "you", testUserTmp+"/"
	}
	if edit != nil {
		edit(&session)
	}
	return BuildRunPlanWithDaemons("/Users/you/code/alpha", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, env, nil, nil,
		JailDaemons{}, FloorStage{}, session)
}

// MMP-D17: the run-as-you session gets a short TMPDIR of its own under the user's temp dir, and
// tmux's and zsh's own variables point into it. Removing any of the three env lines, the
// creation, or the removal fails this.
func TestSelfModeSessionHasItsOwnTmpdir(t *testing.T) {
	if got := SessionTmpDir(testUserTmp+"/", testSessionID); got != testSessionTmp {
		t.Fatalf("SessionTmpDir = %q, want %q", got, testSessionTmp)
	}
	plan := sessionTmpPlan(t, ModeSelf, nil)
	if plan.SessionTmpDir != testSessionTmp {
		t.Fatalf("plan.SessionTmpDir = %q, want %q", plan.SessionTmpDir, testSessionTmp)
	}
	for key, want := range map[string]string{"TMPDIR": testSessionTmp, "TMUX_TMPDIR": testSessionTmp,
		"TMPPREFIX": testSessionTmp + "/zsh", "OVERMIND_SOCKET": testSessionTmp + "/overmind.sock"} {
		if v, ok := sandboxEnvFileValue(plan.EnvFileContent, key); !ok || v != want {
			t.Errorf("the session env file sets %s=%q (present %v), want %q", key, v, ok, want)
		}
	}
	create := []string{mkdirBin, "-m", "0700", testSessionTmp}
	if !slices.ContainsFunc(plan.StageCommands, func(c []string) bool { return slices.Equal(c, create) }) {
		t.Errorf("no stage command makes the session's temp dir: %v", plan.StageCommands)
	}
	if !slices.ContainsFunc(plan.SessionTmpRemoveCommands, func(c []string) bool {
		return slices.Equal(c, []string{rmBin, "-rf", testSessionTmp})
	}) {
		t.Errorf("the session's temp dir is not removed when it ends: %v", plan.SessionTmpRemoveCommands)
	}
	// The profile treats it as the launch's own TMPDIR: read-write, and a unix-socket root.
	for _, want := range []string{`(allow file-read* file-write* (subpath "` + testSessionTmp + `"))`,
		`(local unix-socket (subpath "` + testSessionTmp + `"))`, `(remote unix-socket (subpath "` + testSessionTmp + `"))`} {
		if !strings.Contains(plan.Seatbelt, want) {
			t.Errorf("the self-mode profile lacks %s", want)
		}
	}
	if problems := PlanInvariants(plan); len(problems) != 0 {
		t.Errorf("problems:\n  %s", strings.Join(problems, "\n  "))
	}
	// A unix socket's path is capped at 104 bytes; tmux's under this dir must fit.
	if n := len(testSessionTmp + "/tmux-501/default"); n >= 104 {
		t.Errorf("tmux's socket path under the session TMPDIR is %d bytes", n)
	}
}

// A TMPDIR an env layer of the user's set is kept, and tmux and zsh follow it.
func TestSelfModeSessionTmpdirKeepsAComposedTmpdir(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("TMPDIR", "/Users/you/code/alpha/.tmp/")
	plan := sessionTmpPlan(t, ModeSelf, env)
	for key, want := range map[string]string{"TMPDIR": "/Users/you/code/alpha/.tmp",
		"TMUX_TMPDIR": "/Users/you/code/alpha/.tmp", "TMPPREFIX": "/Users/you/code/alpha/.tmp/zsh"} {
		if v, _ := sandboxEnvFileValue(plan.EnvFileContent, key); v != want {
			t.Errorf("%s = %q, want %q", key, v, want)
		}
	}
}

// MMP-D15: the self-mode profile grants the session's host-services dir that an endpoint
// variable names, by exact path; a sibling session's dir is not named. Deleting the derivation
// (sessionServicesDirs) or its call fails this.
func TestSelfModeProfileGrantsTheSessionServicesDir(t *testing.T) {
	plan := sessionTmpPlan(t, ModeSelf, nil)
	at := strings.Index(plan.Seatbelt, "#seatbelt-test-id:"+ServicesDirReadAllowID+"#")
	if at < 0 || !strings.Contains(plan.Seatbelt[at:], `(subpath "`+testServicesDir+`")`) {
		t.Fatalf("the self-mode profile does not grant the session's services dir %s", testServicesDir)
	}
	if strings.Contains(plan.Seatbelt, "yolo-host-services-1a2b3c4d-other") {
		t.Error("a sibling session's dir is granted")
	}
	// Only session dirs: an endpoint elsewhere, or a container jail's dir, grants nothing.
	env := jsonx.NewOrderedMap()
	env.Set("YOLO_SERVICE_A_ENDPOINT", "/private/tmp/yolo-host-services-1a2b3c4d/a.endpoint")
	env.Set("YOLO_SERVICE_B_ENDPOINT", "/Users/you/b.endpoint")
	env.Set("NOT_AN_ENDPOINT", testServicesDir+"/c.endpoint")
	if dirs := sessionServicesDirs(env); len(dirs) != 0 {
		t.Errorf("sessionServicesDirs granted non-session dirs: %v", dirs)
	}
	env.Set("YOLO_SERVICE_D_ENDPOINT", testServicesDir+"/d.endpoint")
	env.Set("YOLO_SERVICE_E_ENDPOINT", testServicesDir+"/e.endpoint")
	if dirs := sessionServicesDirs(env); !slices.Equal(dirs, []string{testServicesDir}) {
		t.Errorf("sessionServicesDirs = %v, want one %s", dirs, testServicesDir)
	}
}

// THE LAUNCH MAKES AND REMOVES IT: a self-mode launch runs the creation before the agent and the
// removal after it. Deleting the stage step in BuildRunPlanWithDaemons or the teardown's remove in
// RunMacosUser fails this.
func TestASelfModeLaunchMakesAndRemovesItsTmpdir(t *testing.T) {
	var runs []string
	// The fixture runs the stage commands for real. Resolved where it is minted: the launch resolves
	// the per-user temp dir, and macOS's t.TempDir() is under the /var -> /private/var symlink.
	userTmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, _, out, _ := runPackTreeLaunch(t, 0, nil, func(d *Deps, o *Options) {
		o.Mode, o.HostHome, o.HostUser = ModeSelf, "/Users/you", "you"
		d.SandboxUserExists = func() bool { return false }
		d.RunAsGrant = func() bool { return false }
		d.DarwinUserTempDir = func() string { return userTmp + "/" }
		inner, agent := d.Run, d.RunWithProxy
		d.Run = func(argv []string) int {
			runs = append(runs, strings.Join(argv, " "))
			if inner != nil {
				return inner(argv)
			}
			return 0
		}
		d.RunWithProxy = func(argv []string) int {
			runs = append(runs, "AGENT")
			return agent(argv)
		}
	})
	mk, ag, rm := -1, -1, -1
	for i, r := range runs {
		switch {
		case strings.HasPrefix(r, mkdirBin+" -m 0700 "+userTmp+"/yolo-"):
			mk = i
		case r == "AGENT":
			ag = i
		case strings.HasPrefix(r, rmBin+" -rf "+userTmp+"/yolo-"):
			rm = i
		}
	}
	if mk < 0 || ag < 0 || rm < 0 || mk > ag || ag > rm {
		t.Errorf("want mkdir (%d) before the agent (%d) before rm -rf (%d):\n  %s\n%s",
			mk, ag, rm, strings.Join(runs, "\n  "), out.String())
	}
}

// OQ-MM8 (MMP-D18): a run-as-you launch whose user set the hatch says the shared /tmp is open,
// and one without it says nothing. Read through a real launch, so deleting Options.SharedTmp's
// handoff to the plan, or the disclosure call, fails this.
func TestASelfModeLaunchDisclosesTheSharedTmpHatch(t *testing.T) {
	for _, hatch := range []bool{true, false} {
		userTmp, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_, _, out, _ := runPackTreeLaunch(t, 0, nil, func(d *Deps, o *Options) {
			o.Mode, o.HostHome, o.HostUser, o.SharedTmp = ModeSelf, "/Users/you", "you", hatch
			d.SandboxUserExists = func() bool { return false }
			d.RunAsGrant = func() bool { return false }
			d.DarwinUserTempDir = func() string { return userTmp + "/" }
		})
		if got := strings.Contains(out.String(), "tmp: the shared /tmp is open"); got != hatch {
			t.Errorf("hatch %v: disclosure printed %v:\n%s", hatch, got, out.String())
		}
	}
}

// OQ-MM8 (MMP-D18): a run-as-you plan closes the shared /tmp, and the user's hatch
// (PlanSession.SharedTmp) opens it and is disclosed. The account mode ignores the hatch.
// Removing the hatch in selfProfileParams, or its disclosure, fails this.
func TestSelfModePlanClosesTheSharedTmpUnlessTheHatchIsSet(t *testing.T) {
	closed := sessionTmpPlan(t, ModeSelf, nil)
	if strings.Contains(closed.Seatbelt, SharedTmpAllowID) || closed.SharedTmp || sharedTmpDisclosure(closed) != "" {
		t.Errorf("a plan with no hatch opens or discloses the shared /tmp")
	}
	if !strings.Contains(closed.Seatbelt, "#seatbelt-test-id:"+UserTmpAllowID+"#") {
		t.Error("the run-as-you plan lost the user's DARWIN_USER_TEMP_DIR grant")
	}
	open := sessionTmpPlanWith(t, ModeSelf, nil, func(s *PlanSession) { s.SharedTmp = true })
	if !strings.Contains(open.Seatbelt, "#seatbelt-test-id:"+SharedTmpAllowID+"#") || !open.SharedTmp {
		t.Error("the hatch did not open the shared /tmp in the profile")
	}
	if line := sharedTmpDisclosure(open); !strings.Contains(line, "macos_user_shared_tmp") {
		t.Errorf("the hatch is not disclosed: %q", line)
	}
	if account := sessionTmpPlanWith(t, ModeAccount, nil, func(s *PlanSession) { s.SharedTmp = true }); account.SharedTmp {
		t.Error("the account mode took the run-as-you hatch")
	}
}

// The account mode is today's plan: no session TMPDIR, and its profile (the old one) carries no
// services grant.
func TestAccountModeHasNoSessionTmpdirOrServicesGrant(t *testing.T) {
	plan := sessionTmpPlan(t, ModeAccount, nil)
	if plan.SessionTmpDir != "" || len(plan.SessionTmpRemoveCommands) != 0 {
		t.Errorf("the account mode got a session TMPDIR: %q %v", plan.SessionTmpDir, plan.SessionTmpRemoveCommands)
	}
	for _, key := range []string{"TMPDIR", "TMUX_TMPDIR", "TMPPREFIX", "OVERMIND_SOCKET"} {
		if _, ok := sandboxEnvFileValue(plan.EnvFileContent, key); ok {
			t.Errorf("the account mode's env file sets %s", key)
		}
	}
	if strings.Contains(plan.Seatbelt, ServicesDirReadAllowID) {
		t.Error("the account mode's profile carries the services grant")
	}
}

// MMP-D21: the self-mode bootstrap is told the session's TMPDIR, since its env is `env -i`'s
// closed list and the profile closes the shared /tmp. The account mode's bootstrap is today's.
func TestSelfModeBootstrapGetsTheSessionTmpdir(t *testing.T) {
	if plan := sessionTmpPlan(t, ModeSelf, nil); !containsArg(plan.BootstrapArgv, "TMPDIR="+testSessionTmp) {
		t.Errorf("the self-mode bootstrap is not given TMPDIR=%s: %v", testSessionTmp, plan.BootstrapArgv)
	}
	if plan := sessionTmpPlan(t, ModeAccount, nil); slices.ContainsFunc(plan.BootstrapArgv,
		func(w string) bool { return strings.HasPrefix(w, "TMPDIR=") }) {
		t.Error("the account-mode bootstrap was given a TMPDIR")
	}
}

// MMP-D20: a TMPDIR the user's env composes outside every root the run-as-you profile grants
// refuses the launch, naming the fix; one inside a root, or under /tmp with the hatch, does not.
// Deleting the refusal from PlanInvariants fails this.
func TestSelfModeRefusesAComposedTmpdirTheProfileDoesNotGrant(t *testing.T) {
	plan := func(tmpdir string, hatch bool) RunPlan {
		env := jsonx.NewOrderedMap()
		env.Set("TMPDIR", tmpdir)
		return sessionTmpPlanWith(t, ModeSelf, env, func(s *PlanSession) { s.SharedTmp = hatch })
	}
	refused := func(p RunPlan) string {
		for _, msg := range PlanInvariants(p) {
			if strings.Contains(msg, "TMPDIR=") {
				return msg
			}
		}
		return ""
	}
	for _, tc := range []struct {
		tmpdir string
		hatch  bool
		want   string // a fragment of the refusal, or "" for none
	}{
		{"/Users/you/code/alpha/.tmp", false, ""},
		{testUserTmp + "/mine", false, ""},
		{"/var/folders/ab/xyz/T/mine", false, ""},
		{"/tmp/mine", true, ""},
		{"/Users/you/tmp", false, "Remove the TMPDIR override"},
		{"/tmp/mine", false, "`yolo config set macos_user_shared_tmp true --user`"},
		{"relative/tmp", false, "Remove the TMPDIR override"},
	} {
		got := refused(plan(tc.tmpdir, tc.hatch))
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("TMPDIR=%s hatch %v: refusal %q, want one containing %q", tc.tmpdir, tc.hatch, got, tc.want)
		}
	}
	// The account mode composes no session TMPDIR and checks none.
	env := jsonx.NewOrderedMap()
	env.Set("TMPDIR", "/Users/you/tmp")
	if p := sessionTmpPlan(t, ModeAccount, env); p.TmpdirProblem != "" {
		t.Errorf("the account mode refused a TMPDIR: %q", p.TmpdirProblem)
	}
}
