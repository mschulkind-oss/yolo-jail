package awscredadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
)

func get(t *testing.T, fetch Fetch, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler(fetch).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body %q is not JSON: %v", rec.Body.String(), err)
	}
	return body
}

// liveCredential is a complete, servable credential rendered by the HOST half, so
// the shape under test is the one production produces rather than a literal written
// here from the same documentation twice.
func liveCredential() awsauth.Credential {
	return awsauth.Credential{
		AccessKeyID:     "ASIAEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI",
		SessionToken:    "FwoGZXIvYXdzE",
		ExpiresAtMS:     1767225600000,
	}
}

// TestASuccessBodyIsForwardedVERBATIM is the pass-through invariant, and the field
// it really pins is `Token`.
//
// `aws` spells the session token SessionToken and the container-credentials
// protocol spells it Token; an SDK rejects a body missing Token WITHOUT naming the
// field. awsauth.Credential.ContainerCredentials is the one place that rename
// happens, so this test drives the adapter with that function's own output and
// asserts the bytes arrive unchanged. A second rename introduced here — or a
// re-marshal that reordered or retyped anything — fails it.
func TestASuccessBodyIsForwardedVERBATIM(t *testing.T) {
	upstream, err := json.Marshal(liveCredential().ContainerCredentials())
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, func() (Answer, error) {
		return Answer{Body: upstream, OK: true}, nil
	}, http.MethodGet, CredentialsPath)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(upstream) {
		t.Errorf("body = %s, want the host's bytes verbatim %s", rec.Body.String(), upstream)
	}
	body := decodeBody(t, rec)
	if _, ok := body["Token"]; !ok {
		t.Errorf("body has no Token key (%v) — an SDK rejects this without saying why", body)
	}
	if _, ok := body["SessionToken"]; ok {
		t.Errorf("body carries SessionToken (%v); that is `aws`'s spelling, not the "+
			"container-credentials protocol's", body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

// TestALapsedSessionReachesTheAgentWithTheLoginCommand is OQ-SSO6 end to end
// across the two packages, driven through the REAL classifier.
//
// The mint is faked at awsauth's one exec seam — no `aws` binary runs and nothing
// reaches the network — so the Message here is the one a real lapse produces, not a
// literal copied from the source. What it proves: the sentence
// `aws sso login --profile prod` survives the mint, the daemon's ContainerError,
// the framed hop and this handler, and lands in a 4xx body an SDK surfaces.
func TestALapsedSessionReachesTheAgentWithTheLoginCommand(t *testing.T) {
	minter := awsauth.Minter{
		Config: awsauth.Config{Profile: "prod",
			Narrowing: awsauth.Narrowing{Kind: awsauth.NarrowNone}},
		Run: func(context.Context, []string) awsauth.Output {
			return awsauth.Output{Spawned: true, Code: 255,
				Stderr: "Error loading SSO Token: Token for https://example.awsapps.com/start " +
					"does not exist"}
		},
	}
	_, err := minter.Mint(context.Background())
	var mintErr *awsauth.MintError
	if !errors.As(err, &mintErr) {
		t.Fatalf("the fake lapse produced %v, not a *MintError", err)
	}
	refusal, marshalErr := json.Marshal(mintErr.ContainerError())
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}

	rec := get(t, func() (Answer, error) {
		return Answer{Body: refusal, OK: false}, nil
	}, http.MethodGet, CredentialsPath)

	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("status = %d — only a 4xx carries Code/Message to the SDK's error text "+
			"(design §5), so any other status deletes the login command", rec.Code)
	}
	body := decodeBody(t, rec)
	message, _ := body["Message"].(string)
	if !strings.Contains(message, "aws sso login --profile prod") {
		t.Errorf("Message = %q, want the login command verbatim", message)
	}
	if body["Code"] != "ExpiredToken" {
		t.Errorf("Code = %v, want the host's own ExpiredToken", body["Code"])
	}
	if rec.Body.String() != string(refusal) {
		t.Errorf("body = %s, want the host's refusal verbatim %s", rec.Body.String(), refusal)
	}
}

