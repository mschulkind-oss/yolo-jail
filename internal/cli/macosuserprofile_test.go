package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The inputs are paths that exist on no machine running this test, so physicalPath keeps them
// as given and the comparison does not depend on where t.TempDir() resolves (AGENTS.md, the
// darwin PATH-RESOLUTION class).
const (
	profileTestWS     = "/Users/you/code/alpha"
	profileTestTmp    = "/private/var/folders/ab/xyz/T/yolo-alpha"
	profileTestDarwin = "/private/var/folders/ab/xyz/T"
	profileTestHost   = "/Users/you"
)

func profileTestEnv() macosUserProfileEnv {
	return macosUserProfileEnv{
		home:        func() string { return profileTestHost },
		currentUser: func() string { return "you" },
		darwinTmp:   func() string { return profileTestDarwin },
	}
}

func runProfileVerb(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	rc := macosUserProfileMain(args, &out, &errb, profileTestEnv())
	return out.String(), errb.String(), rc
}

// profileTestParams is what the verb must render for the base arguments below.
func profileTestParams() macosuser.ProfileParams {
	return macosuser.ProfileParams{
		Workspace:         profileTestWS,
		Home:              paths.SetupHomeState(profileTestWS, "seatbelt-self"),
		HostHome:          profileTestHost,
		SandboxTmp:        profileTestTmp,
		DarwinUserTempDir: profileTestDarwin,
		StagingRoot:       macosuser.StagingRoot(),
		RunAsUser:         "you",
		ServicesDirs:      []string{profileTestServices},
	}.WithProductionRulings()
}

var profileTestArgs = []string{"--mode", "self", "--workspace", profileTestWS, "--tmp", profileTestTmp,
	"--services-dir", profileTestServices}

// profileTestServices is a session's host-services dir, granted read-only by --services-dir.
const profileTestServices = "/private/tmp/yolo-host-services-1a2b3c4d-987654"

func TestMacosUserProfileVerbPrintsTheGeneratedProfile(t *testing.T) {
	out, errOut, rc := runProfileVerb(t, profileTestArgs...)
	if rc != 0 {
		t.Fatalf("exit %d: %s", rc, errOut)
	}
	if want := macosuser.DenyDefaultProfile(profileTestParams()); out != want {
		t.Errorf("the verb's profile differs from DenyDefaultProfile for the same inputs.\n--- got\n%s\n--- want\n%s", out, want)
	}
}

func TestMacosUserProfileVerbAccountModeNamesTheAccountOnly(t *testing.T) {
	args := append([]string{}, profileTestArgs...)
	args[1] = "account"
	out, errOut, rc := runProfileVerb(t, args...)
	if rc != 0 {
		t.Fatalf("exit %d: %s", rc, errOut)
	}
	pp := profileTestParams()
	pp.RunAsUser = macosuser.SandboxUser
	// And --home's default is the account setup's home, not the self setup's.
	pp.Home = paths.SetupHomeState(profileTestWS, "seatbelt-yolo-account")
	if want := macosuser.DenyDefaultProfile(pp); out != want {
		t.Errorf("account mode must change only the header's user and the default home.\n--- got\n%s\n--- want\n%s", out, want)
	}
}

func TestMacosUserProfileVerbOverridesEachPendingRuling(t *testing.T) {
	args := append(append([]string{}, profileTestArgs...),
		"--shared-tmp", "shared", "--host-software", "/opt/homebrew",
		"--home", "/Users/you/agent-home", "--host-home", "/Users/other", "--darwin-tmp", "/private/var/folders/cd/T")
	out, errOut, rc := runProfileVerb(t, args...)
	if rc != 0 {
		t.Fatalf("exit %d: %s", rc, errOut)
	}
	pp := profileTestParams()
	pp.Tmp = macosuser.TmpShared
	pp.HostSoftware = []string{"/opt/homebrew"}
	pp.Home = "/Users/you/agent-home"
	pp.HostHome = "/Users/other"
	pp.DarwinUserTempDir = "/private/var/folders/cd/T"
	if want := macosuser.DenyDefaultProfile(pp); out != want {
		t.Errorf("the overrides did not reach the profile.\n--- got\n%s\n--- want\n%s", out, want)
	}

	out, _, rc = runProfileVerb(t, append(append([]string{}, profileTestArgs...), "--no-host-software")...)
	if rc != 0 {
		t.Fatalf("exit %d", rc)
	}
	pp = profileTestParams()
	pp.HostSoftware = nil
	if want := macosuser.DenyDefaultProfile(pp); out != want {
		t.Errorf("--no-host-software did not hide every tree.\n--- got\n%s\n--- want\n%s", out, want)
	}
}

func TestMacosUserProfileVerbRefusesMisuseNamingTheFix(t *testing.T) {
	for name, args := range map[string][]string{
		"no mode":               {"--workspace", profileTestWS, "--tmp", profileTestTmp},
		"bad mode":              {"--mode", "guest", "--workspace", profileTestWS, "--tmp", profileTestTmp},
		"no workspace":          {"--mode", "self", "--tmp", profileTestTmp},
		"no tmp":                {"--mode", "self", "--workspace", profileTestWS},
		"retired loopback flag": append(append([]string{}, profileTestArgs...), "--loopback", "deny"),
		"bad tmp":               append(append([]string{}, profileTestArgs...), "--shared-tmp", "public"),
		"contradiction": append(append([]string{}, profileTestArgs...),
			"--no-host-software", "--host-software", "/opt/homebrew"),
		"stray argument": append(append([]string{}, profileTestArgs...), "extra"),
	} {
		t.Run(name, func(t *testing.T) {
			out, errOut, rc := runProfileVerb(t, args...)
			if rc != 2 || out != "" {
				t.Errorf("exit %d with stdout %q; want 2 and nothing on stdout", rc, out)
			}
			if !strings.Contains(errOut, "usage: yolo internal "+macosUserProfileVerb) {
				t.Errorf("the refusal does not print the usage:\n%s", errOut)
			}
		})
	}
}

// TestMacosUserProfileVerbIsDispatchedByRunInternal pins the call site: deleting the
// runInternal arm turns this red, whatever macosUserProfileMain still does.
func TestMacosUserProfileVerbIsDispatchedByRunInternal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); read <- b }()
	saved := os.Stdout
	os.Stdout = w
	rc := runInternal(append([]string{macosUserProfileVerb, "--darwin-tmp", profileTestDarwin,
		"--host-home", profileTestHost}, replaceMode(profileTestArgs, "account")...))
	os.Stdout = saved
	_ = w.Close()
	out := <-read
	if rc != 0 {
		t.Fatalf("yolo internal %s exited %d", macosUserProfileVerb, rc)
	}
	pp := profileTestParams()
	pp.RunAsUser = macosuser.SandboxUser
	// And --home's default is the account setup's home, not the self setup's.
	pp.Home = paths.SetupHomeState(profileTestWS, "seatbelt-yolo-account")
	if want := macosuser.DenyDefaultProfile(pp); string(out) != want {
		t.Errorf("runInternal did not print the generated profile.\n--- got\n%s", out)
	}
}

func replaceMode(args []string, mode string) []string {
	out := append([]string{}, args...)
	out[1] = mode
	return out
}
