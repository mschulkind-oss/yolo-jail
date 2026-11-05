package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestHTTPClassifierAcceptsThreeLookupRoundsButNotFour(t *testing.T) {
	body := []byte(`{"query":"query { item { id } }"}`)
	answer := fixtureHTTPAnswer(true, digestHTTPBody(true, body), "", "original")
	for _, limit := range []int{3, 4} {
		t.Run(fmt.Sprintf("rounds-%d", limit), func(t *testing.T) {
			script := fmt.Sprintf("input=$(cat)\ncount=$(printf '%%s' \"$input\" | grep -o '\"arguments\"' | wc -l)\nif [ \"$count\" -ge %d ]; then cat <<'CLASS'\n%s\nCLASS\nelse next=$((count+1)); printf '{\"version\":\"yolo.call-broker-classifier/1\",\"lookups\":[{\"name\":\"task-project\",\"arguments\":[\"a%%s\"]}]}' \"$next\"; fi", limit, answer)
			classifier := fixtureHTTPClassifier(t, script)
			classifier.Lookups = []loopholedecl.CallBrokerLookup{{Name: "task-project", HTTPMethod: "GET", HTTPPath: "/items/{id}", HTTPResource: "task"}}
			calls := 0
			read := func(_ context.Context, _ loopholedecl.CallBrokerLookup, args []string) (json.RawMessage, error) {
				calls++
				if len(args) != 1 || args[0] != fmt.Sprintf("a%d", calls) {
					t.Fatalf("lookup sequence: calls=%d args=%v", calls, args)
				}
				return json.RawMessage(`{"id":"a1"}`), nil
			}
			_, err := classifier.ClassifyHTTPWithLookups(context.Background(), fixtureHTTPCall(body, true), []string{"Content-Type"}, read)
			if calls != 3 || (err == nil) != (limit == 3) {
				t.Fatalf("round cap: limit=%d calls=%d err=%v", limit, calls, err)
			}
		})
	}
}

func TestHTTPClassifierLookupRoundsRejectCyclesAndUnknownMappings(t *testing.T) {
	body := []byte(`{"query":"query { item { id } }"}`)
	call := fixtureHTTPCall(body, true)
	lookup := `{"version":"yolo.call-broker-classifier/1","lookups":[{"name":"task-project","arguments":["a1"]}]}`
	classifier := fixtureHTTPClassifier(t, "cat >/dev/null\ncat <<'ANSWER'\n"+lookup+"\nANSWER")
	classifier.Lookups = []loopholedecl.CallBrokerLookup{{Name: "task-project", HTTPMethod: "GET", HTTPPath: "/items/{id}", HTTPResource: "task", HTTPIDPointer: "/id", HTTPParentPointer: "/project/id"}}
	calls := 0
	read := func(_ context.Context, _ loopholedecl.CallBrokerLookup, arguments []string) (json.RawMessage, error) {
		calls++
		if len(arguments) != 1 || arguments[0] != "a1" {
			t.Fatalf("lookup arguments changed: %v", arguments)
		}
		return json.RawMessage(`{"id":"a1","project":{"id":"team:alpha"}}`), nil
	}
	if _, err := classifier.ClassifyHTTPWithLookups(context.Background(), call, []string{"Content-Type"}, read); err == nil || !strings.Contains(err.Error(), "cycle") || calls != 1 {
		t.Fatalf("repeated lookup was not bounded: err=%v calls=%d", err, calls)
	}
	calls = 0
	if _, err := classifier.ClassifyHTTPWithLookups(context.Background(), call, []string{"Content-Type"}, nil); err == nil || calls != 0 {
		t.Fatalf("lookup without core reader was allowed: err=%v calls=%d", err, calls)
	}
	unknown := strings.Replace(lookup, `"task-project"`, `"undeclared"`, 1)
	classifier = fixtureHTTPClassifier(t, "cat >/dev/null\ncat <<'ANSWER'\n"+unknown+"\nANSWER")
	classifier.Lookups = []loopholedecl.CallBrokerLookup{{Name: "task-project", HTTPMethod: "GET", HTTPPath: "/items/{id}", HTTPResource: "task"}}
	if _, err := classifier.ClassifyHTTPWithLookups(context.Background(), call, []string{"Content-Type"}, read); err == nil || calls != 0 {
		t.Fatalf("undeclared lookup reached callback: err=%v calls=%d", err, calls)
	}
}

