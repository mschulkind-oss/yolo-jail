package gitbroker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// https.go is the HTTPS upstream (docs/reference/git-broker.md ID-8,
// "The upstream leg"): smart HTTP. HTTPS fetch always tries anonymously first. The broker may try a URL
// outside scope only without credentials; host credentials are filled only while the live scope
// approves that exact URL and its read grant.
//
// A fetch always asks the server for protocol v2 (`Git-Protocol: version=2`). v2 is
// request-response, so the client's stateful stream maps onto stateless POSTs with no
// negotiation state of the broker's own: the broker relays the capability advertisement, then
// POSTs each client request up to its flush and relays the response, until the client sends an
// empty request or closes its side. git sends a `connect` helper no GIT_PROTOCOL, so the
// client starts out expecting v0 and takes the "version 2" advertisement as the server's
// choice, which git's discover_version allows. A server that answers v0 is refused.
//
// A push is v0: the receive-pack advertisement is relayed without its `# service` line and that
// line's flush, the commands are read and judged exactly as over SSH (push.go), and a covered
// push is POSTed whole (the judged bytes, then the pack to the client's end) and the report
// relayed.
//
// TLS uses the system roots. NOTHING turns verification off: no config key and no environment
// variable reaches the TLS config, and the one seam (Broker.rootCAs) is set only by tests.

// pushBufferMax is the largest push body the broker reads whole and sends with a
// Content-Length, git's own default http.postBuffer (1 MiB); a larger one streams chunked.
const pushBufferMax = 1 << 20

var (
	errNoCredential               = errors.New("the host has no credential for this remote")
	errCredentialApprovalChanged  = errors.New("host credential approval changed during the request")
	errCredentialAuthorityChanged = errors.New("host launch authority changed during the request")
)

// credentialTimeout bounds one `git credential` run. A helper that waits on a person (a
// keychain unlock dialog, say) is given that long, and is then killed.
const credentialTimeout = 2 * time.Minute

// userAgent is what the broker calls itself. Some forges serve smart HTTP only to a client
// whose agent starts with "git/".
const userAgent = "git/yolo-git-broker"

// maxAdvertisement bounds a capability or ref advertisement, and maxRequest one v2 client
// request, the broker buffers. Both are gitproto.MaxCommandBytes; variables so a test can
// lower them.
var (
	maxAdvertisement = gitproto.MaxCommandBytes
	maxRequest       = gitproto.MaxCommandBytes
)

// credential is what `git credential fill` answered. fields is every key=value line of the
// answer except protocol, host and path (which the broker sets itself), in order: approve and
// reject send them back verbatim, as git does, so a helper's oauth_refresh_token,
// password_expiry_utc, wwwauth[] and capability[] survive the round trip.
type credential struct {
	username, password string
	fields             []string
}

// httpsLeg is one request's HTTPS upstream.
type httpsLeg struct {
	ctx                context.Context
	b                  *Broker
	client             *http.Client
	remote             *url.URL // the remote URL, user-info dropped
	service            string
	say                func(string)
	ev                 *brokeraudit.Event
	cred               *credential
	approved           bool
	cover              coverer // the push policy, for a push
	credentialPolicy   func() (bool, string, error)
	credentialDenial   string
	lastCredentialSent bool
	credentialRejected bool
	readOnly           bool // an access advertisement GET must not approve/reject credentials
}

// newHTTPSLeg readies the upstream for the requested HTTPS remote. User-info is stripped and
// never becomes an HTTP or credential-helper credential.
func (b *Broker) newHTTPSLeg(ctx context.Context, remote, service string, ev *brokeraudit.Event, say func(string)) (*httpsLeg, error) {
	u, err := url.Parse(remote)
	if err != nil {
		return nil, err
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return nil, errors.New("it carries a query or fragment, or names no host")
	}
	h := &httpsLeg{ctx: ctx, b: b, service: service, say: say, ev: ev}
	clean := *u
	clean.User = nil
	h.remote = &clean
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: b.rootCAs, MinVersion: tls.VersionTLS12}
	h.client = &http.Client{Transport: tr,
		// The broker connects to the approved URL and nowhere else: a redirect is answered, not
		// followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h, nil
}

