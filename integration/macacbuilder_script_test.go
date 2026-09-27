package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// acBuilderScriptFakes writes stand-ins for the commands scripts/mac-ac-linux-builder.sh
// calls, so the script runs on any Linux box: `container` answers as a running Apple
// Container with the builder up at 192.168.64.9 and records every argv, and `ssh-keyscan`
// returns a host key. execFails makes `container exec` exit 1.
func acBuilderScriptFakes(t *testing.T, execFails bool) (bin, home, argvLog string) {
	t.Helper()
	bin, home = t.TempDir(), t.TempDir()
	argvLog = filepath.Join(bin, "container.argv")
	keydir := filepath.Join(home, ".local/share/yolo-jail/ac-builder")
	if err := os.MkdirAll(keydir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"builder_key", "builder_key.pub"} {
		if err := os.WriteFile(filepath.Join(keydir, f), []byte("fake-key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	execExit := "0"
	if execFails {
		execExit = "1"
	}
	container := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + argvLog + "'\n" +
		"case \"$1\" in\n" +
		"  system) exit 0 ;;\n" +
		"  image) echo 'ghcr.io/mschulkind-oss/yolo-jail-builder latest abc'; exit 0 ;;\n" +
		"  ls) echo 'ID IMAGE OS ARCH STATE ADDR'; echo 'yolo-ac-builder img linux arm64 running 192.168.64.9/24'; exit 0 ;;\n" +
		"  exec) exit " + execExit + " ;;\n" +
		"esac\n" +
		"exit 0\n"
	keyscan := "#!/bin/sh\necho \"$1 ssh-ed25519 AAAAC3NzaFAKEHOSTKEY\"\n"
	for name, body := range map[string]string{"container": container, "ssh-keyscan": keyscan} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, home, argvLog
}

func runACBuilderScript(t *testing.T, bin, home string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "mac-ac-linux-builder.sh"))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+home)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// TestTheACBuilderScriptClearsHomelessShelter pins the fix for run 36298992896: the builder
// runs nix unsandboxed and is reused across builds, and nix refuses every build while
// /homeless-shelter exists, so the script clears it on each use. stdout must still be the
// builders spec alone, since callers substitute it directly.
func TestTheACBuilderScriptClearsHomelessShelter(t *testing.T) {
	bin, home, argvLog := acBuilderScriptFakes(t, false)
	stdout, stderr, err := runACBuilderScript(t, bin, home)
	if err != nil {
		t.Fatalf("the script failed: %v\nstderr:\n%s", err, stderr)
	}
	argv, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(argv), "exec yolo-ac-builder rm -rf /homeless-shelter") {
		t.Errorf("the script never cleared /homeless-shelter in the builder; container calls:\n%s", argv)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "ssh-ng://root@192.168.64.9 aarch64-linux ") {
		t.Errorf("stdout must be exactly the builders spec, got:\n%s", stdout)
	}
}

// TestTheACBuilderScriptWarnsWhenItCannotClearHomelessShelter: a failed clear is not fatal
// (the build then names the problem itself), but it is never silent.
func TestTheACBuilderScriptWarnsWhenItCannotClearHomelessShelter(t *testing.T) {
	bin, home, _ := acBuilderScriptFakes(t, true)
	stdout, stderr, err := runACBuilderScript(t, bin, home)
	if err != nil {
		t.Fatalf("a failed clear must not stop the script: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "could not clear /homeless-shelter") {
		t.Errorf("a failed clear must be named on stderr; stderr:\n%s", stderr)
	}
	if !strings.HasPrefix(stdout, "ssh-ng://root@192.168.64.9 ") {
		t.Errorf("the spec must still print after a failed clear, got:\n%s", stdout)
	}
}
