//go:build linux

package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// AT A TERMINAL, a login-only opt-in (YOLO_AUTH_PRELAUNCH_<BIN>_LOGIN, ES-D28) that finds no
// login starts one, then asks for the token again, all with no view flag: token, login, token.
// The fake yolo fails the first token call only. Linux-only for the pty, opened from
// /dev/ptmx as provision's tty tests do, since the repo vendors no pty library.
func TestAuthPrelaunchLoginOnlyLogsInAtATerminal(t *testing.T) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer m.Close()
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	defer slave.Close()

	home := t.TempDir()
	binDir := filepath.Join(home, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "yolo-calls.log")
	fake := "#!/usr/bin/env bash\n" +
		"printf '%s\\n' \"$*\" >> " + shellSingleQuote(log) + "\n" +
		"case \"$*\" in\n" +
		"  *' token') [ \"$(wc -l < " + shellSingleQuote(log) + ")\" -gt 1 ]; exit $? ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", "set -euo pipefail\nBIN=claude\n"+agentAuthPrelaunchShellFn)
	cmd.Dir = home
	cmd.Stdin = slave
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home, "YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the prelaunch failed: %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(log)
	want := "internal openai-auth-client token\ninternal openai-auth-client login\ninternal openai-auth-client token\n"
	if string(calls) != want {
		t.Errorf("calls = %q, want %q\noutput:\n%s", calls, want, out)
	}
	if !strings.Contains(string(out), "claude: OpenAI login is required.") {
		t.Errorf("the prelaunch must say it is starting a login:\n%s", out)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a login-only prelaunch wrote into the home: %v", names)
	}
}
