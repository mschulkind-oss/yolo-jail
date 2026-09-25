package sigv4

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// The credential order is the AWS SDK's own (docs/design/wire-bridge-gateway.md §2):
//
//  1. a static AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY pair (AWS_SESSION_TOKEN
//     with it when set);
//  2. the container-credentials endpoint AWS_CONTAINER_CREDENTIALS_FULL_URI names —
//     aws-auth's pointer — cached until five minutes before its Expiration;
//  3. AWS_BEARER_TOKEN_BEDROCK, sent UNSIGNED as a bearer.
//
// Exactly one is used per request, so a signature and a bearer never travel together.

// Env is the credential variables a Chain reads, snapshotted once by the caller (the
// wire bridge reads them at boot from its key channel, like every other credential it
// holds). Values are secrets; Env formats as a redaction.
type Env struct {
	AccessKeyID        string // AWS_ACCESS_KEY_ID
	SecretAccessKey    string // AWS_SECRET_ACCESS_KEY
	SessionToken       string // AWS_SESSION_TOKEN
	ContainerURI       string // AWS_CONTAINER_CREDENTIALS_FULL_URI
	ContainerToken     string // AWS_CONTAINER_AUTHORIZATION_TOKEN
	ContainerTokenFile string // AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE, read at each fetch as the SDK does
	Bearer             string // AWS_BEARER_TOKEN_BEDROCK
}

// EnvVars is every variable EnvFrom reads, in the order the chain consults them.
var EnvVars = []string{
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
	"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN",
	"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE", "AWS_BEARER_TOKEN_BEDROCK",
}

// EnvFrom snapshots the credential variables through getenv.
func EnvFrom(getenv func(string) string) Env {
	return Env{
		AccessKeyID:        getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey:    getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:       getenv("AWS_SESSION_TOKEN"),
		ContainerURI:       getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI"),
		ContainerToken:     getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"),
		ContainerTokenFile: getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"),
		Bearer:             getenv("AWS_BEARER_TOKEN_BEDROCK"),
	}
}

// String names which sources are present and never a value.
func (e Env) String() string {
	var have []string
	if e.AccessKeyID != "" && e.SecretAccessKey != "" {
		have = append(have, "static key pair")
	}
	if e.ContainerURI != "" {
		have = append(have, "container endpoint "+e.ContainerURI)
	}
	if e.Bearer != "" {
		have = append(have, "AWS_BEARER_TOKEN_BEDROCK")
	}
	if len(have) == 0 {
		return "sigv4.Env{no credential source}"
	}
	return "sigv4.Env{" + strings.Join(have, ", ") + "}"
}

// GoString redacts for %#v too.
func (e Env) GoString() string { return e.String() }

// Resolved is the credential a request uses: a set to sign with, or a bearer to send
// unsigned — never both. Source names where it came from and is safe to log.
type Resolved struct {
	Creds  Credentials
	Bearer string
	Source string
}

// ErrNoCredential: none of the three sources is configured.
var ErrNoCredential = errors.New("no AWS credential for the Bedrock upstream: tried a static " +
	"AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY pair, the aws-auth pointer " +
	"AWS_CONTAINER_CREDENTIALS_FULL_URI, and AWS_BEARER_TOKEN_BEDROCK, and none is set")

// EndpointError is the container-credentials endpoint answering, but not with a
// credential: aws-auth's own refusal (a lapsed SSO session names `aws sso login
// --profile …` in Message), or a body that is not a usable credential set.
type EndpointError struct {
	Status  int
	Code    string
	Message string
}

