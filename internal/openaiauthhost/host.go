// Package openaiauthhost prepares managed host Codex and Pi launches that use
// yolo's machine-wide OpenAI subscription credential service.
package openaiauthhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const BrokerName = "openai-auth-broker"

type requestFunc func(string, any, io.Writer) (json.RawMessage, error)

type deps struct {
	ensure  func(io.Writer) (string, error)
	request requestFunc
	listen  func(string, string) (net.Listener, error)
	home    func() string
	storage func() string
}

// Launch carries environment overrides and, for Codex, the loopback adapter
// whose lifetime must follow the agent process.
type Launch struct {
	vars       map[string]string
	listener   net.Listener
	adapterEnd <-chan error
}

// Prepare returns nil for commands that do not consume the shared OpenAI login.
func Prepare(agent string, stderr io.Writer) (*Launch, error) {
	d := deps{
		ensure: ensureSingleton, request: openauthclient.RequestUnix,
		listen: net.Listen, home: paths.Home, storage: paths.GlobalStorage,
	}
	return prepare(d, filepath.Base(agent), stderr)
}

func prepare(d deps, agent string, stderr io.Writer) (*Launch, error) {
	if agent != "codex" && agent != "pi" {
		return nil, nil
	}
	socket, err := d.ensure(stderr)
	if err != nil {
		return nil, err
	}
	if err := ensureLogin(socket, d.request, stderr); err != nil {
		return nil, err
	}
	launch := &Launch{vars: map[string]string{openauthclient.HostSocketEnv: socket}}
	if agent == "pi" {
		return launch, nil
	}

	managedHome := filepath.Join(d.storage(), "host-agents", "codex")
	response, err := d.request(socket, map[string]any{"action": "token", "view": "codex"}, stderr)
	if err != nil {
		return nil, err
	}
	if err := prepareCodexHome(managedHome, filepath.Join(d.home(), ".codex"), response); err != nil {
		return nil, err
	}
	listener, err := d.listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start managed Codex credential adapter: %w", err)
	}
	end := make(chan error, 1)
	refresh := func(_ context.Context, marker string) (openaiauthadapter.Token, error) {
		raw, err := d.request(socket, map[string]any{"action": "refresh", "refresh_token": marker}, stderr)
		if err != nil {
			return openaiauthadapter.Token{}, err
		}
		var token openaiauthadapter.Token
		if err := json.Unmarshal(raw, &token); err != nil {
			return token, fmt.Errorf("decode managed Codex refresh: %w", err)
		}
		return token, nil
	}
	go func() { end <- openaiauthadapter.Serve(listener, refresh) }()
	launch.listener = listener
	launch.adapterEnd = end
	launch.vars["CODEX_HOME"] = managedHome
	launch.vars["CODEX_REFRESH_TOKEN_URL_OVERRIDE"] = "http://" + listener.Addr().String() + "/oauth/token"
	return launch, nil
}

func ensureLogin(socket string, request requestFunc, stderr io.Writer) error {
	raw, err := request(socket, map[string]any{"action": "status"}, stderr)
	var status struct {
		LoggedIn      bool `json:"logged_in"`
		LoginRequired bool `json:"login_required"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &status)
	}
	if err == nil && status.LoggedIn && !status.LoginRequired {
		return nil
	}
	_, loginErr := request(socket, map[string]any{"action": "login"}, stderr)
	if loginErr != nil {
		return fmt.Errorf("OpenAI browser login: %w", loginErr)
	}
	return nil
}

func prepareCodexHome(managed, ordinary string, response json.RawMessage) error {
	if err := os.MkdirAll(managed, 0o700); err != nil {
		return fmt.Errorf("create managed Codex home: %w", err)
	}
	if err := os.Chmod(managed, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"config.toml", "AGENTS.md", "skills"} {
		source, destination := filepath.Join(ordinary, name), filepath.Join(managed, name)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if target, _ := os.Readlink(destination); target == source {
					continue
				}
			}
			return fmt.Errorf("managed Codex path already exists: %s", destination)
		}
		if err := os.Symlink(source, destination); err != nil {
			return fmt.Errorf("link managed Codex %s: %w", name, err)
		}
	}
	return openauthclient.WriteCodexAuth(filepath.Join(managed, "auth.json"), response)
}

// Environ applies this managed launch's overrides to a base environment.
func (l *Launch) Environ(base []string) []string {
	if l == nil {
		return base
	}
	out := make([]string, 0, len(base)+len(l.vars))
	for _, item := range base {
		key := strings.SplitN(item, "=", 2)[0]
		if _, replaced := l.vars[key]; !replaced {
			out = append(out, item)
		}
	}
	keys := make([]string, 0, len(l.vars))
	for key := range l.vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := l.vars[key]
		out = append(out, key+"="+value)
	}
	return out
}

// Run supervises a managed Codex process and closes its dynamic adapter as soon
// as the agent exits. Pi launches have no child service and should still exec.
func (l *Launch) Run(target string, argv, environ []string, stdin io.Reader, stdout, stderr io.Writer) (int, bool) {
	if l == nil || l.listener == nil {
		return 0, false
	}
	defer func() {
		_ = l.listener.Close()
		if l.adapterEnd != nil {
			<-l.adapterEnd
		}
	}()
	cmd := exec.Command(target, argv[1:]...)
	cmd.Args = argv
	cmd.Env = environ
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal()), true
			}
			return exitErr.ExitCode(), true
		}
		fmt.Fprintf(stderr, "yolo host: run %s: %v\n", target, err)
		return 126, true
	}
	return 0, true
}

func ensureSingleton(stderr io.Writer) (string, error) {
	fronted := paths.HostSingletonSocket(BrokerName)
	hostSocket := openaiauthdaemon.HostSocketPath(fronted)
	state := filepath.Join(loopholes.StateDirFor(BrokerName), "credentials.json")
	argv := execx.SelfExecArgv([]string{"yolo", "internal", "daemon", BrokerName,
		"--socket", fronted, "--state-file", state})
	d := broker.SingletonDeps(BrokerName, argv)
	d.Out = stderr
	broker.BrokerSpawn(d)
	if !broker.BrokerIsAlive(d) {
		return "", fmt.Errorf("OpenAI credential service did not start; see %s", d.LogPath)
	}
	waitForSocket := func() error {
		return waitHostSocket(hostSocket, 2*time.Second, openauthclient.RequestUnix)
	}
	if err := waitForSocket(); err == nil {
		return hostSocket, nil
	}
	// A live singleton from an older yolo may lack the host socket. Replace it
	// once so managed host launches upgrade without a reboot, then give the new
	// process the same bounded startup window as the first one.
	broker.BrokerKill(d, syscall.SIGTERM, 2*time.Second)
	broker.BrokerSpawn(d)
	if err := waitForSocket(); err != nil {
		return "", fmt.Errorf("OpenAI host credential socket unavailable: %w", err)
	}
	return hostSocket, nil
}

func waitHostSocket(socket string, timeout time.Duration, request requestFunc) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if _, err := request(socket, map[string]any{"action": "ping"}, io.Discard); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(25 * time.Millisecond)
	}
}
