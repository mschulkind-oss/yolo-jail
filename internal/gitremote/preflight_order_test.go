package gitremote

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPreparePushPreservesGitSelectedRefOrderAndRejectsDuplicate(t *testing.T) {
	work, _, remote, endpoint, shimDir, packets := gitPreflightFixture(t)
	t.Setenv("YOLO_TEST_GIT_HELPER", "1")
	t.Setenv(EndpointEnv, endpoint)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := exec.Command("git", "tag", "v1")
	c.Dir = work
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	got, err := PreparePush(remote, []string{"refs/tags/v1:refs/tags/v1", "HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commands) != 2 || got.Commands[0].Ref != "refs/tags/v1" || got.Commands[1].Ref != "refs/heads/main" {
		t.Fatalf("selected order %+v", got)
	}
	if b := <-packets; string(b) != "0000" {
		t.Fatalf("dry-run sent %q", b)
	}
	<-packets
	_, err = PreparePush(remote, []string{"HEAD:refs/heads/main", "HEAD:refs/heads/main"}, work)
	if err == nil || !strings.Contains(err.Error(), "refspec") && !strings.Contains(err.Error(), "outside the explicit") && !strings.Contains(err.Error(), "dry-run failed") {
		t.Fatalf("duplicate unexpectedly accepted: %v", err)
	}
}