func TestHTTPReadMethodRequiresBothDeclaredOriginalAndCanonicalPOST(t *testing.T) {
	body := []byte(`{"query":"query { item { id } }"}`)
	answer := fixtureHTTPAnswer(true, digestHTTPBody(true, body), "", "original")
	cases := []struct {
		name, originalMethod, originalPath, canonicalMethod, canonicalPath string
		declared                                                           bool
		allowed                                                            bool
	}{
		{"get", "GET", "/items/a1", "GET", "/items/a1", false, true},
		{"declared post", "POST", "/graphql", "POST", "/graphql", true, true},
		{"undeclared post", "POST", "/graphql", "POST", "/graphql", false, false},
		{"original outside", "POST", "/other", "POST", "/graphql", true, false},
		{"canonical outside", "POST", "/graphql", "POST", "/other", true, false},
		{"put", "PUT", "/graphql", "POST", "/graphql", true, false},
		{"delete", "DELETE", "/graphql", "POST", "/graphql", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canonical := strings.Replace(answer, `"method":"POST","path":"/graphql"`, fmt.Sprintf(`"method":%q,"path":%q`, tc.canonicalMethod, tc.canonicalPath), 1)
			classifier := fixtureHTTPClassifier(t, "cat >/dev/null\ncat <<'ANSWER'\n"+canonical+"\nANSWER")
			if !tc.declared {
				classifier.ReadOverPost = nil
			}
			original := fixtureHTTPCall(body, true)
			original.Method, original.Path = tc.originalMethod, tc.originalPath
			_, err := classifier.ClassifyHTTP(context.Background(), original, []string{"Content-Type"})
			if (err == nil) != tc.allowed {
				t.Fatalf("original %s %s, canonical %s %s, declared %v: err=%v, want allowed=%v", tc.originalMethod, tc.originalPath, tc.canonicalMethod, tc.canonicalPath, tc.declared, err, tc.allowed)
			}
		})
	}
}

func fixtureHTTPCall(body []byte, present bool) HTTPCall {
	return HTTPCall{Method: "POST", Path: "/graphql", Query: "op=rename",
		Headers:     []HTTPHeader{{Name: "content-type", Values: []string{"application/json"}}},
		BodyPresent: present, Body: body}
}

