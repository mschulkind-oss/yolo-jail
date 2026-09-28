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

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const BrokerName = "openai-auth-broker"

type requestFunc func(string, any, io.Writer) (json.RawMessage, error)

type deps struct {
	ensure    func(io.Writer) (string, error)
	request   requestFunc
	listen    func(string, string) (net.Listener, error)
	home      func() string
	storage   func() string
	workspace func() (string, error)
	// newToken mints the managed adapter's per-launch caller token (svcendpoint.NewToken).
	newToken func() (string, error)
}

// Launch carries environment overrides and, for Codex, the loopback adapter
// whose lifetime must follow the agent process.
type Launch struct {
	vars       map[string]string
	listener   net.Listener
	adapterEnd <-chan error
	// live is this launch's shared lock on the managed home's live-launch lock
	// (sharedCallerToken), held until the agent exits.
	live *os.File
}

// PrelaunchPrefix begins every variable of the declarative OpenAI prelaunch a pack's `env`
// declares for one launcher binary: YOLO_AUTH_PRELAUNCH_<BIN>_FLAG (the credential client's
// view flag), _PATH (the home-relative file a jail writes the view into) and _LOGIN (the login
// alone, no view). The jail's launcher reads them (entrypoint's agentAuthPrelaunchShellFn), and
// so does `yolo host` (PrelaunchVar), so neither notch names an agent.
const PrelaunchPrefix = "YOLO_AUTH_PRELAUNCH_"

// PrelaunchVar is the prelaunch variable field ("FLAG", "PATH" or "LOGIN") for launcher bin,
// spelled the way the jail's launcher spells it: the binary's name uppercased, every byte other
// than A-Z, 0-9 and _ replaced by _.
func PrelaunchVar(bin, field string) string {
	suffix := []byte(strings.ToUpper(bin))
	for i, c := range suffix {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			suffix[i] = '_'
		}
	}
	return PrelaunchPrefix + string(suffix) + "_" + field
}

// The view flags the credential client writes (`yolo internal openai-auth-client token`), which
// a pack's prelaunch FLAG names. At the host each is served its own way (prepare).
const (
	CodexViewFlag = "--codex-auth"
	PiViewFlag    = "--pi-auth"
)

// Prelaunch is the declarative OpenAI prelaunch one host launch carries: what the launched
// binary's pack declared, read from the launch's composed environment, as a jail's launcher reads
// it from its own (docs/plans/notch-convergence.md item 15, row C6).
type Prelaunch struct {
	// Bin is the launched binary's name, for the messages.
	Bin string
	// Flag is the view flag, "" when the pack declares none.
	Flag string
	// Login is the login-only prelaunch: prove the login, write no view.
	Login bool
	// Pack is the pack that declared the prelaunch, which keys a managed home, so two packs
	// declaring the same view never share one.
	Pack string
	// Interactive reports whether a human can answer a browser login: stdin is a terminal.
	Interactive bool
}

// Declared reports whether the launch asks for a prelaunch at all.
func (p Prelaunch) Declared() bool { return p.Flag != "" || p.Login }

// Prepare runs the launch's declarative OpenAI prelaunch, and returns nil when it declares none,
// or when no login exists and none can be asked for.
func Prepare(p Prelaunch, stderr io.Writer) (*Launch, error) {
	d := deps{
		ensure: ensureSingleton, request: openauthclient.RequestUnix,
		listen: net.Listen, home: paths.Home, storage: paths.GlobalStorage,
		workspace: os.Getwd, newToken: svcendpoint.NewToken,
	}
	return prepare(d, p, stderr)
}