// endpoint is the remote URL with suffix appended to its path, as git's remote-curl does.
func (h *httpsLeg) endpoint(suffix string) string {
	u := *h.remote
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(u.EscapedPath(), "/") + suffix
}

// do sends one request, filling a credential from the host only after a 401 and while the
// live scope still approves this exact URL and read grant. Every request re-checks authority;
// an anonymous public fetch never acquires a credential midway through the HTTP leg.
func (h *httpsLeg) do(method, suffix, contentType, accept string, body []byte) (*http.Response, error) {
	send := func(withCredential bool) (*http.Response, error) {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		return h.send(method, suffix, contentType, accept, rd, withCredential)
	}
	allowed, denial, authorityErr := h.credentialAllowed()
	if authorityErr != nil {
		return nil, fmt.Errorf("%w: %v", errCredentialAuthorityChanged, authorityErr)
	}
	if !allowed && h.service == serviceReceivePack {
		return nil, fmt.Errorf("%w: %s", errCredentialApprovalChanged, denial)
	}
	h.credentialDenial = ""
	h.credentialRejected = false
	resp, err := send(h.cred != nil && allowed)
	if err != nil {
		return nil, err
	}
	h.lastCredentialSent = h.cred != nil && allowed
	if resp.StatusCode == http.StatusUnauthorized && h.cred == nil {
		allowed, denial, authorityErr = h.credentialAllowed()
		if authorityErr != nil {
			drainClose(resp)
			return nil, fmt.Errorf("%w: %v", errCredentialAuthorityChanged, authorityErr)
		}
		if !allowed {
			h.credentialDenial = denial
			return resp, nil
		}
		drainClose(resp)
		c, err := h.fill()
		if err != nil {
			return nil, err
		}
		h.cred = c
		allowed, denial, authorityErr = h.credentialAllowed()
		if authorityErr != nil {
			return nil, fmt.Errorf("%w: %v", errCredentialAuthorityChanged, authorityErr)
		}
		if !allowed {
			h.credentialDenial = denial
			return nil, fmt.Errorf("%w: %s", errCredentialApprovalChanged, denial)
		}
		if resp, err = send(true); err != nil {
			return nil, err
		}
		h.lastCredentialSent = true
	}
	allowed, denial, authorityErr = h.credentialAllowed()
	if authorityErr != nil {
		drainClose(resp)
		return nil, fmt.Errorf("%w: %v", errCredentialAuthorityChanged, authorityErr)
	}
	if !allowed && h.service == serviceReceivePack {
		drainClose(resp)
		return nil, fmt.Errorf("%w: %s", errCredentialApprovalChanged, denial)
	}
	if resp.StatusCode == http.StatusUnauthorized && !allowed {
		h.credentialDenial = denial
	}
	if resp.StatusCode == http.StatusUnauthorized && !h.lastCredentialSent && h.credentialDenial == "" {
		h.credentialDenial = denial
	}
	if h.lastCredentialSent && !h.readOnly {
		h.settle(resp)
	}
	return resp, nil
}

func (h *httpsLeg) credentialAllowed() (bool, string, error) {
	if h.credentialPolicy == nil {
		return true, "", nil
	}
	return h.credentialPolicy()
}

// send sends one request with the credential held only when the current live policy permits it.
func (h *httpsLeg) send(method, suffix, contentType, accept string, body io.Reader, withCredential bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(h.ctx, method, h.endpoint(suffix), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Pragma", "no-cache")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if h.service == serviceUploadPack {
		req.Header.Set("Git-Protocol", "version=2")
	}
	if withCredential && h.cred != nil {
		req.SetBasicAuth(h.cred.username, h.cred.password)
	}
	return h.client.Do(req)
}

