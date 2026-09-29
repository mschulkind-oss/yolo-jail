package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// THE LINUX PREFLIGHT for TestMacosUserJailDaemonRunsConfinedInTheGuest: its probe script, the
// parser for the script's output, and the host-listing matcher run here, on every machine,
// because the one machine that runs the test proper is a Mac hours into a CI run.

// The first Mac run's (36575801495) own output shape: the in-sandbox `ps` listed the probe's
// bash, whose argv quotes the script with every marker in it, and section() cut the listing
// at the "=== END ===" INSIDE that argv. The parser refuses the output instead of returning a
// section that looks like an answer.
func TestMacosUserJailDaemonProbeRefusesAMarkerItsOwnOutputQuotes(t *testing.T) {
	script := jailDaemonProbeScript("/w/.seen")
	quoted := "_yolojail        bash -lc " + strings.ReplaceAll(script, "\n", `\012`)
	stdout := strings.Join([]string{
		jdProbeMarker, "JAILD=/var/yolo-jail/bin/yolo-jaild",
		jdLogMarker, "serving on 127.0.0.1:1460",
		jdSeenMarker, quoted,
		jdEndMarker, "",
	}, "\n")
	if _, err := parseJailDaemonProbe(stdout); err == nil || !strings.Contains(err.Error(), "appears") {
		t.Fatalf("output whose body quotes the markers parsed (err %v); section() would have cut "+
			"it at the first quoted marker and read the stub as a result", err)
	}

	clean := strings.Join([]string{
		jdProbeMarker, "JAILD=/var/yolo-jail/bin/yolo-jaild",
		jdLogMarker, "serving on 127.0.0.1:1460",
		jdSeenMarker, "399 812 /var/yolo-jail/bin/yolo-jaild supervise",
		jdEndMarker, "",
	}, "\n")
	p, err := parseJailDaemonProbe(clean)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.probe, "JAILD=/var/yolo-jail/bin/yolo-jaild") ||
		!strings.Contains(p.log, "serving on") ||
		!strings.Contains(p.seen, "/var/yolo-jail/bin/yolo-jaild supervise") {
		t.Errorf("the sections are not what the output says: %+v", p)
	}
	if _, err := parseJailDaemonProbe(strings.Replace(clean, jdSeenMarker, "", 1)); err == nil {
		t.Error("output missing a marker parsed")
	}
}

// The probe itself, run by bash against a fake sandbox home: the quoting survives, it waits
// for and echoes the host's answer, and its output carries each marker exactly once — which
// is what the old probe's in-sandbox `ps` broke.
func TestMacosUserJailDaemonProbeScriptRunsAndParses(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	home := t.TempDir()
	logDir := filepath.Join(home, ".local", "state", "yolo-jail-daemons")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "openai-auth-broker.log"),
		[]byte("openai-auth-adapter: serving on 127.0.0.1:41460\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(t.TempDir(), "it's seen") // a quote in the path, on purpose
	hostLine := "399 812 " + macosuser.GuestBinaryPath(macosuser.JaildName, "") + " supervise"
	if err := os.WriteFile(seen, []byte(hostLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bash, "-c", jailDaemonProbeScript(seen))
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe script failed: %v\n%s", err, out)
	}
	p, err := parseJailDaemonProbe(string(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(p.probe, "JAILD=") || !strings.Contains(p.log, "serving on") ||
		!strings.Contains(p.seen, hostLine) {
		t.Errorf("the probe did not print what the fake home and the host's answer hold:\n%s", out)
	}
}

// The host listing's supervisor is the yolo-jaild the sandbox account runs, never the root
// sudo whose argv spells the same words, the adapter the supervisor started, or the agent's
// shell.
func TestMacosUserGuestSupervisorLineIsTheSandboxJaildNotItsSudo(t *testing.T) {
	jaild := macosuser.GuestBinaryPath(macosuser.JaildName, "")
	listing := strings.Join([]string{
		"    0   801 sudo -n --set-home --user=_yolojail /usr/bin/env -i HOME=/Users/_yolojail " +
			"/usr/bin/sandbox-exec -f /var/yolo-jail/profiles/x.sb -- /bin/sh -c . \"$1\" yolo-sandbox-env " +
			"/var/yolo-jail/env/x.daemons.env " + jaild + " supervise",
		"  399   812 " + jaild + " supervise",
		"  399   813 yolo-jaild openai-auth-adapter --listen 127.0.0.1:41460",
		"  399   900 bash -lc echo " + jaild + " supervise",
		"  501   901 /bin/ps -axww -o uid=,pid=,command=",
	}, "\n")
	if got := guestSupervisorLine(listing, "399"); got != "399   812 "+jaild+" supervise" {
		t.Errorf("guestSupervisorLine = %q, want the uid-399 %s supervise line", got, jaild)
	}
	if got := guestSupervisorLine(listing, "501"); got != "" {
		t.Errorf("another uid's listing matched: %q", got)
	}
	without := strings.Replace(listing, "  399   812 "+jaild+" supervise\n", "", 1)
	if got := guestSupervisorLine(without, "399"); got != "" {
		t.Errorf("with no supervisor running, %q matched", got)
	}
	rel := relevantListing(listing, "399")
	if !strings.Contains(rel, "sudo -n") || !strings.Contains(rel, "openai-auth-adapter") ||
		strings.Contains(rel, "/bin/ps") {
		t.Errorf("the failure listing is not the sandbox's processes plus the sudo naming "+
			"yolo-jaild:\n%s", rel)
	}
}
