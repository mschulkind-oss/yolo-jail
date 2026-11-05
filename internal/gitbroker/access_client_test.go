package gitbroker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/gitremote"
)

func TestGitAccessClientRefusesMultipleEffectivePushURLsBeforeHelper(t *testing.T) {
	f := newFixture(t, "tee")
	f.commit("two destinations")
	for _, url := range []string{"yolo::" + f.broker.bound.Repos[0], "yolo::ssh://other.invalid/another.git"} {
		mustGit(t, f.ws, "config", "--add", "remote.origin.pushurl", url)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(f.ws); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	var stderr bytes.Buffer
	_, code := gitremote.ForwardAccess("evaluate", []string{"git", "push", "origin", "HEAD:refs/heads/main"}, gitremote.Env{
		Getenv: func(key string) string {
			if key == gitremote.EndpointEnv {
				return f.endpoint
			}
			return os.Getenv(key)
		}, Stderr: &stderr,
	})
	if code == 0 || !strings.Contains(stderr.String(), "multiple effective push URLs") || f.sshRan() {
		t.Fatalf("multi-destination prediction code=%d stderr=%s ssh=%t", code, stderr.String(), f.sshRan())
	}
}

// TestGitAccessClientThroughPublishedEndpoint drives Git's actual dry-run selection,
// the published host endpoint, the live host verifier, and the jail reply decoder.
func TestGitAccessClientThroughPublishedEndpoint(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("client selected")
	rewrites := packGitRewriteEnv(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(f.ws); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	for _, kv := range rewrites {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GO_HELPER", "1")
	t.Setenv("TEST_BINARY", os.Args[0])
	t.Setenv("PATH", f.helperBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(gitremote.EndpointEnv, f.endpoint)
	var stderr bytes.Buffer
	env := gitremote.Env{Getenv: os.Getenv, Stderr: &stderr}
	argv := []string{"git", "push", "origin", "HEAD:refs/heads/main"}
	prediction, code := gitremote.ForwardAccess("evaluate", argv, env)
	if code != 0 {
		t.Fatalf("real client prediction: %d %s", code, stderr.String())
	}
	var answer struct {
		Schema       string `json:"schema"`
		SourceStatus string `json:"source_status"`
		Verdict      string `json:"verdict"`
		InputsDigest string `json:"inputs_digest"`
		Call         struct{ Digest string }
		Classified   []struct{ Ref string }
		Next         []struct{ Run, Detail string }
	}
	if err := json.Unmarshal(prediction, &answer); err != nil || answer.Schema != "yolo.access.prediction/1" || answer.SourceStatus != "verified" || answer.Verdict != "asks" || answer.InputsDigest == "" || answer.Call.Digest == "" || len(answer.Classified) != 1 || answer.Classified[0].Ref != "refs/heads/main" {
		t.Fatalf("host prediction: %s %v", prediction, err)
	}
	for _, next := range answer.Next {
		if strings.Contains(next.Run, "<") || strings.Contains(next.Run, "confirm ") {
			t.Fatalf("non-runnable next command: %+v", next)
		}
	}
	if got := f.upstreamRef("refs/heads/main"); got != old {
		t.Fatalf("evaluation wrote remote: %s", got)
	}
	filed, code := gitremote.ForwardAccess("ask", argv, env)
	if code != 0 || !bytes.Contains(filed, []byte(`"request_id":"r-`)) {
		t.Fatalf("filing: %d %s %s", code, filed, stderr.String())
	}
	if got := f.upstreamRef("refs/heads/main"); got != old {
		t.Fatalf("filing wrote remote: %s", got)
	}
	if sent, err := os.ReadFile(filepath.Join(f.record, "stdin")); err != nil || string(sent) != "0000" {
		t.Fatalf("host received updates: %q %v", sent, err)
	}
	var filedAnswer struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(filed, &filedAnswer); err != nil || filedAnswer.RequestID == "" {
		t.Fatalf("missing request ID: %s %v", filed, err)
	}
	grant := allow(t, filedAnswer.RequestID)
	covered, code := gitremote.ForwardAccess("evaluate", argv, env)
	if code != 0 || !bytes.Contains(covered, []byte(`"verdict":"runs"`)) || !bytes.Contains(covered, []byte(`"covered_by":"`+grant.ID+`"`)) {
		t.Fatalf("covered client prediction: %d %s %s", code, covered, stderr.String())
	}
	if got := f.upstreamRef("refs/heads/main"); got != old {
		t.Fatalf("covered evaluate wrote remote: %s", got)
	}
}
