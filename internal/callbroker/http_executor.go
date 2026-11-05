package callbroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const httpResponseLimit = 32 << 20

// HTTPExecution describes the receiver's reply, not proof that a remote write
// took effect. PossibleTransmission remains true even if the reply is unusable.
type HTTPExecution struct {
	Status               int
	Headers              http.Header
	Body                 []byte
	PossibleTransmission bool
}

type httpExecutor struct {
	resolve func(context.Context, string) ([]net.IPAddr, error)
	// roots is used only by local TLS fixtures; the production caller leaves it nil.
	roots *x509.CertPool
}

type trackedHTTPConn struct {
	net.Conn
	wrote *atomic.Bool
}

func (c trackedHTTPConn) Write(b []byte) (int, error) {
	// A write may have transmitted bytes even if it returns an error.
	c.wrote.Store(true)
	return c.Conn.Write(b)
}

type preparedHTTPRequest struct {
	conn             net.Conn
	request          *http.Request
	credential       string
	credentialHeader string
	wrote            atomic.Bool
	dialed           atomic.Bool
}

func (p *preparedHTTPRequest) close() {
	if p != nil {
		_ = p.conn.Close()
	}
}

func httpURLContainsCredential(call HTTPCall, credential string) bool {
	return credential != "" && (strings.Contains(call.Path, credential) || strings.Contains(call.Query, credential))
}

func (e httpExecutor) run(ctx context.Context, binding loopholedecl.CallBrokerHTTP, call HTTPCall, credential string) (HTTPExecution, error) {
	prepared, err := e.preflight(ctx, binding, call, credential)
	if err != nil {
		return HTTPExecution{}, err
	}
	defer prepared.close()
	return prepared.send()
}

// preflight performs all local validation, DNS pinning, and TLS certificate
// verification without transmitting an HTTP request byte. A host write calls
// this under the store lock before spending its exact grant.
func (e httpExecutor) preflight(ctx context.Context, binding loopholedecl.CallBrokerHTTP, call HTTPCall, credential string) (*preparedHTTPRequest, error) {
	origin, err := url.Parse(binding.Origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || net.ParseIP(origin.Hostname()) != nil || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("selected HTTP origin changed; relaunch with the selected pack")
	}
	prepared, err := prepareHTTPCall(call, binding.RequestHeaders)
	if err != nil || !sameHeaders(prepared.Headers, call.Headers) {
		return nil, errors.New("canonical HTTP request contains unapproved headers or metadata; check the selected pack")
	}
	if credential == "" || strings.ContainsAny(credential, "\x00\r\n") || httpURLContainsCredential(call, credential) {
		return nil, errors.New("HTTP credential is missing or appears in the request URL; repair the selected host credential")
	}
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil || port == "0" {
		return nil, errors.New("selected HTTP origin port is invalid; relaunch with the selected pack")
	}
	resolver := e.resolve
	if resolver == nil {
		resolver = net.DefaultResolver.LookupIPAddr
	}
	addresses, err := resolver(ctx, origin.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("selected HTTP origin cannot resolve; check host DNS and retry")
	}
	// Resolve once. Never ask net/http (or an ambient proxy) to choose another
	// destination, and still verify the original DNS name in the TLS handshake.
	if addresses[0].IP == nil {
		return nil, errors.New("selected HTTP origin resolved without an IP; check host DNS")
	}
	address := net.JoinHostPort(addresses[0].IP.String(), port)
	target := binding.Origin + call.Path
	if call.Query != "" {
		target += "?" + call.Query
	}
	var body io.Reader
	if call.BodyPresent {
		body = bytes.NewReader(call.Body)
	}
	request, err := http.NewRequestWithContext(ctx, call.Method, target, body)
	if err != nil {
		return nil, errors.New("canonical HTTP target invalid; check the selected pack")
	}
	for _, header := range call.Headers {
		for _, value := range header.Values {
			request.Header.Add(header.Name, value)
		}
	}
	request.Header.Set("User-Agent", "")
	request.Header.Set(binding.Credential.Header, binding.Credential.Prefix+credential)
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, errors.New("HTTP request was not transmitted; check host connectivity and retry")
	}
	secured := tls.Client(conn, &tls.Config{ServerName: origin.Hostname(), RootCAs: e.roots, MinVersion: tls.VersionTLS12})
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, errors.New("HTTP request was not transmitted; check the host TLS certificate and retry")
	}
	return &preparedHTTPRequest{conn: secured, request: request, credential: credential, credentialHeader: binding.Credential.Header}, nil
}