// prepare is the one Go prelaunch, the jail launcher's lifecycle at the host:
//
//   - NOTHING DECLARED, NOTHING DONE. It used to switch on the command's name, so `yolo host
//     -p zai -- pi </dev/null` reached the broker's browser login although pi's pack gates its
//     prelaunch on the `codex` profile (MEASURED 2026-09-27), and a jail's pi on zai never
//     logs in.
//   - A LOGIN ONLY AT A TERMINAL. With no login and no terminal, it says what is missing and
//     lets the command run without the credential, in the jail launcher's words: a browser
//     login nobody can open only hangs, and plenty of invocations need no credential at all.
//   - THE VIEW, served the host's way: the pi view is the host credential socket its extension
//     reads (a jail writes pi's auth file into the jail's home instead; at the host that file is
//     the user's own), the codex view a managed CODEX_HOME keyed on the declaring pack, with its
//     refresh adapter. A login-only prelaunch proves the login and writes nothing.
func prepare(d deps, p Prelaunch, stderr io.Writer) (*Launch, error) {
	if !p.Declared() {
		return nil, nil
	}
	switch p.Flag {
	case "", CodexViewFlag, PiViewFlag:
	default:
		return nil, fmt.Errorf("%s: the host serves no OpenAI view %q (it serves %s and %s)",
			p.Bin, p.Flag, CodexViewFlag, PiViewFlag)
	}
	socket, err := d.ensure(stderr)
	if err != nil {
		return nil, err
	}
	loggedIn, err := ensureLogin(socket, d.request, p, stderr)
	if err != nil || !loggedIn {
		return nil, err
	}
	switch p.Flag {
	case "":
		return nil, nil
	case PiViewFlag:
		return &Launch{vars: map[string]string{openauthclient.HostSocketEnv: socket}}, nil
	}
	launch := &Launch{vars: map[string]string{openauthclient.HostSocketEnv: socket}}
	key := p.Pack
	if key == "" {
		key = p.Bin
	}
	managedHome := filepath.Join(d.storage(), "host-agents", key)
	response, err := d.request(socket, map[string]any{"action": "token", "view": "codex"}, stderr)
	if err != nil {
		return nil, err
	}
	workspace := "."
	if d.workspace != nil {
		workspace, err = d.workspace()
		if err != nil {
			return nil, fmt.Errorf("resolve managed Codex workspace: %w", err)
		}
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve managed Codex workspace: %w", err)
	}
	// THE CALLER TOKEN (docs/plans/notch-convergence.md §2.3, NC-D3, NC-D18). The adapter
	// below listens on the HOST's loopback, which every local process can reach, so it serves
	// only a refresh whose marker carries this token — bound into the auth.json Codex reads,
	// and so sent back by Codex alone. Held in this process and in 0600 files of the managed
	// home only: never in the environment, which the adapter's client does not need. Every
	// concurrent launch shares that home and so that auth.json, so they share the token too.
	callerToken, live, err := sharedCallerToken(managedHome, d.newToken)
	if err != nil {
		return nil, err
	}
	if err := prepareCodexHome(managedHome, filepath.Join(d.home(), ".codex"), workspace, response, callerToken); err != nil {
		_ = live.Close()
		return nil, err
	}
	listener, err := d.listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = live.Close()
		return nil, fmt.Errorf("start managed Codex credential adapter: %w", err)
	}
	launch.live = live
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
	go func() { end <- openaiauthadapter.Serve(listener, callerToken, refresh) }()
	launch.listener = listener
	launch.adapterEnd = end
	launch.vars["CODEX_HOME"] = managedHome
	launch.vars["CODEX_REFRESH_TOKEN_URL_OVERRIDE"] = "http://" + listener.Addr().String() + "/oauth/token"
	return launch, nil
}

// Files of the managed Codex home that carry its caller token and the two locks deciding it.
const (
	callerTokenFile = ".yolo-caller-token"
	decideLockFile  = ".yolo-caller-token.lock"
	liveLockFile    = ".yolo-live.lock"
)