// settle tells the host's credential helper how the credential fared, as git does: approve
// after the first authenticated success, reject on a 401 with it.
func (h *httpsLeg) settle(resp *http.Response) {
	if h.cred == nil {
		return
	}
	allowed, denial, err := h.credentialAllowed()
	if err != nil || !allowed {
		if !allowed {
			h.credentialDenial = denial
		}
		return
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		h.credentialRejected = h.credentialRun("reject", h.cred) == nil
	case resp.StatusCode/100 == 2 && !h.approved:
		if h.credentialRun("approve", h.cred) == nil {
			h.approved = true
		}
	}
}

// fill asks the host's git for a credential. One that comes back without a password, or a
// helper that fails, is errNoCredential: the broker never prompts and never starts a login.
func (h *httpsLeg) fill() (*credential, error) {
	out, err := h.credentialRunOutput("fill", nil)
	if errors.Is(err, errCredentialApprovalChanged) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoCredential, err)
	}
	c := &credential{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			continue
		}
		if !credentialSafe(line) {
			return nil, fmt.Errorf("%w: the host's credential helper answered a control character in %q", errNoCredential, k)
		}
		switch k {
		case "protocol", "host", "path", "quit":
			continue
		case "username":
			c.username = v
		case "password":
			c.password = v
		}
		c.fields = append(c.fields, line)
	}
	if c.password == "" {
		return nil, errNoCredential
	}
	return c, nil
}

