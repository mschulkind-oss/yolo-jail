// Package openaiauthhost prepares managed host Codex, Pi and opencode launches that use
// yolo's machine-wide OpenAI subscription credential service.
package openaiauthhost

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
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
	// procs is how the managed Codex home's one-time daemon retirement reads and signals a
	// process (codexdaemon.go); nil takes ps and kill.
	procs *daemonProcs
	// getenv reads the launch's environment, for XDG_DATA_HOME (opencodeHostAuthPath); nil reads
	// nothing.
	getenv func(string) string
}

// daemonProcs is d.procs, or the real ps and kill.
func (d deps) daemonProcs() daemonProcs {
	if d.procs != nil {
		return *d.procs
	}
	return realDaemonProcs()
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
	// noDaemon is set on a managed Codex launch, whose argv gets `--no-daemon` (Argv,
	// codexdaemon.go): Codex's background server stays off in a launch yolo manages (OQ-CDX1).
	noDaemon bool
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
	CodexViewFlag    = "--codex-auth"
	PiViewFlag       = "--pi-auth"
	OpencodeViewFlag = "--opencode-auth"
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
		workspace: os.Getwd, newToken: svcendpoint.NewToken, getenv: os.Getenv,
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
//   - THE VIEW, served the host's way: the pi and opencode views are the host credential socket
//     their yolo extension and plugin read (a jail writes each one's auth file into the jail's
//     home instead; at the host that file is the user's own, and opencode's plugin offers the
//     shared login in its /connect so the user stores the view themselves, which the launch
//     says until they have: opencodeHostLoginNotice), the codex view a managed CODEX_HOME keyed
//     on the declaring pack, with its refresh adapter. A login-only prelaunch proves the login
//     and writes nothing.
func prepare(d deps, p Prelaunch, stderr io.Writer) (*Launch, error) {
	if !p.Declared() {
		return nil, nil
	}
	switch p.Flag {
	case "", CodexViewFlag, PiViewFlag, OpencodeViewFlag:
	default:
		return nil, fmt.Errorf("%s: the host serves no OpenAI view %q (it serves %s, %s and %s)",
			p.Bin, p.Flag, CodexViewFlag, PiViewFlag, OpencodeViewFlag)
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
	case OpencodeViewFlag:
		if notice := opencodeHostLoginNotice(d); notice != "" {
			fmt.Fprintln(stderr, notice)
		}
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
	callerToken, live, alone, err := sharedCallerToken(managedHome, d.newToken)
	if err != nil {
		return nil, err
	}
	if err := prepareCodexHome(managedHome, filepath.Join(d.home(), ".codex"), workspace, response, callerToken); err != nil {
		_ = live.Close()
		return nil, err
	}
	// CODEX'S BACKGROUND SERVER STAYS OFF HERE (OQ-CDX1, codexdaemon.go): the config key is in
	// the managed config prepareCodexHome just wrote, the flag is Argv's, and this shuts down,
	// once, what an earlier launch's daemon left behind in THIS home. Never the user's ~/.codex.
	retireManagedDaemon(managedHome, alone, d.daemonProcs(), stderr)
	launch.noDaemon = true
	listener, err := d.listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = live.Close()
		return nil, fmt.Errorf("start managed Codex credential adapter: %w", err)
	}
	launch.live = live
	end := make(chan error, 1)
	go func() { end <- serveAdapter(listener, socket, callerToken, d.request, stderr) }()
	launch.listener = listener
	launch.adapterEnd = end
	launch.vars["CODEX_HOME"] = managedHome
	launch.vars["CODEX_REFRESH_TOKEN_URL_OVERRIDE"] = "http://" + listener.Addr().String() + "/oauth/token"
	return launch, nil
}

// opencodeHostAuthPath is the auth store opencode reads at the host, the one yolo's opencode
// plugin reads too: $XDG_DATA_HOME/opencode/auth.json, else ~/.local/share/opencode/auth.json
// (opencode 1.18.34's src/auth/index.ts, through xdg-basedir). "" without a home.
func opencodeHostAuthPath(d deps) string {
	if d.home == nil {
		return ""
	}
	data := ""
	if d.getenv != nil {
		data = d.getenv("XDG_DATA_HOME")
	}
	if data == "" {
		data = filepath.Join(d.home(), ".local", "share")
	}
	return filepath.Join(data, "opencode", "auth.json")
}

// opencodeHostLoginNotice is the line `yolo host` prints for opencode on the ChatGPT subscription
// while the user's own opencode auth store does not hold yolo's view, or "". At the host yolo
// never writes that store, so the shared login the launch just proved reaches opencode only once
// the user picks it in /connect (packs/opencode/plugins/yolo-openai-auth.js): until then opencode
// runs on its own ChatGPT login, or, with none, on the derive's non-key, and its requests fail.
// The launch says which, rather than leave a proved shared login to read as the one in use.
func opencodeHostLoginNotice(d deps) string {
	path := opencodeHostAuthPath(d)
	if path == "" {
		return ""
	}
	const pick = `pick "ChatGPT Plus/Pro (yolo shared login)" for OpenAI in opencode's /connect`
	switch openauthclient.ReadOpencodeLogin(path) {
	case openauthclient.OpencodeSharedLogin:
		return ""
	case openauthclient.OpencodeOwnLogin:
		return "  opencode: runs on its own ChatGPT login, stored in " + path + ", not on yolo's shared one; " +
			pick + " to switch."
	}
	return "  opencode: to run on yolo's shared ChatGPT login, " + pick + " once; yolo does not write " +
		"opencode's logins at the host (" + path + "), and until then opencode's requests on the " +
		"subscription fail."
}

// serveAdapter is THE HOST-SIDE CODEX REFRESH ADAPTER: Codex's token endpoint on an
// already-bound loopback listener, behind callerToken, each refresh forwarded to the host
// broker's private socket through request (openauthclient.RequestUnix in production). One body
// for both of its placements: `yolo host -- codex` serves it in-process (prepare), and a
// macos-user launch opens it as a launch-owned doorway outside the sandbox (DoorwayMain;
// docs/design/host-notch-services.md HS-D15). Closing listener stops it.
func serveAdapter(listener net.Listener, socket, callerToken string, request requestFunc, stderr io.Writer) error {
	refresh := func(_ context.Context, marker string) (openaiauthadapter.Token, error) {
		raw, err := request(socket, map[string]any{"action": "refresh", "refresh_token": marker}, stderr)
		if err != nil {
			return openaiauthadapter.Token{}, err
		}
		var token openaiauthadapter.Token
		if err := json.Unmarshal(raw, &token); err != nil {
			return token, fmt.Errorf("decode managed Codex refresh: %w", err)
		}
		return token, nil
	}
	return openaiauthadapter.Serve(listener, callerToken, refresh)
}

// DoorwayMain is `yolo internal daemon openai-auth-adapter --listen <addr>`: Codex's refresh
// DOORWAY opened outside a sandbox whose agent shares the host's loopback, as the loophole's
// `jail_daemon.host_cmd` declares (docs/design/host-notch-services.md HS-D15; the word is that
// ruling's for the thin adapter an agent's client talks to). A macos-user launch starts it as a
// launch-owned listener (internal/launchservice) on the port it picked for the loophole's
// `listen`, the port packs/codex's CODEX_REFRESH_TOKEN_URL_OVERRIDE names in that sandbox, and
// stops it when the sandboxed command exits.
//
// It is serveAdapter, the adapter `yolo host -- codex` already serves, with its inputs from the
// launch's input file: the caller token the launch minted for the loophole (the one the Codex
// launcher's auth.json writer binds into the refresh marker, openauthclient.WriteCodexAuth), and
// the host broker's private socket (HS-D3's route, never a jail endpoint file).
func DoorwayMain(args []string) int {
	fs := flag.NewFlagSet("openai-auth-adapter", flag.ContinueOnError)
	listen := fs.String("listen", "", "the loopback address the launch picked")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "openai-auth-adapter: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	return launchservice.ServeListener(BrokerName, *listen, doorwayPrepare(openauthclient.RequestUnix, os.Stderr))
}

// doorwayPrepare reads the doorway's two inputs and returns its serve: request is how it reaches
// the broker's socket, a parameter so a test can stand a fake broker in.
func doorwayPrepare(request requestFunc, stderr io.Writer) launchservice.Prepare {
	return func(getenv func(string) string) (func(net.Listener) error, error) {
		token, why := openaiauthadapter.CallerToken(getenv)
		if why != "" {
			return nil, errors.New(why)
		}
		socket := getenv(openauthclient.HostSocketEnv)
		if socket == "" {
			return nil, fmt.Errorf("the launch handed the doorway no host credential socket ($%s is unset)",
				openauthclient.HostSocketEnv)
		}
		return func(l net.Listener) error { return serveAdapter(l, socket, token, request, stderr) }, nil
	}
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
//
// alone reports that this launch minted, so no other launch of the home is live: what lets the
// daemon retirement (retireManagedDaemon) stop a process no other session is attached to.
func sharedCallerToken(managed string, mint func() (string, error)) (string, *os.File, bool, error) {
	if err := os.MkdirAll(managed, 0o700); err != nil {
		return "", nil, false, fmt.Errorf("create managed Codex home: %w", err)
	}
	decide, err := lockFile(filepath.Join(managed, decideLockFile), syscall.LOCK_EX)
	if err != nil {
		return "", nil, false, err
	}
	defer decide.Close()
	live, err := os.OpenFile(filepath.Join(managed, liveLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", nil, false, fmt.Errorf("open managed Codex launch lock: %w", err)
	}
	tokenPath := filepath.Join(managed, callerTokenFile)
	var token string
	alone := false
	switch err := syscall.Flock(int(live.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
	case err == nil:
		// No other launch of this home is live: mint, so a token outlives no session it served.
		alone = true
		if token, err = mint(); err != nil {
			_ = live.Close()
			return "", nil, false, fmt.Errorf("mint the managed Codex credential adapter's caller token: %w", err)
		}
		if err := atomicWritePrivate(tokenPath, []byte(token)); err != nil {
			_ = live.Close()
			return "", nil, false, err
		}
	case errors.Is(err, syscall.EWOULDBLOCK):
		data, err := os.ReadFile(tokenPath)
		if err != nil || !svcendpoint.IsToken(string(data)) {
			_ = live.Close()
			return "", nil, false, fmt.Errorf("another `yolo host -- codex` is running, but its caller token %s "+
				"is unreadable or malformed (%v); end the other session and retry", tokenPath, err)
		}
		token = string(data)
	default:
		_ = live.Close()
		return "", nil, false, fmt.Errorf("lock managed Codex launch lock: %w", err)
	}
	// Converting an exclusive lock to shared is not atomic, which is why decide is still held.
	if err := syscall.Flock(int(live.Fd()), syscall.LOCK_SH); err != nil {
		_ = live.Close()
		return "", nil, false, fmt.Errorf("lock managed Codex launch lock: %w", err)
	}
	return token, live, alone, nil
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
//
// It also turns Codex's background server off (features.daemon_auto_start = false, OQ-CDX1,
// codexdaemon.go), whatever the ordinary config says: this home is the launch's, and a daemon
// started in it would outlive the launch and its refresh adapter.
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
	setDaemonAutoStartOff(root)
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

// HostSocketPath is the host broker's private socket, the path ensureSingleton publishes and every
// host-side consumer of the OpenAI credential service dials: managed Codex, pi's view, and a
// launch-owned service's host half (docs/design/host-notch-services.md HS-D3).
func HostSocketPath() string {
	return openaiauthdaemon.HostSocketPath(paths.HostSingletonSocket(BrokerName))
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
