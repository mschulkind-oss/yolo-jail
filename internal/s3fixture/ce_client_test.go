package s3fixture

import (
	"bytes"
	"strings"
	"testing"
)

func TestCEFixtureRejectsNoncanonicalArgvBeforeEnvironmentOrNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01"},
		{"ce", "GetAnything", "--time-period", "Start=2026-01-01,End=2026-02-01", "--output", "json", "--no-paginate"},
		{"ce", "get-tags", "--time-period", "file://x", "--output", "json", "--no-paginate"},
		{"ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01", "--profile", "x", "--output", "json", "--no-paginate"},
		{"ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01", "--search-string", "https://x", "--output", "json", "--no-paginate"},
		{"ce", "get-tags", "--time-period", "Start=2026-02-30,End=2026-03-01", "--output", "json", "--no-paginate"},
	} {
		var out, errs bytes.Buffer
		rc := RunAWS(args, func(string) string { t.Fatal("invalid argv accessed host environment"); return "" }, nil, &out, &errs)
		if rc != 252 || out.Len() != 0 || errs.Len() == 0 {
			t.Fatalf("args=%q rc=%d out=%s err=%s", args, rc, &out, &errs)
		}
	}
}
func TestCEFixtureLoopbackResponsesAndExplicitPagination(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		args []string
		want string
		rc   int
	}{
		{nil, "{\"Tags\":[\"fixture\"]}\n", 0},
		{[]string{"--search-string", "fixture-pages"}, "{\"Tags\":[\"first-page\"],\"NextPageToken\":\"fixture-next\"}\n", 0},
		{[]string{"--next-page-token", "fixture-next"}, "{\"Tags\":[\"second-page\"]}\n", 0},
		{[]string{"--search-string", "fixture-error"}, "synthetic CE service error; retry a supported query on the host\n", 254},
	} {
		args := append([]string{"ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01"}, tc.args...)
		args = append(args, "--output", "json", "--no-paginate")
		var out, errs bytes.Buffer
		rc := RunAWS(args, func(key string) string {
			if key == "AWS_ENDPOINT_URL" {
				return s.URL()
			}
			return ""
		}, nil, &out, &errs)
		if rc != tc.rc || out.String()+errs.String() != tc.want {
			t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out.String(), errs.String())
		}
	}
	if len(s.CEArgv()) != 4 || len(s.RequestLog()) != 4 || s.Writes() != 0 {
		t.Fatal("CE fake retried or wrote")
	}
}
func TestCEFixtureNeverUsesNonLoopbackOrCredentialFiles(t *testing.T) {
	for _, endpoint := range []string{"https://ce.us-east-1.amazonaws.com", "http://192.0.2.1:1234", "http://localhost:1234", "file://x", "http://user@127.0.0.1:1234", "http://127.0.0.1:1234/path"} {
		var out, errs bytes.Buffer
		rc := RunAWS([]string{"ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01", "--output", "json", "--no-paginate"}, func(key string) string {
			if key == "AWS_ENDPOINT_URL" {
				return endpoint
			}
			if strings.Contains(key, "CREDENTIAL") || key == "AWS_CONFIG_FILE" {
				t.Fatal("CE fixture read credential configuration")
			}
			return ""
		}, nil, &out, &errs)
		if rc != 255 {
			t.Fatalf("endpoint=%q rc=%d", endpoint, rc)
		}
	}
}
