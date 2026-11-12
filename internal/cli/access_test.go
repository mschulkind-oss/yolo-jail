package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitremote"
)

func accessFiles(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close(); errOut.Close() })
	return out, errOut
}
func accessEndpoint(key string) string {
	if key == ghbroker.EndpointEnv {
		return "/endpoint"
	}
	return ""
}

func TestAccessAskFilesWithoutMerging(t *testing.T) {
	out, stderr := accessFiles(t)
	var called int
	call := func(op string, argv []string, _ io.Reader) ([]byte, int) {
		called++
		if op != "ask" || strings.Join(argv, " ") != "gh pr merge 40 --squash" {
			t.Fatalf("call %q %q", op, argv)
		}
		return []byte(`{"schema":"yolo.access.prediction/1","verdict":"asks","request_id":"r-test","next":[]}`), 0
	}
	code := runAccessWith([]string{"--json", "--ask", "--", "gh", "pr", "merge", "40", "--squash"}, call, accessEndpoint, strings.NewReader(""), out, stderr)
	if code != 0 || called != 1 {
		t.Fatalf("exit=%d calls=%d", code, called)
	}
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Schema    string `json:"schema"`
		Verdict   string `json:"verdict"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(out).Decode(&result); err != nil || result.Schema != "yolo.access.prediction/1" || result.Verdict != "asks" || result.RequestID != "r-test" {
		t.Fatalf("response=%+v err=%v", result, err)
	}
}

func TestAccessStdinBinding(t *testing.T) {
	for i, body := range []string{"first bytes\x00\xff", "different exact bytes"} {
		out, stderr := accessFiles(t)
		calls := 0
		code := runAccessWith([]string{"--", "gh", "api", "--input", "-"}, func(op string, argv []string, stdin io.Reader) ([]byte, int) {
			calls++
			data, err := io.ReadAll(stdin)
			if err != nil || string(data) != body {
				t.Fatalf("body %q err=%v want %q", data, err, body)
			}
			if op != "evaluate" || strings.Join(argv, " ") != "gh api --input -" {
				t.Fatalf("unexpected call %q %q", op, argv)
			}
			return []byte(`{"schema":"yolo.access.prediction/1","verdict":"asks","next":[]}`), 0
		}, accessEndpoint, strings.NewReader(body), out, stderr)
		if code != 0 || calls != 1 {
			t.Fatalf("case %d exit=%d calls=%d", i, code, calls)
		}
	}
}

func TestAccessGitCommandDoesNotFallThroughToGitHub(t *testing.T) {
	for _, args := range [][]string{{"--", "git", "push", "origin", "HEAD"}, {"--ask", "--", "git", "push", "origin", "HEAD"}, {"git"}} {
		out, stderr := accessFiles(t)
		calledGH := false
		code := runAccessWith(args, func(string, []string, io.Reader) ([]byte, int) {
			calledGH = true
			return nil, 0
		}, func(key string) string {
			if key == "YOLO_HOST_DIR" {
				return "/synthetic-workspace"
			}
			if key == ghbroker.EndpointEnv {
				return "/github-broker"
			}
			if key == gitremote.EndpointEnv {
				return ""
			}
			return ""
		}, strings.NewReader(""), out, stderr)
		if code != ghbroker.ExitUnavailable || calledGH {
			t.Fatalf("git args %v forwarded to GitHub: exit=%d called=%v", args, code, calledGH)
		}
		if _, err := stderr.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(stderr)
		want := "git-broker"
		if err != nil || !strings.Contains(string(body), want) {
			t.Fatalf("git args %v: missing next step: %q %v", args, body, err)
		}
	}
}

func TestAccessNoJailNamesHostReadout(t *testing.T) {
	out, errOut := accessFiles(t)
	called := false
	code := runAccessWith(nil, func(string, []string, io.Reader) ([]byte, int) { called = true; return nil, 0 }, func(string) string { return "" }, strings.NewReader(""), out, errOut)
	if code != ghbroker.ExitUnavailable || called {
		t.Fatalf("exit=%d called=%v", code, called)
	}
	if _, err := errOut.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _ := errOut.Read(buf)
	if !strings.Contains(string(buf[:n]), "yolo approve --json") {
		t.Fatalf("host advice %q", buf[:n])
	}
}

// GW-D80: a scope grant the gate approved as a pattern is live, and one an older build recorded on
// the same spelling is inert. The readout spells the marked one as the gate does (`pattern:`), so
// the two never read alike.
func TestAccessStatusSpellsAPatternGrantApartFromALegacyOne(t *testing.T) {
	out, _ := accessFiles(t)
	renderAccessStatus(out, []byte(`{"schema":"yolo.access/1","source":"github","state":"running","read":"granted","next":"",`+
		`"repositories":["o/*"],"scope_grants":[`+
		`{"kind":"runs","repo":"github.com/o/*","name":"legacy.yml"},`+
		`{"kind":"runs","repo":"github.com/o/*","name":"live.yml","repo_pattern":true}],`+
		`"requests":[],"grants":[],"working_set":[],"limits":{"pending":0,"pending_cap":3}}`))
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"approved runs grant for github.com/o/* legacy.yml",
		"approved runs grant for pattern:github.com/o/* live.yml",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the readout lacks %q:\n%s", want, text)
		}
	}
}