// TestEveryFailureIsA4xxCarryingCodeAndMessage enumerates every way this handler
// can decline and holds all of them to the one rule the protocol imposes: a message
// travels on a 4xx and on nothing else.
//
// It is written as a census rather than four separate assertions because the
// failure mode is a LATER, more semantically correct status — a 502 for an
// unreachable host, a 500 for a malformed body — which is exactly the edit that
// silently deletes the message. Whoever makes it has to change this test and say
// why here.
func TestEveryFailureIsA4xxCarryingCodeAndMessage(t *testing.T) {
	incomplete, err := json.Marshal(map[string]any{
		"AccessKeyId": "ASIA", "SecretAccessKey": "s", "Expiration": "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path string
		fetch              Fetch
	}{
		{"host hop failed", http.MethodGet, CredentialsPath,
			func() (Answer, error) { return Answer{}, errors.New("dial: no such file") }},
		{"host refused", http.MethodGet, CredentialsPath, func() (Answer, error) {
			return Answer{Body: json.RawMessage(`{"Code":"MintFailed","Message":"aws exited 1"}`)}, nil
		}},
		{"incomplete success body", http.MethodGet, CredentialsPath,
			func() (Answer, error) { return Answer{Body: incomplete, OK: true}, nil }},
		{"unknown path", http.MethodGet, "/", func() (Answer, error) {
			t.Error("the host was asked for a credential for a request to an unknown path")
			return Answer{}, nil
		}},
		{"wrong method", http.MethodPost, CredentialsPath, func() (Answer, error) {
			t.Error("the host was asked for a credential for a non-GET request")
			return Answer{}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, tc.fetch, tc.method, tc.path)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("status = %d, want a 4xx — any other status is a bare failure and "+
					"the Message never reaches the human (design §5)", rec.Code)
			}
			body := decodeBody(t, rec)
			if code, _ := body["Code"].(string); code == "" {
				t.Errorf("body %v has no Code", body)
			}
			if message, _ := body["Message"].(string); message == "" {
				t.Errorf("body %v has no Message", body)
			}
		})
	}
}

// TestAnIncompleteSuccessBodyIsNamedRatherThanForwarded: the host already refuses
// to cache an incomplete credential (awsauth.Credential.Complete), so this is a bug
// path — and it is the bug path an SDK reports worst, because it rejects a body
// missing Token without naming the field. The adapter names it instead.
//
// Mutation check: deleting the missingCredentialFields guard makes this a 200 and
// fails here.
func TestAnIncompleteSuccessBodyIsNamedRatherThanForwarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"no token", map[string]any{"AccessKeyId": "A", "SecretAccessKey": "s",
			"Expiration": "2026-01-01T00:00:00Z"}, "Token"},
		{"empty token", map[string]any{"AccessKeyId": "A", "SecretAccessKey": "s",
			"Token": "", "Expiration": "2026-01-01T00:00:00Z"}, "Token"},
		{"no expiration", map[string]any{"AccessKeyId": "A", "SecretAccessKey": "s",
			"Token": "t"}, "Expiration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			rec := get(t, func() (Answer, error) {
				return Answer{Body: raw, OK: true}, nil
			}, http.MethodGet, CredentialsPath)
			if rec.Code == http.StatusOK {
				t.Fatalf("an incomplete body was served as a 200: %s", rec.Body.String())
			}
			message, _ := decodeBody(t, rec)["Message"].(string)
			if !strings.Contains(message, tc.want) {
				t.Errorf("Message = %q, want it to name the missing %s", message, tc.want)
			}
		})
	}
}

// TestACompleteBodyWithExtraKeysIsStillServed: the four keys are what an SDK reads
// and the adapter checks for exactly those. A host that grows a fifth field must
// not be refused by the jail half, or the two halves can never be deployed apart.
func TestACompleteBodyWithExtraKeysIsStillServed(t *testing.T) {
	raw := json.RawMessage(`{"AccessKeyId":"A","SecretAccessKey":"s","Token":"t",` +
		`"Expiration":"2026-01-01T00:00:00Z","AccountId":"111122223333"}`)
	rec := get(t, func() (Answer, error) { return Answer{Body: raw, OK: true}, nil },
		http.MethodGet, CredentialsPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

// TestMainRefusesTrailingArguments keeps the flag contract honest: a supervisor
// composing the manifest's argv wrongly should exit 2 rather than bind a default
// port and look healthy.
func TestMainRefusesTrailingArguments(t *testing.T) {
	if rc := Main([]string{"--listen", "127.0.0.1:0", "extra"}); rc != 2 {
		t.Errorf("Main with a trailing argument = %d, want 2", rc)
	}
}

// TestMainReportsAnUnbindableAddress. A jail daemon's spawn failure is otherwise
// silent (supervisor.superviseOne drops start()'s error), so the exit code is the
// only signal there is.
func TestMainReportsAnUnbindableAddress(t *testing.T) {
	if rc := Main([]string{"--listen", "bad address"}); rc != 1 {
		t.Errorf("Main with an unbindable address = %d, want 1", rc)
	}
}