func fixtureHTTPAnswer(present bool, bodySHA, body64 string, mode string) string {
	body := fmt.Sprintf(`{"mode":%q,"present":%t`, mode, present)
	if mode == "original" {
		body += fmt.Sprintf(`,"sha256":%q`, bodySHA)
	} else if mode == "inline_base64" {
		body += fmt.Sprintf(`,"base64":%q`, body64)
	}
	body += `}`
	return fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"op=rename","headers":[{"name":"Content-Type","values":["application/json"]}],"body":%s},"reads":[[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]]}}`, body)
}

func fixtureHTTPClassifier(t *testing.T, script string) Classifier {
	t.Helper()
	path := filepath.Join(t.TempDir(), "classifier")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return Classifier{Source: "local-tasks", Argv: []string{"{program:classifier}"},
		Identities: map[string]loopholedecl.CallBrokerProgramIdentity{"classifier": {Bin: "classifier", Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}},
		Roots:      []string{"team:alpha"}, Resources: []loopholedecl.CallBrokerResource{{Type: "project"}, {Type: "task", Parent: "project"}},
		Cells: []loopholedecl.CallBrokerCell{{Name: "update", Tables: []string{"all"}}}, ReadOverPost: []string{"/graphql"}}
}

func scriptAnswer(t *testing.T, answer string, inputPath string) string {
	t.Helper()
	return fmt.Sprintf("cat > %q\ncat <<'YOLO_HTTP_ANSWER'\n%s\nYOLO_HTTP_ANSWER", inputPath, answer)
}

func TestHTTPClassifierBodyBudgetsAndCanonicalDigest(t *testing.T) {
	body := bytesOf('x', httpBodyLimit)
	bodySHA := digestHTTPBody(true, body)
	inputFile := filepath.Join(t.TempDir(), "classifier-input")
	answer := fixtureHTTPAnswer(true, bodySHA, "", "original")
	script := "test -z \"$HTTP_CLASSIFIER_MUST_NOT_INHERIT\" || exit 15\n" + scriptAnswer(t, answer, inputFile) + "\ntest \"$(wc -c < " + inputFile + ")\" -gt 4194304"
	classifier := fixtureHTTPClassifier(t, script)
	t.Setenv("HTTP_CLASSIFIER_MUST_NOT_INHERIT", "credential-value")
	result, err := classifier.ClassifyHTTP(context.Background(), fixtureHTTPCall(body, true), []string{"Content-Type", "content-type"})
	if err != nil {
		t.Fatalf("classifying 3 MiB body: %v", err)
	}
	if !result.Canonical.BodyPresent || len(result.Canonical.Body) != httpBodyLimit || result.CanonicalSHA256 == result.OriginalIntentSHA256 {
		t.Fatalf("result did not preserve original bytes or distinct canonical digest: body=%d original=%s canonical=%s", len(result.Canonical.Body), result.OriginalIntentSHA256, result.CanonicalSHA256)
	}
	wire, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) <= classifierLimit || len(wire) > httpClassifierInputLimit {
		t.Fatalf("HTTP classifier JSON input length=%d", len(wire))
	}
	var got httpClassifierInput
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("classifier stdin was not one JSON value: %v", err)
	}
	if got.Front != "http" || len(got.Request.BodyBase64) != base64.StdEncoding.EncodedLen(httpBodyLimit) || !got.Request.BodyPresent {
		t.Fatalf("classifier input lacks HTTP body bytes: front=%q body=%d present=%v", got.Front, len(got.Request.BodyBase64), got.Request.BodyPresent)
	}
	if strings.Contains(string(wire), "credential-value") {
		t.Fatal("classifier input exposed caller environment")
	}
}

func TestHTTPClassifierRejectsBodyOverThreeMiBBeforeStartingClassifier(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	classifier := fixtureHTTPClassifier(t, fmt.Sprintf("touch %q\nprintf '%%s' '{}'", marker))
	_, err := classifier.ClassifyHTTP(context.Background(), fixtureHTTPCall(bytesOf('x', httpBodyLimit+1), true), nil)
	if err == nil || !strings.Contains(err.Error(), "3 MiB") {
		t.Fatalf("oversized HTTP body accepted: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("oversized HTTP body started classifier: %v", err)
	}
}

func TestHTTPClassifierFiltersUnapprovedAndAuthorityHeaders(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "input")
	body := []byte("payload")
	answer := fixtureHTTPAnswer(true, digestHTTPBody(true, body), "", "original")
	classifier := fixtureHTTPClassifier(t, scriptAnswer(t, answer, inputPath))
	call := fixtureHTTPCall(body, true)
	call.Headers = []HTTPHeader{
		{Name: "Authorization", Values: []string{"client-secret"}}, {Name: "Host", Values: []string{"attacker.invalid"}},
		{Name: "Cookie", Values: []string{"session=secret"}}, {Name: "Set-Cookie", Values: []string{"session=secret2"}},
		{Name: "Yolo-Request", Values: []string{"forged"}}, {Name: "Proxy-Authorization", Values: []string{"proxy-secret"}},
		{Name: "Connection", Values: []string{"close"}}, {Name: "X-Other", Values: []string{"ignored"}},
		{Name: "content-type", Values: []string{"application/json"}},
	}
	_, err := classifier.ClassifyHTTP(context.Background(), call, []string{"Content-Type", "content-type"})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"client-secret", "attacker.invalid", "session=secret", "session=secret2", "forged", "proxy-secret", "ignored"} {
		if strings.Contains(string(wire), forbidden) {
			t.Errorf("classifier received filtered header value %q", forbidden)
		}
	}
	var got httpClassifierInput
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Request.Headers) != 1 || got.Request.Headers[0].Name != "Content-Type" || len(got.Request.Headers[0].Values) != 1 {
		t.Fatalf("classifier headers=%+v, want only approved Content-Type", got.Request.Headers)
	}
}

func TestHTTPClassifierValidatesOriginalBodyReferenceAndPresence(t *testing.T) {
	body := []byte("old")
	for _, tc := range []struct {
		name     string
		call     HTTPCall
		answer   string
		accepted bool
	}{
		{"exact original", fixtureHTTPCall(body, true), fixtureHTTPAnswer(true, digestHTTPBody(true, body), "", "original"), true},
		{"wrong digest", fixtureHTTPCall(body, true), fixtureHTTPAnswer(true, strings.Repeat("0", 64), "", "original"), false},
		{"wrong presence", fixtureHTTPCall(body, true), fixtureHTTPAnswer(false, digestHTTPBody(true, body), "", "original"), false},
		{"absent differs from empty", fixtureHTTPCall(nil, false), fixtureHTTPAnswer(true, digestHTTPBody(true, nil), "", "original"), false},
		{"empty present", fixtureHTTPCall([]byte{}, true), fixtureHTTPAnswer(true, digestHTTPBody(true, nil), "", "original"), true},
		{"unexpected reference inline bytes", fixtureHTTPCall(body, true), strings.Replace(fixtureHTTPAnswer(true, digestHTTPBody(true, body), "", "original"), `"sha256"`, `"base64":"eA==","sha256"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classifier := fixtureHTTPClassifier(t, scriptAnswer(t, tc.answer, filepath.Join(t.TempDir(), "input")))
			_, err := classifier.ClassifyHTTP(context.Background(), tc.call, []string{"Content-Type"})
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v err=%v", tc.accepted, err)
			}
		})
	}
	if digestHTTPBody(false, nil) == digestHTTPBody(true, nil) {
		t.Fatal("absent and empty body digest collided")
	}
}

