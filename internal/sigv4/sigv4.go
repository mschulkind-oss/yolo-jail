// Package sigv4 signs HTTP requests with AWS Signature Version 4, on Go's standard
// library alone.
//
// It exists for the wire bridge's Bedrock arm (docs/design/wire-bridge-gateway.md,
// Part 1): the bridge signs its own upstream requests so an SSO-backed credential
// refreshes inside a running jail, which a Bedrock API key minted from the same
// session cannot do. The AWS SDK is deliberately not vendored — it would put an AWS
// module into the hermetic build for one algorithm — so the signer is pinned instead
// by AWS's published test suite (testdata/v4, from awslabs/aws-c-auth), which every
// change to canonicalization must keep green.
//
// Three things live here and nowhere else: the signing algorithm (Sign), the host
// patterns that decide WHETHER a request is signed (BedrockRuntimeRegion,
// AgentCoreGatewayRegion — OQ-WG1 ruled that the decision keys on the upstream host),
// and the credential chain the bridge resolves lazily (Chain, credentials.go).
//
// A credential never reaches a log, an error or a panic from this package:
// Credentials formats as a redaction, and no error string carries a key, a secret or
// a session token.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials is one AWS credential set. SessionToken is empty for a long-lived key
// pair and set for everything aws-auth serves (an assumed-role or SSO session).
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// String redacts: a Credentials value printed with %v, %s or %+v shows the access
// key's first four characters and nothing secret, so a stray log line cannot leak
// a credential.
func (c Credentials) String() string {
	id := c.AccessKeyID
	if len(id) > 4 {
		id = id[:4] + "…"
	}
	return fmt.Sprintf("sigv4.Credentials{AccessKeyID: %s, secret and token redacted}", id)
}

// GoString redacts for %#v too, which does not consult String.
func (c Credentials) GoString() string { return c.String() }

func (c Credentials) valid() bool { return c.AccessKeyID != "" && c.SecretAccessKey != "" }

// Options is one signing's parameters. The zero values are the production ones for
// every AWS service except S3; the test-suite knobs exist because the published
// vectors exercise the generic algorithm, which differs in exactly those three places.
type Options struct {
	Region  string
	Service string
	// Time is the signing instant; zero means now.
	Time time.Time

	// SinglePathEncoding canonicalizes the DECODED path, URI-encoded once and
	// dot-segment normalized, which is the published test suite's mode (and S3's
	// encoding). The default, for every other service, URI-encodes the path as it
	// goes on the wire a second time, which is what an AWS endpoint recomputes from
	// the path it received.
	SinglePathEncoding bool
	// ContentSHA256Header adds and signs x-amz-content-sha256 (the suite's
	// sign_body). Bedrock does not require it; the payload hash is signed either way.
	ContentSHA256Header bool
	// UnsignedSessionToken adds x-amz-security-token AFTER signing, so it is sent but
	// not signed (the suite's omit_session_token). Production signs the token.
	UnsignedSessionToken bool
}

const (
	algorithm  = "AWS4-HMAC-SHA256"
	timeFormat = "20060102T150405Z"
	dateFormat = "20060102"
)

// Sign adds X-Amz-Date, X-Amz-Security-Token (for a session credential) and
// Authorization to req. body is exactly the bytes req will send — its SHA-256 is
// the payload hash — and req's own body is not read. Every header already on req
// is signed; headers net/http's transport adds later (User-Agent, Accept-Encoding,
// Content-Length) are not, which SigV4 allows.
//
// Sign refuses a request that already carries an Authorization header: a bearer and
// a signature must never travel together (docs/design/wire-bridge-gateway.md §2.1).
func Sign(req *http.Request, body []byte, creds Credentials, opts Options) error {
	_, err := sign(req, body, creds, opts)
	return err
}

// signing is one signature's intermediate forms, returned to this package's tests so
// each vector's canonical request and string-to-sign are pinned, not only the final
// signature. It is never exported: the canonical request carries the session token.
type signing struct {
	canonicalRequest string
	stringToSign     string
	signature        string
}

func sign(req *http.Request, body []byte, creds Credentials, opts Options) (signing, error) {
	if !creds.valid() {
		return signing{}, errors.New("sigv4: credential set has no access key id or secret")
	}
	if opts.Region == "" || opts.Service == "" {
		return signing{}, errors.New("sigv4: region and service are required")
	}
	if req.Header.Get("Authorization") != "" {
		return signing{}, errors.New("sigv4: request already carries an Authorization header; " +
			"a signature and a bearer never travel together")
	}
	now := opts.Time
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	amzDate := now.Format(timeFormat)

	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("X-Amz-Date", amzDate)
	payloadHash := hexSHA256(body)
	if opts.ContentSHA256Header {
		req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	}
	if creds.SessionToken != "" && !opts.UnsignedSessionToken {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	canonHeaders, signedHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL, opts.SinglePathEncoding),
		canonicalQuery(req.URL.RawQuery),
		canonHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{now.Format(dateFormat), opts.Region, opts.Service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{algorithm, amzDate, scope, hexSHA256([]byte(canonicalRequest))}, "\n")

	key := hmacSHA256([]byte("AWS4"+creds.SecretAccessKey), now.Format(dateFormat))
	key = hmacSHA256(key, opts.Region)
	key = hmacSHA256(key, opts.Service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, creds.AccessKeyID, scope, signedHeaders, signature))
	if creds.SessionToken != "" && opts.UnsignedSessionToken {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	return signing{canonicalRequest: canonicalRequest, stringToSign: stringToSign, signature: signature}, nil
}

// canonicalHeaders is every header on req plus host, names lower-cased and sorted,
// values trimmed with inner runs of whitespace collapsed to one space, repeated
// headers joined with commas in the order they appear.
func canonicalHeaders(req *http.Request) (canonical, signed string) {
	values := map[string][]string{}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		values[lower] = append(values[lower], vs...)
	}
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	values["host"] = []string{host}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		trimmed := make([]string, len(values[name]))
		for i, v := range values[name] {
			trimmed[i] = strings.Join(strings.Fields(v), " ")
		}
		b.WriteString(name + ":" + strings.Join(trimmed, ",") + "\n")
	}
	return b.String(), strings.Join(names, ";")
}

// canonicalURI is the request path as SigV4 signs it. See Options.SinglePathEncoding
// for the two modes.
func canonicalURI(u *url.URL, single bool) string {
	if single {
		p := u.Path
		if p == "" {
			p = "/"
		}
		return uriEncode(normalizePath(p), false)
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return uriEncode(p, false)
}

// normalizePath removes dot segments and collapses repeated slashes, keeping a
// trailing slash (RFC 3986 §5.2.4, as the suite applies it).
func normalizePath(p string) string {
	trailing := strings.HasSuffix(p, "/")
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	joined := "/" + strings.Join(out, "/")
	if trailing && joined != "/" {
		joined += "/"
	}
	return joined
}

// canonicalQuery re-encodes every key and value strictly and sorts by key, then by
// value. A '+' is a literal plus, never a space, as AWS reads it.
func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	type pair struct{ k, v string }
	var pairs []pair
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		pairs = append(pairs, pair{uriEncode(unescape(k), true), uriEncode(unescape(v), true)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// uriEncode is SigV4's URI encoding: every byte but the RFC 3986 unreserved set is
// percent-encoded in upper-case hex; '/' is kept unless encodeSlash.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
