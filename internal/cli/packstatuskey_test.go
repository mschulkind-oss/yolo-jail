package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestPackStatusKeyPrintsTheFullBuildLine: `yolo pack status <pack>/<name>` is where a launch's
// digest points (OQ-RO9), so it prints the selected fork's build line whole with the digest the
// launch shows. Driven through packMain, so the dispatch is pinned too.
func TestPackStatusKeyPrintsTheFullBuildLine(t *testing.T) {
	newPatchedFixture(t, "")
	var out, errw bytes.Buffer
	if rc := packMain([]string{"status", "forkpack/tool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{"fork forkpack/tool", "recipe " + packload.BuildLineDigest("sh build.sh"), "    sh build.sh\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("`yolo pack status forkpack/tool` lacks %q:\n%s", want, out.String())
		}
	}
}

// TestPackStatusKeyNamesTheKeysThatExist: a key nothing selected builds is refused with the keys
// that are, and a word that is not a key at all is a usage error naming the shape.
func TestPackStatusKeyNamesTheKeysThatExist(t *testing.T) {
	newPatchedFixture(t, "")
	var out, errw bytes.Buffer
	if rc := packMain([]string{"status", "forkpack/nope"}, &out, &errw, false); rc != 1 ||
		!strings.Contains(errw.String(), "the selected ones are forkpack/tool") {
		t.Errorf("an unknown key: rc=%d\n%s", rc, errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"status", "claude"}, &out, &errw, false); rc != 2 ||
		!strings.Contains(errw.String(), "is not a <pack>/<name> key") || out.Len() != 0 {
		t.Errorf("a bare word: rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	errw.Reset()
	if rc := packMain([]string{"status", "a/b", "c"}, &out, &errw, false); rc != 2 ||
		!strings.Contains(errw.String(), `unexpected argument "c"`) {
		t.Errorf("two arguments: rc=%d\n%s", rc, errw.String())
	}
}

// TestPackStatusKeySaysWhenItsPackDidNotResolve: a key whose pack is configured but did not resolve
// here (a store miss, a local path this process cannot read: the ordinary case in a jail) is not
// "nothing selected builds it" — it says the pack did not resolve, why, and what to run next.
func TestPackStatusKeySaysWhenItsPackDidNotResolve(t *testing.T) {
	f := newPatchedFixture(t, "")
	if err := os.Rename(f.forkDir, f.forkDir+".gone"); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	rc := packMain([]string{"status", "forkpack/tool"}, &out, &errw, false)
	if rc != 1 || !strings.Contains(errw.String(), "the pack forkpack is configured but did not resolve here") ||
		!strings.Contains(errw.String(), "then run `yolo pack status forkpack/tool` again") {
		t.Errorf("rc=%d, want 1 and the unresolved pack named with the next step:\n%s", rc, errw.String())
	}
	if strings.Contains(errw.String(), "no selected pack builds") {
		t.Errorf("an unresolved pack was reported as one that builds nothing:\n%s", errw.String())
	}
}

// TestPackStatusKeyInAJailPointsAtTheHost: in a jail, the unresolved pack's next step is the host,
// where the launch disclosed the digest.
func TestPackStatusKeyInAJailPointsAtTheHost(t *testing.T) {
	f := newPatchedFixture(t, "")
	if err := os.Rename(f.forkDir, f.forkDir+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_VERSION", "test")
	var out, errw bytes.Buffer
	rc := packMain([]string{"status", "forkpack/tool"}, &out, &errw, false)
	if rc != 1 || !strings.Contains(errw.String(), "run `yolo pack status forkpack/tool` in a terminal on the host") {
		t.Errorf("rc=%d, want 1 and the host named as the next step:\n%s", rc, errw.String())
	}
}