func TestHTTPClassifierAcceptsCompleteInlineRewriteAndRejectsMalformed(t *testing.T) {
	original := fixtureHTTPCall([]byte("old"), true)
	good := fixtureHTTPAnswer(true, "", base64.StdEncoding.EncodeToString([]byte("new")), "inline_base64")
	classifier := fixtureHTTPClassifier(t, scriptAnswer(t, good, filepath.Join(t.TempDir(), "input")))
	result, err := classifier.ClassifyHTTP(context.Background(), original, []string{"Content-Type"})
	if err != nil || string(result.Canonical.Body) != "new" || !result.Canonical.BodyPresent {
		t.Fatalf("rewrite=%q present=%v err=%v", result.Canonical.Body, result.Canonical.BodyPresent, err)
	}
	for _, answer := range []string{
		fixtureHTTPAnswer(true, "", "bmV3!", "inline_base64"),
		strings.Replace(good, `"base64":"bmV3"`, `"base64":"bmV3","sha256":"`+strings.Repeat("0", 64)+`"`, 1),
		strings.Replace(good, `"mode":"inline_base64"`, `"mode":"original"`, 1),
	} {
		bad := fixtureHTTPClassifier(t, scriptAnswer(t, answer, filepath.Join(t.TempDir(), "input")))
		if _, err := bad.ClassifyHTTP(context.Background(), original, []string{"Content-Type"}); err == nil {
			t.Fatalf("invalid rewrite accepted: %s", answer)
		}
	}
}

