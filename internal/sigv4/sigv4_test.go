package sigv4

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The vectors in testdata/v4 are AWS's published SigV4 test suite, copied verbatim
// from awslabs/aws-c-auth (tests/aws-signing-test-suite/v4, header-signing files) on
// 2026-09-25. Each case pins the canonical request, the string-to-sign and the
// signature, so a canonicalization change fails at the step it broke rather than
// only at the final hex.

type vectorContext struct {
	Credentials struct {
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		Token           string `json:"token"`
	} `json:"credentials"`
	Normalize        bool   `json:"normalize"`
	Region           string `json:"region"`
	Service          string `json:"service"`
	SignBody         bool   `json:"sign_body"`
	Timestamp        string `json:"timestamp"`
	OmitSessionToken bool   `json:"omit_session_token"`
}

// parseVectorRequest reads the suite's request.txt: a request line, headers with
// folded continuation lines, a blank line, and the body. It is hand-rolled because
// net/http refuses what the suite deliberately contains (spaces and raw UTF-8 in the
// path, folded headers), and the signer is what is under test, not the parser.
func parseVectorRequest(t *testing.T, raw string) (*http.Request, []byte) {
	t.Helper()
	head, body, _ := strings.Cut(raw, "\n\n")
	lines := strings.Split(head, "\n")
	method, rest, _ := strings.Cut(lines[0], " ")
	target := rest[:strings.LastIndex(rest, " ")]
	path, query, _ := strings.Cut(target, "?")
	decoded, err := url.PathUnescape(path)
	if err != nil {
		decoded = path
	}
	req := &http.Request{Method: method, Header: http.Header{},
		URL: &url.URL{Scheme: "https", Path: decoded, RawQuery: query}}
	var lastName string
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			vs := req.Header[lastName]
			vs[len(vs)-1] += " " + strings.TrimSpace(line)
			continue
		}
		name, value, _ := strings.Cut(line, ":")
		lastName = http.CanonicalHeaderKey(name)
		if lastName == "Host" {
			req.Host = value
			req.URL.Host = value
			continue
		}
		req.Header[lastName] = append(req.Header[lastName], value)
	}
	return req, []byte(body)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAWSSigV4TestSuite runs every header-signing case of AWS's suite.
func TestAWSSigV4TestSuite(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "v4", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no vectors under testdata/v4: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var ctx vectorContext
			if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "context.json"))), &ctx); err != nil {
				t.Fatal(err)
			}
			if !ctx.Normalize {
				t.Fatalf("case is unnormalized; this package signs the normalized form only")
			}
			when, err := time.Parse(time.RFC3339, ctx.Timestamp)
			if err != nil {
				t.Fatal(err)
			}
			req, body := parseVectorRequest(t, readFile(t, filepath.Join(dir, "request.txt")))
			got, err := sign(req, body, Credentials{AccessKeyID: ctx.Credentials.AccessKeyID,
				SecretAccessKey: ctx.Credentials.SecretAccessKey, SessionToken: ctx.Credentials.Token},
				Options{Region: ctx.Region, Service: ctx.Service, Time: when, SinglePathEncoding: true,
					ContentSHA256Header: ctx.SignBody, UnsignedSessionToken: ctx.OmitSessionToken})
			if err != nil {
				t.Fatal(err)
			}
			if want := readFile(t, filepath.Join(dir, "header-canonical-request.txt")); got.canonicalRequest != want {
				t.Errorf("canonical request:\n--- got\n%s\n--- want\n%s", got.canonicalRequest, want)
			}
			if want := readFile(t, filepath.Join(dir, "header-string-to-sign.txt")); got.stringToSign != want {
				t.Errorf("string to sign:\n--- got\n%s\n--- want\n%s", got.stringToSign, want)
			}
			if want := strings.TrimSpace(readFile(t, filepath.Join(dir, "header-signature.txt"))); got.signature != want {
				t.Errorf("signature = %s, want %s", got.signature, want)
			}
			wantAuth := signedRequestHeader(t, filepath.Join(dir, "header-signed-request.txt"), "Authorization")
			if a := req.Header.Get("Authorization"); a != wantAuth {
				t.Errorf("Authorization = %q, want %q", a, wantAuth)
			}
			if ctx.Credentials.Token != "" {
				if got := req.Header.Get("X-Amz-Security-Token"); got != ctx.Credentials.Token {
					t.Errorf("X-Amz-Security-Token not sent with a session credential")
				}
			}
		})
	}
}

func signedRequestHeader(t *testing.T, path, name string) string {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(readFile(t, path)))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.EqualFold(k, name) {
			return v
		}
	}
	t.Fatalf("%s has no %s header", path, name)
	return ""
}

// TestCanonicalURIEncodesTheWirePathAgain pins production mode, which the suite does
// not exercise: every service but S3 URI-encodes the path as sent a second time, so a
// model id's ':' sent as %3A signs as %253A. AWS's rule ("each path segment must be
// URI-encoded twice"), stated in its SigV4 documentation, not a published vector.
func TestCanonicalURIEncodesTheWirePathAgain(t *testing.T) {
	u, err := url.Parse("https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-sonnet%3A0/invoke")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalURI(u, false), "/model/anthropic.claude-sonnet%253A0/invoke"; got != want {
		t.Errorf("canonicalURI = %s, want %s", got, want)
	}
	u2, _ := url.Parse("https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1/chat/completions")
	if got := canonicalURI(u2, false); got != "/openai/v1/chat/completions" {
		t.Errorf("a plain path must sign as itself, got %s", got)
	}
}

func TestSignRefusesARequestThatAlreadyCarriesAuthorization(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/x", nil)
	req.Header.Set("Authorization", "Bearer something")
	err := Sign(req, nil, Credentials{AccessKeyID: "AKID", SecretAccessKey: "s"},
		Options{Region: "us-east-1", Service: BedrockService})
	if err == nil {
		t.Fatal("Sign signed over an existing Authorization header; a bearer and a signature must never travel together")
	}
}

func TestCredentialsNeverFormatTheirSecrets(t *testing.T) {
	c := Credentials{AccessKeyID: "AKIDSECRETPREFIX", SecretAccessKey: "the-secret-key", SessionToken: "the-session-token"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, c)
		for _, secret := range []string{"the-secret-key", "the-session-token", "SECRETPREFIX"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s of Credentials leaks %q: %s", verb, secret, out)
			}
		}
	}
	e := Env{AccessKeyID: "AKID", SecretAccessKey: "the-secret-key", SessionToken: "the-session-token",
		Bearer: "the-bearer", ContainerToken: "the-container-token"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, e)
		for _, secret := range []string{"the-secret-key", "the-session-token", "the-bearer", "the-container-token"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s of Env leaks %q: %s", verb, secret, out)
			}
		}
	}
}
