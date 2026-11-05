package gitremote

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

func TestForwardAccessRejectsContradictoryProducerPrediction(t *testing.T) {
	work, _, remote, helperEndpoint, shimDir, packets := gitPreflightFixture(t)
	t.Setenv("YOLO_TEST_GIT_HELPER", "1")
	t.Setenv(EndpointEnv, helperEndpoint)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	fake := serve(t, func(s *hostservice.Session) {
		s.StdoutBytes([]byte(`{"schema":"yolo.access.prediction/1","source":"git","source_status":"not-read","scope_source":"live-host","scope_verified":true,"eligibility_status":"not-covered","coverage_status":"covered","call":{"source":"git","canonical":["git-receive-pack"],"digest":"abc"},"classified":[{"ref":"refs/heads/main"}],"verdict":"runs","rule":"remote covered","inputs_digest":"abc","next":[]}`))
		s.Exit(0)
	})
	var stderr bytes.Buffer
	body, code := ForwardAccess("evaluate", []string{"git", "push", remote, "HEAD:refs/heads/main"}, Env{Getenv: func(k string) string {
		if k == EndpointEnv {
			return fake
		}
		return os.Getenv(k)
	}, Stderr: &stderr})
	if code != exitUnavailable || body != nil || !strings.Contains(stderr.String(), "unsupported prediction") {
		t.Fatalf("contradictory host reply accepted: code=%d body=%s stderr=%s", code, body, stderr.String())
	}
	if got := <-packets; string(got) != "0000" {
		t.Fatalf("dry-run updated refs: %q", got)
	}
	<-packets // ls-remote read leg
}