// sharedCallerToken returns the caller token a managed Codex launch serves behind, and the lock
// that keeps it valid while the launch runs (NC-D18). Every `yolo host -- codex` shares one
// managed CODEX_HOME, so one auth.json, and Codex reloads that file before each refresh: if each
// launch bound a token of its own, the latest launch's would replace every other session's, and
// those sessions' adapters would refuse the marker their Codex now sends. So the token belongs to
// the home's LIVE launches. A launch holds a shared flock on liveLockFile until its agent exits.
// A launch that can take it exclusively is the only one, and mints a fresh token; otherwise it
// reuses the live launches' token from callerTokenFile. decideLockFile serializes the decision,
// so no launch can rotate the token between another's mint and its shared lock.
//
// Sharing proves no less than a per-launch token did: both live in 0600 files of the one managed
// home, so the proof was always "the caller can read the managed Codex home".
func sharedCallerToken(managed string, mint func() (string, error)) (string, *os.File, error) {
	if err := os.MkdirAll(managed, 0o700); err != nil {
		return "", nil, fmt.Errorf("create managed Codex home: %w", err)
	}
	decide, err := lockFile(filepath.Join(managed, decideLockFile), syscall.LOCK_EX)
	if err != nil {
		return "", nil, err
	}
	defer decide.Close()
	live, err := os.OpenFile(filepath.Join(managed, liveLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("open managed Codex launch lock: %w", err)
	}
	tokenPath := filepath.Join(managed, callerTokenFile)
	var token string
	switch err := syscall.Flock(int(live.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
	case err == nil:
		// No other launch of this home is live: mint, so a token outlives no session it served.
		if token, err = mint(); err != nil {
			_ = live.Close()
			return "", nil, fmt.Errorf("mint the managed Codex credential adapter's caller token: %w", err)
		}
		if err := atomicWritePrivate(tokenPath, []byte(token)); err != nil {
			_ = live.Close()
			return "", nil, err
		}
	case errors.Is(err, syscall.EWOULDBLOCK):
		data, err := os.ReadFile(tokenPath)
		if err != nil || !svcendpoint.IsToken(string(data)) {
			_ = live.Close()
			return "", nil, fmt.Errorf("another `yolo host -- codex` is running, but its caller token %s "+
				"is unreadable or malformed (%v); end the other session and retry", tokenPath, err)
		}
		token = string(data)
	default:
		_ = live.Close()
		return "", nil, fmt.Errorf("lock managed Codex launch lock: %w", err)
	}
	// Converting an exclusive lock to shared is not atomic, which is why decide is still held.
	if err := syscall.Flock(int(live.Fd()), syscall.LOCK_SH); err != nil {
		_ = live.Close()
		return "", nil, fmt.Errorf("lock managed Codex launch lock: %w", err)
	}
	return token, live, nil
}

func lockFile(path string, how int) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

// ensureLogin reports whether the machine's OpenAI login exists, starting the browser login when
// it does not and a human can answer it (p.Interactive). With no terminal it prints the jail
// launcher's two lines and reports false, so the launch continues without the credential.
func ensureLogin(socket string, request requestFunc, p Prelaunch, stderr io.Writer) (bool, error) {
	raw, err := request(socket, map[string]any{"action": "status"}, stderr)
	var status struct {
		LoggedIn      bool `json:"logged_in"`
		LoginRequired bool `json:"login_required"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &status)
	}
	if err == nil && status.LoggedIn && !status.LoginRequired {
		return true, nil
	}
	if !p.Interactive {
		fmt.Fprintf(stderr, "  %s: OpenAI login is required, and this is not an interactive terminal\n", p.Bin)
		fmt.Fprintf(stderr, "  → run '%s' once from a terminal to log in; continuing without a credential.\n", p.Bin)
		return false, nil
	}
	fmt.Fprintf(stderr, "  %s: OpenAI login is required.\n", p.Bin)
	_, loginErr := request(socket, map[string]any{"action": "login"}, stderr)
	if loginErr != nil {
		return false, fmt.Errorf("OpenAI browser login: %w", loginErr)
	}
	return true, nil
}

func prepareCodexHome(managed, ordinary, workspace string, response json.RawMessage, callerToken string) error {
	if err := os.MkdirAll(managed, 0o700); err != nil {
		return fmt.Errorf("create managed Codex home: %w", err)
	}
	if err := os.Chmod(managed, 0o700); err != nil {
		return err
	}
	if err := writeManagedCodexConfig(filepath.Join(managed, "config.toml"),
		filepath.Join(ordinary, "config.toml"), workspace); err != nil {
		return err
	}
	for _, name := range []string{"AGENTS.md", "skills"} {
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
	return openauthclient.WriteCodexAuth(filepath.Join(managed, "auth.json"), response, callerToken)
}

// writeManagedCodexConfig copies the ordinary host config into the isolated
// CODEX_HOME and marks only this launch's absolute workspace trusted. Codex 0.154
// keys project trust under projects.<absolute-path>.trust_level. The ordinary
// config remains untouched, and the managed copy is rebuilt on every launch so
// host config changes still take effect.
func writeManagedCodexConfig(destination, source, workspace string) error {
	root := map[string]any{}
	if data, err := os.ReadFile(source); err == nil {
		decoded, err := (codec.TOML{}).Decode(data)
		if err != nil {
			return fmt.Errorf("decode ordinary Codex config: %w", err)
		}
		root = decoded.(map[string]any)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read ordinary Codex config: %w", err)
	}
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		projects = map[string]any{}
		root["projects"] = projects
	}
	project, ok := projects[workspace].(map[string]any)
	if !ok {
		project = map[string]any{}
		projects[workspace] = project
	}
	project["trust_level"] = "trusted"
	data, err := (codec.TOML{}).Encode(root)
	if err != nil {
		return fmt.Errorf("encode managed Codex config: %w", err)
	}
	return atomicWritePrivate(destination, data)
}

func atomicWritePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
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
		if l.live != nil {
			_ = l.live.Close()
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
