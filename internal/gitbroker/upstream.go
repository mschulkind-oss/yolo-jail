package gitbroker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// upstream.go is the leg from the broker to the remote: which transports it carries, the
// host ssh it runs and the environment it runs it in (docs/reference/git-broker.md ID-7,
// docs/reference/git-broker.md). Credentialed HTTPS uses an exact URL in live scope; SSH
// uses the Git source's narrow SSH comparison in scope_endpoint.go. The original upstream URL
// is preserved. The broker may also try an HTTPS fetch anonymously outside scope, without
// credential helper access.

const (
	transportSSH   = "ssh"
	transportHTTPS = "https"
)

// transportOf says which upstream an approved remote URL takes, or why the broker carries
// none. Only ssh://, git+ssh://, ssh+git://, https:// and scp-style [user@]host:path remotes
// cross: git's `<transport>::<address>` remote-helper form (ext::, fd::, …), file://, http://,
// git://, any other scheme and a local path are refused, as is anything starting with "-" that ssh
// could read as an option. The scope file accepts any transport string; this is where it stops.
func transportOf(remote string) (string, error) {
	if strings.HasPrefix(remote, "-") {
		return "", errors.New("it starts with \"-\", which the host's ssh would read as an option")
	}
	if i := strings.Index(remote, "::"); i > 0 && isURLSchemeChars(remote[:i]) {
		return "", fmt.Errorf("it is git's %s:: remote-helper form, which runs a program on the host", remote[:i])
	}
	if scheme, _, ok := strings.Cut(remote, "://"); ok {
		switch strings.ToLower(scheme) {
		case "ssh", "https", "git+ssh", "ssh+git":
		default:
			return "", fmt.Errorf("it uses %s://", scheme)
		}
		if _, err := gitproto.Identity(remote); err != nil {
			return "", err
		}
		if strings.EqualFold(scheme, "https") {
			return transportHTTPS, nil
		}
		return transportSSH, nil
	} else if _, err := gitproto.Identity(remote); err != nil {
		return "", err
	}
	if _, err := splitSSH(remote); err != nil {
		return "", err
	}
	return transportSSH, nil
}

// isURLSchemeChars is git's is_urlschemechar over s: a letter first, then letters, digits,
// "+", "-" and ".". git reads `<s>::<address>` as a remote helper when s is one.
func isURLSchemeChars(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		alpha := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if i == 0 && !alpha {
			return false
		}
		if !alpha && !(c >= '0' && c <= '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return s != ""
}

// sshTarget is an SSH remote taken apart the way git's connect.c does.
type sshTarget struct {
	dest string // [user@]host, as ssh's destination argument
	port string
	path string // the path git sends the remote command, unquoted
}

// scpPathColon skips the colons inside a bracketed IPv6 host to find git's scp-style
// host:path separator.
func scpPathColon(remote string) int {
	colon := strings.IndexByte(remote, ':')
	if colon < 0 {
		return colon
	}
	bracket := strings.LastIndexByte(remote[:colon], '[')
	if bracket < 0 {
		return colon
	}
	close := strings.IndexByte(remote[bracket:], ']')
	if close < 0 {
		return colon
	}
	close += bracket
	if close+1 < len(remote) && remote[close+1] == ':' {
		return close + 1
	}
	return colon
}

// parseSSH takes an SSH URL (ssh://, git+ssh:// or ssh+git://) or scp-style remote apart. For
// URL-form SSH, a path starting "/~" loses its leading slash, as git's own does; an scp-style
// path is used as written.
func parseSSH(remote string) (sshTarget, error) {
	if _, err := transportOf(remote); err != nil {
		return sshTarget{}, err
	}
	return splitSSH(remote)
}

// splitSSH takes an SSH remote apart from its raw text. Git can percent-decode SSH URL paths,
// but the broker deliberately refuses %-escapes: exact URL approval, repository identity and
// the path sent upstream must not disagree about which repository the text names. A URL whose
// user, host or path a URL decoder would read differently from the text is refused rather than
// guessed at. The user part, which reaches ssh's argv and an ssh config's %r, must be a plain
// login name: [A-Za-z0-9._-], not starting with "-".
func splitSSH(remote string) (sshTarget, error) {
	var t sshTarget
	var user, host string
	if scheme, rest, ok := strings.Cut(remote, "://"); ok {
		if !strings.EqualFold(scheme, "ssh") && !strings.EqualFold(scheme, "git+ssh") && !strings.EqualFold(scheme, "ssh+git") {
			return t, fmt.Errorf("%s:// is not an SSH remote", scheme)
		}
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			return t, errors.New("it names no path")
		}
		authority, rawPath := rest[:slash], rest[slash:]
		hostPort := authority
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			user, hostPort = authority[:at], authority[at+1:]
			if user == "" {
				return t, errors.New("its user part is empty")
			}
		}
		u, err := url.Parse(remote)
		if err != nil {
			return t, err
		}
		decodedUser := ""
		if u.User != nil {
			decodedUser = u.User.Username()
			if _, hasPassword := u.User.Password(); hasPassword {
				decodedUser += ":"
			}
		}
		if decodedUser != user || u.Host != hostPort || u.Path != rawPath || u.RawQuery != "" || u.Fragment != "" {
			return t, errors.New("it holds a %-escape, which exact approval and the upstream path could interpret differently; " +
				"write the remote without one")
		}
		host, t.port = u.Hostname(), u.Port()
		t.path = rawPath
		if strings.HasPrefix(t.path, "/~") {
			t.path = t.path[1:]
		}
	} else {
		colon := scpPathColon(remote)
		if colon < 0 {
			return t, errors.New("it is not an SSH remote")
		}
		host, t.path = remote[:colon], remote[colon+1:]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			user, host = host[:at], host[at+1:]
			if user == "" {
				return t, errors.New("its user part is empty")
			}
		}
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	}
	if user != "" && !validSSHUser(user) {
		return t, errors.New("its user part holds a character outside A-Z, a-z, 0-9, \".\", \"_\" and \"-\", or starts with \"-\"")
	}
	t.dest = host
	if user != "" {
		t.dest = user + "@" + host
	}
	switch {
	case host == "" || strings.HasPrefix(host, "-"):
		return t, errors.New("its host is empty or starts with \"-\"")
	case t.path == "" || strings.HasPrefix(t.path, "-"):
		return t, errors.New("its path is empty or starts with \"-\"")
	}
	return t, nil
}

