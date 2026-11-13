package macosuser

// seatbeltdisclosure_test.go pins §8.3 of docs/research/agent-safehouse.md through
// RunMacosUser: a live launch names the Seatbelt profile it installs, every time, and prints it
// whole under --verbose (YOLO_VERBOSE, which the CLI's --verbose sets).

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// seatbeltLaunch runs one mocked launch, verbose or not, and returns its output and the
// profile it wrote (the file written at the plan's profile path).
func seatbeltLaunch(t *testing.T, verbose bool) (out, profilePath, profile string) {
	t.Helper()
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	d.Getenv = func(k string) string {
		if verbose && k == paths.VerboseEnv {
			return "1"
		}
		return ""
	}
	write := d.WriteHostFile
	d.WriteHostFile = func(path, content string, mode os.FileMode) bool {
		if strings.Contains(content, "(version 1)") {
			profilePath, profile = path, content
		}
		return write(path, content, mode)
	}
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d, want 42\n%s", rc, buf.String())
	}
	if profilePath == "" {
		t.Fatalf("the launch wrote no Seatbelt profile; output:\n%s", buf.String())
	}
	return buf.String(), profilePath, profile
}

func TestALiveLaunchNamesItsSeatbeltProfile(t *testing.T) {
	out, path, profile := seatbeltLaunch(t, false)
	if want := "sandbox: Seatbelt profile " + path + " ("; !strings.Contains(out, want) {
		t.Fatalf("the launch does not name its profile (want %q):\n%s", want, out)
	}
	if !strings.Contains(out, "--verbose prints it in full") {
		t.Errorf("the line does not say how to see the profile:\n%s", out)
	}
	if strings.Contains(out, strings.TrimRight(profile, "\n")) {
		t.Errorf("the profile itself was printed without --verbose")
	}
}

func TestAVerboseLaunchPrintsItsSeatbeltProfile(t *testing.T) {
	out, path, profile := seatbeltLaunch(t, true)
	if !strings.Contains(out, "sandbox: Seatbelt profile "+path) {
		t.Fatalf("the verbose launch does not name its profile:\n%s", out)
	}
	if !strings.Contains(out, strings.TrimRight(profile, "\n")) {
		t.Errorf("the verbose launch does not print the profile it installed")
	}
}

func TestSeatbeltDisclosureNamesThePosture(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"(version 1)\n(deny default)\n(allow file-read*)\n", "deny-default, 3 lines"},
		{"(version 1)\n(allow default)\n", "allow-default with targeted denies, 2 lines"},
	} {
		got := seatbeltDisclosure(RunPlan{ProfilePath: "/p.sb", Seatbelt: tc.body}, false)
		if !strings.Contains(got, tc.want) {
			t.Errorf("disclosure %q lacks %q", got, tc.want)
		}
	}
}