func TestHTTPClassifierStrictAnswerRejectsDuplicateUnknownAndTrailingFields(t *testing.T) {
	cases := []string{
		`{"version":"yolo.call-broker-classifier/1","version":"yolo.call-broker-classifier/1","local":true}`,
		`{"version":"yolo.call-broker-classifier/1","unknown":true,"local":true}`,
		`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","Method":"PUT"}}}`,
		`{"version":"yolo.call-broker-classifier/1","local":true} {}`,
	}
	for _, answer := range cases {
		classifier := fixtureHTTPClassifier(t, scriptAnswer(t, answer, filepath.Join(t.TempDir(), "input")))
		if _, err := classifier.ClassifyHTTP(context.Background(), fixtureHTTPCall(nil, false), nil); err == nil {
			t.Errorf("strict HTTP answer accepted: %s", answer)
		}
	}
}

func TestHTTPClassifierRejectsMetadataBudgetsBeforeStartingClassifier(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	classifier := fixtureHTTPClassifier(t, fmt.Sprintf("touch %q\nprintf '%%s' '{}'", marker))
	cases := []struct {
		name string
		edit func(*HTTPCall)
	}{
		{"raw path and query", func(c *HTTPCall) { c.Path = "/" + strings.Repeat("p", httpRawAggregateLimit) }},
		{"header values", func(c *HTTPCall) {
			c.Headers = []HTTPHeader{{Name: "Content-Type", Values: []string{strings.Repeat("v", httpRawAggregateLimit+1)}}}
		}},
		{"header count", func(c *HTTPCall) {
			c.Headers = make([]HTTPHeader, httpHeaderCountLimit+1)
			for i := range c.Headers {
				c.Headers[i] = HTTPHeader{Name: fmt.Sprintf("X-%d", i), Values: []string{"v"}}
			}
		}},
		{"repeated header values", func(c *HTTPCall) {
			c.Headers = []HTTPHeader{{Name: "Content-Type", Values: make([]string, httpHeaderCountLimit+1)}}
		}},
		{"header name", func(c *HTTPCall) {
			c.Headers = []HTTPHeader{{Name: "X-" + strings.Repeat("a", httpHeaderNameLimit), Values: []string{"v"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := fixtureHTTPCall(nil, false)
			tc.edit(&call)
			_, err := classifier.ClassifyHTTP(context.Background(), call, []string{"Content-Type"})
			if err == nil {
				t.Fatal("over-budget HTTP metadata accepted")
			}
		})
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("metadata refusal started classifier: %v", err)
	}
}

func TestHTTPClassifierAnswerRemainsBoundedAndDeadlineIsUnchanged(t *testing.T) {
	oversized := fixtureHTTPClassifier(t, "head -c 65537 /dev/zero | tr '\\000' x")
	if _, err := oversized.ClassifyHTTP(context.Background(), fixtureHTTPCall(nil, false), nil); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized HTTP answer accepted: %v", err)
	}
	slow := fixtureHTTPClassifier(t, "sleep 3\nprintf '%s' '{\"version\":\"yolo.call-broker-classifier/1\",\"local\":true}'")
	start := time.Now()
	if _, err := slow.ClassifyHTTP(context.Background(), fixtureHTTPCall(nil, false), nil); err == nil || time.Since(start) > 2700*time.Millisecond {
		t.Fatalf("HTTP classifier deadline changed: elapsed=%s err=%v", time.Since(start), err)
	}
}

func TestHTTPDigestLengthDelimitsFieldsAndPreservesWriteOrdering(t *testing.T) {
	base := fixtureHTTPCall([]byte("body"), true)
	a := digestHTTPCall(base, nil, []Write{{Cell: "a", Chain: []Resource{{Type: "task", ID: "a1"}}}})
	b := digestHTTPCall(base, nil, []Write{{Cell: "a1", Chain: []Resource{{Type: "task", ID: "a"}}}})
	c := digestHTTPCall(base, nil, []Write{{Cell: "a", Chain: []Resource{{Type: "task", ID: "a1"}}}, {Cell: "b", Chain: []Resource{{Type: "task", ID: "b1"}}}})
	if a == b || a == c {
		t.Fatal("length-delimited digest collided across field boundaries or ordered writes")
	}
}

func bytesOf(value byte, count int) []byte { return bytes.Repeat([]byte{value}, count) }
