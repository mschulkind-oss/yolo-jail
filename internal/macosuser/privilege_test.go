package macosuser

import (
	"os"
	"strings"
	"testing"
)

func TestTheRunAsPrefixIsTheRuledRuleShape(t *testing.T) {
	want := "/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true"
	if got := strings.Join(RunAsGrantProbeArgv(), " "); got != want {
		t.Errorf("G2 probe = %q, want %q", got, want)
	}
	if got := strings.Join(RunAsSandboxArgv(nil, "/usr/bin/true"), " "); got != want {
		t.Errorf("RunAsSandboxArgv = %q, want %q", got, want)
	}
	got := strings.Join(RunAsSandboxArgv([]string{"--set-home"}, "-i", "HOME=/h"), " ")
	if got != "/usr/bin/sudo -n --set-home --user=_yolojail /usr/bin/env -i HOME=/h" {
		t.Errorf("with --set-home: %q", got)
	}
}

func TestRunAsArgvProblemsAcceptsOnlyTheRule(t *testing.T) {
	ok := [][]string{
		RunAsGrantProbeArgv(),
		{"/usr/bin/sudo", "-n", "--set-home", "--user=_yolojail", "/usr/bin/env", "-i", "A=1", "/bin/zsh"},
		{"/bin/rm", "-f", "/x"}, // not a sudo argv: not this check's business
	}
	for _, a := range ok {
		if p := RunAsArgvProblems("t", a); len(p) > 0 {
			t.Errorf("%q: %v", a, p)
		}
	}
	bad := [][]string{
		{"sudo", "-n", "--user=_yolojail", "/usr/bin/env", "/usr/bin/true"},       // sudo off PATH
		{"/usr/bin/sudo", "--user=_yolojail", "/usr/bin/env", "/usr/bin/true"},    // may prompt
		{"/usr/bin/sudo", "-n", "--user=_yolojail", "/bin/test", "-r", "/x"},      // not /usr/bin/env
		{"/usr/bin/sudo", "-n", "/bin/rm", "-f", "/x"},                            // root
		{"/usr/bin/sudo", "-n", "--user=root", "/usr/bin/env", "/usr/bin/true"},   // root
		{"/usr/bin/sudo", "-n", "-E", "--user=_yolojail", "/usr/bin/env", "true"}, // SETENV
		{"/usr/bin/sudo", "-n", "--login", "--user=_yolojail", "/usr/bin/env", "x"},
	}
	for _, a := range bad {
		if p := RunAsArgvProblems("t", a); len(p) == 0 {
			t.Errorf("%q passed", a)
		}
	}
}

func TestTheSudoersRuleIsExactlyTheRuledOne(t *testing.T) {
	got, err := SudoersRule("matt")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Written by `yolo yolo-account create` for matt. Lets matt run commands as _yolojail,\n" +
		"# never as root, without a password. `yolo yolo-account remove` removes it; setup rewrites it.\n" +
		"Defaults>_yolojail secure_path=\"/usr/bin:/bin:/usr/sbin:/sbin\"\n" +
		"matt ALL=(_yolojail) NOPASSWD: /usr/bin/env\n"
	if got != want {
		t.Errorf("rule =\n%s\nwant\n%s", got, want)
	}
	if SudoersFile != "/etc/sudoers.d/yolo-jail" || strings.Contains(SudoersFile[len("/etc/sudoers.d/"):], ".") {
		t.Errorf("SudoersFile %q: sudo ignores a sudoers.d name with a dot", SudoersFile)
	}
	for _, u := range []string{"", "root", "ALL", "%admin", "a b", "a,b", "m\natt", "_yolojail", "1abc"} {
		if _, err := SudoersRule(u); err == nil {
			t.Errorf("SudoersRule(%q) accepted", u)
		}
	}
}

func TestJudgeStagingRoot(t *testing.T) {
	const me = 501
	for _, c := range []struct {
		name string
		f    StagingRootFacts
		want StagingRootVerdict
	}{
		{"absent", StagingRootFacts{}, StagingRootAbsent},
		{"unreadable", StagingRootFacts{Err: os.ErrPermission}, StagingRootUnknown},
		{"mine", StagingRootFacts{Exists: true, IsDir: true, UID: me, Mode: 0o755}, StagingRootOK},
		{"squatted", StagingRootFacts{Exists: true, IsDir: true, UID: 602, Mode: 0o755}, StagingRootForeign},
		{"link", StagingRootFacts{Exists: true, IsSymlink: true, UID: me}, StagingRootUnsafe},
		{"file", StagingRootFacts{Exists: true, UID: me, Mode: 0o644}, StagingRootUnsafe},
		{"group-writable", StagingRootFacts{Exists: true, IsDir: true, UID: me, Mode: 0o775}, StagingRootUnsafe},
		{"acl", StagingRootFacts{Exists: true, IsDir: true, UID: me, Mode: 0o755, HasACL: true}, StagingRootUnsafe},
	} {
		if got := JudgeStagingRoot(c.f, me); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if StagingRoot() != "/Users/Shared/yolo-staged" || LegacyStateDir() != "/var/yolo-jail" {
		t.Errorf("roots: %q, %q", StagingRoot(), LegacyStateDir())
	}
}