// credentialSafe says a line written to `git credential`'s stdin holds no newline, carriage
// return, NUL or other control character, so it cannot end the record early or smuggle in a
// field (git's own credential code refuses the same).
func credentialSafe(line string) bool {
	for i := 0; i < len(line); i++ {
		if c := line[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func (h *httpsLeg) credentialRun(action string, c *credential) error {
	_, err := h.credentialRunOutput(action, c)
	return err
}

// credentialRunOutput runs `git credential <action>` on the host (GB-D3, ID-8): the host's git,
// placement-checked, in a fresh empty directory the broker owns with GIT_CEILING_DIRECTORIES
// set to it, so no repository's config is read; an environment built from nothing; no terminal
// prompt and no askpass program, so a helper with nothing stored answers nothing rather than
// asking anyone.
func (h *httpsLeg) credentialRunOutput(action string, c *credential) ([]byte, error) {
	if h.credentialPolicy != nil {
		allowed, denial, authorityErr := h.credentialAllowed()
		if authorityErr != nil {
			return nil, fmt.Errorf("%w: %v", errCredentialAuthorityChanged, authorityErr)
		}
		if !allowed {
			return nil, fmt.Errorf("%w: %s", errCredentialApprovalChanged, denial)
		}
	}
	b := h.b
	git, err := lookPath("git", b.getenv("PATH"))
	if err != nil {
		return nil, errors.New("the host has no git on the PATH yolo was launched with")
	}
	if why := brokercore.PlacementRefusal("git", b.bound.Workspace, b.spawnCwd)(git); why != "" {
		return nil, errors.New(why)
	}
	parent := paths.BrokerSourceDir(Source)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "credential-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	lines := []string{"protocol=https", "host=" + h.remote.Host}
	if p := strings.TrimPrefix(h.remote.Path, "/"); p != "" {
		lines = append(lines, "path="+p)
	}
	switch {
	case c != nil:
		lines = append(lines, c.fields...)
	}
	var in strings.Builder
	for _, l := range lines {
		// Defense in depth: fill already refused such an answer, and a URL with an escaped
		// control character in its path stops here.
		if !credentialSafe(l) {
			k, _, _ := strings.Cut(l, "=")
			return nil, fmt.Errorf("the credential's %q holds a newline or control character, so `git credential %s` was not run", k, action)
		}
		in.WriteString(l + "\n")
	}
	in.WriteString("\n")

	ctx, cancel := context.WithTimeout(h.ctx, credentialTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "credential", action)
	cmd.Dir = dir
	cmd.Env = credentialEnv(b.getenv, dir)
	cmd.Stdin = strings.NewReader(in.String())
	cmd.SysProcAttr = childProcAttr()
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	// Its stderr is dropped: a helper's message can name the account, and the user is told
	// what to run on the host instead.
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("`git credential %s` failed (%v)", action, err)
	}
	return out, nil
}

// credentialEnv is the host git's environment for `git credential`, built from nothing (ID-8):
// PATH, HOME and the session bus a keyring helper needs, plus what keeps it from asking anyone.
// GIT_ASKPASS set empty stops git falling back to core.askPass or SSH_ASKPASS, and
// GCM_INTERACTIVE=never stops Git Credential Manager opening a login, so a helper with nothing
// stored answers nothing (git-broker.md, "Failure paths": the broker never prompts and never starts a login).
func credentialEnv(getenv func(string) string, dir string) []string {
	env := []string{"PATH=" + getenv("PATH"), "HOME=" + getenv("HOME")}
	if v := getenv("DBUS_SESSION_BUS_ADDRESS"); v != "" {
		env = append(env, "DBUS_SESSION_BUS_ADDRESS="+v)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CEILING_DIRECTORIES="+dir, "GIT_ASKPASS=", "GCM_INTERACTIVE=never")
}

func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// mediaType is a response's Content-Type without parameters.
func mediaType(resp *http.Response) string {
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return mt
}

// serveHTTPS answers one request over the HTTPS upstream and returns its exit code. Nothing
// has reached the client yet.
func (b *Broker) serveHTTPS(ctx context.Context, req request, in io.Reader, out io.Writer, ev *brokeraudit.Event, cover coverer, say func(string), credentialPolicy func() (bool, string, error)) int {
	h, err := b.newHTTPSLeg(ctx, req.URL, req.Service, ev, say)
	if err != nil {
		ev.Set, ev.Outcome, ev.Reason = "refused", "refused", "remote not usable over https"
		say("the approved remote " + req.URL + " cannot be reached over HTTPS: " + err.Error() + "; nothing ran. " +
			"Fix the remote's URL with `git remote set-url`.")
		return brokercore.ExitUsage
	}
	h.cover = cover
	h.credentialPolicy = credentialPolicy
	// Each request has its own transport; its idle connections end with the request.
	defer h.client.CloseIdleConnections()
	if req.Service == serviceUploadPack {
		ev.Set = "read"
		return h.fetch(in, out)
	}
	return h.push(in, out)
}

// advertisement GETs info/refs for the leg's service. A failure is told to the user and
// returned as the exit code; nothing has reached the client.
func (h *httpsLeg) advertisement() (*http.Response, int) {
	resp, err := h.do(http.MethodGet, "/info/refs?service="+h.service, "", "*/*", nil)
	if err != nil {
		return nil, h.requestFailed(err)
	}
	if code := h.checkResponse(resp, "application/x-"+h.service+"-advertisement"); code != 0 {
		drainClose(resp)
		return nil, code
	}
	return resp, 0
}

// requestFailed tells the user why a request that reached no answer failed.
func (h *httpsLeg) requestFailed(err error) int {
	host := h.remote.Host
	h.ev.Outcome = "failed"
	if errors.Is(err, errCredentialAuthorityChanged) {
		h.ev.Outcome, h.ev.Reason = "refused", "host launch authority changed during the request"
		h.say("the broker's live host authority changed, so this request stopped: " + err.Error() + "; relaunch the jail.")
		return brokercore.ExitNoPerm
	}
	if errors.Is(err, errCredentialApprovalChanged) {
		h.ev.Outcome, h.ev.Reason = "refused", "credential approval changed during the request"
		h.say("the live read approval changed, so the broker stopped this operation before the next request: " + err.Error() + "; relaunch or approve the scope again.")
		return brokercore.ExitUsage
	}
	if errors.Is(err, errNoCredential) {
		h.ev.Outcome, h.ev.Reason = "refused", "the host has no credential for the remote"
		h.say("the host has no credential for " + host + " (the server asked for one, and the host's " +
			"`git credential fill` returned no password), so nothing was transferred. On the host, run " +
			"`git credential fill` with `protocol=https` and `host=" + host + "` to see what its credential " +
			"helper answers, and store a credential there (for example by running this fetch or push once on the " +
			"host). The broker never prompts for a username or password and never starts a login.")
		return brokercore.ExitUnavailable
	}
	h.ev.Reason = "the HTTPS request failed"
	h.say("the HTTPS request to " + host + " failed (" + err.Error() + "), so nothing was transferred. " +
		"Try `git ls-remote " + h.remote.String() + "` on the host.")
	return brokercore.ExitUnavailable
}

// checkResponse refuses a response that is not a success of the wanted type, telling the user
// why, and returns 0 for one that is.
func (h *httpsLeg) checkResponse(resp *http.Response, want string) int {
	host := h.remote.Host
	switch {
	case resp.StatusCode == http.StatusUnauthorized && h.lastCredentialSent && h.cred != nil && h.credentialDenial != "":
		h.ev.Outcome, h.ev.Reason = "refused", "credential approval changed before helper feedback"
		h.say("the server " + host + " refused a host credential that was sent while approved, but the live approval " +
			"changed before its helper could be updated: " + h.credentialDenial)
		return brokercore.ExitUsage
	case resp.StatusCode == http.StatusUnauthorized && h.lastCredentialSent && h.cred != nil:
		h.ev.Outcome, h.ev.Reason = "refused", "the server refused the host's credential"
		if h.credentialRejected {
			h.say("the server " + host + " refused the credential the host's `git credential fill` gave (HTTP 401), " +
				"so nothing was transferred; the broker told the host's credential helper to reject it. Store a working " +
				"credential on the host (run this fetch or push once there), then try again.")
		} else {
			h.say("the server " + host + " refused the credential the host's `git credential fill` gave (HTTP 401), " +
				"so nothing was transferred. The broker could not confirm the host credential helper's rejection; store a " +
				"working credential on the host (run this fetch or push once there), then try again.")
		}
		return brokercore.ExitUnavailable
	case resp.StatusCode == http.StatusUnauthorized && h.credentialDenial != "":
		h.ev.Outcome, h.ev.Reason = "refused", "remote requires credentials but this request is not approved"
		denial := strings.Replace(h.credentialDenial, ", so nothing ran: ", ", so the host credential was not used: ", 1)
		denial = strings.Replace(denial, "so nothing ran and the host's ssh was not started.", "so no host credential was used.", 1)
		h.say("the server " + host + " requires a credential (HTTP 401). The broker tried anonymously and sent no host credential: " +
			denial)
		return brokercore.ExitUsage
	case resp.StatusCode == http.StatusUnauthorized:
		// Only a push POST over pushBufferMax gets here: it streams, so it cannot be replayed
		// with a credential.
		h.ev.Outcome, h.ev.Reason = "refused", "the server asked for a credential only at the push's upload"
		h.say("the server " + host + " asked for a credential only when the push was uploaded (HTTP 401), after " +
			"reading its refs without one. The push is over 1 MiB, so it streams and cannot be sent again: no " +
			"credential was asked of the host and nothing was pushed. Push from the host instead.")
		return brokercore.ExitUnavailable
	case resp.StatusCode/100 == 3:
		h.ev.Outcome, h.ev.Reason = "refused", "the server redirected"
		h.say(fmt.Sprintf("the server %s redirected the request (HTTP %d, to %s); the broker connects only to the "+
			"approved URL, so nothing was transferred. Set the remote to the URL it redirects to with "+
			"`git remote set-url`, and approve it at the config-change gate.", host, resp.StatusCode,
			resp.Header.Get("Location")))
		return brokercore.ExitUsage
	case resp.StatusCode != http.StatusOK:
		h.ev.Outcome, h.ev.Reason = "failed", fmt.Sprintf("the server answered HTTP %d", resp.StatusCode)
		h.say(fmt.Sprintf("the server %s answered HTTP %d, so nothing was transferred. Try `git ls-remote %s` on "+
			"the host.", host, resp.StatusCode, h.remote.String()))
		return brokercore.ExitUnavailable
	case mediaType(resp) != want:
		h.ev.Outcome, h.ev.Reason = "refused", "not a smart-HTTP server"
		h.say("the server " + host + " did not answer as a smart-HTTP git server (its content type was " +
			fmt.Sprintf("%q", resp.Header.Get("Content-Type")) + "), so nothing was transferred. Use its SSH remote.")
		return brokercore.ExitUnavailable
	}
	return 0
}

// skipServiceLine reads the first packet of an advertisement, skipping a `# service=` line and
// its flush, which a v0 server (and some v2 ones) send first.
func skipServiceLine(r *gitproto.Reader) (gitproto.Packet, error) {
	p, err := r.Read()
	if err != nil {
		return p, err
	}
	if p.Kind == gitproto.Data && bytes.HasPrefix(p.Data, []byte("# service=")) {
		if p, err = r.Read(); err != nil {
			return p, err
		}
		if p.Kind != gitproto.Flush {
			return p, fmt.Errorf("%w: no flush after the service line", gitproto.ErrProtocol)
		}
		return r.Read()
	}
	return p, nil
}

// errTooLarge is an advertisement over maxAdvertisement.
var errTooLarge = errors.New("advertisement too large")

// readToFlush reads packets up to and including a flush, the caller's Tee collecting them,
// and refuses past maxAdvertisement bytes, the first packet's already counted in used.
func readToFlush(r *gitproto.Reader, used int) error {
	for {
		p, err := r.Read()
		if err != nil {
			return err
		}
		used += len(p.Data) + 4
		if used > maxAdvertisement {
			return errTooLarge
		}
		if p.Kind == gitproto.Flush {
			return nil
		}
	}
}

// advertisementFailed tells the user why an advertisement could not be relayed.
func (h *httpsLeg) advertisementFailed(what string, err error) int {
	h.ev.Outcome, h.ev.Reason = "failed", "unreadable "+what
	if errors.Is(err, errTooLarge) {
		h.ev.Reason = what + " over the broker's limit"
		h.say(fmt.Sprintf("the server %s's %s is over %d bytes, more than the broker relays, so nothing was "+
			"transferred. Run this on the host, or use the remote's SSH URL.", h.remote.Host, what, maxAdvertisement))
		return brokercore.ExitUnavailable
	}
	h.say("the server " + h.remote.Host + "'s " + what + " could not be read (" + err.Error() +
		"), so nothing was transferred. Try `git ls-remote " + h.remote.String() + "` on the host.")
	return brokercore.ExitUnavailable
}

// fetch is a v2 fetch: the capability advertisement, then one POST per client request.
func (h *httpsLeg) fetch(in io.Reader, out io.Writer) int {
	resp, code := h.advertisement()
	if resp == nil {
		return code
	}
	// The advertisement is buffered whole before any of it reaches the client, so a v0 or
	// broken one is refused before git has seen a byte.
	var adv bytes.Buffer
	ar := gitproto.NewReader(resp.Body)
	first, err := skipServiceLine(ar)
	if err == nil && (first.Kind != gitproto.Data || string(bytes.TrimSuffix(first.Data, []byte("\n"))) != "version 2") {
		drainClose(resp)
		h.ev.Outcome, h.ev.Reason = "refused", "the server does not speak protocol v2"
		h.say("the HTTPS server " + h.remote.Host + " does not speak protocol v2; use its SSH remote " +
			"(`git remote set-url` to its git@ or ssh:// URL, then approve it). Nothing was transferred.")
		return brokercore.ExitUnavailable
	}
	if err == nil {
		_ = gitproto.NewWriter(&adv).Data(first.Data)
		ar.Tee(&adv)
		err = readToFlush(ar, adv.Len())
	}
	drainClose(resp)
	if err != nil {
		return h.advertisementFailed("capability advertisement", err)
	}
	if _, err := out.Write(adv.Bytes()); err != nil {
		h.ev.Outcome, h.ev.Reason = "failed", "the jail stopped reading"
		return brokercore.ExitUnavailable
	}

	// From here git has protocol bytes and ignores the exit code: a failure is said in an ERR
	// packet, which git prints and dies on.
	cr := gitproto.NewReader(in)
	for {
		var body bytes.Buffer
		cr.Tee(&body)
		n, total := 0, 0
		var rerr error
		for {
			p, err := cr.Read()
			if err != nil {
				rerr = err
				break
			}
			total += len(p.Data) + 4
			if total > maxRequest {
				cr.Tee(nil)
				h.ev.Outcome, h.ev.Reason = "refused", "a fetch request over the broker's limit"
				msg := fmt.Sprintf("the git-broker refused a fetch request over %d bytes, so it was not sent "+
					"upstream; fetch this on the host, or use the remote's SSH URL", maxRequest)
				h.say(msg + ".")
				_ = gitproto.NewWriter(out).Line("ERR " + msg + "\n")
				return brokercore.ExitUsage
			}
			if p.Kind == gitproto.Flush {
				break
			}
			n++
		}
		cr.Tee(nil)
		if rerr != nil {
			if errors.Is(rerr, io.EOF) && n == 0 {
				// git closed its side between requests: the fetch is over. Measured with git
				// 2.55 through the connect helper: after a fetch that transferred a pack, git
				// closes with no flush; after ls-refs alone (an up-to-date fetch, ls-remote) it
				// sends an empty request first, handled below.
				h.ev.Outcome = "ran"
				return 0
			}
			h.ev.Outcome, h.ev.Reason = "failed", "the client's request was cut off or unreadable"
			return brokercore.ExitUnavailable
		}
		if n == 0 {
			// An empty request ends the session; git closes its side after it.
			_, _ = io.Copy(io.Discard, in)
			h.ev.Outcome = "ran"
			return 0
		}
		if code := h.post(body.Bytes(), out); code != 0 {
			return code
		}
	}
}

// post sends one v2 request and relays its response to the client.
func (h *httpsLeg) post(body []byte, out io.Writer) int {
	resp, err := h.do(http.MethodPost, "/git-upload-pack", "application/x-git-upload-pack-request",
		"application/x-git-upload-pack-result", body)
	if err != nil {
		return h.errPacket(out, func() int { return h.requestFailed(err) })
	}
	if code := h.checkResponse(resp, "application/x-git-upload-pack-result"); code != 0 {
		drainClose(resp)
		return h.errPacket(out, func() int { return code })
	}
	_, err = io.Copy(out, resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		h.ev.Outcome, h.ev.Reason = "failed", "the response was cut off"
		h.say("the server " + h.remote.Host + "'s response ended early (" + err.Error() + "), so this fetch is " +
			"incomplete; run it again.")
		return brokercore.ExitUnavailable
	}
	return 0
}

// errPacket runs fail, which tells the user on stderr, and then sends git an ERR packet so
// git, which has had protocol bytes and ignores the exit code, fails the fetch.
func (h *httpsLeg) errPacket(out io.Writer, fail func() int) int {
	code := fail()
	_ = gitproto.NewWriter(out).Line("ERR the git-broker's HTTPS upstream failed; its reason is above\n")
	return code
}

// push is a push over HTTPS: the receive-pack advertisement relayed, the commands judged
// (push.go), and either a refusal answered here with nothing POSTed, or the whole push POSTed
// and the report relayed.
func (h *httpsLeg) push(in io.Reader, out io.Writer) int {
	resp, code := h.advertisement()
	if resp == nil {
		h.ev.Set = "refused"
		return code
	}
	var adv bytes.Buffer
	ar := gitproto.NewReader(resp.Body)
	first, err := skipServiceLine(ar)
	if err == nil {
		if first.Kind == gitproto.Data && bytes.HasPrefix(first.Data, []byte("version ")) {
			err = fmt.Errorf("%w: a %q advertisement for a push", gitproto.ErrProtocol, bytes.TrimSpace(first.Data))
		} else if first.Kind == gitproto.Data {
			_ = gitproto.NewWriter(&adv).Data(first.Data)
			ar.Tee(&adv)
			err = readToFlush(ar, adv.Len())
		} else {
			// An empty repository's advertisement can be a flush alone.
			_ = gitproto.NewWriter(&adv).Flush()
		}
	}
	drainClose(resp)
	if err != nil {
		h.ev.Set = "refused"
		return h.advertisementFailed("push advertisement", err)
	}
	if _, err := out.Write(adv.Bytes()); err != nil {
		h.ev.Set, h.ev.Outcome, h.ev.Reason = "refused", "failed", "the jail stopped reading"
		return brokercore.ExitUnavailable
	}

	p, code, ok := h.b.readPush(h.ctx, in, h.ev, h.cover, h.say)
	if !ok {
		return code
	}
	if p.refused {
		return h.b.refuseToClient(out, in, p, h.remote.Host, h.ev, h.say)
	}
	if len(p.cmds) == 0 {
		// A push of nothing: receive-pack would answer nothing, so nothing goes up.
		return 0
	}
	// The judged bytes as read, then the pack to the client's end when there is one. A body of
	// at most pushBufferMax is read whole and sent with a Content-Length, as git's own client
	// does under http.postBuffer: a server or CGI host that cannot take a chunked body takes
	// it, and a 401 on it is replayed with the host's credential like every other request. A
	// larger body streams chunked, and a 401 on it cannot be replayed.
	var full io.Reader = bytes.NewReader(p.teed)
	if gitproto.NeedsPack(p.cmds) {
		full = io.MultiReader(full, in)
	}
	head, err := io.ReadAll(io.LimitReader(full, pushBufferMax+1))
	if err != nil {
		h.ev.Outcome, h.ev.Reason = "failed", "the jail's push ended early"
		h.say("the push from the jail ended early (" + err.Error() + "), so nothing was sent to " + h.remote.Host + ".")
		return brokercore.ExitUnavailable
	}
	var presp *http.Response
	if len(head) <= pushBufferMax {
		presp, err = h.do(http.MethodPost, "/git-receive-pack", "application/x-git-receive-pack-request",
			"application/x-git-receive-pack-result", head)
	} else {
		allowed, denial, authorityErr := h.credentialAllowed()
		if authorityErr != nil {
			err = fmt.Errorf("%w: %v", errCredentialAuthorityChanged, authorityErr)
		} else if !allowed {
			err = fmt.Errorf("%w: %s", errCredentialApprovalChanged, denial)
		} else {
			presp, err = h.send(http.MethodPost, "/git-receive-pack", "application/x-git-receive-pack-request",
				"application/x-git-receive-pack-result", io.MultiReader(bytes.NewReader(head), full), h.cred != nil && allowed)
			h.lastCredentialSent = h.cred != nil && allowed
			if err == nil && h.lastCredentialSent {
				h.settle(presp)
			}
		}
	}
	// A failed POST is answered in git's own report, then the rest of git's stream is read and
	// dropped, as refuseToClient does. The report goes first: a push of deletes only sends no
	// pack and keeps its side open until it has read the report.
	if err != nil {
		code := h.requestFailed(err)
		_ = writeRefusal(out, p.caps, p.cmds, sameReason(len(p.cmds), "the git-broker's HTTPS upstream failed: "+err.Error()))
		_, _ = io.Copy(io.Discard, in)
		return code
	}
	if code := h.checkResponse(presp, "application/x-git-receive-pack-result"); code != 0 {
		drainClose(presp)
		_ = writeRefusal(out, p.caps, p.cmds, sameReason(len(p.cmds), "the git-broker's HTTPS upstream failed; its reason is above"))
		_, _ = io.Copy(io.Discard, in)
		return code
	}
	rep, repErr := relayReport(out, presp.Body, p.caps)
	_ = presp.Body.Close()
	_, _ = io.Copy(io.Discard, in)
	h.b.record(h.ev.Repo, p, h.ev.Refs, rep, repErr, h.say)
	return 0
}

func sameReason(n int, reason string) []string {
	r := make([]string, n)
	for i := range r {
		r[i] = reason
	}
	return r
}