func (p *preparedHTTPRequest) send() (HTTPExecution, error) {
	var outcome HTTPExecution
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			if !p.dialed.CompareAndSwap(false, true) {
				return nil, errors.New("HTTP prepared connection cannot be replayed")
			}
			return trackedHTTPConn{Conn: p.conn, wrote: &p.wrote}, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(p.request)
	outcome.PossibleTransmission = p.wrote.Load()
	if err != nil {
		if outcome.PossibleTransmission {
			return outcome, errors.New("HTTP transmission may have reached the host; inspect remote state before a new request")
		}
		return outcome, errors.New("HTTP request was not transmitted; check host TLS and connectivity before retrying")
	}
	defer response.Body.Close()
	outcome.Status = response.StatusCode
	outcome.Headers = make(http.Header)
	for key, values := range response.Header {
		if blockedHTTPHeader(key) || strings.EqualFold(key, p.credentialHeader) {
			continue
		}
		for _, value := range values {
			if strings.EqualFold(key, "Location") && strings.Contains(value, p.credential) {
				return outcome, errors.New("HTTP redirect contains the known credential; inspect host state before retrying")
			}
			outcome.Headers.Add(key, strings.ReplaceAll(value, p.credential, "[redacted]"))
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, httpResponseLimit+1))
	if err != nil || len(data) > httpResponseLimit {
		return outcome, errors.New("HTTP response is unreadable or exceeds 32 MiB; inspect host state before retrying")
	}
	outcome.Body = bytes.ReplaceAll(data, []byte(p.credential), []byte("[redacted]"))
	return outcome, nil
}

func readFencedHTTPCredential(path string, afterParentValidated ...func()) (string, error) {
	const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	fd, err := unix.Open("/", dirFlags, 0)
	if err != nil {
		return "", errors.New("HTTP credential root cannot be opened safely; ask the pack author to use a real host path")
	}
	defer func() { _ = unix.Close(fd) }()
	components := strings.Split(strings.TrimPrefix(path, string(os.PathSeparator)), string(os.PathSeparator))
	for _, component := range components[:len(components)-1] {
		next, openErr := unix.Openat(fd, component, dirFlags, 0)
		if openErr != nil {
			return "", errors.New("HTTP credential parent is missing or symlinked; ask the pack author to select its real path, then run yolo config apply")
		}
		_ = unix.Close(fd)
		fd = next
	}
	if len(afterParentValidated) != 0 && afterParentValidated[0] != nil {
		afterParentValidated[0]()
	}
	leaf, err := unix.Openat(fd, components[len(components)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", errors.New("HTTP credential file cannot be opened safely; repair the selected host file")
	}
	file := os.NewFile(uintptr(leaf), path)
	defer file.Close()
	info, err := file.Stat()
	owner, ok := infoOwner(info)
	if err != nil || !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 0 || info.Size() > 8<<10 || owner != os.Getuid() {
		return "", errors.New("HTTP credential file is not a private host-user file of at most 8 KiB; repair its owner and mode")
	}
	data, err := io.ReadAll(io.LimitReader(file, (8<<10)+1))
	if err != nil || len(data) > 8<<10 {
		return "", errors.New("HTTP credential file exceeds 8 KiB; repair the selected host file")
	}
	return string(data), nil
}

func infoOwner(info os.FileInfo) (int, bool) {
	if info == nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}

// loadHTTPCredential is called only after a live decision. No credential value
// belongs in launch input, pending records, classifier input or audit events.
func loadHTTPCredential(spec loopholedecl.CallBrokerHTTPCredential, approvedPaths []string, afterParentValidated ...func()) (string, error) {
	var value string
	switch spec.Source {
	case "host_env":
		value = os.Getenv(spec.Key)
	case "fenced_file":
		if !filepath.IsAbs(spec.Key) || filepath.Clean(spec.Key) != spec.Key || !slices.Contains(approvedPaths, spec.Key) {
			return "", errors.New("HTTP credential path is outside approved host paths; use yolo config apply")
		}
		var err error
		value, err = readFencedHTTPCredential(spec.Key, afterParentValidated...)
		if err != nil {
			return "", err
		}
	default:
		return "", errors.New("unknown HTTP credential source; relaunch the selected pack")
	}
	if value == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.IndexFunc(spec.Prefix, unicode.IsControl) >= 0 {
		return "", errors.New("HTTP credential is empty or contains controls; repair the host credential")
	}
	return value, nil
}
