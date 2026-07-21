package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// This file holds the production ("real") backing for the Deps seams —
// subprocess / filesystem / platform probes. They mirror the private helpers in
// macos_user.py (_is_macos, _host_user, _sandbox_user_exists, _git_config,
// _install_root_file, _taken_ids, _set_random_password) and the run path's
// subprocess.run calls. All are macOS-relevant at runtime but COMPILE on every
// GOOS (pure os/exec), so `GOOS=darwin go build ./...` and Linux CI both pass.

func isMacOSReal() bool { return runtime.GOOS == "darwin" }

// selfExeReal returns the running yolo binary path (os.Executable), staged for
// the sandbox to self-exec as the bootstrap. Falls back to "yolo" (resolved off
// PATH) only if os.Executable fails — the plan invariant flags an unstaged path.
func selfExeReal() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "yolo"
}

func whichReal(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// sandboxUserExistsReal `id <user>` returns 0
// (timeout 5s → False).
func sandboxUserExistsReal(user string) bool {
	cmd := exec.Command("id", user)
	return runWithTimeout(cmd, 5*time.Second) == 0
}

// gitConfigReal `git config --get <key>` (timeout 5s),
// stdout trimmed; "" + false when unset/empty/error.
func gitConfigReal(key string) (string, bool) {
	cmd := exec.Command("git", "config", "--get", key)
	out, rc := outputWithTimeout(cmd, 5*time.Second)
	if rc != 0 {
		return "", false
	}
	val := strings.TrimSpace(out)
	if val == "" {
		return "", false
	}
	return val, true
}

func hostUserReal() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return ""
}

// runReal runs argv inheriting stdio and returns the returncode (subprocess.run
// with no capture). A start failure yields 1 (Python would raise; the call
// sites treat non-zero as failure).
func runReal(argv []string) int {
	if len(argv) == 0 {
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return exitCodeOf(err)
	}
	return 0
}

// runBashReal runs `bash -c <script>` inheriting stdio; returns the returncode.
func runBashReal(script string) int {
	return runReal([]string{"bash", "-c", script})
}

// tee <path> (content on stdin, stdout to /dev/null), sudo chmod <mode> <path>.
// Any failure → false.
func installRootFileReal(path, content, mode string) bool {
	parent := filepath.Dir(path)
	if runReal([]string{"sudo", mkdirBin, "-p", parent}) != 0 {
		return false
	}
	tee := exec.Command("sudo", teeBin, path)
	tee.Stdin = strings.NewReader(content)
	tee.Stdout = nil // subprocess.DEVNULL
	tee.Stderr = os.Stderr
	if err := tee.Run(); err != nil {
		return false
	}
	return runReal([]string{"sudo", chmodBin, mode, path}) == 0
}

// GIDs (Groups/PrimaryGroupID) via dscl, timeout 10s each. Best-effort.
func takenIDsReal() map[int]struct{} {
	ids := map[int]struct{}{}
	for _, kv := range [][2]string{{"Users", "UniqueID"}, {"Groups", "PrimaryGroupID"}} {
		cmd := exec.Command("dscl", ".", "-list", "/"+kv[0], kv[1])
		out, rc := outputWithTimeout(cmd, 10*time.Second)
		if rc != 0 {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}
			last := parts[len(parts)-1]
			if isDigits(strings.TrimLeft(last, "-")) {
				if n, err := strconv.Atoi(last); err == nil {
					ids[n] = struct{}{}
				}
			}
		}
	}
	return ids
}

// then `sudo /bin/sh -c 'dscl . -passwd /Users/<u> "$YOLO_SBPW"'` with the
// password passed via an env var (never argv, so it can't show in `ps`).
func setRandomPasswordReal(user string) bool {
	rand := exec.Command("openssl", "rand", "-base64", "32")
	pwOut, rc := outputWithTimeout(rand, 5*time.Second)
	if rc != 0 {
		return false
	}
	pw := strings.TrimSpace(pwOut)
	cmd := exec.Command("sudo", "/bin/sh", "-c",
		"dscl . -passwd /Users/"+user+" \"$YOLO_SBPW\"")
	cmd.Env = []string{"YOLO_SBPW=" + pw, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run() == nil
}

func pathIsDirReal(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func pathExistsReal(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// --- small subprocess helpers ---------------------------------------------

func runWithTimeout(cmd *exec.Cmd, d time.Duration) int {
	if err := cmd.Start(); err != nil {
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return 1
	case err := <-done:
		return exitCodeOf(err)
	}
}

func outputWithTimeout(cmd *exec.Cmd, d time.Duration) (string, int) {
	var buf strings.Builder
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return "", 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return buf.String(), 1
	case err := <-done:
		return buf.String(), exitCodeOf(err)
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
