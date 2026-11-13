package openaiauthhost

// privatedaemon.go is the OpenAI credential daemon of a launch with NO JAIL: `yolo host -- codex`,
// `yolo host -- pi`, `yolo host -- opencode`, the services a `yolo host` launch starts, and
// `yolo openai-auth status|import|logout`.
//
// HD-R1 (docs/design/host-daemon-ownership.md) gives every host-side daemon to the launch that
// wanted it, and these launches are no exception: each spawns a daemon of its own, at sockets no
// other launch shares, and ends it with itself. They used to ensure the one machine-wide daemon
// at /tmp/yolo-openai-auth-broker.sock, and replace it when it lacked the private host socket,
// which reached the daemon every older jail on the machine was using.
//
// WHAT STAYS SHARED is the state, as everywhere else under HD-R1: the one credentials file and
// the refresh lock beside it, which serialize this daemon with every jail's. So two of these
// launches, or one beside a jail, need nothing serializing them; there is no shared socket left
// to own.
//
// HOW IT ENDS. The launch SIGTERMs it when it is done with it (stop), and the daemon finishes a
// refresh in flight before it exits (openaiauthdaemon.Main). A launch that execs its agent in
// its own place (pi, opencode) never gets to: the daemon is handed this process's pid
// (`--end-with-pid`), which the exec keeps, so it ends when the agent does, and removes its own
// sockets. A launch killed outright ends it the same way.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/logcap"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// privateStartTimeout bounds the wait for a spawned daemon's private socket to answer.
const privateStartTimeout = 5 * time.Second

// PrivateLogPath is the log a jail-less launch's daemon writes, the one every OpenAI daemon on
// the machine appends to (host-service-openai-auth-broker.log), so a reader finds them together.
func PrivateLogPath() string {
	return filepath.Join(paths.GlobalStorage(), "logs", "host-service-"+BrokerName+".log")
}

// privateSocket names a fronted socket no other launch uses: this process's pid and random hex,
// under paths.HostSingletonDir (/tmp in production, short enough for darwin's sun_path).
func privateSocket() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return filepath.Join(paths.HostSingletonDir,
		fmt.Sprintf("yolo-openai-host-%d-%s.sock", os.Getpid(), hex.EncodeToString(b))), nil
}

// HelperPin resolves the pinned yolo a launch starts its helpers from, called when the first helper
// is about to start (so a launch that starts none resolves none). An error is the launch's refusal.
type HelperPin func() (*helperpin.Pin, error)

// execFunc is the program a private daemon is exec'd from.
type execFunc func() (string, error)

// pinnedExec is the pin's path, after the stat check of docs/reference/pinned-host-helpers.md,
// "The check before every exec". No pin, or one whose file is gone or replaced, is an error and never a fallback to the
// running binary, which may be another build.
func pinnedExec(pin HelperPin) execFunc {
	return func() (string, error) {
		if pin == nil {
			return "", fmt.Errorf("this launch resolved no host yolo to start the OpenAI credential service from. %s",
				reporoot.ReinstallSteps)
		}
		p, err := pin()
		if err != nil {
			return "", err
		}
		if p == nil || p.Path == "" {
			return "", fmt.Errorf("this launch resolved no host yolo to start the OpenAI credential service from. %s",
				reporoot.ReinstallSteps)
		}
		if !p.Unchanged() {
			return "", fmt.Errorf("the yolo this launch pinned is no longer at %s (an upgrade removed or replaced it). %s",
				p.Path, reporoot.ReinstallSteps)
		}
		return p.Path, nil
	}
}

// invokedExec is the yolo the user just ran, for `yolo openai-auth`: a verb serves no jail and no
// launch, so it has no pin, and its daemon ends with it.
func invokedExec() (string, error) { return execx.SelfExecArgv([]string{"yolo"})[0], nil }

// startPrivate spawns this launch's own OpenAI credential daemon and returns its private
// host-to-host socket, once that answers, and the stop that ends it. stop is idempotent and
// does not wait: the daemon finishes a refresh in flight and exits on its own.
//
// exe names the program it runs and jail the jail or workspace it serves, for the daemon's start line.
func startPrivate(_ io.Writer, exe execFunc, jail string) (string, func(), error) {
	yolo, err := exe()
	if err != nil {
		return "", nil, err
	}
	fronted, err := privateSocket()
	if err != nil {
		return "", nil, fmt.Errorf("name the OpenAI credential service's socket: %w", err)
	}
	hostSocket := openaiauthdaemon.HostSocketPath(fronted)
	state := filepath.Join(loopholes.StateDirFor(BrokerName), "credentials.json")
	argv := []string{yolo, "internal", "daemon", BrokerName,
		"--socket", fronted, "--state-file", state, "--end-with-pid", strconv.Itoa(os.Getpid())}
	logPath := PrivateLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	cmd := exec.Command(argv[0], argv[1:]...)
	if lf, lerr := logcap.Open(logPath, 0o644); lerr == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if jail != "" {
		cmd.Env = append(os.Environ(), helperpin.EnvJail+"="+jail)
	}
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start the OpenAI credential service: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	pid := cmd.Process.Pid
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		// Discarded: ESRCH means it already exited, which is what was asked.
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	if err := waitPrivate(hostSocket, exited, openauthclient.RequestUnix); err != nil {
		stop()
		return "", nil, fmt.Errorf("the OpenAI credential service did not start (%v); see %s", err, logPath)
	}
	return hostSocket, stop, nil
}

// waitPrivate waits for the daemon's private socket to answer a ping, reporting at once a daemon
// that exited, within privateStartTimeout.
func waitPrivate(socket string, exited <-chan struct{}, request requestFunc) error {
	deadline := time.Now().Add(privateStartTimeout)
	for {
		_, err := request(socket, map[string]any{"action": "ping"}, io.Discard)
		if err == nil {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("it exited at startup")
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("its socket did not answer within %s: %w", privateStartTimeout, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