func (e *EndpointError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("aws-auth credential endpoint answered %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("aws-auth credential endpoint answered %d %s: %s", e.Status, e.Code, e.Message)
}

// UnreachableError is the container-credentials endpoint not answering at all.
type UnreachableError struct{ Err error }

func (e *UnreachableError) Error() string {
	return "aws-auth credential endpoint is unreachable (" + e.Err.Error() + ")"
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// ContainerFetchTimeout bounds one fetch; a request makes at most one
// (docs/design/wire-bridge-gateway.md §2.1).
const ContainerFetchTimeout = 5 * time.Second

// RefreshBefore is how long before a served set's Expiration the chain fetches a new
// one, the SDK's own margin.
const RefreshBefore = 5 * time.Minute

// staleGrace is how close to Expiration a cached set may still be used when its
// refresh fails: inside the refresh window a set that has not expired still signs,
// and a set this close to expiry, or past it, is never served.
const staleGrace = 30 * time.Second

// Chain resolves the credential for each request. It is safe for concurrent use:
// container fetches are single-flight (one in flight; every waiter takes its result
// or the fresh cache it left), and the cached set is replaced whole.
type Chain struct {
	Env Env
	// Client fetches from the container endpoint; nil means one with
	// ContainerFetchTimeout.
	Client *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	cached  Credentials
	expires time.Time
	have    bool
}

// Resolve returns the credential this request uses, fetching from the container
// endpoint only when the cache is empty or inside its refresh window.
func (c *Chain) Resolve(ctx context.Context) (Resolved, error) {
	e := c.Env
	if e.AccessKeyID != "" && e.SecretAccessKey != "" {
		return Resolved{Creds: Credentials{AccessKeyID: e.AccessKeyID,
			SecretAccessKey: e.SecretAccessKey, SessionToken: e.SessionToken},
			Source: "AWS_ACCESS_KEY_ID"}, nil
	}
	if e.ContainerURI != "" {
		creds, err := c.container(ctx)
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{Creds: creds, Source: "aws-auth (" + e.ContainerURI + ")"}, nil
	}
	if e.Bearer != "" {
		return Resolved{Bearer: e.Bearer, Source: "AWS_BEARER_TOKEN_BEDROCK"}, nil
	}
	return Resolved{}, ErrNoCredential
}

// Invalidate drops the cached set, so the next Resolve fetches: the caller's answer to
// AWS rejecting a signature as expired.
func (c *Chain) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.have = false
	c.cached = Credentials{}
	c.expires = time.Time{}
}

func (c *Chain) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Chain) container(ctx context.Context) (Credentials, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.have && now.Before(c.expires.Add(-RefreshBefore)) {
		return c.cached, nil
	}
	creds, expires, err := c.fetch(ctx, now)
	if err != nil {
		if c.have && now.Before(c.expires.Add(-staleGrace)) {
			return c.cached, nil
		}
		c.have = false
		c.cached = Credentials{}
		return Credentials{}, err
	}
	c.cached, c.expires, c.have = creds, expires, true
	return creds, nil
}

func (c *Chain) fetch(ctx context.Context, now time.Time) (Credentials, time.Time, error) {
	u, err := url.Parse(c.Env.ContainerURI)
	if err != nil || !allowedContainerURI(u) {
		return Credentials{}, time.Time{}, &EndpointError{Message: "AWS_CONTAINER_CREDENTIALS_FULL_URI " +
			"must be an https URL or an http URL on this machine's loopback; refusing to send a " +
			"credential request to " + redactURL(c.Env.ContainerURI)}
	}
	ctx, cancel := context.WithTimeout(ctx, ContainerFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Credentials{}, time.Time{}, &UnreachableError{Err: err}
	}
	token := c.Env.ContainerToken
	if c.Env.ContainerTokenFile != "" {
		data, err := os.ReadFile(c.Env.ContainerTokenFile)
		if err != nil {
			return Credentials{}, time.Time{}, &EndpointError{Message: "cannot read " +
				"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE " + c.Env.ContainerTokenFile + ": " + err.Error()}
		}
		token = strings.TrimSpace(string(data))
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: ContainerFetchTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Credentials{}, time.Time{}, &UnreachableError{Err: stripURLError(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Credentials{}, time.Time{}, &UnreachableError{Err: err}
	}
	var doc struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
		Expiration      string `json:"Expiration"`
		Code            string `json:"Code"`
		Message         string `json:"Message"`
	}
	decodeErr := json.Unmarshal(body, &doc)
	if resp.StatusCode != http.StatusOK {
		msg := doc.Message
		if decodeErr != nil || msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return Credentials{}, time.Time{}, &EndpointError{Status: resp.StatusCode, Code: doc.Code, Message: msg}
	}
	if decodeErr != nil {
		return Credentials{}, time.Time{}, &EndpointError{Status: resp.StatusCode,
			Message: "the body is not a JSON credential set"}
	}
	var missing []string
	for name, v := range map[string]string{"AccessKeyId": doc.AccessKeyID,
		"SecretAccessKey": doc.SecretAccessKey, "Expiration": doc.Expiration} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Credentials{}, time.Time{}, &EndpointError{Status: resp.StatusCode,
			Message: "the credential set has no " + strings.Join(sorted(missing), ", ")}
	}
	expires, err := time.Parse(time.RFC3339, doc.Expiration)
	if err != nil {
		return Credentials{}, time.Time{}, &EndpointError{Status: resp.StatusCode,
			Message: "the credential set's Expiration is not an RFC 3339 time"}
	}
	if !now.Before(expires.Add(-staleGrace)) {
		return Credentials{}, time.Time{}, &EndpointError{Status: resp.StatusCode,
			Message: "the credential set it served is already expired (Expiration " + doc.Expiration + ")"}
	}
	return Credentials{AccessKeyID: doc.AccessKeyID, SecretAccessKey: doc.SecretAccessKey,
		SessionToken: doc.Token}, expires, nil
}

// allowedContainerURI is the AWS SDK's rule for a full URI: https anywhere, http only
// to a loopback host, so the authorization token and the answer never cross a network.
func allowedContainerURI(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return false
}

// stripURLError keeps a transport error's cause without the url.Error wrapper's
// method and URL prefix: the URL is already in the message the caller composes.
func stripURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "an unparseable URL"
	}
	u.User = nil
	u.RawQuery = ""
	return u.String()
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