// validSSHUser says user is a plain login name: [A-Za-z0-9._-]+, not starting with "-".
func validSSHUser(user string) bool {
	if user == "" || user[0] == '-' {
		return false
	}
	for i := 0; i < len(user); i++ {
		c := user[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// sshArgv is the argv after `ssh` for service against remote (ID-7): no forwarding of any
// kind, no local command, a connect timeout, and no BatchMode, so a key needing a touch or a
// passphrase prompts on the host. The remote command is git's own spelling, the path
// single-quoted.
func sshArgv(remote, service, protocol string) ([]string, error) {
	t, err := parseSSH(remote)
	if err != nil {
		return nil, err
	}
	argv := []string{"-x", "-T", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ConnectTimeout=30"}
	if t.port != "" {
		argv = append(argv, "-p", t.port)
	}
	if validProtocol(protocol) {
		argv = append(argv, "-o", "SendEnv=GIT_PROTOCOL")
	}
	return append(argv, "--", t.dest, service+" "+sqQuote(t.path)), nil
}

// sqQuote single-quotes s as git's sq_quote_buf does: a quote becomes '\” and a "!"
// becomes '\!', so the remote shell reads s as one word whatever it holds. shquote.Quote is
// not used: it leaves a safe word bare, and git, and so every server's forced command,
// expects the quotes.
func sqQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString(`'\''`)
		case '!':
			b.WriteString(`'\!'`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// validProtocol says p is a GIT_PROTOCOL value the broker passes on: version=2 or version=1,
// nothing else. git sends a connect-only helper none (the plan's pitfalls), so in practice
// this is always empty; a value from the jail is never passed through unchecked.
func validProtocol(p string) bool { return p == "version=2" || p == "version=1" }

// sshEnv is the host ssh's environment, built from nothing (ID-7): the host's PATH, HOME and
// USER, the launching session's agent socket, and what a key prompt on the host needs. No
// variable of the broker's own environment reaches it otherwise, so no token the host
// session holds (GH_TOKEN, AWS_*) does.
func sshEnv(getenv func(string) string, protocol string) []string {
	env := []string{"PATH=" + getenv("PATH"), "HOME=" + getenv("HOME")}
	for _, k := range []string{"USER", "SSH_AUTH_SOCK", "DISPLAY", "WAYLAND_DISPLAY", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE"} {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if validProtocol(protocol) {
		env = append(env, "GIT_PROTOCOL="+protocol)
	}
	return env
}

// lookPath finds name on pathEnv, as exec.LookPath does on the process's own PATH.
func lookPath(name, pathEnv string) (string, error) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// upstream is one running host ssh.
type upstream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	tail   *tailBuffer
}

// waitDelay bounds how long a finished or cancelled upstream's output pipes are waited on
// when something it started still holds them open.
const waitDelay = 5 * time.Second

// startUpstream runs the host program at path with argv and env, in dir. Its stderr is
// passed to the jail's stderr as it arrives, and its tail kept for the broker's own message.
//
// It runs in a process group of its own, and when ctx ends (the jail went away, or the
// daemon was told to stop) the whole group is killed, so nothing the program started, such
// as a child holding its stderr, keeps the call alive.
func startUpstream(ctx context.Context, path string, argv, env []string, dir string, stderr io.Writer) (*upstream, error) {
	cmd := exec.CommandContext(ctx, path, argv...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.SysProcAttr = childProcAttr()
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	u := &upstream{cmd: cmd, tail: &tailBuffer{max: 4096}}
	cmd.Stderr = io.MultiWriter(stderr, u.tail)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	u.stdin, u.stdout = stdin, bufio.NewReaderSize(stdout, 64<<10)
	return u, nil
}

// wait returns the program's exit code once its stderr is drained (or waitDelay after it
// exited, if something else still holds it). The caller has read its stdout to the end, or
// stopped needing it.
func (u *upstream) wait() int {
	err := u.cmd.Wait()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() >= 0 {
		return ee.ExitCode()
	}
	// The program exited but something it started held its stderr past waitDelay: its own
	// exit code still stands.
	if errors.Is(err, exec.ErrWaitDelay) && u.cmd.ProcessState != nil && u.cmd.ProcessState.ExitCode() >= 0 {
		return u.cmd.ProcessState.ExitCode()
	}
	return 1
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
